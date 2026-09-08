#!/usr/bin/env python3
"""
VSA cluster upgrade automation CLI (PoC).

Wraps existing VCP core-api endpoints from the manual SRE runbook.

--dry-run: GET-only, print current status, no mutations.
Without --dry-run: send the mutation and poll until execution status is terminal.
"""

from __future__ import annotations

import argparse
import base64
import binascii
import json
import os
import subprocess
import sys
import time
from pathlib import Path
from typing import Any

import requests
import yaml

# Allow running as script without package install
_SCRIPT_DIR = Path(__file__).resolve().parent
if str(_SCRIPT_DIR) not in sys.path:
    sys.path.insert(0, str(_SCRIPT_DIR))

from generate_token import (  # noqa: E402
    DEFAULT_TOKEN_LIFETIME_SECONDS,
    generate_token,
)

# core/models/cluster_upgrade.go: PENDING, IN_PROGRESS, COMPLETED, FAILED, CANCELLED
TERMINAL_STATUSES = {"COMPLETED", "FAILED", "CANCELLED"}
FAILED_STATUSES = {"FAILED", "CANCELLED"}

# A multi-hour batch poll must survive transient core-api or port-forward blips.
MAX_CONSECUTIVE_POLL_ERRORS = 5
MAX_GITHUB_ANNOTATIONS = 10

# core-api rejects tokens noticeably before their nominal exp, so refresh early.
TOKEN_REFRESH_MARGIN_SECONDS = 900
CONFIG_ENVIRONMENTS = ("autopush", "stage", "prod")
DEFAULT_CONFIG_NAME = "config.{environment}.yaml"


def resolve_config_path(path: str) -> Path | None:
    candidate = Path(path)
    if candidate.is_file():
        return candidate
    alt = Path(__file__).resolve().parent.parent / path
    if alt.is_file():
        return alt
    return None


def load_config(path: str) -> dict[str, Any]:
    with open(path, encoding="utf-8") as fh:
        return yaml.safe_load(fh)


def infer_environment(project_id: str) -> str:
    if "-autopush-sde-tst" in project_id:
        return "autopush"
    if "-autopush-sde-sqa" in project_id:
        return "autopush"
    if "-staging-sde" in project_id:
        return "stage"
    if project_id.startswith("netapp-") and project_id.endswith("-sde"):
        return "prod"
    return "unknown"


def resolve_project(config: dict[str, Any], project_id: str) -> dict[str, Any]:
    catalog = config.get("projects") or {}
    entry = catalog.get(project_id)
    if not isinstance(entry, dict):
        known = ", ".join(sorted(catalog.keys())) or "(none)"
        raise ValueError(f"unknown project {project_id!r}; config.projects has: {known}")

    region = (entry.get("region") or "").strip()
    short_name = (entry.get("short_name") or "").strip()
    if not region or not short_name:
        raise ValueError(f"project {project_id} is missing region or short_name")

    cluster = (entry.get("cluster") or "").strip()
    service_account = (entry.get("service_account") or "").strip()
    if not service_account:
        raise ValueError(
            f"project {project_id} is missing service_account "
            "(regional SA used for execution and JWT signJwt)"
        )
    project_number = os.environ.get("VCP_PROJECT_NUMBER") or str(entry.get("project_number") or "")
    return {
        "id": project_id,
        "location": short_name,
        "gcp_region": region,
        "environment": infer_environment(project_id),
        "impersonate_sa": service_account,
        "service_account": service_account,
        "project_number": project_number,
        "gke_cluster": cluster,
        "audience": config.get("audience", "https://cloudvolumesgcp-api.netapp.com"),
        "core_api_in_cluster": (
            (config.get("core_api") or {}).get("cluster_endpoint")
            or config.get("core_api_in_cluster")
            or "http://core-service.vcp.svc.cluster.local:8080"
        ),
    }


