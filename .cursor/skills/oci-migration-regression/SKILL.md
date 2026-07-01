---
name: oci-migration-regression
description: >-
  Run the OCI database migration regression locally before a build, using the
  actual OCI release images. Use when validating that a release's migrations
  apply cleanly, reproducing the "Dirty database version N. Fix and force
  version." failure, or checking an old->new release upgrade. OCI has NO
  db-migrate image: the oci-proxy image migrates on start. Use when the user
  mentions OCI migrations, the migration gate, dirty database versions, release
  image migrations, or upgrade regressions.
---

# OCI Migration Regression

## OCI migration model (read first)

OCI has **no `vcp-db-migrate` job/image** (that is the GCP/Helm path). The
**`oci-proxy` image is the migrator**: on start, with
`RUN_MIGRATION_ON_START=true`, it runs `db.Migrate()` (pre-migrations → GORM
AutoMigrate → post-migrations → post-fixes) *before* it connects to Temporal
(`oci-proxy/app.go` `main()`). So every check here drives the **actual release
image** as a one-shot migrator — never `go run`, never a db-migrate image.

A failure leaves golang-migrate's `schema_migrations` / `schema_migrations_post`
row `dirty=true`, which blocks all later migrations — the production
`Dirty database version N` error.

## When to use which mode

| Goal | Mode |
|------|------|
| Does ONE release image migrate cleanly (optionally on a prod-like schema)? | single |
| Does upgrading from release A images to release B images stay clean? | upgrade |
| From the version I run today, which available versions are SAFE to upgrade to? | matrix |

## Workflow

```
Task Progress:
- [ ] 1. Pin the release image(s)
- [ ] 2. Verify the image(s) are pullable
- [ ] 3. Run the gate (single or upgrade)
- [ ] 4. Interpret PASS / FAIL
```

All commands run from the repo root (`vsa-control-plane/`). Each maps to an
`oci-infra` MCP tool — drive it by chat or by shell, identically.

### 1. Pin the release

A manifest pins the exact image set (see `scripts/oci-local-dev/release.example.json`):
the `oci-proxy` ref is the migrator.

```bash
bash scripts/oci-local-dev/release.sh apply scripts/oci-local-dev/current-release.json
bash scripts/oci-local-dev/release.sh show
```
MCP: `set_release(manifest_path=...)`, `show_release()`.

### 2. Verify images are pullable

```bash
bash scripts/oci-local-dev/release.sh verify   # needs `docker login iad.ocir.io`
```
MCP: `verify_release()`. `manifest unknown` = wrong tag; `unauthorized` = login.

### 3. Run the gate

**Single** (uses the pinned `OCI_PROXY_IMAGE` by default):

```bash
bash scripts/oci-local-dev/oci-migrate-gate.sh --fresh
# explicit image + prod-like baseline:
bash scripts/oci-local-dev/oci-migrate-gate.sh \
  --image iad.ocir.io/idqogasfjw45/oci-proxy:<tag> \
  --snapshot scripts/oci-local-dev/snapshots/vcp-prodlike.sql
```
MCP: `run_migration_gate(image="", snapshot_path="", fresh=True)`.

**Upgrade** (old release image → new release image — the real OCI upgrade path):

```bash
bash scripts/oci-local-dev/oci-migrate-gate.sh \
  --from-image iad.ocir.io/idqogasfjw45/oci-proxy:<old-tag> \
  --to-image   iad.ocir.io/idqogasfjw45/oci-proxy:<new-tag>
```
MCP: `run_migration_upgrade(from_image=..., to_image=...)`.

**Matrix** (which available versions are safe to upgrade to — runs the upgrade
gate from one baseline against many candidate versions discovered in the
registry):

```bash
# discover available oci-proxy versions
bash scripts/oci-local-dev/version-compat.sh list           # --all for every tag
# test the upgrade from today's version to each newer candidate
bash scripts/oci-local-dev/version-compat.sh check --from <current-tag>
# ...or only specific targets:
bash scripts/oci-local-dev/version-compat.sh check --from <current-tag> \
  --to <tagA>,<tagB>
```
MCP: `list_proxy_versions()`, `check_upgrade_matrix(from_ref=..., to_refs="")`.
Output is a per-candidate `PASS` / `FAIL` (with the dirty version + file) / `ENV?`
(amd64 emulation crash — inconclusive) table, plus the recommended highest clean
target. It tests sequentially and can take minutes per candidate; use `--to` or
`--limit` to bound it.

### 4. Interpret results

- **GATE PASSED** → migrations applied with no dirty versions.
- **GATE FAILED (dirty_version N)** → the run names the table, phase, and likely
  `database/vcp/migrations/<phase>/<file>.up.sql`. Fix the SQL, rebuild the
  image, re-run. In upgrade mode, a baseline failure means the OLD release
  itself can't migrate a fresh DB (fix that first); a candidate failure is the
  upgrade breaking — block the build.

## How it runs the image

The gate starts the image detached, sharing the Postgres container's network
namespace (`--network container:vcp-postgres`, so `DB_HOST=127.0.0.1`), with
`RUN_MIGRATION_ON_START=true`, `LOCAL_REGION=us-east1` (a valid REGION_NUMBER_MAP
key — `us-ashburn-1` would make `LoadConfig` exit), and `TEMPORAL_ADDRESS` set to
a dead port so the proxy exits right after migrating. Outcome is classified from
stable migrate log markers plus an authoritative dirty-row check, then the
migrator container is removed.

## Prerequisites

- Docker Desktop running; `docker login iad.ocir.io` for private OCIR repos.
- A local Postgres — the gate starts one via `docker-compose.local.yml` or a
  standalone `postgres:16-alpine` container (used automatically when no compose
  provider is available).

### Apple Silicon caveat (important)

The release images are `linux/amd64`. Some are built with a Go version that
**crashes in the Go runtime under QEMU emulation** on Apple Silicon
(`fatal error: lfstack.push` / `invalid packing`) — before any migration runs.
The gate detects this and reports it as an ENVIRONMENT issue (exit code 3), not a
migration defect. To get a real image-driven result, run the gate on a **native
`linux/amd64` host** (CI runner, OKE amd64 node, or amd64 VM). On the Mac, use
the source/git-ref path instead: `make_snapshot_from_release(ref=<old-ref>)` then
`run_migration_gate(snapshot_path=...)` (compiles natively, no emulation).

## Notes

- A fresh empty DB will NOT reproduce drift-dependent failures (e.g. SQLSTATE
  42P16 from `CREATE OR REPLACE VIEW`). Use `--snapshot` or upgrade mode.
- Snapshots: `schema-snapshot.sh from-db --dsn <replica-dsn>` (sanitized replica)
  or `from-release --ref <git-ref>` (replay a deployed release's chain).
- Full reference: `doc/guides/oci-infra-mcp-regression.md`.
