#!/usr/bin/env python3
"""
VSA Cluster API automation: transport plus CLI.

Runs any registered VCP core-api operation from the SRE runbooks:

    cluster_api.py --environment stage --project <sde> list-images
    cluster_api.py --environment stage --project <sde> --ontap-version 9.18.1 register-image
    cluster_api.py --environment stage --project <sde> --ontap-version 9.18.1 \
        upgrade --pool-ids <uuid> --max-concurrency 1
    cluster_api.py --environment stage --project <sde> --ontap-version 9.18.1 \
        verify --progress-id <batch-id>

This module owns config loading, producer resolution, JWT minting, requests and
progress polling, and it dispatches the CLI. Operation implementations live in
domain modules that register into operations.py: upgrade work is in
cluster_upgrade.py, and later domains are added the same way.

--dry-run makes write operations print the request they would send instead of
sending it. Read operations are unaffected.
"""

from __future__ import annotations

import argparse
import base64
import binascii
import importlib
import json
import os
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

# Running this file directly makes it `__main__`. Alias it under its real name
# so the domain modules that `import cluster_api` for transport reuse this
# module instead of loading a second copy of it.
sys.modules.setdefault("cluster_api", sys.modules[__name__])

from generate_token import (  # noqa: E402
    DEFAULT_TOKEN_LIFETIME_SECONDS,
    generate_token,
)
import operations  # noqa: E402

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

TARGET_IMAGE_FIELDS = ("ontap_version", "vsa_image_path", "vsa_name", "mediator_name")

# One module per Core API domain. Importing a module registers its operations,
# so a new domain is a new module plus one entry here.
OPERATION_MODULES = ("cluster_upgrade",)


# --------------------------------------------------------------------------- #
# config and producer resolution
# --------------------------------------------------------------------------- #


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


def resolve_target_image(config: dict[str, Any], ontap_version: str) -> None:
    """Promote a reviewed image_versions entry to the active target_image.

    Adding a new ONTAP release is a config change, so operators pass only the
    version and never retype image paths at dispatch time.
    """
    version = (ontap_version or "").strip()
    if not version:
        return

    catalog = config.get("image_versions") or {}
    entry = catalog.get(version)
    if entry is None:
        # Not in the catalog: keep the version and let the caller fall back to
        # explicit overrides, so an unlisted release is still reachable.
        config.setdefault("target_image", {})["ontap_version"] = version
        known = ", ".join(sorted(str(k) for k in catalog)) or "(none)"
        print(f"WARN: ONTAP {version} is not in image_versions ({known}); using explicit overrides")
        return

    target = dict(entry)
    target["ontap_version"] = version
    config["target_image"] = target


def apply_target_image_overrides(config: dict[str, Any], overrides: dict[str, str]) -> None:
    """Apply explicit target-image values on top of the rollout config."""
    target = config.setdefault("target_image", {})
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


# --------------------------------------------------------------------------- #
# auth and requests
# --------------------------------------------------------------------------- #


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
    headers = {
        "Authorization": cached_token(config),
        "Content-Type": "application/json",
    }
    correlation_id = config.get("correlation_id")
    if correlation_id:
        headers["x-correlation-id"] = correlation_id
    return headers


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

    try:
        resp = requests.request(
            method,
            url,
            headers=auth_headers(config),
            json=body,
            timeout=120,
        )
    except requests.RequestException as exc:
        # A port-forward restart mid-poll must not kill a multi-hour upgrade run;
        # callers treat a non-200 as retryable.
        return 0, f"request error: {exc}"
    try:
        payload = resp.json() if resp.content else None
    except json.JSONDecodeError:
        payload = resp.text
    return resp.status_code, payload


# --------------------------------------------------------------------------- #
# reporting and progress polling
# --------------------------------------------------------------------------- #


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


# --------------------------------------------------------------------------- #
# CLI
# --------------------------------------------------------------------------- #

_PARAM_KINDS = {"str": str, "int": int}


def load_operation_modules(modules: tuple[str, ...] = OPERATION_MODULES) -> None:
    for module in modules:
        importlib.import_module(module)


