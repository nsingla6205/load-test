# VLM Worker Reconciler

Go binary and Dockerfile for the post-upgrade reconciler hook Job in the `vlm-worker` Helm chart.

**Design document:** [doc/architecture/designs/0029-vlm-worker-reconciler-design.md](../../doc/architecture/designs/0029-vlm-worker-reconciler-design.md)

## What it does

Queries VCP Postgres for active ONTAP versions, applies version-hierarchy rules, and scales inactive `vlm-worker-*` Deployments to `replicas=0`. Active and migration-required workers are left untouched.

## Image

The binary is compiled into `gcr.io/distroless/static:nonroot` — no shell, no package manager, non-root user. It runs as the Job's sole container with the reconcile logic embedded in the Go binary (not in the Helm template).

## Building

```bash
# Build image locally (linux/amd64 for production)
make vlm-worker-reconciler-image imageVersion=<version>

# Build and push to GAR
make vlm-worker-reconciler-push imageVersion=<version>
```

Requires `gcloud auth configure-docker us-docker.pkg.dev` before pushing.

## Source

| File | Purpose                                                                                                       |
|------|---------------------------------------------------------------------------------------------------------------|
| `main.go` | Config, K8s API client, DB query, orchestration and guardrails                                                |
| `version.go` | Version parsing, normalization, and classification rules (Rules 1–3, G4, G5)                                  |
| `Dockerfile` | Multi-stage build: `mirror.gcr.io/library/golang:1.25.12-alpine` builder → `gcr.io/distroless/static:nonroot` |

## Temporal D-patch drain Deployments

Deployments labeled `vlm.netapp.io/role=drain` are **always kept**. They are retired only by the versioning rollout hook after Temporal reports drained — see [0032](../../doc/architecture/designs/0032-vlm-dpatch-temporal-worker-versioning.md) and [tools/vlm-worker-versioning-rollout](../vlm-worker-versioning-rollout/).