def apply_project(config: dict[str, Any], project: dict[str, Any]) -> None:
    config["environment"] = project["environment"]
    config["region"] = project["location"]
    config["project"] = project
    config["auth"] = {
        "impersonate_service_account": project["impersonate_sa"],
        "client_email": project["impersonate_sa"],
        "audience": project["audience"],
        "project_number": project["project_number"],
    }
    if not config.get("core_api"):
        config["core_api"] = {"base_url": "http://localhost:8080"}
    print(
        f"project={project['id']} env={project['environment']} "
        f"location={project['location']} gcp_region={project['gcp_region']} "
        f"sa={project['service_account']}"
    )


def apply_target_image_overrides(config: dict[str, Any], args: argparse.Namespace) -> None:
    """Apply CLI target-image values, falling back to the rollout config."""
    target = config.setdefault("target_image", {})
    overrides = {
        "ontap_version": args.target_ontap_version,
        "vsa_image_path": args.target_vsa_image_path,
        "vsa_name": args.target_vsa_name,
        "mediator_name": args.target_mediator_name,
    }
    for key, value in overrides.items():
        if value:
            target[key] = value


def require_target_image(config: dict[str, Any], fields: tuple[str, ...]) -> dict[str, Any]:
    target = config.get("target_image") or {}
    missing = [field for field in fields if not target.get(field)]
    if missing:
        flags = ", ".join(f"--target-{field.replace('_', '-')}" for field in missing)
        raise ValueError(f"missing target image values: {flags} (or target_image config values)")
    return target


def jwt_expiry(token: str) -> int | None:
    """Read the exp claim without verifying the signature (already trusted)."""
    try:
        payload = token.split(".")[1]
        padded = payload + "=" * (-len(payload) % 4)
        return int(json.loads(base64.urlsafe_b64decode(padded))["exp"])
    except (IndexError, ValueError, KeyError, binascii.Error):
        return None


def cached_token(config: dict[str, Any]) -> str:
    """Return a valid JWT, re-signing once the cached one nears expiry.

    A regional batch upgrade can poll for hours, well past a single token's
    lifetime, so every request goes through this cache instead of holding one
    token for the whole run.
    """
    cache = config.setdefault("_token_cache", {})
    now = time.time()
    if cache.get("token") and now < cache.get("refresh_at", 0):
        return cache["token"]

    lifetime = int(
        config.get("auth", {}).get("token_lifetime_seconds")
        or DEFAULT_TOKEN_LIFETIME_SECONDS
    )
    token = generate_token(config["auth"], lifetime_seconds=lifetime)
    expires_at = jwt_expiry(token) or (now + lifetime)
    cache["token"] = token
    cache["refresh_at"] = expires_at - TOKEN_REFRESH_MARGIN_SECONDS

    if cache.get("minted"):
        print(f"Refreshed core-api token (valid for {int(expires_at - now)}s)")
    cache["minted"] = True
    return token


def auth_headers(config: dict[str, Any]) -> dict[str, str]:
    token = cached_token(config)
    return {
        "Authorization": token,
        "Content-Type": "application/json",
    }


def api_request(
    config: dict[str, Any],
    method: str,
    path: str,
    *,
    body: dict[str, Any] | None = None,
) -> tuple[int, Any]:
    base = config["core_api"]["base_url"].rstrip("/")
    url = f"{base}{path}"
    if not config.get("auth", {}).get("project_number"):
        print(
            f"missing project_number for {config.get('project', {}).get('id', '?')} "
            f"(set projects.<id>.project_number or VCP_PROJECT_NUMBER; needed for JWT)"
        )
        return 1, None

    resp = requests.request(
        method,
        url,
        headers=auth_headers(config),
        json=body,
        timeout=120,
    )
    try:
        payload = resp.json() if resp.content else None
    except json.JSONDecodeError:
        payload = resp.text
    return resp.status_code, payload


def print_status(label: str, payload: Any) -> None:
    print(f"\n=== {label} ===")
    print(json.dumps(payload, indent=2, default=str))


def github_summary(lines: list[str]) -> None:
    """Append markdown to the GitHub Actions run summary, if running in CI."""
    path = os.environ.get("GITHUB_STEP_SUMMARY")
    if not path:
        return
    try:
        with open(path, "a", encoding="utf-8") as fh:
            fh.write("\n".join(lines) + "\n")
    except OSError as exc:
        print(f"WARN: could not write GitHub step summary: {exc}")


