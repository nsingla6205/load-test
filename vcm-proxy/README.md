### VSA Cluster Manager (VCM)

VCM is a GCP-only service for GCNV North Star. It owns ONTAP lifecycle and VSA VM orchestration inside Google-managed tenant projects.

This directory is the HTTP API entrypoint (`vcm-proxy`). It does not own business logic: it serves the OpenAPI surface and runs VCM workflows in-process (go-workflows + VLM), using `core` as a library.

Callers are Google CLH components. Validations and resource-metadata persistence are handled upstream; VCM proceeds to cluster provisioning.

The local stack is **VCM-only**. It deploys `vcm-proxy`, `vcm-prepool`, and a Spanner emulator. It does **not** deploy postgres, Temporal, core-api, or google-proxy.

### Code Layout

```
vcm-proxy/
├── app.go                  # HTTP server, workflow engine, VLM wiring
├── api/
│   ├── vcm-api.yaml        # OpenAPI spec
│   ├── .ogenserver.yml     # ogen server generator config
│   ├── generate.go         # go:generate for the server
│   ├── vcm-servergen/      # Generated HTTP server (do not edit)
│   └── endpoints/          # Config, pool, hostgroup, operation handlers
├── config/                 # Server and VCM config store
├── middleware/             # Configured-gate and related HTTP middleware
├── Dockerfile              # Production image
├── Dockerfile.dev          # Dev image used by Skaffold
└── air.toml                # Air live-reload config
```

Related directories (outside this module, still required to run locally):

```
.
├── tools/vcm-prepool/                      # Pre-pool infra setup HTTP service
├── core/orchestrator/factory/gcp/vcm/     # VCM orchestrator factory
├── core/orchestrator/workflows/vcm-workflows/
├── core/orchestrator/activities/vcm/
├── database/vcm/                          # Spanner-backed VCM store
├── skaffold.yaml                          # `vcm` profile (build + deploy + port-forward)
├── skaffold/k8s/vcm.yaml                   # Local k8s manifests (namespace vcm)
└── skaffold/scripts/after_deploy_vcm.sh   # Patches regional env and the credentials Secret after deploy
```

### How to Run VCM Using Skaffold Locally (Minikube Cluster)

Run all `make` / `skaffold` commands from the **repository root**, not from `vcm-proxy/`.

#### Prerequisites.

