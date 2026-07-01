# Local OCI dev setup — running oci-proxy and vcp-worker from source in Cursor

This guide explains how to run the `oci-proxy` and `vcp-worker` Go services
directly from source (via Cursor's debugger or `go run`) against a fully
local Postgres + Temporal + **`vlm-worker`** stack running on **Docker**
(Docker Desktop on macOS). The `docker-compose.local.yml` is driven through a
small committed helper (`scripts/oci-local-dev/container-engine.sh`) that
resolves Docker — even when Docker Desktop's CLI isn't symlinked onto PATH —
and drives `compose`/`exec` for you. See §2.1.

`vlm-worker` runs as a prebuilt container (not from source — the binary is
proprietary, distributed through the OCI Container Registry) and connects
to the same local Temporal as the from-source services, polling the
`vsa-lifecycle-manager-<ontapVersion>` task queue.

It also documents which files in this setup are committable vs. local-only.

## TL;DR

The fastest path is to let the Cursor subagent drive the whole setup.
Everything below is the same procedure expanded for reference.

### Option A — Automated, recommended (Cursor slash command)

1. **Open Cursor in Agent mode** (model picker → make sure you're not in
   Ask). The workspace folder you open must be the *parent* of
   `vsa-control-plane/` (the directory that will eventually hold
   `.vscode/launch.json` and `docker-compose.local.yml`).
2. In the chat input, type a forward slash and pick
   `/setup-oci-local`. The picker resolves it from
   [`.cursor/skills/setup-oci-local/SKILL.md`](../../.cursor/skills/setup-oci-local/SKILL.md)
   (the `name:` frontmatter makes it discoverable) and the agent at
   [`.cursor/agents/oci-local-dev.mdc`](../../.cursor/agents/oci-local-dev.mdc)
   takes over. Both natural-language phrases ("setup oci local dev",
   "bring up oci local", "start oci dev environment") and the
   [committable command](../../.cursor/commands/setup-oci-local.md) work
   too — they all dispatch the same agent.
3. **Answer the agent's prompts.** It uses the `AskQuestion` tool (so
   answers are hidden behind a structured form) for any `REPLACE_ME`
   placeholder, then writes them straight into the local `*.env` files
   without echoing them back. It will ask for:
   - `OCI_ONTAP_ADMIN_PASSWORD` (any non-empty string for local dev).
   - `ONTAP_CREDENTIAL_ENCRYPT_KEY` — exactly **32 bytes**, written
     byte-identically to both `worker/oci.dev.env` and
     `kubernetes/OCI/vlm-worker/vlm-worker.dev.env`. Generate one with
     `openssl rand -base64 24 | head -c 32` if you don't already have one.
   - `OCI_TENANCY`, `OCI_USER`, `OCI_FINGERPRINT`, and optional
     `OCI_PASSPHRASE` (only if your `vlm-worker.oci.key.pem` is
     passphrase-encrypted). Copy these from your `~/.oci/config` DEFAULT
     profile.
4. **Place the OCI API private-key PEM.** The agent will instruct you to
   drop your private key at
   `vsa-control-plane/kubernetes/OCI/vlm-worker/vlm-worker.oci.key.pem`
   (already gitignored). It will NOT auto-generate or fetch one.
5. **Let the agent start the stack.** It first runs
   `scripts/oci-local-dev/container-engine.sh ensure` (which verifies Docker
   Desktop's daemon is reachable), then
   `export OCI_PRIVATE_KEY="$(cat .../vlm-worker.oci.key.pem)"` in the
   shell, then `container-engine.sh compose up -d`, waits for healthchecks,
   and confirms `vlm-worker` is polling the
   `vsa-lifecycle-manager-<ontapVersion>` task queue.
6. **Launch the Go services.** When the agent prints
   `All checks passed.`, open Run & Debug (`Cmd-Shift-D`) and launch
   one of the compounds it created in `.vscode/launch.json`:
   - `OCI stack: proxy + customer worker` — `oci-proxy` + customer
     `vcp-worker` + a live `vlm-worker (logs)` terminal.
   - `OCI stack: proxy + customer + background workers` — adds the
     background worker (its `METRICS_PORT` is auto-set to `9091`).

What the agent **creates / writes** vs. what it leaves alone:

| Touched | How | Why |
|---|---|---|
| `<workspace-root>/.vscode/launch.json` | Created via the `Write` tool if missing | Three Go debug configs (`oci-proxy`, `vcp-worker customer`, `vcp-worker background`) plus a `vlm-worker (logs)` `node-terminal` and two compounds. Outside the repo, gitignored. |
| `<workspace-root>/docker-compose.local.yml` | Created if missing | Postgres 16, Temporal 1.27.2 (auto-setup), Temporal UI, prebuilt vlm-worker. Pinned to the image tag from `kubernetes/OCI/vlm-worker/overrides_eng_cp.yaml`. |
| `<workspace-root>/local-dev/postgres-init/01-create-databases.sql` | Created if missing | Creates `vcp`, `temporal`, `temporal_visibility` on Postgres first boot. |
| `vsa-control-plane/oci-proxy/oci.dev.env` | `cp -n` from `.tmpl` | Local-only env for oci-proxy. The agent edits in place for any `REPLACE_ME` you supply. |
| `vsa-control-plane/worker/oci.dev.env` | `cp -n` from `.tmpl` | Local-only env for vcp-worker. Same edit-in-place flow. |
| `vsa-control-plane/kubernetes/OCI/vlm-worker/vlm-worker.dev.env` | `cp -n` from `.tmpl` | Local-only env for the vlm-worker container. Same edit-in-place flow. |
| **Anything ending in `.dev.env.tmpl`** | **Never edited at runtime** | They are the committable source of truth. Update them via a code change + PR, never via the agent. |
| `vsa-control-plane/kubernetes/OCI/vlm-worker/vlm-worker.oci.key.pem` | Never created or read | The agent only checks `test -f`. You provision it by hand. `*.pem` is gitignored. |

The agent will **not** run any destructive command (`container-engine.sh
compose down -v`, `docker volume rm`, deleting env files) without
explicit confirmation. It will also never echo back any value you typed into
an `AskQuestion` form — it confirms with "saved" / "updated" and moves on.

### Option B — Manual (no agent)

If you'd rather not use the agent, the steps from §6 (Reproducing the
setup from scratch) are the canonical manual flow. The TL;DR version:

```bash
# 1. From the workspace parent (the dir that contains vsa-control-plane/):
#    Create .vscode/launch.json, docker-compose.local.yml, and
#    local-dev/postgres-init/01-create-databases.sql by copy-pasting
#    from §3 of this guide.

# 2. Inside vsa-control-plane/, materialise your local env files:
cp oci-proxy/oci.dev.env.tmpl                        oci-proxy/oci.dev.env
cp worker/oci.dev.env.tmpl                           worker/oci.dev.env
cp kubernetes/OCI/vlm-worker/vlm-worker.dev.env.tmpl kubernetes/OCI/vlm-worker/vlm-worker.dev.env

# 3. Edit the three oci.dev.env / vlm-worker.dev.env files, replace
#    REPLACE_ME values. Set ONTAP_CREDENTIAL_ENCRYPT_KEY to the SAME
#    32-byte string in both worker/oci.dev.env and
#    kubernetes/OCI/vlm-worker/vlm-worker.dev.env.

# 4. Drop your OCI API private key at
#    vsa-control-plane/kubernetes/OCI/vlm-worker/vlm-worker.oci.key.pem.

# 5. From the workspace parent again, ensure Docker and start the infra.
#    `ensure` verifies Docker Desktop's daemon is reachable.
vsa-control-plane/scripts/oci-local-dev/container-engine.sh ensure
export OCI_PRIVATE_KEY="$(cat vsa-control-plane/kubernetes/OCI/vlm-worker/vlm-worker.oci.key.pem)"
vsa-control-plane/scripts/oci-local-dev/container-engine.sh compose up -d

# 6. In Cursor, open Run & Debug and pick:
#    "OCI stack: proxy + customer worker"
```

## 1. Architecture of the local setup

```
┌─────────────────────── Cursor workspace root ───────────────────────┐
│ /Users/<you>/.../scheduler_fix/                                     │
│                                                                     │
│   .vscode/launch.json            Cursor debug configs (Go + Delve)  │
│   docker-compose.local.yml       Postgres + Temporal + Temporal UI  │
│                                  + vlm-worker (prebuilt container)  │
│   local-dev/                                                        │
│     postgres-init/                                                  │
│       01-create-databases.sql    Creates vcp, temporal,             │
│                                  temporal_visibility DBs            │
│                                                                     │
│   vsa-control-plane/             ← the git repo                     │
│     oci-proxy/                                                      │
│       app.go                     entry point                        │
│       oci.dev.env.tmpl        committed template                 │
│       oci.dev.env                local, gitignored (you create it)  │
│     worker/                                                         │
│       main.go                    entry point                        │
│       oci.dev.env.tmpl        committed template                 │
│       oci.dev.env                local, gitignored (you create it)  │
│     kubernetes/OCI/vlm-worker/                                      │
│       vlm-worker.dev.env.tmpl  committed template                │
│       vlm-worker.dev.env          local, gitignored (you create it) │
│       vlm-worker.oci.key.pem      local, gitignored (your OCI key)  │
└─────────────────────────────────────────────────────────────────────┘
```

Both services read all configuration from environment variables. The
`oci.dev.env` files mirror the rendered `ConfigMap` of the OCI Helm charts
(`kubernetes/OCI/oci-proxy/templates/configMap.yaml` and
`kubernetes/OCI/vcp-worker-chart/templates/configMap.yaml`) plus the keys that
in-cluster come from the `vcp-db-secret` Kubernetes `Secret`.

Cursor's debugger (`type: go`, `mode: debug`) compiles each package with
debug symbols, runs it under Delve, and pipes stdout/stderr to a dedicated
Debug Console.

## 2. Prerequisites

- **Docker Desktop** (or Colima / Rancher Desktop) with `docker compose` v2.
  The helper in §2.1 auto-finds Docker Desktop's CLI even if the installer
  didn't symlink `docker` onto your PATH.
- Go 1.21+ (`go version`).
- Cursor with the Go extension installed (auto-prompts the first time you
  start a `type: go` debug session). Delve (`dlv`) is installed automatically
  by the extension, or manually via `go install github.com/go-delve/delve/cmd/dlv@latest`.

### 2.1 Container engine (Docker)

The stack runs on **Docker** using `docker-compose.local.yml`. A committed
helper resolves Docker and drives `compose`/`exec`, so the `/setup-oci-local`
flow works the same by hand, in CI, or via the agent:

```
vsa-control-plane/scripts/oci-local-dev/container-engine.sh
```

| Subcommand | Effect |
|---|---|
| `ensure` | Verify Docker is usable. If Docker Desktop's daemon is reachable it does nothing; if the CLI is present but the daemon is down it tells you to start Docker Desktop. Idempotent, safe to re-run. |
| `detect` | Print the resolved `ENGINE` / `COMPOSE` / `COMPOSE_FILE`. Read-only. |
| `engine` | Print `docker`. |
| `compose [args]` | Run `docker compose -f docker-compose.local.yml [args]`. |
| `exec [args]` | Run `docker exec [args]`. |

