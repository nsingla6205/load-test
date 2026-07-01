#!/usr/bin/env bash
#
# version-compat.sh — oci-proxy upgrade-compatibility matrix.
#
# Discovers the oci-proxy tags available in the container registry and, for a
# chosen baseline (the version you're upgrading FROM), runs the image-driven
# migration UPGRADE gate (oci-migrate-gate.sh) against each candidate version.
# It reports, per candidate, whether the upgrade would leave the schema DIRTY
# (the production "Dirty database version N" failure) and recommends the best
# version to upgrade to.
#
# This is the multi-version wrapper around oci-migrate-gate.sh's upgrade mode:
# for each candidate it brings a fresh DB to the OLD release, then applies the
# NEW release's migrations on top — exactly what runs when OCI upgrades.
#
# Subcommands:
#   list   [--repo REPO] [--all]
#       Print the tags available for the oci-proxy repo. By default only
#       "release-like" tags (NNN.N.N[-...]) are shown; --all shows every tag.
#
#   check  --from REF [--to T1,T2,...] [--repo REPO] [--all] [--limit N]
#          [--timeout SECONDS] [--keep-going]
#       Run the upgrade gate from REF to each candidate and print a matrix +
#       a recommendation. REF and each target may be a full image ref
#       (iad.ocir.io/.../oci-proxy:TAG) or a bare TAG (resolved against REPO).
#       --to omitted  -> discover tags from the registry (release-like unless
#                        --all), excluding the baseline tag.
#       --limit N     -> test at most N candidates (after version sort).
#       --timeout S   -> per-image migration budget passed to the gate (def 240).
#
# Options/env:
#   OCI_PROXY_REPO   default repo (else taken from .oci-release.env OCI_PROXY_IMAGE,
#                    else iad.ocir.io/idqogasfjw45/oci-proxy).
#
# Exit codes: 0 = at least one compatible target (or list ok); 1 = no compatible
# target / all candidates dirty; 2 = setup/usage error or broken baseline.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
. "$SCRIPT_DIR/lib.sh"

GATE="$SCRIPT_DIR/oci-migrate-gate.sh"
RELEASE_ENV_FILE="${OCI_RELEASE_ENV_FILE:-$WORKSPACE_ROOT/.oci-release.env}"
DEFAULT_REPO_CONST="iad.ocir.io/idqogasfjw45/oci-proxy"

# --- helpers -----------------------------------------------------------------

# default_repo — repo to resolve bare tags against and to list.
default_repo() {
  if [ -n "${OCI_PROXY_REPO:-}" ]; then printf '%s' "$OCI_PROXY_REPO"; return; fi
  if [ -f "$RELEASE_ENV_FILE" ]; then
    # shellcheck disable=SC1090
    set -a; . "$RELEASE_ENV_FILE"; set +a
    if [ -n "${OCI_PROXY_IMAGE:-}" ]; then printf '%s' "${OCI_PROXY_IMAGE%:*}"; return; fi
  fi
  printf '%s' "$DEFAULT_REPO_CONST"
}

# ref_is_full — true if the arg looks like a full image ref (has a "/").
ref_is_full() { case "$1" in */*) return 0 ;; *) return 1 ;; esac; }

# to_ref REPO REF — expand a bare TAG to REPO:TAG; pass full refs through.
to_ref() { if ref_is_full "$2"; then printf '%s' "$2"; else printf '%s:%s' "$1" "$2"; fi; }

# is_older A B — true if version A sorts strictly before B (A is a downgrade vs B).
is_older() {
  [ "$1" != "$2" ] || return 1
  [ "$(printf '%s\n%s\n' "$1" "$2" | sort -V | head -1)" = "$1" ]
}

# tag_of REF — the tag portion of a ref (text after the last ':' in the last path segment).
tag_of() {
  local last="${1##*/}"
  case "$last" in *:*) printf '%s' "${last##*:}" ;; *) printf '%s' "$1" ;; esac
}

# list_tags REPO — print all tags, one per line.
list_tags() {
  local repo="$1"
  have skopeo || die "skopeo not found. Install it ('brew install skopeo') or pass --to with explicit tags."
  have python3 || die "python3 not found (needed to parse the registry tag list). Install it or pass --to with explicit tags."
  skopeo list-tags "docker://$repo" 2>/dev/null \
    | python3 -c 'import sys,json; print("\n".join(json.load(sys.stdin).get("Tags",[])))' \
    || die "could not list tags for $repo (logged in? 'docker login ${repo%%/*}')"
}

