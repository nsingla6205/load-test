# VLM D-Patch In-Place Upgrade with Temporal Worker Versioning (GCP)

## Related Documents

| Document | Title | Description |
|----------|-------|-------------|
| [0011-vcp-worker_versioning-design.md](./0011-vcp-worker_versioning-design.md) | Worker Versioning | ONTAP-keyed VLM Deployments and task queues |
| [0029-vlm-worker-reconciler-design.md](./0029-vlm-worker-reconciler-design.md) | VLM Worker Reconciler | Post-upgrade hook Job precedent |
| Confluence | Temporal Worker Versioning for VLM | VLM worker opt-in + VCP promote/drain contract |

## Overview

Major and P-patch ONTAP releases already add a new `ontapVersionVlmImageMappings` entry, a new `vlm-worker-*` Deployment, and a new task queue. **D-patch** (urgent VLM image fix on an unchanged ONTAP line) reuses the same mapping key and queue but today replaces the worker image via a rolling update, which forces in-flight VLM workflows to replay on new code.

This design adds **Temporal Worker Versioning (Pinned)** for **GCP** VLM workers only, so a D-patch runs Build B alongside Build A on the **same** queue until Temporal reports Build A drained, then retires Build A pods.

**Out of scope (phase 1):** OCI chart, new ONTAP D suffix in version strings, VCP `ExecuteWorkflow` changes.

## Problem

| Scenario | Today | Desired |
|----------|-------|---------|
| P-release / major ONTAP | New mapping + Deployment + queue | Unchanged (already supported) |
| D-patch (same ONTAP line) | Bump digest → rolling replace → replay risk | Blue/green Build IDs on same queue |
| In-flight cluster create/upgrade | May non-determinism-fail on deploy | Finish on Build A; new starts on Build B |

## Decision Summary

| Topic | Decision |
|-------|----------|
| Platform | GCP `kubernetes/vlm-worker` only |
| Versioning model | Temporal Worker Deployment + Pinned (VLM worker sets `TEMPORAL_VERSIONING_ENABLED`) |
| ONTAP identity | Unchanged — same `ontapVersion`, same `vsa-lifecycle-manager-<version>` queue |
| Build identity | `TEMPORAL_WORKER_BUILD_NUMBER` per image (P4 change or digest short hash) |
| Worker Deployment name | `vsa-lifecycle-manager-<versionName>` (matches task queue suffix) |
| Rollout driver | Pre-upgrade ensure-drain gate + post-upgrade promote/retire Helm hook Jobs |
| Retire gate | Temporal `DrainageStatus == drained` before scaling Build A to 0 |
| Reconciler interaction | Skip Deployments labeled `vlm.netapp.io/role: drain` |
| VCP workflow starts | Unchanged — no `VersioningOverride` on `ExecuteWorkflow` |

## Architecture

```text
helm upgrade (digest change on 9.18.1P3 mapping)
    │
    ├─► pre-upgrade ensure-drain Job
    │     1. create/patch Build A ConfigMap + drain Deployment
    │     2. wait Build A Ready and polling (failure blocks upgrade)
    │
    ├─► Helm updates vlm-worker-9-18-1p3 to Build B
    │
    └─► post-upgrade promote-retire Job
          1. wait both builds polling
          2. SetCurrentVersion(B)
          3. poll DescribeVersion(A) until drained
          4. scale drain Deployment → 0
          5. optional DeleteVersion(A)
```

Both Deployments poll the same `TASK_QUEUE` from ConfigMap (`vsa-lifecycle-manager-9.18.1P3`). Temporal routes new workflows to Current (B); pinned in-flight work stays on A.

## Components

### 1. VLM worker (DOT repo — already merged per Confluence)

Opt-in via env (no VCP code change on worker binary in this repo):

- `TEMPORAL_VERSIONING_ENABLED=true`
- `TEMPORAL_WORKER_BUILD_NUMBER=<unique per image>`

Default behavior: Pinned.

### 2. GCP Helm chart (`kubernetes/vlm-worker`)

ConfigMap — add to each versioned ConfigMap:

- `TEMPORAL_VERSIONING_ENABLED: "true"`
- `TEMPORAL_WORKER_BUILD_NUMBER: "<buildId>"`

Values — extend mapping schema:

```yaml
ontapVersionVlmImageMappings:
  - ontapVersion: "9.18.1P3"
    vlmImageDigest: "sha256:NEW..."
    buildId: "8141647"          # new; required when versioning enabled
    drainingBuilds:             # optional; hook-managed during rollout
      - buildId: "8083481"
        vlmImageDigest: "sha256:OLD..."
```

Deployments:

- Primary: `vlm-worker-<version>` — always rendered from current mapping.
- Drain (conditional): `vlm-worker-<version>-drain-<buildA>` when `drainingBuilds` non-empty; label `vlm.netapp.io/role: drain`.

Feature flag: `vlmWorkerVersioning.enabled: false` until staging sign-off.

### 3. Temporal helper (`lib/temporal/workerdeployment/` or `workflow_engine/temporal/`)

