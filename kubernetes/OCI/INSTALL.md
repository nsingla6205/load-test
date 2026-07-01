# OCI Helm charts — installation and versioning

This guide covers installing **`oci-proxy`**, **`vcp-worker-chart`**, and **`vlm-worker`** on OKE with Temporal-safe **task queue versioning** and **parallel VCP worker images**.

## Concepts

| Mechanism | Purpose |
|-----------|---------|
| **`versionsSupported` (VCP)** | One Kubernetes Deployment + ConfigMap per entry. Old binaries keep polling until you remove their row after workflows drain. |
| **`TASK_QUEUE_VERSION` / `taskQueueVersion`** | Starters (`oci-proxy`) and workers (`vcp-worker`) append this suffix to base queues (e.g. `customer-workflows-<suffix>`). **Must match** between oci-proxy and the **newest** VCP `versionsSupported[].name` (after sanitizing: lower case, `.` → `-`). |
| **`ontapVersionVlmImageMappings` (VLM)** | One Deployment per ONTAP line; pin immutable image tags/digests in production. |

Application code still requires safe Temporal workflow changes (`GetVersion`, etc.) for incompatible logic; charts prevent accidental **single-pod** cutover from replacing all workers at once.

---

## Prerequisites

1. **Namespaces** — e.g. `vcp` for workers and proxy, `vlm` for VLM (adjust to your convention).
2. **Secrets**
   - OCIR pull secret (e.g. `ocir-secret`) in each namespace that pulls images.
   - PostgreSQL credentials secret referenced by `workerConfig.database.existingSecretName` (VCP) and oci-proxy database settings.
3. **Temporal** — frontend reachable at the address in `workerConfig.temporalAddress` / `temporalAddress`.
4. **Shared encryption key** — `workerConfig.vsaVlmEncryptionKey` must match between VCP and VLM values.

---

## 1. Configure values

### 1.1 `vcp-worker-chart`

Copy `values.yaml` and merge **`overrides.yaml`** (or `overrides_nmobsa.yaml`) with your environment.

Key fields:

- **`versionsSupported`** — List of `{ name, sha }`. With `global.useTags: true`, leave **`images.vcpWorker.tag` empty** so each `name` is the image tag. With `useTags: false`, set `sha` to the image digest per row.
- **`workerConfig.nkdevTest`** — `false` in production (uses `versionsSupported[].name` for deployment names and `TASK_QUEUE_VERSION`).
- **`workerConfig.database.*`** — OCI PostgreSQL host, `existingSecretName`, etc.

Example: single version `23032026`:

```yaml
versionsSupported:
  - name: "23032026"
    sha: ""
images:
  vcpWorker:
    tag: ""
```

### 1.2 `oci-proxy`

Set **`taskQueueVersion`** to the **same string** as the **last** (newest) entry in VCP `versionsSupported`, before sanitization you use in practice (the chart sanitizes: lower, `.` → `-`).

Example if newest VCP version name is `23032026`:

```yaml
taskQueueVersion: "23032026"
```

If empty, **`Chart.Version`** is used (pipeline must keep oci-proxy and VCP queue suffixes aligned if you rely on this).

### 1.3 `vlm-worker`

Set **`ontapVersionVlmImageMappings`** with **pinned** tags (and digests in prod). Align **`workerConfig.vsaVlmEncryptionKey`** with VCP.

---

## 2. Install order

1. **Secrets** (OCIR, DB, optional OCI auth if not using workload identity).
2. **`vlm-worker`** (or ensure VLM is up before VCP needs it).
3. **`vcp-worker-chart`**.
4. **`oci-proxy`** (API that starts workflows — must see the **same** `taskQueueVersion` as newest VCP workers).

Use consistent **image tags / version names** across the release in your CI values.

---

## 3. Helm commands

Replace `<namespace>`, release names, and `-f` files as appropriate.

```bash
# VLM
helm upgrade --install vlm-worker ./vlm-worker -n <namespace> \
  -f ./vlm-worker/values.yaml \
  -f ./vlm-worker/overrides.yaml

# VCP worker
helm upgrade --install vcp-worker ./vcp-worker-chart -n vcp \
  -f ./vcp-worker-chart/values.yaml \
  -f ./vcp-worker-chart/overrides.yaml

# OCI proxy
helm upgrade --install oci-proxy ./oci-proxy -n vcp \
  -f ./oci-proxy/values.yaml \
  -f ./oci-proxy/overrides.yaml
```

Dry-run:

```bash
helm upgrade --install vcp-worker ./vcp-worker-chart -n vcp -f ./vcp-worker-chart/values.yaml -f ./vcp-worker-chart/overrides.yaml --dry-run --debug
```

---

## 4. Rolling out a **new** VCP image without dropping in-flight workflows

1. **Build and push** the new image (tag e.g. `24042026`).
2. **Append** a new row to **`versionsSupported`** (do not remove the old row yet):

   ```yaml
   versionsSupported:
     - name: "23032026"
       sha: ""
     - name: "24042026"
       sha: ""
   ```

3. **Upgrade** `vcp-worker-chart`. You should see a **second** Deployment (e.g. `vcp-customer-worker-24042026`) alongside the old one.
4. Set **`oci-proxy` `taskQueueVersion`** to **`24042026`** (or the sanitized equivalent) so **new** workflow starts use the new queue. Upgrade **oci-proxy**.
5. Wait until **Temporal** shows no active workflows on the old task queue / version (per your runbook and metrics).
6. **Remove** the old `versionsSupported` row and **helm upgrade** VCP again to scale down old Deployments.

---

## 5. Metrics Service (VCP)

The metrics Service selects pods for the **last** entry in **`versionsSupported`** (newest). Scraping targets the latest worker by design.

---

## 6. Troubleshooting

| Symptom | Check |
|---------|--------|
| Workflows not progressing | `TASK_QUEUE_VERSION` on oci-proxy vs `TASK_QUEUE_VERSION` env on VCP pods (newest) must match. |
| Image pull errors | `versionsSupported[].name` matches tag in registry; `global` registry paths and `imagePullSecrets`. |
| Non-deterministic errors | Workflow code change without Temporal versioning; charts alone do not fix incompatible history. |

---

## 7. Files reference

| Chart | Default values | Example overrides |
|-------|----------------|-------------------|
| vcp-worker-chart | `vcp-worker-chart/values.yaml` | `overrides.yaml`, `overrides_nmobsa.yaml` |
| oci-proxy | `oci-proxy/values.yaml` | `overrides.yaml`, `overrides_nmobsa.yaml` |
| vlm-worker | `vlm-worker/values.yaml` | `overrides.yaml` (if present) |
