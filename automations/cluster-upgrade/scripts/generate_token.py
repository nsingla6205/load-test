#!/usr/bin/env python3
"""Generate a signed JWT for VCP core-api using GCP SA impersonation."""

from __future__ import annotations

import json
import time
from typing import Any

from google.auth import default
from google.auth import impersonated_credentials
from googleapiclient import discovery


# core-api has been observed rejecting tokens after ~40-45 min. Use a 45 min
# lifetime so the caller's 15 min refresh margin replaces them after 30 min.
DEFAULT_TOKEN_LIFETIME_SECONDS = 2700


def generate_token(
    auth: dict[str, Any],
    lifetime_seconds: int = DEFAULT_TOKEN_LIFETIME_SECONDS,
) -> str:
    """Return a signed JWT for core-api Authorization header.

    Impersonates auth['impersonate_service_account'] (the regional SA from
    config.projects.<id>.service_account) and calls iam.serviceAccounts.signJwt.
    """
    source_credentials, _ = default()
    target_scopes = ["https://www.googleapis.com/auth/cloud-platform"]
    credentials = impersonated_credentials.Credentials(
        source_credentials=source_credentials,
        target_principal=auth["impersonate_service_account"],
        target_scopes=target_scopes,
    )

    iam = discovery.build("iam", "v1", credentials=credentials)
    client_email = auth["client_email"]
    audience = auth["audience"]
    project_number = auth["project_number"]
    iat = int(time.time())

    payload = json.dumps(
        {
            "sub": client_email,
            "aud": audience,
            "iss": client_email,
            "iat": iat,
            "exp": iat + lifetime_seconds,
            "Google": {"project_number": int(project_number)},
        },
        separators=(",", ":"),
    )

    req = iam.projects().serviceAccounts().signJwt(
        name=f"projects/-/serviceAccounts/{auth['impersonate_service_account']}",
        body={"payload": payload},
    )
    return req.execute()["signedJwt"]


if __name__ == "__main__":
    import argparse
    import sys

    parser = argparse.ArgumentParser(description="Generate VCP core-api JWT")
    parser.add_argument("--impersonate-sa", required=True)
    parser.add_argument("--client-email", required=True)
    parser.add_argument("--audience", default="https://cloudvolumesgcp-api.netapp.com")
    parser.add_argument("--project-number", required=True)
    args = parser.parse_args()

    auth = {
        "impersonate_service_account": args.impersonate_sa,
        "client_email": args.client_email,
        "audience": args.audience,
        "project_number": args.project_number,
    }
    try:
        print(generate_token(auth))
    except Exception as exc:  # noqa: BLE001 — CLI entrypoint
        print(f"error: {exc}", file=sys.stderr)
        sys.exit(1)
