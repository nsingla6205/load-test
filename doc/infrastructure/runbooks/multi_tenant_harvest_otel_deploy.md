# Multi-tenant Harvest + OpenTelemetry (VCP)

Initial deploy path for expert-mode / multi-tenant ONTAP metrics collection.
Single-tenancy [`kubernetes/vsa-harvest-otel`](../../kubernetes/vsa-harvest-otel) is unchanged.

## Feature flag

`multiTenancy.enabled` defaults to **true**. Production runs **both** charts
in namespace **`vcp`**:

| Path | Chart | Clusters |
|---|---|---|
| Single-tenancy | `vsa-harvest-otel` (ns `vcp`) | Non-MT / farm-registered clusters |
| Multi-tenancy | `vsa-harvest-multi-tenancy` (ns `vcp`) | MT / inventory-driven clusters |

Set the same flag in each independently deployed release:

```yaml
multiTenancy:
  enabled: true   # default
```

1. `vsa-harvest-multi-tenancy` — MT pollers + in-chart OTel sidecar when enabled
2. VCP Core — MT Istio allow for `getAllVolumeTenancy` when enabled

Set `enabled: false` only to stop MT resources; single-tenancy `vsa-harvest-otel` stays up either way.

## Components

1. **Harvest** — Helm chart [`vsa-harvest-multi-tenancy`](../../kubernetes/vsa-harvest-multi-tenancy)
   - Namespace: **`vcp`** (same as Core / single-tenancy harvest; separate Deployments)
   - One Deployment + Service per `inventory.clusters[]` entry (gcnv-harvest overrides shape)
   - Poller calls VCP `GET /v1/getAllVolumeTenancy?clusterName=<lowercase-inventory-name>`
   - Prometheus metrics on `:12990`

2. **OpenTelemetry** — **in-chart sidecar** on each harvest poller pod (not a separate
   `ontap-opentelemetry-collector` release; no MT changes in the OTel repo)
   - `otel.enabled: true` (default), ConfigMap key `relay`
   - Scrapes `127.0.0.1:12990` on the same pod
   - Drops samples without `tenant_project`, sets `gcp.project.id` from that label
   - **Tenant-only** export (no stamp / monitoring-project dual export on this path)

Sample values:

- Harvest + OTel overrides: [`ci/overrides-example.yaml`](../../kubernetes/vsa-harvest-multi-tenancy/ci/overrides-example.yaml)
- OTel value notes: [`ci/otel-multi-tenancy-values.example.yaml`](../../kubernetes/vsa-harvest-multi-tenancy/ci/otel-multi-tenancy-values.example.yaml)

## Coexistence with single-tenancy

| Concern | Single-tenancy | Multi-tenancy |
|---|---|---|
| Chart | `vsa-harvest-otel` | `vsa-harvest-multi-tenancy` |
| Namespace | `vcp` | `vcp` |
| Model | Shared PM2 farm + leases | One poller pod per cluster |
| OTel | Sidecar in harvest pods | Sidecar in each MT harvest pod (this chart) |
| Worker `HARVEST_HOST` | Farm service (unchanged) | N/A (static inventory) |

Do not point VCP worker farm registration at the MT chart.

## Prerequisites

1. Secret Manager entries with `pvt_ip`, `username`, `password` per inventory `secret`
2. Core API volume-tenancy bulk endpoint deployed
3. Istio policy allowing `cluster.local/ns/vcp/sa/harvest-multi-tenancy` → `GET /v1/getAllVolumeTenancy`
4. Workload Identity on harvest SA (ServiceControl + Core API + tenant Metric Writer) — confirm IAM model with PE before pilot

## Inventory

`inventory.clusters[].name` is lowercased for poller identity, VCP `clusterName`, and
Service DNS. The OTel sidecar does **not** need a separate `clusters[]` list (localhost scrape).
This inventory is **MT-only** — it is not the gcnv-harvest stamp list.

## Chart version compatibility

VCP CI stamps `kubernetes/vsa-harvest-multi-tenancy/Chart.yaml` with the same
release tag used for `vsa-harvest-otel`. Regional repos must set
`chartVersion` to that published tag (same bump process as
`cloud-deploy/vsa-harvest-otel/vsa-harvest-otel.yaml`).

## Regional follow-up

Once this sidecar path is deployed, stop deploying the separate
`ontap-opentelemetry-collector-multi-tenancy` Cloud Deploy package (if present)
to avoid double scrape/export. Regional packages that still target namespace
`ontap-monitoring` must be updated to **`vcp`**.
