# VCP Volume Deletion Runbook

## Purpose

Use this runbook to delete a VCP-managed volume, including a volume in `ERROR`,
when the operator does not have access to the customer project.

VCP core-api does not support volume deletion. Use the Google Proxy endpoint:

```text
DELETE /v1beta/projects/{projectNumber}/locations/{locationId}/volumes/{volumeId}
```

Google Proxy creates the database job, changes the volume to `DELETING`, and
submits `DeleteVolumeWorkflow` through the per-pool Temporal sequencer. Do not
start the workflow directly or delete the database row manually.

## Safety

- Obtain the required incident/change approval before deleting customer data.
- Have a second operator verify the project number, location, volume UUID, and
  volume name.
- Never put a service-account key, JWT, or ONTAP password in source control,
  tickets, chat, or shell history.
- Use a newly issued key. Revoke any key that has been shared through an
  insecure channel.
- Database commands in this runbook are read-only.

## Prerequisites

- Access to Google Cloud Shell.
- Access to the regional VCP jump host and Kubernetes cluster.
- Access to the VCP database through the SRE-provided `connect.sh`.
- An approved service account with permission to call
  `iam.serviceAccounts.signJwt`.
- Secret Manager access in the regional SDE project for ONTAP verification.
- `curl`, `jq`, Python 3, `google-auth`, and `google-api-python-client`.

Set these values:

```bash
export CUSTOMER_PROJECT_NUMBER="<customer-project-number>"
export LOCATION="<region-or-zone>"
export VOLUME_UUID="<volume-uuid>"
export SUPPORT_SA_EMAIL="<approved-service-account-email>"
export VCP_BASE_URL="https://vcp.<region>-<environment>.internal.gw.gnf.netapp.com"
export CORRELATION_ID="support-volume-delete-<ticket-id>"
```

## 1. Add `key.json` to Cloud Shell

Create/download a JSON key for the approved service account using the standard
GCP Console procedure. Upload it to Cloud Shell as `$HOME/key.json`.

```bash
chmod 600 "${HOME}/key.json"
```

Do not use a key copied from a ticket or chat. Prefer short-lived
impersonation when available; this runbook documents the current SRE
break-glass key procedure.

## 2. Create the token-generation script

Create `$HOME/script.py` in Cloud Shell with the following sanitized version of
the SRE script:

```python
import json
import os
import time

from google.oauth2 import service_account
from googleapiclient import discovery


key_file = os.path.expanduser("~/key.json")
client_email = os.environ["SUPPORT_SA_EMAIL"]
project_number = os.environ["CUSTOMER_PROJECT_NUMBER"]
audience = os.environ.get(
    "VCP_AUDIENCE",
    "https://cloudvolumesgcp-api.netapp.com",
)

credentials = service_account.Credentials.from_service_account_file(key_file)
if credentials.service_account_email != client_email:
    raise RuntimeError("SUPPORT_SA_EMAIL does not match key.json")

iam = discovery.build("iam", "v1", credentials=credentials)
issued_at = int(time.time())
payload = json.dumps(
    {
        "sub": client_email,
        "aud": audience,
        "iss": client_email,
        "iat": issued_at,
        "exp": issued_at + 2700,
        "Google": {"project_number": int(project_number)},
    },
    separators=(",", ":"),
)

request = iam.projects().serviceAccounts().signJwt(
    name=f"projects/-/serviceAccounts/{client_email}",
    body={"payload": payload},
)
print(request.execute()["signedJwt"])
```

The customer project number is embedded in the JWT and must match the project
number in the request URL. The issuer must be the service account that signed
the JWT.

## 3. Generate the access token

In the same Cloud Shell session:

```bash
export ACCESS_TOKEN="$(python "${HOME}/script.py")"
test -n "${ACCESS_TOKEN}"
```

The token lifetime is 45 minutes. Regenerate it if it expires during the
procedure.

Google Proxy expects the signed JWT itself as the complete `Authorization`
header. Use:

```text
Authorization: ${ACCESS_TOKEN}
```

Do **not** use `Authorization: Bearer ${ACCESS_TOKEN}`.

## 4. Look up the VCP volume

Connect to the regional jump host and run the environment-specific SRE
`connect.sh` procedure:

