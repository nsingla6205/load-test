# VCP OCI Deployment Guide — Quick Start

Deploy **oci-proxy**, **vcp-worker**, and **vlm-worker** on OKE by building
container images from the binaries provided in the release bundle, then
deploying with Helm.

---

## 1. Prerequisites


| Tool      | Min version | Install                                                                                                                                        |
| --------- | ----------- | ---------------------------------------------------------------------------------------------------------------------------------------------- |
| `helm`    | 3.14+       | [https://helm.sh/docs/intro/install/](https://helm.sh/docs/intro/install/)                                                                     |
| `kubectl` | 1.28+       | [https://kubernetes.io/docs/tasks/tools/](https://kubernetes.io/docs/tasks/tools/)                                                             |
| `jq`      | 1.6+        | [https://jqlang.github.io/jq/download/](https://jqlang.github.io/jq/download/)                                                                 |
| `docker`  | 20.10+      | [https://docs.docker.com/engine/install/](https://docs.docker.com/engine/install/)                                                             |
| `oci`     | 3.x+        | [https://docs.oracle.com/en-us/iaas/Content/API/SDKDocs/cliinstall.htm](https://docs.oracle.com/en-us/iaas/Content/API/SDKDocs/cliinstall.htm) |


You also need:

- A running OKE cluster with `kubectl` context configured.
- OCIR credentials (tenancy namespace + auth token) for your target registry.
- OCI Vault, compartment, and compute image OCIDs for your environment.
- A PostgreSQL instance reachable from the cluster.
- A Temporal server reachable from the cluster (must be running **before** oci-proxy).
- ~20 GB free disk space for downloading the release bundle.

---

## 2. Download the release bundle

1. Log in to the tenancy using a service account with access to the release bucket.
2. Download the release bundle:

```bash
oci os object bulk-download \
  --bucket-name oci-vcp-vsa-artifacts \
  --download-dir . \
  --prefix vcp-releases/<TAG> \
  --namespace <OCI_TENANCY_OBJECT_STORAGE_NS>
```

1. `cd` into the release folder:

```bash
cd vcp-releases/<TAG>
```

The release bundle contains:


| Path                                                       | Description                                       |
| ---------------------------------------------------------- | ------------------------------------------------- |
| `release-manifest.json`                                    | Single source of truth for versions and artifacts |
| `deploymentKit/VCP_OCI_DEPLOY_GUIDE.md`                    | This file                                         |
| `deploymentKit/oci-proxy-overrides.yaml`                   | Helm override template for oci-proxy              |
| `deploymentKit/vcp-worker-overrides.yaml`                  | Helm override template for vcp-worker             |
| `deploymentKit/vlm-worker-overrides.yaml`                  | Helm override template for vlm-worker             |
| `helm/oci-proxy/oci-proxy-<TAG>.tgz`                       | Helm chart for oci-proxy                          |
| `helm/oci-vcp-worker-chart/oci-vcp-worker-chart-<TAG>.tgz` | Helm chart for vcp-worker                         |
| `helm/oci-vlm-worker/oci-vlm-worker-<TAG>.tgz`             | Helm chart for vlm-worker                         |
| `artifacts/vcp-worker/vcp-worker`                          | vcp-worker Go binary                              |
| `artifacts/oci-proxy/oci-proxy`                            | oci-proxy Go binary                               |
| `build-context/release-build-context-<TAG>.tar.gz`         | Dockerfiles and config files for image builds     |
| `VSA/`                                                     | VSA/VLM/Mediator images (uploaded separately)     |


Verify the key files are present:

```bash
ls -ltr artifacts/oci-proxy/* artifacts/vcp-worker/* \
  build-context/*.tar.gz deploymentKit/*.yaml \
  release-manifest.json helm/*/*.tgz
```

---

## 3. Set environment variables

Set these once — all subsequent steps use them:

```bash
export MANIFEST="release-manifest.json"
export TAG=$(jq -r '.metadata.version' "$MANIFEST")
export ONTAP_VERSION=$(jq -r '.vsaArtifacts.versions[0].ontapVersion' "$MANIFEST")
export VLM_BINARY=$(jq -r '.vsaArtifacts.versions[0].vlmVersion' "$MANIFEST")
export OCIR_HOST="<OCIR_HOSTNAME>"          # e.g. iad.ocir.io
export OCIR_NS="<OCIR_TENANCY_NS>"          # e.g. idqogasfjw45
export IMAGE_REGISTRY="${OCIR_HOST}/${OCIR_NS}"
export NS="vcp"

echo "TAG:            $TAG"
echo "ONTAP_VERSION:  $ONTAP_VERSION"
echo "VLM_BINARY:     $VLM_BINARY"
echo "Registry:       $IMAGE_REGISTRY"
echo "Namespace:      $NS"
```

> Verify all values printed correctly before continuing.

---

## 4. Unpack Helm charts

```bash
for CHART_DIR in helm/oci-proxy helm/oci-vcp-worker-chart helm/oci-vlm-worker; do
  for CHART_TGZ in "$CHART_DIR"/*.tgz; do
    [ -f "$CHART_TGZ" ] && tar xzf "$CHART_TGZ" -C "$CHART_DIR" && echo "Extracted: $CHART_TGZ"
  done
done
```

Set chart path variables:

```bash
export OCI_PROXY_CHART=helm/oci-proxy/oci-proxy
export VCP_WORKER_CHART=helm/oci-vcp-worker-chart/oci-vcp-worker-chart
export VLM_WORKER_CHART=helm/oci-vlm-worker/oci-vlm-worker

echo "OCI_PROXY_CHART:  $OCI_PROXY_CHART"
echo "VCP_WORKER_CHART: $VCP_WORKER_CHART"
echo "VLM_WORKER_CHART: $VLM_WORKER_CHART"
```

Expected directories after extraction: `helm/oci-proxy/oci-proxy/`, `helm/oci-vcp-worker-chart/oci-vcp-worker-chart/`, `helm/oci-vlm-worker/oci-vlm-worker/`.

---

## 5. Verify binaries

```bash
chmod +x artifacts/vcp-worker/vcp-worker artifacts/oci-proxy/oci-proxy
chmod +x VSA/${ONTAP_VERSION}/VLM/${VLM_BINARY}

file artifacts/vcp-worker/vcp-worker           # ELF 64-bit LSB executable
file artifacts/oci-proxy/oci-proxy             # ELF 64-bit LSB executable
file VSA/${ONTAP_VERSION}/VLM/${VLM_BINARY}    # ELF 64-bit LSB executable
```

All three must show `ELF 64-bit LSB executable`. If any fail, contact the release team for a replacement binary.

---

## 6. Extract build context

The build-context tarball contains Dockerfiles and config files needed for image builds:

```bash
BUNDLE_FILE="build-context/$(jq -r '.vcpArtifacts.buildContext.filename' "$MANIFEST")"
tar xzf "$BUNDLE_FILE"
```

This creates:

- `worker/Dockerfile.oci`
- `oci-proxy/Dockerfile.oci`
- `common/vsa_config/vlm-config-oci.json`
- `config/vmrs_oci.yaml`

---

## 7. Build container images

All builds must run from the release folder root so the Dockerfile `COPY` paths resolve correctly.

### 7.1 Build oci-proxy and vcp-worker

```bash
# oci-proxy — bakes in common/vsa_config/ directory
docker build --no-cache --platform linux/amd64 \
  --build-arg BASE=gcr.io/distroless/base-debian12:nonroot \
  -f oci-proxy/Dockerfile.oci \
  -t "${IMAGE_REGISTRY}/oci-proxy:${TAG}" .

# vcp-worker — bakes in vlm-config-oci.json and vmrs_oci.yaml
docker build --no-cache --platform linux/amd64 \
  --build-arg BASE=gcr.io/distroless/base-debian12:nonroot \
  -f worker/Dockerfile.oci \
  -t "${IMAGE_REGISTRY}/vcp-worker:${TAG}" .
```

### 7.2 Create the vlm-worker Dockerfile and build

The vlm-worker Dockerfile is not included in the build-context tarball. Create it manually:

```bash
mkdir -p vlm-worker
cat > vlm-worker/Dockerfile.oci <<'DOCKERFILE'
ARG BASE=gcr.io/distroless/base-debian12:nonroot
FROM ${BASE}

ARG ONTAP_VERSION
ARG VLM_VERSION

WORKDIR /bin
COPY VSA/${ONTAP_VERSION}/VLM/${VLM_VERSION} /bin/main

USER nonroot:nonroot
ENTRYPOINT ["/bin/main"]
DOCKERFILE
```

Then build:

```bash
docker build --no-cache --platform linux/amd64 \
  --build-arg BASE=gcr.io/distroless/base-debian12:nonroot \
  --build-arg ONTAP_VERSION="${ONTAP_VERSION}" \
  --build-arg VLM_VERSION="${VLM_BINARY}" \
  -f vlm-worker/Dockerfile.oci \
  -t "${IMAGE_REGISTRY}/vlm-worker:${TAG}" .
```

> **Custom base image:** Replace `gcr.io/distroless/base-debian12:nonroot` with your
> org's hardened base image if required.

### What each build produces

| Image      | Binary → container path                    | Config baked in                        |
| ---------- | ------------------------------------------ | -------------------------------------- |
| oci-proxy  | `artifacts/oci-proxy/oci-proxy` → `/app`   | `common/vsa_config/` directory         |
| vcp-worker | `artifacts/vcp-worker/vcp-worker` → `/app` | `vlm-config-oci.json`, `vmrs_oci.yaml` |
| vlm-worker | `VSA/<ONTAP_VERSION>/VLM/<VLM_VERSION>` → `/bin/main` | None — config via Helm values |


---

## 8. Push images to OCIR

```bash
# Login to OCIR
echo "<AUTH_TOKEN>" | docker login "$OCIR_HOST" \
  -u "${OCIR_NS}/oracleidentitycloudservice/<USER_EMAIL>" \
  --password-stdin

# Push all three images
docker push "${IMAGE_REGISTRY}/oci-proxy:${TAG}"
docker push "${IMAGE_REGISTRY}/vcp-worker:${TAG}"
docker push "${IMAGE_REGISTRY}/vlm-worker:${TAG}"
```

---

## 9. Import VSA and Mediator images to OCI Compute

The `VSA/<ONTAP_VERSION>/` folder in the release bucket contains disk images required to launch storage VMs:

```
VSA/<ONTAP_VERSION>/VSA/<vsa-image-file>            # ONTAP storage VM disk image
VSA/<ONTAP_VERSION>/Mediator/<mediator-image-file>  # Mediator VM disk image
```

vcp-worker uses these images at runtime to provision VSA and Mediator VMs. It requires **OCI Compute custom image OCIDs** — not Object Storage paths — so the disk image objects must be imported as custom compute images in the deployer's OCI tenancy.

The resulting OCIDs are used in `vcp-worker-overrides.yaml`:

| OCID | Helm override field | Purpose |
|------|---------------------|---------|
| `VSA_IMAGE_OCID` | `workerConfig.vsaImageName` | ONTAP storage VM compute image |
| `VSA_MEDIATOR_IMAGE_OCID` | `workerConfig.vsaMediatorImageName` | Mediator VM compute image |

### 9.1 Import the VSA image

Import the VSA disk image (`VSA/<ONTAP_VERSION>/VSA/<vsa-image-file>`) from the release bucket as a custom compute image in your OCI tenancy. This can be done via the OCI Console, CLI, Terraform, or any method your team uses to create custom images.

Record the resulting OCID:

```bash
export VSA_IMAGE_OCID="<ocid1.image.oc1.<region>.aaaaaa...>"
```

### 9.2 Import the Mediator image

Import the Mediator disk image (`VSA/<ONTAP_VERSION>/Mediator/<mediator-image-file>`) as a custom compute image in the same compartment.

Record the resulting OCID:

```bash
export VSA_MEDIATOR_IMAGE_OCID="<ocid1.image.oc1.<region>.aaaaaa...>"
```

### 9.3 Where these OCIDs are used

Both OCIDs are required in `vcp-worker-overrides.yaml` (Section 10):

| OCID variable | Helm override field | Purpose |
|---|---|---|
| `VSA_IMAGE_OCID` | `workerConfig.vsaImageName` | vcp-worker uses this to launch ONTAP storage VMs |
| `VSA_MEDIATOR_IMAGE_OCID` | `workerConfig.vsaMediatorImageName` | vcp-worker uses this to launch Mediator VMs |

> **Supported formats:** Use `QCOW2` for `.qcow2` files and `VMDK` for `.vmdk`. Inspect the file extension to decide. See the [OCI custom image documentation](https://docs.oracle.com/en-us/iaas/Content/Compute/Tasks/importingcustomimage.htm) for supported formats and requirements.

---

## 10. Update override templates

Edit each override file in the `deploymentKit/` folder — replace every `<PLACEHOLDER>` with your environment values:

```bash
vi deploymentKit/oci-proxy-overrides.yaml
vi deploymentKit/vcp-worker-overrides.yaml
vi deploymentKit/vlm-worker-overrides.yaml
```

Key placeholder reference:


| Placeholder                 | Value                                                                              |
| --------------------------- | ---------------------------------------------------------------------------------- |
| `<OCIR_HOSTNAME>`           | Same as `$OCIR_HOST` (e.g. `iad.ocir.io`)                                          |
| `<OCIR_TENANCY_NS>`         | Same as `$OCIR_NS` (e.g. `idqogasfjw45`)                                           |
| `<OCI_REGION>`              | OCI region identifier (e.g. `us-ashburn-1`)                                        |
| `<POSTGRES_HOST>`           | OCI Database primary connection host                                               |
| `<IMAGE_TAG>`               | Value of `$TAG` — `echo $TAG`                                                      |
| `<VLM_IMAGE_TAG>`           | Value of `$TAG` — `echo $TAG` (vlm-worker image is pushed with the release tag)    |
| `<VSA_IMAGE_OCID>`          | Compute image OCID from Section 9.1 (`echo $VSA_IMAGE_OCID`)                       |
| `<VSA_MEDIATOR_IMAGE_OCID>` | Compute image OCID from Section 9.2 (`echo $VSA_MEDIATOR_IMAGE_OCID`)              |
| `<OCI_VAULT_OCID>`          | OCI Vault OCID                                                                     |
| `<OCI_VAULT_SECRET_OCID>`   | OCI Vault secret OCID                                                              |
| `<OCI_MASTER_KEY_OCID>`     | OCI Vault master encryption key OCID                                               |
| `<OCI_VAULT_COMPARTMENT_OCID>` | Compartment OCID for Vault secrets                                              |
| `<OCI_CERT_COMPARTMENT_OCID>`  | Compartment OCID for certificates                                               |
| `<OCI_ISSUER_CA_OCID>`      | OCI Certificate Authority OCID used to issue VSA certs                             |
| `<VSA_DEPLOYED_DNS_NAME>`   | DNS suffix for deployed VSA hosts (e.g. `iad.vsa`)                                 |
| `<OCI_VSA_DNS_ZONE_OCID>`   | OCI DNS zone OCID for VSA hostnames                                                |
| `<OCI_USE_TLS_SNI_OVERRIDE>` | Replace with unquoted `true` or `false` — enable TLS SNI override for VSA          |
| `<ONTAP_VERSION>`           | Same as `$ONTAP_VERSION` — `jq -r '.vsaArtifacts.versions[0].ontapVersion' "$MANIFEST"` |
| `<VSA_VLM_ENCRYPTION_KEY>`  | 32-byte AES-256 key — **must be identical** in vcp-worker and vlm-worker overrides |


Verify no placeholders remain before deploying:

```bash
for f in deploymentKit/oci-proxy-overrides.yaml deploymentKit/vcp-worker-overrides.yaml deploymentKit/vlm-worker-overrides.yaml; do
  awk '!/^[[:space:]]*#/ && /<[A-Z0-9_]+>/' "$f"
done
```

The command should produce no output (comment lines and Helm command examples are ignored).

---

## 11. Create namespace and OCIR pull secret

```bash
kubectl create namespace "$NS" 2>/dev/null || echo "Namespace already exists"

kubectl create secret docker-registry ocir-secret \
  --namespace "$NS" \
  --docker-server="$OCIR_HOST" \
  --docker-username="${OCIR_NS}/oracleidentitycloudservice/<USER_EMAIL>" \
  --docker-password="<AUTH_TOKEN>" \
  2>/dev/null || echo "Secret already exists"
```

---

## 12. Deploy with Helm

> **Important:** Temporal must be running and reachable **before** installing oci-proxy.
> oci-proxy blocks on Temporal connectivity at startup. If Temporal is not yet deployed,
> see [oci-temporal-deployment.md](../oci-temporal-deployment.md) first.
>
> Install order: **Temporal → vcp-worker → vlm-worker → oci-proxy**

```bash
helm upgrade --install vcp-worker "$VCP_WORKER_CHART" -n "$NS" \
  -f deploymentKit/vcp-worker-overrides.yaml

helm upgrade --install vlm-worker "$VLM_WORKER_CHART" -n "$NS" \
  -f deploymentKit/vlm-worker-overrides.yaml

helm upgrade --install oci-proxy "$OCI_PROXY_CHART" -n "$NS" \
  -f deploymentKit/oci-proxy-overrides.yaml
```

---

## 13. Verify

```bash
kubectl -n "$NS" get pods
```

All pods should reach `Running` / `1/1 Ready`. Then check logs:

```bash
kubectl -n "$NS" logs deploy/vcp-worker  --tail=20
kubectl -n "$NS" logs -l app=vlm-worker  --tail=20
kubectl -n "$NS" logs deploy/oci-proxy   --tail=20
```

Look for successful Temporal worker registration and database connectivity in each log.

---

## 14. Troubleshooting


| Symptom                                        | Check                                                                                                            |
| ---------------------------------------------- | ---------------------------------------------------------------------------------------------------------------- |
| `ImagePullBackOff`                             | Verify `ocir-secret` exists in the namespace; confirm `$OCIR_HOST` in secret matches image hostname in overrides |
| `CrashLoopBackOff` on oci-proxy                | Temporal not reachable — check `temporalAddress` in values and ensure Temporal is running                        |
| `CrashLoopBackOff` on vcp-worker or vlm-worker | DB host/credentials wrong, or `vsaVlmEncryptionKey` mismatch between the two                                     |
| Helm install fails                             | Ensure chart `.tgz` files extracted correctly and directories exist                                              |
| Missing placeholders                           | Run the Section 10 placeholder check — must return no output before deploying                                    |
| Pods stuck `Pending`                           | Check node capacity and namespace resource quotas                                                                |
| docker build `COPY` fails                      | Ensure all builds run from the release folder root — not from inside a subdirectory                              |
| Wrong architecture / exec format error         | Ensure `--platform linux/amd64` is used in all `docker build` commands                                           |


