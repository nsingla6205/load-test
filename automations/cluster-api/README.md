# VSA Cluster API Automation

Extensible SRE tooling for authenticated VCP Core API operations. The initial
catalog covers image listing and registration, cluster upgrades, and
post-operation verification. It consumes existing APIs and does **not**
reimplement their workflows.

Three catalogs, one per environment:

| File | Environment | Project id pattern |
|---|---|---|
| `config.autopush.yaml` | autopush tst | `netapp-{location}-autopush-sde-tst` |
| `config.stage.yaml` | staging | `netapp-{location}-staging-sde` |
| `config.prod.yaml` | production | `netapp-{location}-sde` |

Each file holds the reviewed image catalog (`image_versions`, `target_image`), rollout defaults (`batch`, `poll`), the write gate (`governance`), and that environment's producer map under `projects`. Select with `--environment` and `--project`.

**Platform contract (existing, do not duplicate):** [`doc/architecture/vsa-cluster-upgrade-design.md`](../../doc/architecture/vsa-cluster-upgrade-design.md)

That document is already in `main` (VSCP-2533 single upgrade, VSCP-7128 batch resilience). It remains the source of truth for upgrade selection, waves, job lifecycle, and version resolution. This directory provides the generic API client and operation catalog used to automate the SRE runbook.

## Current execution model

The supported CI path is GitHub → Cloud Deploy → an in-cluster Job. GitHub
does not run `kubectl`, connect to the private GKE control plane, or
port-forward `core-service`.

The old `.github/workflows/vsa-cluster-api.yaml` direct-GKE workflow is
retained as a reference but is not the production execution path. Its WIF
principal cannot be bound to the regional SDE service accounts under the
current organization policy, and a GitHub runner cannot reach private GKE.

### us-e4 read-only POC

Dispatch `.github/workflows/vsa-cluster-api-cloud-deploy-poc.yaml`. It
uses:

- Cloud Deploy project `netapp-us-e4-staging-sde`;
- region `us-east4`;
- delivery pipeline `gcnv-vsa-cluster-api-stg-us-e4`;
- dedicated target `gcnv-vsa-cluster-api-stg-us-e4`, backed by the
  existing private `cloud-deploy-worker-pool` and targeting
  `netapp-us-e4-staging-sde/us-e4-gke-stg`;
- POC KSA `vcp/cluster-api-poc`;
- existing runtime GSA
  `otel-collector-sa@netapp-us-e4-staging-sde.iam.gserviceaccount.com`.

Provide the required `poc_image` dispatch input as a BusyBox-compatible image
already admitted by us-e4 Binary Authorization, including its
`@sha256:<digest>`. It must provide `/bin/sh`, `wget` with HTTPS/CA support,
`sed`, and `grep`. The workflow rejects tags.

The Cloud Deploy source is `cloud-deploy-poc/`. Its Job only:

1. calls `GET /health`;
2. obtains the Workload Identity access token from the metadata server;
3. calls IAM Credentials `signJwt` on the regional GSA;
4. calls authenticated `GET /v1/imageVersions`.

It prints only the health result and image-version count, never the JWT.
Each release gets a unique Job name and is cleaned up by Kubernetes TTL.

Prerequisite bindings:

- self `roles/iam.serviceAccountTokenCreator` on the POC GSA;
- `roles/iam.workloadIdentityUser` on that GSA for
  `serviceAccount:netapp-us-e4-staging-sde.svc.id.goog[vcp/cluster-api-poc]`;
- `roles/clouddeploy.releaser` for `vcp-ci` on the producer project;
- `roles/iam.serviceAccountUser` for `vcp-ci` on the delivery pipeline's
  execution SA;
- bucket-scoped Cloud Deploy release-artifact access for `vcp-ci`.

After the POC is green, use the dedicated
`cluster-api-sa@PROJECT_ID.iam.gserviceaccount.com` identity described in
the design document. Mutating batch dispatch remains disabled until that
identity, Core whitelist, protected approval environment, and exact-pool
review are in place.

### What the platform already provides

From the design doc (Phase 1 + Phase 2 **completed**):

