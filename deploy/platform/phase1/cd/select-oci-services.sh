#!/usr/bin/env bash
# Select which services from deploy/platform/phase1/cd/services.tsv must be rebuilt
# between --base-revision (currently running on production) and --head-revision.
# Prints a single tab-separated machine line to stdout:
#   incremental\t<comma-separated-build-services>
#   full\t
# All human diagnostics are written to stderr.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../../.." && pwd)"
cd "$root"

base_rev=""
head_rev="HEAD"
reuse_manifest=""
registry="ccr.ccs.tencentyun.com"
namespace="hvac"
services_file="$root/deploy/platform/phase1/cd/services.tsv"
include_optional="0"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --base-revision)
      base_rev="${2:-}"
      shift 2
      ;;
    --head-revision)
      head_rev="${2:-}"
      shift 2
      ;;
    --reuse-manifest)
      reuse_manifest="${2:-}"
      shift 2
      ;;
    --registry)
      registry="${2:-}"
      shift 2
      ;;
    --namespace)
      namespace="${2:-}"
      shift 2
      ;;
    --services-file)
      services_file="${2:-}"
      shift 2
      ;;
    --include-optional)
      include_optional="${2:-}"
      shift 2
      ;;
    *)
      echo "error: unknown option '$1'" >&2
      exit 1
      ;;
  esac
done

emit_full() {
  local reason="$1"
  echo "oci-select: full build (${reason})" >&2
  printf 'full\t\n'
  exit 0
}

if [[ ! -f "$services_file" ]]; then
  echo "error: services catalog missing: $services_file" >&2
  exit 1
fi
if [[ ! "$base_rev" =~ ^[0-9a-f]{40}$ ]]; then
  emit_full "invalid base revision '${base_rev}'"
fi
if ! base_resolved="$(git rev-parse --verify "${base_rev}^{commit}" 2>/dev/null)"; then
  emit_full "base revision ${base_rev} is not present in git history"
fi
if ! head_resolved="$(git rev-parse --verify "${head_rev}^{commit}" 2>/dev/null)"; then
  emit_full "head revision ${head_rev} cannot be resolved"
fi
if ! git merge-base --is-ancestor "$base_resolved" "$head_resolved" 2>/dev/null; then
  emit_full "base revision ${base_resolved:0:12} is not an ancestor of ${head_resolved:0:12}"
fi
if [[ -z "$reuse_manifest" || ! -s "$reuse_manifest" ]]; then
  emit_full "reuse manifest is missing or empty"
fi

