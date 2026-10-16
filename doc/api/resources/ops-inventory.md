# Ops Inventory API (Core, internal, read-only)

Read-only admin API in **vcp-core** for Google tooling and internal SRE. It reads operator resources straight from the VCP database, with one GET and one LIST for every type.

It is **not** part of the public GCNV contract. Fields may change.

| Purpose | Method & Path |
|---------|---------------|
| Get one resource | `POST /v1/internal/projects/{projectNumber}/locations/{locationId}/resources:get` |
| List resources | `POST /v1/internal/projects/{projectNumber}/locations/{locationId}/resources:list` |

- **Resource types:** `pool`, `volume`, `snapshot`, `replication`, `backup`, `backupVault`, `backupPolicy`, `cmek`, `activeDirectory`, `job`.
- **Spec:** `vcp-core/api.yaml` (operations `v1_getInternalResource`, `v1_listInternalResources`, schemas `Ops*_v1`).
- **Code:**
  - `vcp-core/handlers/opsinventory/`: the shared handler plus one reader per type.
  - `database/vcp/readstore/`: the read-only DB store.

## Rules

### Path
- `projectNumber` is the GCP project number, or `-` for any project.
- `locationId` is a region or zone, or `-`.
- **Location check (same rule as Google Proxy):** the region must be this Core's `LOCAL_REGION`, otherwise 400. A zone does **not** filter rows.

### What rows you get
- Rows come back **as stored**, including `CREATING`, `UPDATING`, `ERROR` and soft-deleted rows. Deleted rows have `deletedAt` set; live rows have `deletedAt: null`.
- The data is the VCP DB only. There is **no CVP merge**: for backup, replication, AD and CMEK, CVP-only fields that Google Proxy shows are absent here.

### Get
- Send exactly one of `identity.uuid` or `identity.name`.
- **Get by uuid** works with `projects/-`. With a project number, a resource of another project returns 404.
- **Get by name** needs a project number. Some types also need the parent that makes the name unique:

  | Type | Required parent |
  |---|---|
  | volume | `pool` |
  | snapshot, replication | `volume` |
  | backup | `backupVault` |

  - It returns the live row first, else the most recently deleted row.
  - Two live rows with the name return 409.
  - Not supported for `cmek` and `job` (use uuid).

### List
- Keyset paging ordered by internal id. `pageSize` defaults to **10**; values above **50** are clamped to 50. A negative value returns 400.
- `nextPageToken` is empty on the last page.
- A token is only valid with the same project and filters. It is base64 and not signed: a caller can decode the internal row id in it.
- `includeDeleted` (default `false`) adds soft-deleted rows.
- **Parents** (AND-ed, at most one per type):

  | Type | Allowed parents |
  |---|---|
  | pool | `cmek`, `activeDirectory`, `host` |
  | volume | `pool`, `svm`, `volumePerformanceGroup`, `backupVault`, `backupPolicy` |
  | snapshot | `volume` |
  | replication | `volume`, `sourceVolume`, `destinationVolume` |
  | backup | `backupVault` (required), `volume` |
  | job | `resource` |

- `filters.name` and `filters.state` are exact matches. `correlationId` and `type` are for jobs only. `cmek` and `job` have no name filter.
- `timeFilters` on `createdAt` / `updatedAt` use the half-open range `[from, to)`, in UTC.
- A project number is required, except for `job`. A job list with `projects/-` must send at least one other filter: a `resource` parent, `correlationId`, `state`, `type`, or a time filter. Admin jobs have no project, so this is the only way to reach them.

### Job and Temporal
Get-job adds a read-only describe of the job's Temporal workflow (5 s budget, one call; two for the sequencer check). It returns execution metadata only:

- **Workflow:** ids (run, first run, parent, root), type, task queue, status, times, history length and size, state transitions, timeouts, run and execution expiration, cancel requested, last reset, worker deployment / build id / versioning behavior.
- **Pending workflow task:** state, attempt, times. A high attempt means workers keep failing it.
- **Each pending activity:** id, type, state, attempt / maximum attempts, last heartbeat, next retry time, retry interval, expiration, paused, last worker identity, last failure **type**.
- **Each pending child:** workflow id, run id, type, parent close policy.

Workflow input/result, memo, search attribute values, user metadata, heartbeat details, failure messages and history events are never returned. Completed activities are not shown, because they live only in the workflow history.

`temporal.enrichmentStatus` is one of:

| Status | Meaning |
|---|---|
| `COMPLETE` | Describe worked. `stateMismatch` is true when the DB job is still NEW/PROCESSING but the run is closed. |
| `SKIPPED` | The job has no workflow id. Every list row is also `SKIPPED`. |
| `NOT_FOUND` | The workflow does not exist in Temporal. |
| `QUEUED_IN_SEQUENCER` | A NEW sequencer-backed job whose own workflow is missing, while the per-pool sequencer is running. |
| `UNAVAILABLE` | Temporal error or timeout. The call still returns 200. |

### Errors and cloud
- **Error format:** every error is `{"code": N, "message": "..."}`, including requests rejected by schema validation (`invalid request: ...`).
- **GCP only:** on OCI both operations return 501.

## Security
- **Explicit field allowlists:** every response is built field by field from reviewed `Ops*_v1` schemas.
- **Never returned:**
  - credentials and secret or certificate IDs
  - KMS grants and key data
  - HMAC keys
  - AD passwords and credential paths
  - job payloads and raw error text
  - raw Temporal payloads
- **Withheld text:** free-text state details are replaced by `stateDetailsWithheld: true`.
- **Audit:** one `ops-inventory: read completed` log line per call, with no response body. Note that the shared Core access log (`LoggingHttpHandler`) still logs request and response bodies for every route.
- **Auth:** the normal Core JWT middleware (`GCP_AUTH_ACCEPTED_SERVICE_ACCOUNTS`). The route is not in the auth skip list.

## Example

```bash
curl -s -X POST "$CORE/v1/internal/projects/1111/locations/us-east4/resources:list" -H "Content-Type: application/json" -d '{"resourceType":"volume","parents":[{"type":"pool","id":"<pool uuid>"}],"pageSize":25}'
```

## Changing the API: regenerate code

After editing `vcp-core/api.yaml`, run from the repo root:

```bash
make generate-core-api
```

This regenerates **both** `vcp-core/servergen` (server) and `clients/core-api` (client) with **ogen v1.10.1**, the version the committed code was generated with.

```bash
make generate-mocks
```

This regenerates the mockery mocks (mockery v2.53.4), including `servergen.MockHandler`, `coreapi.MockInvoker` and `readstore.MockReadStore`. Run it when an interface changes, for example after adding an operation.

Notes:
- **Don't use `go generate` in `vcp-core/`.** `vcp-core/generate.go` points at `ogen@latest`, which regenerates every file with a different ogen version.
- **CI checks this.** CI runs `make verify-generated` and fails if generated files are not committed, so commit `servergen/` and `clients/core-api/` together with `api.yaml`.
