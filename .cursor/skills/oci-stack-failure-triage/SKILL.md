---
name: oci-stack-failure-triage
description: Diagnose OCI VSA failures across oci-proxy, vcp-worker, and vlm-worker from log files the user provides (individual .log/.txt paths, a folder, or a .zip/.tar.gz bundle) or from live oci-infra MCP logs, and report which service's log the failure came from. Use when an OCI pool/SVM/cluster/upgrade operation fails, when the user shares OCI log files or a zip of logs, or asks whether oci-proxy, vcp-worker, or vlm-worker failed.
---

# OCI Stack Failure Triage (oci-proxy + vcp-worker + vlm-worker)

Common triage for the OCI control plane. Builds on `/vlm-failure-triage`: the
vlm-worker signature catalog is reused as-is, and this skill adds oci-proxy and
vcp-worker plus cross-service attribution. No helper scripts: read the logs
directly with Read / Grep, choosing the strategy by file size.

## Request flow and linking IDs

```
client -> oci-proxy (HTTP 202, starts workflow) -> vcp-worker (customer-workflows queue)
       -> vlm-worker (child workflow on vsa-lifecycle-manager-<ontapVersion>)
```

| ID | Where it appears |
|---|---|
| `opc-request-id` → `requestCorrelationID` | request correlation; all three services (oci-proxy request, vcp-worker `requestFields`, vlm-worker top-level) |
| `opc-work-request-id` → `requestOpcWorkRequestID` | a **separate** header; oci-proxy uses it as the vcp-worker workflow ID (`createPoolParams.WorkflowID = opcWorkRequestID` in `oci-proxy/api/endpoints/pool_endpoints.go`) |
| vcp-worker workflow ID | `workflowID` in vcp-worker `requestFields` (= `opc-work-request-id`); oci-proxy `GET /v1beta/workRequests/<id>` polling |
| VLM child workflow ID | `workflowID: <id>` inside vcp-worker `child workflow execution error (...)` = `WorkflowID` in vlm-worker |
| `deploymentID` / `clusterID` / pool OCID | vlm-worker (`deploymentID`), vcp-worker (`clusterID`), oci-proxy (`traceURL` pool OCID) |

oci-proxy usually logs no error for async failures: it returns `202`, then the
client polls `GET /v1beta/workRequests/<id>` and the final response body carries
`"status":"failed"` with the vcp-worker error. Async failures originate in
vcp-worker or vlm-worker.

Clients may send the same value in both headers, but do not assume it: pivot on
each ID under its own field (`requestCorrelationID` vs `requestOpcWorkRequestID` /
`workflowID`).

## Step 1 — Gather the files

- **Archive**: extract to a fresh temp dir, quoting the path (user paths often
  contain spaces and parentheses). Use the command for the format:
  ```bash
  D=$(mktemp -d /tmp/oci-triage.XXXX)
  unzip -q -o "<file.zip>" -d "$D"                        # .zip
  tar xzf "<file.tar.gz|file.tgz>" -C "$D"                # .tar.gz / .tgz
  tar xf "<file.tar>" -C "$D"                             # .tar
  gunzip -c "<file.log.gz>" > "$D/$(basename "<file.log.gz>" .gz)"   # single-file .gz
  ```
  Expand nested archives the same way. Skip `__MACOSX/` and dotfiles.
- **Folder**: use the folder path as is.
- **Individual files**: use as given. `SERVICE=PATH` from the user forces attribution.
- **Pasted text**: analyze inline.
- **Nothing given**: call `get_logs` on the `oci-infra` MCP server for `oci-proxy`,
  `vcp-worker`, `vlm-worker` (tail 500 each; `status` first if unsure).

Then size every input file in one call, using the path for each input type:
```bash
find "$D" -type f -not -path '*/__MACOSX/*' -exec wc -lc {} +          # extracted archive
find "<folder>" -type f -not -name '.*' -exec wc -lc {} +              # folder
wc -lc "<file1>" "<file2>"                                              # individual files
```
Delete `$D` when the report is done (only if you created it).

**Coverage**: note whether each log is complete or partial (an MCP tail, one
replica of several, rotated). Absence of lines in a partial log proves nothing.

## Step 2 — Attribute each file (or line) to a service

Check in this order; stop at the first match. Never guess — if nothing matches,
ask the user which service the file is.

