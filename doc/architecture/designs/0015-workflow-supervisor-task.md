# Workflow Supervisor Task

## Overview

The workflow supervisor task is responsible for detecting long-running or stalled Temporal workflows and performing compensating cleanup in VCP when workflows do not complete successfully. It runs as part of the background job system and evaluates jobs that are still in `NEW` state after orchestration started. When a workflow times out or is not found after a grace period, the supervisor triggers resource-specific handlers to clean up partially provisioned artifacts in the control plane, ensuring consistency and preventing leaked resources.

## Components

- **Supervisor Task Runner (`core/tasks/workflow_supervisor_task.go`)**
  - Runs two sequential scans per sweep: `scanNewStateJobs` for `NEW` state jobs and `scanProcessingStateTimeouts` for `PROCESSING` state jobs whose workflows have timed out.
  - Describes the Temporal workflow execution for each job to determine the current status.
  - Applies a grace period when the workflow execution is not found (to handle eventual consistency right after job creation).
  - Emits cleanup events to registered handlers when workflows time out or remain missing beyond the grace window.
  - For `PROCESSING` state jobs, cleanup is only triggered when Temporal explicitly reports `TIMED_OUT` status (conservative policy); describe errors or non-timeout statuses are skipped.
  - Acquires a transactional `SELECT ... FOR UPDATE` lock on each job before terminating the workflow; the lock query uses the expected job state (`NEW` or `PROCESSING`) to prevent race conditions where the job state changed between scan and cleanup.
  - Uses job-type-specific workflow timeouts (via `getWorkflowTimeoutForJobType`) to determine when PROCESSING jobs have exceeded their timeout window. See [PROCESSING State Timeout Detection](0023-workflow-supervisor-processing-state-timeout.md) for full details.

- **Supervisor Handlers (`core/tasks/supervisor-handler/`)**
  - Implement resource-specific cleanup logic invoked when the supervisor detects a timeout.
  - Current handlers:
    - `kms_supervisor_handler.go`: Cleans up CMEK configuration resources in VCP and SDE, including the auxiliary `SDE_KMS_CREATE` job newly created when provisioning CMEK through the proxy.
    - `pool_supervisor_handler.go`: Cleans up pool metadata from VCP.
    - `volume_supervisor_handler.go`: Cleans up volume metadata and related child resources from VCP.
    - `backup_supervisor_handler.go`: Handles backup related jobs (unchanged).
    - `snapshot_supervisor_handler.go`: Handles snapshot jobs (unchanged).
    - `replication_supervisor_handler.go`: Handles replication jobs (unchanged).
  - Handlers advertise supported job types and run cleanup routines under a `30s` timeout to avoid prolonged retries.

## Key Design Choices

- **Grace Period for Missing Workflows**: `WORKFLOW_SUPERVISOR_NOT_FOUND_GRACE_PERIOD` defaults to 5 minutes, preventing cleanup while the workflow might still be starting.
- **Event-driven Cleanup**: Handlers receive an `EventTimeout` signal after the workflow state check completes. The supervisor marks jobs as `ERROR` only after handler cleanup succeeds, allowing future supervisor runs to retry cleanup if needed.
- **Modular Handlers**: Handlers are independently testable and register the job types they support, simplifying addition of new cleanup routines.
- **Temporal Describe API**: The supervisor relies on `DescribeWorkflowExecution` to differentiate between `TIMED_OUT`, running, or missing workflows.

## Handler Responsibilities

### CMEK & SDE Cleanup

- Fetch the KMS config metadata (`GetKmsConfig`).
- Attempt to delete corresponding SDE configuration if attributes are present.
- Remove the config from VCP (`DeleteKmsConfig`) with timeout detail for auditing.
- Mark the paired `SDE_KMS_CREATE` job as `ERROR` when the CVP create call fails so that background processors and dashboards reflect the cleanup status.
- Jobs are created with an override grace period (default **14 minutes** via `CMEK_WORKFLOW_GLOBAL_TIMEOUT_MINUTES`) so the supervisor waits for CVP propagation before intervening.
- If the CVP response parsing fails or the subsequent `CreateKmsConfig` orchestration returns an error, the override grace period is cleared (set to `0`) so the supervisor’s regular five‑minute sweep can reclaim the job without waiting the full grace window, preventing stale SDE artefacts.

