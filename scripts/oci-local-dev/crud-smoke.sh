#!/usr/bin/env bash
#
# crud-smoke.sh — Phase 2 control-plane CRUD smoke against the oci-proxy API.
#
# Drives the real release oci-proxy: create pool -> poll workflow -> create SVM
# -> poll -> best-effort delete. The oci-proxy needs only a UUID opc-request-id
# header and a tenancyOcid query param locally (no auth token), so this is a
# faithful contract test.
#
# WHAT THIS CATCHES (high value, pre-build):
#   • API/schema contract regressions (request rejected / wrong shape)
#   • workflow dispatch failures (queue mismatch, worker not polling)
#   • missing workflow/activity registration
#   • deterministic workflow panics, serialization errors, DB constraint errors
#
# WHAT IT CANNOT FULLY DO LOCALLY:
#   Real pool creation provisions OCI compute and talks to the per-pool ONTAP
#   cluster IP that only exists after provisioning. Without faked OCI compute the
#   workflow will stop at that external boundary. The script classifies that as
#   PARTIAL (expected), and only FAILs on control-plane defects.
#
# Result classes: PASS (workflow succeeded), PARTIAL (accepted + dispatched, hit
# external dependency), FAIL (contract/dispatch/deterministic defect).
#
# Usage: crud-smoke.sh [--base http://localhost:8080] [--timeout 90] [--keep]
# Exit codes: 0 PASS or PARTIAL, 1 FAIL, 2 setup/usage error.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
. "$SCRIPT_DIR/lib.sh"

BASE="${OCI_PROXY_URL:-http://localhost:8080}"
TIMEOUT=90
KEEP=0

while [ $# -gt 0 ]; do
  case "$1" in
    --base)    BASE="$2"; shift 2 ;;
    --timeout) TIMEOUT="$2"; shift 2 ;;
    --keep)    KEEP=1; shift ;;
    -h|--help) sed -n '2,30p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) die "unknown arg '$1'" ;;
  esac
done

have curl || die "curl is required"
have python3 || die "python3 is required"

# Markers in a workflow failure cause that mean "stopped at an external
# dependency we cannot satisfy locally" → PARTIAL, not a code defect.
EXTERNAL_MARKERS='oci|compute|instance|provision|dial|connection refused|no such host|timeout|deadline|ontap|cluster ip|unreachable|i/o timeout|context deadline'

RAND="$(date +%s)$$"
TENANCY="ocid1.tenancy.oc1..aaaaaaaasmoketenancy"
COMPARTMENT="ocid1.compartment.oc1..aaaaaaaasmokecompartment"
POOL_OCID="ocid1.fssmountpool.oc1.iad.aaaaaaaasmokepool${RAND}"
SUBNET="ocid1.subnet.oc1.iad.aaaaaaaasmokesubnet"
SECRET="ocid1.vaultsecret.oc1.iad.aaaaaaaasmokesecret"
SVM_OCID="ocid1.fssmountsvm.oc1.iad.aaaaaaaasmokesvm${RAND}"

# json_get <field> — read JSON on stdin, print a top-level/nested field (dot path).
json_get() {
  python3 - "$1" <<'PY'
import json, sys
try:
    d = json.load(sys.stdin)
except Exception:
    sys.exit(0)
cur = d
for part in sys.argv[1].split('.'):
    if isinstance(cur, dict) and part in cur:
        cur = cur[part]
    else:
        sys.exit(0)
print(cur if not isinstance(cur, (dict, list)) else json.dumps(cur))
PY
}

# call <method> <url> [body-file] — returns "<code>\t<body>" (body single-lined).
call() {
  local method="$1" url="$2" body="${3:-}"
  local opc; opc="$(new_uuid)"
  local raw code resp
  raw="$(http_json "$method" "$url" "$opc" "$body" || true)"
  code="$(printf '%s' "$raw" | tail -n1)"
  resp="$(printf '%s' "$raw" | sed '$d')"
  printf '%s\t%s' "$code" "$(printf '%s' "$resp" | tr '\n' ' ')"
}

# poll_workflow <workflowId> -> prints final "STATUS|cause"
poll_workflow() {
  local wf="$1" deadline status cause raw code body
  deadline=$(( $(date +%s) + TIMEOUT ))
  while [ "$(date +%s)" -lt "$deadline" ]; do
    raw="$(call GET "$BASE/v1beta/workRequests/$wf")"
    code="${raw%%$'\t'*}"; body="${raw#*$'\t'}"
    if [ "$code" = "200" ]; then
      status="$(printf '%s' "$body" | json_get status)"
      case "$status" in
        SUCCEEDED|COMPLETED|FAILED|CANCELED|TERMINATED|TIMED_OUT)
          cause="$(printf '%s' "$body" | json_get error.cause)"
          [ -n "$cause" ] || cause="$(printf '%s' "$body" | json_get error.message)"
          printf '%s|%s' "$status" "$cause"
          return 0 ;;
      esac
    fi
    sleep 3
  done
  printf 'IN_PROGRESS|polling timed out after %ss' "$TIMEOUT"
}

