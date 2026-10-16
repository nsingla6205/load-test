### VSA Control Plane

This repo hosts all the code for the VSA Control Plane. The VSA Control Plane is a set of microservices that are responsible for managing the lifecycle of a VSA.

### Code Layout
The code is organized into the following directories:

```
.
├── artifacts/              # Compiled binaries and build artifacts
├── builder/                # Dockerfiles and build scripts
├── checksums/              # Checksum files for various modules
├── cicd/                   # CI/CD scripts, Dockerfiles, and pipeline configs
├── clients/                # Client libraries for the VSA Control Plane
│   ├── core-api/
│   ├── cvp/
│   ├── google-proxy-client/
│   ├── ontap-rest/
│   └── vlm/
├── common/                 # Shared/common code across services
├── config/                 # Configuration files (YAML, env, etc.)
├── core/                   # Core API (Hyperscaler Agnostic)
├── database/               # Database logic, mocks, and interfaces
├── doc/                    # Documentation and architecture diagrams
├── google-proxy/           # Google Proxy service and API
├── hyperscaler/            # Hyperscaler provider implementations
├── kubernetes/             # Helm charts and Kubernetes manifests
├── scripts/                # Utility scripts for code generation and verification
├── security/               # Network policies and security configs
├── skaffold/               # Skaffold-specific configurations
├── telemetry/              # Telemetry service, API, and supporting code
├── tools/                  # External tools (Swagger, migration, etc.)
├── utils/                  # Utility functions and helpers
├── vsa_config/             # VSA configuration files and logic
├── worker/                 # Worker service and supporting code
└── workflow_engine/        # Workflow engine and Temporal client integration
```

### How to Run VSA Using Skaffold Locally (Minikube Cluster)