def github_error(message: str) -> None:
    if os.environ.get("GITHUB_ACTIONS") == "true":
        print(f"::error::{message}")


def collect_failure_details(data: dict[str, Any]) -> list[str]:
    """Flatten UpgradeProgress errors, failed clusters, and warnings."""
    details: list[str] = []
    for err in data.get("errors") or []:
        scope = err.get("clusterId") or "batch"
        details.append(
            f"[{scope}] {err.get('code', 'UNKNOWN')}: {err.get('message', '')} "
            f"(type={err.get('type', '')}, retryable={err.get('retryable', False)})"
        )
    for cluster in data.get("clusters") or []:
        if cluster.get("status") in FAILED_STATUSES:
            step = cluster.get("currentStep") or "unknown step"
            details.append(
                f"[{cluster.get('clusterId', 'unknown')}] {cluster.get('status')} at {step}"
            )
    for warning in data.get("warnings") or []:
        details.append(f"warning: {warning}")
    return details


def cluster_counts(data: dict[str, Any]) -> dict[str, int]:
    counts: dict[str, int] = {}
    for cluster in data.get("clusters") or []:
        status = cluster.get("status", "UNKNOWN")
        counts[status] = counts.get(status, 0) + 1
    return counts


def report_outcome(label: str, progress_id: str, data: dict[str, Any]) -> int:
    """Print terminal status plus failure details, mirrored into CI output."""
    status = data.get("status", "UNKNOWN")
    counts = cluster_counts(data)
    details = collect_failure_details(data)

    print_status("EXECUTION STATUS", data)

    summary = [f"### {label} `{progress_id}`", "", f"- Final status: **{status}**"]
    if counts:
        summary.append("- Clusters: " + ", ".join(f"{k}={v}" for k, v in sorted(counts.items())))
    if details:
        summary += ["", "Failure details:"] + [f"- {line}" for line in details]
    github_summary(summary + [""])

    failed_clusters = sum(counts.get(s, 0) for s in FAILED_STATUSES)
    if status == "COMPLETED" and not failed_clusters:
        print(f"\n{label} {progress_id}: COMPLETED")
        return 0

    print(f"\n{label} {progress_id}: {status}")
    for line in details:
        print(f"  {line}")
    for line in details[:MAX_GITHUB_ANNOTATIONS]:
        github_error(f"{label} {progress_id}: {line}")
    if not details:
        github_error(f"{label} {progress_id} ended as {status} with no error details")
    return 1


def poll_progress(
    config: dict[str, Any],
    path: str,
    progress_id: str,
    label: str,
    *,
    interval: int,
    timeout: int,
) -> int:
    """Poll an UpgradeProgress endpoint until terminal status, timeout, or fatal error."""
    deadline = time.time() + timeout
    consecutive_errors = 0

    while time.time() < deadline:
        http_status, data = api_request(config, "GET", path)
        if http_status != 200 or not isinstance(data, dict):
            consecutive_errors += 1
            print(
                f"WARN: {label} poll failed "
                f"({consecutive_errors}/{MAX_CONSECUTIVE_POLL_ERRORS}): {http_status}: {data}"
            )
            if consecutive_errors >= MAX_CONSECUTIVE_POLL_ERRORS:
                github_error(
                    f"{label} {progress_id}: polling failed {consecutive_errors} times in a row "
                    f"(last {http_status}). The upgrade may still be running in VCP."
                )
                return 1
            time.sleep(interval)
            continue

        consecutive_errors = 0
        status = data.get("status", "UNKNOWN")
        counts = cluster_counts(data)
        progress = ", ".join(f"{k}={v}" for k, v in sorted(counts.items())) or "no child jobs yet"
        remaining = int(deadline - time.time())
        print(f"{label} {progress_id}: {status} [{progress}] ({remaining}s budget left)")

        if status in TERMINAL_STATUSES:
            return report_outcome(label, progress_id, data)
        time.sleep(interval)

    print(f"\nTIMEOUT: {label} {progress_id} still running after {timeout}s")
    github_error(
        f"{label} {progress_id}: poll timed out after {timeout}s. The upgrade is still "
        "running in VCP; re-check with the matching status command using this id."
    )
    return 1


