# Leaked Resources Monitoring Framework

## Overview

The leaked resources framework detects and reports control-plane resources that are inconsistent across systems (e.g. VCP vs CCFE) or orphaned (e.g. volume without a pool, snapshot without a volume). It runs **only in the core process** (not the worker), on a fixed schedule, and produces a single report per run. It does **not** perform cleanup; it only scans, detects, and reports so operators can act or future automation can be added.

**Flow:** Scan → Detect → Report.

- **Scan:** Each detector queries the data it needs (VCP DB, and optionally CCFE or other sources).
- **Detect:** Detectors compare data and produce **leak records** (e.g. pool in CCFE but not in VCP).
- **Report:** All records from all detectors are aggregated and passed to a **reporter** (e.g. log, metrics, or later GCS).

The framework is **extensible**: new detectors and reporters can be added without changing the core pipeline.

---

## Where It Runs

- **Process:** Core (same process that serves the API and runs the workflow supervisor).
- **Trigger:** Cron scheduled in `vcp-core/cmd/main.go` via the same background task scheduler used by the workflow supervisor.
- **Schedule:** Every 6 hours (configurable via cron expression).
- **Concurrency:** A database-backed lock (admin job spec) ensures only one instance runs at a time across pods.

---

## Package Layout

```
core/orchestrator/leakedresources/
├── pipeline.go          # Pipeline: run detectors, aggregate, report
├── reporting.go         # Reporter interface + LogReporter
├── types.go             # Re-exports from model for backward compatibility
├── model/
│   ├── types.go         # LeakRecord, ResourceType
│   └── detector.go      # Detector interface
├── ccfe/
│   └── client.go        # Internal CCFE list-storage-pools client (no user-facing API)
└── detectors/
    ├── pool.go          # Pool: CCFE vs VCP
    ├── volume.go        # Volume: orphan (pool missing)
    └── snapshot.go      # Snapshot: orphan (volume missing)
```

---

## Pipeline

The **pipeline** is the single entry point that:

1. Resets per-run reporter state at the start of the run (before any detector reports).
2. Runs each registered **detector** in order.
3. Reports **each detector's** leak records to the reporter(s) that detector was registered with.

The cron invokes `leakedresources.Run(ctx, storage, temporalClient)`, which builds the default pipeline, registers the built-in detectors along with their reporters, and runs the pipeline. No caller needs to know about individual detectors.

**Key types:**

- `Pipeline` holds a list of `(detector, reporter)` registrations.
- `Register(detector, ...reporters)` pairs a detector with the reporters its records flow to (no reporters => log-only `LogReporter`; one => used directly; many => wrapped in `MultiReporter`).
- `Run(ctx, storage, temporalClient)` creates the pipeline, registers the built-in detectors with their reporters, and runs it.

**Per-detector reporting:** because each detector reports independently, different detectors can be routed differently (e.g. one log-only, another log + metrics, a third to a future ServiceNow reporter). Reporters that hold per-run state (e.g. the metrics gauge) implement the optional `runAware` interface; the pipeline calls `BeginRun` at the start of the run (before any detector reports) so per-detector reporting accumulates within a run instead of each detector clobbering the previous one's state. `BeginRun` must be idempotent, since a reporter instance shared across registrations (like the default metrics reporter) may be reset more than once per run.

---

## Detector Interface

Every detector implements `model.Detector`:

```go
type Detector interface {
    Name() string
    Detect(ctx context.Context, storage database.Storage) ([]LeakRecord, error)
}
```

- **Name:** Used in logs when a detector fails (e.g. `"pool"`, `"volume_orphan"`).
- **Detect:** Performs the scan and comparison, returns leak records. Errors are logged and the pipeline continues with other detectors.

Detectors live under `leakedresources/detectors/` and depend only on `leakedresources/model` and the database interface to avoid import cycles.

---

## Leak Record

A **leak record** is a single inconsistent or orphan resource:

| Field          | Type            | Description |
|----------------|-----------------|-------------|
| ResourceType   | `pool` / `volume` / `snapshot` | Kind of resource. |
| ResourceID     | string          | Stable ID (e.g. UUID). |
| ResourceName   | string          | Human-readable name (optional). |
| ProjectID      | string          | Project/account (optional). |
| Region         | string          | Region/location (optional). |
| Reason         | string          | Why it is considered leaked (see below). |
| Extra          | map[string]string | Detector-specific fields (e.g. `pool_id`, `volume_id`). |

**Reasons (examples):**

- **Pool:** `in_ccfe_not_in_vcp`, `in_vcp_not_in_ccfe`
- **Volume:** `volume_orphan_pool_missing`
- **Snapshot:** `snapshot_orphan_volume_missing`

---

## Built-in Detectors

### 1. Pool (CCFE vs VCP)

- **Name:** `pool`
- **Logic:**
  - List all non-deleted pools from VCP; derive unique (project, region) from `Account.Name` and `PoolAttributes.PrimaryZone`.
  - For each (project, region), list pools from CCFE (internal GET only) and list VCP pools in that scope.
  - Compare by pool resource name: report **in CCFE but not in VCP** and **in VCP but not in CCFE**.
- **Dependencies:** VCP `ListPools`; internal CCFE list-storage-pools client (see below).
- **When CCFE is disabled:** If `GCP_HYDRATE_BASE_URL` is empty, the CCFE client returns no data and the detector skips CCFE comparison for that run (no false “in VCP not in CCFE”).

### 2. Volume Orphan

- **Name:** `volume_orphan`
- **Logic:** List non-deleted pools to build the set of valid pool IDs. List non-deleted volumes; any volume whose `pool_id` is not in that set is reported as orphan (`volume_orphan_pool_missing`).
- **Dependencies:** VCP only (`ListPools`, `ListVolumes`).

