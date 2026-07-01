# OCI Pre-Build Regression: the `oci-infra` MCP, the Agent, and the Gates

> **Audience:** OCI / VSA Control Plane engineers and release owners.
> **TL;DR:** Pin the *exact* image versions shared to OCI and run migration +
> pool/SVM CRUD regressions **before** cutting a build — driven by an agent over
> an MCP server, or by hand, or in CI. Born from the production incident
> `Dirty database version 40. Fix and force version.`

---

## 1. Why this exists

A post-migration (`0040_vpg_is_shared_to_allocation_type`) failed in production
and left golang-migrate's version table **dirty**, which blocks *every*
subsequent migration in that phase. The existing PR check only verified that
migration `*.up.sql` / `*.down.sql` files exist as **pairs** — it never *runs*
the SQL, so a runtime failure (e.g. `CREATE OR REPLACE VIEW` raising SQLSTATE
`42P16` against a drifted schema) shipped undetected.

This harness closes that gap: it **installs the exact release** and **executes**
migrations and control-plane CRUD against it, so the failure surfaces locally or
in CI instead of in a customer environment.

---

## 2. Key concepts

| Term | Meaning |
|------|---------|
| **MCP server** | Model Context Protocol server — exposes a set of agent-callable *tools*. Our `oci-infra` server is a thin wrapper over shell scripts; it adds no logic of its own. |
| **The agent** | The `oci-local-dev` Cursor subagent (`.cursor/agents/oci-local-dev.mdc`) that operates the local stack and now also drives the regression gates via the MCP tools. |
| **Release pin** | A release is **not** one version string; it is the set of image tags/digests for `oci-proxy`, `vcp-worker`, `vlm-worker` (+ the ONTAP line). The harness pins all of them. |
| **The three phases** | Phase 0 = migrations, Phase 1 = install the pinned release, Phase 2 = pool/SVM CRUD smoke. |

### How the layers fit together

```
        Agent (oci-local-dev)        You (CLI)            GitHub Actions
                 |                       |                      |
                 v                       |                      |
        oci-infra MCP server            |                      |
        (scripts/.../mcp/server.py)     |                      |
                 \______________________|______________________/
                                  |
                                  v
              shell scripts in scripts/oci-local-dev/
   release.sh  migration-gate.sh  stack.sh  crud-smoke.sh  schema-snapshot.sh
                                  |
                                  v
                       Docker + compose stack
        postgres · temporal · vlm-worker · oci-proxy · vcp-worker · ontap-sim
```

The single source of truth is the **scripts**. The MCP server, the agent, and CI
all call the same scripts, so a green run means the same thing everywhere.

---

## 3. Prerequisites

- macOS (Apple Silicon supported) or Linux.
- **Docker** (Docker Desktop on macOS). The harness auto-adds Docker Desktop's
  CLI to PATH if it was not symlinked at install.
- Registry access to pull the pinned images (`docker login iad.ocir.io`) — the
  image-driven Phase 0 gate pulls the `oci-proxy` release image. GHCR fallback
  also works for the base stack.
- **Python 3** — the MCP server and helpers.
- **Go** (matching `go.mod`) — only for the legacy source-based
  `migration-gate.sh`; the image-driven `oci-migrate-gate.sh` needs no Go.

---

## 4. One-time setup

macOS/Homebrew Python is "externally managed" (PEP 668) and may not expose a
`pip` command, so use a virtualenv via `python3 -m`:

```bash
# from the repo root (vsa-control-plane/)
python3 -m venv scripts/oci-local-dev/mcp/.venv
scripts/oci-local-dev/mcp/.venv/bin/python -m pip install -r scripts/oci-local-dev/mcp/requirements.txt
chmod +x scripts/oci-local-dev/*.sh
```

The MCP server is registered for Cursor in `.cursor/mcp.json`, pointed at the
venv's Python:

```json
{
  "mcpServers": {
    "oci-infra": {
      "command": "${workspaceFolder}/scripts/oci-local-dev/mcp/.venv/bin/python",
      "args": ["${workspaceFolder}/scripts/oci-local-dev/mcp/server.py"]
    }
  }
}
```

Reload Cursor. In the MCP settings you should see **oci-infra** with its tools
enabled.

---

## 5. Using it via the Agent

Ask the `oci-local-dev` agent in natural language; it calls the MCP tools for
you. Example prompts:

- “Pin release from `my-release.json` and run the migration gate against the
  prod-like snapshot.”
- “Bring up the OCI stack with CRUD services and run the pool + SVM smoke.”
- “Show me the vcp-worker logs.”
- “Tear down and wipe the volumes.”

The agent maps these to `set_release`, `run_migration_gate`, `bringup`,
`crud_smoke`, `get_logs`, `teardown`.

### MCP tool reference

