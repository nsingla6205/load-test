# VSA Cluster Upgrade Automation

SRE operational tooling for the **already-shipped** VCP cluster upgrade platform. This does **not** redesign or reimplement upgrade workflows.

Three catalogs, one per environment:

| File | Environment | Project id pattern |
|---|---|---|
| `config.autopush.yaml` | autopush tst | `netapp-{location}-autopush-sde-tst` |
| `config.stage.yaml` | staging | `netapp-{location}-staging-sde` |
| `config.prod.yaml` | production | `netapp-{location}-sde` |

Each file holds rollout defaults (`target_image`, `batch`) and that environment's producer map under `projects`. Select with `--environment` and `--project`.

**Platform contract (existing, do not duplicate):** [`doc/architecture/vsa-cluster-upgrade-design.md`](../../doc/architecture/vsa-cluster-upgrade-design.md)

That document is already in `main` (VSCP-2533 single upgrade, VSCP-7128 batch resilience). It is the source of truth for APIs, selection, waves, job lifecycle, and version resolution. This directory only automates the **manual SRE runbook** around those APIs.

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
| Read-only validation / register image | `GET /health`, `GET/POST /v1/imageVersions` | `--dry-run`, `register-image` |
| Single pool upgrade | `POST /v1/clusters/{clusterId}/upgrade` | `upgrade-pool` |
| Regional batch (5 at a time) | `POST /v1/clusters/batchUpgrade` (`maxConcurrency` default 5) | `batch-upgrade` |
| Poll job status | `GET /v1/clusters/upgrade/{jobId}` | After execute: poll until EXECUTION STATUS |
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

- CLI: `--dry-run` (GET + STATUS) or a command without `--dry-run` (execute + EXECUTION STATUS)
- Staging E2E: one pool + small `poolList` batch
- Document runbook step → API mapping (this README)

### Phase 1 — GitHub Actions kick-off (see scaling below)

- Checked-in catalogs `config.autopush.yaml`, `config.stage.yaml`, `config.prod.yaml`
- `workflow_dispatch` to start a rollout (default action: `dry-run`)
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
workflow_dispatch
        │
        ▼
  load config.<env>.yaml  ──filter projects──► matrix (≤15 jobs)
        │
        ├── --dry-run (GET-only, print current status)
        ├── register-image (execute, print image-version status)
        └── upgrade (execute, poll until EXECUTION STATUS)
        ├── register-image (opt, per project)   POST /v1/imageVersions
        └── upgrade (bounded parallel)
              └── ONE POST /v1/clusters/batchUpgrade per project
                    └── VCP waves of maxConcurrency (default 5) until all eligible pools done
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
| Timeouts | The job polls to a terminal status, bounded by `poll_timeout_minutes` (job timeout is that plus headroom). For a 100-cluster region (~1h/pool × waves of 5), either raise the budget or let it time out and resume with `batch-status` on the printed `batchUpgradeId`. |
| Long-poll stability | `kubectl port-forward` is supervised and auto-restarts; the CLI tolerates up to 5 consecutive failed polls before giving up. |
| Token expiry | A poll can outlive the JWT. The CLI caches the token and re-signs it 15 min before `exp`, so a multi-hour run keeps authenticating without one `signJwt` call per request. WIF source credentials refresh from the Actions OIDC endpoint for the life of the job. |

Dispatch inputs (`workflow_dispatch` allows at most 10, current workflow uses 9):

- `environment`: `autopush` \| `stage` \| `prod` (loads `config.<environment>.yaml`)
- `project`, `action`: `dry-run` \| `register-image` \| `canary-upgrade` \| `regional-upgrade` \| `pool-status` \| `batch-status`
- `from_ontap`: exact source ONTAP versions, comma-separated
- `target_image`: comma-separated `key=value` overrides (`ontap_version`, `vsa_image_path`, `vsa_name`, `mediator_name`); empty falls back to config
- `force_upgrade`, `progress_id` (required by the status actions), `gke_cluster`
- `poll_timeout_minutes`: a choice (60 / 120 / 240 / 480 / 720) because GitHub expressions cannot do arithmetic; the job timeout is looked up from it and is always larger
- Adding multi-project matrix support means consolidating inputs further, since 10 is the hard cap.

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
3. Staging **one** location, `maxClusters=1` (or `upgrade-pool`).
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

`.github/workflows/vsa-cluster-upgrade.yaml` starts with autopush only.

1. Dispatch `VSA Cluster Upgrade`.
2. Use project `netapp-au-se1-autopush-sde-tst` (default) or another confirmed autopush map entry.
3. Run with `--dry-run` first. It mints a JWT, performs only GET requests, and prints **STATUS**.
4. After that is green, run `canary-upgrade`. It executes and polls until **EXECUTION STATUS** is terminal.
5. Only then run `regional-upgrade`.

Required repository secret:

- `GCP_WORKLOAD_IDENTITY_PROVIDER`

The workflow does **not** use a shared `GCP_SERVICE_ACCOUNT`. It authenticates as `projects.<id>.service_account` from the config. That WIF principal must be allowed to impersonate each regional SA (`roles/iam.workloadIdentityUser`). Existing image-copy IAM does not cover this.

The workflow looks up `region` and `cluster` from `config.projects.<id>`. It discovers the GCP project number. If `cluster` is empty, it falls back to a unique GKE cluster whose name contains `lmgmt`, unless `gke_cluster` is supplied at dispatch. It fails closed if discovery is ambiguous.

