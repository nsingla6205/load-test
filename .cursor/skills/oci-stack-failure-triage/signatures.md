# OCI Stack Failure Signature Catalog (oci-proxy + vcp-worker)

Known failure signatures for the OCI control plane. Each entry: the service whose
log carries it, the log signature to match, the root cause, and the checks that
confirm/fix it.

**vlm-worker runtime signatures (workflows/activities) live in
[../vlm-failure-triage/signatures.md](../vlm-failure-triage/signatures.md)** —
match vlm-worker runtime lines there, not here. This file also holds vlm-worker
**rollout** signatures (Helm hook Jobs such as `vlm-worker-versioning-*`), because
those come from this repo's deployment tooling, not from VLM workflows. Do not
duplicate entries across files.

Grow this file as new failures are diagnosed. Keep root cause distinct from
downstream symptoms, and always record which service's log the signature
appears in.

---

## Cross-service: vlm-worker failure surfaced in vcp-worker (symptom)

**Service log**: `vcp-worker`

**Signature**
```
ociCreatePoolWorkflow failed ... error="child workflow execution error
(type: CreateVSAClusterDeploymentWorkflow, workflowID: <id>, runID: <id>, ...): ..."
```
Also `ociUpdatePoolWorkflow failed`, `error in ociDeletePoolWorkflow`, or any
`child workflow execution error (type: <VSA...Workflow>` (`CreateVSASVMWorkflow`,
`DeleteVSAClusterDeploymentWorkflow`, `UpdateVSAClusterDeploymentWorkflow`, ...).

**Root cause**
Not a vcp-worker root cause. vcp-worker runs the VLM workflow as a child on the
`vsa-lifecycle-manager-<ontapVersion>` task queue
(`clients/vlm/vlm_workflow_client.go`); the child failed inside vlm-worker.

**Action**
Take the `workflowID` from the error, find it in the vlm-worker log, and triage
the earliest vlm-worker error with the vlm catalog. Report **vlm-worker** as the
failing service, vcp-worker as propagation.

---

## Cross-service: VLM child workflow with no vlm-worker lines (unconfirmed)

**Service log**: `vcp-worker`

**Signature**
vcp-worker shows a timeout or failure for a `child workflow execution error
(type: <VSA...Workflow>, workflowID: <id> ...)`, but the provided vlm-worker log
has **no lines** for that `workflowID`.

**Root cause**
**Unknown until confirmed.** Missing lines alone prove nothing: the log may be a
500-line tail, from another vlm-worker replica, or rotated. Only after the checks
below show the child never started may you conclude that no vlm-worker polled the
queue (version mismatch between the pool's ONTAP version and the deployed
vlm-worker, or vlm-worker down). If the history shows the child ran, get the full
logs from every vlm-worker replica and triage it as a vlm-worker failure.

**Checks**
1. `temporal workflow describe --workflow-id <id>` — did the child start, and on which task queue?
2. `temporal task-queue describe --task-queue vsa-lifecycle-manager-<version>` — any pollers?
3. Pods: `kubectl -n vcp get pods -l app=vlm-worker-<version>` where `<version>` is the
   chart's lower-case dashed form (e.g. `vlm-worker-9-19-1p1`), or list all with
   `kubectl -n vcp get pods | grep vlm-worker-`. Locally: `get_logs(service="vlm-worker")`.
4. vlm-worker startup line `Starting Temporal worker ... taskQueue=vsa-lifecycle-manager-<version>`
   — the version must match the child's queue.

---

## vcp-worker: could not resolve a VLM worker version (pool BuildInfo)

**Service log**: `vcp-worker`

**Signature**
```
OCI pool update stopped: could not resolve a VLM worker version
error="could not resolve a VLM worker version to route the OCI pool update; buildInfo=..."
```

**Root cause**
vcp-worker failure, before any VLM child workflow is scheduled:
`workflows.ResolveVLMWorkerVersion(pool)` returned empty and the update exits with
`ErrMissingRequiredInputError` (`core/orchestrator/workflows/oci/pool_workflows.go`).
The pool's stored `BuildInfo` is missing or invalid, so no task queue was chosen.
Do not inspect vlm-worker queues or pods for this.

