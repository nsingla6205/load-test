#!/usr/bin/env bash
# SPDX-License-Identifier: UPL-1.0
# Mirror ReleaseManifest + binaries + selected Helm OCI charts (helm pull; name contains "oci") + Dockerfiles to OCI Object Storage.
# Integrity: oras resolve digest vs manifest + .sha256 sidecar (must match manifest) + on-disk blob hash + size (no Cosign).
# Layout: vcp-releases/<ver>/artifacts/<binary>/ holds <binary> blob and ORAS sidecars (*.sha256, *.sig, …); no SHA256SUMS file.
#         and Dockerfiles matched by sourcePath dirname / Dockerfile.<suffix> / *-<dirname> (unique).
# Ref: doc/architecture/designs/ghcr-to-oci-object-storage-mirror/DESIGN.md

set -euo pipefail

: "${MANIFEST_VERSION:?}"
: "${MANIFEST_IMAGE:?}"
: "${GITHUB_WORKSPACE:?}"

# Root of repo checkout that matches release tag paths (e.g. .../release-ref). Defaults to GITHUB_WORKSPACE.
DOCKERFILE_REPO_ROOT="${DOCKERFILE_REPO_ROOT:-${GITHUB_WORKSPACE}}"

MANIFEST_REF="${MANIFEST_IMAGE}:${MANIFEST_VERSION}"
PREFIX="vcp-releases/${MANIFEST_VERSION}"
WORKDIR="${RUNNER_TEMP:-/tmp}/mirror-${MANIFEST_VERSION}"
mkdir -p "${WORKDIR}"

command -v jq >/dev/null || { echo "jq required" >&2; exit 1; }
command -v oras >/dev/null || { echo "oras required" >&2; exit 1; }
command -v helm >/dev/null || { echo "helm required" >&2; exit 1; }
command -v oci >/dev/null || { echo "oci CLI required" >&2; exit 1; }

NS="$(oci os ns get --auth security_token --query data --raw-output)"
BUCKET="${OCI_BUCKET_NAME:?}"
UPLOAD_COUNT=0

log_section() {
  local title="$1"
  echo ""
  echo "=================================================================="
  echo "== ${title}"
  echo "=================================================================="
}

log_step() {
  local msg="$1"
  echo "--> ${msg}"
}

put_file() {
  local local_path="$1"
  local object_name="$2"
  UPLOAD_COUNT=$((UPLOAD_COUNT + 1))
  log_step "Uploading [${UPLOAD_COUNT}] ${object_name}"
  oci os object put \
    --auth security_token \
    --namespace-name "$NS" \
    --bucket-name "$BUCKET" \
    --name "$object_name" \
    --file "$local_path" \
    --force
}

