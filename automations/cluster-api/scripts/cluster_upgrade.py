#!/usr/bin/env python3
"""
Upgrade operations for the VCP Cluster API catalog.

This module owns only cluster upgrade work:
  upgrade          POST /v1/clusters/batchUpgrade and poll
  upgrade-cluster  POST /v1/clusters/{poolId}/upgrade and poll
  verify           confirm image + upgrade job terminal status
  discover         SQL pool inventory for an explicit poolList

Importing this module registers the operations above. cluster_api provides the
CLI and the transport (config, auth, requests, polling). Later domains
(images, hosts, …) get their own modules.
"""

from __future__ import annotations

import json
import os
import subprocess
from typing import Any

import yaml

import cluster_api
import operations
from operations import (
    Param,
    Params,
    SAFETY_MUTATE,
    SAFETY_READ,
    image_index,
    poll_settings,
    split_list,
    submit,
)


@operations.register(
    "verify",
    "Post-change checks: image registered and active, default version, upgrade job terminal",
    SAFETY_READ,
    params=(
        Param("progress_id", "jobId or batchUpgradeId to confirm as COMPLETED", required=False),
        Param(
            "progress_kind",
            "Which progress endpoint to read",
            choices=("auto", "batch", "pool"),
            default="auto",
        ),
    ),
)
def op_verify(config: dict[str, Any], params: Params) -> int:
    failures: list[str] = []
    notes: list[str] = []

    status, health = cluster_api.api_request(config, "GET", "/health")
    if status != 200:
        print(f"verify: GET /health -> {status}: {health}")
        return 1
    print("GET /health: OK")

    status, data = cluster_api.api_request(config, "GET", "/v1/imageVersions")
    if status != 200:
        print(f"verify: GET /v1/imageVersions -> {status}: {data}")
        return 1

    target = (config.get("target_image") or {}).get("ontap_version", "")
    versions = image_index(data)
    current = data.get("current", "") if isinstance(data, dict) else ""

    if not target:
        notes.append("no target ONTAP version given, so image checks were skipped")
    elif target not in versions:
        failures.append(f"ONTAP {target} is not registered in /v1/imageVersions")
    else:
        entry = versions[target]
        if not entry.get("isActive", False):
            failures.append(f"ONTAP {target} is registered but isActive=false")
        else:
            print(f"image {target}: registered, active (vsaName={entry.get('vsaName')})")
        if current and current != target:
            notes.append(f"default version is {current}, not the target {target}")

        deployment_name = (config.get("deployment") or {}).get("vsa_image_name", "")
        if deployment_name and entry.get("vsaName") != deployment_name:
            notes.append(
                f"deployment.vsa_image_name ({deployment_name}) != "
                f"registered vsaName ({entry.get('vsaName')})"
            )

    progress_id = (params.get("progress_id") or "").strip()
    if progress_id:
        kind, progress = _fetch_progress(config, progress_id, params.get("progress_kind") or "auto")
        if progress is None:
            failures.append(f"no upgrade progress found for id {progress_id}")
        else:
            cluster_api.print_status(f"{kind.upper()} UPGRADE PROGRESS", progress)
            job_status = progress.get("status", "UNKNOWN")
            counts = cluster_api.cluster_counts(progress)
            failed = sum(counts.get(s, 0) for s in cluster_api.FAILED_STATUSES)
            if job_status != "COMPLETED" or failed:
                failures.append(
                    f"{kind} upgrade {progress_id} is {job_status}"
                    + (f" with {failed} failed cluster(s)" if failed else "")
                )
                failures += cluster_api.collect_failure_details(progress)
            else:
                print(f"{kind} upgrade {progress_id}: COMPLETED ({counts or 'no child jobs'})")
    else:
        notes.append("no progress_id given, so no upgrade job was confirmed")

    # UpgradeProgress_v1 carries clusterId/status/currentStep only, so per-pool
    # ONTAP version confirmation stays a manual ONTAP-side check.
    notes.append("per-pool ONTAP version is not exposed by core-api; confirm on the cluster if required")

    summary = ["### verify", ""]
    summary += [f"- FAIL: {line}" for line in failures] or ["- All checks passed"]
    summary += [f"- note: {line}" for line in notes] + [""]
    cluster_api.github_summary(summary)

    for line in notes:
        print(f"note: {line}")
    if failures:
        for line in failures:
            print(f"FAIL: {line}")
            cluster_api.github_error(f"verify: {line}")
        return 1
    print("\nverify: all checks passed")
    return 0