**Checks**
1. The `buildInfo=` value in the error — is the ONTAP / VLM version empty or malformed?
2. The pool row's `BuildInfo` in the DB; compare with a healthy pool of the same release.

---

## vlm-worker rollout: Worker Deployment not found (Helm hook)

**Service log**: `vlm-worker` rollout Job (`vlm-worker-versioning-ensure-drain`)

**Signature**
```
ERROR ensure-drain failed error="read cutover state for \"vsa-lifecycle-manager-<ver>\":
describe deployment \"vsa-lifecycle-manager-<ver>\": Worker Deployment not found"
Error: UPGRADE FAILED: pre-upgrade hooks failed: resource Job/vcp/vlm-worker-versioning-ensure-drain not ready
```

**Root cause**
First versioned upgrade on a queue that only had unversioned workers: Temporal
has not created the Worker Deployment yet, and `workerdeployment.CurrentBuildID`
returns `NotFound` as an error, so the pre-upgrade `ensure-drain` Job exits 1.
The post-upgrade Jobs can hit the same error if they start before the first
versioned worker polls.

**Action**
The failed upgrade changed nothing (the hook runs before Helm applies the workers).
Fix in `workflow_engine/temporal/workerdeployment`: make `CurrentBuildID` return
`""` for `serviceerror.NotFound`, the same value it returns for an unversioned
deployment (all callers already handle `""`). Keep other errors (unavailable,
permission) fatal. Re-run the upgrade after the fix ships.

---

## oci-proxy: dirty database migration on start

**Service log**: `oci-proxy`

**Signature**
```
Failed to initialize database ... error="... Dirty database version <N>. Fix and force version."
```
Pod then exits (`os.Exit(1)` in `oci-proxy/app.go`) and crash-loops.

**Root cause**
oci-proxy migrates on start (`RUN_MIGRATION_ON_START=true`). A previous run of
migration `<N>` failed half-way and left `schema_migrations.dirty=true`; every
later start refuses to run.

**Checks**
1. Find the first failure of migration `<N>` in an **earlier** oci-proxy log (the
   SQL error, e.g. `42P16` on `CREATE OR REPLACE VIEW`) — that is the root cause.
2. Reproduce with the `oci-migration-regression` skill against a prod-like snapshot.
3. Fix the migration, then clear the dirty flag per the DB runbook (needs approval;
   do not force the version without understanding the partial apply).

---

## oci-proxy / vcp-worker: database unreachable

**Service log**: `oci-proxy` or `vcp-worker`

**Signature**
```
Failed to connect to the database, retrying...      (oci-proxy)
Failed to initialize database                        (oci-proxy, exits)
Failed to get database connection                    (vcp-worker, exits)
```

**Root cause**
Postgres not reachable or credentials wrong (host/port, secret, network).

**Checks**
1. DB host/port and credentials in the chart secret/configMap
   (`kubernetes/OCI/oci-proxy`, `kubernetes/OCI/vcp-worker-chart`).
2. NSG/security-list between the OKE node subnet and the OCI DB system.
3. Locally: `status` / `get_logs(service="postgres")` on `oci-infra`.

---

## oci-proxy / vcp-worker: Temporal unreachable

**Service log**: `oci-proxy` or `vcp-worker`

**Signature**
```
Failed to initialize Temporal client ... error="... connection refused | context deadline exceeded | x509: ..."
```

**Root cause**
Temporal frontend address/port, namespace, or mTLS certs wrong; or Temporal is down.

**Checks**
1. Temporal frontend host/port and namespace in the service configMap.
2. `x509` in the error → client cert/CA secret (`kubernetes/OCI/scripts/create-vcp-secrets-from-temporal.sh`).
3. Locally: `get_logs(service="temporal")`.

---

## vcp-worker: OCI startup configuration invalid

**Service log**: `vcp-worker`

**Signature** (any, at startup; worker exits)
```
Invalid OCI client certificate configuration
Invalid OCI service certificate configuration
Certificate lifetime validation failed
Failed to initialize OCI clients at startup
OCI worker startup environment validation failed
Unsupported cloud provider, cannot initialize VLM provider factory
```