copy_vsa_release_folder() {
  local manifest_path="$1"
  local ontap_version ontap_bucket_version src_profile src_ns src_bucket src_root src_prefix dst_prefix
  local list_json list_count tmp_dir

  log_section "Step 3b: VSA/VLM source copy"
  ontap_version="$(
    jq -r '
      .vsaArtifacts.ontapVersion
      // (
        .vsaArtifacts.versions // []
        | map(.ontapVersion // empty)
        | map(select(length > 0))
        | first
      )
      // empty
    ' "${manifest_path}"
  )"
  if [[ -z "${ontap_version}" ]]; then
    log_step "No VSA ONTAP version in manifest (expected vsaArtifacts.ontapVersion or vsaArtifacts.versions[].ontapVersion); skipping VSA/VLM source copy."
    return 0
  fi

  src_profile="${VSA_SOURCE_PROFILE:-}"
  src_ns="${VSA_SOURCE_NAMESPACE:-}"
  src_bucket="${VSA_SOURCE_BUCKET_NAME:-}"
  src_root="${VSA_SOURCE_PREFIX_ROOT:-releases}"
  src_root="${src_root%/}"
  ontap_bucket_version="${VSA_SOURCE_ONTAP_FOLDER_OVERRIDE:-}"
  if [[ -z "${ontap_bucket_version}" ]]; then
    ontap_bucket_version="${ontap_version}"
  fi
  src_prefix="${src_root}/${ontap_bucket_version}/"
  dst_prefix="${PREFIX}/VSA/${ontap_bucket_version}/"

  : "${src_profile:?VSA_SOURCE_PROFILE is required when vsaArtifacts.ontapVersion is present}"
  : "${src_ns:?VSA_SOURCE_NAMESPACE is required when vsaArtifacts.ontapVersion is present}"
  : "${src_bucket:?VSA_SOURCE_BUCKET_NAME is required when vsaArtifacts.ontapVersion is present}"

  log_step "Detected ONTAP version (manifest): ${ontap_version}"
  if [[ -n "${VSA_SOURCE_ONTAP_FOLDER_OVERRIDE:-}" ]]; then
    log_step "Resolved ONTAP folder (bucket): ${ontap_bucket_version} (from override)"
  else
    log_step "Resolved ONTAP folder (bucket): ${ontap_bucket_version}"
  fi
  log_step "Source: profile=${src_profile} namespace=${src_ns} bucket=${src_bucket} prefix=${src_prefix}"
  log_step "Destination prefix: ${dst_prefix}"
  log_step "Checking source folder/object listing"
  list_json="$(
    oci os object list \
      --profile "${src_profile}" \
      --auth api_key \
      --namespace-name "${src_ns}" \
      --bucket-name "${src_bucket}" \
      --prefix "${src_prefix}" \
      --limit 1000 \
      --output json
  )"
  list_count="$(printf '%s' "${list_json}" | jq -r '.data | length')"
  log_step "Found ${list_count} source object entries"
  if [[ "${list_count}" -eq 0 ]]; then
    echo "Source folder not found or empty: bucket=${src_bucket} prefix=${src_prefix}" >&2
    exit 1
  fi

  tmp_dir="${WORKDIR}/vsa-src"
  rm -rf "${tmp_dir}"
  mkdir -p "${tmp_dir}"
  echo "${ontap_bucket_version}" > "${WORKDIR}/vsa-ontap-version.txt"

  copied_count=0
  printf '%s' "${list_json}" | jq -r '.data[].name' | while IFS= read -r object_name; do
    [[ -z "${object_name}" ]] && continue
    if [[ "${object_name}" == */ ]]; then
      continue
    fi
    rel_path="${object_name#${src_prefix}}"
    local_path="${tmp_dir}/${rel_path}"
    log_step "VSA copy: ${object_name} -> ${dst_prefix}${rel_path}"
    mkdir -p "$(dirname "${local_path}")"
    oci os object get \
      --profile "${src_profile}" \
      --auth api_key \
      --namespace-name "${src_ns}" \
      --bucket-name "${src_bucket}" \
      --name "${object_name}" \
      --file "${local_path}" >/dev/null
    put_file "${local_path}" "${dst_prefix}${rel_path}"
    copied_count=$((copied_count + 1))
  done

  put_file "${WORKDIR}/vsa-ontap-version.txt" "${PREFIX}/VSA/ontapVersion"
  log_step "Copied VSA/VLM source folder ${src_prefix} -> ${dst_prefix}"
}

resolve_deployment_kit_source() {
  local pull_root="$1"
  local rel_file="$2"
  local candidate

  candidate="${pull_root}/artifacts/${rel_file}"
  if [[ -f "${candidate}" ]]; then
    printf '%s\n' "${candidate}"
    return 0
  fi

  candidate="${pull_root}/${rel_file}"
  if [[ -f "${candidate}" ]]; then
    printf '%s\n' "${candidate}"
    return 0
  fi

  candidate="$(
    find "${pull_root}" -type f -name "$(basename "${rel_file}")" -print -quit 2>/dev/null || true
  )"
  if [[ -n "${candidate}" ]] && [[ -f "${candidate}" ]]; then
    printf '%s\n' "${candidate}"
    return 0
  fi

  return 1
}