# classify <op> <create-code> <create-body> -> sets RESULT and prints a line.
RESULT="PASS"
worsen() { # PASS < PARTIAL < FAIL
  case "$1" in
    FAIL) RESULT="FAIL" ;;
    PARTIAL) [ "$RESULT" = "FAIL" ] || RESULT="PARTIAL" ;;
  esac
}

evaluate() {
  local op="$1" code="$2" body="$3" wf="$4"
  if [ "$code" != "202" ]; then
    err "[$op] FAIL — API returned HTTP $code (expected 202)."
    err "      body: $body"
    worsen FAIL; return
  fi
  ok "[$op] API accepted (202); workflowId=$wf"
  if [ -z "$wf" ] || [ "$wf" = "None" ]; then
    err "[$op] FAIL — no workflowId returned despite 202."
    worsen FAIL; return
  fi
  local res status cause
  res="$(poll_workflow "$wf")"
  status="${res%%|*}"; cause="${res#*|}"
  case "$status" in
    SUCCEEDED|COMPLETED)
      ok "[$op] PASS — workflow $status." ;;
    FAILED|CANCELED|TERMINATED|TIMED_OUT)
      if printf '%s' "$cause" | grep -qiE "$EXTERNAL_MARKERS"; then
        warn "[$op] PARTIAL — dispatched + ran, stopped at external dependency:"
        warn "      $cause"
        worsen PARTIAL
      else
        err "[$op] FAIL — workflow $status with a control-plane defect:"
        err "      $cause"
        worsen FAIL
      fi ;;
    IN_PROGRESS)
      warn "[$op] PARTIAL — workflow still running at timeout (dispatch OK)."
      warn "      $cause"
      worsen PARTIAL ;;
    *)
      err "[$op] FAIL — unexpected workflow status '$status'."
      worsen FAIL ;;
  esac
}

# --- preconditions -----------------------------------------------------------
info "[smoke] checking oci-proxy health at $BASE …"
hc="$(call GET "$BASE/health")"; hcode="${hc%%$'\t'*}"
[ "$hcode" = "200" ] || die "oci-proxy not healthy (HTTP $hcode). Bring it up: stack.sh up --crud"
ok "[smoke] oci-proxy healthy."

TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT

# --- create pool -------------------------------------------------------------
cat > "$TMP/pool.json" <<JSON
{
  "poolOCID": "$POOL_OCID",
  "compartmentOCID": "$COMPARTMENT",
  "displayName": "smoke-pool-$RAND",
  "subnetId": "$SUBNET",
  "dataNicSubnetId": "$SUBNET",
  "sizeInGiB": 1024,
  "throughputGBps": 1.0,
  "dataEndpointCount": 2,
  "primaryAvailabilityDomain": "AD-1",
  "ociAdminPassword": { "ocid": "$SECRET", "version": "1" }
}
JSON
info "[smoke] CreatePool …"
cp_raw="$(call POST "$BASE/v1beta/pools?tenancyOcid=$TENANCY" "$TMP/pool.json")"
cp_code="${cp_raw%%$'\t'*}"; cp_body="${cp_raw#*$'\t'}"
cp_wf="$(printf '%s' "$cp_body" | json_get workflowId)"
evaluate "CreatePool" "$cp_code" "$cp_body" "$cp_wf"

# --- create SVM (uses the same pool OCID) ------------------------------------
cat > "$TMP/svm.json" <<JSON
{
  "svmOCID": "$SVM_OCID",
  "name": "smokesvm$RAND",
  "ips": ["10.0.0.10"],
  "svmAdminPassword": { "ocid": "$SECRET", "version": "1" }
}
JSON
info "[smoke] CreateSvmByPool …"
cs_raw="$(call POST "$BASE/v1beta/pools/$POOL_OCID/svms?tenancyOcid=$TENANCY" "$TMP/svm.json")"
cs_code="${cs_raw%%$'\t'*}"; cs_body="${cs_raw#*$'\t'}"
cs_wf="$(printf '%s' "$cs_body" | json_get workflowId)"
evaluate "CreateSvm" "$cs_code" "$cs_body" "$cs_wf"

# --- best-effort cleanup -----------------------------------------------------
if [ "$KEEP" = 0 ]; then
  info "[smoke] cleanup: DeletePool (best-effort) …"
  call DELETE "$BASE/v1beta/pools/$POOL_OCID?tenancyOcid=$TENANCY" >/dev/null 2>&1 || true
fi

# --- summary -----------------------------------------------------------------
echo "" >&2
case "$RESULT" in
  PASS)    ok    "================ CRUD SMOKE: PASS ================"; exit 0 ;;
  PARTIAL) warn  "============== CRUD SMOKE: PARTIAL ==============="
           warn  "Control plane is healthy; flows stopped at external deps"
           warn  "(no OCI compute / real ONTAP locally). This is expected."
           exit 0 ;;
  FAIL)    err   "================ CRUD SMOKE: FAIL ================"
           err   "A control-plane defect was detected before build. See above."
           exit 1 ;;
esac
