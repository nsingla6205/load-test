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
    │     1. if the drain Deployment is missing, exit (Helm creates the chart object)
    │     2. if it exists and the cutover is not finished, scale it up and wait until Build A is polling
    │
    ├─► Helm creates or updates the drain ConfigMap and Deployment, then updates vlm-worker-9-18-1p3 to Build B
    │
    └─► post-upgrade promote-retire Job
          1. wait both builds polling
          2. SetCurrentVersion(B)
          3. poll DescribeVersion(A) until drained
          4. scale drain Deployment → 0
          5. optional DeleteVersion(A)
```

Both Deployments poll the same `TASK_QUEUE` from ConfigMap (`vsa-lifecycle-manager-9.18.1P3`). Temporal routes new workflows to Current (B); pinned in-flight work stays on A.

## Initial design and later changes

### Initial design

The drain ConfigMap and Deployment were normal chart resources, and the pre-upgrade hook created or patched those same objects before Helm updated the primary:

1. Pre-upgrade: create or patch Build A, then block until Build A is Ready and polling.
2. Helm updates the primary Deployment to Build B and records the drain objects in the release.
3. Post-upgrade: wait until both builds are polling, `SetCurrentVersion(B)`, wait until A is drained, scale the drain Deployment to 0. `DeleteVersion(A)` stays optional.
4. The next values change clears `drainingBuilds`.

### Changes

| Change | Reason |
|--------|--------|
| The pre-upgrade hook does not create the drain ConfigMap or Deployment. If the Deployment is missing, the hook exits and Helm creates the chart object. If it already exists and the cutover is not finished, the hook scales it up and waits until Build A is polling. | The first upgrade that introduces the drain name failed. The hook created the ConfigMap, then Helm applied the chart. The object was on the cluster, but the previous release did not list the name. Helm 3 reports `no ConfigMap with the name "<name>" found` from that previous-release lookup. The next upgrade succeeds only because the failed revision stored the manifest. Creating the object in the hook and listing it as a chart resource cannot both succeed on that first upgrade. |
| The drain objects stay in the chart. They are not hook-only, and they are not dropped from the chart for the hook to own. | Dropping them from the chart makes Helm delete live objects the previous release still lists, after the hook has recreated them and before the primary rolls. Build A then has no poller. A `pre-upgrade` hook with `before-hook-creation` deletes and recreates the drain pods on every later release that still names the same build, before the Job can skip. `hook-failed` alone leaves the object, and the next hook Create returns AlreadyExists. |
| The drain Deployment copies its live replica count when the object already exists. | The promote Job scales that Deployment to 0. A later upgrade that still lists `drainingBuilds` would otherwise set replicas back to `app.replicas` and start Build A pods again. |
| Both hooks exit once Build B is already Current and Build A is drained. If B is already Current and A is not drained, skip `SetCurrentVersion` and still finish the drain. | A later chart bump re-runs the post-upgrade Job while `drainingBuilds` is still set. On Temporal 1.29, `SetCurrentVersion` where the previous and new build are the same writes `DrainageStatus: draining` onto that version's deployment summary. `describe-version` is the authority: Current omits `DrainageStatus` when it is unspecified, and the deployment summary can stay stale. |
| After that exit, the Job writes `<release>-versioning-cutover` with key `<ontap>__<buildId>__<drainingBuild>=done`. The next upgrade omits both hook Jobs when every `drainingBuilds` entry is already there. | Starting the Jobs on every later deploy waits on the Istio sidecar and fails the release if Temporal cannot be read. Leaving `drainingBuilds` set must not start those Jobs again. `helm template` with no cluster cannot see the ConfigMap, so it still renders the Jobs. |
| With versioning enabled, the primary rolling update uses `maxUnavailable: 0` and `maxSurge: 1`. | The hook no longer blocks the upgrade until a newly introduced drain Deployment is polling. Old Build A pods stay until the new Build B pods are Ready. The post-upgrade Job still waits for both builds to poll before it promotes. |
| When `buildId` is set and `drainingBuilds` is empty, the post-upgrade Job sets that build as Current if Current is unset, after the build is polling. It does not call `SetCurrentVersion` when that build is already Current. If a different build is already Current, the Job fails. It records `<ontap>__<buildId>__initial`, and the next upgrade omits the hook Jobs. | First enablement was a manual `SetCurrentVersion`. The same hook can do it when there is no previous build to drain. |

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
    drainingBuilds:             # optional; chart-rendered previous build for this cutover
      - buildId: "8083481"
        vlmImageDigest: "sha256:OLD..."
```

Deployments:

- Primary: `vlm-worker-<version>` — always rendered from current mapping.
- Drain: `vlm-worker-<version>-drain-<buildA>` is a chart resource when `drainingBuilds` is non-empty. Label `vlm.netapp.io/role: drain`. The hook does not create it. Helm 3 fails the first upgrade if a hook creates an object the previous release does not list.

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
  1. Pre-upgrade: if the drain Deployment is already on the cluster and the cutover is not finished, scale it to the chart replica count and wait until Build A is Ready and polling. If it is missing, exit 0 so Helm can create the chart object. Creating it in the hook fails that first upgrade.
  2. Helm creates or updates the drain ConfigMap and Deployment, then updates the primary Deployment to Build B. The primary rolling update uses `maxUnavailable: 0`, so old Build A pods stay until new Build B pods are Ready.
  3. Post-upgrade waits until Build A and Build B are polling before promote.
  4. `PromoteBuild(B)`, unless B is already Current. Calling `SetCurrentVersion` for the build that is already Current writes `draining` onto the deployment summary on Temporal 1.29.
  5. Poll until `IsBuildDrained(A)` (configurable interval, overall timeout).
  6. `scaleDeployment(drainDeployment, 0)` via K8s API.
  7. Optional `DeleteBuild(A)`.
- When the drain Deployment already exists, the pre-upgrade gate fails closed. Post-upgrade errors leave drain pods running for operator recovery.
- Hook ordering: run **before** reconciler hook (lower weight) so drain workers are not scaled by reconciler mid-rollout.

### 5. Reconciler guard (`tools/vlm-worker-reconciler`)

Extend `listVLMWorkerDeployments` / reconcile loop to skip Deployments with label `vlm.netapp.io/role=drain`.

## Rollout Sequence (canonical)

1. Pipeline updates `vlmImageDigest` + `buildId` on existing mapping; adds `drainingBuilds` entry with previous digest/build.
2. The chart renders drain A. On the first upgrade that introduces that name, the pre-upgrade hook leaves creation to Helm. On a later upgrade, if the cutover is not finished, the hook scales the existing drain Deployment up and waits until it is polling.
3. Helm updates the primary Deployment to Build B. `maxUnavailable: 0` keeps old Build A pods until new pods are Ready.
4. The post-upgrade hook promotes B when B is not already Current, waits for A drained, and scales A to 0.
5. Later upgrades may leave `drainingBuilds` set. Once B is Current and A is drained, the Job records that pair. The next upgrade omits both hook Jobs when every pair is already recorded. The drain Deployment template reads the live replica count, so Helm does not scale those pods back up.
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