| Capability | API / behavior |
|---|---|
| Single cluster upgrade | `POST /v1/clusters/{clusterId}/upgrade` — cluster ID **is** pool UUID |
| Upgrade status | `GET /v1/clusters/upgrade/{jobId}` |
| Batch upgrade | `POST /v1/clusters/batchUpgrade` — `percentage` or `poolList` |
| Batch status | `GET /v1/clusters/batchUpgrade/{batchUpgradeId}` |
| Image versions | `GET/POST /v1/imageVersions`, `DELETE /v1/imageVersions/{ontapVersion}` |
| Wave concurrency | `maxConcurrency` default **5** |
| Failure abort | `failureThreshold` default **0** (attempt every selected cluster); set `1+` to skip remaining waves |
| Batch filters | `zones`, `ontapVersions`, `clusterStates` only — **no** account / deployment / tags |
| Target for a normal upgrade | `VSA_IMAGE_NAME` matched to `image_versions.vsa_name` |
| API `current` / `isCurrent` | `ONTAP_VERSION_DETAILS` env, not the newest DB row |
| Prechecks | READY or DISABLED; zone-switch blocked; no active job; skip if already at target (unless `forceUpgrade`) |

**Design-doc future work (not this automation):** batch rollback, inter-wave delay, maintenance windows / scheduled upgrades, canary. Track those as VCP platform tickets if needed; do not invent them in this CLI.

## Scope alignment (runbook vs platform)

| Runbook step | Platform (design doc) | This CLI |
|---|---|---|
| Staging-before-prod gate | Process / manual | GitHub Environment approval (when prod is added) |
| Pool discovery (account, deployment, ONTAP version) | DB queries; batch API filters: `zones`, `ontapVersions`, `clusterStates` only | Explicit pool UUIDs or SQL via `discover` |
| JWT auth (SA impersonation + signJwt) | Core-api auth | `generate_token.py` |
| Read-only validation / register image | `GET /health`, `GET/POST /v1/imageVersions` | `list-images`, `register-image` |
| Single pool upgrade | `POST /v1/clusters/{clusterId}/upgrade` | `upgrade-cluster`, or `upgrade --pool-ids <one uuid>` |
| Regional batch (5 at a time) | `POST /v1/clusters/batchUpgrade` (`maxConcurrency` default 5) | `upgrade` |
| Poll job status | `GET /v1/clusters/upgrade/{jobId}` | After execute: poll until EXECUTION STATUS; re-read with `verify` |
| ONTAP CLI verification (`cluster image show`, `node show`) | Not in VCP API | **Gap** — manual or future ONTAP integration |
| Update `VSA_IMAGE_NAME` / deployment env | Helm / release process | **Gap** — documented prerequisite, not automated |
| Customer communication | TBD | **Out of scope** |

### Use cases

1. **Targeted upgrade** — one customer / account / pool: supply pool UUID(s) via config or SQL discovery.
2. **Regional rollout** — percentage-based batch with ONTAP version filter, waves of 5, optional failure threshold.

## Gaps (runbook vs design — operational only)

These are **operator** gaps. The upgrade engine itself is implemented per the design doc.

1. **Two-step target version** — Design: normal upgrade uses env `VSA_IMAGE_NAME` / `VSA_MEDIATOR_IMAGE_NAME`; `current` is `ONTAP_VERSION_DETAILS`. Registering `image_versions` alone does not change the default target. Rollout = **release/Helm env + DB row**.
2. **Customer / account / deployment selection** — Design: batch filters are zone / ONTAP version / cluster state only. Runbook SQL uses `account_id` and `deployment_name`. Automation uses `discover` → `poolList`.
3. **Auth and reachability** — JWT via SA impersonation; core-api via `--base-url` (runbook: `kubectl port-forward`). Not part of the upgrade design.
4. **ONTAP CLI health / post-verify** — Design prechecks are VCP-side (state, zone switch, jobs, build images). CPU / SAN / sensors / mgmt LIF and `cluster image show` stay manual unless a later VLM probe is added.
5. **Staging-before-prod** — Process only. GH Environment protection for prod; this catalog is autopush + staging.
6. **Customer communication** — TBD in runbook; out of scope.

## Automation plan (phased)