The helper is **docker-first** and auto-adds Docker Desktop's CLI to PATH if
the installer didn't symlink it into `/usr/local/bin`:

```bash
vsa-control-plane/scripts/oci-local-dev/container-engine.sh ensure
vsa-control-plane/scripts/oci-local-dev/container-engine.sh detect
# expect: ENGINE=docker   COMPOSE=docker compose   COMPOSE_FILE=...
```

### 2.2 First-time Docker setup on macOS

1. **Install Docker Desktop** (if you haven't already): download from
   docker.com, or `brew install --cask docker`.
2. **Start it and wait until it's running:**

   ```bash
   open -a Docker          # then wait for the whale icon to settle
   docker info >/dev/null 2>&1 && echo "docker OK" || echo "docker NOT reachable"
   ```

3. **If `docker` isn't found on PATH** (Docker Desktop sometimes only
   symlinks `docker-compose`), either symlink the CLI yourself or just rely on
   the helper, which finds it under
   `/Applications/Docker.app/Contents/Resources/bin`:

   ```bash
   ln -s /Applications/Docker.app/Contents/Resources/bin/docker /opt/homebrew/bin/docker
   ```

4. **Confirm the helper resolves Docker + compose:**

   ```bash
   vsa-control-plane/scripts/oci-local-dev/container-engine.sh detect
   # expect: ENGINE=docker   COMPOSE=docker compose   COMPOSE_FILE=...
   ```

### 2.3 Apple Silicon notes (vlm-worker)

The same `docker-compose.local.yml` runs unchanged — Postgres, Temporal,
Temporal UI, and the prebuilt **`vlm-worker`** container all work on Docker
Desktop. Two vlm-worker-specific things to know on an Apple Silicon Mac:

- **Registry auth.** `vlm-worker` is pulled from OCIR
  (`iad.ocir.io/idqogasfjw45/vlm-worker:…`). Run `docker login iad.ocir.io`
  with an OCI auth token first, or switch to the GHCR fallback image
  documented in §3.2 (`image:` comment) which needs no OCIR auth.