# release_like — filter stdin to release-like tags (NNN.N.N[-...]).
release_like() { grep -E '^[0-9]+\.[0-9]+\.[0-9]+' || true; }

# --- list --------------------------------------------------------------------
cmd_list() {
  local repo="" all=0
  while [ $# -gt 0 ]; do case "$1" in
    --repo) repo="$2"; shift 2 ;;
    --all)  all=1; shift ;;
    *) die "list: unknown arg '$1'" ;;
  esac; done
  [ -n "$repo" ] || repo="$(default_repo)"
  info "[compat] tags for $repo:"
  local tags; tags="$(list_tags "$repo")"
  if [ "$all" = 1 ]; then
    printf '%s\n' "$tags" | sort -V
  else
    local rel; rel="$(printf '%s\n' "$tags" | release_like | sort -V)"
    if [ -n "$rel" ]; then
      printf '%s\n' "$rel"
      info "[compat] ($(printf '%s\n' "$tags" | grep -c . ) total tags; showing release-like only — use --all for every tag)"
    else
      warn "[compat] no release-like tags; showing all:"
      printf '%s\n' "$tags" | sort -V
    fi
  fi
}

# --- check -------------------------------------------------------------------
cmd_check() {
  local repo="" from="" to_csv="" all=0 limit=0 timeout=240 keep_going=1
  while [ $# -gt 0 ]; do case "$1" in
    --from)       from="$2"; shift 2 ;;
    --to)         to_csv="$2"; shift 2 ;;
    --repo)       repo="$2"; shift 2 ;;
    --all)        all=1; shift ;;
    --limit)      limit="$2"; shift 2 ;;
    --timeout)    timeout="$2"; shift 2 ;;
    --keep-going) keep_going=1; shift ;;   # accepted for clarity; always on
    *) die "check: unknown arg '$1'" ;;
  esac; done
  [ -n "$from" ] || die "check needs --from <tag|image>"
  [ -n "$repo" ] || repo="$(default_repo)"

  local from_ref from_tag
  from_ref="$(to_ref "$repo" "$from")"
  from_tag="$(tag_of "$from_ref")"

  # Build the candidate tag list.
  local targets=()
  if [ -n "$to_csv" ]; then
    IFS=',' read -r -a targets <<< "$to_csv"
  else
    local discovered
    discovered="$(list_tags "$repo")"
    [ "$all" = 1 ] || discovered="$(printf '%s\n' "$discovered" | release_like)"
    # drop the baseline tag and sort
    while IFS= read -r t; do
      [ -n "$t" ] || continue
      [ "$t" = "$from_tag" ] && continue
      targets+=("$t")
    done < <(printf '%s\n' "$discovered" | sort -V)
  fi
  [ "${#targets[@]}" -gt 0 ] || die "no candidate targets (try --all, or pass --to)."
  if [ "$limit" -gt 0 ] && [ "${#targets[@]}" -gt "$limit" ]; then
    targets=("${targets[@]:0:$limit}")
  fi

  info "[compat] baseline (from): $from_ref"
  info "[compat] candidates (${#targets[@]}): ${targets[*]}"
  info "[compat] running upgrade gate per candidate (per-image budget ${timeout}s)…"
  log ""

  # Results collected as "TAG|STATUS|DETAIL".
  local rows=() pass_tags=()
  local t target_ref out rc status detail dv df
  for t in "${targets[@]}"; do
    target_ref="$(to_ref "$repo" "$t")"

    # Downgrades aren't valid upgrade targets — skip without spending a gate run.
    if is_older "$t" "$from_tag"; then
      info "[compat] === ${from_tag}  →  ${t} ===  (skipped: older than baseline)"
      rows+=("${t}|SKIP|downgrade (older than baseline)")
      log ""
      continue
    fi

    info "[compat] === ${from_tag}  →  ${t} ==="
    rc=0
    out="$(NO_COLOR=1 "$GATE" --from-image "$from_ref" --to-image "$target_ref" \
            --fresh --timeout "$timeout" 2>&1)" || rc=$?

    # Baseline itself broken -> every candidate is meaningless; stop early.
    if printf '%s' "$out" | grep -q 'BASELINE (old) release failed to migrate'; then
      err ""
      err "[compat] the BASELINE image '$from_ref' cannot migrate a fresh DB."
      err "         Fix/choose a different --from before testing upgrades."
      printf '%s\n' "$out" | grep -E 'dirty_version=|likely file:|migration FAILED' | sed 's/^/         /' >&2 || true
      exit 2
    fi

    # Image-manifest / container-start problems are NOT migration failures —
    # classify them separately so they don't masquerade as schema breakage.
    if printf '%s' "$out" | grep -q 'does not provide the specified platform'; then
      rows+=("${t}|PLAT?|no linux/amd64 image (multi-arch manifest missing amd64)")
      log ""
      continue
    fi
    if printf '%s' "$out" | grep -q 'could not start migrator container'; then
      rows+=("${t}|ENV?|migrator container failed to start — inconclusive")
      log ""
      continue
    fi

    case "$rc" in
      0) status="PASS"; detail="clean upgrade"; pass_tags+=("$t") ;;
      3) status="ENV?"; detail="emulation/runtime crash — inconclusive (run on amd64)" ;;
      2) status="ERROR"; detail="gate setup error" ;;
      *)
        status="FAIL"
        # NOTE: each cmdsub ends with `|| true` — under `set -euo pipefail` a
        # grep that matches nothing returns 1, which would otherwise abort the
        # whole matrix mid-candidate.
        dv="$(printf '%s' "$out" | grep -oE 'table=[a-z_]+ phase=[a-z]+ dirty_version=[0-9]+' | head -1 || true)"
        df="$(printf '%s' "$out" | grep -oE 'likely file: [^ ]+' | head -1 | sed 's#likely file: .*/##' || true)"
        if [ -n "$dv" ]; then
          detail="dirty: ${dv#table=}${df:+ ($df)}"
        else
          detail="$(printf '%s' "$out" | grep -oE 'SQLSTATE [0-9A-Z]+' | head -1 || true)"
          [ -n "$detail" ] && detail="migration failed (${detail})" || detail="migration failed"
        fi
        ;;
    esac
    rows+=("${t}|${status}|${detail}")
    log ""
  done

  # --- report ---------------------------------------------------------------
  log ""
  ok "================ OCI-PROXY UPGRADE COMPATIBILITY ================"
  ok "baseline (from): $from_ref"
  log ""
  printf '%s\n' "  TARGET                                RESULT  DETAIL" >&2
  printf '%s\n' "  ------------------------------------  ------  ------------------------------------" >&2
  local r tag st de
  for r in "${rows[@]}"; do
    tag="${r%%|*}"; st="${r#*|}"; de="${st#*|}"; st="${st%%|*}"
    printf '  %-36s  %-6s  %s\n' "$tag" "$st" "$de" >&2
  done
  log ""

  if [ "${#pass_tags[@]}" -gt 0 ]; then
    local best; best="$(printf '%s\n' "${pass_tags[@]}" | sort -V | tail -1)"
    ok "RECOMMENDED upgrade target: ${best}"
    ok "  (highest version whose upgrade from ${from_tag} migrated cleanly)"
    [ "${#pass_tags[@]}" -gt 1 ] && ok "  all compatible: $(printf '%s ' "${pass_tags[@]}")"
    log ""
    ok "================================================================"
    return 0
  else
    err "NO compatible upgrade target found from ${from_tag}."
    err "  No candidate migrated cleanly. Legend: FAIL=migration/schema broke,"
    err "  PLAT?=image lacks linux/amd64, ENV?=runtime/start issue, SKIP=downgrade."
    err "  Inspect the FAIL details above and fix the offending migration SQL."
    log ""
    err "================================================================"
    return 1
  fi
}

# --- main --------------------------------------------------------------------
main() {
  local sub="${1:-}"
  [ $# -gt 0 ] && shift || true
  case "$sub" in
    list)  cmd_list "$@" ;;
    check) cmd_check "$@" ;;
    -h|--help|help|"") sed -n '2,55p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' ;;
    *) die "unknown subcommand '$sub' (use: list | check | --help)" ;;
  esac
}

main "$@"
