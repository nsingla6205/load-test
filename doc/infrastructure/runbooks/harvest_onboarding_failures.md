# Runbook for harvest_onboarding_failures

Harvest registration failures during pool onboarding. Alert metric: `vcp_job_status_updates`,
state `failed to register nodes to harvest farm`, usually activity `UploadHarvestTemplate`.

**Note:** `CREATE_POOL` is often **DONE**. Pool may be **READY**; pollers/metrics may be missing.
Harvest errors are **Prometheus-only** — not in `jobs.error_details`.

# Alert Information

| Field | Description |
| :---: | :--- |
| Alert Name | harvest_onboarding_failures |
| Metric | `vcp_job_status_updates` — state `failed to register nodes to harvest farm` |
| Typical failure | `UploadHarvestTemplate` → HTTP **503** on Harvest Farm `/config/upload` |
| Workflow | `RegisterNodeToHarvestFarmWorkflow` (child of `CreatePoolWorkflow`) |
| Severity | Warning / Error |

# Debugging Steps (Guidelines)

1. Acknowledge the alert and note timestamp (UTC).
2. **Collect context** (account, pool, child workflow, TaskQueue).
3. Match findings to a **failure scenario** below and follow its steps.
4. Verify pollers on Harvest Farm; document and escalate if needed.

---

# Collect Context

Gather these before choosing a failure scenario. Inputs: metric `project_id`, incident time window (UTC).

**Account**

```sql
SELECT id, uuid, name FROM accounts WHERE name = '<CUSTOMER_PROJECT_NUMBER>';
```

**CREATE_POOL job** (`state = DONE` is normal)

```sql
SELECT j.workflow_id, j.state, j.correlation_id, j.resource_uuid AS pool_uuid,
       j.resource_name AS pool_name, j.created_at
FROM jobs j
WHERE j.account_id = <ACCOUNT_ID>
  AND j.type IN ('CREATE_POOL', 'CREATE_LARGE_POOL')
  AND j.created_at >= '<START_UTC>' AND j.created_at < '<END_UTC>'
ORDER BY j.created_at DESC;
```

**Child workflow** (deterministic id — avoid broad `workflow list`)

```text
register-node-to-harvest-farm-<POOL_UUID>-<ACCOUNT_ID>
```

```bash
temporal workflow describe --namespace default \
  --workflow-id "register-node-to-harvest-farm-<POOL_UUID>-<ACCOUNT_ID>"

temporal workflow show --namespace default \
  --workflow-id "register-node-to-harvest-farm-<POOL_UUID>-<ACCOUNT_ID>"
```

Save **`TaskQueue`** from describe. Use that value in later steps — do not copy queue names from other environments.

**Route to a failure scenario**

| `workflow show` shows | Scenario |
|----------------------|----------|
| `UploadHarvestTemplate` failed (503, etc.) | 1 — try reset; if stuck on old queue → 2 |
| Stuck at `WORKFLOW_TASK_SCHEDULED`, old TaskQueue (0 pollers) | 2 — reset first, then restart on latest queue |
| `RegisterNodeToHarvestFarm` or lease activity failed | 3 |

---

# Failure Scenarios

## Scenario 1: UploadHarvestTemplate failed (e.g. 503)

**Symptoms:** Child workflow `FAILED`; metric mentions `UploadHarvestTemplate` / 503; `CREATE_POOL` `DONE`.

**Check partial registration** (table `node_node_group_maps`):

```sql
SELECT n.id AS node_id, n.name, ng.lease_name, nnm.id AS mapping_id
FROM pools p
JOIN nodes n ON n.pool_id = p.id AND n.deleted_at IS NULL
LEFT JOIN node_node_group_maps nnm ON nnm.node_id = n.id AND nnm.deleted_at IS NULL
LEFT JOIN node_groups ng ON ng.id = nnm.node_group_id AND ng.deleted_at IS NULL
WHERE p.uuid = '<POOL_UUID>';
```

| DB result | Meaning |
|-----------|---------|
| Rows with `lease_name` | DB + leases OK; **upload failed** |
| No rows | Failure earlier — Scenario 3 |

**Logs**

```text
jsonPayload.correlation_id="<CORRELATION_ID>"
resource.labels.namespace_name="vcp"
```

```bash
kubectl -n vcp logs -l app=harvest-farm \
  --since-time=<START_UTC> --until-time=<END_UTC>
kubectl -n vcp exec -it <harvest-farm-pod> -- curl -s localhost:3000/healthz
```

