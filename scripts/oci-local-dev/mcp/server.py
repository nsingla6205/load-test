#!/usr/bin/env python3
"""oci-infra MCP server.

Lets an agent install a PINNED OCI release locally and run pre-build gates:
migrations (Phase 0), stack bringup at an exact release (Phase 1), and
control-plane pool/SVM CRUD smoke (Phase 2). Every tool is a thin, auditable
wrapper over the shell scripts in scripts/oci-local-dev/, so the same checks run
identically by hand, in CI, or via the agent.

Run (stdio transport):
    python3 server.py
Register in .cursor/mcp.json (see README.md).
"""
from __future__ import annotations

import os
import subprocess
from pathlib import Path

try:
    from mcp.server.fastmcp import FastMCP
except ImportError as exc:  # pragma: no cover - clearer hint than a raw traceback
    raise SystemExit(
        "The 'mcp' package is required. Install it:\n"
        "  pip install -r scripts/oci-local-dev/mcp/requirements.txt"
    ) from exc

SCRIPTS_DIR = Path(__file__).resolve().parent.parent  # scripts/oci-local-dev/
REPO_ROOT = SCRIPTS_DIR.parent.parent                 # vsa-control-plane/
WORKSPACE_ROOT = REPO_ROOT.parent

mcp = FastMCP("oci-infra")


def _run(args: list[str], timeout: int = 600, cwd: Path | None = None) -> str:
    """Run a harness script and return a single combined transcript string."""
    try:
        proc = subprocess.run(
            args,
            cwd=str(cwd or WORKSPACE_ROOT),
            capture_output=True,
            text=True,
            timeout=timeout,
            env={**os.environ, "NO_COLOR": "1"},
        )
    except subprocess.TimeoutExpired as exc:
        return f"TIMEOUT after {timeout}s running: {' '.join(args)}\n{exc.stdout or ''}{exc.stderr or ''}"
    out = (proc.stdout or "") + (proc.stderr or "")
    return f"exit_code={proc.returncode}\n{out}".strip()


def _script(name: str) -> str:
    return str(SCRIPTS_DIR / name)


@mcp.tool()
def set_release(manifest_path: str) -> str:
    """Pin the exact image set 'shared to OCI' from a release manifest JSON.

    manifest_path: path to a manifest like scripts/oci-local-dev/release.example.json
    (release, ontapVersion, images.ociProxy/vcpWorker/vlmWorker). Writes
    .oci-release.env so the stack installs precisely those images.
    """
    return _run(["bash", _script("release.sh"), "apply", manifest_path], timeout=60)


@mcp.tool()
def show_release() -> str:
    """Show the currently pinned release (image refs from .oci-release.env)."""
    return _run(["bash", _script("release.sh"), "show"], timeout=30)


@mcp.tool()
def verify_release() -> str:
    """Best-effort check that each pinned image is reachable in the registry."""
    return _run(["bash", _script("release.sh"), "verify"], timeout=120)


@mcp.tool()
def run_migration_gate(image: str = "", snapshot_path: str = "", fresh: bool = True,
                       timeout_seconds: int = 240) -> str:
    """Phase 0 (OCI, IMAGE-DRIVEN): run the OCI release image as a one-shot
    migrator and detect a dirty schema — the production 'Dirty database version
    N' class of bug.

    In OCI there is NO separate db-migrate image: the oci-proxy image runs
    db.Migrate() on start (RUN_MIGRATION_ON_START=true) before it touches
    Temporal, so this gate uses the actual release image as the migrator.

    image: oci-proxy image to test (default: the pinned OCI_PROXY_IMAGE from the
    last set_release). snapshot_path: optional prod-like schema SQL to migrate on
    top of (a fresh empty DB will NOT reproduce drift-dependent failures like
    42P16). fresh: drop+recreate the DB first.
    """
    args = ["bash", _script("oci-migrate-gate.sh"), "--timeout", str(timeout_seconds)]
    if image:
        args += ["--image", image]
    if fresh:
        args.append("--fresh")
    if snapshot_path:
        args += ["--snapshot", snapshot_path]
    return _run(args, timeout=timeout_seconds + 180)


@mcp.tool()
def run_migration_upgrade(from_image: str, to_image: str, timeout_seconds: int = 240) -> str:
    """Phase 0 (OCI upgrade path): bring a fresh DB to the OLD release with its
    image, then apply the NEW release's migrations on top and detect dirty — the
    exact migration that runs when OCI upgrades from one release to the next.

    from_image: OLD oci-proxy image (already-deployed release).
    to_image:   NEW oci-proxy image (candidate release).
    Use the oci-proxy image refs from each release manifest. FAIL means the
    upgrade would dirty the DB in OCI — block the build.
    """
    args = ["bash", _script("oci-migrate-gate.sh"),
            "--from-image", from_image, "--to-image", to_image,
            "--timeout", str(timeout_seconds)]
    return _run(args, timeout=2 * timeout_seconds + 240)