* [Install minikube](https://minikube.sigs.k8s.io/docs/start).
* [Install Docker](https://docs.docker.com/get-docker/).
* [Install Skaffold](https://skaffold.dev/docs/install/).
* [Install Github CLI](https://cli.github.com). Authenticate so `gh auth token` works (`gh auth login`).

#### Steps

##### 1. Start a minikube cluster.

```
minikube start
```

##### 2. Run Skaffold

Set credentials, then start the `vcm` Skaffold profile with a single Make target.

`make skaffold-dev-vcm` builds `app/vcm-proxy` and `app/vcm-prepool` (via `build-vcm-proxy` / `build-vcm-prepool`) before invoking Skaffold. Those binaries live under `app/`, which is gitignored; a clean checkout needs this step so the dev Dockerfiles can `COPY` them.

`GHVSA_PAT` is required to pull private GitHub packages during the image build. `VSA_NODE_PASSWORD`, `VCM_LOCAL_ONTAP_USERNAME`, and `VCM_LOCAL_ONTAP_PASSWORD` are required. The after-deploy hook writes them into the `vcm-credentials` Secret (consumed with `secretKeyRef`). The hook prompts for any that are unset and exits if a value is missing or still the manifest placeholder.

The stack supports three autopush-tst regions. `VCM_REGION` defaults to `au-se1` if not set.

| `VCM_REGION` | GCP region | Metadata server | Secret project |
|---|---|---|---|
| `au-se1` (default) | `australia-southeast1` | `34.116.66.254:9090` | `266893635349` (`netapp-au-se1-autopush-sde-tst`) |
| `us-c1` | `us-central1` | `34.116.66.254:9090` (AU-SE1; VSA image pull) | `254387308849` (`netapp-us-c1-autopush-sde-tst`) |
| `us-e4` | `us-east4` | `34.116.66.254:9090` (AU-SE1; VSA image pull) | `402007546452` (`netapp-us-e4-autopush-sde-tst`) |

For `us-c1` and `us-e4`, the metadata host stays on AU-SE1 so VSA image pull works; `VSA_AUTH_TYPE` remains `0` in `skaffold/k8s/vcm.yaml` because that metadata SA cannot manage secrets in the other regions' Secret Manager projects.

```
export GHVSA_PAT=$(gh auth token)
export VSA_NODE_PASSWORD=<password-to-be-set-on-ontap>
export VCM_LOCAL_ONTAP_USERNAME=<username-to-be-set-on-ontap>
export VCM_LOCAL_ONTAP_PASSWORD=<password-to-be-set-on-ontap>

# Default region (au-se1):
make skaffold-dev-vcm

# Explicit region:
make skaffold-dev-vcm VCM_REGION=us-c1
```

Convenience aliases:

```
make skaffold-dev-vcm-au-se1
make skaffold-dev-vcm-us-c1
make skaffold-dev-vcm-us-e4
```

The after-deploy hook (`skaffold/scripts/after_deploy_vcm.sh`) patches `LOCAL_REGION`, `GCE_METADATA_HOST`, and `SECRET_MANAGER_PROJECT_ID` into the `vcm-env` ConfigMap based on the selected region, and writes the ONTAP credentials into the `vcm-credentials` Secret. Non-regional env variables live in `skaffold/k8s/vcm.yaml` and can be customized there.

**Switching regions**: stop the running Skaffold (`Ctrl-C`), then re-run with the new `VCM_REGION`. The hook will patch the ConfigMap and restart the pods.

This builds and deploys the VCM services to your local Kubernetes cluster (namespace `vcm`). Once deployed, you can access:

- VCM Prepool: http://localhost:8090
- VCM Proxy: http://localhost:8092
- Spanner emulator (gRPC): localhost:9010
- Spanner emulator (REST): localhost:9020

Health checks:

```
curl -s http://localhost:8092/health
curl -s http://localhost:8090/health
```

Verify the effective region:

```
kubectl get configmap vcm-env -n vcm -o jsonpath='{.data.LOCAL_REGION}'
```

Prepool HTTP API (port 8090): `POST /prepoolSetup`, `GET /jobs`, `GET /jobs/{jobId}`, plus reclaim/teardown routes — see `tools/vcm-prepool/api/handler.go`.

#### Live reloading the code

While Skaffold is running, it watches `app/vcm-proxy` and `app/vcm-prepool`. After a Go code change, rebuild only the affected binary (or both):

```
make build-vcm-proxy build-vcm-prepool
```

Skaffold syncs the rebuilt binaries into the running containers; you do not need to restart the Make target. To disable watch:

```
skaffold dev -p vcm --no-watch
# or
skaffold dev -p vcm --watch=false
```

#### Handling Errors

If pods are stuck or credentials were missing at first deploy, reset Minikube and redeploy:

```
minikube delete
minikube start
```

Then:

```
export GHVSA_PAT=$(gh auth token)
export VSA_NODE_PASSWORD=<password-to-be-set-on-ontap>
export VCM_LOCAL_ONTAP_USERNAME=<username-to-be-set-on-ontap>
export VCM_LOCAL_ONTAP_PASSWORD=<password-to-be-set-on-ontap>
make skaffold-dev-vcm
```

If `VSA_NODE_PASSWORD`, `VCM_LOCAL_ONTAP_USERNAME`, or `VCM_LOCAL_ONTAP_PASSWORD` is unset, `skaffold/scripts/after_deploy_vcm.sh` prompts for it. All three are required for `ENV=local`.

#### Linting locally

From the repository root:

```
# Installing golangci-lint and vsacictl
brew install golangci-lint
brew upgrade golangci-lint
cd ./cicd
go build -o ~/go/bin/vsacictl .
cd ..

# Running vsacictl
vsacictl lint
```

### Guides

- `doc/architecture/designs/0027-vcm-repo-structure.md` — module layout and how `vcm-proxy` relates to `core` and VLM.
- `vcm-proxy/api/vcm-api.yaml` — HTTP API for config, clusters, igroups, volumes, and operations.