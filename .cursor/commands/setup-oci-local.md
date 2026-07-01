# VSA Control Plane — setup OCI local dev (command)

You are the **OCI local-dev operator** for this repository. Your job is to
bring up a fully working local environment for `oci-proxy` and `vcp-worker`
running directly from source under Cursor's Go debugger, against a Postgres,
Temporal, and `vlm-worker` stack running on **Docker** (Docker Desktop on
macOS).

**Container engine:** the stack runs on Docker. Never call `docker` directly —
route container commands through `scripts/oci-local-dev/container-engine.sh`
(`ensure` | `detect` | `engine` | `compose` | `exec`). The helper resolves
Docker even when Docker Desktop's CLI isn't symlinked onto PATH; if `ensure`
reports the daemon isn't reachable, start Docker Desktop (`open -a Docker`)
and re-run.

**Authoritative source:** read `.cursor/agents/oci-local-dev.mdc` in full
at the start of the run and follow its Steps 0–7 in order. Do **not**
re-derive the workflow from memory; update the agent file when the
procedure changes, not this command file.

**Reference doc:** `doc/guides/local-oci-dev-setup.md` documents what we
are building and which files are committable vs. local-only.

---

## Scope

| In scope | Out of scope |
|---|---|
| Materialising `.vscode/launch.json`, `docker-compose.local.yml` (incl. the prebuilt `vlm-worker` service), `local-dev/postgres-init/01-create-databases.sql` (with `vcp`, `temporal`, `temporal_visibility`) at the **workspace root** (the parent of `vsa-control-plane/`). | Anything inside Kubernetes / Skaffold / GCP — that's `/local-env` territory (`.cursor/agents/local-env.mdc`). |
| `cp -n` for the three `*.dev.env.tmpl` templates: `oci-proxy/`, `worker/`, and `kubernetes/OCI/vlm-worker/`. | Editing `*.dev.env.tmpl` files. They are the source of truth and committable. |
| Prompting for `REPLACE_ME` secrets (`OCI_ONTAP_ADMIN_PASSWORD`; `ONTAP_CREDENTIAL_ENCRYPT_KEY` written byte-identically to BOTH `worker/oci.dev.env` and `kubernetes/OCI/vlm-worker/vlm-worker.dev.env`; `OCI_TENANCY`/`OCI_USER`/`OCI_FINGERPRINT`) via `AskQuestion`. | Echoing any secret value the user provides, ever. Treating `VSA_VLM_ENCRYPTION_KEY` as a separate value — it is a Helm-legacy ConfigMap name not consumed by Go code; set it to the same string as `ONTAP_CREDENTIAL_ENCRYPT_KEY` for parity. |
| Telling the user to `export OCI_PRIVATE_KEY="$(cat .../vlm-worker.oci.key.pem)"` before `container-engine.sh compose up`. | Reading or echoing PEM contents anywhere; auto-generating an OCI API key. |
| Ensuring Docker is reachable via `scripts/oci-local-dev/container-engine.sh ensure` (tells the user to start Docker Desktop if the daemon is down). | Installing Docker Desktop for the user — point them at docker.com / `brew install --cask docker` if it's missing. |
| Starting `container-engine.sh compose up -d` (postgres + temporal + temporal-ui + vlm-worker) and verifying health, including that `vlm-worker` is polling Temporal on `vsa-lifecycle-manager-<ontapVersion>`. | Tearing the stack down — only on explicit user request. |
| Telling the user which Cursor Run/Debug compound to launch (`OCI stack: proxy + customer worker`). | Actually running `dlv` / starting Go services. The user clicks Run & Debug. |

---

## Hard constraints

- **Never echo or log secrets.** Confirm with "saved" / "updated" and move on.
- **Never commit** `*.env` files, `*.pem` / `*.key` files (gitignored in
  `vsa-control-plane/.gitignore`), `.vscode/launch.json` at the workspace
  root, `docker-compose.local.yml` at the workspace root, or anything
  under `local-dev/`. They are intentionally gitignored or outside the repo.
