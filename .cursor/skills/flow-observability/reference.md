# VCP flow observability reference

## Locate a flow

Search in this order:

1. `worker/main.go` workflow registration
2. `core/orchestrator/workflows/oci/` and matching tests
3. `core/orchestrator/factory/oci/`
4. `oci-proxy/api/endpoints/` or `google-proxy/`
5. `worker/metrics/` for jobs and rotation flows

| Flow | Typical entry |
|---|---|
| Execute maintenance | `OCIExecutePoolMaintenanceWorkflow` |
| Reset admin password | `OCIResetOciAdminPasswordWorkflow` |
| Update pool | `OCIUpdatePoolWorkflow` |
| Create/delete pool | `OCICreatePoolWorkflow`, `OCIDeletePoolWorkflow` |
| SVM operations | `svm_workflow.go` |
| Cluster upgrade | `OCIClusterUpgradeWorkflow` |
| Fabric-pool key rotation | `OCIRotateFabricPoolKeysWorkflow` |
| RBAC refresh | `OCIRefreshRbacForPoolWorkflow` |

## Existing metrics implementations

| Surface | Existing implementation |
|---|---|
| OCI Temporal workflows | `core/orchestrator/workflows/oci/metrics.go` |
| OCI HTTP API | `oci-proxy/metrics/metrics.go` |
| Shared OCI registry | `utils/metrics/registry.go` |
| Worker jobs and rotation | `worker/metrics/metric.go` and focused emitters |
| ONTAP reverse proxy | `ontap-proxy/middleware/metrics.go` |
| Telemetry and billing | `telemetry/` |
| Leaked-resource jobs | `core/orchestrator/leakedresources/metrics.go` |

Never create a new metrics implementation until these locations and the target package have been checked.

## OCI workflow reuse

`core/orchestrator/workflows/oci/metrics.go` already owns:

- `oci_workflow_stage_total`
- `oci_workflow_duration_seconds`
- `emitStage`
- `emitDuration`
- workflow, stage, result, and queue constants

Add new `wf*` or `stage*` constants there when needed. Do not create another collector or helper.

Wrapper pattern:

```go
start := workflow.Now(ctx)
if err := wf.Setup(ctx, params); err != nil {
	emitDuration(ctx, wfName, queueCustomer, start)
	return err
}
_, errRun := wf.Run(ctx, params, resource)
if errRun != nil {
	emitDuration(ctx, wfName, queueCustomer, start)
	return errRun
}
emitDuration(ctx, wfName, queueCustomer, start)
```

Stage pattern:

```go
if err := executeStage(); err != nil {
	logger.Error("Stage failed", "error", err, "poolUUID", pool.UUID)
	emitStage(ctx, wfName, queueCustomer, stageName, resultFailure)
	return nil, workflows.ConvertToVSAError(err)
}
emitStage(ctx, wfName, queueCustomer, stageName, resultSuccess)
```

Use explicit duration calls on all exits to match existing OCI workflows. Stage success is emitted only after the complete stage succeeds.

## Creation fallback

If the target package has no metrics implementation and no shared owner:

1. Inspect the nearest comparable package.
2. Use its registry and Prometheus/OTEL style.
3. Define bounded workflow, stage, result, and queue labels.
4. Keep collector mutation behind reusable helpers.
5. For Temporal workflows, wrap mutation in `workflow.SideEffect`.
6. Add registration and behavior tests.

Do not place cloud SDK imports in `core/`.

## Cardinality and naming

- Counters use `_total`.
- Durations use `_seconds`.
- Label values are bounded constants where possible.
- Never use raw errors, request bodies, URLs containing ids, passwords, tokens, or user-provided free text as labels.
- Normalize HTTP paths before using them as labels.

## Logs

Use `util.GetLogger(ctx)` (import `workflow_engine/util`) or `wf.Logger` for workflows, and the project slog abstraction elsewhere.

Required when applicable:

- Stable message
- `"error", err`
- `workflowID` or job id
- Operation or stage
- Resource id such as `poolUUID` or `poolOCID`

Never log passwords, tokens, certificate material, Vault values, request bodies, or PII.

## Tests

- OCI definitions: `core/orchestrator/workflows/oci/metrics_test.go`
- OCI flow behavior: matching workflow test file
- HTTP metrics: `oci-proxy/middleware/oci_http_test.go`
- Worker metrics: `worker/metrics/metric_test.go`

Use counter deltas for process-global collectors and verify both success and failure paths.