copy_deployment_kit_files() {
  local manifest_path="$1"
  local pull_root="$2"
  local files_count file_name src_file

  log_section "Step 3: Deployment kit files"
  files_count="$(jq -r '(.deploymentKit.files // []) | length' "${manifest_path}")"
  if [[ "${files_count}" -eq 0 ]]; then
    log_step "No deploymentKit.files entries in manifest; skipping deployment kit copy."
    return 0
  fi

  log_step "Found ${files_count} deployment kit file entries"
  while IFS= read -r file_name; do
    [[ -z "${file_name}" ]] && continue
    src_file="$(resolve_deployment_kit_source "${pull_root}" "${file_name}" || true)"
    if [[ -z "${src_file}" ]]; then
      echo "Deployment kit file declared in manifest but not found in pulled manifest artifact: ${file_name}" >&2
      exit 1
    fi

    log_step "Deployment kit: ${file_name}"
    log_step "  source: ${src_file}"
    log_step "  target: ${PREFIX}/deploymentKit/${file_name}"
    put_file "${src_file}" "${PREFIX}/deploymentKit/${file_name}"
  done < <(jq -r '.deploymentKit.files[]?' "${manifest_path}" 2>/dev/null || true)
}

copy_build_context() {
  local manifest_path="$1"
  local bc_oci bc_sha bc_size bc_filename bc_digest
  local bc_dir bc_file got_sha got_size got_digest

  log_section "Step 3a: Build context artifact"
  bc_oci="$(jq -r '.vcpArtifacts.buildContext.oci // empty' "${manifest_path}")"
  if [[ -z "${bc_oci}" ]]; then
    log_step "No vcpArtifacts.buildContext.oci in manifest; skipping build context copy."
    return 0
  fi

  bc_sha="$(jq -r '.vcpArtifacts.buildContext.sha256 // empty' "${manifest_path}")"
  bc_size="$(jq -r '.vcpArtifacts.buildContext.sizeBytes // empty' "${manifest_path}")"
  bc_filename="$(jq -r '.vcpArtifacts.buildContext.filename // empty' "${manifest_path}")"
  bc_digest="$(jq -r '.vcpArtifacts.buildContext.digest // empty' "${manifest_path}")"

  if [[ -z "${bc_filename}" ]]; then
    echo "vcpArtifacts.buildContext.filename is required when buildContext.oci is present" >&2
    exit 1
  fi

  log_step "Build context source: ${bc_oci}"
  log_step "Expected filename: ${bc_filename}"

  bc_dir="${WORKDIR}/build-context"
  rm -rf "${bc_dir}"
  mkdir -p "${bc_dir}"

  got_digest="$(oras resolve "${bc_oci}")"
  log_step "Resolved build context digest: ${got_digest}"
  if [[ -n "${bc_digest}" ]] && [[ "${got_digest}" != "${bc_digest}" ]]; then
    echo "Digest mismatch for build context: got ${got_digest} want ${bc_digest}" >&2
    exit 1
  fi

  (
    cd "${bc_dir}"
    oras pull "${bc_oci}"
  )

  bc_file=""
  if [[ -f "${bc_dir}/${bc_filename}" ]]; then
    bc_file="${bc_dir}/${bc_filename}"
  elif [[ -f "${bc_dir}/artifacts/${bc_filename}" ]]; then
    bc_file="${bc_dir}/artifacts/${bc_filename}"
  else
    bc_file="$(
      find "${bc_dir}" -type f -name "${bc_filename}" -print -quit 2>/dev/null || true
    )"
  fi
  if [[ -z "${bc_file}" ]] || [[ ! -f "${bc_file}" ]]; then
    echo "Build context file ${bc_filename} not found after ORAS pull from ${bc_oci}" >&2
    echo "Files present under ${bc_dir}:" >&2
    find "${bc_dir}" -type f -maxdepth 3 2>/dev/null >&2 || true
    exit 1
  fi
  log_step "Resolved build context file path: ${bc_file}"

  if [[ -n "${bc_sha}" ]]; then
    got_sha="$(sha256sum "${bc_file}" | awk '{print $1}')"
    if [[ "${got_sha}" != "${bc_sha}" ]]; then
      echo "SHA256 mismatch for build context: got ${got_sha} want ${bc_sha}" >&2
      exit 1
    fi
  fi

  if [[ -n "${bc_size}" ]]; then
    got_size="$(wc -c < "${bc_file}" | tr -d ' ')"
    if [[ "${got_size}" != "${bc_size}" ]]; then
      echo "Size mismatch for build context: got ${got_size} want ${bc_size}" >&2
      exit 1
    fi
  fi

  log_step "Uploading build context: ${PREFIX}/build-context/${bc_filename}"
  put_file "${bc_file}" "${PREFIX}/build-context/${bc_filename}"
}