Wrap Experimental SDK APIs behind a small interface:

| Method | SDK |
|--------|-----|
| `PromoteBuild(ctx, deploymentName, buildID)` | `SetCurrentVersion` |
| `IsBuildPolling(ctx, taskQueue, deploymentName, buildID)` | `DescribeTaskQueueEnhanced` with `ReportPollers`; require a recent poller matching the deployment and build |
| `IsBuildDrained(ctx, deploymentName, buildID)` | `DescribeVersion` → `DrainageInfo` |
| `DeleteBuild(ctx, deploymentName, buildID)` | `DeleteVersion` (optional) |

Prerequisite: `go.temporal.io/sdk` v1.33.0 already exposes `WorkerDeploymentClient()` — no SDK bump required for VCP hook binary.

Temporal client for hook: reuse SA/network path from reconciler Job or inject `SERVER_IP`/`SERVER_PORT` from chart values.

### 4. Rollout hook Jobs (`tools/vlm-worker-versioning-rollout/`)

Modeled on `tools/vlm-worker-reconciler/`:

- Distroless Go binary with pre-upgrade `ensure-drain` and post-upgrade `promote-and-retire` modes
- Inputs: env listing target ONTAP version + old/new build IDs + digests (from Helm-rendered Job spec)
- Steps per D-patch upgrade (digest changed, same `ontapVersion`):
  1. Pre-upgrade: create/patch the drain ConfigMap and Deployment from the old build values.
  2. Pre-upgrade: wait until Build A is Ready and polling; exit non-zero on failure to block the unsafe primary update.
  3. Helm updates the primary Deployment to Build B.
  4. Post-upgrade: ensure Build A and Build B are polling.
  5. `PromoteBuild(B)`.
  6. Poll until `IsBuildDrained(A)` (configurable interval, overall timeout).
  7. `scaleDeployment(drainDeployment, 0)` via K8s API.
  8. Optional `DeleteBuild(A)`.
- The pre-upgrade safety gate fails closed. Post-upgrade errors leave drain pods running for operator recovery.
- Hook ordering: run **before** reconciler hook (lower weight) so drain workers are not scaled by reconciler mid-rollout.

### 5. Reconciler guard (`tools/vlm-worker-reconciler`)

Extend `listVLMWorkerDeployments` / reconcile loop to skip Deployments with label `vlm.netapp.io/role=drain`.

## Rollout Sequence (canonical)

1. Pipeline updates `vlmImageDigest` + `buildId` on existing mapping; adds `drainingBuilds` entry with previous digest/build.
2. The pre-upgrade hook creates/patches drain A and blocks until it is Ready and polling.
3. Helm updates primary to B and records drain A as a normal release resource.
4. The post-upgrade hook promotes B, waits for A drained, and scales A to 0.
5. Remove `drainingBuilds` entry on the next chart values update.
6. Reconciler continues to scale inactive ONTAP-version workers to 0 (unchanged semantics).

## First-Time Enablement

Per Confluence §16:

- If no Current Version exists, promote B before relying on new workflow starts.
- Workflows started by unversioned workers before enablement may stall unless an unversioned poller remains until they complete. Plan enablement during a quiet window or keep legacy worker until bootstrap completes.

## Edge Cases

| Case | Handling |
|------|----------|
| Digest unchanged | Hook no-op |
| Promote before B polling | `SetCurrentVersion` rejected (`AllowNoPollers=false`) — hook retries |
| Kill drain pods early | Pinned workflows stall — do not scale drain Deployment until drained |
| Reconciler vs drain | Label guard + hook ordering |
| `WORKFLOW_ID_REUSE_POLICY_TERMINATE_IF_RUNNING` | Unrelated to versioning; same deployment ID still terminates in-flight |
| Parallel homegrown queue versioning | Do not mix `TASK_QUEUE_VERSION` suffix scheme with this for VLM — single source of truth per ONTAP queue |

## Testing

| Layer | Coverage |
|-------|----------|
| Unit | Temporal helper with SDK mocks; hook state machine |
| Integration | Staging: digest bump on one mapping; verify dual Deployments, promote, drain, scale-down |
| Regression | P-release add-mapping flow unchanged; reconciler still scales idle ONTAP versions |

## Success Criteria

1. D-patch digest bump does not terminate the sole poller for in-flight pinned workflows before drainage.
2. New VLM workflow starts route to Build B after promote.
3. Build A pods retire only after Temporal reports drained.
4. Existing multi-ONTAP-version Deployment model unchanged.

## Implementation Ownership

| Area | Owner / location |
|------|------------------|
| Temporal API wrapper | `workflow_engine/temporal/` or `lib/temporal/workerdeployment/` |
| Rollout hook binary | `tools/vlm-worker-versioning-rollout/` |
| Helm templates + values | `kubernetes/vlm-worker/` |
| Reconciler guard | `tools/vlm-worker-reconciler/` |
| VLM worker env (Pinned) | DOT / VLM image pipeline |

**Status:** Draft — pending review before implementation plan.
