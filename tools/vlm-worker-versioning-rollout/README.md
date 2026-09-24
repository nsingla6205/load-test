# VLM Worker Versioning Rollout

Helm hook binary for GCP VLM D-patch Temporal Worker Versioning (design 0032).

## Rollout

The binary has two Helm-hook modes:

1. `ensure-drain` runs before an upgrade. It creates or patches the Build A
   ConfigMap and Deployment, then blocks the upgrade until Build A is Ready and
   has a recent matching poller in Temporal's task-queue description. A failure
   exits non-zero so Helm cannot replace the primary Build A pods before the
   drain pollers are available.
2. `promote-and-retire` runs after the upgrade. It waits for Build A and Build B
   to poll, promotes Build B, waits for Build A to drain, then scales the drain
   Deployment to 0.

`DRY_RUN=true` applies only to post-upgrade promotion and retirement. The
pre-upgrade safety gate always creates or patches Build A drain resources,
because Helm will update the primary image even during a dry-run rollout.

## Build

```bash
go build -o vlm-worker-versioning-rollout ./tools/vlm-worker-versioning-rollout
```

## Pipeline D-patch checklist

1. Set `vlmWorkerVersioning.enabled: true` (and `rollout.enabled: true`) in the env.
2. On the ONTAP mapping: set `buildId` to the **new** build; set `drainingBuilds` to the **previous** buildId + digest (do this **before** `helm upgrade`).
3. `helm upgrade` runs the pre-hook to establish Build A drain pollers before updating primary to Build B; the post-hook then promotes and retires.
4. After success, remove `drainingBuilds` on a later values change.

Worker Deployment name defaults to sanitized task queue (`.` → `-`).