def _fetch_progress(
    config: dict[str, Any], progress_id: str, kind: str
) -> tuple[str, dict[str, Any] | None]:
    """Read batch or single-cluster progress, probing both when kind is auto."""
    paths = {
        "batch": f"/v1/clusters/batchUpgrade/{progress_id}",
        "pool": f"/v1/clusters/upgrade/{progress_id}",
    }
    order = ["batch", "pool"] if kind == "auto" else [kind]
    for candidate in order:
        status, data = cluster_api.api_request(config, "GET", paths[candidate])
        if status == 200 and isinstance(data, dict):
            return candidate, data
        print(f"GET {paths[candidate]} -> {status}")
    return kind, None


@operations.register(
    "discover",
    "SQL pool discovery against VCP Postgres (requires psql and a database config)",
    SAFETY_READ,
    params=(
        Param("ontap_version_prefix", "Source ONTAP prefix, e.g. 9.17.1", default="9.17.1"),
        Param("account_id", "Restrict to one account id", kind="int"),
        Param("deployment_name", "Restrict to one deployment name"),
    ),
)
def op_discover(config: dict[str, Any], params: Params) -> int:
    db = config.get("database")
    if not db:
        print("discover requires a database section in the environment config")
        return 1

    prefix = str(params.get("ontap_version_prefix") or "").replace("'", "''")
    account_filter = ""
    if params.get("account_id"):
        account_filter = f" AND account_id = {int(params['account_id'])}"
    deployment_filter = ""
    if params.get("deployment_name"):
        dep = str(params["deployment_name"]).replace("'", "''")
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
    print("Running: psql ... -c <query>")
    try:
        result = subprocess.run(cmd, capture_output=True, text=True, check=True, env=env)
    except subprocess.CalledProcessError as exc:
        print(exc.stderr or exc.stdout)
        return 1
    except FileNotFoundError:
        print("psql not found — install the PostgreSQL client or run the SQL manually (see README)")
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