#### Prerequisites.
* [Install minikube](https://minikube.sigs.k8s.io/docs/start).
* [Install helm](https://helm.sh/docs/intro/install/).
* [Install Docker](https://docs.docker.com/get-docker/).
* [Install Skaffold](https://skaffold.dev/docs/install/).
* [Install Github CLI](https://cli.github.com).

#### Steps

##### 1. Start a minikube cluster.

```
minikube start
```

##### 2. Temporary steps.

###### 2.0 If you're using Autopush for the first time.

Refer to [Autopush instructions below.](#autopush_instructions)

###### 2.1 Find your regional tenant project ID.

1. Navigate to your GCP console > VPC Network > VPC network peering.
1. Observe the `Peered project ID` for the `netapp-autopush-tst-network`.
1. Navigate to that project, in the latchkey org.
1. ?

I found my regional tenant project ID from an error message. If you already have an Atom/SDE based cluster, you can also find it from the `pool_attributes` in the database.

###### 2.2 Copy ONTAP image into your tenant project.

Find `vsa_image_name` in `/common/vsa_config/vlm-config.json`

```bash
gcloud compute images create <vsa_image_name> --source-image <vsa_image_name>  --source-image-project=g1p-functional-ap-tst-02 --project=<regional_tenant_project_id_ending_in_tp>
```

###### 2.3 Grab the latest VLM image.

Ask someone what the current tag to use is. You need to be in Seclab for this to work.

```bash
docker pull ghcr.io/vcp-vsa-control-plane/vcp-container-images-us/vlm-worker:r9.17.1px_7825887
minikube image load ghcr.io/vcp-vsa-control-plane/vcp-container-images-us/vlm-worker:r9.17.1px_7825887
```

##### 3. Run Skaffold

Add Temporal helm chart repo for skaffold

```bash
helm repo add temporal https://go.temporal.io/helm-charts
helm repo update
helm repo list
```

Run the following to set the necessary environment variables related to credentials.
All non-authentication related env. variables are in `skaffold.env`, and can be customized there.

```bash
export GHVSA_PAT=$(gh auth token)
export DB_PASSWORD=<password-to-use-for-db>
export DB_ADMIN_PASSWORD=<password-to-use-for-db>
export VSA_NODE_PASSWORD=<password-to-be-set-on-ontap>
```

Then for local development, run:
```bash
make build-all-binaries-dev skaffold-dev
```

If you are planning to use SKS clusters you will need to use your GitHub PAT that has read/write permissions to the registry. 
```bash
export GHVSA_PAT=<github_pat>
```

Then run the following command to build and deploy the services on your SKS cluster:
```bash
make build-all-binaries-dev
skaffold run -p sks --default-repo=ghcr.io/<gh-username>
```

This will build and deploy all the services to your local Kubernetes cluster. Once deployed, you can access the services using the following URLs:

- Google Proxy: http://localhost:9000
- Postgres: http://localhost:5433
- Local Temporal Web: http://localhost:8080
- Harvest Farm: http://localhost:3000
- Metrics Processor: http://localhost:9090

#### Debugging the code

```bash
skaffold debug
```
Once the services are deployed, you can attach a debugger to the services using the following URLs:

- Core API: http://localhost:56268
- Google Proxy: http://localhost:56269
- Workflow Server: http://localhost:56270

#### Live reloading the code

Skaffold will automatically watch for changes in the code and rebuild and redeploy the services. If this needs to be disabled, you can run the following command:

```bash
    skaffold dev --no-watch
    # or
    skaffold dev --watch=false
```

#### Updating services when using SKS clusters

If you are using SKS clusters, you will need to update the services using the following command:

```bash
nkdev update service
```

#### Handling Errors

Error: pq: duplicate key value violates unique constraint "cluster_metadata_info_pkey"
Error: Not enough hosts to serve the request
Solution: Clean the local cluster.

If you are using Minikube, reset it by running the following commands:
```
minikube delete
minikube start
```

Then, redeploy:

```bash
export GHVSA_PAT=<your_github_pat>
skaffold dev
```

#### Regenerating API code

After changing an OpenAPI spec, regenerate the code from the repo root and commit it together with the spec (CI runs `make verify-generated`):

```bash
make generate-core-api       # vcp-core/api.yaml -> vcp-core/servergen + clients/core-api (ogen v1.10.1)
make generate-google-proxy   # google-proxy/api/gcp-api.yaml -> gcp-servergen + clients/google-proxy-client
make generate-mocks          # mockery mocks (needed when an interface changes)
make generate                # everything above, plus metrics, VCM and the retry engine wrapper
```

Use the `make` targets, not `go generate`: some `generate.go` files point at `ogen@latest`, which would regenerate every file with a different ogen version. See doc/api/resources/ops-inventory.md for the ops inventory API.

#### Linting locally
```bash
# Installing golangci-lint and vsacictl
brew install golangci-lint
brew upgrade golangci-lint
cd ./cicd
go build -o ~/go/bin/vsacictl .
cd ..

# Running vsacictl
vsacictl lint
```

<a id="autopush_instructions"></a>

#### Autopush instructions.


<a id="autopush_access"></a>

##### Get access to Autopush.

Request for allow-listing: <https://docs.google.com/spreadsheets/d/1o2rNTKv-mu6fYdvJetTA6gqLpJx0MnT_8iT317RECJc/edit?resourcekey=0-tAG1yhd-qmqG6H6yP17WvQ&gid=1741495625#gid=1741495625>

Raise a ticket with Google: <https://partnerissuetracker.corp.google.com/issues/432149046>


<a id="autopush_steps"></a>

##### Steps.

1.  Create a network for the filesystem.

    1.  Query all existing VPCs.
    
            # List all VPCs in the project.
            gcloud compute networks list
    
    2.  Create a VPC for GCNV.
    
            gcloud compute networks create gcnv-file-storage-vpc \
              --subnet-mode=custom \
              --bgp-routing-mode=global \
              --description="VPC for hosting the remote file system."
    
    3.  Create a subnet in us.
    
            gcloud compute networks subnets create gcnv-file-storage-subnet-us \
              --network=gcnv-file-storage-vpc \
              --region=us-west1 \
              --range=10.0.1.0/24

2.  Allocate IP addresses for private connection to NetApp volumes.

        gcloud compute addresses create gcnv-managed-network \
               --global \
               --purpose=VPC_PEERING \
               --prefix-length=24 \
               --description="VPC peering range for NetApp Volumes." \
               --network=gcnv-file-storage-vpc

3.  Create a private connection.

        gcloud services vpc-peerings connect \
               --service=netapp-tst-autopush-endpoint.appspot.com \
               --ranges=gcnv-managed-network \
               --network=gcnv-file-storage-vpc


<a id="autopush_console"></a>

##### Autopush console.

<https://console.cloud.google.com/netapp/pools?p2env=features%2F1649438282178%2Fbuild_units%2Fmain%2Fenvironments%2Fautopush&project=g1p-rc74879-dev>

### Triagebot

Triagebot is a Cursor rule that triages failed flows by fetching GCP logs for a given correlation id and analyzing them against the repo’s design and workflow docs. The report is returned in chat only (no files written).

#### Prerequisites

1. **Google Cloud SDK (gcloud)**  
   Install from [cloud.google.com/sdk](https://cloud.google.com/sdk/docs/install).

2. **Authenticate to GCP**  
   Log in so `gcloud` can read Cloud Logging in the target project:

   ```bash
   gcloud auth login
   ```

   Ensure your account has access to the GCP project that contains the logs (e.g. `netapp-us-c1-staging-sde`).

#### How to use

1. In Cursor chat, start your message with **`triagebot`** (case-insensitive).
2. When prompted, provide:
   - **GCP project id** (e.g. `netapp-us-c1-staging-sde`)
   - **Correlation id** (the `x-correlation-id` value from the request or error)
3. Optionally you can pass them inline:  
   `triagebot project=<project_id> correlation_id=<correlation_id>`

Triagebot will fetch logs, analyze them against `doc/workflows/` and related design docs, and reply with a concise triage report (outcome, failed step, evidence, root cause, next actions). Logs are written under `triagebot_logs/<correlation_id>.json` in the repo for the run only.

### Guides

For operator and developer runbooks and debugging guides, see the doc/guides directory. Notable guides:

- doc/guides/temporal-debugging.md — Operational guide for debugging Temporal workflows, tctl usage (including admintools usage for restricted clusters), and UI access for local and cluster environments.