def cmd_dry_run(config: dict[str, Any], args: argparse.Namespace) -> int:
    """Connect, mint JWT, run GET-only checks, and return current status."""
    print("DRY RUN: no mutations (no POST/PUT/PATCH/DELETE)")

    status, health = api_request(config, "GET", "/health")
    if status != 200:
        print(f"dry-run failed: GET /health -> {status}: {health}")
        return 1
    print("GET /health: OK")

    status, data = api_request(config, "GET", "/v1/imageVersions")
    if status != 200:
        print(f"dry-run failed: GET /v1/imageVersions -> {status}: {data}")
        return 1
    print("GET /v1/imageVersions: OK (JWT accepted)")
    print_status("STATUS", data)

    target = config.get("target_image", {})
    deployment = config.get("deployment", {})
    current = data.get("current", "") if isinstance(data, dict) else ""
    target_ver = target.get("ontap_version", "")
    versions = {v["ontapVersion"]: v for v in (data.get("versions") or [])} if isinstance(data, dict) else {}

    if target_ver and target_ver not in versions:
        print(f"\nWARN: target ONTAP {target_ver} not in image_versions — run register-image (without --dry-run)")
    elif target_ver:
        v = versions[target_ver]
        print(f"\nTarget {target_ver}: vsaName={v.get('vsaName')} active={v.get('isActive')}")

    vsa_env = deployment.get("vsa_image_name", "")
    if vsa_env and target_ver in versions and versions[target_ver].get("vsaName") != vsa_env:
        print(
            f"\nWARN: deployment.vsa_image_name ({vsa_env}) != "
            f"image_versions.vsaName ({versions[target_ver].get('vsaName')})."
        )

    if current and target_ver and current != target_ver:
        print(f"\nWARN: API current={current} but target={target_ver}.")

    command = getattr(args, "command", None)
    if command == "register-image":
        print_status("PLANNED POST /v1/imageVersions (not sent)", {
            "ontapVersion": target.get("ontap_version"),
            "vsaImagePath": target.get("vsa_image_path"),
            "vsaName": target.get("vsa_name"),
            "mediatorName": target.get("mediator_name"),
            "isActive": target.get("is_active", True),
        })
    elif command == "upgrade-pool":
        print_status("PLANNED POST /v1/clusters/{poolId}/upgrade (not sent)", {
            "poolId": getattr(args, "pool_id", None),
            "forceUpgrade": getattr(args, "force_upgrade", False),
        })
    elif command == "batch-upgrade":
        print_status("PLANNED POST /v1/clusters/batchUpgrade (not sent)", {
            "selectionType": getattr(args, "selection_type", "percentage"),
            "percentage": getattr(args, "percentage", None),
            "ontapVersions": getattr(args, "ontap_versions", "") or "(all eligible)",
            "maxConcurrency": getattr(args, "max_concurrency", None),
            "maxClusters": getattr(args, "max_clusters", None),
        })

    print("\nDRY RUN COMPLETED: GET-only, 0 mutations")
    return 0


def cmd_register_image(config: dict[str, Any], args: argparse.Namespace) -> int:
    try:
        target = require_target_image(
            config, ("ontap_version", "vsa_image_path", "vsa_name", "mediator_name")
        )
    except ValueError as exc:
        print(exc)
        return 1
    body = {
        "ontapVersion": target["ontap_version"],
        "vsaImagePath": target["vsa_image_path"],
        "vsaName": target["vsa_name"],
        "mediatorName": target["mediator_name"],
        "isActive": target.get("is_active", True),
    }
    status, data = api_request(config, "POST", "/v1/imageVersions", body=body)
    if status not in (200, 201):
        print(f"register-image failed: {status}: {data}")
        return 1
    print(json.dumps(data, indent=2))
    status, listed = api_request(config, "GET", "/v1/imageVersions")
    if status == 200:
        print_status("EXECUTION STATUS", listed)
    print("\nReminder: also update VSA_IMAGE_NAME / VSA_MEDIATOR_IMAGE_NAME / ONTAP_VERSION_DETAILS in the VCP release.")
    return 0