### Pool Cleanup

- Fetch the pool (`GetPoolByUUID`).
- Remove the pool from VCP (`DeletePool`).

### Volume Cleanup

- Delete volume metadata and child resources via `DeleteVolumeAndChildResources`.

### Additional Handlers

- Backup, snapshot, and replication handlers follow the same pattern; they were unaffected by the latest CMEK/SDE changes and continue to implement idempotent cleanup for their respective job types.

All handlers tolerate "not found" conditions to support idempotent retries.

## Failure Handling

- If Temporal describe calls fail with an error other than `NotFound`, the supervisor logs and skips cleanup.
- Handler errors are logged, and the job state remains unchanged (still `NEW`), allowing future runs to retry cleanup.
- Job state transitions to `ERROR` only after handler cleanup completes without error (and only if the supervisor was able to lock the job row).

## Metrics

- The background scheduler increments `vcp.background.task.runs` when the workflow supervisor acquires the admin-job lock and begins a sweep. The metric is tagged with `task=WORKFLOW_SUPERVISOR_SWEEP` and also updates the `vcp.background.task.last_run_timestamp` observable gauge to record when the scan started.
- Failures while registering or acquiring the lock emit `vcp.background.task.errors` with `task=WORKFLOW_SUPERVISOR_SWEEP` and a `reason` attribute of `schedule_registration`, `create_admin_job_spec`, `acquire_lock`, or `load_job_spec` so operators can distinguish where the sweep stalled.

## Configuration

- `WORKFLOW_SUPERVISOR_NOT_FOUND_GRACE_PERIOD`: Grace period for missing workflows (default 5 minutes).
- `WORKFLOW_SUPERVISOR_PROCESSING_TIMEOUT_GRACE_PERIOD`: Additional wait time after the workflow timeout before treating a PROCESSING job as timed out (default 5 minutes).
- `WORKFLOW_SUPERVISOR_PROCESSING_TIMEOUT_ENABLED`: Feature flag to enable/disable the PROCESSING state scan (default `true`).
- `UPGRADE_JOB_PICKUP_TIMEOUT`: Maximum time a top-level single or batch cluster upgrade may remain `PENDING` without a start timestamp before it is terminated and marked `FAILED` (default 60 seconds).
- `resourceCleanupTimeout`: Hard-coded 30s timeout for handler execution.
- `temporalDescribeTimeout`: Hard-coded 15s timeout for describe calls.

### Override Grace Period Guidance

- Use the `OverrideGracePeriod` attribute only when a newly created job is expected to look stalled for a short window (for example, while waiting for an external service such as CVP to surface a workflow).
- Set a positive duration to delay supervisor intervention; the supervisor skips cleanup until the period elapses.
- To let the supervisor run its normal five-minute sweep, omit the field or set it to `0`.
- When surface errors after provisioning has begun (e.g., CVP response parsing or orchestration failures), clear the override (set to `0`) so the supervisor can immediately reclaim the job and clean up dependent resources.

## Testing

Unit tests cover:

- Each handler’s behavior for timeout events, missing resources, and successful cleanup.
- Supervisor runner scenarios for grace-period logic, timed-out workflows, non-timeout statuses, and handler error propagation.
- Lock acquisition behaviour: tests now confirm that jobs found in a non-expected state are skipped without invoking cleanup or Temporal termination, preventing double processing.
- PROCESSING state timeout detection: scan, per-job timeout filtering, conservative cleanup policy, and handler branching. See [0023-workflow-supervisor-processing-state-timeout.md](0023-workflow-supervisor-processing-state-timeout.md).

See:

- `core/tasks/workflow_supervisor_task_test.go`
- `core/tasks/supervisor-handler/*_test.go`