declare -A reuse_refs=()
while IFS=$'\t' read -r m_svc _m_local m_ref; do
  [[ -z "${m_svc:-}" || "$m_svc" == \#* ]] && continue
  if [[ -n "${m_ref:-}" ]]; then
    reuse_refs["$m_svc"]="$m_ref"
  fi
done < "$reuse_manifest"

if [[ "${#reuse_refs[@]}" -eq 0 ]]; then
  emit_full "reuse manifest contains no entries"
fi

declare -a changed_files=()
if [[ "$base_resolved" != "$head_resolved" ]]; then
  mapfile -t changed_files < <(git diff --name-only "${base_resolved}..${head_resolved}")
fi

declare -A changed_file_set=()
go_global_changed=0
healthcheck_changed=0
go_any_relevant=0
web_changed=0
node_changed=0
migrator_changed=0
mqtt_changed=0

declare -a migrator_copy_sources=()
migrations_df="$root/deploy/platform/phase1/migrations/Dockerfile"
if [[ -f "$migrations_df" ]]; then
  while IFS= read -r df_line; do
    df_line="${df_line#"${df_line%%[![:space:]]*}"}"
    [[ -z "$df_line" || "$df_line" == \#* ]] && continue
    if [[ "$df_line" =~ ^COPY[[:space:]]+(.+)$ ]]; then
      read -r -a copy_tokens <<< "${BASH_REMATCH[1]}"
      # COPY may carry --flags before source paths; skip flag tokens and trailing destination.
      declare -a srcs=()
      idx=0
      last_idx=$(( ${#copy_tokens[@]} - 1 ))
      while (( idx < last_idx )); do
        tok="${copy_tokens[$idx]}"
        if [[ "$tok" != --* ]]; then
          srcs+=("$tok")
        fi
        idx=$((idx + 1))
      done
      for s in "${srcs[@]+"${srcs[@]}"}"; do
        s="${s#./}"
        migrator_copy_sources+=("$s")
      done
      unset srcs
    fi
  done < "$migrations_df"
fi

matches_migrator_path() {
  local f="$1"
  case "$f" in
    deploy/platform/phase1/migrations/*|deploy/platform/phase1/postgres/init/*)
      return 0
      ;;
  esac
  local src
  for src in "${migrator_copy_sources[@]+"${migrator_copy_sources[@]}"}"; do
    if [[ "$f" == "$src" || "$f" == "${src%/}/"* ]]; then
      return 0
    fi
  done
  return 1
}

for f in "${changed_files[@]+"${changed_files[@]}"}"; do
  [[ -z "$f" ]] && continue
  changed_file_set["$f"]=1
  case "$f" in
    go.work|go.work.sum|go.mod|go.sum)
      go_global_changed=1
      go_any_relevant=1
      ;;
    tools/container-healthcheck/*)
      healthcheck_changed=1
      go_any_relevant=1
      ;;
    cmd/*|libs/*|modules/*|services/*|tools/*|*/go.mod|*/go.sum)
      go_any_relevant=1
      ;;
  esac
  case "$f" in
    apps/hvac-web/*|package.json|package-lock.json|scripts/check-web-js-runtime.mjs|deploy/platform/phase1/nginx/*)
      web_changed=1
      ;;
  esac
  case "$f" in
    services/operations-agent-service/*|deploy/platform/phase1/images/operations-agent.Dockerfile)
      node_changed=1
      ;;
  esac
  if matches_migrator_path "$f"; then
    migrator_changed=1
  fi
  case "$f" in
    infra/telemetry/mqtt/crl-plugin/*|deploy/platform/phase1/mqtt/Dockerfile)
      mqtt_changed=1
      ;;
  esac
done

repo_root_real="$(cd "$root" && pwd -P)"

repo_name_for_image() {
  local local_image="$1"
  local without_registry="$local_image"
  if [[ "$without_registry" == */* ]]; then
    without_registry="${without_registry#*/}"
  fi
  printf '%s\n' "${without_registry%%:*}"
}

has_valid_reuse_ref() {
  local service="$1"
  local local_image="$2"
  local ref="${reuse_refs[$service]:-}"
  [[ -n "$ref" ]] || return 1
  local repo_name expected_prefix suffix
  repo_name="$(repo_name_for_image "$local_image")"
  expected_prefix="${registry}/${namespace}/${repo_name}@sha256:"
  if [[ "$ref" != "${expected_prefix}"* ]]; then
    return 1
  fi
  suffix="${ref#"$expected_prefix"}"
  [[ "$suffix" =~ ^[0-9a-f]{64}$ ]]
}

go_package_changed() {
  local pkg="$1"
  if [[ "$go_global_changed" == 1 ]]; then
    return 0
  fi
  if [[ "$go_any_relevant" == 0 ]]; then
    return 1
  fi
  if ! command -v go >/dev/null 2>&1; then
    return 0
  fi

  local dep_dirs=""
  if ! dep_dirs="$(go list -deps -f '{{.Dir}}' "$pkg" 2>/dev/null)"; then
    # Fail-closed when Go dependency resolution cannot verify safety.
    return 0
  fi

  declare -A dep_rel_dirs=()
  local dir rel
  while IFS= read -r dir; do
    [[ -z "$dir" ]] && continue
    case "$dir" in
      "$repo_root_real")
        dep_rel_dirs["."]=1
        ;;
      "$repo_root_real/"*)
        rel="${dir#"$repo_root_real/"}"
        dep_rel_dirs["$rel"]=1
        ;;
    esac
  done <<< "$dep_dirs"

  if [[ "${#dep_rel_dirs[@]}" -eq 0 ]]; then
    return 0
  fi

  local pkg_clean="${pkg#./}"
  dep_rel_dirs["$pkg_clean"]=1

  local f d cur
  for d in "${!dep_rel_dirs[@]}"; do
    for f in "${changed_files[@]+"${changed_files[@]}"}"; do
      if [[ "$d" == "." ]]; then
        if [[ "$f" != */* ]]; then
          return 0
        fi
      elif [[ "$f" == "${d}/"* || "$f" == "$d" ]]; then
        return 0
      fi
    done

    cur="$d"
    while [[ "$cur" != "." && -n "$cur" ]]; do
      if [[ -n "${changed_file_set["${cur}/go.mod"]:-}" || -n "${changed_file_set["${cur}/go.sum"]:-}" ]]; then
        return 0
      fi
      if [[ "$cur" == */* ]]; then
        cur="${cur%/*}"
      else
        cur="."
      fi
    done
  done

  return 1
}

python_package_changed() {
  local pkg="${1#./}"
  pkg="${pkg%/}"
  local f
  for f in "${changed_files[@]+"${changed_files[@]}"}"; do
    if [[ "$f" == "${pkg}/"* || "$f" == "$pkg" ]]; then
      return 0
    fi
  done
  return 1
}

declare -a build_list=()
declare -a reuse_list=()
declare -a skip_list=()

while IFS=$'\t' read -r service local_image kind required package <&3; do
  [[ -z "${service:-}" || "$service" == \#* ]] && continue

  reuse_valid=0
  if has_valid_reuse_ref "$service" "$local_image"; then
    reuse_valid=1
  fi

  if [[ "$required" != required && "$reuse_valid" == 0 && "$include_optional" != 1 ]]; then
    skip_list+=("$service")
    continue
  fi

  needs_build=0
  case "$kind" in
    go)
      if [[ "$healthcheck_changed" == 1 ]] || go_package_changed "$package"; then
        needs_build=1
      fi
      ;;
    static)
      if go_package_changed "$package"; then
        needs_build=1
      fi
      ;;
    web)
      if [[ "$web_changed" == 1 ]]; then
        needs_build=1
      fi
      ;;
    node)
      if [[ "$node_changed" == 1 ]]; then
        needs_build=1
      fi
      ;;
    migrator)
      if [[ "$migrator_changed" == 1 ]]; then
        needs_build=1
      fi
      ;;
    mqtt)
      if [[ "$mqtt_changed" == 1 ]]; then
        needs_build=1
      fi
      ;;
    python)
      if python_package_changed "$package"; then
        needs_build=1
      fi
      ;;
    *)
      needs_build=1
      ;;
  esac

  if [[ "$needs_build" == 1 ]]; then
    build_list+=("$service")
  elif [[ "$reuse_valid" == 1 ]]; then
    reuse_list+=("$service")
  elif [[ "$required" == required ]]; then
    echo "oci-select: missing valid reuse digest for required service '${service}'; marking for rebuild" >&2
    build_list+=("$service")
  else
    skip_list+=("$service")
  fi
done 3< "$services_file"

build_csv=""
if [[ "${#build_list[@]}" -gt 0 ]]; then
  build_csv="$(IFS=,; printf '%s' "${build_list[*]}")"
fi

echo "oci-select: base=${base_resolved:0:12} head=${head_resolved:0:12} changed_files=${#changed_files[@]} build=${#build_list[@]} reuse=${#reuse_list[@]} skip=${#skip_list[@]}" >&2
if [[ "${#build_list[@]}" -gt 0 ]]; then
  echo "oci-select: build=[${build_csv}]" >&2
fi
if [[ "${#skip_list[@]}" -gt 0 ]]; then
  echo "oci-select: skip=[$(IFS=,; printf '%s' "${skip_list[*]}")]" >&2
fi

printf 'incremental\t%s\n' "$build_csv"