@operations.register(
    "upgrade",
    "POST /v1/clusters/batchUpgrade: upgrade selected pools and poll to terminal status",
    SAFETY_MUTATE,
    params=(
        Param("pool_ids", "Explicit pool UUIDs (poolList selection)", kind="list"),
        Param("percentage", "Percentage of eligible pools (percentage selection)", kind="int"),
        Param("max_clusters", "Hard cap on selected pools; 0 means the config cap", kind="int", default=0),
        Param("from_ontap", "Source ONTAP versions, exact match (API filters with SQL IN, not prefix)"),
        Param("max_concurrency", "Pools upgraded in parallel", kind="int"),
        Param("failure_threshold", "Failed pools tolerated before the batch aborts", kind="int"),
        Param(
            "force_upgrade",
            "Pin the target build images instead of the deployment default",
            kind="bool",
            default=False,
        ),
        Param("timeout_minutes", "Poll budget; 0 means the config budget", kind="int", default=0),
    ),
)
def op_upgrade(config: dict[str, Any], params: Params) -> int:
    batch_cfg = config.get("batch") or {}
    pool_ids = split_list(params.get("pool_ids")) or split_list(config.get("pool_ids"))
    percentage = params.get("percentage")

    if pool_ids and percentage:
        print("pass either pool_ids or percentage, not both")
        return 1
    if not pool_ids and not percentage:
        print("upgrade needs a selection: pool_ids (poolList) or percentage")
        return 1

    body: dict[str, Any] = {
        "maxConcurrency": params.get("max_concurrency") or batch_cfg.get("max_concurrency", 5),
        "failureThreshold": (
            params["failure_threshold"]
            if params.get("failure_threshold") is not None
            else batch_cfg.get("failure_threshold", 1)
        ),
    }

    if pool_ids:
        body["selectionType"] = "poolList"
        body["poolIds"] = pool_ids
    else:
        body["selectionType"] = "percentage"
        if not 1 <= int(percentage) <= 100:
            print(f"percentage must be between 1 and 100, got {percentage}")
            return 1
        body["percentage"] = int(percentage)
        # The API matches build_info->>'ontapVersion' with SQL IN, not LIKE, so
        # every source version must be listed exactly (9.17.1P1, not 9.17.1).
        versions = split_list(params.get("from_ontap"))
        if versions:
            body["filters"] = {"ontapVersions": versions}

    cap = int(batch_cfg.get("max_clusters_cap") or 0)
    requested_cap = int(params.get("max_clusters") or 0)
    limit = min([value for value in (cap, requested_cap) if value > 0], default=0)
    if limit:
        body["maxClusters"] = limit
        if requested_cap and cap and requested_cap > cap:
            print(f"max_clusters {requested_cap} exceeds the config cap {cap}; using {cap}")

    if params.get("force_upgrade"):
        try:
            target = cluster_api.require_target_image(config, ("vsa_name", "mediator_name"))
        except ValueError as exc:
            print(exc)
            return 1
        body.update(
            {
                "vsaBuildImage": target["vsa_name"],
                "mediatorBuildImage": target["mediator_name"],
                "forceUpgrade": True,
            }
        )

    metadata = config.get("change_metadata")
    if metadata:
        body["metadata"] = metadata

    result = submit(
        config, "POST", "/v1/clusters/batchUpgrade", body, dry_run=bool(params.get("dry_run"))
    )
    if result is None:
        return 0
    status, data = result
    if status not in (200, 202):
        print(f"upgrade failed: {status}: {data}")
        cluster_api.github_error(f"upgrade rejected: {status}: {data}")
        return 1

    print(json.dumps(data, indent=2))
    batch_id = data.get("batchUpgradeId") or data.get("jobId")
    if not batch_id:
        print("upgrade accepted but no batchUpgradeId returned; cannot poll progress")
        cluster_api.print_status("EXECUTION STATUS", data)
        return 1

    total = data.get("totalClusters", "?")
    print(f"\nbatch upgrade id: {batch_id} (totalClusters={total})")
    cluster_api.github_summary(
        [
            "### upgrade accepted",
            "",
            f"- Batch id: `{batch_id}`",
            f"- Selection: `{body['selectionType']}`, totalClusters={total}",
            f"- maxConcurrency={body['maxConcurrency']}, failureThreshold={body['failureThreshold']}",
            "",
        ]
    )

    poll = poll_settings(config, params, default_interval=120)
    return cluster_api.poll_progress(
        config,
        f"/v1/clusters/batchUpgrade/{batch_id}",
        batch_id,
        "batch upgrade",
        interval=poll["interval"],
        timeout=poll["timeout"],
    )


@operations.register(
    "upgrade-cluster",
    "POST /v1/clusters/{poolId}/upgrade: upgrade a single cluster and poll to terminal status",
    SAFETY_MUTATE,
    params=(
        Param("pool_id", "Pool UUID to upgrade", required=True),
        Param(
            "force_upgrade",
            "Pin the target build images instead of the deployment default",
            kind="bool",
            default=False,
        ),
        Param("timeout_minutes", "Poll budget; 0 means the config budget", kind="int", default=0),
    ),
)
def op_upgrade_cluster(config: dict[str, Any], params: Params) -> int:
    body: dict[str, Any] = {}
    if params.get("force_upgrade"):
        try:
            target = cluster_api.require_target_image(config, ("vsa_name", "mediator_name"))
        except ValueError as exc:
            print(exc)
            return 1
        body = {
            "vsaBuildImage": target["vsa_name"],
            "mediatorBuildImage": target["mediator_name"],
            "forceUpgrade": True,
        }

    pool_id = params["pool_id"]
    result = submit(
        config, "POST", f"/v1/clusters/{pool_id}/upgrade", body, dry_run=bool(params.get("dry_run"))
    )
    if result is None:
        return 0
    status, data = result
    if status not in (200, 202):
        print(f"upgrade-cluster failed: {status}: {data}")
        cluster_api.github_error(f"upgrade-cluster rejected: {status}: {data}")
        return 1

    print(json.dumps(data, indent=2))
    job_id = data.get("jobId")
    if not job_id:
        print("upgrade accepted but no jobId returned; cannot poll progress")
        cluster_api.print_status("EXECUTION STATUS", data)
        return 1

    print(f"\npool upgrade job id: {job_id}")
    poll = poll_settings(config, params, default_interval=60)
    return cluster_api.poll_progress(
        config,
        f"/v1/clusters/upgrade/{job_id}",
        job_id,
        "pool upgrade",
        interval=poll["interval"],
        timeout=poll["timeout"],
    )