**Root cause**
Missing/invalid OCI env vars, certificates, or provider setting in the vcp-worker
deployment. The `error=` field names the exact variable/cert.

**Checks**
1. Compare the vcp-worker configMap/secret against `kubernetes/OCI/vcp-worker-chart/values.yaml`
   and the active overrides file.
2. Cert expiry/format for the cert named in the error.

---

## vcp-worker: OCI API error (OCI SDK)

**Service log**: `vcp-worker` (also seen in vlm-worker for compute/disk calls)

**Signature**
```
Error returned by <Service> Service. Http Status Code: <code>. Error Code: <code>.
Opc request id: <id>. Message: <msg> Operation Name: <op>
```

**Root cause by code**
- `404 NotAuthorizedOrNotFound` — IAM policy missing for the dynamic group /
  principal, wrong OCID, or wrong compartment.
- `401 NotAuthenticated` — bad API key / instance-principal setup.
- `409 Conflict` / `IncorrectState` — resource busy or in a transitional state; usually retried.
- `429 TooManyRequests` — tenancy throttling; see the vlm catalog entry (usually transient).
- `5xx InternalServerError` — OCI-side; retry and check OCI service health.

**Checks**
1. Look up the `Opc request id` with OCI support / audit logs for the exact denial.
2. For 404: IAM policies on the compartment named in the request.

---

## vcp-worker: ONTAP credentials from OCI Vault

**Service log**: `vcp-worker`

**Signature**
```
Failed to fetch ONTAP credentials from OCI Vault
```

**Root cause**
Vault secret missing/renamed or the worker principal lacks `read secret-bundles`
on the vault compartment. The wrapped `error=` usually carries the OCI SDK code.

**Checks**
1. Secret exists and is ACTIVE in the vault referenced by the pool.
2. IAM policy for the worker principal on that vault/compartment.

---

## vcp-worker: activity timeout (usually a symptom)

**Service log**: `vcp-worker`

**Signature**
```
Activity timed out operation=<op> resource=<res> error="... activity (ScheduleToClose|StartToClose) timeout"
```

**Root cause**
Mapped to `ErrWorkflowSupervisorTimeout` by `handleActivityError`
(`core/orchestrator/workflows/oci/pool_workflows.go`). Look for an earlier error
from the same activity (retries) or from the dependency it waits on (DB, OCI API,
ONTAP). Only call the timeout the root cause if nothing earlier explains it.

---

## vcp-worker: pool update left cluster in mixed-version state

**Service log**: `vcp-worker`

**Signature**
```
OCI pool update VLM batch failed; cluster is in mixed-version state
Batched OCI pool update failed
```

**Root cause**
One VLM batch of the rolling update failed. The origin is the vlm-worker
`UpdateVSAClusterDeploymentWorkflow` / `UpgradeVSAClusterDeploymentWorkflow`
child — trace it into the vlm-worker log.

---

## oci-proxy: request rejected before dispatch

**Service log**: `oci-proxy`

**Signature**
```
Opc-Request-Id header is required          (HTTP 400)
Opc-Work-Request-Id header is required     (HTTP 400)
idempotency lookup failed
```

**Root cause**
Client did not send the required OCI headers on a mutating call, or the
idempotency DB lookup failed (check for a DB error just before it). No workflow
was started, so vcp-worker/vlm-worker will have no lines for this request.

---

## oci-proxy: background schedules not ensured (non-fatal)

**Service log**: `oci-proxy`

**Signature**
```
Failed to ensure OCI certificate rotation schedule
Failed to ensure OCI temp-password verification schedule
```

**Root cause**
Temporal schedule create/update failed at startup. Non-fatal: the API keeps
serving. Not the cause of a pool/SVM failure unless the failure is in cert
rotation or temp-password verification.

---

## Template for new entries

```markdown
## <service>: <short title>

**Service log**: `<oci-proxy | vcp-worker>`

**Signature**
\`\`\`
<verbatim log fragment>
\`\`\`

**Root cause**
<what actually went wrong>

**Checks**
1. <check>
```