@mcp.tool()
def list_proxy_versions(repo: str = "", all_tags: bool = False) -> str:
    """List the oci-proxy versions (tags) available in the container registry.

    repo: image repo to list (default: the OCI_PROXY_IMAGE repo from the last
    set_release, else iad.ocir.io/idqogasfjw45/oci-proxy). all_tags=False shows
    only release-like tags (NNN.N.N[-...]); all_tags=True shows every tag.
    Use these tags as from/to for check_upgrade_matrix.
    """
    args = ["bash", _script("version-compat.sh"), "list"]
    if repo:
        args += ["--repo", repo]
    if all_tags:
        args.append("--all")
    return _run(args, timeout=120)


@mcp.tool()
def check_upgrade_matrix(from_ref: str, to_refs: str = "", repo: str = "",
                         all_tags: bool = False, limit: int = 0,
                         timeout_seconds: int = 240) -> str:
    """Cross-version compatibility matrix for oci-proxy: from a baseline version,
    run the migration UPGRADE gate against each candidate version and report which
    upgrades stay clean vs leave the schema DIRTY, plus the recommended target.

    For every candidate it brings a fresh DB to the OLD (from_ref) release, then
    applies the candidate's migrations on top — exactly the upgrade OCI runs.

    from_ref: baseline you're upgrading FROM (a bare TAG or a full image ref).
    to_refs:  comma-separated candidate tags/refs. If empty, candidates are
              discovered from the registry (release-like unless all_tags=True),
              excluding the baseline tag.
    repo:     repo for bare tags (default like list_proxy_versions).
    limit:    cap the number of candidates (after version sort); 0 = no cap.
    timeout_seconds: per-image migration budget.

    NOTE: each candidate runs two image migrations, so this can take minutes per
    candidate. On Apple Silicon, amd64 images run under emulation; a candidate may
    report ENV? (runtime crash) which is inconclusive — confirm on a native amd64
    host / CI runner. Exit is non-zero when no compatible target is found.
    """
    args = ["bash", _script("version-compat.sh"), "check", "--from", from_ref,
            "--timeout", str(timeout_seconds)]
    if to_refs:
        args += ["--to", to_refs]
    if repo:
        args += ["--repo", repo]
    if all_tags:
        args.append("--all")
    if limit and limit > 0:
        args += ["--limit", str(limit)]
    # Generous overall budget: roughly two image-migrations per candidate. We
    # don't know the candidate count here, so allow a large ceiling.
    return _run(args, timeout=3600)


@mcp.tool()
def make_snapshot_from_release(ref: str, out: str = "scripts/oci-local-dev/snapshots/vcp-prodlike.sql") -> str:
    """Build a prod-like schema snapshot by replaying the migration chain of an
    already-deployed release git ref (alternative to run_migration_upgrade when
    you have a git ref instead of the OLD image). The gate then applies only NEW
    migrations on top via run_migration_gate(snapshot_path=...).
    """
    return _run(["bash", _script("schema-snapshot.sh"), "from-release", "--ref", ref, "--out", out], timeout=900)


@mcp.tool()
def bringup(crud: bool = False) -> str:
    """Phase 1: start the stack at the pinned release. crud=True also starts
    oci-proxy + vcp-worker + ontap-sim needed for CRUD smoke. Pin a release first.
    """
    args = ["bash", _script("stack.sh"), "up"]
    if crud:
        args.append("--crud")
    return _run(args, timeout=600)


@mcp.tool()
def teardown(crud: bool = True, wipe: bool = False) -> str:
    """Stop the stack. wipe=True also removes volumes (fresh DB next time)."""
    args = ["bash", _script("stack.sh"), "down"]
    if crud:
        args.append("--crud")
    if wipe:
        args.append("--wipe")
    return _run(args, timeout=180)


@mcp.tool()
def status() -> str:
    """Show container status for the stack."""
    return _run(["bash", _script("stack.sh"), "ps"], timeout=60)


@mcp.tool()
def get_logs(service: str, tail: int = 200) -> str:
    """Tail logs for a stack service (e.g. oci-proxy, vcp-worker, vlm-worker,
    temporal, postgres, ontap-sim)."""
    return _run(["bash", _script("stack.sh"), "logs", service, "--tail", str(tail)], timeout=60)


@mcp.tool()
def crud_smoke(timeout_seconds: int = 90, keep: bool = False) -> str:
    """Phase 2: control-plane pool + SVM CRUD smoke against the release oci-proxy.

    Returns PASS / PARTIAL / FAIL. FAIL means a control-plane defect (contract,
    dispatch, deterministic workflow error) caught before build. PARTIAL means
    the flow dispatched and ran but stopped at an external dependency that cannot
    be faked locally (real OCI compute / per-pool ONTAP IP).
    """
    args = ["bash", _script("crud-smoke.sh"), "--timeout", str(timeout_seconds)]
    if keep:
        args.append("--keep")
    return _run(args, timeout=timeout_seconds + 120)


if __name__ == "__main__":
    mcp.run()