log_section "Step 1: Resolve and pull release manifest"
log_step "Manifest reference: ${MANIFEST_REF}"
MANIFEST_DIGEST="$(oras resolve "${MANIFEST_REF}")"
echo "${MANIFEST_DIGEST}" > "${WORKDIR}/manifest.digest.txt"
log_step "Pinned manifest digest: ${MANIFEST_DIGEST}"

MAN_PULL="${WORKDIR}/manifest-pull"
rm -rf "${MAN_PULL}"
mkdir -p "${MAN_PULL}"
(
  cd "${MAN_PULL}"
  oras pull "${MANIFEST_REF}"
)

MAN_JSON=""
if [[ -f "${MAN_PULL}/artifacts/release-manifest.json" ]]; then
  MAN_JSON="${MAN_PULL}/artifacts/release-manifest.json"
elif [[ -f "${MAN_PULL}/release-manifest.json" ]]; then
  MAN_JSON="${MAN_PULL}/release-manifest.json"
else
  echo "release-manifest.json not found under ${MAN_PULL}; listing:" >&2
  find "${MAN_PULL}" -type f | head -50 >&2
  exit 1
fi

echo "Validating manifest JSON..."
jq -e '.apiVersion and .kind == "ReleaseManifest" and .metadata.version' "${MAN_JSON}" >/dev/null
if ! jq -e '(.vcpArtifacts.binaries // []) | length > 0' "${MAN_JSON}" >/dev/null; then
  echo "WARN: vcpArtifacts.binaries is empty; upload will only include manifest/helm/dockerfiles (if any)." >&2
fi

META_VER="$(jq -r '.metadata.version // empty' "${MAN_JSON}")"
if [[ -n "${META_VER}" ]] && [[ "${META_VER}" != "${MANIFEST_VERSION}" ]]; then
  echo "WARN: metadata.version (${META_VER}) != input MANIFEST_VERSION (${MANIFEST_VERSION})" >&2
fi

log_section "Step 2: Upload manifest metadata"
log_step "Destination prefix: ${PREFIX}/"
put_file "${MAN_JSON}" "${PREFIX}/release-manifest.json"
put_file "${WORKDIR}/manifest.digest.txt" "${PREFIX}/release-manifest.digest"
copy_deployment_kit_files "${MAN_JSON}" "${MAN_PULL}"
copy_build_context "${MAN_JSON}"
copy_vsa_release_folder "${MAN_JSON}"

# Binary names (for mapping Dockerfiles into artifact groups)
BINARY_LIST_TMP="${WORKDIR}/binary-names.txt"
jq -r '(.vcpArtifacts.binaries // [])[] | select((.name // "") | length > 0) | .name' "${MAN_JSON}" > "${BINARY_LIST_TMP}" 2>/dev/null || true

