# VCP build process and cadence

This repo has **two unrelated “build” pipelines**. Mixing them up is the usual source of confusion during cluster upgrades.

| Pipeline | What it produces | Cadence |
|---|---|---|
| **VCP software** | Docker images + Helm charts for `core`, proxies, `vcp-worker`, … | PR on every change; tagged images on every merge; weekly RC cut |
| **VSA / ONTAP compute images** | GCE images + upgrade tarball (`x-9-18-1`, `cot.image.ONTAP-….tgz`) | **Not on a cron in this repo.** Manual `workflow_dispatch` after seclab publishes a build |

The ONTAP bits that `automations/cluster-upgrade` registers (`target_image`) come from the **second** pipeline. Deploying a new VCP Helm chart comes from the **first**.

---

## 1. VCP software (this GitHub repo)

### 1.1 How often things run

| Event | Workflow | Frequency |
|---|---|---|
| Open / update a PR to `main`, `release/2*`, or `release/oci/2*` | [`main.yaml`](../../.github/workflows/main.yaml) | Every PR synchronize. Concurrent runs on the same branch are cancelled. |
| Merge to `main` or `release/2*` / `release/oci/2*` | [`release.yaml`](../../.github/workflows/release.yaml) | Every merge. Creates the next `*-DEV.*` or `*-RC.*` tag and publishes images. |
| Weekly sprint cut | [`weeklyReleaseCron.yaml`](../../.github/workflows/weeklyReleaseCron.yaml) | **Friday 02:30 UTC** (`cron: '30 2 * * 5'`). Also runnable by hand. |
| Promote RC → GA | [`promote.yaml`](../../.github/workflows/promote.yaml) | **Manual** (`workflow_dispatch`). |
| Rebuild the CI builder image `vsa-builder:v9` | [`build-vsa-builder.yaml`](../../.github/workflows/build-vsa-builder.yaml) | **Manual**. |

Housekeeping (not product builds): GCP resource cleanup Monday 00:00 UTC; self-hosted runner cleanup every 3 hours.

### 1.2 What a PR build does

[`main.yaml`](../../.github/workflows/main.yaml) does **not** publish a release. It gates merge:

1. Jira ticket check (`vsacictl jira allow_merge`)
2. Unit-test shards derived from `go.work`
3. `make verify-generated`, ONTAP client / retry-engine checks
4. Lint (`vsacictl lint`)
5. **Verify build**: `make build-all-binaries-prod` + `skaffold build` with tag `test-build` (images are not the weekly release)
6. Integration tests ([`integrationtest.yaml`](../../.github/workflows/integrationtest.yaml))

Path-filtered extras: Helm chart validation on `kubernetes/**`, SQL migration pair checks on `database/**/migrations/**`.

All of these jobs run in `ghcr.io/vcp-vsa-control-plane/vsa-builder:v9` (or Ubuntu for a few lightweight steps).

### 1.3 What a merge / weekly cut publishes

Reusable publisher: [`build_common.yaml`](../../.github/workflows/build_common.yaml).

Default images:

`core`, `google-proxy`, `vcp-db-migrate`, `vcp-iam-lifecycle`, `vcp-worker`, `telemetry`, `vcp-cloudrun-deployer`, `ontap-proxy`, `vcm-proxy`