def cmd_upgrade_pool(config: dict[str, Any], args: argparse.Namespace) -> int:
    pool_id = args.pool_id
    body: dict[str, Any] = {}
    if args.force_upgrade:
        try:
            target = require_target_image(config, ("vsa_name", "mediator_name"))
        except ValueError as exc:
            print(exc)
            return 1
        body = {
            "vsaBuildImage": target["vsa_name"],
            "mediatorBuildImage": target["mediator_name"],
            "forceUpgrade": True,
        }

    status, data = api_request(
        config,
        "POST",
        f"/v1/clusters/{pool_id}/upgrade",
        body=body,
    )
    if status not in (200, 202):
        print(f"upgrade-pool failed: {status}: {data}")
        return 1
    print(json.dumps(data, indent=2))
    job_id = data.get("jobId")
    if not job_id:
        print("upgrade accepted but no jobId returned; cannot poll progress")
        print_status("EXECUTION STATUS", data)
        return 1
    print(f"\npool upgrade job id: {job_id}")
    return poll_progress(
        config,
        f"/v1/clusters/upgrade/{job_id}",
        job_id,
        "pool upgrade",
        interval=args.poll_interval,
        timeout=args.timeout,
    )


def cmd_batch_upgrade(config: dict[str, Any], args: argparse.Namespace) -> int:
    batch_cfg = config.get("batch", {})
    body: dict[str, Any] = {
        "selectionType": args.selection_type,
        "maxConcurrency": args.max_concurrency or batch_cfg.get("max_concurrency", 5),
        "failureThreshold": args.failure_threshold
        if args.failure_threshold is not None
        else batch_cfg.get("failure_threshold", 1),
    }

    if args.selection_type == "percentage":
        body["percentage"] = args.percentage
        # The API matches build_info->>'ontapVersion' with SQL IN, not LIKE, so
        # every version must be listed exactly (9.17.1P1, not 9.17.1).
        versions = [v.strip() for v in (args.ontap_versions or "").split(",") if v.strip()]
        if versions:
            body["filters"] = {"ontapVersions": versions}
    else:
        pool_ids = args.pool_ids or config.get("pool_ids") or []
        if not pool_ids:
            print("poolList requires --pool-ids or pool_ids in config")
            return 1
        body["poolIds"] = pool_ids

    if args.max_clusters:
        body["maxClusters"] = args.max_clusters

    if args.force_upgrade:
        try:
            target = require_target_image(config, ("vsa_name", "mediator_name"))
        except ValueError as exc:
            print(exc)
            return 1
        body.update({
            "vsaBuildImage": target["vsa_name"],
            "mediatorBuildImage": target["mediator_name"],
            "forceUpgrade": True,
        })

    status, data = api_request(config, "POST", "/v1/clusters/batchUpgrade", body=body)
    if status not in (200, 202):
        print(f"batch-upgrade failed: {status}: {data}")
        return 1
    print(json.dumps(data, indent=2))
    job_id = data.get("jobId") or data.get("batchUpgradeId")
    if not job_id:
        print("batch accepted but no batchUpgradeId returned; cannot poll progress")
        print_status("EXECUTION STATUS", data)
        return 1
    print(f"\nbatch upgrade id: {job_id} (totalClusters={data.get('totalClusters', '?')})")
    return poll_progress(
        config,
        f"/v1/clusters/batchUpgrade/{job_id}",
        job_id,
        "batch upgrade",
        interval=args.poll_interval,
        timeout=args.timeout,
    )


