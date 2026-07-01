---
name: setup-oci-local
description: Bring up the OCI local development stack for oci-proxy and vcp-worker — Postgres + Temporal + vlm-worker on Docker, env files, and Cursor Run/Debug compounds. Use when the user asks to set up OCI local dev, start the OCI stack, run oci-proxy or vcp-worker locally, bring up OCI Postgres + Temporal, or types /setup-oci-local.
---

# Setup OCI Local Dev

Brings up a fully working local environment for `oci-proxy` and `vcp-worker`
running from source under Cursor's Go debugger, against Postgres, Temporal, and
the prebuilt `vlm-worker` running in **Docker** (Docker Desktop on macOS).

## Container engine

The stack runs on Docker. Never call `docker` directly — route every container
command through the committed helper, which resolves Docker (auto-finding
Docker Desktop's CLI even when it isn't symlinked onto PATH) and drives
compose/exec:

```
vsa-control-plane/scripts/oci-local-dev/container-engine.sh {ensure|detect|engine|compose|exec}
```

If `ensure` reports the daemon isn't reachable, start Docker Desktop
(`open -a Docker`) and re-run.

## Authoritative sources

Read these in order at the start of every run. Do not paraphrase or
re-derive — defer to them when in doubt:

1. `.cursor/agents/oci-local-dev.mdc` — Steps 0–7, troubleshooting, hard
   constraints.
2. `doc/guides/local-oci-dev-setup.md` — committable vs local-only files,
   full reference doc.

## Security rules

- **Never echo or log secrets** (`DB_PASSWORD`, `OCI_ONTAP_ADMIN_PASSWORD`,
  `ONTAP_CREDENTIAL_ENCRYPT_KEY` / `VSA_VLM_ENCRYPTION_KEY`, OCI API private
  key). Confirm with "saved" / "updated" and move on. Note that
  `ONTAP_CREDENTIAL_ENCRYPT_KEY` is the canonical name read by Go code; its
  value must be byte-identical across `worker/oci.dev.env` and
  `kubernetes/OCI/vlm-worker/vlm-worker.dev.env`.
- **Never commit** `*.env` files, `.vscode/launch.json` at the workspace
  root, `docker-compose.local.yml` at the workspace root, or anything under
  `local-dev/`.
- The only committable artifacts this skill touches:
  - `vsa-control-plane/oci-proxy/oci.dev.env.tmpl`
  - `vsa-control-plane/worker/oci.dev.env.tmpl`
  - `vsa-control-plane/kubernetes/OCI/vlm-worker/vlm-worker.dev.env.tmpl`
  - `vsa-control-plane/scripts/oci-local-dev/container-engine.sh`
  - `vsa-control-plane/doc/guides/local-oci-dev-setup.md`
  - `vsa-control-plane/.cursor/agents/oci-local-dev.mdc`
  - `vsa-control-plane/.cursor/commands/setup-oci-local.md`
  - This skill (`.cursor/skills/setup-oci-local/SKILL.md`)
- Ask before any destructive command (`container-engine.sh compose down -v`,
  deleting env files, killing user containers).

## Workflow

Run from the workspace root (the directory that contains
`vsa-control-plane/`). Each step's exact commands and validations live in
`.cursor/agents/oci-local-dev.mdc`; this skill provides the high-level
checklist.

Copy this progress block and update as you go:

```
Task Progress:
- [ ] Step 0: Resolve REPO_ROOT and WORKSPACE_ROOT
- [ ] Step 1: Verify Go; ensure Docker via
              scripts/oci-local-dev/container-engine.sh ensure (start Docker
              Desktop if the daemon isn't reachable), then `... detect`
- [ ] Step 2: Materialise workspace-root infra files
        .vscode/launch.json
        docker-compose.local.yml
        local-dev/postgres-init/01-create-databases.sql
- [ ] Step 3: cp -n *.dev.env.tmpl *.dev.env for all three services:
        oci-proxy/oci.dev.env
        worker/oci.dev.env
        kubernetes/OCI/vlm-worker/vlm-worker.dev.env
- [ ] Step 4: Prompt user for any REPLACE_ME values via AskQuestion
- [ ] Step 5: container-engine.sh compose up -d
- [ ] Step 6: Verify postgres + temporal healthy, vcp + temporal DBs exist,
              Temporal UI reachable
- [ ] Step 7: Print Cursor Run/Debug compound instructions and URLs
```

For the full file contents the skill writes when materialising the
workspace-root infra files (launch.json, docker-compose.local.yml, init SQL),
read section 3 of `doc/guides/local-oci-dev-setup.md`.

## Inputs

Accept any of the following inline with `/setup-oci-local`:

- `--mode=local` (default) — local Docker Postgres + Temporal.
- `--mode=oci-postgres` — Docker Temporal, but `DB_HOST` flipped to the OCI
  Postgres endpoint and `DB_SSL_MODE=require`. Prompt for the OCI DB
  password.
- `--skip-secrets` — fill `REPLACE_ME` values with safe non-empty
  placeholders. Only on explicit request and only acceptable when the user
  acknowledges they will not exercise OCI-credential code paths.

If no flag is supplied, default to `--mode=local`.

## Acceptance criteria

Done when **all** of the following are true:

1. `container-engine.sh compose ps` shows `postgres`, `temporal`, and
   `temporal-ui` healthy / running.
2. `container-engine.sh exec vcp-postgres psql -U postgres -lqt` lists both
   `vcp` and `temporal` databases.
3. `curl -fsS http://localhost:8088/ >/dev/null` returns 0 (Temporal UI
   reachable).
4. `vsa-control-plane/oci-proxy/oci.dev.env` and
   `vsa-control-plane/worker/oci.dev.env` both exist, are gitignored
   (`git check-ignore -v` confirms), and contain no `REPLACE_ME` markers.
5. The user has been told which compound to launch in Cursor's
   Run & Debug.

## Output

After acceptance criteria pass, print exactly this block (substituting
real container names if the user customised them):

```
All checks passed.

Open Run & Debug (Cmd-Shift-D) and launch one of:
  • OCI stack: proxy + customer worker
  • OCI stack: proxy + customer + background workers

Useful URLs:
  • Temporal UI    http://localhost:8088
  • oci-proxy API  http://localhost:8080
  • metrics        http://localhost:9090 (customer), :9091 (background)
```

## When NOT to use this skill

Redirect the user if their request maps to a different workflow:

- "skaffold", "kubernetes local", "minikube" → use the `local-env` agent
  (`.cursor/agents/local-env.mdc`).
- "code review", "review PR" → use `/review`.
- "set up an OCI cluster in OKE", "deploy to OKE" → out of scope; this
  skill is for running services from source on the developer's laptop.

## Communication style

- One-line summary per step
  (e.g. `Step 2: launch.json EXISTS · docker-compose MISSING → wrote`).
- On failure, show the exact stderr and the most likely fix from the agent
  rule's Troubleshooting table — never retry blindly.
- Never quote a secret value the user provides, even partially. Confirm
  with "saved" / "updated" and move on.
- Defer to `doc/guides/local-oci-dev-setup.md` for any detail not covered
  here.
