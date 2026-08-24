# vsa-harvest-multi-tenancy

Deploys one direct harvest-multi poller per ONTAP cluster for **multi-tenancy**,
with an in-chart OpenTelemetry sidecar for **tenant-project** metric export.
Isolated from single-tenancy [`vsa-harvest-otel`](../vsa-harvest-otel) (PM2 farm),
but runs in the **same `vcp` namespace** as separate Deployments.

## Topology

| Path | Chart | Namespace |
|---|---|---|
| Single-tenancy (unchanged) | `vsa-harvest-otel` | `vcp` |
| Multi-tenancy (this chart) | `vsa-harvest-multi-tenancy` | `vcp` |

Chart versioning matches `vsa-harvest-otel`: CI stamps `Chart.yaml`
`version`/`appVersion` from the VCP release tag. Regional
`chartVersion` must use that same published tag (do not keep a separate
`0.1.0` channel).

**OTel for MT lives in this chart** as a sidecar on each harvest poller pod
(same pattern as `vsa-harvest-otel`). Do **not** change the
`ontap-open-telemetry` repo for MT; the separate MT OTel Helm release is not
required for this path.

## Feature flag

`multiTenancy.enabled` defaults to **true**. Prod runs **both** this chart and
single-tenancy `vsa-harvest-otel` because clusters exist on both paths. Use the
same flag name in Core for the Istio allow:

```yaml
multiTenancy:
  enabled: true   # default
```

- This chart: deploys MT pollers (+ OTel sidecar when `otel.enabled`) when enabled; renders nothing when false.
- VCP Core chart: MT Istio allow for `getAllVolumeTenancy` when enabled.
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
  --namespace vcp --create-namespace \
  -f kubernetes/vsa-harvest-multi-tenancy/ci/overrides-example.yaml
```

Each cluster renders Service DNS (useful for debugging / external scrapers):

```text
harvest-poller-<lowercase-name>.vcp.svc.cluster.local:12990
```

The OTel sidecar scrapes **`127.0.0.1:<service.port>`** on the same pod — no
separate OTel `clusters[]` list to keep in sync.

The poller image defaults to the same Artifact Registry mirror used by
`vsa-harvest-otel` (`vcp-container-images-us/harvest-multi`). Regional
overrides should keep the image on an accessible mirror and pin its digest.

## Exports

- **Consumer project**: Harvest ServiceControl (`project` label)
- **Tenant project**: OTel sidecar (`tenant_project` → `gcp.project.id`);
  samples without `tenant_project` are dropped. No stamp/monitoring dual export
  on the MT path.

## Dependencies

- Core API `GET /v1/getAllVolumeTenancy` available in the environment
- Istio allow from `vcp/sa/harvest-multi-tenancy` to that path
- Pod annotation `proxy.istio.io/config: holdApplicationUntilProxyStarts` (same as `vsa-harvest-otel`) so Harvest does not dial ONTAP before the sidecar is ready
- PE IAM: the harvest WI GCP SA needs ServiceControl + Core API access **and**
  Monitoring Metric Writer on customer tenant projects (sidecar shares the poller SA)

Set `serviceAccount.gcpServiceAccount` in regional overrides. The chart uses the
value for both the KSA `iam.gke.io/gcp-service-account` annotation and the
poller's `GCP_AUTH_SERVICE_ACCOUNT` environment variable, matching the existing
`vsa-harvest-otel` identity setup. The same GSA may be reused when PE has granted
it the required tenant-project permissions and the
`roles/iam.workloadIdentityUser` binding for
`serviceAccount:PROJECT.svc.id.goog[vcp/harvest-multi-tenancy]`.

Set `coreAPI.gcpServiceURL` to the same value as Core API's `GCP_SERVICE_URL`
and the regional `vsa-harvest-otel` `harvestConfig.gcpServiceUrl`. The poller
uses it as the tenancy JWT audience; an empty or different value causes Core
API to reject `getAllVolumeTenancy` with `invalid issuer or audience in JWT`.