**Fix (only if TaskQueue still has workers)**

```bash
temporal task-queue describe --task-queue "<TASK_QUEUE_FROM_DESCRIBE>"
```

Reset to the event **before** first `UploadHarvestTemplate` (verify id in `workflow show`):

```bash
temporal workflow reset --namespace default \
  --workflow-id "register-node-to-harvest-farm-<POOL_UUID>-<ACCOUNT_ID>" \
  --event-id <EVENT_ID_BEFORE_UPLOAD> \
  --reason "retry harvest YAML upload after 503"
```

Or use **drift reconcile** if `ENABLE_HARVEST_POLLER_DRIFT_RECONCILE=true`.

**After reset:** `WORKFLOW_TASK_FAILED` with `cause: RESET_WORKFLOW` and your `--reason` text is **normal** — not an app error. If no new events after that → Scenario 2.

**Verify**

```bash
kubectl -n vcp exec -it <harvest-farm-pod> -- \
  curl -s localhost:3000/pollers/prometheus-targets
```

---

## Scenario 2: Stuck after reset — retired TaskQueue

**Symptoms:** Partial registration in DB (`node_node_group_maps` + `lease_name`) but upload never completed. After a **reset** (Scenario 1), workflow stays `RUNNING` with history ending at `WORKFLOW_TASK_SCHEDULED` and no new activities. `task-queue describe` on the workflow's queue shows **0 pollers**; live workers poll a **different** queue (retired RC, e.g. `rc-41`).

**Recovery order:** Try **reset first** (Scenario 1). Use the steps below only when reset does not progress because **no worker polls the workflow's task queue**.

**Check**

```bash
temporal task-queue describe --task-queue "<TASK_QUEUE_FROM_DESCRIBE>"
kubectl -n vcp exec <customer-worker-pod> -- printenv | grep TASK_QUEUE
```

Confirm partial registration (Scenario 1 query): rows with `lease_name` present.

**Fix A — Reset first** (preferred when the workflow queue still has pollers)

```bash
temporal task-queue describe --task-queue "<TASK_QUEUE_FROM_DESCRIBE>"
```

If pollers > 0, reset to the event **before** first `UploadHarvestTemplate`:

```bash
temporal workflow reset --namespace default \
  --workflow-id "register-node-to-harvest-farm-<POOL_UUID>-<ACCOUNT_ID>" \
  --event-id <EVENT_ID_BEFORE_UPLOAD> \
  --reason "retry harvest YAML upload after 503"
```

`WORKFLOW_TASK_FAILED` with `cause: RESET_WORKFLOW` is **normal**. Wait for `UploadHarvestTemplate` to run. If workflow completes → verify pollers and stop.

**Fix B — Restart on latest worker queue** (when reset is stuck: 0 pollers on old queue)

Terminate the stuck run, then start a **new** execution on the **current** customer-worker task queue.

**1. Terminate stuck workflow**

```bash
temporal workflow terminate --namespace default \
  --workflow-id "register-node-to-harvest-farm-<POOL_UUID>-<ACCOUNT_ID>" \
  --reason "stuck on obsolete task queue after reset"
```

**2. Get current task queue** (from a live customer-worker pod — do not reuse the old RC queue)

```bash
kubectl -n vcp exec <customer-worker-pod> -- printenv | grep TASK_QUEUE
```

**3. Build workflow input from DB**

```sql
SELECT p.id AS pool_id,
       p.uuid AS pool_uuid,
       p.name AS pool_name,
       p.deployment_name,
       p.account_id,
       COALESCE((p.pool_attributes->>'is_regional_ha')::boolean, false) AS is_regional_ha,
       a.name AS customer_project_id
FROM pools p
JOIN accounts a ON a.id = p.account_id
WHERE p.uuid = '<POOL_UUID>';
```

`TenantProjectID` is not on `pools`. Pull it from the original child workflow input:

```bash
temporal workflow show --namespace default \
  --workflow-id "register-node-to-harvest-farm-<POOL_UUID>-<ACCOUNT_ID>" \
  --run-id <ORIGINAL_FAILED_RUN_ID> \
  --output json | jq -r '.events[] | select(.workflowExecutionStartedEventAttributes != null) | .workflowExecutionStartedEventAttributes.input.payloads[0].data' | base64 -d | jq .
```