## Quick start (staging)

### Prerequisites

- `gcloud auth application-default login` with permission to impersonate the project's `service_account` (`iam.serviceAccounts.signJwt`)
- Port-forward to core-api (or reachable `--base-url`):

```bash
kubectl port-forward svc/core-service -n vcp 8080:8080
```

- Python 3.10+ and dependencies:

```bash
cd automations/cluster-upgrade
python3 -m venv .venv && source .venv/bin/activate
pip install -r requirements.txt
```

Use `config.autopush.yaml`, `config.stage.yaml`, or `config.prod.yaml` via `--environment`. Target image command-line flags override `target_image`; omitted flags fall back to that file. GitHub Actions can still pass a release image at dispatch without committing a change.

### Commands

```bash
# 1. Dry-run — connect, mint JWT, GET-only, print STATUS (no mutation)
python scripts/cluster_upgrade.py \
  --environment autopush \
  --project netapp-au-se1-autopush-sde-tst \
  --dry-run

# 2. Register target image (executes, then prints EXECUTION STATUS)
python scripts/cluster_upgrade.py \
  --environment autopush \
  --project netapp-au-se1-autopush-sde-tst \
  --target-ontap-version 9.18.1 \
  --target-vsa-image-path GCNV/9.18.1/upgrade/cot.image.ONTAP-9.18.1.tgz \
  --target-vsa-name x-9-18-1 \
  --target-mediator-name cvo-mediator-x-9-18-1 \
  register-image

# 3a. Single pool upgrade (executes, polls until EXECUTION STATUS)
python scripts/cluster_upgrade.py \
  --environment autopush \
  --project netapp-au-se1-autopush-sde-tst \
  upgrade-pool --pool-id <pool-uuid>

# 3b. Regional batch (executes, polls until EXECUTION STATUS)
python scripts/cluster_upgrade.py \
  --environment autopush \
  --project netapp-au-se1-autopush-sde-tst \
  batch-upgrade \
  --selection-type percentage \
  --percentage 25 \
  --ontap-versions 9.17.1P1,9.17.1P2

# 4a. Check one pool upgrade using jobId returned by upgrade-pool
python scripts/cluster_upgrade.py \
  --environment autopush \
  --project netapp-au-se1-autopush-sde-tst \
  pool-status --job-id <job-id>

# 4b. Check a batch using batchUpgradeId returned by batch-upgrade
python scripts/cluster_upgrade.py \
  --environment autopush \
  --project netapp-au-se1-autopush-sde-tst \
  batch-status --batch-id <batch-upgrade-id>
```

`--percentage` is the share of *eligible* pools to select. `--ontap-versions` filters
eligibility by the pool's current ONTAP version and is matched with SQL `IN`, so each
version must be spelled exactly as stored in `build_info->>'ontapVersion'`
(`9.17.1P1`, not `9.17.1`). Use `discover` to read the real values first; omit the flag
to take every eligible pool.

Mutating commands execute and then poll until the job reaches `COMPLETED`, `FAILED`, or
`CANCELLED`. Each poll prints the batch status plus per-cluster status counts. On a
non-successful outcome the CLI prints every `errors[]` entry (code, message, type,
retryable), each failed cluster with the step it died on, and any `warnings[]`, then
exits non-zero. A `COMPLETED` batch that contains a failed child also exits non-zero.

The status commands accept `--wait` to poll an already-running job the same way; without
it they do a single read. A pool UUID is not a progress ID: save the `jobId` returned by
`upgrade-pool`. For a batch, save `batchUpgradeId` (also returned as `jobId`).

By default, upgrades use the target configured in the deployed VCP environment.
To upgrade to the CLI-provided VSA and mediator names instead, add `--force-upgrade`
to `upgrade-pool` or `batch-upgrade` (or enable `force_upgrade` in the workflow).
The API requires this flag for explicit build-image pairs.

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
python scripts/cluster_upgrade.py \
  --environment autopush \
  --project netapp-au-se1-autopush-sde-tst \
  discover --ontap-version-prefix 9.17.1
```

## Safety defaults

- `--dry-run` is GET-only (`/health`, `/v1/imageVersions`) and prints **STATUS**
- Without `--dry-run`, mutations run and poll until **EXECUTION STATUS** is `COMPLETED` / `FAILED` / `CANCELLED`
- Exit code is non-zero for `FAILED`, `CANCELLED`, any failed child cluster, poll timeout, or repeated poll errors, so a GitHub job cannot pass on a failed upgrade
- Failure details go to the GitHub run summary and as `::error::` annotations; a poll timeout prints the progress id to resume with
- Config `failure_threshold` default **1** (SRE cautious rollout). Design-doc API default is **0** (no abort). Set `0` only when a full region must be attempted.
- `upgrade-pool` warns when `deployment.vsa_image_name` / `ontap_version_details` do not match `GET /v1/imageVersions` (design: env is the real target)

## Non-prod validation checklist

- [ ] `--dry-run` reaches core-api, JWT is accepted, and prints STATUS from `image_versions`
- [ ] `register-image` returns `201` (or `409` if the version already exists)
- [ ] Single pool upgrade returns `202` and reaches `COMPLETED`
- [ ] Batch upgrade with `maxConcurrency=5` processes a small `poolList` (2–3 pools)
- [ ] Failure path: invalid pool id → `409` surfaced clearly
- [ ] Document time-to-complete per pool for capacity planning