Constraint: **consume** the design-doc APIs. Do not add a second upgrade implementation. Platform items below are optional follow-ups **only** if SRE cannot operate without them; they belong in VCP tickets, not this CLI.

### Phase 0 — This directory (VSCP-8017)

- CLI: registry-driven operations, read ones unattended and writes gated by `--dry-run` plus a change ticket
- Staging E2E: one pool + small `poolList` batch
- Document runbook step → API mapping (this README)

### Phase 1 — GitHub Actions kick-off (see scaling below)

- Checked-in catalogs `config.autopush.yaml`, `config.stage.yaml`, `config.prod.yaml`
- `workflow_dispatch` to run an operation (default: `list-images`)
- **One GH job per producer project**; VCP batch API upgrades all clusters in that project
- GitHub Environment protection for prod (required reviewers + staging attestation)

### Phase 2 — Optional VCP (only if operators hit a wall)

Do **not** start these until Phase 0/1 prove the existing APIs are insufficient:

- `accountId` / `deploymentName` on batch filters (design today: not supported)
- Eligibility count API (pools by ONTAP version) so discovery is not SQL-only
- Post-upgrade ONTAP version probe via VLM (design: job `COMPLETED` is the API signal)

### Phase 3 — Align with design-doc “Future Enhancements”

Already listed in the design doc; separate VCP work:

- Scheduled / maintenance-window upgrades
- Inter-wave delay
- Rollback (explicitly **not implemented** in the design)

Release process: keep `image_versions` insert in lockstep with `VSA_IMAGE_NAME`, `VSA_MEDIATOR_IMAGE_NAME`, and `ONTAP_VERSION_DETAILS` (see design “Version Management”).

## GitHub Actions: scale across projects and clusters

**Rule:** GH Actions orchestrates **producer projects**. VCP Temporal batch workflows orchestrate **clusters inside a project**. Do not loop pools in YAML.

Each VSA region is its own GCP producer project, VCP, `image_versions` table, and core-api. There is no cross-region upgrade API.

```
workflow_dispatch (operation + selection)
        │
        ▼
  load config.<env>.yaml  ──filter projects──► matrix (≤15 jobs)
        │
        ├── list-images     GET  /v1/imageVersions
        ├── register-image  POST /v1/imageVersions          (per project)
        ├── upgrade         ONE POST /v1/clusters/batchUpgrade per project
        │                     └── VCP waves of maxConcurrency until all selected pools are done
        │                     └── CLI polls to COMPLETED / FAILED / CANCELLED
        └── verify          GET  /v1/imageVersions + the progress endpoint for the batch id
```

Same pattern as `vsa-mediator-image-copy.yml`: `workflow_dispatch` + environment choice + per-target work. Catalog lives in git like `config/VSA-Image/`.

### Scale across 15+ regions / projects