def add_operation_parsers(sub: argparse._SubParsersAction) -> None:
    """Render one subparser per registered operation from its Param list."""
    for name in operations.names():
        operation = operations.get(name)
        parser = sub.add_parser(name, help=f"[{operation.safety}] {operation.summary}")
        for param in operation.params:
            if param.kind == "bool":
                parser.add_argument(param.flag, action="store_true", help=param.help)
                continue
            kwargs: dict[str, Any] = {"help": param.help, "default": param.default}
            if param.kind == "list":
                kwargs["nargs"] = "*"
            else:
                kwargs["type"] = _PARAM_KINDS.get(param.kind, str)
            if param.choices:
                kwargs["choices"] = list(param.choices)
            if param.required:
                kwargs["required"] = True
            parser.add_argument(param.flag, **kwargs)


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="VSA cluster automation (VCP core-api operations)")
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
    parser.add_argument(
        "--project", required=True, help="SDE producer project id (config.projects key)"
    )
    parser.add_argument(
        "--ontap-version",
        default="",
        help="Target ONTAP version; resolves the image_versions entry from config",
    )
    parser.add_argument("--target-vsa-image-path", default="", help="Override target VSA image path")
    parser.add_argument("--target-vsa-name", default="", help="Override target VSA image name")
    parser.add_argument("--target-mediator-name", default="", help="Override target mediator image name")
    parser.add_argument(
        "--change-ticket",
        default=os.environ.get("CHANGE_TICKET", ""),
        help="Change record for write operations (required where the config demands it)",
    )
    parser.add_argument(
        "--correlation-id",
        default=os.environ.get("VCP_CORRELATION_ID", ""),
        help="x-correlation-id sent with every request, for log triage",
    )
    parser.add_argument(
        "--dry-run",
        action="store_true",
        help="Write operations print the request instead of sending it",
    )
    parser.add_argument(
        "--operations",
        action="store_true",
        help="List the registered operations with their safety class and exit",
    )
    sub = parser.add_subparsers(dest="operation", required=False)
    add_operation_parsers(sub)
    return parser


def operation_params(operation: operations.Operation, args: argparse.Namespace) -> operations.Params:
    params = {param.name: getattr(args, param.name, param.default) for param in operation.params}
    params["dry_run"] = args.dry_run
    return params


def check_write_gate(operation: operations.Operation, config: dict[str, Any], args: argparse.Namespace) -> str:
    """Return an error message when a write operation is missing its approval."""
    if not operation.is_write or args.dry_run:
        return ""
    if (config.get("governance") or {}).get("require_change_ticket") and not args.change_ticket:
        return (
            f"{operation.name} is a {operation.safety} operation in "
            f"{config.get('environment')} and needs --change-ticket"
        )
    return ""


def main() -> int:
    parser = build_parser()
    args = parser.parse_args()

    if args.operations:
        print("\n".join(operations.describe()))
        return 0
    if not args.operation:
        print("pass an operation: " + " | ".join(operations.names()))
        return 1

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
        operation = operations.get(args.operation)
        apply_project(config, resolve_project(config, args.project))
        resolve_target_image(config, args.ontap_version)
        apply_target_image_overrides(
            config,
            {
                "vsa_image_path": args.target_vsa_image_path,
                "vsa_name": args.target_vsa_name,
                "mediator_name": args.target_mediator_name,
            },
        )
    except ValueError as exc:
        print(exc)
        return 1

    if args.correlation_id:
        config["correlation_id"] = args.correlation_id
    if args.change_ticket:
        config["change_metadata"] = {
            "changeTicket": args.change_ticket,
            "triggeredBy": os.environ.get("GITHUB_ACTOR", os.environ.get("USER", "unknown")),
        }

    gate_error = check_write_gate(operation, config, args)
    if gate_error:
        print(gate_error)
        github_error(gate_error)
        return 1

    print(f"operation={operation.name} safety={operation.safety} dry_run={args.dry_run}")
    return operation.handler(config, operation_params(operation, args))


# Imported last: a domain module pulls transport from this module, so every
# transport symbol above must exist before its handlers are registered.
load_operation_modules()

if __name__ == "__main__":
    raise SystemExit(main())
