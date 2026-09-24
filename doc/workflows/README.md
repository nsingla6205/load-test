# VSA Control Plane Workflows Documentation

This directory contains detailed documentation for all workflows in the VSA Control Plane system. Each workflow is documented with its purpose, activities, child workflows, and execution flow.

## Workflow Categories

### Core Workflows
- [Volume Workflows](./core/volume-workflows.md) - Volume lifecycle management
- [Pool Workflows](./core/pool-workflows.md) - Storage pool management
- [Cluster Workflows](./core/cluster-workflows.md) - VSA cluster management
- [Backup Workflows](./core/backup-workflows.md) - Backup and restore operations
- [Snapshot Workflows](./core/snapshot-workflows.md) - Snapshot management
- [ADC Workflows](./core/adc-workflows.md) - Application Data Collection

### Background Workflows
- [Scheduled Backup Workflows](./background/scheduled-backup-workflows.md) - Automated backup scheduling
- [Resource Cleanup Workflows](./background/resource-cleanup-workflows.md) - Resource cleanup and maintenance

#### Certificate CA and authentication reconciliation
- Core API `POST /v1/pools/reconcileCertificateAuthority` accepts `all`,
  `poolList`, or `pool` selection and always reconciles toward the Helm CA.
  The `202` response includes a persistent `jobId`. Poll
  `GET /v1/pools/reconcileCertificateAuthority?jobId={jobId}` for aggregate and
  per-pool status; forced rotation jobs are polled the same way on
  `GET /v1/pools/forceCertificateRotation?jobId={jobId}`. Each status route only
  reports jobs from its own operation and returns `404` otherwise. `DONE` means
  every selected/discovered pool completed; `ERROR` includes the failed child
  pool jobs. Worker startup never initiates CA changes.
- `ReconcileVsaCAOnChangeWorkflow` carries the desired CA URI and selected pool
  UUIDs in Temporal history. With `all`, it scans certificate-auth (AuthType 2)
  pools in batches.
- `ReconcilePoolCAWorkflow` serializes each pool through the control workflow
  before rotating toward that explicit desired CA.
- `ReconcilePoolAuthTypeWorkflow` is scheduled separately and selects only
  AuthType 1 pools that retain a parent certificate ID, CA URI, or the approved
  fallback username marker proving prior certificate authentication. Native
  AuthType 1 pools are filtered out and
  defensively skipped by the activity. Emergency-fallback rows use
  `username_pre_cert_fix` to keep password bootstrap on `admin` while restoring
  the original pool and expert certificate usernames at promotion. The workflow restores authentication only
  after CA lifetime validation, root-to-intermediate `client_ca` and `server_ca`
  trust installation and readback, security-login verification, parent
  candidate connectivity, and certificate-auth expert credential connectivity.
- Minted candidates are persisted as `certificate_id_new`/`staged_ca_uri` and
  reused after post-stage failures. Only an unreferenced stage-write failure,
  or a guarded detach of a stale or wrong-CA candidate, permits revocation.
- `MINIMUM_ISSUING_CA_REMAINING_LIFETIME` defaults to `2160h` (90 days / 3 months). The effective
  issuance floor is the greater of this value and `CERTIFICATE_LIFETIME`.

#### On-demand certificate rotation
- Core API `POST /v1/pools/forceCertificateRotation` accepts `all`, `poolList`,
  or `pool` selection and starts `ForceVsaCertificateRotationWorkflow`. Its
  `jobId` is polled through the same certificate-maintenance GET endpoint.
- Scheduled and forced certificate rotation retain each pool's persisted CA by
  default. Set `ENABLE_CA_CHANGE_DURING_CERTIFICATE_ROTATION=true` to allow
  those paths to move pools to the Helm-configured CA. Explicit CA
  reconciliation is not controlled by this flag.
- `ForcePoolCertificateRotationWorkflow` rotates one pool's certificate
  regardless of remaining lifetime. It is gated by
  `ENABLE_FORCED_CERTIFICATE_ROTATION` and `ENABLE_VSA_CERTIFICATE_ROTATION`, both
  read in a `SideEffect` so the decision is recorded in history, and it fails with
  a non-retryable error when either is off.
- It queues `ForceRotatePoolCertificateWorkflow` through the per-pool
  `Pool_<uuid>_Ops_All` control workflow, so a forced run serializes against pool
  update, delete, and scheduled rotation exactly like the fleet path.
- The forced child skips the remaining-lifetime check, does not chain password
  rotation, and propagates rotation failures instead of swallowing them. The skip
  is carried into the activity as `PoolContext.Force`, because the activity
  re-derives eligibility itself and would otherwise no-op on a current certificate.