| Layer | How |
|---|---|
| Inventory | `config.autopush.yaml` / `config.stage.yaml` / `config.prod.yaml` — keyed by SDE project id; each entry has `region`, `short_name`, `project_number`, `cluster`, `service_account`. |
| Matrix | GH `strategy.matrix.project` from that catalog. |
| Parallelism | **Do not** start all prod regions at once. `max-parallel: 2` (staging can be higher). Canary project first, then the rest. |
| Auth | WIF (`id-token: write`) + **per-project** `service_account` from the catalog. That regional SA runs gcloud/kubectl and signs the JWT. Do not use a shared org SA. Each regional SA needs `roles/iam.workloadIdentityUser` for this workflow's WIF principal, `iam.serviceAccountTokenCreator` on itself, and GKE access in its producer project. |
| Reach core-api | There is **no** public URL (`core-service` is ClusterIP). Reuse **image-copy IAP**, not cleanup. See [How GH reaches core-api](#how-gh-reaches-core-api). |
| Staging → prod | Use `config.stage.yaml` then `config.prod.yaml`. GitHub Environment reviewers for prod. |
| Timeouts | The job polls to a terminal status, bounded by `poll.timeout_minutes` in the config (the 740-minute job timeout is that plus headroom). For a 100-cluster region (~1h/pool × waves of 5), either raise the config budget or let it time out and re-read with `verify --progress-id <batchUpgradeId>`. |
| Long-poll stability | `kubectl port-forward` is supervised and auto-restarts; the CLI tolerates up to 5 consecutive failed polls before giving up. |
| Token expiry | A poll can outlive the JWT. The CLI caches the token and re-signs it 15 min before `exp`, so a multi-hour run keeps authenticating without one `signJwt` call per request. WIF source credentials refresh from the Actions OIDC endpoint for the life of the job. |

Dispatch inputs (`workflow_dispatch` allows at most 10, and the workflow uses all 10):

- `environment`: `autopush` \| `stage` \| `prod` (loads `config.<environment>.yaml`)
- `project`: producer project id, a `config.projects` key
- `operation`: `list-images` \| `verify` \| `register-image` \| `upgrade`
- `ontap_version`: target version; must exist in `image_versions`. Also pins the build for `upgrade`
- `pool_ids`: comma or space separated pool UUIDs (`poolList` selection)
- `percentage`: 1-100 share of eligible pools, used when `pool_ids` is empty
- `max_concurrency`: `1` \| `2` \| `5` \| `10`
- `progress_id`: batch or job id for `verify`
- `change_ticket`: change record; required for a write in prod
- `dry_run`: a write prints its request body instead of sending it

Everything else is a reviewed config value rather than a dispatch field, because
the input cap is hard at 10 and these are the settings that should not change per
run: `failure_threshold` and `max_clusters_cap` in `batch`, the poll budget in
`poll`, the write gate in `governance`, and the GKE cluster in `projects.<id>.cluster`.
Multi-project matrix support would need further consolidation.

### Scale across all clusters in one project

Use the **existing** batch API once per project. VCP already waves internally (`maxConcurrency` default 5). That replaces the SRE “5 pools at a time” script.

| Rollout shape | Request |
|---|---|
| Full region (all eligible) | `selectionType=percentage`, `percentage=100`, `filters.ontapVersions=[exact versions]`, `maxConcurrency=5` |
| Canary in a region | Same filters, `percentage=5` or `maxClusters=3`, `failureThreshold=1` |
| Named customers | `discover` SQL → `selectionType=poolList` (no account filter on the API) |
| After canary | Second dispatch: `percentage=100` (already-at-target pools are skipped by VCP prechecks) |

`failureThreshold`: SRE start at **1** (abort remaining waves). After a green canary, use **0** only when you accept finishing the region despite failures (design-doc default).

Do **not**:

- Fan out one GH matrix cell per pool (15 regions × hundreds of pools blows GH limits and bypasses VCP job/claim logic)
- Run 15 prod `percentage=100` jobs with `max-parallel` unlimited
- Treat `COMPLETED` on one staging pool as a prod gate for all regions — gate **per location** (or require all staging locations)

### Recommended rollout sequence

1. Dry-run matrix, staging + prod — connectivity, JWT, and env vs `image_versions` validation.
2. Register image **and** confirm Helm/`VSA_IMAGE_NAME` already rolled to that project.
3. Staging **one** location, a single pool via `pool_ids`.
4. Staging that location, `percentage=100`.
5. Remaining staging locations, `max-parallel=2`.
6. Prod canary location, required reviewers.
7. Remaining prod, still `max-parallel=2`.

### How GH reaches core-api

`core-service` is **ClusterIP** (`kubernetes/vsa-control-plane/charts/core`: `service.type: ClusterIP`). In-cluster callers use `http://core-service.vcp.svc.cluster.local:8080`. No existing GH workflow HTTP-calls that Service.

| Workflow | Reuse | Does not |
|---|---|---|
| `weekly_gcp_resources_cleanup.yaml` | Self-hosted `[self-hosted, linux]`, `google-github-actions/auth` WIF | Artifact Registry only (`AR_GCP_PROJ`). No GKE, no producer VCP. |
| `image-copy-vm.yml` / `vsa-mediator-image-copy.yml` | Same WIF action, then `gcloud … --tunnel-through-iap` to a VM with **no external IP** | Private compute, not VCP ClusterIP. |
| `integrationtest.yaml` / `promote.yaml` | Self-hosted + builder image | Build/test/tag. No regional GKE. |

**Stack to reuse:**

1. **Runner** — `[self-hosted, linux]` (cleanup).
2. **GCP auth** — `google-github-actions/auth@v2` + WIF, `service_account` = catalog `projects.<id>.service_account`. That regional SA must be a WIF target, have GKE access in its producer project, `iap.tunnelResourceAccessor` if the master is private, and `iam.serviceAccounts.signJwt` on itself.
3. **Enter the cluster** — `gcloud container clusters get-credentials` on catalog `cluster` / `region` / project id (Connect Gateway or IAP to a private control plane).
4. **Call core-api** — no public HTTPS. Either `kubectl -n vcp port-forward svc/core-service 8080:8080` for the step, or `kubectl -n vcp run` a pod that curls `http://core-service.vcp.svc.cluster.local:8080` (better if Istio mTLS blocks port-forward).

Laptop runbook port-forward is the same ClusterIP; GH uses WIF+IAP instead of a human kubeconfig. Do **not** put core-api on a public LoadBalancer. Upgrade APIs are core `/v1/clusters/…`, not google-proxy.

### Autopush GitHub workflow

`.github/workflows/vsa-cluster-api.yaml` starts with autopush only.

1. Dispatch `VSA Cluster API Operations`.
2. Use project `netapp-au-se1-autopush-sde-tst` (default) or another confirmed autopush map entry.
3. Run `list-images` first. It mints a JWT, performs only GET requests, and prints the registered catalog.
4. Run `register-image` with the target `ontap_version` (use `dry_run` once to see the exact body).
5. Run `upgrade` with a single `pool_ids` entry and `max_concurrency=1`. It executes and polls until terminal.
6. Run `verify` with the printed `batchUpgradeId`, then widen to `percentage` waves.

Required repository variables (already set for the build pipelines):

- `AR_GCP_PROJ_NUM`, `AR_WLI_POOL`, `AR_WLI_PROV`

The workflow does **not** use a shared `GCP_SERVICE_ACCOUNT`. It authenticates as `projects.<id>.service_account` from the config. Each regional SA must allow that WIF principal as `roles/iam.workloadIdentityUser`:

```bash
gcloud iam service-accounts add-iam-policy-binding \
  otel-collector-sa@<producer-project>.iam.gserviceaccount.com \
  --project=<producer-project> \
  --role=roles/iam.workloadIdentityUser \
  --member='principalSet://iam.googleapis.com/projects/411435067244/locations/global/workloadIdentityPools/vsa-wli-pool1/attribute.repository/VCP-VSA-control-Plane/vsa-control-plane'
```

Do **not** use `GCP_WORKLOAD_IDENTITY_PROVIDER` here. That pool lives in `gcnv-vsa-prod`, and `constraints/iam.allowedPolicyMemberDomains` rejects its principals in SDE producer projects, which sit in a different org (`FAILED_PRECONDITION: ... not in permitted organization`). The `AR_WLI_POOL` provider is in the permitted org and its attribute condition already allows this repo.

The workflow looks up `region` and `cluster` from `config.projects.<id>` and discovers the GCP project number. If `cluster` is empty it falls back to the single non-`backup-restore` GKE cluster in that region, and fails closed when discovery is ambiguous; set `projects.<id>.cluster` in the config rather than passing it at dispatch.

## Operation catalog

The CLI dispatches a registry, so it runs core-api operations rather than
upgrades specifically:

| Layer | File | Responsibility |
|---|---|---|
| CLI and transport | `scripts/cluster_api.py` | Dispatch plus config and producer resolution, JWT minting and refresh, requests, progress polling, GitHub summary and annotations |
| Catalog | `scripts/operations.py` | Registry, shared helpers, and image ops until they get their own module |
| Upgrade module | `scripts/cluster_upgrade.py` | `upgrade`, `upgrade-cluster`, `verify`, `discover` |
| Dispatch surface | `.github/workflows/vsa-cluster-api.yaml` | Maps dispatch inputs onto the selected operation's flags |

Currently registered (`--operations` prints this list):

| Operation | Safety | Calls |
|---|---|---|
| `list-images` | read | `GET /health`, `GET /v1/imageVersions` |
| `verify` | read | `GET /v1/imageVersions` plus the progress endpoint for a given id |
| `discover` | read | VCP Postgres via `psql` (no core-api) |
| `register-image` | mutate | `POST /v1/imageVersions` |
| `upgrade` | mutate | `POST /v1/clusters/batchUpgrade`, then polls to terminal |
| `upgrade-cluster` | mutate | `POST /v1/clusters/{poolId}/upgrade`, then polls to terminal |

Safety classes are enforced, not decorative: `--dry-run` makes every `mutate`
operation print the request instead of sending it, and `governance.require_change_ticket`
(true in `config.prod.yaml`) blocks a `mutate` dispatch that carries no change record.

### Adding an operation

Add a domain module (or extend `cluster_upgrade.py` for upgrade work) and
register one decorated function, then list the module in `OPERATION_MODULES`
in `cluster_api.py`. The CLI generates flags from the declared `Param` list:

```python
import cluster_api
import operations
from operations import Param, SAFETY_READ


@operations.register(
    "list-pools",
    "GET /v1/pools: pools in the producer project",
    SAFETY_READ,
    params=(Param("state", "Filter by pool state", default="READY"),),
)
def op_list_pools(config, params):
    status, data = cluster_api.api_request(config, "GET", "/v1/pools")
    ...
```

Exposing it in CI means adding the name to the workflow's `operation` options
and mapping its flags in the run step. Everything else, including auth, token
refresh, correlation id, summaries and exit codes, is inherited.

## Quick start (staging)

### Prerequisites

- `gcloud auth application-default login` with permission to impersonate the project's `service_account` (`iam.serviceAccounts.signJwt`)
- Port-forward to core-api (or reachable `--base-url`):

```bash
kubectl port-forward svc/core-service -n vcp 8080:8080
```

- Python 3.10+ and dependencies:

```bash
cd automations/cluster-api
python3 -m venv .venv && source .venv/bin/activate
pip install -r requirements.txt
```

Use `config.autopush.yaml`, `config.stage.yaml`, or `config.prod.yaml` via `--environment`. `--ontap-version` selects an `image_versions` entry, so adding a release is a reviewed config change rather than image paths typed at dispatch time. `--target-vsa-name`, `--target-mediator-name` and `--target-vsa-image-path` still override individual fields for an unlisted build.

### Commands

```bash
# 0. What can this run?
python scripts/cluster_api.py \
  --environment autopush --project netapp-au-se1-autopush-sde-tst --operations

# 1. list-images — connect, mint JWT, GET-only, print the catalog
python scripts/cluster_api.py \
  --environment autopush \
  --project netapp-au-se1-autopush-sde-tst \
  list-images

# 2. register-image — target comes from image_versions in config
python scripts/cluster_api.py \
  --environment autopush \
  --project netapp-au-se1-autopush-sde-tst \
  --ontap-version 9.18.1 \
  register-image

# 3a. Canary: named pools, one at a time (executes, polls to terminal)
python scripts/cluster_api.py \
  --environment autopush \
  --project netapp-au-se1-autopush-sde-tst \
  --ontap-version 9.18.1 \
  upgrade --pool-ids <pool-uuid> --max-concurrency 1

# 3b. Regional wave: share of eligible pools, filtered by source version
python scripts/cluster_api.py \
  --environment autopush \
  --project netapp-au-se1-autopush-sde-tst \
  --ontap-version 9.18.1 \
  upgrade --percentage 25 --from-ontap 9.17.1P1,9.17.1P2 --max-concurrency 5

# 4. verify — image registered and active, and the batch ended COMPLETED
python scripts/cluster_api.py \
  --environment autopush \
  --project netapp-au-se1-autopush-sde-tst \
  --ontap-version 9.18.1 \
  verify --progress-id <batch-upgrade-id>
```

Add `--dry-run` to any write to print the exact request body without sending it.

`--percentage` is the share of *eligible* pools to select. `--from-ontap` filters
eligibility by the pool's current ONTAP version and is matched with SQL `IN`, so each
version must be spelled exactly as stored in `build_info->>'ontapVersion'`
(`9.17.1P1`, not `9.17.1`). Use `discover` to read the real values first; omit the flag
to take every eligible pool. `--pool-ids` and `--percentage` are mutually exclusive:
pools give `poolList` selection, a percentage gives `percentage` selection.

Write operations execute and then poll until the job reaches `COMPLETED`, `FAILED`, or
`CANCELLED`. Each poll prints the batch status plus per-cluster status counts. On a
non-successful outcome the CLI prints every `errors[]` entry (code, message, type,
retryable), each failed cluster with the step it died on, and any `warnings[]`, then
exits non-zero. A `COMPLETED` batch that contains a failed child also exits non-zero.
The poll budget comes from `poll.timeout_minutes` in the config; dispatch cannot extend it.

`verify` re-reads a finished run: pass the `batchUpgradeId` (or a single-cluster
`jobId`, which it probes for automatically) as `--progress-id`. A pool UUID is not a
progress id. `UpgradeProgress_v1` carries `clusterId`, `status` and `currentStep` only,
so `verify` cannot confirm the per-pool ONTAP version and says so; that check stays on
the ONTAP side.

By default, upgrades use the target configured in the deployed VCP environment.
Passing `--ontap-version` with `--force-upgrade` pins the CLI's VSA and mediator names
instead. The workflow adds `--force-upgrade` automatically whenever `ontap_version` is
supplied, because the API requires that flag for an explicit build-image pair.

### Pool discovery (SQL)

Equivalent to the runbook query — run against VCP Postgres, paste UUIDs into config or pass to batch `poolList`:

```sql
SELECT uuid, name, account_id, deployment_name,
       build_info->>'ontapVersion' AS ontap_version
FROM pools
WHERE deleted_at IS NULL
  AND state = 'READY'
  AND build_info->>'ontapVersion' LIKE '9.17.1%';
```

Or use `discover` (requires `psql` and `database` section in config):

```bash
python scripts/cluster_api.py \
  --environment autopush \
  --project netapp-au-se1-autopush-sde-tst \
  discover --ontap-version-prefix 9.17.1
```

## Safety defaults

- Read operations (`list-images`, `verify`, `discover`) never send a write, so they are safe to run unattended
- `--dry-run` turns any write into a printed request body and exits 0 with zero mutations
- `governance.require_change_ticket` is **true** in `config.prod.yaml`: a prod write without `--change-ticket` fails before the request, and the workflow repeats the same check before it touches the cluster
- The ticket travels with the request as `metadata.changeTicket` plus the dispatching actor, and `x-correlation-id` is the GitHub run id, so a rollout is traceable in VCP logs
- `register-image` is idempotent: an already-registered version is reported and skipped instead of re-POSTed
- Selection is explicit: `pool_ids` or `percentage`, never both, and `batch.max_clusters_cap` caps a percentage wave regardless of dispatch
- Writes poll until `COMPLETED` / `FAILED` / `CANCELLED`. Exit code is non-zero for `FAILED`, `CANCELLED`, any failed child cluster, poll timeout, or repeated poll errors, so a GitHub job cannot pass on a failed upgrade
- Failure details go to the GitHub run summary and as `::error::` annotations; a poll timeout prints the progress id to re-read with `verify`
- Config `failure_threshold` default **1** (SRE cautious rollout). Design-doc API default is **0** (no abort). Set `0` only when a full region must be attempted.
- `list-images` and `verify` flag a mismatch between `deployment.vsa_image_name` and the registered `vsaName` (design: the deployment env is the real target)

## Non-prod validation checklist

- [ ] `list-images` reaches core-api, JWT is accepted, and prints the registered versions
- [ ] `register-image --dry-run` prints the expected body, then the real run returns `201` (or reports the version as already registered)
- [ ] `upgrade` with one `pool_ids` entry returns `202` and reaches `COMPLETED`
- [ ] `upgrade --percentage` with `maxConcurrency=5` processes a small wave (2–3 pools)
- [ ] `verify --progress-id <batchUpgradeId>` passes after that wave and fails when pointed at a failed job
- [ ] Failure path: invalid pool id → `409` surfaced clearly
- [ ] Document time-to-complete per pool for capacity planning