def cmd_pool_status(config: dict[str, Any], args: argparse.Namespace) -> int:
    status, data = api_request(config, "GET", f"/v1/clusters/upgrade/{args.job_id}")
    if status != 200:
        print(f"pool upgrade status failed: {status}: {data}")
        return 1
    if args.wait:
        return poll_progress(
            config,
            f"/v1/clusters/upgrade/{args.job_id}",
            args.job_id,
            "pool upgrade",
            interval=args.poll_interval,
            timeout=args.timeout,
        )
    print_status("POOL UPGRADE PROGRESS", data)
    for line in collect_failure_details(data):
        print(f"  {line}")
    return 0


def cmd_batch_status(config: dict[str, Any], args: argparse.Namespace) -> int:
    status, data = api_request(config, "GET", f"/v1/clusters/batchUpgrade/{args.batch_id}")
    if status != 200:
        print(f"batch upgrade status failed: {status}: {data}")
        return 1
    if args.wait:
        return poll_progress(
            config,
            f"/v1/clusters/batchUpgrade/{args.batch_id}",
            args.batch_id,
            "batch upgrade",
            interval=args.poll_interval,
            timeout=args.timeout,
        )
    print_status("BATCH UPGRADE PROGRESS", data)
    for line in collect_failure_details(data):
        print(f"  {line}")
    return 0


def cmd_discover(config: dict[str, Any], args: argparse.Namespace) -> int:
    db = config.get("database")
    if not db:
        print("discover requires database section in the environment config")
        return 1

    prefix = args.ontap_version_prefix.replace("'", "''")
    account_filter = ""
    if args.account_id:
        account_filter = f" AND account_id = {int(args.account_id)}"
    deployment_filter = ""
    if args.deployment_name:
        dep = args.deployment_name.replace("'", "''")
        deployment_filter = f" AND deployment_name = '{dep}'"

    sql = f"""
SELECT uuid, name, account_id, deployment_name,
       build_info->>'ontapVersion' AS ontap_version
FROM pools
WHERE deleted_at IS NULL
  AND state = 'READY'
  AND build_info->>'ontapVersion' LIKE '{prefix}%'
  {account_filter}
  {deployment_filter}
ORDER BY created_at;
""".strip()

    env = os.environ.copy()
    if db.get("password"):
        env["PGPASSWORD"] = db["password"]

    cmd = [
        "psql",
        "-h", db["host"],
        "-p", str(db.get("port", 5432)),
        "-U", db["user"],
        "-d", db["name"],
        "-At",
        "-F", "\t",
        "-c", sql,
    ]
    print(f"Running: psql ... -c <query>")
    try:
        result = subprocess.run(cmd, capture_output=True, text=True, check=True, env=env)
    except subprocess.CalledProcessError as exc:
        print(exc.stderr or exc.stdout)
        return 1
    except FileNotFoundError:
        print("psql not found — install PostgreSQL client or run the SQL manually (see README)")
        return 1

    uuids: list[str] = []
    if result.stdout.strip():
        print("uuid\tname\taccount_id\tdeployment_name\tontap_version")
        for line in result.stdout.strip().splitlines():
            print(line)
            uuids.append(line.split("\t", 1)[0])

    print(f"\nFound {len(uuids)} pool(s)")
    if uuids:
        print("pool_ids:")
        print(yaml.dump({"pool_ids": uuids}, default_flow_style=True).strip())
    return 0


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="VSA cluster upgrade automation (PoC)")
    parser.add_argument(
        "--environment",
        required=True,
        choices=CONFIG_ENVIRONMENTS,
        help="Loads config.<environment>.yaml (autopush | stage | prod)",
    )
    parser.add_argument(
        "--config",
        default="",
        help="Override config path. Default: config.<environment>.yaml",
    )
    parser.add_argument("--project", required=True, help="SDE producer project id (config.projects key)")
    parser.add_argument("--target-ontap-version", default="", help="Target ONTAP version; overrides config")
    parser.add_argument("--target-vsa-image-path", default="", help="Target VSA image path; overrides config")
    parser.add_argument("--target-vsa-name", default="", help="Target VSA image name; overrides config")
    parser.add_argument("--target-mediator-name", default="", help="Target mediator image name; overrides config")
    parser.add_argument(
        "--dry-run",
        action="store_true",
        help="GET-only: connect, mint JWT, print current status; no mutations",
    )
    sub = parser.add_subparsers(dest="command", required=False)

    sub.add_parser("register-image", help="POST /v1/imageVersions")

    p_pool = sub.add_parser("upgrade-pool", help="Upgrade a single pool/cluster")
    p_pool.add_argument("--pool-id", required=True)
    p_pool.add_argument("--force-upgrade", action="store_true", help="Pin target build images from config")
    p_pool.add_argument("--poll-interval", type=int, default=60)
    p_pool.add_argument("--timeout", type=int, default=7200)

    p_batch = sub.add_parser("batch-upgrade", help="POST /v1/clusters/batchUpgrade")
    p_batch.add_argument("--selection-type", choices=["percentage", "poolList"], default="percentage")
    p_batch.add_argument("--percentage", type=int, default=25)
    p_batch.add_argument(
        "--ontap-versions",
        default="",
        help="Comma-separated exact source ONTAP versions (API uses IN, not prefix match). "
        "Empty selects every eligible pool.",
    )
    p_batch.add_argument("--pool-ids", nargs="*", default=None)
    p_batch.add_argument("--max-concurrency", type=int, default=None)
    p_batch.add_argument("--failure-threshold", type=int, default=None)
    p_batch.add_argument("--max-clusters", type=int, default=0)
    p_batch.add_argument("--force-upgrade", action="store_true", help="Use target VSA and mediator CLI/config values")
    p_batch.add_argument("--poll-interval", type=int, default=120)
    p_batch.add_argument("--timeout", type=int, default=86400)

    p_pool_status = sub.add_parser("pool-status", help="GET progress for a single upgrade job")
    p_pool_status.add_argument("--job-id", required=True, help="jobId returned by upgrade-pool")
    p_pool_status.add_argument("--wait", action="store_true", help="Poll until terminal status")
    p_pool_status.add_argument("--poll-interval", type=int, default=60)
    p_pool_status.add_argument("--timeout", type=int, default=7200)

    p_batch_status = sub.add_parser("batch-status", help="GET progress for a batch upgrade")
    p_batch_status.add_argument("--batch-id", required=True, help="batchUpgradeId returned by batch-upgrade")
    p_batch_status.add_argument("--wait", action="store_true", help="Poll until terminal status")
    p_batch_status.add_argument("--poll-interval", type=int, default=120)
    p_batch_status.add_argument("--timeout", type=int, default=86400)

    p_disc = sub.add_parser("discover", help="SQL pool discovery (requires psql + database config)")
    p_disc.add_argument("--ontap-version-prefix", default="9.17.1")
    p_disc.add_argument("--account-id", type=int, default=None)
    p_disc.add_argument("--deployment-name", default=None)

    return parser