- `Force` changes the eligibility gate, not the scope: a forced run replaces the
  same certificates the scheduled path would. For ONTAP mode pools that includes
  certificate-auth expert mode credentials, which expire independently of the pool
  certificate. Password-auth expert credentials remain unchanged and are handled
  only by the separate AuthType reconciliation flow.
- Expert credentials are reconciled to the target CA before parent promotion.
  The previous expert leaf stays parked in `certificate_id_new` until the next
  expert reconcile, matching parent certificate deferred cleanup.
- Exists because a freshly created pool can never satisfy the lifetime check, so
  the scheduled path cannot verify rotation end to end. Driven by the
  `tests/ccfe/certificate_rotation` suite in `vsa-cp-cd`.

### Replication Workflows
- [Replication Workflows](./replication/replication-workflows.md) - Volume replication management

### KMS Workflows
- [KMS Workflows](./kms/kms-workflows.md) - Key Management Service operations

### FlexCache Workflows
- [FlexCache Workflows](./flexcache/flexcache-workflows.md) - FlexCache volume management

### Control Workflows
- [Control Workflows](./control/control-workflows.md) - Workflow orchestration and control

## Workflow Architecture

All workflows in the VSA Control Plane follow a consistent architecture pattern:

### Base Workflow Structure
```go
type BaseWorkflow struct {
    ID         string
    Status     string
    CustomerID string
    Logger     log.Logger
}
```

### Workflow Interface
```go
type WorkflowInterface interface {
    Setup(ctx workflow.Context, input interface{}) error
    Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError)
    UpdateJobStatus(ctx workflow.Context, status string, err error) error
}
```

### Common Workflow States
- `CREATED` - Workflow has been created but not started
- `RUNNING` - Workflow is currently executing
- `COMPLETED` - Workflow completed successfully
- `FAILED` - Workflow failed with an error
- `CANCELLED` - Workflow was cancelled
- `TIMEOUT` - Workflow timed out
- `RETRY` - Workflow is retrying after a failure
- `PAUSED` - Workflow is paused
- `RESUMED` - Workflow was resumed from pause
- `ABORTED` - Workflow was aborted
- `PENDING` - Workflow is pending execution

## Error Handling

All workflows implement consistent error handling patterns:

1. **Error Conversion**: Convert errors to `*vsaerrors.CustomError` for consistent error handling
2. **Rollback Management**: Implement rollback mechanisms for failed operations
3. **Retry Policies**: Configure retry policies for transient failures
4. **Logging**: Comprehensive logging at all levels with correlation IDs

## Retry Policies

Workflows use configurable retry policies with the following parameters:

- `InitialInterval`: Initial retry interval (default: 5s)
- `BackoffCoefficient`: Backoff multiplier (default: 2.0)
- `MaximumInterval`: Maximum retry interval (default: 5m)
- `MaximumAttempts`: Maximum retry attempts (default: 3)
- `StartToCloseTimeout`: Activity timeout (default: 55m)

## Activity Options

All activities use consistent activity options:

```go
ao := workflow.ActivityOptions{
    StartToCloseTimeout: retryPolicy.StartToCloseTimeout,
    RetryPolicy: &temporal.RetryPolicy{
        InitialInterval:        retryPolicy.InitialInterval,
        BackoffCoefficient:     retryPolicy.BackoffCoefficient,
        MaximumInterval:        retryPolicy.MaximumInterval,
        MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
        NonRetryableErrorTypes: []string{"PanicError"},
    },
}
```

## Logging and Tracing

All workflows integrate with the slog logging framework:

- **Context Propagation**: Request correlation IDs are propagated through workflows
- **Structured Logging**: JSON-formatted logs with consistent field naming
- **OpenTelemetry Integration**: Automatic trace and span ID inclusion
- **Error Correlation**: Errors are correlated with workflow execution context

## Testing

All workflows have comprehensive test coverage:

- **Unit Tests**: Individual workflow function testing
- **Integration Tests**: End-to-end workflow testing
- **Mock Activities**: Mock implementations for testing
- **Error Scenarios**: Testing failure and retry scenarios

## Monitoring and Observability

Workflows provide extensive monitoring capabilities:

- **Metrics**: Performance and execution metrics
- **Tracing**: Distributed tracing across workflow execution
- **Logging**: Structured logging for debugging and analysis
- **Health Checks**: Workflow health and status monitoring

## Related Documentation

- [Temporal Debugging Guide](../guides/temporal-debugging.md)
- [Logging Framework ADR](../architecture/decisions/0011-slog-logging-framework.md)
- [API Documentation](../api/overview.md)