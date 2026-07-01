# oci-infra MCP — pre-build OCI release gate

Install the **exact release shared to OCI** locally and catch failures (DB
migrations, pool/SVM CRUD) **before** generating a build. Born from the
production `Dirty database version 40. Fix and force version.` incident.

Everything is a thin wrapper over the shell scripts in
`scripts/oci-local-dev/`, so the same checks run by hand, in CI, or via the
agent over MCP.

## Phases

| Phase | What | Tool / script | Catches |
|------|------|---------------|---------|
| 0 | Run the **OCI release image** as a migrator (single, or old→new upgrade) against a prod-like schema | `run_migration_gate` / `run_migration_upgrade` / `oci-migrate-gate.sh` | dirty schema, broken/upgrade migrations (the dirty-40 bug) |
| 1 | Install the **pinned release** images and bring the stack up | `set_release` + `bringup` / `release.sh`, `stack.sh` | image/tag/auth problems, startup/config/migrate-on-start failures |
| 2 | Pool + SVM CRUD smoke against the release `oci-proxy` | `crud_smoke` / `crud-smoke.sh` | API/contract regressions, workflow dispatch, missing registration, deterministic workflow/DB defects |

> **OCI migration model:** OCI has no `vcp-db-migrate` image. The `oci-proxy`
> image migrates on start (`RUN_MIGRATION_ON_START=true` → `db.Migrate()`, before
> Temporal), so Phase 0 drives the **actual release image** as a one-shot
> migrator. (`migration-gate.sh` is the legacy source-based `go run` path, kept
> for fast pre-commit dev.)

### Honest scope
- Phase 0 is the highest-ROI gate and is fully reproducible — **feed it a
  prod-like snapshot** (`schema-snapshot.sh`), because a fresh empty DB will not
  reproduce drift-dependent failures (e.g. `CREATE OR REPLACE VIEW` → 42P16).
- Phase 2 validates the **control plane**. Real pool creation provisions OCI
  compute and talks to a per-pool ONTAP cluster IP that only exists after
  provisioning, so full CRUD cannot go green locally. `crud-smoke.sh` reports
  `PARTIAL` when a flow dispatches and runs but stops at that external boundary,
  and only `FAIL`s on control-plane defects. `ontap_sim.py` is the seam toward
  fuller e2e once OCI compute is also faked.

## Setup

The server is launched through a **self-bootstrapping** wrapper,
[`run-server.sh`](run-server.sh): on first run it creates a local `.venv` next
to itself and installs `requirements.txt`; later runs just exec the server. It
resolves its own path, so it works regardless of where the repo is cloned. You
do **not** need to create the venv by hand. (`.venv/` is gitignored, so each
clone bootstraps its own.)

### Register the server in Cursor (per developer)

`.cursor/mcp.json` is **gitignored** (it can hold machine-specific paths), so it
is not committed — each developer adds the entry once. Use a
`${workspaceFolder}`-relative path (no home-dir or absolute paths), so the same
snippet works on every machine:

- If you open **`vsa-control-plane`** as your Cursor workspace, put this in
  `vsa-control-plane/.cursor/mcp.json`:

  ```json
  {
    "mcpServers": {
      "oci-infra": {
        "command": "${workspaceFolder}/scripts/oci-local-dev/mcp/run-server.sh"
      }
    }
  }
  ```

- If you open the **parent folder** as your workspace (as the local-dev guide
  suggests, so `docker-compose.local.yml` is at the root), put this in
  `<parent>/.cursor/mcp.json` instead (note the `vsa-control-plane/` prefix):

  ```json
  {
    "mcpServers": {
      "oci-infra": {
        "command": "${workspaceFolder}/vsa-control-plane/scripts/oci-local-dev/mcp/run-server.sh"
      }
    }
  }
  ```

Then reload Cursor (Settings → MCP) and the agent can call the tools below. The
first call triggers the one-time venv bootstrap (a few seconds).

## Typical flow (agent or CLI)

```bash
cd scripts/oci-local-dev

# 1. Pin the exact release shared to OCI
cp release.example.json my-release.json && $EDITOR my-release.json
./release.sh apply my-release.json
./release.sh verify          # optional: confirm images are reachable

# 0. Migration gate — image-driven (uses the pinned oci-proxy image)
./oci-migrate-gate.sh --fresh
#    ...or test the real upgrade old image -> new image:
./oci-migrate-gate.sh \
    --from-image iad.ocir.io/idqogasfjw45/oci-proxy:<old> \
    --to-image   iad.ocir.io/idqogasfjw45/oci-proxy:<new>

# 1. Bring up the release stack (+crud services for Phase 2)
./stack.sh up --crud

# 2. Control-plane CRUD smoke
./crud-smoke.sh

# teardown
./stack.sh down --crud --wipe
```

## MCP tools

- `set_release(manifest_path)` / `show_release()` / `verify_release()`
- `run_migration_gate(image="", snapshot_path="", fresh=True, timeout_seconds=240)`
- `run_migration_upgrade(from_image, to_image, timeout_seconds=240)`
- `list_proxy_versions(repo="", all_tags=False)` — list oci-proxy tags in the registry
- `check_upgrade_matrix(from_ref, to_refs="", repo="", all_tags=False, limit=0, timeout_seconds=240)`
  — run the upgrade gate from a baseline to many candidates; reports a PASS/FAIL
  matrix + the recommended (highest clean) upgrade target
- `make_snapshot_from_release(ref, out=...)`
- `bringup(crud=False)` / `teardown(crud=True, wipe=False)` / `status()`
- `get_logs(service, tail=200)`
- `crud_smoke(timeout_seconds=90, keep=False)`

### Version compatibility (which version is safe to upgrade to)

```bash
cd scripts/oci-local-dev

# what oci-proxy versions exist?
./version-compat.sh list                       # release-like tags ( --all for every tag )

# from the version you run today, test the upgrade to every newer candidate:
./version-compat.sh check --from 26061.0.0-DEV.33
# ...or test specific targets only:
./version-compat.sh check --from 26061.0.0-DEV.33 --to 26062.0.0-OCI-RC.1,26062.0.0-DEV.31_1
```

Each candidate brings a fresh DB to the baseline release, then applies the
candidate's migrations on top (the real OCI upgrade). The matrix marks each
`PASS` (clean), `FAIL` (dirty — with the offending `schema_migrations_post`
version + file), or `ENV?` (amd64 emulation crash on Apple Silicon —
inconclusive; confirm on a native amd64 host), and recommends the highest clean
target. MCP: `list_proxy_versions()` / `check_upgrade_matrix(from_ref=...)`.

## Running the gate

There is no dedicated CI workflow; run Phase 0 on demand via the CLI
(`scripts/oci-local-dev/migration-gate.sh --fresh [--snapshot ...]`) or the MCP
tools. Commit a vetted `snapshots/vcp-prodlike.sql` so the gate reproduces prod
drift.