def main() -> int:
    parser = build_parser()
    args = parser.parse_args()
    config_arg = args.config or DEFAULT_CONFIG_NAME.format(environment=args.environment)
    config_path = resolve_config_path(config_arg)
    if config_path is None:
        print(f"config not found: {config_arg}")
        return 1

    config = load_config(str(config_path))
    file_env = (config.get("environment") or "").strip()
    if file_env and file_env != args.environment:
        print(
            f"config {config_path} is environment={file_env!r}, "
            f"but --environment {args.environment} was passed"
        )
        return 1
    try:
        apply_project(config, resolve_project(config, args.project))
        apply_target_image_overrides(config, args)
    except ValueError as exc:
        print(exc)
        return 1

    if args.dry_run:
        return cmd_dry_run(config, args)
    if not args.command:
        print(
            "pass --dry-run, or a command: register-image | upgrade-pool | "
            "batch-upgrade | pool-status | batch-status | discover"
        )
        return 1

    handlers = {
        "register-image": cmd_register_image,
        "upgrade-pool": cmd_upgrade_pool,
        "batch-upgrade": cmd_batch_upgrade,
        "pool-status": cmd_pool_status,
        "batch-status": cmd_batch_status,
        "discover": cmd_discover,
    }
    return handlers[args.command](config, args)


if __name__ == "__main__":
    raise SystemExit(main())