- **Architecture / emulation.** The published `vlm-worker` image is
  `linux/amd64`. Docker Desktop emulates amd64 (Rosetta by default — keep
  **Settings → General → "Use Rosetta for x86/amd64 emulation"** enabled), and
  the `vlm-worker` service pins `platform: linux/amd64`. The image must be a
  **Go ≤1.24 or ≥1.25.4** build — Go 1.25.0–1.25.3 amd64 binaries crash under
  all emulation ([golang/go#75721](https://github.com/golang/go/issues/75721));
  use a known-good tag such as `R9.19.1X-8138587`. (Intel Macs / amd64 Linux
  are native — nothing to do.)

## 3. What's in the workspace root (outside the git repo)

The three files below live **above** `vsa-control-plane/` (i.e. in the
workspace folder Cursor opens). They are NOT part of the git repo and are not
shared via `git push`. Copy the contents from this doc when reproducing the
setup on a new machine.

### 3.1 `.vscode/launch.json`

Defines three individual configs and two compound configs. The compounds let
you start the whole OCI stack with one click.

```json
{
  "version": "0.2.0",
  "configurations": [
    {
      "name": "oci-proxy (debug)",
      "type": "go",
      "request": "launch",
      "mode": "debug",
      "program": "${workspaceFolder}/vsa-control-plane/oci-proxy",
      "cwd": "${workspaceFolder}/vsa-control-plane",
      "envFile": "${workspaceFolder}/vsa-control-plane/oci-proxy/oci.dev.env",
      "showLog": true,
      "internalConsoleOptions": "openOnSessionStart"
    },
    {
      "name": "vcp-worker (customer, debug)",
      "type": "go",
      "request": "launch",
      "mode": "debug",
      "program": "${workspaceFolder}/vsa-control-plane/worker",
      "cwd": "${workspaceFolder}/vsa-control-plane",
      "envFile": "${workspaceFolder}/vsa-control-plane/worker/oci.dev.env",
      "env": {
        "WORKER_TASK_QUEUE": "customer-workflows",
        "VLM_CONFIG_FILE_PATH": "${workspaceFolder}/vsa-control-plane/common/vsa_config/vlm-config-oci.json",
        "VMRS_CONFIG_PATH": "${workspaceFolder}/vsa-control-plane/config/vmrs_oci.yaml"
      },
      "showLog": true,
      "internalConsoleOptions": "openOnSessionStart"
    },
    {
      "name": "vcp-worker (background, debug)",
      "type": "go",
      "request": "launch",
      "mode": "debug",
      "program": "${workspaceFolder}/vsa-control-plane/worker",
      "cwd": "${workspaceFolder}/vsa-control-plane",
      "envFile": "${workspaceFolder}/vsa-control-plane/worker/oci.dev.env",
      "env": {
        "WORKER_TASK_QUEUE": "background-workflows",
        "METRICS_PORT": "9091",
        "RUN_MIGRATION_ON_START": "false",
        "VLM_CONFIG_FILE_PATH": "${workspaceFolder}/vsa-control-plane/common/vsa_config/vlm-config-oci.json",
        "VMRS_CONFIG_PATH": "${workspaceFolder}/vsa-control-plane/config/vmrs_oci.yaml"
      },
      "showLog": true,
      "internalConsoleOptions": "openOnSessionStart"
    },
    {
      "name": "vlm-worker (logs)",
      "type": "node-terminal",
      "request": "launch",
      "cwd": "${workspaceFolder}",
      "command": "${workspaceFolder}/vsa-control-plane/scripts/oci-local-dev/container-engine.sh compose logs -f vlm-worker"
    }
  ],
  "compounds": [
    {
      "name": "OCI stack: proxy + customer worker",
      "configurations": [
        "oci-proxy (debug)",
        "vcp-worker (customer, debug)",
        "vlm-worker (logs)"
      ],
      "stopAll": true
    },
    {
      "name": "OCI stack: proxy + customer + background workers",
      "configurations": [
        "oci-proxy (debug)",
        "vcp-worker (customer, debug)",
        "vcp-worker (background, debug)",
        "vlm-worker (logs)"
      ],
      "stopAll": true
    }
  ]
}
```

The `vlm-worker (logs)` config uses VSCode/Cursor's built-in
`node-terminal` launch type, which runs an arbitrary shell command and
registers it as a debug session (visible in the Call Stack panel
alongside the Go sessions). It calls
`container-engine.sh compose logs -f vlm-worker` so it tails the logs on
the Docker stack. Output streams to
the Integrated Terminal under a tab labeled `vlm-worker (logs)`.
`stopAll: true` on the compounds means stopping the compound (red square)
also stops the `compose logs -f` tail — no orphan terminals.

### 3.2 `docker-compose.local.yml`

Brings up Postgres 16, Temporal (auto-setup image — runs its own schema
migrations against the `temporal` execution DB and the
`temporal_visibility` DB), the Temporal Web UI, and the prebuilt
`vlm-worker` container.

Two things in this file are easy to get wrong; both burned us during
initial setup and are now documented inline:

1. **Temporal 1.27.x needs a separate visibility DB.** If `DBNAME` and
   `VISIBILITY_DBNAME` point at the same database, the auto-setup
   container skips the visibility schema and history/matching crash on
   `pq: relation "executions_visibility" does not exist`.
2. **The auto-setup image binds the gRPC frontend to the container's
   primary IP, not loopback.** A healthcheck against `127.0.0.1:7233`
   will refuse from inside the container even though the host can reach
   `localhost:7233`. Use `$$(hostname):7233` instead.

```yaml
services:
  postgres:
    image: postgres:16-alpine
    container_name: vcp-postgres
    restart: unless-stopped
    environment:
      POSTGRES_USER: postgres
      POSTGRES_PASSWORD: postgres
      POSTGRES_DB: postgres
    ports:
      - "5432:5432"
    volumes:
      - ./local-dev/postgres-init:/docker-entrypoint-initdb.d:ro
      - vcp-pg-data:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U postgres -d postgres"]
      interval: 5s
      timeout: 5s
      retries: 20

  temporal:
    image: temporalio/auto-setup:1.27.2
    container_name: vcp-temporal
    restart: unless-stopped
    depends_on:
      postgres:
        condition: service_healthy
    environment:
      DB: postgres12
      DB_PORT: 5432
      POSTGRES_USER: postgres
      POSTGRES_PWD: postgres
      POSTGRES_SEEDS: postgres
      DBNAME: temporal
      VISIBILITY_DBNAME: temporal_visibility   # must differ from DBNAME on 1.27.x
      ENABLE_ES: "false"
      SKIP_DEFAULT_NAMESPACE_CREATION: "false"
      DEFAULT_NAMESPACE: default
      DEFAULT_NAMESPACE_RETENTION: 72h
    ports:
      - "7233:7233"
      - "7234:7234"
      - "7235:7235"
      - "7239:7239"
    healthcheck:
      # Frontend binds to the container's primary IP, not 127.0.0.1.
      test: ["CMD-SHELL", "temporal operator cluster health --address $$(hostname):7233 | grep -q SERVING"]
      interval: 10s
      timeout: 5s
      retries: 30
      start_period: 30s

  temporal-ui:
    image: temporalio/ui:2.36.0
    container_name: vcp-temporal-ui
    restart: unless-stopped
    depends_on:
      - temporal
    environment:
      TEMPORAL_ADDRESS: temporal:7233
      TEMPORAL_CORS_ORIGINS: http://localhost:3000
    ports:
      - "8088:8080"

  # Prebuilt OCI vlm-worker container. Image tag is pinned to the OCI
  # override in kubernetes/OCI/vlm-worker/overrides_eng_cp.yaml.
  #
  # GHCR fallback (no OCIR auth required):
  #   image: ghcr.io/vcp-vsa-control-plane/vcp-container-images-us/vlm-worker:R9.18.1x_8083481
  #
  # OCI_PRIVATE_KEY must be exported in the calling shell before
  # `compose up` (multi-line PEM cannot live in env_file):
  #   export OCI_PRIVATE_KEY="$(cat vsa-control-plane/kubernetes/OCI/vlm-worker/vlm-worker.oci.key.pem)"
  vlm-worker:
    image: iad.ocir.io/idqogasfjw45/vlm-worker:R9.19.1Sx_8084596_260430
    container_name: vcp-vlm-worker
    restart: unless-stopped
    depends_on:
      temporal:
        condition: service_healthy
    env_file:
      - ./vsa-control-plane/kubernetes/OCI/vlm-worker/vlm-worker.dev.env
    environment:
      SERVER_IP: temporal   # docker-compose service name, not localhost
      OCI_PRIVATE_KEY: "${OCI_PRIVATE_KEY:?missing - see vlm-worker service header in docker-compose.local.yml}"

volumes:
  vcp-pg-data:
```

### 3.3 `local-dev/postgres-init/01-create-databases.sql`

Mounted into the Postgres container at `/docker-entrypoint-initdb.d/` and
executed once on first boot.

```sql
CREATE DATABASE vcp;                  -- used by oci-proxy + vcp-worker
CREATE DATABASE temporal;             -- Temporal executions/history schema
CREATE DATABASE temporal_visibility;  -- Temporal SQL visibility schema (1.27.x)
```

The Postgres image only runs `/docker-entrypoint-initdb.d/*.sql` on
**first volume init**. If you edit this file later, you must
`container-engine.sh compose down -v && compose up -d` to recreate the volume
and re-run the init script (destructive — wipes all local databases).

### 3.4 OCI API private key PEM

`vlm-worker` uses `FILE_BASED_AUTH=true` (raw OCI config provider). The
non-key OCI identifiers (`OCI_TENANCY`, `OCI_USER`, `OCI_FINGERPRINT`,
`OCI_REGION`, optional `OCI_PASSPHRASE`) live in `vlm-worker.dev.env`.
The private-key contents are injected at `compose up` time from a
shell variable populated from the user's PEM file:

```bash
export OCI_PRIVATE_KEY="$(cat vsa-control-plane/kubernetes/OCI/vlm-worker/vlm-worker.oci.key.pem)"
```

The PEM file lives next to `vlm-worker.dev.env` and is gitignored via
the `*.pem` rule in `vsa-control-plane/.gitignore`. Never commit it.

Why not bake the key into the env file? Docker `env_file` entries cannot
span newlines, and an OCI API private key is a multi-line PKCS#8 PEM.

Why not bake it into `docker-compose.local.yml`? The compose file would
become a secret-bearing file, defeating the gitignore boundary.

The compose `${OCI_PRIVATE_KEY:?...}` guard makes `compose up`
fail loudly if you forget to export the variable.

## 4. What's in the git repo (committable)

These files live inside `vsa-control-plane/` and SHOULD be committed.

| Path | Purpose |
|---|---|
| `oci-proxy/oci.dev.env.tmpl` | Template env for oci-proxy. Defaults to the local Docker stack. `OCI_ONTAP_ADMIN_PASSWORD=REPLACE_ME`. |
| `worker/oci.dev.env.tmpl` | Template env for vcp-worker. Defaults to the local Docker stack. Contains both `VSA_VLM_ENCRYPTION_KEY` (Helm-legacy, unused by Go) and `ONTAP_CREDENTIAL_ENCRYPT_KEY` (read by `clients/vlm/credential_helper.go:17`). Both ship as `REPLACE_ME_32_BYTE_KEY___________` and should be set to the same 32-byte string. Also has `OCI_VSA_EXT_IP_FOR_NODE_MGMT=false` (toggle to `true` if you need a public IP on each node's mgmt LIF) and `OCI_ONTAP_ADMIN_PASSWORD=REPLACE_ME`. |
| `kubernetes/OCI/vlm-worker/vlm-worker.dev.env.tmpl` | Template env for the prebuilt vlm-worker container. Defaults to the local Docker stack. Contains `ONTAP_CREDENTIAL_ENCRYPT_KEY` (must be byte-identical to the worker's value) plus OCI identifiers. |
| `.cursor/agents/oci-local-dev.mdc` | Cursor subagent that automates this whole guide. |
| `.cursor/commands/setup-oci-local.md` | Cursor slash command (`/setup-oci-local`) that invokes the subagent. |
| `.cursor/skills/setup-oci-local/SKILL.md` | Cursor skill — what makes `/setup-oci-local` show in the `/` picker. |
| `.gitignore` | Now includes `*.pem` / `*.key` rules so OCI API keys can't be accidentally committed. |
| `doc/guides/local-oci-dev-setup.md` | This document. |

Each `.dev.env.tmpl` file is a flat-key derivation of the rendered ConfigMap
defined by the Helm chart at `kubernetes/OCI/<chart>/templates/configMap.yaml`,
with values pulled from `values.yaml` and overridden by
`overrides_eng_cp.yaml`. Keys sourced from `vcp-db-secret` (i.e. `DB_USER`,
`DB_PASSWORD`, `DB_ADMIN_USER`, `DB_ADMIN_PASSWORD`) are added explicitly.

## 5. What's NOT committable

| File | Reason |
|---|---|
| `vsa-control-plane/oci-proxy/oci.dev.env` | Matches `*.env` in `.gitignore`. Holds your real secret values. |
| `vsa-control-plane/worker/oci.dev.env` | Same. |
| `vsa-control-plane/kubernetes/OCI/vlm-worker/vlm-worker.dev.env` | Same. |
| `vsa-control-plane/kubernetes/OCI/vlm-worker/vlm-worker.oci.key.pem` | Matches `*.pem` in `.gitignore`. Your OCI API private key. |
| `.vscode/launch.json` (workspace root) | `.vscode/` is in `.gitignore`. Lives outside the repo anyway. |
| `docker-compose.local.yml` (workspace root) | Lives outside the repo. |
| `local-dev/postgres-init/*.sql` (workspace root) | Lives outside the repo. |

`.gitignore` excerpt (the relevant lines):

```17:28:vsa-control-plane/.gitignore
*.env
!skaffold.env
.run/

# Private key material (e.g. kubernetes/OCI/vlm-worker/vlm-worker.oci.key.pem,
# any per-developer OCI API signing keys). Never commit private keys.
*.pem
*.key
!**/testdata/**/*.pem
!**/testdata/**/*.key
```

## 6. Reproducing the setup from scratch

A new contributor doing the same thing on their machine:

### 6.1 Prepare the workspace folder

```bash
# Pick a parent folder above the cloned repo.
mkdir -p ~/work/vcp-dev && cd ~/work/vcp-dev
git clone <repo-url> vsa-control-plane
```

The folder layout must end up as:

```
~/work/vcp-dev/
├── vsa-control-plane/
└── (the four files below)
```

### 6.2 Copy the four "local infra" files into the workspace root

Create these by copy-pasting from section 3 above:

1. `.vscode/launch.json`
2. `docker-compose.local.yml`
3. `local-dev/postgres-init/01-create-databases.sql`

```bash
mkdir -p .vscode local-dev/postgres-init
# Paste the three files from section 3.
```

### 6.3 Materialise the env files inside the repo

```bash
cd vsa-control-plane
cp oci-proxy/oci.dev.env.tmpl                       oci-proxy/oci.dev.env
cp worker/oci.dev.env.tmpl                          worker/oci.dev.env
cp kubernetes/OCI/vlm-worker/vlm-worker.dev.env.tmpl kubernetes/OCI/vlm-worker/vlm-worker.dev.env
```

Edit each file and replace the `REPLACE_ME` values:

- `oci-proxy/oci.dev.env`:
  - `OCI_ONTAP_ADMIN_PASSWORD` — any non-empty placeholder is fine if you're
    not actually exercising the credential-rotation paths.
- `worker/oci.dev.env`:
  - `ONTAP_CREDENTIAL_ENCRYPT_KEY` — exactly 32 ASCII bytes (AES-256).
    Read by `clients/vlm/credential_helper.go:17` to encrypt the ONTAP
    credentials inside every workflow payload sent to vlm-worker. **Must
    be byte-identical** to the value in
    `kubernetes/OCI/vlm-worker/vlm-worker.dev.env`. Set
    `VSA_VLM_ENCRYPTION_KEY` to the same value for Helm parity — it is
    a ConfigMap leftover, not read by any Go code today.
  - `OCI_ONTAP_ADMIN_PASSWORD` — same as above.
  - `OCI_VSA_EXT_IP_FOR_NODE_MGMT` (optional) — defaults to `false`.
    Toggle to `true` only when you need to SSH to the VSA over the
    public internet. Propagates to
    `DevFlags.ExtIPForNodeMgmt=true` on every
    `CreateVSAClusterDeploymentRequest`.
- `kubernetes/OCI/vlm-worker/vlm-worker.dev.env`:
  - `ONTAP_CREDENTIAL_ENCRYPT_KEY` — **byte-identical 32 bytes** to the
    one above. vcp-worker encrypts; vlm-worker decrypts. A mismatch (or
    one side unset) makes child workflows fail with
    `illegal base64 data at input byte 0` before they run a single
    activity. See §9 "Common gotchas" for the full diagnosis.
  - `OCI_TENANCY`, `OCI_USER`, `OCI_FINGERPRINT` — copy from your
    `~/.oci/config` DEFAULT profile.
  - `OCI_PASSPHRASE` — only set if your API key is passphrase-encrypted.

### 6.4 Provision the OCI API private key

Place your OCI API private-key PEM at:

```
vsa-control-plane/kubernetes/OCI/vlm-worker/vlm-worker.oci.key.pem
```

This file is gitignored via `*.pem`. The fingerprint of this key must
match the `OCI_FINGERPRINT` you set above.

### 6.5 Start the infra

```bash
cd ~/work/vcp-dev
CE=vsa-control-plane/scripts/oci-local-dev/container-engine.sh
# Make sure Docker is reachable (start Docker Desktop if `ensure` complains).
"$CE" ensure
# Inject the multi-line PEM into the shell before `up` (compose's
# ${OCI_PRIVATE_KEY:?...} guard fails the up if unset).
export OCI_PRIVATE_KEY="$(cat vsa-control-plane/kubernetes/OCI/vlm-worker/vlm-worker.oci.key.pem)"
"$CE" compose up -d
"$CE" compose ps   # postgres, temporal, temporal-ui, vlm-worker
```

Verify:

```bash
"$CE" exec vcp-postgres psql -U postgres -c '\l' | grep -E 'vcp|temporal|temporal_visibility'
curl -fsS http://localhost:8088/ >/dev/null && echo "Temporal UI reachable"
"$CE" exec vcp-temporal sh -c \
  'temporal task-queue describe \
     --task-queue vsa-lifecycle-manager-9.18.1 \
     --namespace default \
     --address $(hostname):7233' \
  | grep -E 'workflow|activity'  # vlm-worker should appear as a poller
```

### 6.6 Launch the Go services in Cursor

Open `~/work/vcp-dev` as the workspace folder. `Cmd-Shift-D` →
**OCI stack: proxy + customer worker** → green play arrow.

On first launch Cursor will offer to install Delve if it's not already on
`PATH`. Approve.

`vlm-worker` is already running in the container engine; the launch compounds
bring up just the from-source services (`oci-proxy`, `vcp-worker`) under the
debugger.

## 7. Where to see the logs

| Component | Where logs appear in Cursor | CLI fallback |
|---|---|---|
| `oci-proxy` | Debug Console (`Cmd-Shift-Y`), dropdown → `oci-proxy (debug)` | n/a — runs under Delve |
| `vcp-worker` (customer) | Debug Console, dropdown → `vcp-worker (customer, debug)` | n/a |
| `vcp-worker` (background) | Debug Console, dropdown → `vcp-worker (background, debug)` | n/a |
| `vlm-worker` | Integrated Terminal (`` Ctrl-` ``), tab → `vlm-worker (logs)` | `$CE compose logs -f vlm-worker` |
| `postgres` | n/a | `$CE compose logs -f postgres` |
| `temporal` | n/a | `$CE compose logs -f temporal` |
| `temporal-ui` | n/a | <http://localhost:8088> |
| all four containers | n/a | `$CE compose logs -f` |

where `$CE` is `vsa-control-plane/scripts/oci-local-dev/container-engine.sh`
(the Docker helper).

### How the IDE wiring works

- **Cursor Debug Console** (`Cmd-Shift-Y`): each Go debug session has its
  own entry in the dropdown at the top of the console. Switch between
  `oci-proxy (debug)` and `vcp-worker (customer, debug)` to see each
  process's stdout/stderr independently.
- **Cursor "Call Stack" panel**: shows one collapsible node per active
  debug session (including the `vlm-worker (logs)` node-terminal
  session). Click a session to make it the focus of step/pause for the
  Go sessions; clicking the `vlm-worker (logs)` node reveals its
  Integrated Terminal tab.
- **Cursor Integrated Terminal** (`` Ctrl-` ``): the `vlm-worker (logs)`
  compound entry opens a dedicated terminal tab here that runs
  `container-engine.sh compose logs -f vlm-worker`. It
  dies automatically when you stop the compound (red square).

### Other useful endpoints

- **Temporal Web UI**: <http://localhost:8088> → **Task Queues** tab.
  Confirms workers have connected (`customer-workflows`,
  `background-workflows`, and `vsa-lifecycle-manager-9.18.1` for
  `vlm-worker`).
- **Postgres**: `$CE exec -it vcp-postgres psql -U postgres -d vcp`
  for ad-hoc schema inspection (`$CE` = the container-engine helper).
- **vlm-worker Prometheus metrics** (inside the container):
  `$CE exec vcp-vlm-worker wget -qO- localhost:9100/metrics | head -20`.

## 8. Switching between the local container stack and OCI Postgres

For pointing the same services at the real OCI Postgres (e.g. when bypassing
the local container stack), edit four blocks in each `oci.dev.env`:

```bash
# from
DB_HOST=localhost
DB_SSL_MODE=disable
DB_PASSWORD=postgres
DB_ADMIN_PASSWORD=postgres
TEMPORAL_ADDRESS=localhost:7233

# to
DB_HOST=primary.djzfvolyanswjizl6jz5bkqucnfeha.postgresql.us-ashburn-1.oci.oraclecloud.com
DB_SSL_MODE=require
DB_PASSWORD=<vcp-db-secret DB_PASSWORD>
DB_ADMIN_PASSWORD=<vcp-db-secret DB_ADMIN_PASSWORD>
TEMPORAL_ADDRESS=localhost:7233   # via: kubectl -n temporal port-forward svc/temporal-frontend 7233:7233
```

Your IP must be allow-listed in OCI for the Postgres primary endpoint.

## 9. Common gotchas

- **`RUN_MIGRATION_ON_START` conflict.** Only one process should run schema
  migrations. The templates ship with `oci-proxy/oci.dev.env: RUN_MIGRATION_ON_START=true`
  and `worker/oci.dev.env: RUN_MIGRATION_ON_START=false`. If you reverse them
  or set both to `true`, you'll see migration-lock errors.
- **Background worker port collision.** `METRICS_PORT=9090` collides if both
  `customer` and `background` workers run. The `vcp-worker (background, debug)`
  launch config sets `METRICS_PORT=9091`; the per-config `env` block always
  wins over `envFile`.
- **`temporal` namespace missing.** If the Temporal UI doesn't show the
  `default` namespace, the auto-setup container failed. Tail
  `$CE compose logs temporal` — usually a Postgres connectivity issue
  during the very first boot. Resolve, then `$CE compose down -v && compose
  up -d` to retry the schema bootstrap (`$CE` = the container-engine helper).
- **Temporal stuck in `health: starting` with `pq: relation
  "executions_visibility" does not exist`.** Temporal 1.27.x requires
  `DBNAME` and `VISIBILITY_DBNAME` to be different databases (`temporal`
  vs `temporal_visibility`). Both must exist before Temporal boots, which
  is why `01-create-databases.sql` creates three.
- **Temporal healthcheck never flips to `healthy` even though logs show
  `Temporal server started.`** The auto-setup image binds the gRPC
  frontend to the container's primary IP, not `127.0.0.1`. The healthcheck
  in §3.2 uses `$$(hostname):7233` which resolves to the right interface.
- **`REGION_NUMBER_MAP` must include `LOCAL_REGION`.** `common/config.go`
  `validateRegionMap()` exits the process if `LOCAL_REGION` is not a key in
  the parsed JSON. Both example files ship with `us-ashburn-1` included.
- **OCI-only env vars at worker startup.** `worker/main.go` calls
  `ociworkflows.ValidateOCIWorkerStartupEnv()` for the customer worker on
  OCI; it requires `VSA_IMAGE_NAME`, `VSA_MEDIATOR_IMAGE_NAME`,
  `OCI_ONTAP_ADMIN_PASSWORD`, `LOCAL_REGION`, `SECRET_URI`. The
  `.dev.env.tmpl` files set all of these; only `OCI_ONTAP_ADMIN_PASSWORD` and
  the two encryption-key lines (`VSA_VLM_ENCRYPTION_KEY` +
  `ONTAP_CREDENTIAL_ENCRYPT_KEY`) are marked `REPLACE_ME`.
- **`compose up` aborts with `missing - see vlm-worker service
  header`.** You forgot to `export OCI_PRIVATE_KEY="$(cat ...)"` before
  `up`. The `${VAR:?...}` guard in the compose file is intentional — it
  prevents starting vlm-worker without a real PEM in scope.
- **vlm-worker keeps restarting with OCI signature errors.** The
  `OCI_FINGERPRINT` in `vlm-worker.dev.env` must match the fingerprint of
  the PEM at `vlm-worker.oci.key.pem`. If you rotated keys, refresh both.
- **Workflows enqueued but vlm-worker doesn't pick them up.** The
  `TASK_QUEUE` in `vlm-worker.dev.env` (default
  `vsa-lifecycle-manager-9.18.1`) must match the `ontapVersion` you're
  exercising. The Helm chart at runtime builds this as
  `<workerConfig.taskQueuePrefix>-<ontapVersion>`; locally you set it
  explicitly.
- **`ONTAP_CREDENTIAL_ENCRYPT_KEY` mismatch — workflow input fails to
  decode.** `clients/vlm/credential_helper.go` ships a custom
  `OntapCredentials.MarshalJSON` / `UnmarshalJSON` that flips between
  plain JSON and AES-GCM+base64 based on whether the env var is set.
  When one side has the key set and the other doesn't, the child
  workflow `vlm.CreateVSAClusterDeploymentWorkflow` fails *before
  running* with:

  ```
  unable to decode the workflow function input payload with error:
  payload item 0: unable to decode: illegal base64 data at input byte 0
  ```

  The `{` of a JSON object is byte 0x7B, not in the base64 alphabet, so
  `base64.DecodeString` bails immediately. **Fix:** set
  `ONTAP_CREDENTIAL_ENCRYPT_KEY` to the same 32-byte string on BOTH
  `worker/oci.dev.env` and `kubernetes/OCI/vlm-worker/vlm-worker.dev.env`,
  then restart vcp-worker (and `container-engine.sh compose up -d
  --force-recreate vlm-worker` if you changed the container side). When the keys exist
  but differ, you'll see `cipher: message authentication failed`
  instead — same fix.
- **`VSA_VLM_ENCRYPTION_KEY` is a Helm leftover.** It appears in the
  vcp-worker ConfigMap but no Go code in this repo reads it. Set it for
  parity with prod, but the only env var that actually drives runtime
  behaviour is `ONTAP_CREDENTIAL_ENCRYPT_KEY` (which lives in the
  vcp-worker Secret in prod).
- **`container-engine.sh` says the Docker daemon isn't reachable.** Docker
  Desktop isn't running. `open -a Docker`, wait for the whale icon to settle,
  then verify with `docker info`. See §2.2.
- **`docker` command not found, but Docker Desktop is installed.** The
  installer didn't symlink the CLI onto PATH. The helper finds it under
  `/Applications/Docker.app/...` automatically; for your own terminal,
  symlink it (see §2.2 step 3).
- **Apple Silicon: vlm-worker exits with `exec format error`.** amd64
  emulation is off. Ensure the `vlm-worker` service pins
  `platform: linux/amd64` and **Settings → General → "Use Rosetta for
  x86/amd64 emulation"** is enabled, then restart Docker Desktop. See §2.3.

## 10. Summary of files we changed in this setup

| File | Status | Action by a new dev |
|---|---|---|
| `vsa-control-plane/oci-proxy/oci.dev.env.tmpl` | New, committable | `cp` to `oci.dev.env` |
| `vsa-control-plane/worker/oci.dev.env.tmpl` | New, committable | `cp` to `oci.dev.env` |
| `vsa-control-plane/kubernetes/OCI/vlm-worker/vlm-worker.dev.env.tmpl` | New, committable | `cp` to `vlm-worker.dev.env` |
| `vsa-control-plane/.gitignore` | Updated, committable | Adds `*.pem` / `*.key` so OCI API keys can't leak |
| `vsa-control-plane/.cursor/agents/oci-local-dev.mdc` | New, committable | Trigger via `/setup-oci-local` or "setup oci local dev" in Cursor chat |
| `vsa-control-plane/.cursor/commands/setup-oci-local.md` | New, committable | Provides the `/setup-oci-local` slash command |
| `vsa-control-plane/.cursor/skills/setup-oci-local/SKILL.md` | New, committable | Makes `/setup-oci-local` appear in Cursor's `/` picker |
| `vsa-control-plane/scripts/oci-local-dev/container-engine.sh` | New, committable | Docker helper. `ensure` verifies the Docker daemon; auto-finds Docker Desktop's CLI and drives `compose`/`exec` |
| `vsa-control-plane/doc/guides/local-oci-dev-setup.md` | New, committable | Read this doc |
| `vsa-control-plane/oci-proxy/oci.dev.env` | Local only | Created by `cp`, then fill secrets |
| `vsa-control-plane/worker/oci.dev.env` | Local only | Created by `cp`, then fill secrets |
| `vsa-control-plane/kubernetes/OCI/vlm-worker/vlm-worker.dev.env` | Local only | Created by `cp`, then fill secrets |
| `vsa-control-plane/kubernetes/OCI/vlm-worker/vlm-worker.oci.key.pem` | Local only | Place your OCI API private key here |
| `~/work/vcp-dev/.vscode/launch.json` | Local only | Copy contents from section 3.1 |
| `~/work/vcp-dev/docker-compose.local.yml` | Local only | Copy contents from section 3.2 |
| `~/work/vcp-dev/local-dev/postgres-init/01-create-databases.sql` | Local only | Copy contents from section 3.3 |
