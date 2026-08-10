# Runbook for create_largepool_failures

This runbook provides a structured approach to **identifying, investigating, and diagnosing** large capacity pool creation failures in the VSA Control Plane system.

## ⚠️ Important: Process Adherence Required

**This document is a diagnostic and investigation guide, NOT an implementation manual.**

- **Purpose:** Guide support personnel on **where to check**, **what to check**, and **how to identify** root causes
- **Do NOT:** Implement fixes, workarounds, or changes without following proper change management processes
- **Always:** Follow the change management process outlined in Section 6.D before taking any remediation actions
- **Escalate:** When in doubt, escalate to SME or Incident manager rather than implementing changes directly

All fixes, workarounds, and configuration changes **MUST** go through proper change management (tickets, approvals, testing, deployment).

# Alert Information

| Field               | Description                                                                                   |
| :-----------------: | :------------------------------------------------------------------------------------------: |
| Alert Name          | create_largepool_failures                                                                     |
| Alert Link          | [GCP Monitoring Alert](https://console.cloud.google.com/monitoring/alerting/policies/[POLICY_ID]?project=vsa-monitoring-prod) |
| Alert Threshold     | Above 1                                                                                        |
| Date of Creation    | [Date]                                                                                         |
| SME                 | The Subject Matter Expert responsible for this alert or system.                                |
| Severity            | Error                                                                                          |

# Debugging Steps (Guidelines)

**Purpose of this section:** Guide support personnel on **where to check** and **what to check** to identify root causes. This is an **investigation and diagnostic guide**, not an implementation manual.

## 1. Acknowledge the Alert
- **Action:** Acknowledge the alert in the monitoring system to prevent repeated notifications.
- **Record:** Note the time of acknowledgment for future reference.
- **Initial Assessment:** Check if this is a single failure or a pattern (multiple pools failing).

## 2. Gather Initial Context
- **Review Alert Details:**
  - Check the alert description, severity, and any associated dashboards or logs.
  - Identify the affected pool name, project number, location, and operation/job ID.
  - Note the correlation ID and request ID for log correlation.
- **Check Recent Changes:**
  - Review deployment logs, configuration changes, or infrastructure updates.
  - Check if any recent changes to large pool validation logic or VLM configuration.
  - Verify if environment variables for large pool limits were modified.
- **Identify Scope:**
  - Determine if this affects a specific region, project, or is system-wide.
  - Check if other large pools are being created successfully.

## 3. Validate the Alert
- **Confirm Legitimacy:**
  - Verify the pool is actually in error state (not a false positive).
  - Check the API response, DB state, and workflow/job status.
  - Confirm the job type is `CREATE_LARGE_POOL` (not `CREATE_POOL`).
- **Verify Error Type:**
  - Check if it's a client-side error (400/409) or server-side error (500/503).
  - Review the error message and tracking ID from the job.

## 4. Identify the Root Cause

### A. Logs Analysis

#### API Layer Logs
- **Location:** `google-proxy` service logs
- **What to Check:**
  - Validation errors for large pool parameters (size, throughput, IOPS).
  - Error messages indicating which parameter failed validation.
  - HTTP status codes (400 for validation, 500 for internal errors).
- **Key Fields:** `correlation_id`, `request_id`, `job_type`, `error_details`

#### Orchestrator/Workflow Logs
- **Location:** `worker` service logs, Temporal workflow logs
- **What to Check:**
  - Workflow execution errors and activity failures.
  - Large pool validation errors from `LargeCapacityPoolValidator`.
  - VLM (VSA Lifecycle Manager) operation failures.
  - Timeout errors (large pools use 60-minute timeout vs 55-minute for regular).
- **Key Fields:** `workflow_id`, `activity_type`, `error`, `tracking_id`

#### Database Logs
- **Location:** Database connection and query logs
- **What to Check:**
  - Pool creation record in database.
  - Job state transitions (NEW → PROCESSING → ERROR).
  - Unique constraint violations (duplicate pool names/vendor IDs).
  - Transaction failures or deadlocks.

#### GCP Operation Logs
- **Location:** GCP Console → Operations
- **What to Check:**
  - VSA cluster deployment operations (longer for large pools).
  - Network provisioning operations.
  - Resource quota errors (large pools require more resources).

### B. Metrics Review

#### System Health Metrics
- **API Error Rates:** Check for spikes in 400/500 errors around alert time.
- **Workflow Success Rates:** Compare large pool vs regular pool success rates.
- **Database Health:** Connection pool usage, query latency, transaction failures.
- **Temporal Metrics:** Workflow execution times, activity durations, retry counts.

#### Resource Utilization
- **GCP Quotas:** Check compute, network, and storage quotas (large pools need more).
- **VLM Queue Depth:** Check if VLM task queue is backed up.
- **Worker Capacity:** Verify workers are available and not overloaded.

### C. System Health Check

#### Database Connectivity
- Verify DB connectivity from orchestrator and worker services.
- Check for connection pool exhaustion.
- Review DB migration status (ensure all migrations applied).

#### Temporal Infrastructure
- **Worker Status:** Ensure Temporal workers are running and healthy.
  - **Check via Metrics:** Review Temporal worker metrics in monitoring project (worker health, task processing rate).
  - **Check via CLI:** Use `tctl --namespace <vcp-namespace> workflow list --limit 10` to verify workers are processing tasks.
- **Workflow Registration:** Verify `CreatePoolWorkflow` is registered (same workflow, different job type).
  - **Check via CLI:** Use `tctl --namespace <vcp-namespace> workflow describe --workflow-id <workflow_id>` to verify workflow type.
- **Task Queue:** Check `CustomerTaskQueue` is processing tasks.
  - **Check via Metrics:** Review task queue depth and processing rate metrics in monitoring project.
  - **Check via CLI:** Use `tctl --namespace <vcp-namespace> task-queue describe --task-queue CustomerTaskQueue` to check queue status.
- **Workflow Status:** Review workflow execution status for the failed job.
  - **Check via Metrics:** Review workflow execution metrics (duration, success rate, error rate) in monitoring project.
  - **Check via CLI:** Use `tctl --namespace <vcp-namespace> workflow describe --workflow-id <workflow_id>` to get workflow status and history.

#### GCP Resource Availability
- **Compute Quotas:** Large pools require more VM instances (minimum 6 HA pairs).
- **Network Quotas:** More subnets, firewall rules, and IP addresses needed.
- **Storage Quotas:** Larger storage requirements (6 TiB minimum).

### D. Dependency Check

#### GCP APIs
- **Service Networking API:** Required for tenancy/subnet creation.
- **Compute Engine API:** Required for VM instance creation.
- **IAM API:** Required for service account management.
- **Storage API:** Required if auto-tiering is enabled.
- **DNS API:** Required for DNS configuration.
- **Secret Manager API:** Required if using secrets.
- **KMS API:** Required if KMS is configured.

#### Service Account Permissions
- **Required Roles:**
  - `serviceAccountAdmin` - For service account creation
  - `projectIamAdmin` - For IAM bindings
  - `compute.instanceAdmin` - For VM operations
  - `storage.admin` - For GCS bucket operations (if auto-tiering)
- **Verify:** Service account has sufficient permissions for large pool operations.

#### VLM (VSA Lifecycle Manager) Configuration
- **Config File:** `/config/vmrs_gcp.yaml` must be valid and accessible.
- **Instance Types:** Verify required instance types are available in the region.
- **HA Pairs:** Large pools require minimum 6 HA pairs (configurable via `NUMBER_OF_HA_PAIRS_LARGE_CAPACITY`).
- **Queue:** Check `VSALifecycleManagerQueue` is operational.

### E. Configuration Review

#### Large Pool Specific Parameters

**Size Validation:**
- **Minimum Size:** 6 TiB (`MIN_LV_POOL_COOL_TIER_CAPACITY`)
- **Maximum Size (without auto-tiering):** 2.48 PiB (`MAX_LV_HOT_TIER_POOL_CAPACITY`)
- **Maximum Size (with auto-tiering):** 20 PiB (`MAX_LV_POOL_CAPACITY`)
- **Granularity:** Must be a multiple of 1 GiB (`MIN_SIZE_GRANULARITY`)
- **Error Message:** "SizeInBytes must be at least 6 TiB for Large Capacity pools"

**Throughput Validation:**
- **Minimum Throughput:** 64 MiBps (`MIN_LV_THROUGHPUT`)
- **Maximum Throughput:** 60,000 MiBps (`MAX_LV_THROUGHPUT`)
- **Error Message:** "TotalThroughputMibps must be between 64 and 60000 MiBps for Large Capacity pools"

**IOPS Validation:**
- **Minimum IOPS:** 1,024 IOPS (16 × 64 MiBps, `MIN_LV_CUSTOM_IOPS`)
- **Maximum IOPS:** 960,000 IOPS (16 × 60,000 MiBps, `MAX_LV_CUSTOM_IOPS`)
- **IOPS/Throughput Ratio:** Must be at least 16 IOPS per MiBps
- **Error Message:** "TotalIops must be at least [calculated] IOPS for Large Capacity pools"

**Auto-Tiering Validation (if enabled):**
- **Minimum Hot Tier Size:** 6 TiB (`MIN_HOT_TIER_SIZE_LARGE_VOLUMES`)
- **Hot Tier Size:** Must be less than total pool size
- **Feature Flag:** `AUTO_TIERING_ENABLED` must be `true`
- **Error Message:** "HotTierSizeInBytes must be between 6 TiB and pool size"

**QoS Type:**
- **Supported:** `auto` (default) or `manual` (if `ENABLE_MQOS=true`)
- **Error Message:** "Given QoS type not supported for Unified Flex Storage Pool"

#### Environment Variables

**Large Pool Specific:**
- `MIN_LV_POOL_COOL_TIER_CAPACITY` (default: 6 TiB)
- `MAX_LV_POOL_CAPACITY` (default: 20 PiB)
- `MAX_LV_HOT_TIER_POOL_CAPACITY` (default: 2.48 PiB)
- `MIN_LV_THROUGHPUT` (default: 64 MiBps)
- `MAX_LV_THROUGHPUT` (default: 60,000 MiBps)
- `MIN_LV_CUSTOM_IOPS` (default: 1,024 IOPS)
- `MAX_LV_CUSTOM_IOPS` (default: 960,000 IOPS)
- `MIN_HOT_TIER_SIZE_LARGE_VOLUMES` (default: 6 TiB)

**True-Zonal Large Pool Specific (when true-zonal path is selected):**
- `MIN_TRUE_ZONAL_THROUGHPUT` (default: `MIN_LV_THROUGHPUT`)
- `MAX_TRUE_ZONAL_THROUGHPUT` (default: `MAX_LV_THROUGHPUT`)
- `MIN_TRUE_ZONAL_CUSTOM_IOPS` (default: `IOPS_PER_MIBPS * MIN_TRUE_ZONAL_THROUGHPUT`, i.e. 1,024 IOPS)
- `MAX_TRUE_ZONAL_CUSTOM_IOPS` (default: `IOPS_PER_MIBPS * MAX_TRUE_ZONAL_THROUGHPUT`, i.e. 960,000 IOPS)

**Important fallback behavior:**
- If `MAX_TRUE_ZONAL_*`/`MIN_TRUE_ZONAL_*` are not set, validation falls back to LV equivalents.
- To avoid mismatched behavior, set the same true-zonal limits in **core**, **google-proxy**, and **vcp-worker** runtime environments.

**Workflow Timeouts:**
- `START_TO_CLOSE_WORKFLOW_TIMEOUT_LV` (default: 60m) - **Longer than regular pools (55m)**
- `POOL_ACTIVITY_HEARTBEAT_TIMEOUT` (default: 5m)
- `NUMBER_OF_HA_PAIRS_LARGE_CAPACITY` (default: 6)
- `VLM_CREATE_VSA_CLUSTER_DEPLOYMENT_WF_TIMEOUT_MINUTES_LV` (default: 45 minutes)

**Feature Flags:**
- `AUTO_TIERING_ENABLED` - Must be `true` if auto-tiering is requested
- `REGIONAL_SUPPORT_ENABLED` - Required for regional pools
- `ENABLE_MQOS` - Required for manual QoS type

## 5. Formulate a Hypothesis

Based on the gathered information, develop a hypothesis about the root cause:

### Common Root Causes for Large Pool Failures

1. **Validation Errors (400):**
   - Pool size below 6 TiB or above limits
   - Throughput/IOPS out of allowed range
   - Hot tier size below 6 TiB (if auto-tiering enabled)
   - QoS type not supported
   - Size not a multiple of 1 GiB

2. **Resource Constraints (409/422):**
   - Duplicate pool name/vendor ID
   - Insufficient GCP quotas (compute, network, storage)
   - Insufficient HA pairs available (minimum 6 required)
   - Instance types not available in region

3. **Configuration Errors (400/422):**
   - Auto-tiering requested but feature flag disabled
   - Manual QoS requested but `ENABLE_MQOS=false`
   - Invalid VLM configuration file
   - Missing or incorrect service account permissions

4. **Infrastructure Failures (500/503):**
   - Database connectivity issues
   - Temporal worker unavailable
   - VLM service unavailable or queue backed up
   - GCP API outages or rate limiting
   - Network provisioning failures

5. **Timeout Errors:**
   - Workflow timeout (60 minutes exceeded)
   - Activity timeout (heartbeat timeout exceeded)
   - VLM deployment timeout (45 minutes exceeded)
   - GCP operation timeout

## 6. Implement a Solution/Mitigation

**⚠️ CRITICAL: Process Adherence Required**

**This section describes potential solutions and mitigation strategies for reference. DO NOT implement any fixes or workarounds without:**
1. Creating a bug ticket first (see Section 6.D)
2. Obtaining proper approvals (SME, Managers)
3. Following change management process (tickets, approvals, testing, deployment)
4. Documenting all actions in the bug ticket

**First, determine if this is a **client-side error** (400/409) or **server-side error** (500/503) to apply the appropriate resolution strategy.**

### A. Client-Side Errors (400/409) - Customer Action Required

**Characteristics:**
- HTTP status codes: 400 (Bad Request), 409 (Conflict), 422 (Unprocessable Entity)
- Validation errors or parameter issues
- Error message indicates invalid input parameters
- Job may not be created (validation fails before job creation)

**Action Required:** **Inform the customer** - Create a google support case for customer communication.

#### A.1. Validation Errors (400) - Inform Customer

**Common Validation Errors:**
- "SizeInBytes must be at least 6 TiB for Large Capacity pools"
- "TotalThroughputMibps must be between 64 and 60000 MiBps for Large Capacity pools"
- "TotalIops must be at least [X] IOPS for Large Capacity pools"
- "HotTierSizeInBytes must be between 6 TiB and pool size"
- "Given pool size must be a multiple of 1 GiB"
- "Given QoS type not supported for Unified Flex Storage Pool"

**Steps to Inform Customer:**
1. **Identify the Specific Error:**
   - Extract the exact error message from logs/API response.
   - Identify which parameter(s) failed validation.
   - Note the tracking ID and correlation ID for reference.

2. **Prepare Customer Communication:**
   - **Template Message (Customer-Facing):**
     ```
     Your large pool creation request failed due to invalid parameters.
     
     Error: [Exact error message]
     Operation ID: [Operation ID] (if available - use this to check operation status)
     Correlation ID: [Correlation ID] (if you provided x-correlation-id header)
     
     Required Corrections:
     - [Specific parameter] must be [correct value/range]
     - [Additional corrections if multiple parameters]
     
     Please review the large pool requirements and resubmit with corrected parameters.
     Reference: [Link to API documentation or requirements]
     ```
   
   - **Internal Reference (for support case notes, not sent to customer):**
     - Tracking ID: [Tracking ID] (internal error code)
     - Job UUID: [Job UUID] (if job was created)
     - Workflow ID: [Workflow ID] (if workflow was started)

3. **Provide Correct Parameter Values:**
   - **Size:** Must be between 6 TiB and 2.48 PiB (or 20 PiB with auto-tiering), multiple of 1 GiB.
   - **Throughput:** Must be between 64 and 60,000 MiBps.
   - **IOPS:** Must be between 1,024 and 960,000 IOPS, and at least 16× throughput.
   - **Hot Tier (if auto-tiering):** Must be ≥ 6 TiB and < pool size.
   - **QoS Type:** Must be "auto" or "manual" (if `ENABLE_MQOS=true`).

4. **Document the Interaction:**
   - Record customer contact details and communication.
   - Note the specific parameters that were incorrect.
   - Track if this is a recurring issue (may indicate documentation gaps).

#### A.2. Conflict Errors (409) - Inform Customer

**Common Conflict Errors:**
- "Pool with same vendorId already exists"
- Duplicate resource ID conflicts

**Steps to Inform Customer:**
1. **Check Pool State:**
   - If pool exists in `CREATING` state: Inform customer they can reuse the operation.
   - If pool exists in other state: Inform customer to use a unique `resourceId`/`vendorId`.

2. **Customer Communication:**
   - **If Pool in CREATING:**
     ```
     A pool with the same vendorId is currently being created.
     Operation ID: [operation_id]
     You can query this operation to check status or wait for completion.
     ```
   - **If Pool Exists:**
     ```
     A pool with vendorId '[vendorId]' already exists.
     Please use a unique resourceId/vendorId for your new pool.
     ```

3. **No Internal Action Required:**
   - Do not modify customer data.
   - Do not delete existing pools.
   - Customer must resolve the conflict.

#### A.3. Resource Quota Errors (422) - May Require Customer Action

**Note:** Some quota errors (like GCP project quotas) are customer-side, while others (like system quotas) are server-side.

**Customer-Side Quota Errors:**
- GCP project compute quotas exceeded
- GCP project network quotas exceeded
- Customer project billing issues

**Steps:**
1. **Verify Error Source:**
   - Check if error is from customer's GCP project quotas.
   - Verify if it's a system-wide quota issue.

2. **If Customer Quota:**
   - Inform customer to:
     - Check their GCP project quotas.
     - Request quota increases from GCP.
     - Free up unused resources.
   - Provide GCP quota documentation link.

3. **If System Quota:**
   - Treat as server-side error (see Section B).

### B. Server-Side Errors (500/503) - Internal Action Required

**Characteristics:**
- HTTP status codes: 500 (Internal Server Error), 503 (Service Unavailable)
- Database errors, workflow failures, infrastructure issues
- System-level problems requiring internal fixes
- Job may be created but workflow fails

**Action Required:** **Internal team action** - We can apply fixes, workarounds, and code changes **ONLY after following proper change management process (Section 6.D)**.

#### B.1. Temporary Mitigation (Require Change Management)

**⚠️ MANDATORY: Change Management Process Required**

**DO NOT implement any workarounds without following the change management process in Section 6.D.**

**This section describes potential workarounds for reference only. All workarounds MUST:**
1. Be tracked in a bug ticket (created first - see Section 6.D, Step 1)
2. Have approval from SME and Managers (documented in ticket)
3. Follow proper change management process (tickets, approvals, testing, deployment)
4. Be documented with rationale and impact assessment

**If you are unsure about any workaround, escalate to SME or team lead.**

##### For Database Errors (500)
- **Action:** Escalate to database team/SME - Database is a managed service shared by multiple applications.
- **Steps:**
  1. **Verify the Issue:**
     - Check DB connectivity from orchestrator and worker services.
     - Verify if error is specific to large pool creation or system-wide.
     - Check application logs for database connection errors.
  2. **Gather Information for Escalation:**
     - Error message and tracking ID
     - Correlation ID and workflow ID
     - Timestamp and duration of issue
     - Affected services (orchestrator, worker, or both)
     - Check if other applications are also affected
  3. **Escalate to Database Team:**
     - Create ticket with full error details and context
     - Include logs showing database connection/query failures
     - Note if this is a transient issue or persistent
     - Do not attempt database-level workarounds (connection pool changes, migrations, restarts) as these affect all applications
  4. **Monitor and Coordinate:**
     - Wait for database team resolution
     - Monitor for recovery
     - Retry pool creation after database team confirms fix

##### For Workflow/Temporal Errors (500)
- **Action:** Restore workflow execution capability.
- **Steps:**
  1. **Verify Temporal workers are running and healthy:**
     - **Check Metrics:** Review worker health metrics in monitoring project (worker count, heartbeat status).
     - **Check CLI:** Use `tctl --namespace <vcp-namespace> workflow list --limit 10` to verify workers are processing.
  2. **Check if `CreatePoolWorkflow` is registered:**
     - **Check CLI:** Use `tctl --namespace <vcp-namespace> workflow describe --workflow-id <workflow_id>` to verify workflow type.
  3. **Verify task queue is processing tasks:**
     - **Check Metrics:** Review `CustomerTaskQueue` metrics in monitoring project (queue depth, processing rate).
     - **Check CLI:** Use `tctl --namespace <vcp-namespace> task-queue describe --task-queue CustomerTaskQueue`.
  4. **Review workflow execution status:**
     - **Check Metrics:** Review workflow execution metrics in monitoring project (duration, status, error details).
     - **Check CLI:** Use `tctl --namespace <vcp-namespace> workflow describe --workflow-id <workflow_id>` to get full workflow history.
- **Workaround (if approved):**
  - Restart Temporal workers if they're stuck (check worker metrics first).
  - Manually retry failed workflows via CLI: `tctl --namespace <vcp-namespace> workflow signal --workflow-id <id> --signal-name retry` (if safe).
  - Temporarily increase worker capacity if metrics show overload (queue depth increasing, processing rate decreasing).

##### For VLM/GCP Provisioning Errors (500/503)
- **Action:** Resolve VLM or GCP infrastructure issues.
- **Steps:**
  1. Check VLM service health and queue depth.
  2. Verify GCP API status and quotas.
  3. Check for ongoing GCP operations that may be stuck.
  4. Verify service account permissions.
- **Workaround (if approved):**
  - Clear VLM queue if backed up (after verifying no critical operations).
  - Retry stuck GCP operations manually.
  - Temporarily increase timeouts if operations are legitimately slow.

##### For Timeout Errors
- **Action:** Determine if timeout is legitimate or indicates a stuck operation.
- **Steps:**
  1. Check GCP console for ongoing operations.
  2. **Verify workflow status:**
     - **Check Metrics:** Review workflow execution duration metrics in monitoring project to see if it's progressing.
     - **Check CLI:** Use `tctl --namespace <vcp-namespace> workflow describe --workflow-id <workflow_id>` to check workflow status and last activity time.
  3. **Check if operation is progressing:**
     - **Check Metrics:** Review activity heartbeat metrics in monitoring project (should see regular heartbeats if progressing).
     - **Check CLI:** Use `tctl --namespace <vcp-namespace> workflow show --workflow-id <workflow_id>` to see activity execution history and heartbeats.
  4. Determine if 60-minute timeout is sufficient for the operation (compare actual duration from metrics vs timeout).
- **Workaround (if approved):**
  - If operation is stuck (no heartbeats, no progress in metrics): Cancel via CLI `tctl --namespace <vcp-namespace> workflow cancel --workflow-id <id>` and retry (after cleanup).
  - If operation is slow but progressing (heartbeats present, metrics show progress): Increase timeout temporarily.
  - If recurring: Investigate why operations take longer than expected (review metrics for patterns).

#### B.2. Permanent Fixes (Require Change Management)

**⚠️ MANDATORY: Change Management Process Required**

**DO NOT implement any permanent fixes without following the change management process in Section 6.D.**

**This section describes potential permanent fixes for reference only. All fixes MUST:**
1. Be tracked in a bug ticket (created first - see Section 6.D, Step 1)
2. Go through proper planning, design, and approval process
3. Follow change management process (tickets, approvals, testing, deployment)
4. Be properly tested and reviewed before deployment

##### Configuration Updates
- **Feature Flags:**
  - Enable/disable feature flags (`AUTO_TIERING_ENABLED`, `ENABLE_MQOS`).
  - Update via configuration management system.
  - Document change and notify team.
- **Environment Variables:**
  - Update large pool limits if requirements change.
  - Modify timeout values if operations legitimately need more time.
  - Update via deployment process with proper testing.
- **VLM Configuration:**
  - Update `/config/vmrs_gcp.yaml` if instance types or HA pair requirements change.
  - Deploy via standard configuration deployment process.

##### Infrastructure Scaling
- **GCP Quotas:**
  - Request permanent quota increases for large pool requirements.
  - Document quota needs and submit GCP quota increase requests.
  - Monitor quota usage after increases.
- **Worker Capacity:**
  - Scale Temporal workers if consistently overloaded.
  - Update worker deployment configuration.
  - Monitor worker performance after scaling.
- **Database Scaling:**
  - Scale database if connection pool or performance is an issue.
  - Coordinate with DB team for scaling operations.
  - Update connection pool configuration.

##### Code/Workflow Fixes
- **Bug Fixes:**
  1. **Raise Bug Ticket:**
     - Create bug ticket with full details (error, logs, reproduction steps).
     - Assign to appropriate team (orchestrator, workflow, VLM).
     - Set priority based on impact (P0 for production outages, P1 for frequent failures).
  2. **Code Changes:**
     - Fix bugs in validation logic, workflow orchestration, or activity implementations.
     - Add proper error handling and logging.
     - Write unit and integration tests.
  3. **Testing:**
     - Test fixes in development/staging environment.
     - Verify fix resolves the issue without introducing regressions.
  4. **Deployment:**
     - Deploy via standard CI/CD process.
     - Monitor after deployment to verify fix.
- **Timeout Adjustments:**
  - Increase timeouts if operations legitimately take longer.
  - Update `START_TO_CLOSE_WORKFLOW_TIMEOUT_LV` if needed.
  - Document timeout changes and rationale.
- **Error Handling Improvements:**
  - Improve error messages to guide users to correct parameters.
  - Add more detailed logging for debugging.
  - Enhance error taxonomy and tracking.

##### Documentation Updates
- **API Documentation:**
  - Update API docs if parameter validation errors are common.
  - Add examples of correct large pool creation requests.
  - Document common pitfalls and how to avoid them.
- **Runbook Updates:**
  - Add new troubleshooting steps discovered.
  - Update common symptoms table with new patterns.
  - Document workarounds that were effective.

### C. Error Classification Decision Tree

```
Is HTTP status 400, 409, or 422?
├─ YES → Client-Side Error
│   ├─ Validation error (400)?
│   │   └─ Inform customer to correct parameters
│   ├─ Conflict error (409)?
│   │   └─ Inform customer about duplicate resource
│   └─ Quota error (422)?
│       ├─ Customer GCP quota?
│       │   └─ Inform customer to increase quotas
│       └─ System quota?
│           └─ Treat as server-side error
│
└─ NO (500, 503, or other) → Server-Side Error
    ├─ Database error?
    │   └─ Apply DB workaround or fix
    ├─ Workflow/Temporal error?
    │   └─ Apply workflow workaround or fix
    ├─ VLM/GCP error?
    │   └─ Apply infrastructure workaround or fix
    └─ Unknown error?
        └─ Investigate and classify, then apply appropriate fix
```

### D. Change Management Process for Server-Side Fixes

**⚠️ THIS IS THE MANDATORY PROCESS - Follow this for ALL fixes and workarounds**

**Before implementing ANY fix or workaround described in Sections 6.B.1 or 6.B.2, you MUST follow this process.**

**Step 1: Create Bug Ticket**
1. **Create Ticket:** Raise a bug ticket against the issue with full details:
   - Error details, logs, correlation ID, tracking ID
   - Root cause analysis
   - Impact assessment
   - Affected customers/operations

**Step 2: Decide on Workaround (Based on Bug Ticket)**
2. **Assess Need for Workaround:**
   - Review bug ticket to determine if immediate workaround is needed
   - Consider: severity, customer impact, availability of permanent fix timeline
   - Determine if workaround is safe and won't cause data loss
3. **If Workaround Needed:**
   - **Get Approval:** Obtain approval from SME and Managers (document in bug ticket)
   - **Document in Ticket:** Record workaround applied, time, rationale, and steps taken
   - **Apply Workaround:** Execute approved workaround following change management process
   - **Monitor:** Watch for resolution and any side effects (update ticket with findings)
4. **If No Workaround Needed:**
   - Proceed directly to permanent fix planning

**Step 3: Permanent Fix (Tracked in Bug Ticket)**
5. **Plan:** Design solution, estimate effort, get approval (document in bug ticket)
6. **Implement:** Code changes, tests, documentation updates
7. **Test:** Unit tests, integration tests, staging validation
8. **Review:** Code review, architecture review if needed
9. **Deploy:** Deploy via CI/CD with proper monitoring
10. **Verify:** Confirm fix resolves the issue in production
11. **Close:** Close ticket and update documentation

## 7. Verify the Fix

- **Monitor System:**
  - Verify the alert clears and no new failures occur.
  - Check that the pool transitions to `READY` state.
  - Monitor subsequent large pool creations for success.
- **Run Tests:**
  - Execute large pool creation tests with various configurations.
  - Verify edge cases (minimum size, maximum size, auto-tiering enabled/disabled).
- **Check Logs and Metrics:**
  - Confirm absence of related errors in logs.
  - **Verify successful workflow completion:**
    - **Check Metrics:** Review workflow completion metrics in monitoring project (success rate, duration).
    - **Check CLI:** Use `tctl --namespace <vcp-namespace> workflow describe --workflow-id <workflow_id>` to verify workflow status is COMPLETED.

## 8. Document the Resolution

- **Record Details:**
  - Document the root cause, resolution steps, and any temporary mitigations applied.
  - Note the specific parameter values that caused the failure.
  - Record any configuration changes made.
- **Update Runbook:**
  - Add new insights or steps discovered during troubleshooting.
  - Update common symptoms table with new patterns.
- **Share Learnings:**
  - Communicate findings with the team to prevent recurrence.
  - Update API documentation if parameter validation errors were common.

---

# Large Pool Creation Error Debugging Detailed Runbook

## Step 1: Collect Failure Details

### Immediate Information Gathering
- **Error Message:** Record the exact error message from API response or logs.
- **Operation/Job ID:** Note the operation ID and job UUID returned by the API.
- **Request Payload:** Capture all parameters sent in the create pool request:
  - `largeCapacity: true` (must be set)
  - `sizeInBytes` (must be ≥ 6 TiB)
  - `totalThroughputMibps` (must be 64-60,000 MiBps)
  - `totalIops` (must be 1,024-960,000 IOPS, and ≥ 16× throughput)
  - `allowAutoTiering` (if true, requires `hotTierSizeInBytes` ≥ 6 TiB)
  - `qosType` (must be "auto" or "manual" if `ENABLE_MQOS=true`)
- **Pool State:** Check the pool state in the database (should be `CREATING` or error state).
- **Job Type:** Verify job type is `CREATE_LARGE_POOL` (not `CREATE_POOL`).

### Log Correlation
- **Correlation ID:** Use to find all related logs across services.
- **Request ID:** Use to trace the request through the system.
- **Workflow ID:** Use to query Temporal workflow status.

## Step 2: Identify Where the Failure Occurred

### Failure Point Detection

#### A. API/Validation Layer (400/409 errors, immediate response)
**Indicators:**
- HTTP 400 with validation error message
- HTTP 409 with conflict error
- Error returned immediately (no job created)
- Job not created in database

**Common Errors:**
- "SizeInBytes must be at least 6 TiB for Large Capacity pools"
- "TotalThroughputMibps must be between 64 and 60000 MiBps for Large Capacity pools"
- "TotalIops must be at least [X] IOPS for Large Capacity pools"
- "HotTierSizeInBytes must be between 6 TiB and pool size"
- "Given pool size must be a multiple of 1 GiB"
- "Given QoS type not supported for Unified Flex Storage Pool"

#### B. Orchestrator/Database Layer (500 errors, job not created, DB errors)
**Indicators:**
- HTTP 500 error
- Job not created in database
- Database connection errors in logs
- Unique constraint violations

**Common Errors:**
- Database connection failures
- Transaction deadlocks
- Unique constraint violations (duplicate pool name/vendor ID)
- Migration errors

#### C. Workflow/Temporal Layer (Job created, pool stuck in CREATING)
**Indicators:**
- Job created with state `NEW` or `PROCESSING`
- Pool stuck in `CREATING` state
- Workflow not progressing (no activity updates in metrics or CLI)
- Activity failures in workflow logs

**Common Errors:**
- Workflow not registered
- Temporal worker unavailable (check worker metrics)
- Activity timeout errors (check activity duration metrics)
- Workflow timeout (60 minutes exceeded - check workflow duration metrics)

**How to Verify:**
- **Check Metrics:** Review workflow execution metrics in monitoring project (workflow status, activity status, duration).
- **Check CLI:** Use `tctl --namespace <vcp-namespace> workflow describe --workflow-id <workflow_id>` to get workflow status and execution history.

#### D. VLM/GCP Resource Provisioning (Timeouts, quota, permission errors)
**Indicators:**
- Workflow progresses but fails during VSA deployment
- GCP operation errors in logs
- Quota exceeded errors
- Permission denied errors

**Common Errors:**
- "Insufficient quota for instance type"
- "Service account does not have required permissions"
- "VLM queue is full"
- "VSA cluster deployment timeout"

## Step 3: Troubleshoot by Component

### A. API/Validation Errors (400/409)

#### Large Pool Size Validation
**Check:**
- Is `largeCapacity: true` set in the request?
- Is `sizeInBytes` ≥ 6 TiB (`MIN_LV_POOL_COOL_TIER_CAPACITY`)?
- Is `sizeInBytes` ≤ 2.48 PiB without auto-tiering (`MAX_LV_HOT_TIER_POOL_CAPACITY`)?
- Is `sizeInBytes` ≤ 20 PiB with auto-tiering (`MAX_LV_POOL_CAPACITY`)?
- Is `sizeInBytes` a multiple of 1 GiB?

**Customer Action Required:**
- Customer must adjust `sizeInBytes` to meet requirements.
- If auto-tiering is needed, customer must ensure it's enabled and size is within 20 PiB limit.
- **Internal Action:** Inform customer of the error and provide correct parameter ranges.

#### Large Pool Throughput Validation
**Check:**
- Is `totalThroughputMibps` between 64 and 60,000 MiBps?
- Is `totalThroughputMibps` set (not null/zero)?

**Customer Action Required:**
- Customer must adjust throughput to be within 64-60,000 MiBps range.
- **Internal Action:** Inform customer of the error and provide correct throughput range.

#### Large Pool IOPS Validation
**Check:**
- Is `totalIops` between 1,024 and 960,000 IOPS?
- If both throughput and IOPS are provided, is IOPS ≥ 16 × throughput?
- If only throughput is provided, calculated IOPS = throughput × 16 (must be ≤ 960,000)

**Customer Action Required:**
- Customer must adjust IOPS to meet minimum requirement (16× throughput).
- Customer must ensure IOPS is within 1,024-960,000 range.
- **Internal Action:** Inform customer of the error and provide IOPS calculation guidance.

#### Auto-Tiering Validation (if enabled)
**Check:**
- Is `AUTO_TIERING_ENABLED=true`?
- Is `allowAutoTiering: true` in request?
- Is `hotTierSizeInBytes` ≥ 6 TiB (`MIN_HOT_TIER_SIZE_LARGE_VOLUMES`)?
- Is `hotTierSizeInBytes` < `sizeInBytes`?

**Action Required:**
- **If feature flag disabled:** Internal team must enable `AUTO_TIERING_ENABLED` (server-side fix).
- **If hot tier size invalid:** Customer must adjust hot tier size to be ≥ 6 TiB and < pool size (client-side fix).
- **Internal Action:** Determine root cause and apply appropriate fix (internal config change or inform customer).

#### QoS Type Validation
**Check:**
- Is `qosType` set to "auto" (default)?
- If "manual", is `ENABLE_MQOS=true`?

**Action Required:**
- **If QoS type invalid:** Customer must use "auto" QoS type (client-side fix).
- **If manual QoS requested but flag disabled:** Internal team must enable `ENABLE_MQOS` flag (server-side fix).
- **Internal Action:** Determine if this is a customer parameter error or missing feature flag, then apply appropriate fix.

#### Conflict Errors (409)
**Check:**
- Does a pool with the same `vendorId` already exist?
- If pool exists in `CREATING` state, can the operation be reused?

**Customer Action Required:**
- Customer must use a unique `resourceId`/`vendorId`, or reuse the existing operation if pool is in `CREATING` state.
- **Internal Action:** Inform customer about the conflict and provide guidance on resolution.

### B. Orchestrator/DB Errors (500, job not created, DB errors)

#### Database Connectivity
**Check:**
- Can the orchestrator service connect to the database?
- Are there connection pool exhaustion errors?
- Are there database migration errors?

**Fix:**
- Verify database is accessible and healthy.
- Check connection pool configuration.
- Apply pending database migrations.
- Retry after transient DB issues resolve.

#### Database Transaction Errors
**Check:**
- Are there transaction deadlock errors?
- Are there unique constraint violations?
- Are there foreign key constraint violations?

**Fix:**
- Retry after a short delay (deadlocks are usually transient).
- Verify pool name/vendor ID is unique.
- Check for orphaned records causing constraint violations.

### C. Workflow/Temporal Startup Errors

#### Temporal Worker Status
**Check:**
- **Via Metrics:** Are Temporal workers running and healthy?
  - Review worker health metrics in monitoring project (worker count, heartbeat status, task processing rate).
- **Via CLI:** Use `tctl --namespace <vcp-namespace> workflow list --limit 10` to verify workers are processing tasks.
- **Via Metrics:** Is the `CustomerTaskQueue` processing tasks?
  - Review task queue metrics in monitoring project (queue depth, processing rate, task completion rate).
- **Via CLI:** Use `tctl --namespace <vcp-namespace> task-queue describe --task-queue CustomerTaskQueue` to check queue status.

**Fix:**
- Restart workers if metrics show they're down or unhealthy.
- Check worker logs for registration errors.
- Verify task queue configuration.

#### Workflow Registration
**Check:**
- **Via CLI:** Is `CreatePoolWorkflow` registered in workers?
  - Use `tctl --namespace <vcp-namespace> workflow describe --workflow-id <workflow_id>` to verify workflow type.
- Are workflow versions compatible?
  - Check worker deployment logs for workflow registration.

**Fix:**
- Ensure workers have the latest workflow code.
- Verify workflow registration in worker startup logs.
- Redeploy workers if workflow registration is missing.

#### Workflow Execution
**Check:**
- **Via Metrics:** Does the workflow start?
  - Review workflow start metrics in monitoring project (workflow initiation rate, workflow count by status).
- **Via CLI:** Use `tctl --namespace <vcp-namespace> workflow describe --workflow-id <workflow_id>` to check workflow status.
- **Via Metrics:** Are activities being executed?
  - Review activity execution metrics in monitoring project (activity start rate, completion rate, duration).
- **Via Metrics:** Are there activity timeout errors?
  - Review activity timeout metrics in monitoring project (timeout count, timeout rate by activity type).

**Fix:**
- **Via CLI:** Use `tctl --namespace <vcp-namespace> workflow show --workflow-id <workflow_id>` to see detailed execution history.
- Verify activity timeouts are sufficient (60m for large pools) - check timeout configuration.
- Check for non-retryable errors causing immediate failure - review error metrics and workflow history.

### D. GCP Resource Provisioning (Timeouts, Permission Errors)

#### VSA Cluster Deployment (VLM)
**Check:**
- Is VLM service available and healthy?
- Is `VSALifecycleManagerQueue` processing tasks?
- Are there sufficient HA pairs available (minimum 6 for large pools)?
- Is the VLM config file (`/config/vmrs_gcp.yaml`) valid?

**Fix:**
- Verify VLM service is running.
- Check VLM queue depth and processing rate.
- Verify instance types are available in the region.
- Validate VLM configuration file.

#### Network Provisioning
**Check:**
- Is Service Networking API enabled?
- Is SN host project configured correctly?
- Are there sufficient network quotas (subnets, IPs, firewall rules)?
- Are firewall rule names conflicting?

**Fix:**
- Enable Service Networking API.
- Verify SN host project configuration.
- Request quota increases if needed.
- Use unique firewall rule names.

#### Compute Resources
**Check:**
- Are there sufficient compute quotas for 6+ HA pairs?
- Are required instance types available in the region?
- Are there zone resource pool exhaustion errors?

**Fix:**
- Request compute quota increases.
- Verify instance types are available.
- Try a different zone if resource pool is exhausted.

#### IAM/Service Account
**Check:**
- Does the service account have required roles?
- Can the service account impersonate other service accounts?
- Are org policies blocking resource creation?

**Fix:**
- Grant required IAM roles to service account.
- Verify service account impersonation permissions.
- Check org policies for restrictions.

#### Storage (if auto-tiering enabled)
**Check:**
- Is Storage API enabled?
- Is project billing active?
- Are there sufficient storage quotas?

**Fix:**
- Enable Storage API.
- Verify billing is active.
- Request storage quota increases if needed.

#### KMS (if configured)
**Check:**
- Is KMS config present and correct?
- Can service account access KMS?
- Is DNS/firewall configured for KMS access?

**Fix:**
- Verify KMS configuration.
- Grant KMS access to service account.
- Configure DNS and firewall for KMS access.

## Step 4: Check for Stuck or Partial State

### Pool Stuck in CREATING State

**Investigation Steps:**
1. **Query Job Status:**
   - Use API: `GET /v1beta/{name}/operations/{operation_id}`
   - Check job state in database: `SELECT state, error_details FROM jobs WHERE uuid = '<job_uuid>'`
   - **Query Temporal workflow:**
     - **Via Metrics:** Review workflow status metrics in monitoring project (workflow status distribution, execution duration).
     - **Via CLI:** Use `tctl --namespace <vcp-namespace> workflow describe --workflow-id <workflow_id>` to check workflow status and execution details.

2. **Inspect Workflow Execution:**
   - **Via Metrics:** Review workflow execution metrics in monitoring project (activity execution status, duration, error rates).
   - **Via CLI:** Use `tctl --namespace <vcp-namespace> workflow show --workflow-id <workflow_id>` to see detailed execution history.
   - Check which activity failed and why (from workflow history).
   - Look for timeout errors or retry exhaustion (check activity duration vs timeout metrics).

3. **Check GCP Operations:**
   - Review GCP Console → Operations for ongoing operations.
   - Check VSA cluster deployment status.
   - Verify network provisioning operations.

4. **Verify Resource State:**
   - Check if partial resources were created (VPCs, subnets, VMs).
   - Identify which resources need cleanup.

**Remediation:**

**First, determine if this is a client-side or server-side issue:**
- **Client-side (400/409):** Inform customer - do not take internal action.
- **Server-side (500/503):** Apply internal remediation (see below).

**If Server-Side Error (500/503):**

- **If Workflow is Active:**
  - Wait for workflow to complete or timeout (60 minutes).
  - **Monitor progress:**
    - **Via Metrics:** Review workflow execution metrics in monitoring project (activity completion, duration trends).
    - **Via CLI:** Use `tctl --namespace <vcp-namespace> workflow describe --workflow-id <workflow_id>` periodically to check status.
  - Do not manually delete resources while workflow is active.

- **If Workflow is Stuck/Failed:**
  - **Identify the failed activity:**
    - **Via Metrics:** Review activity failure metrics in monitoring project (failed activity types, error rates).
    - **Via CLI:** Use `tctl --namespace <vcp-namespace> workflow show --workflow-id <workflow_id>` to see which activity failed.
  - **If safe and approved:** Retry the failed activity via CLI: `tctl --namespace <vcp-namespace> workflow signal --workflow-id <id> --signal-name retry`.
  - **If not safe:** Delete the pool to trigger cleanup and recreate (with change management approval).
  - Document the action taken and rationale.

- **If Resources are Orphaned:**
  - **With approval:** Manually clean up orphaned GCP resources.
  - Delete the pool record from database.
  - Retry pool creation after cleanup.
  - Document cleanup actions for audit trail.

**If Client-Side Error (400/409):**
- **Do not take internal remediation actions.**
- Inform customer about the error and required corrections.
- Provide guidance on correct parameter values.
- Customer must resubmit with corrected parameters.

## Step 5: Escalate or Remediate

### Escalation Criteria
- Issue persists after following all troubleshooting steps.
- Root cause is unclear or requires code changes.
- Multiple pools failing simultaneously (potential system-wide issue).
- Data loss or corruption risk.

### Information to Gather Before Escalation
- **Error Details:**
  - Full error message and stack trace.
  - Tracking ID and correlation ID.
  - Job UUID and workflow ID.
- **Request Details:**
  - Complete request payload (sanitized).
  - Pool name, project, region, zone.
- **System State:**
  - Database pool and job records.
  - Temporal workflow execution details.
  - GCP operation status.
- **Logs:**
  - API logs (google-proxy).
  - Orchestrator logs.
  - Workflow/activity logs (worker).
  - Database logs.
  - VLM logs (if available).
- **Steps Taken:**
  - Document all troubleshooting steps attempted.
  - Note any temporary fixes applied.
  - Record any configuration changes made.

---

## Quick Reference: Common Symptoms & Fixes

| Symptom | Likely Cause | Mitigation |
|---------|--------------|------------|
| 400: "SizeInBytes must be at least 6 TiB" | Pool size below minimum | Increase size to ≥ 6 TiB |
| 400: "SizeInBytes must be less than 2.48 PiB" | Pool size exceeds limit (no auto-tiering) | Reduce size or enable auto-tiering for 20 PiB limit |
| 400: "TotalThroughputMibps must be between 64 and 60000" | Throughput out of range | Adjust to 64-60,000 MiBps |
| 400: "TotalIops must be at least [X] IOPS" | IOPS below minimum (16× throughput) | Increase IOPS to at least 16× throughput |
| 400: "HotTierSizeInBytes must be between 6 TiB" | Hot tier size below minimum | Increase hot tier size to ≥ 6 TiB |
| 400: "Given pool size must be a multiple of 1 GiB" | Size not aligned to 1 GiB | Round size to nearest 1 GiB multiple |
| 400: "QoS type not supported" | Invalid QoS type | Use "auto" or enable `ENABLE_MQOS` for "manual" |
| 409: Pool exists | Duplicate vendorId | Use unique resourceId or reuse operation |
| 500: DB errors | Connectivity, schema | Check DB, retry after fix |
| Workflow not starting | Temporal/worker | Check Temporal, worker logs |
| Workflow timeout (60m) | Operation taking too long | Check GCP operations, consider increasing timeout |
| VLM queue full | VLM service overloaded | Wait for queue to drain, scale VLM if needed |
| Insufficient HA pairs | Less than 6 HA pairs available | Ensure minimum 6 HA pairs, check `NUMBER_OF_HA_PAIRS_LARGE_CAPACITY` |
| GCP quota exceeded | Resource limits reached | Request quota increases, free up resources |
| Service account permissions | Missing IAM roles | Grant required roles to service account |
| Pool stuck in CREATING | Workflow failure | Inspect logs, retry or cleanup |

---

## Large Pool Specific Configuration Reference

### Size Limits
- **Minimum:** 6 TiB (cool tier capacity)
- **Maximum (no auto-tiering):** 2.48 PiB (hot tier capacity)
- **Maximum (with auto-tiering):** 20 PiB (total pool capacity)
- **Granularity:** Must be multiple of 1 GiB

### Performance Limits
- **Throughput:** 64 - 60,000 MiBps
- **IOPS:** 1,024 - 960,000 IOPS
- **IOPS/Throughput Ratio:** Minimum 16 IOPS per MiBps

### Auto-Tiering Limits (if enabled)
- **Hot Tier Minimum:** 6 TiB
- **Hot Tier Maximum:** Must be less than total pool size

### Infrastructure Requirements
- **Minimum HA Pairs:** 6 (configurable via `NUMBER_OF_HA_PAIRS_LARGE_CAPACITY`)
- **Workflow Timeout:** 60 minutes (vs 55m for regular pools)
- **VLM Deployment Timeout:** 45 minutes

### Feature Flags
- `AUTO_TIERING_ENABLED` - Must be `true` for auto-tiering
- `ENABLE_MQOS` - Must be `true` for manual QoS
- `REGIONAL_SUPPORT_ENABLED` - Required for regional pools

---

## Accessing Temporal Workflow Information

**Note:** Temporal Web UI is not directly exposed. Use the following methods to access workflow information:

### Method 1: Temporal CLI (tctl)

**Prerequisites:**
- `tctl` CLI tool available locally or accessible from admintools pod
- Access to the Temporal namespace
- Workflow ID (same as job UUID from database)

**Common Commands:**

1. **Describe Workflow Status:**
   ```bash
   tctl --namespace <vcp-namespace> workflow describe --workflow-id <workflow_id>
   ```
   - Shows workflow status, execution time, and basic information
   - Use this to check if workflow is running, completed, or failed

2. **Show Workflow History:**
   ```bash
   tctl --namespace <vcp-namespace> workflow show --workflow-id <workflow_id>
   ```
   - Shows detailed execution history with all activities
   - Use this to see which activity failed and why
   - Shows activity inputs, outputs, and errors

3. **List Workflows:**
   ```bash
   tctl --namespace <vcp-namespace> workflow list --query 'WorkflowType="CreatePoolWorkflow"'
   ```
   - Lists workflows matching the query
   - Use this to find workflows by type or status

4. **Query Workflow Status:**
   ```bash
   tctl --namespace <vcp-namespace> workflow query --workflow-id <workflow_id> --query-type "status"
   ```
   - Queries workflow for custom status information
   - Use this if workflow has a status query handler

5. **Signal Workflow (if needed):**
   ```bash
   tctl --namespace <vcp-namespace> workflow signal --workflow-id <workflow_id> --signal-name <SignalName> --input '"payload"'
   ```
   - Sends a signal to a running workflow
   - Use with caution and proper approval

6. **Cancel/Terminate Workflow (operator-only, use caution):**
   ```bash
   tctl --namespace <vcp-namespace> workflow cancel --workflow-id <workflow_id>
   tctl --namespace <vcp-namespace> workflow terminate --workflow-id <workflow_id> --reason "reason"
   ```
   - Use only with proper approval and change management
   - Document the reason for cancellation/termination

**Using from admintools pod (for restricted clusters):**
```bash
kubectl -n <ops-namespace> exec -it deploy/admintools -- /bin/sh -c "tctl --namespace <vcp-namespace> workflow describe --workflow-id <workflow_id>"
```

### Method 2: Monitoring Metrics (Recommended)

**Location:** Monitoring project (GCP Monitoring)

**Available Metrics:**
- **Workflow Execution Metrics:**
  - Workflow status distribution (RUNNING, COMPLETED, FAILED, TIMED_OUT)
  - Workflow execution duration
  - Workflow start/completion rates
  - Workflow error rates by type

- **Activity Execution Metrics:**
  - Activity execution status (SUCCESS, FAILED, TIMED_OUT)
  - Activity duration by type
  - Activity retry counts
  - Activity heartbeat status

- **Worker Health Metrics:**
  - Worker count and health status
  - Task processing rate
  - Task queue depth
  - Worker heartbeat status

- **Task Queue Metrics:**
  - Queue depth by task queue name
  - Task processing rate
  - Task completion rate
  - Task failure rate

**How to Access:**
1. Navigate to GCP Monitoring project
2. Use metric explorer or create custom dashboards
3. Filter by:
   - `workflow_type` = "CreatePoolWorkflow"
   - `job_type` = "CREATE_LARGE_POOL"
   - `workflow_id` = "<workflow_id>"
   - `correlation_id` = "<correlation_id>"

**Key Metrics to Monitor:**
- `temporal_workflow_execution_duration` - How long workflows take
- `temporal_workflow_status` - Current workflow status
- `temporal_activity_execution_status` - Activity success/failure
- `temporal_worker_health` - Worker availability
- `temporal_task_queue_depth` - Queue backlog

### Method 3: Database Queries

**Job Status:**
```sql
SELECT uuid, state, error_details, tracking_id, workflow_id, created_at, updated_at 
FROM jobs 
WHERE uuid = '<job_uuid>' OR workflow_id = '<workflow_id>';
```

**Workflow ID Lookup:**
- Workflow ID = Job UUID (from `jobs` table)
- Use workflow ID to query Temporal via CLI or metrics

## Operational Readiness Checklist

### Pre-Creation Verification
- [ ] Pool size is between 6 TiB and 2.48 PiB (or 20 PiB with auto-tiering)
- [ ] Size is a multiple of 1 GiB
- [ ] Throughput is between 64 and 60,000 MiBps
- [ ] IOPS is between 1,024 and 960,000 IOPS
- [ ] IOPS is at least 16× throughput
- [ ] If auto-tiering: hot tier size is ≥ 6 TiB and < pool size
- [ ] QoS type is "auto" or "manual" (if `ENABLE_MQOS=true`)
- [ ] `largeCapacity: true` is set in request

### Infrastructure Verification
- [ ] Required GCP APIs enabled (Service Networking, Compute, IAM, Storage, DNS, Secret Manager, KMS)
- [ ] Service accounts have correct roles
- [ ] SN host project configured
- [ ] Temporal workflows and workers registered
- [ ] DB migrations applied and healthy
- [ ] VLM service available and queue operational
- [ ] Sufficient GCP quotas (compute, network, storage)
- [ ] Minimum 6 HA pairs available
- [ ] Required instance types available in region

### Configuration Verification
- [ ] Environment variables for large pool limits are set correctly
- [ ] `START_TO_CLOSE_WORKFLOW_TIMEOUT_LV` is set to 60m (or appropriate value)
- [ ] `NUMBER_OF_HA_PAIRS_LARGE_CAPACITY` is set to 6 (or appropriate value)
- [ ] Feature flags are enabled as needed (`AUTO_TIERING_ENABLED`, `ENABLE_MQOS`)
- [ ] VLM config file (`/config/vmrs_gcp.yaml`) is valid

---

**Tip:**  
For any error, always check the logs for the specific component (API, Orchestrator, Workflow, DB, VLM) and correlate with the operation/job ID and correlation ID for targeted debugging. Large pools have stricter requirements and longer timeouts than regular pools, so ensure all parameters meet the large pool specifications.

---

# Useful Tools and Resources

* **Monitoring System:** [GCP Monitoring](https://console.cloud.google.com/monitoring)
  - Temporal workflow and activity metrics
  - Worker health and task queue metrics
  - Filter by `workflow_type`, `job_type`, `workflow_id`, or `correlation_id`
* **Logging Platform:** [GCP Logging](https://console.cloud.google.com/logs)
  - API logs (google-proxy service)
  - Orchestrator logs (core-api service)
  - Workflow/activity logs (worker service)
  - Filter by `correlation_id` or `workflow_id`
* **Temporal CLI (tctl):**
  - **Reference:** `doc/guides/temporal-debugging.md`
  - **Common Commands:**
    - `tctl --namespace <vcp-namespace> workflow describe --workflow-id <id>` - Get workflow status
    - `tctl --namespace <vcp-namespace> workflow show --workflow-id <id>` - Get detailed execution history
    - `tctl --namespace <vcp-namespace> workflow list --query 'WorkflowType="CreatePoolWorkflow"'` - List workflows
  - **Access:** Via local `tctl` or admintools pod: `kubectl exec -it deploy/admintools -- tctl ...`
* **Temporal Metrics:**
  - Available in monitoring project
  - Workflow execution metrics (status, duration, error rates)
  - Activity execution metrics (status, duration, retry counts)
  - Worker health metrics (worker count, heartbeat status)
  - Task queue metrics (queue depth, processing rate)
* **Troubleshooting Guide:** https://confluence.ngage.netapp.com/spaces/VSCP/pages/1273328576/Pool+Volume+CRUD+Operations+Troubleshooting+Guide