1. User override `SERVICE=PATH`.
2. Filename: `oci-proxy*` → oci-proxy; `vcp-worker*`, `vcp-customer-worker*`,
   `vcp-background-worker*` → vcp-worker; `vlm-worker*`, `*lifecycle-manager*` → vlm-worker.
   A bare `worker.log` / `0.log` is decided by content.
   Helm hook Job logs (`vlm-worker-versioning-*`, e.g. `ensure-drain`) are the
   **vlm-worker rollout**, not vlm-worker runtime — route them as rollout (Step 6).
3. Line prefix (combined logs — attribute **per line**):
   `[pod/<pod>/<container>] ...` (kubectl `--prefix`) or `<svc>-1  | ...` (docker compose).
   Map the pod/container name with the filename rules above.
4. Content fingerprint (Grep the file with `head_limit` ~5):

| Service | Fingerprints |
|---|---|
| oci-proxy | `"message":"Request - /v1beta/`, `"message":"Response - `, `"httpRequest":{`, `Starting OCI proxy API`, `Starting HTTP server on` |
| vcp-worker | `"requestFields":{` together with `"jobUUID"`/`"workflowID"` and no HTTP `Request -` lines, `Starting worker [taskQueue =`, `registering OCI customer workflows` |
| vlm-worker | `"TaskQueue":"vsa-lifecycle-manager-`, `"deploymentID":"`, `Starting VLM Worker`, logrus `time="..." level=` |

## Step 3 — Read by file size

Pick per file from Step 1's `wc` output:

| Size | Strategy |
|---|---|
| **Small** — ≤ 1,500 lines and ≤ 1 MB | Read the whole file. |
| **Medium** — ≤ 20,000 lines or ≤ 10 MB | Grep for errors/warnings (pattern below) with `-n`; then Read ±40 lines around the **first** error and around the terminal error. |
| **Large** — bigger than medium | Grep `output_mode: count` for errors first. Then Grep only lines carrying the request's IDs (correlation / workflow / deploymentID) with `head_limit` ≤ 200, and Read small windows (±30 lines) around the first and last error. Never Read the whole file. |

Lines can be very long (full HTTP bodies, OCI SDK dumps). If a Read window is
huge, narrow with Grep on `"message":"[^"]{0,200}` or Read fewer lines.

**Error/warning pattern** (Grep, case-insensitive):
```
"(severity|level)":"(error|fatal|panic|critical|warn|warning)"|level=(error|fatal|panic|warn|warning)|panic:|fatal error:
```
Use only `error|fatal|panic|critical` when warnings are noisy.

**Terminal-state pivots**:
- oci-proxy: `"status":5\d\d` (HTTP 5xx), `"status":4\d\d`, `status\\":\\"failed` (work-request poll result).
- vcp-worker: `status":"FAILED`, `child workflow execution error`, `Workflow failed`, `phase failed`.
- vlm-worker: the last `"severity":"error"` for the top-level VLM `WorkflowType`.

Timestamps: prefer the JSON `timestamp`/`time` field; the kubectl prefix time is the
log-collection time (nanosecond precision, usually equal). Compare across services in UTC.

## Step 4 — Pick the failing service

Use the earliest *originating* error on the request path, not the last place it surfaced:

- vcp-worker `child workflow execution error (type: <VLM workflow>, workflowID: X ...)`
  → find `X` in vlm-worker. If vlm-worker has an earlier error for that workflow
  (or its children), **vlm-worker** failed; vcp-worker only propagated it.
- vcp-worker child error but the provided vlm-worker log has no lines for `X` →
  **Unknown**, not a vlm-worker deployment issue yet. The log may be a tail, from
  another replica, or rotated. Confirm with the workflow history of `X` or the
  task-queue pollers (see catalog) before naming a cause.
- vcp-worker `could not resolve a VLM worker version` → **vcp-worker** (pool
  `BuildInfo`); no child workflow was started, so do not look at vlm-worker.
- vcp-worker error with no child-workflow wrapper (OCI SDK error, Vault, DB,
  validation) → **vcp-worker**.
- Only oci-proxy has the failure (4xx/5xx on the request, `idempotency lookup failed`,
  startup/DB) → **oci-proxy**; the other services should have no lines for it.
- Startup errors (DB, Temporal, certs) → the service that logged them.

Inside vlm-worker, walk the chain down to the innermost activity: the
`activity error (type: <Activity>...)` text names it, and the first error line of
that activity is the root-cause line. Repeated identical errors at a fixed interval
are the activity retrying — cite the first and the last (count them).

## Step 5 — Separate root cause from symptoms

Timeouts (`ScheduleToClose`, `Activity timed out`), rollback workflows
(`DeleteVSAClusterDeploymentWorkflow`), `...Workflow failed` / `phase failed`
wrappers, and generic `An error occurred during VLM workflow execution` are
symptoms of an earlier error.