**4. Start workflow on latest queue**

```bash
temporal workflow start \
  --namespace default \
  --task-queue "<CURRENT_TASK_QUEUE>" \
  --type RegisterNodeToHarvestFarmWorkflow \
  --workflow-id "register-node-to-harvest-farm-<POOL_UUID>-<ACCOUNT_ID>" \
  --input '{
    "PoolID": <POOL_ID>,
    "MaxNodesPerGroup": 200,
    "CustomerProjectID": "<CUSTOMER_PROJECT_NUMBER>",
    "TenantProjectID": "<TENANT_PROJECT_NUMBER>",
    "PoolUUID": "<POOL_UUID>",
    "AccountID": <ACCOUNT_ID>,
    "DeploymentName": "<DEPLOYMENT_NAME>",
    "PoolName": "<POOL_NAME>",
    "IsRegionalHA": <true|false>
  }'
```

Example (placeholder values — replace with DB/worker output):

```bash
temporal workflow start \
  --namespace default \
  --task-queue customer-workflows-26055-0-0-dev-26 \
  --type RegisterNodeToHarvestFarmWorkflow \
  --workflow-id "register-node-to-harvest-farm-db24e83b-484d-406d-d0d1-24fc10c29731-42" \
  --input '{
    "PoolID": 12345,
    "MaxNodesPerGroup": 200,
    "CustomerProjectID": "710801665999",
    "TenantProjectID": "1014670304435",
    "PoolUUID": "db24e83b-484d-406d-d0d1-24fc10c29731",
    "AccountID": 42,
    "DeploymentName": "gcnv-a1b2c3d4e5f6",
    "PoolName": "my-pool-01",
    "IsRegionalHA": false
  }'
```

**Verify**

```bash
temporal workflow describe --namespace default \
  --workflow-id "register-node-to-harvest-farm-<POOL_UUID>-<ACCOUNT_ID>"

kubectl -n vcp exec -it <harvest-farm-pod> -- \
  curl -s localhost:3000/pollers/prometheus-targets
```

**Fix C — Other recovery options**

- **Drift reconcile** (preferred when enabled) — enable/trigger `HarvestPollerDriftReconcileWorkflow`
- **Manual POST** to `http://harvest-farm-service.vcp.svc.cluster.local:3000/config/upload` (ops)
- **`HarvestPollerUpgradeWorkFlow`** on current queue — only if `SECRET_ID` is set:

```sql
SELECT id, harvest_config->>'SECRET_ID' AS secret_id
FROM node_node_group_maps WHERE id IN (<MAPPING_IDS>);
```

**Do not:** repeat reset on a queue with **0 pollers** — it will stay stuck. Deploying new workers alone does **not** unblock the old run; use their task queue for Fix B.

---

## Scenario 3: Failed before upload

**Symptoms:** `RegisterNodeToHarvestFarm` or `ValidateAndCreateKubernetesLease` failed; no `lease_name` in partial-registration query (Scenario 1).

```sql
SELECT id, name, state FROM nodes
WHERE pool_id = (SELECT id FROM pools WHERE uuid = '<POOL_UUID>')
  AND deleted_at IS NULL;
```

Check `temporal workflow show` and worker logs for `correlation_id`. Fix DB/K8s/node issue; escalate — upload-only reset does not apply.

---

# Quick Reference

| Symptom | Action |
|--------|--------|
| 503 on upload, queue has pollers | Reset before upload event (Scenario 1) |
| Reset stuck, old queue 0 pollers | Reset first; if stuck → terminate + restart on latest queue (Scenario 2) |
| `RESET_WORKFLOW` event after reset | Normal — if no progress after, check pollers on queue |
| Mappings + leases exist, no pollers | Restart on latest queue or manual upload |
| New workers deployed | Does not fix old runs; use their task queue for restart |

**Escalate to Billing/platform with:** metric labels, pool uuid, job `workflow_id`, child `TaskQueue` + `RunId`, partial DB yes/no, recovery tried.

# Related Documentation

- [0016 Harvest Collector Design](../../architecture/designs/0016-harvest-collector-system.md)
- [0030 Harvest Drift Reconcile](../../architecture/designs/0030-harvest-poller-drift-reconcile-job.md)
- [Temporal Debugging Guide](../../guides/temporal-debugging.md)
- [VSA Metrics Not Flowing](./vsa_metrics_not_flowing.md)