| Tool | What it does |
|------|--------------|
| `set_release(manifest_path)` | Pin the exact image set into `.oci-release.env` |
| `show_release()` / `verify_release()` | Show the pin / check images are reachable |
| `run_migration_gate(image="", snapshot_path="", fresh=True)` | **Phase 0** — run an OCI release image as a migrator |
| `run_migration_upgrade(from_image, to_image)` | **Phase 0** — old→new release upgrade migration |
| `make_snapshot_from_release(ref, out=...)` | Build a prod-like schema snapshot (git-ref alternative) |
| `bringup(crud=False)` / `teardown(crud=True, wipe=False)` / `status()` | **Phase 1** |
| `get_logs(service, tail=200)` | Tail a service's logs |
| `crud_smoke(timeout_seconds=90, keep=False)` | **Phase 2** |

---

## 6. Using it by hand (CLI)

```bash
cd scripts/oci-local-dev

# 1. Pin the EXACT release shared to OCI
cp release.example.json my-release.json
$EDITOR my-release.json          # set oci-proxy / vcp-worker / vlm-worker tags
./release.sh apply my-release.json
./release.sh verify              # optional: confirm images pull

# 0. Migration gate — image-driven (the oci-proxy image is the OCI migrator)
./oci-migrate-gate.sh --fresh                       # pinned oci-proxy image
./oci-migrate-gate.sh \
    --from-image iad.ocir.io/idqogasfjw45/oci-proxy:<old> \
    --to-image   iad.ocir.io/idqogasfjw45/oci-proxy:<new>   # real upgrade path

# 1. Install + bring up the release stack (+crud services for Phase 2)
./stack.sh up --crud

# 2. Pool + SVM CRUD smoke
./crud-smoke.sh

# teardown
./stack.sh down --crud --wipe
```

The release manifest (`release.example.json`) shape:

```json
{
  "release": "R9.19.1X-8138587",
  "ontapVersion": "9.19.1X29",
  "images": {
    "ociProxy": "iad.ocir.io/idqogasfjw45/oci-proxy:<tag>",
    "vcpWorker": "iad.ocir.io/idqogasfjw45/vcp-worker:<tag>",
    "vlmWorker": "iad.ocir.io/idqogasfjw45/vlm-worker:R9.19.1X-8138587"
  }
}
```

---

## 7. The three regressions in detail

### Phase 0 — Database migration gate (`oci-migrate-gate.sh`)

**OCI migration model:** OCI has no `vcp-db-migrate` image. The `oci-proxy`
image migrates on start (`RUN_MIGRATION_ON_START=true` → `db.Migrate()`, before
Temporal in `oci-proxy/app.go` `main()`). So this gate runs the **actual release
image** as a one-shot migrator against a local Postgres and:

1. fails on any migration error (classified from the image's migrate logs);
2. detects a **dirty** `schema_migrations` / `schema_migrations_post` row and
   maps the dirty version back to the likely file (e.g.
   `database/vcp/migrations/post/0040_vpg_is_shared_to_allocation_type.up.sql`).

Two modes:

- **single** — `--image` (default: pinned `OCI_PROXY_IMAGE`), optionally on a
  `--snapshot` prod-like schema.
- **upgrade** — `--from-image` (old release) then `--to-image` (new release):
  brings the schema to the old release, then applies the new release's
  migrations on top — the exact migration OCI runs on upgrade.

It shares the Postgres container's network namespace and points
`TEMPORAL_ADDRESS` at a dead port so the proxy exits right after migrating;
`LOCAL_REGION=us-east1` keeps `LoadConfig` happy (region is irrelevant to SQL).

> **Critical:** for single mode, feed it a **prod-like snapshot** (or use upgrade
> mode). The `42P16` failure behind the dirty-40 incident only reproduces against
> a drifted schema; a fresh empty DB passes and hides the bug.
>
> `migration-gate.sh` (the `tools/migrate` `go run` path, with an up→down→up
> idempotency cycle) remains for fast source-level pre-commit checks.

### Phase 1 — Install the pinned release (`release.sh` + `stack.sh`)

Installs the **exact images** shared to OCI (not `:latest`) onto the base stack
(postgres + temporal + vlm-worker), and with `--crud` also starts `oci-proxy`,
`vcp-worker`, and the mock `ontap-sim`. Validates the release pulls, starts, and
passes `RUN_MIGRATION_ON_START`.

### Phase 2 — Pool & SVM CRUD smoke (`crud-smoke.sh`)

Drives the release `oci-proxy` API — `POST /v1beta/pools`,
`POST /v1beta/pools/{poolOCID}/svms`, polling `GET /v1beta/workRequests/{id}`,
then best-effort delete — and classifies the result.

---

## 8. Prod-like schema snapshots

```bash
# Option A — schema-only dump from a sanitized replica (never prod write traffic)
./schema-snapshot.sh from-db --dsn 'postgresql://reader@replica:5432/vcp' \
    --out snapshots/vcp-prodlike.sql

# Option B — replay the already-deployed release's migration chain, so the gate
# applies only the NEW migrations on top (the real upgrade path)
./schema-snapshot.sh from-release --ref origin/release-9.19 \
    --out snapshots/vcp-prodlike.sql
```

Snapshots are schema-only (no row data); review before committing. A committed
`snapshots/vcp-prodlike.sql` is picked up automatically by CI.

---

## 9. Interpreting results

| Result | Meaning | Action |
|--------|---------|--------|
| **Phase 0 PASS** | Migrations clean, idempotent, no dirty version | Safe to proceed |
| **Phase 0 FAIL** | A migration errored or left a dirty version (file is named) | Fix the migration SQL; do **not** build |
| **CRUD PASS** | Workflow succeeded | Control + data plane healthy |
| **CRUD PARTIAL** | API accepted + workflow dispatched/ran, then stopped at an external dependency (real OCI compute / per-pool ONTAP IP) | Expected locally — control plane is healthy |
| **CRUD FAIL** | Control-plane defect: bad API contract, workflow didn't dispatch, missing workflow/activity registration, deterministic panic, DB constraint error | Fix before build |

> **Honest boundary:** on a laptop, full pool creation cannot complete because it
> provisions real OCI compute and talks to a per-pool ONTAP cluster IP that only
> exists after provisioning. That is why Phase 2 reports `PARTIAL` rather than
> faking success. `ontap_sim.py` is the seam toward fuller e2e.

---

## 10. Running the same regression against OKE (real e2e)

The approach generalizes from your Mac to an OKE cluster reachable via
`kubectl`. On OKE you get **real OCI compute + real ONTAP**, so the `PARTIAL`
boundary disappears and pool/SVM CRUD can be validated end-to-end.

| Concern | Local | OKE (kubeconfig on your Mac) |
|---|---|---|
| Pin the release | `release.sh` → `.oci-release.env` | Helm values: `vcp-worker-chart` `versionsSupported[].name`, `oci-proxy` `taskQueueVersion`, `vlm-worker` `ontapVersionVlmImageMappings[].vlmImageTag` |
| Install | `docker compose up` | `helm upgrade --install` (see `kubernetes/OCI/INSTALL.md`) |
| Migrations | `tools/migrate` vs PG container | `RUN_MIGRATION_ON_START` / migrate Job vs the OCI Postgres |
| Reach the API | `curl localhost:8080` | `kubectl port-forward svc/oci-proxy 8080:80`, then same flow |
| Pool/SVM CRUD | PARTIAL (external deps faked) | **Full PASS/FAIL** — real provisioning |

> **Caveats for OKE:** it costs real cloud resources + quota and takes minutes;
> the smoke **must clean up** (delete pool/SVM); never target prod — use a
> dedicated test compartment/tenancy and a test/cloned DB. Position local as the
> cheap pre-build gate and OKE as the integration regression.

*(A `--target oke` mode for the scripts is a planned enhancement.)*

---

## 11. Running Phase 0 (CLI / MCP)

Run the gate on demand — there is no dedicated CI workflow. The CLI
(`scripts/oci-local-dev/migration-gate.sh --fresh [--snapshot ...]`) and the MCP
tools auto-start a standalone Postgres and run the gate. Commit a vetted
`snapshots/vcp-prodlike.sql` so the gate reproduces prod drift.

The existing `migration-validation.yml` CI check is **distinct** — it only checks
that up/down migration files exist as pairs and never runs the SQL.

---

## 12. Troubleshooting

| Symptom | Likely cause | Fix |
|---|---|---|
| `the 'mcp' package is required` | MCP SDK not installed | `pip install -r scripts/oci-local-dev/mcp/requirements.txt` |
| `set release first via ... release.sh apply` | No release pinned before `--crud` bringup | Run `release.sh apply <manifest.json>` |
| Phase 0 reports a dirty version | A migration failed mid-run | Open the named `*.up.sql`; fix the failing statement (e.g. drop+recreate the view instead of `CREATE OR REPLACE`) |
| Phase 0 passes locally but prod fails | No prod-like snapshot used | Generate and pass `--snapshot` (see §8) |
| CRUD smoke: `oci-proxy not healthy` | Stack not up with `--crud` | `stack.sh up --crud`, then `stack.sh logs oci-proxy` |
| Image pull `denied` | Not logged into OCIR | `docker login iad.ocir.io` |
| `Docker not found` | Docker Desktop not installed/running | `scripts/oci-local-dev/container-engine.sh ensure` |

For the broader local-dev environment (vlm-worker, Temporal, Apple-Silicon
emulation, etc.) see `doc/guides/local-oci-dev-setup.md` and the
`oci-local-dev` agent playbook.

---

## 13. FAQ

**Does the MCP contain the logic?** No — it is a thin wrapper over the scripts so
hand/CI/agent runs are identical.

**Will Phase 2 ever go fully green locally?** Not without faking OCI compute +
ONTAP. Run it on OKE for true e2e.

**Is it safe to run Phase 0 against prod?** No. Use a clone or a dedicated test
DB; `migrate` is destructive.

**What single thing prevents another dirty-40?** Phase 0 with a committed
prod-like snapshot, wired into the PR gate.