Also: Helm chart push, GitHub pre-release or release, **Black Duck + Grype** (see [§4](#4-security-scans)), Cosign signing, Slack (unless disabled). `build_target` is `gcp`, `oci`, or `both`.

### 1.4 Tag and branch model

Implemented by `vsacictl tag …` in [`cicd/cmd/release-cmd/`](../../cicd/cmd/release-cmd/). Command notes: [`cicd/cmd/release-cmd/ReadME.md`](../../cicd/cmd/release-cmd/ReadME.md).

Sprint id is **calendar week-of-month**, not ISO week:

```
YY + MM + weekOfMonth
weekOfMonth = (dayOfMonth - 1) / 7 + 1
```

Example: 3 Sep 2026 → `26091` (year 26, month 09, first week of September).

| Kind | Example | Created when |
|---|---|---|
| DEV on `main` | `26091.0.0-DEV.24` | Each merge to `main` (`vsacictl tag fetch_latest_build` increments the last number) |
| Weekly RC branch | `release/26091` | Friday cron: first time that sprint has no `release/<sprint>` branch |
| First RC | `26091.0.0-RC.1` | Same Friday job, from the current DEV tag |
| Next DEV sprint | `26092.0.0-DEV.1` | Same Friday job, tagged on `main` |
| Later RCs | `26091.0.0-RC.2` | Merges into `release/26091` (Jira target version selects the RC line) |
| OCI RC | `26091.0.0-OCI-RC.1` on `release/oci/26091` | Friday cut + merges to `release/oci/*` |
| GA | `26091.0.0` | Manual `vsacictl tag promotional` |

If HEAD is already tagged, merge tagging is skipped (same SHA is not retagged).

### 1.5 Local vs CI

Local loop is Makefile + Skaffold; see [README](../../README.md) (`make build-all-binaries-dev skaffold-dev`). That is independent of weekly tags.

---

## 2. VSA / ONTAP compute images (cluster upgrade inputs)

VCP does **not** compile ONTAP. Seclab publishes GCE images + COT files; this repo **copies** them across orgs.

Canonical write-up: [`.github/workflows/vsa-mediator-image-copy.md`](../../.github/workflows/vsa-mediator-image-copy.md). Config files: [`config/VSA-Image/README.md`](../../config/VSA-Image/README.md). Helm/env consumption: [`ontap-version-consumption-and-testing.md`](ontap-version-consumption-and-testing.md).

### 2.1 Cadence

| Step | Trigger | Frequency in this repo |
|---|---|---|
| ONTAP / seclab image **build** | Outside VCP | **Unknown here** — not scheduled in `.github/workflows` |
| Copy into autopush (COT verify) | [`vsa-mediator-image-copy.yml`](../../.github/workflows/vsa-mediator-image-copy.yml) | **Manual** `workflow_dispatch` |
| Promote autopush → staging → prod | Same workflow | **Manual** |
| Helm `VSA_IMAGE_NAME` / `ONTAP_VERSION_DETAILS` | Greencloud + VCP values PRs | Manual; often paired via [`release-orchestrator.yml`](../../.github/workflows/release-orchestrator.yml) |

There is **no** weekly cron that ships a new `x-9-18-1` image.

### 2.2 Copy path (GCP)

```
sssbu-seclab  →  gcnv-vsa-prod (Pull VM, COT)
              →  gcnv-autopush-images
              →  gcnv-staging-images
              →  gcnv-prod-images
```

Only autopush re-runs Chain of Trust. Staging copies autopush; prod copies staging.

### 2.3 Relation to cluster upgrade automation

[`automations/cluster-upgrade/README.md`](../../automations/cluster-upgrade/README.md): registering `image_versions` is not enough. A live upgrade target is:

1. Image copied into that environment’s bucket
2. Helm env `VSA_IMAGE_NAME`, `VSA_MEDIATOR_IMAGE_NAME`, `ONTAP_VERSION_DETAILS`
3. `POST /v1/imageVersions` (CLI `register-image`)

Those three stay in lockstep. The Friday VCP RC does **not** by itself change cluster ONTAP versions.

---

## 4. Security scans

There is **no** CodeQL analyze workflow and **no** scheduled Grype/Black Duck cron. Image CVE and SCA scans run when `build_common.yaml` publishes (every merge + weekly RC + GA promote + optional manual Security Scan). PR-time security is LLM review, not container scanning.

### 4.1 Inventory

| Scan | Workflow | Trigger | Blocks merge / release? | What it covers |
|---|---|---|---|---|
| **Anchore Grype** | [`anchore-grype.yml`](../../.github/workflows/anchore-grype.yml) via `build_common` `grype_scan_image` | After every published image set | **No.** `fail-build: false`. `severity-cutoff: high`. SARIF upload to GitHub Code Scanning (`continue-on-error: true`) | GHCR images in `IMAGES_TO_SCAN` (built images + `additional_images_toscan`, default `cloud-backup-adc` at tag `3.0`). Config: [`.grype.yaml`](../../.grype.yaml) |
| **Black Duck — repo** | `build_common` `black_duck_scan_repo` | Same as Grype (after `build_and_publish`) | Fail-closed on Detect CLI (`bash -e`); not a PR gate | Synopsys Detect 9.10.1: `DETECTOR` + `SIGNATURE_SCAN` on the checkout. Project `GCNV VSA Control Plane` / version `current`. Host: `https://blackduck.eng.netapp.com`. Runners: `[self-hosted, linux, bds]` |
| **Black Duck — images** | `build_common` `black_duck_scan_image` | Same | Same as repo scan | Detect `DOCKER` tool per matrix image from GHCR |
| **Manual Security Scan** | [`security_scan.yaml`](../../.github/workflows/security_scan.yaml) | **`workflow_dispatch` only.** PR trigger is commented (`Will be enabled after AGA`) | N/A | Diff vs `origin/main`; if any mapped image path changed, calls `build_common` with tag `sec.build.<10-char-sha>`, `create_release: false`, `push_charts: false`. Then Black Duck + Grype run as usual |
| **CSC LLM PR review** | [`security-review.yml`](../../.github/workflows/security-review.yml) → [`csc-pr-security-review.yaml`](../../.github/workflows/csc-pr-security-review.yaml) | PR `opened`, `reopened`, or labeled `security-review`. **Not** on `synchronize`. Same-repo PRs only (no forks) | Review comments; not listed as a required status in these files | CSC image `ghcr.io/greenqloud/llm-security-review@sha256:…`. Models `claude-sonnet-4.6` / fallback `gpt-5.4`. `step-security/harden-runner` egress audit. Contact: `#csc-diamond-security` |
| **VCP LLM PR review** | [`llm-security-review.yml`](../../.github/workflows/llm-security-review.yml) | PR `opened`, `synchronize`, `reopened` | **No.** `continue-on-error: true` (comment: `LLM_PROXY_API_KEY` expired) | `scripts/security_review.py` on `[self-hosted, vsa-pr-bot]` via NetApp LLM proxy |
| **Dependabot version PRs** | [`.github/dependabot.yml`](../../.github/dependabot.yml) | **Weekly** (gomod, github-actions, docker) | Separate PRs, limit 20 | Not a scan of built images. Dependabot **alerts / security updates** are GitHub org settings, not this file |
| **Cosign sign + verify** | `build_common` after push | Every published image / binary / OCI manifest / build-context | Sign/verify failure fails the publish job | Supply-chain signing, not CVE scanning. Keys from GCloud secrets. Reused helper: [`.github/scripts/cosign-retry.sh`](../../.github/scripts/cosign-retry.sh) |
| **VSA image COT** | [`vsa-mediator-image-copy.yml`](../../.github/workflows/vsa-mediator-image-copy.yml) | Manual copy to **autopush** only | Copy job fails if COT fails | Cryptographic verification of seclab GCE images. Staging/prod copy does **not** re-verify |

### 4.2 Cadence in one line

| When | Scans that actually run |
|---|---|
| Every PR (open / push) | VCP LLM review (non-blocking). CSC LLM review only on open/reopen or `security-review` label — **not** on later commits |
| Every merge / Friday RC / GA | Cosign, Black Duck repo, Black Duck per image, Grype (+ SARIF) |
| Weekly (calendar) | Dependabot update PRs. **No** weekly Black Duck/Grype-only job |
| Operator demand | `Security Scan` workflow → subset image build + same Black Duck/Grype jobs |
| VSA image promotion | COT on autopush copy only |

### 4.3 Not present in this repo’s Actions

- GitHub CodeQL **analyze** workflow (only Grype SARIF upload via `github/codeql-action/upload-sarif`)
- Trivy, Snyk, gosec, govulncheck, Gitleaks, Semgrep as dedicated workflows
- [`pr_analysis.yml`](../../.github/workflows/pr_analysis.yml) — OpenAI PR description helper, not a security scanner
- A **pre-use intake registry** that Grype/Black Duck-scans third-party images *before* Dockerfiles may `FROM` them (see [§5](#5-image-registries-pull-publish-scan))

---

## 5. Image registries (pull, publish, scan)

**Do we have a central repo where we pull external container images and scan them before use?** **No**, not in this repo’s pipelines.

External bases (`gcr.io/distroless/*`, `mirror.gcr.io/library/golang`, `ubuntu:22.04`, `gcr.io/cloud-sql-connectors/cloud-sql-proxy`, upstream Temporal) are pulled **at Docker build time**. Grype and Black Duck run **after** VCP images are built and pushed. They inspect the **composed first-party image on GHCR**, not a quarantine copy of the upstream.

Closest approximations:

| Mechanism | What it is | Pre-use CVE scan? |
|---|---|---|
| GHCR `ghcr.io/vcp-vsa-control-plane/${AR_IMAGE_PATH}/…` | Canonical CI **publish + Grype/Black Duck** target | After we build, not before `FROM` |
| GCP Artifact Registry `gcnv-artifact-registry-nonprod` | Mirror/push for GKE secondary pull | **Not** what Grype pulls (`anchore-grype.yml` uses GHCR only) |
| [`scripts/temporal/mirror-images.sh`](../../scripts/temporal/mirror-images.sh) | Copy `temporalio-{server,admin-tools,ui}` into GHCR or GAR | **No.** Not in Grype/Black Duck `IMAGES_TO_SCAN` |
| Autopush VSA GCE + COT | Cryptographic verify of seclab disks | Not a container CVE scan |
| Dependabot `docker` | Weekly PRs for Dockerfile base bumps | Version PRs, not Grype |

`AR_IMAGE_PATH`, `AR_GCP_PROJ`, `AR_GCP_REGION`, `AR_HELM_PATH` are GitHub Actions **variables**. Checked-in Helm/Makefile defaults for GCP are **`gcnv-artifact-registry-nonprod`**. Production GKE registry overrides are applied in the deployment pipeline (Greencloud), not in these chart defaults. Project `gcnv-artifact-registry` appears in VSA image-copy IAM docs as an SA home, not as a documented first-party container repository in this tree.

### 5.1 Container registries (non-prod vs prod)

| Role | Non-prod (in this repo) | Prod (in this repo) |
|---|---|---|
| **CI / GKE primary pull** | `ghcr.io/vcp-vsa-control-plane` (`primaryImageRegistry` + path `vcp-vsa-control-plane`) | Same GHCR org — charts do not switch GHCR by env |
| **CI GCP push** | `${AR_GCP_REGION}-docker.pkg.dev/${AR_GCP_PROJ}/${AR_IMAGE_PATH}` via `vsacictl images push` | Same GitHub vars (one CI Artifact Registry). Not a second prod-only push in `build_common.yaml` |
| **GKE secondary pull (chart default)** | `europe-north1-docker.pkg.dev/gcnv-artifact-registry-nonprod/vcp-container-images-us` (core, worker, ontap-proxy). VLM uses `us-docker.pkg.dev/gcnv-artifact-registry-nonprod/vcp-container-images-us` | **Not set in this repo’s `values.yaml`.** Expect Greencloud/env overrides |
| **Makefile local GAR** | `us-docker.pkg.dev/gcnv-artifact-registry-nonprod/vcp-container-images-us` | n/a |
| **VLM worker image push** | `us-docker.pkg.dev/gcnv-artifact-registry-nonprod/vcp-container-images-us/vlm-worker` | Same workflow; no prod AR project in [`vlm-worker-image-push.yml`](../../.github/workflows/vlm-worker-image-push.yml) |
| **Harvest / OTel** | `us-docker.pkg.dev/gcnv-artifact-registry-nonprod/vcp-container-images-us/harvest-multi`, `…/vcp-open-telemetry-images/opentelemetry-collector-contrib` | Same: chart defaults are nonprod |
| **Temporal images (mirrored)** | GHCR `ghcr.io/vcp-vsa-control-plane/temporalio-{server,admin-tools,ui}` and/or `europe-north1-docker.pkg.dev/gcnv-artifact-registry-nonprod/temporal-container-images` | Chart defaults GHCR; GCR path in [`scripts/temporal/README.md`](../../scripts/temporal/README.md) is nonprod |
| **Temporal Helm OCI** | `oci://ghcr.io/vcp-vsa-control-plane/${AR_HELM_PATH}` and `oci://${AR_GCP_REGION}-docker.pkg.dev/${AR_GCP_PROJ}/${AR_HELM_PATH}` | Same CI vars |
| **OCI runtime** | `iad.ocir.io/idqogasfjw45` ([`overrides_eng_cp.yaml`](../../kubernetes/OCI/oci-proxy/overrides_eng_cp.yaml)) | `iad.ocir.io/nmobsa` ([`overrides_nmobsa.yaml`](../../kubernetes/OCI/oci-proxy/overrides_nmobsa.yaml)) |

### 5.2 First-party images Grype / Black Duck pull

Scan URL pattern ([`anchore-grype.yml`](../../.github/workflows/anchore-grype.yml), Black Duck `DOCKER`):

`ghcr.io/vcp-vsa-control-plane/${{ vars.AR_IMAGE_PATH }}/<image>:<tag>`

Default `<image>` list from `build_common` `images_to_build` plus `additional_images_toscan`:

`core`, `google-proxy`, `vcp-db-migrate`, `vcp-iam-lifecycle`, `vcp-worker`, `telemetry`, `vcp-cloudrun-deployer`, `ontap-proxy`, `vcm-proxy`, and **`cloud-backup-adc:3.0`** (ADC sidecar; not rebuilt each release).

`oci-proxy` is pushed to GHCR (not GCP) and is on the security-scan path map; it is **not** in the default `images_to_build` string. `vsa-builder:v9` is the CI runner image and is **not** in the Grype matrix.

Helm charts also pull (not in that scan list unless listed above): `vlm-worker`, Harvest/OTel collector, `cloud-sql-proxy`.

**Temporal server images are not scanned.** `temporalio-server`, `temporalio-admin-tools`, and `temporalio-ui` are copied by [`scripts/temporal/mirror-images.sh`](../../scripts/temporal/mirror-images.sh) into GHCR (`ghcr.io/vcp-vsa-control-plane/temporalio-*`) or GAR `temporal-container-images`. They are **not** in `images_to_build` / `IMAGES_TO_SCAN`. `build_common` may push the Temporal **Helm chart** from `main`; it does not Grype those three images. OCI copies (`kubernetes/OCI/temporal/push-images-to-ocir.sh`) are also retag-only.

What **is** scanned is `vcp-worker`: that is VCP’s Temporal **worker** binary on distroless, not the Temporal server runtime.

### 5.3 External bases pulled at build time (no intake scan)

| Source | Used for |
|---|---|
| `gcr.io/distroless/base` | [`common/Dockerfile`](../../common/Dockerfile) (`base` for proxies/core/worker/telemetry) |
| `gcr.io/distroless/base-debian12:nonroot` | OCI Dockerfiles (`worker/Dockerfile.oci`, `oci-proxy/Dockerfile.oci`) |
| `gcr.io/distroless/static:nonroot` | [`tools/vlm-worker-reconciler/Dockerfile`](../../tools/vlm-worker-reconciler/Dockerfile) |
| `mirror.gcr.io/library/golang:1.26.6` | Prod/dev compile images under `builder/`, `common/Dockerfile.dev` |
| `ubuntu:22.04` | [`tools/vsa-builder/Dockerfile`](../../tools/vsa-builder/Dockerfile) |
| `gcr.io/cloud-sql-connectors/cloud-sql-proxy:2.21.1` | Chart sidecar, not a VCP build artifact |
| Docker Hub Temporal (then mirror) | [`scripts/temporal/mirror-images.sh`](../../scripts/temporal/mirror-images.sh) |
| `ghcr.io/greenqloud/llm-security-review@sha256:…` | CSC PR reviewer only |

### 5.4 GCE VSA / Mediator images (not Docker; not Grype)

| Env | GCP project | GCS bucket | Scan in this repo |
|---|---|---|---|
| Seclab source | `sssbu-seclab` | GCE images | Outside VCP |
| Pull / COT staging | `gcnv-vsa-prod` | `vsa-compute-images`; COT `gs://cot-releases-public` | COT on autopush copy |
| Autopush (non-prod) | `gcnv-autopush-images` | `gcnv-autopush-images-bucket` | COT |
| Staging (non-prod) | `gcnv-staging-images` | `gcnv-staging-images-bucket` | Copy only |
| Prod | `gcnv-prod-images` | `gcnv-prod-images-bucket` | Copy only |

---

## 6. Pointers

| Topic | Where |
|---|---|
| Tag / promote CLI | [`cicd/cmd/release-cmd/ReadME.md`](../../cicd/cmd/release-cmd/ReadME.md) |
| Image copy architecture | [`.github/workflows/vsa-mediator-image-copy.md`](../../.github/workflows/vsa-mediator-image-copy.md) |
| Cluster upgrade APIs | [`doc/architecture/vsa-cluster-upgrade-design.md`](../architecture/vsa-cluster-upgrade-design.md) |
| OCI GHCR → Object Storage mirror | [`doc/architecture/designs/ghcr-to-oci-object-storage-mirror/DESIGN.md`](../architecture/designs/ghcr-to-oci-object-storage-mirror/DESIGN.md) (manual dispatch) |
| Grype ignore rules | [`.grype.yaml`](../../.grype.yaml) |
