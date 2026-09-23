---
name: flow-observability
description: Check and add reusable Prometheus/OTEL metrics and structured slog for any named control-plane flow or PR/diff. Reuses existing metrics definitions and helpers first; creates metrics.go only when the target package has no metrics implementation. Adds missing OCI emitStage/emitDuration and tests. Use when asked to inspect or instrument a workflow, operation, API, job, or code flow.
disable-model-invocation: true
---

# Flow Observability

Inspect and instrument a named flow, file, package, branch, or PR with metrics and structured logs. A PR is optional.

Always discover and reuse existing metrics code before editing a flow. Create `metrics.go` only when the target package has no suitable metrics implementation.

Repo conventions and recipes: [reference.md](reference.md).

## Inputs

Resolve the target in this order:

1. Named flow or operation, such as reset password, execute maintenance, update pool, or `OCIUpdatePoolWorkflow`
2. Explicit file or package
3. PR number or URL
4. Current branch versus its base
5. Uncommitted changes

For a named flow, inspect current implementation and tests rather than requiring a diff.

## Workflow

- [ ] 1. Locate the flow and tests
- [ ] 2. Discover existing metrics code and helpers
- [ ] 3. Map flow stages and terminal paths
- [ ] 4. Reuse or extend existing metrics definitions
- [ ] 5. Create metrics.go only if no suitable implementation exists
- [ ] 6. Add missing emission calls and structured logs
- [ ] 7. Add or extend tests
- [ ] 8. Run targeted tests
- [ ] 9. Report

## 1. Locate the flow

Search the workflow registration, workflow implementation, factory, endpoint, activities, and tests. For OCI, start with:

- `worker/main.go`
- `core/orchestrator/workflows/oci/`
- `core/orchestrator/factory/oci/`
- `oci-proxy/api/endpoints/`

Skip generated files, mocks, and unrelated flows.

## 2. Discover existing metrics first

Before adding any metric call:

1. Search the target package and its parent/sibling packages for:
   - `metrics.go`, `metric.go`, `*_metrics.go`
   - `emitStage`, `EmitStage`, `emitDuration`
   - Prometheus `CounterVec`, `HistogramVec`, `GaugeVec`
   - OTEL counters/histograms
   - registration and test patterns
2. Read the existing definitions, helper signatures, labels, registry, constants, and tests.
3. Determine whether an existing metric and helper semantically cover the flow.
4. Reuse existing helpers and label values whenever possible.

Do not create a duplicate collector, registry, `emitStage`, or `emitDuration`.

For any workflow in `core/orchestrator/workflows/oci/`, first inspect `core/orchestrator/workflows/oci/metrics.go`. Reuse:

- `oci_workflow_stage_total`
- `oci_workflow_duration_seconds`
- `emitStage`
- `emitDuration`
- existing `wf*`, `stage*`, `result*`, and queue constants

Add only missing workflow or stage constants to that existing file.

## 3. Map coverage

For the target flow identify:

- Exported workflow wrapper and every return path
- Setup failure, Run failure, and success
- Each activity, child workflow, external call, validation, and persistence stage
- Optional/skipped branches
- VLM/provider failures and recovery branches
- Existing Error/Info logs

Optional work emits no success metric when skipped.

## 4. Reuse or extend

If suitable metrics code exists:

- Reuse its collectors, registry, helpers, labels, and tests.
- Add only missing constants or narrowly reusable helper behavior.
- Keep emission calls thin; collector details stay in the metrics implementation.
- Never duplicate a metric under a new name for the same operation.

For OCI workflows:

- Add `emitDuration` on Setup failure, Run failure, and success.
- Add `emitStage(..., resultFailure)` before every failing return for a stage.
- Add `emitStage(..., resultSuccess)` once after the stage fully completes.
- Use the same VLM stage for VLM call errors, partial response failures, and terminal recovery failure where appropriate.
- Keep Temporal metric mutation inside the existing `workflow.SideEffect` helpers.

## 5. Create metrics.go only when absent

Create `metrics.go` only when all are true:

1. No `metrics.go`, `metric.go`, or equivalent implementation exists in the target package.
2. No shared parent or sibling metrics package already owns the operation.
3. Existing helpers cannot represent the flow without incorrect semantics.

Before creation, inspect the nearest comparable package and follow its:

- Prometheus or OTEL choice
- Registry and registration behavior
- Metric naming and bounded labels
- Temporal determinism pattern
- Test style

For a new Temporal workflow metrics implementation, define reusable stage and duration collectors plus `emitStage` and `emitDuration`; callers must not directly mutate collectors. Do not create an OCI-local `metrics.go` because one already exists.

## 6. Logs

Add structured logs only where missing:

- Info at flow start and terminal success
- Error before failure returns
- Stable message plus key-value fields
- `"error", err`, workflow/job id, operation, and resource ids when available

Never log passwords, tokens, certificate material, secret values, request bodies, or PII.

## 7. Tests

- Extend existing metric tests before creating a new test file.
- Assert stage success/failure and duration observations.
- Add flow tests for important failure paths, especially VLM/provider failures.
- Compare counter deltas when collectors are process-global.

Run the narrow package tests touched by the change.

## Report

```markdown
## Flow observability

- **Verdict**: Pass | Pass with suggestions | Block
- **Target**: `<flow, file, branch, or PR>`
- **Metrics implementation reused**: `<path and helpers>`
- **Created metrics.go**: Yes | No

### Changes made
- Metrics definitions/helpers: ...
- Emission calls: ...
- Logs: ...
- Tests: ...

### Coverage
| Stage | Success | Failure | Logs | Tests |
|---|---|---|---|---|
| `stage` | ✅/❌/N/A | ✅/❌/N/A | ✅/❌ | ✅/❌ |

### Findings
- Fixed: ...
- Suggestion: ...
- Critical: ...
```

Block only when required observability remains missing.