# Map Dockerfile (name + sourcePath) to a binary group name, or empty if unmatched.
dockerfile_target_binary() {
  local src="$1" df_name="$2"
  local stem sfx b m count

  stem="$(dirname "${src}")"
  [[ "${stem}" == "." ]] && stem=""
  sfx="${df_name#Dockerfile.}"
  [[ "${sfx}" == "${df_name}" ]] && sfx=""

  while IFS= read -r b; do
    [[ -z "${b}" ]] && continue
    [[ "${b}" == "${stem}" ]] && echo "${b}" && return
  done < "${BINARY_LIST_TMP}"

  if [[ -n "${sfx}" ]]; then
    while IFS= read -r b; do
      [[ -z "${b}" ]] && continue
      [[ "${b}" == "${sfx}" ]] && echo "${b}" && return
    done < "${BINARY_LIST_TMP}"
  fi

  m=""
  count=0
  while IFS= read -r b; do
    [[ -z "${b}" ]] && continue
    [[ -z "${stem}" ]] && continue
    if [[ "${b}" == *"-${stem}" ]]; then
      m="${b}"
      count=$((count + 1))
    fi
  done < "${BINARY_LIST_TMP}"
  if [[ "${count}" -eq 1 ]]; then
    echo "${m}"
    return
  fi
  echo ""
}