- The committable artifacts you may touch are:
  - `vsa-control-plane/oci-proxy/oci.dev.env.tmpl`
  - `vsa-control-plane/worker/oci.dev.env.tmpl`
  - `vsa-control-plane/kubernetes/OCI/vlm-worker/vlm-worker.dev.env.tmpl`
  - `vsa-control-plane/scripts/oci-local-dev/container-engine.sh`
  - `vsa-control-plane/doc/guides/local-oci-dev-setup.md`
  - `vsa-control-plane/.cursor/agents/oci-local-dev.mdc`
  - `vsa-control-plane/.cursor/commands/setup-oci-local.md` (this file)
- **Ask before any destructive command** (`container-engine.sh compose
  down -v`, `docker volume rm`, deleting env files, killing user
  containers).
- Run all Shell commands from the **workspace root** (the directory that
  contains `vsa-control-plane/`).

---

## Inputs the user may provide

Accept any of the following inline with `/setup-oci-local`:

- `--mode=local` (default) — local Docker Postgres + Temporal.
- `--mode=oci-postgres` — keep Docker Temporal, but point `DB_HOST` at the
  OCI Postgres endpoint and flip `DB_SSL_MODE=require`. Prompt for the OCI
  DB password.
- `--skip-secrets` — fill `REPLACE_ME` values with safe non-empty
  placeholders. Only do this on explicit request; the user must
  acknowledge they won't be exercising OCI-credential code paths.

If no flag is given, default to `--mode=local` and prompt as described in the
agent rule.

---

## Output style

- One-line summary per Step (e.g. `Step 2: launch.json EXISTS · docker-compose MISSING → wrote`).
- After Step 6 health checks pass, print the **Cursor Run/Debug** instructions
  and the useful URLs:
  ```
  Open Run & Debug (Cmd-Shift-D) and launch:
    • OCI stack: proxy + customer worker
    • OCI stack: proxy + customer + background workers

  Useful URLs:
    • Temporal UI    http://localhost:8088
    • oci-proxy API  http://localhost:8080
    • metrics        http://localhost:9090 (customer), :9091 (background)
  ```
- On failure, show the exact stderr/output and the most likely fix from the
  agent rule's Troubleshooting table — do not retry blindly.

---

## When NOT to run this command

If the user says any of these, redirect:

- "skaffold" / "kubernetes" / "k8s local" / "minikube" → run
  `/local-env` (the existing `.cursor/agents/local-env.mdc` agent).
- "review" / "code review" → that's `/review`.
- "set up an OCI cluster in OKE" → out of scope; this command is for
  running services from source on the developer's laptop.

---

## Acceptance criteria

Done when **all** of the following are true:

1. `container-engine.sh compose ps` shows `postgres`, `temporal`,
   `temporal-ui`, **and `vlm-worker`** all healthy / running.
2. `container-engine.sh exec vcp-postgres psql -U postgres -lqt | cut -d\| -f1`
   lists `vcp`, `temporal`, and `temporal_visibility`.
3. `curl -fsS http://localhost:8088/ >/dev/null` returns 0 (Temporal UI
   reachable).
4. `vsa-control-plane/oci-proxy/oci.dev.env`,
   `vsa-control-plane/worker/oci.dev.env`, and
   `vsa-control-plane/kubernetes/OCI/vlm-worker/vlm-worker.dev.env` all
   exist, are gitignored (`git check-ignore -v` confirms), and contain
   no `REPLACE_ME` markers.
5. `vsa-control-plane/kubernetes/OCI/vlm-worker/vlm-worker.oci.key.pem`
   exists, is gitignored, and its contents are exported as `OCI_PRIVATE_KEY`
   in the calling shell at `compose up` time.
6. `container-engine.sh exec vcp-temporal temporal task-queue describe
   --task-queue vsa-lifecycle-manager-<ontapVersion> --namespace default
   --address $(hostname):7233` lists `vcp-vlm-worker`'s pollers under both
   `workflow` and `activity` queue types.
7. The user has been told which compound to launch in Cursor's Run & Debug.

Print a short summary at the end ("All checks passed — launch
`OCI stack: proxy + customer worker` from Run & Debug.") and stop.
