# Public API Resource Index

This file replaces the former monolithic endpoint listing. Each resource now has a dedicated, detailed guide including:
- CRUD endpoint definitions (request + response examples)
- Internal orchestration & workflow sequence
- LRO (Long Running Operation) lifecycle and polling semantics
- PlantUML sequence diagram of the call path

## Resource Guides
| Resource | Guide Path |
|----------|------------|
| Pools | doc/api/resources/pools.md |
| Volumes | doc/api/resources/volumes.md |
| Snapshots | doc/api/resources/snapshots.md |
| Backups & Backup Vaults | doc/api/resources/backups.md |
| Replications | doc/api/resources/replications.md |
| HostGroups | doc/api/resources/hostgroups.md |
| Active Directories | doc/api/resources/active-directories.md |
| KMS Configurations | doc/api/resources/kms-configs.md |
| Backup Policies | doc/api/resources/backup-policies.md |
| Operations (LRO Polling) | doc/api/resources/operations.md |
| Health | doc/api/resources/health.md |
| Ops Inventory (Core, internal, read-only) | doc/api/resources/ops-inventory.md |

## Conventions (Global)
- Base Path: `/v1beta/projects/{projectNumber}/locations/{locationId}`
- Correlation Header: `X-Correlation-Id`
- UUID: 36-char v4 for primary identifiers (poolId, volumeId, etc.)
- LRO: Mutating operations return HTTP 202 Operation objects unless synchronous creation permitted (some backup creation 201 cases)
- Errors: Standard envelope `{code:number,message:string}` with HTTP statuses (400, 401, 403, 404, 409, 422, 429, 500, 501, 503)

## LRO Polling Quick Reference
1. Receive 202 with `name` = Operation resource path.
2. Poll `GET {operationName}` until `done=true`.
3. On success: `response` holds final resource.
4. On failure: `error` present (HTTP 200 on polling route).

For per-resource specific fields (states, transitions, rollback semantics) see the individual guides.

---
For full schema consult `google-proxy/api/gcp-api.yaml`.