# --- Binaries (grouped under artifacts/<name>/: binary blob + ORAS sidecars) ---
log_section "Step 4: Mirror binary artifacts"
echo "Processing vcpArtifacts.binaries..."
while IFS= read -r row; do
  [[ -z "${row}" ]] && continue
  name="$(echo "${row}" | jq -r '.name')"
  ghcr_oci="$(echo "${row}" | jq -r '.ghcr_oci')"
  want_digest="$(echo "${row}" | jq -r '.digest')"
  want_sha="$(echo "${row}" | jq -r '.sha256')"
  want_size="$(echo "${row}" | jq -r '.sizeBytes')"

  log_step "Binary: ${name}"
  log_step "Source OCI artifact: ${ghcr_oci}"
  got_digest="$(oras resolve "${ghcr_oci}")"
  if [[ "${got_digest}" != "${want_digest}" ]]; then
    echo "Digest mismatch for ${name}: got ${got_digest} want ${want_digest}" >&2
    exit 1
  fi

  BIN_DIR="${WORKDIR}/bin-${name}"
  rm -rf "${BIN_DIR}"
  mkdir -p "${BIN_DIR}"
  (
    cd "${BIN_DIR}"
    oras pull "${ghcr_oci}"
  )

  want_lc="$(printf '%s' "${want_sha}" | tr '[:upper:]' '[:lower:]')"
  sha256_file=""
  while IFS= read -r f; do
    [[ -z "${f}" ]] && continue
    if [[ "$(basename "${f}" .sha256)" == "${name}" ]]; then
      sha256_file="${f}"
      break
    fi
  done < <(find "${BIN_DIR}" -type f -name '*.sha256' 2>/dev/null | LC_ALL=C sort || true)
  if [[ -z "${sha256_file}" ]]; then
    while IFS= read -r f; do
      [[ -z "${f}" ]] && continue
      sha256_file="${f}"
      break
    done < <(find "${BIN_DIR}" -type f -name '*.sha256' 2>/dev/null | LC_ALL=C sort || true)
  fi
  if [[ -z "${sha256_file}" ]] || [[ ! -f "${sha256_file}" ]]; then
    echo "ERROR: missing .sha256 sidecar after ORAS pull for binary ${name} under ${BIN_DIR}" >&2
    exit 1
  fi

  sidecar_hash="$(awk 'NF {print $1; exit}' "${sha256_file}" | tr '[:upper:]' '[:lower:]')"
  if [[ "${#sidecar_hash}" -ne 64 ]] || ! [[ "${sidecar_hash}" =~ ^[0-9a-f]{64}$ ]]; then
    echo "ERROR: first field in ${sha256_file} is not a 64-char hex SHA-256 for ${name}" >&2
    exit 1
  fi
  if [[ "${sidecar_hash}" != "${want_lc}" ]]; then
    echo "ERROR: manifest sha256 != .sha256 sidecar for ${name} (manifest ${want_sha}, sidecar ${sidecar_hash})" >&2
    exit 1
  fi

  # Resolve blob: optional path from sidecar line 1, else file whose hash matches manifest.
  relpath="$(awk 'NF>=2 { gsub(/^[ \t]+|[ \t]+$/, "", $2); print $2; exit }' "${sha256_file}")"
  relpath="${relpath#./}"
  blob=""
  if [[ -n "${relpath}" ]] && [[ "${relpath}" != /* ]]; then
    cand="${BIN_DIR}/${relpath}"
    if [[ -f "${cand}" ]]; then
      hb="$(sha256sum "${cand}" | awk '{print $1}' | tr '[:upper:]' '[:lower:]')"
      if [[ "${hb}" == "${want_lc}" ]]; then
        blob="${cand}"
      fi
    fi
  fi
  if [[ -z "${blob}" ]]; then
    while IFS= read -r f; do
      [[ -z "${f}" ]] && continue
      h="$(sha256sum "${f}" | awk '{print $1}' | tr '[:upper:]' '[:lower:]')"
      if [[ "${h}" == "${want_lc}" ]]; then
        blob="${f}"
        break
      fi
    done < <(find "${BIN_DIR}" -type f ! -name '*.sig' ! -name '*.cosign' ! -name '*.sha256' ! -name '*.json' ! -name '*.pem' 2>/dev/null || true)
  fi
  if [[ -z "${blob}" ]] || [[ ! -f "${blob}" ]]; then
    echo "Could not locate binary blob for ${name} under ${BIN_DIR} (expected hash ${want_sha})" >&2
    exit 1
  fi

  got_sha="$(sha256sum "${blob}" | awk '{print $1}' | tr '[:upper:]' '[:lower:]')"
  got_size="$(wc -c < "${blob}" | tr -d ' ')"
  if [[ "${got_sha}" != "${want_lc}" ]]; then
    echo "SHA256 mismatch for ${name}: got ${got_sha} want ${want_sha}" >&2
    exit 1
  fi
  if [[ "${got_size}" != "${want_size}" ]]; then
    echo "Size mismatch for ${name}: got ${got_size} want ${want_size}" >&2
    exit 1
  fi

  ART="${PREFIX}/artifacts/${name}"
  # Stable object name for the blob (same as manifest binary name)
  put_file "${blob}" "${ART}/${name}"

  # Sidecar / signature files from GHCR pull
  find "${BIN_DIR}" -type f ! -samefile "${blob}" \( -name '*.sha256' -o -name '*.sig' -o -name '*.cosign' -o -name '*.pem' \) -print0 2>/dev/null |
    while IFS= read -r -d '' sf; do
      base="$(basename "${sf}")"
      put_file "${sf}" "${ART}/${base}"
    done
done < <(jq -c '.vcpArtifacts.binaries[]?' "${MAN_JSON}" 2>/dev/null || true)

# --- Helm charts (OCI): helm pull only for entries whose chart name contains "oci" (case-insensitive) ---
log_section "Step 5: Mirror Helm OCI charts"
echo "Processing vcpArtifacts.helmCharts (helm pull; name contains \"oci\" only)..."
OCI_HELM_COUNT="$(jq '[.vcpArtifacts.helmCharts[]? | select((.name // "") | ascii_downcase | contains("oci"))] | length' "${MAN_JSON}" 2>/dev/null || echo 0)"
echo "  Selected charts (oci in name): ${OCI_HELM_COUNT}"
while IFS= read -r row; do
  [[ -z "${row}" ]] && continue
  hname="$(echo "${row}" | jq -r '.name')"
  ghcr_uri="$(echo "${row}" | jq -r '.ghcr')"
  want_sha="$(echo "${row}" | jq -r '.sha256 // empty')"
  ver="$(echo "${row}" | jq -r '.version // empty')"

  if [[ "${ghcr_uri}" != oci://* ]]; then
    echo "WARN: Skipping ${hname}: expected ghcr to start with oci://, got ${ghcr_uri}" >&2
    continue
  fi

  rest="${ghcr_uri#oci://}"
  registry_path="${rest%:*}"
  if [[ "${registry_path}" == "${rest}" ]]; then
    if [[ -z "${ver}" ]]; then
      echo "ERROR: ${hname}: no chart version in URI or manifest .version" >&2
      exit 1
    fi
  else
    if [[ -z "${ver}" ]]; then
      ver="${rest##*:}"
    fi
  fi
  oci_chart_ref="oci://${registry_path}"

  log_step "Helm chart: ${hname} from ${oci_chart_ref} (version ${ver})"
  HELM_DIR="${WORKDIR}/helm-${hname}"
  rm -rf "${HELM_DIR}"
  mkdir -p "${HELM_DIR}"
  helm pull "${oci_chart_ref}" --version "${ver}" --destination "${HELM_DIR}"

  tgz_files=()
  while IFS= read -r f; do
    [[ -z "${f}" ]] && continue
    tgz_files+=("${f}")
  done < <(find "${HELM_DIR}" -maxdepth 1 -type f -name '*.tgz' 2>/dev/null | LC_ALL=C sort || true)
  if [[ "${#tgz_files[@]}" -eq 0 ]]; then
    echo "helm pull produced no .tgz under ${HELM_DIR} for ${hname}" >&2
    exit 1
  fi

  if [[ -n "${want_sha}" ]]; then
    matched=0
    for f in "${tgz_files[@]}"; do
      got="$(sha256sum "${f}" | awk '{print $1}')"
      if [[ "${got}" == "${want_sha}" ]]; then
        matched=1
        break
      fi
    done
    if [[ "${matched}" -eq 0 ]]; then
      echo "SHA256 mismatch for helm chart ${hname}: manifest wants ${want_sha}" >&2
      for f in "${tgz_files[@]}"; do
        echo "  file $(basename "${f}"): $(sha256sum "${f}" | awk '{print $1}')" >&2
      done
      exit 1
    fi
  fi

  while IFS= read -r -d '' f; do
    rel="${f#"${HELM_DIR}/"}"
    put_file "${f}" "${PREFIX}/helm/${hname}/${rel}"
  done < <(find "${HELM_DIR}" -type f -print0 || true)
done < <(jq -c '.vcpArtifacts.helmCharts[]? | select((.name // "") | ascii_downcase | contains("oci"))' "${MAN_JSON}" 2>/dev/null || true)

# --- Dockerfiles: place next to matching binary under artifacts/<binary>/; else dockerfiles/ ---
log_section "Step 6: Mirror Dockerfiles"
echo "Processing vcpArtifacts.dockerfiles..."
while IFS= read -r row; do
  [[ -z "${row}" ]] && continue
  df_name="$(echo "${row}" | jq -r '.name')"
  src="$(echo "${row}" | jq -r '.sourcePath')"
  src_file="${DOCKERFILE_REPO_ROOT}/${src}"
  if [[ ! -f "${src_file}" ]]; then
    echo "Dockerfile missing at ${src_file} (set DOCKERFILE_REPO_ROOT to a checkout of the release tag). Skipping ${df_name}." >&2
    continue
  fi
  tgt="$(dockerfile_target_binary "${src}" "${df_name}")"
  if [[ -n "${tgt}" ]]; then
    put_file "${src_file}" "${PREFIX}/artifacts/${tgt}/${df_name}"
    log_step "Dockerfile ${df_name} -> artifacts/${tgt}/"
  else
    put_file "${src_file}" "${PREFIX}/dockerfiles/${df_name}"
    log_step "Dockerfile ${df_name} -> dockerfiles/ (no single matching binary for sourcePath=${src})"
  fi
done < <(jq -c '.vcpArtifacts.dockerfiles[]?' "${MAN_JSON}" 2>/dev/null || true)

# Optional completion marker (Design §5 P9)
jq -n \
  --arg v "${MANIFEST_VERSION}" \
  --arg run "${GITHUB_RUN_ID:-local}" \
  --arg sha "${MANIFEST_DIGEST}" \
  '{released: true, version: $v, run: $run, manifestArtifactDigest: $sha}' > "${WORKDIR}/released.json"
put_file "${WORKDIR}/released.json" "${PREFIX}/RELEASED"

log_section "Step 7: Completed"
log_step "Mirror complete for ${MANIFEST_VERSION}"
log_step "Destination bucket: ${BUCKET}"
log_step "Destination prefix: ${PREFIX}/"
log_step "Total uploaded objects: ${UPLOAD_COUNT}"