## Step 6 — Match signatures

- vlm-worker runtime lines (workflows/activities) → [../vlm-failure-triage/signatures.md](../vlm-failure-triage/signatures.md)
- oci-proxy / vcp-worker / cross-service / vlm-worker rollout (Helm hook Jobs) → [signatures.md](signatures.md)

Unknown signature: say so, quote the failing workflow/activity and error verbatim,
give hypotheses labelled `Hypothesis`, and do not guess a root cause. Once
root-caused, add an entry to the right catalog (vlm-worker → vlm catalog; others →
this catalog).

## Output template

```markdown
## Failing service
**<oci-proxy | vcp-worker | vlm-worker>** — `<file>:<line>` (<timestamp>)
Operation: <e.g. mediator upgrade, pool create> · Correlation: `<opc-request-id>`
Propagation: <e.g. vlm-worker -> vcp-worker (child workflow error) -> oci-proxy (workRequest status failed)>

## Root cause
<one or two sentences; say "Unknown signature" if no catalog entry matches>

## Evidence
- [<service>] `<file>:<line>` <timestamp> <verbatim log fragment>
- [<service>] `<file>:<line>` <timestamp> <verbatim log fragment> (symptom)

## OCI checks to run
1. <specific OCI-side check>

## Suggested next step
<the single most useful action>
```

Rules:
- `Failing service` is exactly one service. If the logs cannot decide, write
  `Unknown` and list what is missing (e.g. "vlm-worker log not provided").
- Evidence oldest first, 3–6 lines, each tagged with service and `file:line`;
  mark downstream effects `(symptom)`.
- If a service's log was not provided, say so in `Propagation` instead of
  assuming it was clean.
- Mark anything not shown in the logs as `Hypothesis` or `Unknown`.

## Examples

```
/oci-stack-failure-triage ~/Downloads/oci-logs.zip
/oci-stack-failure-triage /tmp/oci-proxy.log /tmp/vcp-worker.txt /tmp/vlm-worker.log
/oci-stack-failure-triage vcp-worker=/tmp/worker-0.log vlm-worker=/tmp/worker-1.log
/oci-stack-failure-triage                      # live: get_logs for all three services
```

### Example — VLM failure surfaced through vcp-worker

Input: `oci-logs.zip` with `oci-proxy.log`, `vcp-worker.txt`, `0.log` (vlm, by content).

```markdown
## Failing service
**vlm-worker** — `0.log:812` (2026-05-19T10:41:43Z)
Operation: pool create · Correlation: `opc-123`
Propagation: vlm-worker -> vcp-worker (child workflow error) -> oci-proxy (workRequest status failed)

## Root cause
HA interconnect failure: the even node can't reach its partner over the `ic`
LIF, so ONTAP never enables storage failover and the deployment rolls back.

## Evidence
- [vlm-worker] `0.log:812` 10:41:43 Failed to enable storage failover service. Reason: Cannot access partner via HA interconnect.
- [vlm-worker] `0.log:1460` 10:54:47 WaitForClusterHAReadyActivity ... ScheduleToClose timeout (symptom)
- [vcp-worker] `vcp-worker.txt:2210` 10:58:00 ociCreatePoolWorkflow failed ... child workflow execution error (type: CreateVSAClusterDeploymentWorkflow ...) (symptom)

## OCI checks to run
1. NSG/security-list on the `ic` subnet between ic_local_ip and ic_partner_ip.
2. Source/dest check disabled on the `ic` VNICs.

## Suggested next step
Run OCI Network Path Analyzer ic_local_ip -> ic_partner_ip; fix the ic-subnet
NSG rule, then redeploy.
```

### Example — oci-proxy startup failure

```markdown
## Failing service
**oci-proxy** — `oci-proxy.log:14` (2026-05-19T10:30:00Z)
Operation: service startup · Correlation: n/a
Propagation: oci-proxy only (crash-loop; no workflows dispatched)

## Root cause
oci-proxy refuses to start because migration 40 is marked dirty in
`schema_migrations`; an earlier run of that migration failed half-way.

## Evidence
- [oci-proxy] `oci-proxy.log:14` 10:30:00 Failed to initialize database error="Dirty database version 40. Fix and force version."

## OCI checks to run
1. Earlier oci-proxy log for the first failure of migration 40 (the SQL error).

## Suggested next step
Reproduce with the `oci-migration-regression` skill on a prod-like snapshot, fix the migration, then clear the dirty flag per the DB runbook.
```
