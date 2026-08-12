# Multi-tenant Harvest + OpenTelemetry (VCP)

Initial deploy path for expert-mode / multi-tenant ONTAP metrics collection.
Single-tenancy [`kubernetes/vsa-harvest-otel`](../../kubernetes/vsa-harvest-otel) is unchanged.

## Feature flag

`multiTenancy.enabled` defaults to **true**. Production runs **both** charts:

| Path | Chart | Clusters |
|---|---|---|
| Single-tenancy | `vsa-harvest-otel` (ns `vcp`) | Non-MT / farm-registered clusters |
| Multi-tenancy | `vsa-harvest-multi-tenancy` (ns `ontap-monitoring`) | MT / inventory-driven clusters |

Set the same flag in each independently deployed release:

```yaml
multiTenancy:
  enabled: true   # default
```

1. `vsa-harvest-multi-tenancy` — MT pollers when enabled
2. VCP Core — MT Istio allow for `getAllVolumeTenancy` when enabled
3. `ontap-opentelemetry-collector` — tenant-project routing when enabled

Set `enabled: false` only to stop MT resources; single-tenancy `vsa-harvest-otel` stays up either way.

## Components

1. **Harvest** — Helm chart [`vsa-harvest-multi-tenancy`](../../kubernetes/vsa-harvest-multi-tenancy)
   - Namespace: `ontap-monitoring`
   - One Deployment + Service per `inventory.clusters[]` entry (gcnv-harvest overrides shape)
   - Poller calls VCP `GET /v1/getAllVolumeTenancy?clusterName=<lowercase-inventory-name>`
   - Prometheus metrics on `:12990`

2. **OpenTelemetry** — separate `ontap-opentelemetry-collector` release
   - `multiTenancy.enabled: true`, `replicaCount: 1`
   - Scrapes `harvest-poller-<name>.ontap-monitoring.svc.cluster.local:12990`
   - Routes admitted series to customer `tenant_project` via `gcp.project.id`
   - **Not in this pass:** stamp / monitoring-project dual export

Sample values:

- Harvest overrides: [`ci/overrides-example.yaml`](../../kubernetes/vsa-harvest-multi-tenancy/ci/overrides-example.yaml)
- OTel values: [`ci/otel-multi-tenancy-values.example.yaml`](../../kubernetes/vsa-harvest-multi-tenancy/ci/otel-multi-tenancy-values.example.yaml)

## Coexistence with single-tenancy

| Concern | Single-tenancy | Multi-tenancy |
|---|---|---|
| Chart | `vsa-harvest-otel` | `vsa-harvest-multi-tenancy` |
| Namespace | `vcp` | `ontap-monitoring` |
| Model | Shared PM2 farm + leases | One poller pod per cluster |
| OTel | Sidecar in harvest pods | Separate ontap-otel chart |
| Worker `HARVEST_HOST` | Farm service (unchanged) | N/A (static inventory) |

Do not point VCP worker farm registration at the MT chart.

## Prerequisites

1. Secret Manager entries with `pvt_ip`, `username`, `password` per inventory `secret`
2. Core API volume-tenancy bulk endpoint deployed
3. Istio policy allowing `cluster.local/ns/ontap-monitoring/sa/harvest-multi-tenancy` → `GET /v1/getAllVolumeTenancy`
4. Workload Identity on harvest SA (ServiceControl + Core API) and OTel SA (tenant Metric Writer) — confirm IAM model with PE before pilot

## Inventory sync

`inventory.clusters[].name` (MT harvest) and `clusters[]` (MT OTel) must stay
synchronized and lowercased. This inventory is **MT-only** — it is not the
gcnv-harvest stamp list. Do not copy clusters from `cloud-deploy/harvest`.

## Chart version compatibility

VCP CI stamps `kubernetes/vsa-harvest-multi-tenancy/Chart.yaml` with the same
release tag used for `vsa-harvest-otel`. Regional repos must set
`chartVersion` to that published tag (same bump process as
`cloud-deploy/vsa-harvest-otel/vsa-harvest-otel.yaml`).