```bash
ssh <regional-jump-host>
cd <directory-containing-connect.sh>
./connect.sh
```

In `psql`, obtain and verify the volume:

```sql
SELECT
    v.uuid,
    v.name,
    v.state,
    v.state_details,
    a.name AS customer_project_number,
    p.uuid AS pool_uuid,
    p.name AS pool_name,
    p.vendor_id,
    p.pool_attributes->>'primary_zone' AS primary_zone,
    p.pool_attributes->>'secondary_zone' AS secondary_zone
FROM volumes v
JOIN accounts a ON a.id = v.account_id
LEFT JOIN pools p ON p.id = v.pool_id
WHERE v.uuid = '<volume-uuid>';
```

Confirm that the UUID and name identify the intended customer volume, the
customer project number matches the JWT and URL, and `LOCATION` matches the
pool's region or zone.

Capture the ONTAP connection information for final verification:

```sql
SELECT name, endpoint_address, host_dns_name
FROM nodes
WHERE pool_id = (
    SELECT pool_id FROM volumes WHERE uuid = '<volume-uuid>'
)
ORDER BY name;

SELECT
    pool_credentials->>'username' AS ontap_username,
    pool_credentials->>'auth_type' AS ontap_auth_type,
    pool_credentials->>'secret_id' AS ontap_password_secret_id
FROM pools
WHERE id = (
    SELECT pool_id FROM volumes WHERE uuid = '<volume-uuid>'
);
```

The current VCP field is `pools.pool_credentials`; older SRE instructions may
call it `connection_details`. Never select or print an inline password.

## 5. Test access with GET

Transfer only the generated JWT to the jump host using the approved secure
clipboard/channel. Do not pass it as a command argument or save it to a file.
On the jump host, capture it without echoing it or recording it in shell
history:

```bash
read -r -s -p "Access token: " ACCESS_TOKEN
printf '\n'
export ACCESS_TOKEN
```

Also export `CUSTOMER_PROJECT_NUMBER`, `LOCATION`, `VOLUME_UUID`,
`VCP_BASE_URL`, and `CORRELATION_ID` on the jump host. Then make the
non-mutating GET request:

```bash
curl -sS \
  "${VCP_BASE_URL}/v1beta/projects/${CUSTOMER_PROJECT_NUMBER}/locations/${LOCATION}/volumes/${VOLUME_UUID}" \
  --header "Authorization: ${ACCESS_TOKEN}" \
  --header "x-correlation-id: ${CORRELATION_ID}" \
  --header "Content-Type: application/json" |
  jq .
```

If GET succeeds, skip step 6. The service account is already accepted and no
restart is needed.

If GET returns `401`, verify the project number, JWT audience, issuer, key, and
token age first. Only then continue to step 6.

## 6. Conditionally allowlist the service account

This step is needed only when the valid token from step 5 is rejected because
the issuer is not allowlisted.

Check both rendered ConfigMaps:

```bash
kubectl -n vcp get configmap core-service-env-config \
  -o jsonpath='{.data.GCP_AUTH_ACCEPTED_SERVICE_ACCOUNTS}{"\n"}'

kubectl -n vcp get configmap google-proxy-service-env-config \
  -o jsonpath='{.data.GCP_AUTH_ACCEPTED_SERVICE_ACCOUNTS}{"\n"}'
```

Add `SUPPORT_SA_EMAIL` to the deployment's
`gcpAuthAcceptedServiceAccounts`/`acceptedSAs` configuration through the normal
regional deployment process. Do not make an untracked live ConfigMap edit.

After deployment, verify the rendered value. Restart core and Google Proxy only
if the deployment did not roll their pods automatically:

```bash
kubectl -n vcp rollout restart deployment/core-service
kubectl -n vcp rollout restart deployment/google-proxy
kubectl -n vcp rollout status deployment/core-service
kubectl -n vcp rollout status deployment/google-proxy
```

Deployment names can differ by environment; confirm them before running the
restart commands. Regenerate `ACCESS_TOKEN`, then repeat the GET request in
step 5. Do not proceed until GET returns the expected volume.

## 7. Check delete prerequisites

A volume in `ERROR` can be submitted for deletion. The API can reject deletion
when:

- a backup create/delete operation is active;
- volume replication exists;
- a clone split is active;
- the volume is in another transitional state;
- pool zone switching is active; or
- delete protection detects connected NFS clients, an SMB restricted action,
  or a SAN host-group attachment.

Resolve any reported prerequisite. Do not bypass it with SQL or direct Temporal
execution.

## 8. Delete the volume

```bash
response_file="$(mktemp)"
http_code="$(
  curl --silent --show-error \
    --output "${response_file}" \
    --write-out '%{http_code}' \
    --request DELETE \
    "${VCP_BASE_URL}/v1beta/projects/${CUSTOMER_PROJECT_NUMBER}/locations/${LOCATION}/volumes/${VOLUME_UUID}" \
    --header "Authorization: ${ACCESS_TOKEN}" \
    --header "x-correlation-id: ${CORRELATION_ID}" \
    --header "Content-Type: application/json"
)"

printf 'HTTP %s\n' "${http_code}"
if [[ -s "${response_file}" ]]; then
  jq . "${response_file}"
else
  printf 'No response body\n'
fi
```

Expected responses:

- `202`: deletion started. Record the Operation name/job UUID.
- `204`: deletion is already complete. Continue with verification.
- `400` or `409`: resolve the prerequisite reported in the response.
- `401`: repeat steps 3, 5, and, if necessary, 6.
- `500`: stop and inspect Google Proxy and worker logs using
  `CORRELATION_ID`; do not retry blindly.

## 9. Monitor the operation

For a `202` response:

```bash
export OPERATION_NAME="$(jq -r '.name // empty' "${response_file}")"
export OPERATION_ID="${OPERATION_NAME##*/}"
test -n "${OPERATION_ID}"

curl --silent --show-error \
  "${VCP_BASE_URL}/v1beta/projects/${CUSTOMER_PROJECT_NUMBER}/locations/${LOCATION}/operations/${OPERATION_ID}" \
  --header "Authorization: ${ACCESS_TOKEN}" \
  --header "x-correlation-id: ${CORRELATION_ID}" |
  jq .
```

The operation is successful only when it completes without an error.

Verify in the VCP database:

```sql
SELECT uuid, type, state, tracking_id, error_details, workflow_id, created_at, updated_at
FROM jobs
WHERE uuid = '<operation-id>' OR workflow_id = '<operation-id>';

SELECT uuid, name, state, state_details, deleted_at
FROM volumes
WHERE uuid = '<volume-uuid>';
```

## 10. Verify ONTAP cleanup

Use the node address and credential reference from step 4. For
password-authenticated pools, retrieve the password from Secret Manager in the
regional SDE project:

```bash
export REGIONAL_SDE_PROJECT="<regional-sde-project>"
export ONTAP_HOST="<node-endpoint-address>"
export ONTAP_USERNAME="<username-from-pool_credentials>"
export ONTAP_PASSWORD_SECRET_ID="<secret-id-from-pool_credentials>"

gcloud secrets versions access latest \
  --project="${REGIONAL_SDE_PROJECT}" \
  --secret="${ONTAP_PASSWORD_SECRET_ID}"
```

Do not store or echo the password again. For certificate-authenticated pools,
use the approved certificate login procedure.

Connect to ONTAP:

```bash
ssh "${ONTAP_USERNAME}@${ONTAP_HOST}"
```

Confirm the volume name no longer exists:

```text
volume show -volume <volume-name>
```

ONTAP absence alone is not sufficient. Also confirm that the VCP operation/job
completed and the volume row is deleted.

## 11. Clean up credentials

```bash
unset ACCESS_TOKEN
rm -f "${response_file}" "${HOME}/script.py" "${HOME}/key.json"
```

Delete/disable the service-account key in GCP IAM. If the allowlist entry was
temporary, remove it through the normal regional deployment process.

## Escalation

Stop and escalate with the correlation ID, operation ID, job record, and exact
error when:

- the delete prerequisite cannot be safely resolved;
- the workflow ends in `ERROR`;
- ONTAP cleanup succeeds but VCP remains undeleted;
- VCP reports deletion but the ONTAP volume remains; or
- pool or credential metadata is missing.

Do not directly start `DeleteVolumeWorkflow`, edit the volume state, or delete
database rows as a substitute for this procedure.
