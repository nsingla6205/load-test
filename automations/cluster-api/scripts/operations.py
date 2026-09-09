#!/usr/bin/env python3
"""
Shared operation catalog for VCP Cluster API automation.

This module is the registry, not the place for operation implementations.
Each domain keeps its own module and registers handlers here:

  cluster_upgrade.py  — upgrade, upgrade-cluster, verify, discover
  (later)             — list-images / register-image, and other Core APIs

The CLI and GitHub workflow both drive operations from this registry.
Transport (config, auth, requests, polling) comes from cluster_api.
"""

from __future__ import annotations

import json
from dataclasses import dataclass, field
from typing import Any, Callable

import cluster_api

SAFETY_READ = "read"
SAFETY_MUTATE = "mutate"
SAFETY_DESTRUCTIVE = "destructive"
WRITE_SAFETY = (SAFETY_MUTATE, SAFETY_DESTRUCTIVE)

Params = dict[str, Any]
Handler = Callable[[dict[str, Any], Params], int]


@dataclass(frozen=True)
class Param:
    """A single operation parameter, rendered into a CLI flag by the CLI layer."""

    name: str
    help: str
    kind: str = "str"  # str | int | list | bool
    default: Any = None
    required: bool = False
    choices: tuple[str, ...] | None = None

    @property
    def flag(self) -> str:
        return "--" + self.name.replace("_", "-")


@dataclass(frozen=True)
class Operation:
    name: str
    summary: str
    safety: str
    handler: Handler
    params: tuple[Param, ...] = field(default_factory=tuple)

    @property
    def is_write(self) -> bool:
        return self.safety in WRITE_SAFETY


REGISTRY: dict[str, Operation] = {}


def register(
    name: str,
    summary: str,
    safety: str,
    params: tuple[Param, ...] = (),
) -> Callable[[Handler], Handler]:
    def decorator(handler: Handler) -> Handler:
        if name in REGISTRY:
            raise ValueError(f"operation {name!r} is already registered")
        REGISTRY[name] = Operation(
            name=name,
            summary=summary,
            safety=safety,
            handler=handler,
            params=params,
        )
        return handler

    return decorator


def get(name: str) -> Operation:
    try:
        return REGISTRY[name]
    except KeyError:
        known = ", ".join(sorted(REGISTRY))
        raise ValueError(f"unknown operation {name!r}; available: {known}") from None


def names() -> list[str]:
    return sorted(REGISTRY)


def describe() -> list[str]:
    return [f"{op.name:16} [{op.safety:6}] {op.summary}" for op in sorted(REGISTRY.values(), key=lambda o: o.name)]


def submit(
    config: dict[str, Any],
    method: str,
    path: str,
    body: dict[str, Any] | None,
    *,
    dry_run: bool,
) -> tuple[int, Any] | None:
    """Send a write, or print it and return None when running as a dry run."""
    if dry_run:
        cluster_api.print_status(f"PLANNED {method} {path} (not sent)", body or {})
        print("\nDRY RUN: 0 mutations sent")
        return None
    return cluster_api.api_request(config, method, path, body=body)


def poll_settings(config: dict[str, Any], params: Params, default_interval: int) -> dict[str, int]:
    """Poll budget comes from the reviewed config; dispatch cannot extend it."""
    poll = config.get("poll") or {}
    timeout_minutes = params.get("timeout_minutes") or poll.get("timeout_minutes") or 720
    interval = params.get("poll_interval") or poll.get("interval_seconds") or default_interval
    return {"timeout": int(timeout_minutes) * 60, "interval": int(interval)}


def split_list(value: Any) -> list[str]:
    """Accept a list, or a comma/whitespace separated string from CI inputs."""
    if not value:
        return []
    if isinstance(value, (list, tuple)):
        items = value
    else:
        items = str(value).replace(",", " ").split()
    return [str(item).strip() for item in items if str(item).strip()]


def image_index(payload: Any) -> dict[str, dict[str, Any]]:
    if not isinstance(payload, dict):
        return {}
    return {v.get("ontapVersion"): v for v in (payload.get("versions") or []) if isinstance(v, dict)}


# Image operations stay here until a dedicated module is added.
# Upgrade operations live in cluster_upgrade.py.


@register(
    "list-images",
    "GET /v1/imageVersions: registered VSA images and the current default",
    SAFETY_READ,
)
def op_list_images(config: dict[str, Any], params: Params) -> int:
    status, health = cluster_api.api_request(config, "GET", "/health")
    if status != 200:
        print(f"list-images failed: GET /health -> {status}: {health}")
        return 1
    print("GET /health: OK")

    status, data = cluster_api.api_request(config, "GET", "/v1/imageVersions")
    if status != 200:
        print(f"list-images failed: GET /v1/imageVersions -> {status}: {data}")
        return 1
    print("GET /v1/imageVersions: OK (JWT accepted)")
    cluster_api.print_status("IMAGE VERSIONS", data)

    versions = image_index(data)
    current = data.get("current", "") if isinstance(data, dict) else ""
    target = (config.get("target_image") or {}).get("ontap_version", "")

    rows = ["| ONTAP | vsaName | mediatorName | active | default |", "|---|---|---|---|---|"]
    for version, entry in sorted(versions.items(), key=lambda kv: str(kv[0])):
        rows.append(
            f"| `{version}` | `{entry.get('vsaName', '')}` | `{entry.get('mediatorName', '')}` "
            f"| {entry.get('isActive', '')} | {'yes' if version == current else ''} |"
        )
    cluster_api.github_summary(["### Registered image versions", ""] + rows + [""])

    print(f"\ncurrent default: {current or '(none reported)'}")
    if target:
        if target in versions:
            entry = versions[target]
            print(
                f"target {target}: vsaName={entry.get('vsaName')} "
                f"mediatorName={entry.get('mediatorName')} active={entry.get('isActive')}"
            )
        else:
            print(f"target {target}: NOT registered — run register-image first")
    return 0


@register(
    "register-image",
    "POST /v1/imageVersions: register the target VSA and mediator build",
    SAFETY_MUTATE,
)
def op_register_image(config: dict[str, Any], params: Params) -> int:
    try:
        target = cluster_api.require_target_image(config, cluster_api.TARGET_IMAGE_FIELDS)
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

    status, listed = cluster_api.api_request(config, "GET", "/v1/imageVersions")
    if status == 200 and target["ontap_version"] in image_index(listed):
        print(f"ONTAP {target['ontap_version']} is already registered; nothing to do")
        cluster_api.print_status("IMAGE VERSIONS", listed)
        return 0

    result = submit(config, "POST", "/v1/imageVersions", body, dry_run=bool(params.get("dry_run")))
    if result is None:
        return 0
    status, data = result
    if status not in (200, 201):
        print(f"register-image failed: {status}: {data}")
        cluster_api.github_error(f"register-image failed: {status}: {data}")
        return 1

    print(json.dumps(data, indent=2))
    status, listed = cluster_api.api_request(config, "GET", "/v1/imageVersions")
    if status == 200:
        cluster_api.print_status("EXECUTION STATUS", listed)
    cluster_api.github_summary(
        [
            "### register-image",
            "",
            f"- Registered ONTAP `{target['ontap_version']}` as `{target['vsa_name']}`",
            f"- Mediator `{target['mediator_name']}`",
            "",
        ]
    )
    print(
        "\nReminder: also update VSA_IMAGE_NAME / VSA_MEDIATOR_IMAGE_NAME / "
        "ONTAP_VERSION_DETAILS in the VCP release."
    )
    return 0