### 3. Snapshot Orphan

- **Name:** `snapshot_orphan`
- **Logic:** List non-deleted volumes to build the set of valid volume IDs. List non-deleted snapshots; any snapshot whose `volume_id` is not in that set is reported as orphan (`snapshot_orphan_volume_missing`).
- **Dependencies:** VCP only (`ListVolumes`, `GetSnapshotsWithCondition`).

---

## CCFE Client (Internal Only)

The **CCFE client** (`leakedresources/ccfe/client.go`) is used **only** by the pool detector inside the leaked-resources pipeline. It is **not** exposed as a user-facing “list CCFE pools” API.

- **Purpose:** GET list of storage pools from CCFE for a given (project, location) so the pool detector can compare with VCP.
- **Config:** `GCP_HYDRATE_BASE_URL` (same as hydration). If empty, `ListStoragePools` returns `(nil, nil)` and the pool detector skips CCFE comparison.
- **Auth:** Uses the same token getter as hydration (e.g. `auth.GenerateCallbackToken`).
- **API path:** `GET {baseURL}/v1beta1/projects/{projectID}/locations/{location}/storagePools`
- **Extensibility:** Client supports `WithHTTPClient` and `WithTokenGetter` for tests.

---

## Reporting

- **Reporter interface:** `Report(ctx, records []model.LeakRecord) error`. The pipeline calls it once per detector per run with that detector's records. A detector returning no records does not invoke its reporter.
- **Default:** built-in detectors are registered with `LogReporter` (logs a summary and one line per record) plus `MetricsReporter` (exports the `vcp.leaked_resources.count` gauge).
- **`runAware` (optional):** reporters with per-run state implement `BeginRun(ctx)`. `MetricsReporter` resets its gauge snapshot in `BeginRun`; `MultiReporter` propagates `BeginRun` to its children. The pipeline calls it at the start of the run, before any detector reports. `BeginRun` must be idempotent, because a reporter instance shared across detector registrations may have `BeginRun` invoked more than once per run (always before any `Report`).
- **Extensibility:** additional reporters (e.g. GCS upload, ServiceNow) are added by passing them to `Pipeline.Register(detector, ...reporters)` in the entry point. A detector registered with no reporters defaults to log-only.

---

## Configuration

| Item | Source | Notes |
|------|--------|--------|
| Schedule | Cron expression in vcp-core/cmd/main.go | Default: every 6 hours. |
| Lock timeout | `leakedResourcesMonitoringLockTimeoutSeconds` | How long the lock is held so other pods skip. |
| CCFE base URL | `GCP_HYDRATE_BASE_URL` | If unset, pool detector skips CCFE list. |
| CCFE auth | Same as hydration | e.g. `auth.GenerateCallbackToken`. |

---

## Extending the Framework

### Adding a detector

1. Implement `model.Detector` in a new file under `leakedresources/detectors/` (e.g. `stuck_delete_job.go`).
2. In `leakedresources.Run()` in `pipeline.go`, register it with the reporters its records should flow to:
   - log + metrics (default): `p.Register(detectors.NewMyDetector(...), logAndMetrics...)`
   - log-only (no metrics): `p.Register(detectors.NewMyDetector(...))` or `p.Register(detectors.NewMyDetector(...), logReporter)`
3. Use existing `LeakRecord` reasons or add new ones; document them in this doc or a companion runbook.

### Adding a reporter

1. Implement the `Reporter` interface (`Report(ctx, records []model.LeakRecord) error`).
2. If the reporter holds per-run state, also implement `runAware` (`BeginRun(ctx)`) so the pipeline resets it once per run.
3. In `leakedresources.Run()`, pass it to `Register` for the detectors that should use it, e.g. `p.Register(myDetector, logReporter, myReporter)`. Multiple reporters are automatically wrapped in a `MultiReporter`.

### Adding another pool or volume scenario

- **Option A:** Extend the existing pool or volume detector with more logic and new `Reason` values.
- **Option B:** Add a separate detector (e.g. “pool stuck delete”, “volume CCFE vs VCP”) and register it; both will run and their records are aggregated.

---

## Relationship to Workflow Supervisor

The **workflow supervisor** (see [0015-workflow-supervisor-task.md](0015-workflow-supervisor-task.md)) and the **leaked resources framework** are separate:

| Aspect | Workflow supervisor | Leaked resources framework |
|--------|---------------------|----------------------------|
| Goal | Recover **stuck workflows** (timeout / not found) and run **cleanup** in VCP. | **Detect and report** inconsistencies/orphans (no cleanup). |
| Trigger | Cron every 5 minutes. | Cron every 6 hours. |
| Scope | Jobs in `NEW` state and their Temporal workflow. | Cross-system comparison (e.g. CCFE vs VCP) and orphan checks (volume/snapshot). |
| Output | Cleanup (DB updates, workflow termination). | Log (and optionally metrics/GCS) with leak records. |

Both run in **core** and use the same cron/lock mechanism (admin job spec) for single-run guarantees.

---

## Metrics and Observability

- The same background-task metrics used by the workflow supervisor apply: e.g. lock acquisition, run start, and errors (schedule, acquire_lock, run) so operators can see when the leaked-resources task ran and when it failed.
- Leak records are logged at WARN level with resource type, id, name, project, region, and reason. Future work may add Prometheus gauges or counters per reason and resource type.

---

## Summary

The leaked resources framework is a **scan–detect–report** pipeline that runs in **core** on a 6-hour cron, uses a **single lock** per run, and supports **multiple detectors** (pool CCFE vs VCP, volume orphan, snapshot orphan) and a **pluggable reporter** (default: log). The CCFE client is **internal only**. The design is **extensible** for new detectors and reporters without changing the core flow.
