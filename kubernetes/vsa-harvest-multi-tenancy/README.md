# vsa-harvest-multi-tenancy

Deploys one direct harvest-multi poller per ONTAP cluster for **multi-tenancy**.
Isolated from single-tenancy [`vsa-harvest-otel`](../vsa-harvest-otel) (PM2 farm in `vcp`).

## Topology

| Path | Chart | Namespace |
|---|---|---|
| Single-tenancy (unchanged) | `vsa-harvest-otel` | `vcp` |
| Multi-tenancy (this chart) | `vsa-harvest-multi-tenancy` | `ontap-monitoring` |

Chart versioning matches `vsa-harvest-otel`: CI stamps `Chart.yaml`
`version`/`appVersion` from the VCP release tag. Regional
`chartVersion` must use that same published tag (do not keep a separate
`0.1.0` channel).

OTel for MT is a **separate** `ontap-opentelemetry-collector` release with
`multiTenancy.enabled: true` (see `ci/otel-multi-tenancy-values.example.yaml`).

## Feature flag

`multiTenancy.enabled` defaults to **true**. Prod runs **both** this chart and
single-tenancy `vsa-harvest-otel` because clusters exist on both paths. Use the
same flag name in each independently deployed chart:

```yaml
multiTenancy:
  enabled: true   # default
```

- This chart: deploys MT pollers when enabled; renders nothing when false.
- VCP Core chart: MT Istio allow for `getAllVolumeTenancy` when enabled.
- ONTAP OTel chart: tenant-project routing when enabled (separate release).
- Single-tenancy `vsa-harvest-otel` is unaffected either way.

## Inventory (gcnv-harvest compatible)

```yaml
inventory:
  clusters:
    - name: us-oma-gc-cl01-d001c220r0107
      datacenter: us-oma
      addr: 192.168.44.4
      secret: US-OMA-GC-CL01-D001C220R0107-sre-ro
```

Secret Manager payload per `secret` key: `pvt_ip`, `username`, `password`.

## Install

```bash
helm upgrade --install vsa-harvest-mt ./kubernetes/vsa-harvest-multi-tenancy \
  --namespace ontap-monitoring --create-namespace \
  -f kubernetes/vsa-harvest-multi-tenancy/ci/overrides-example.yaml
```

Each cluster renders Service DNS:

```text
harvest-poller-<lowercase-name>.ontap-monitoring.svc.cluster.local:12990
```

Keep the same lowercased names in OTel `clusters[]`.

## Exports

- **Consumer project**: Harvest ServiceControl (`project` label)
- **Tenant project**: OTel `tenant_project` → `gcp.project.id` (tenant-only for this pass)

## Dependencies

- Core API `GET /v1/getAllVolumeTenancy` available in the environment
- Istio allow from `ontap-monitoring/sa/harvest-multi-tenancy` to that path
- PE IAM: OTel SA needs Monitoring Metric Writer on customer tenant projects
