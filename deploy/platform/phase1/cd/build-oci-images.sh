#!/usr/bin/env bash
# Build and optionally push immutable OCI images for every service in services.tsv,
# then stage a lightweight control-only release (image-manifest.tsv + source.tar + CD scripts)
# so the target server never runs `docker build` or `npm ci`.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../../.." && pwd)"
cd "$root"

staging="${PRODUCTION_RELEASE_DIR:-$root/out/production-release}"
services="$root/deploy/platform/phase1/cd/services.tsv"
registry="${OCI_REGISTRY:-ccr.ccs.tencentyun.com}"
namespace="${OCI_NAMESPACE:-hvac}"
push_images="${OCI_PUSH:-1}"
services_filter="${OCI_SERVICES_FILTER:-}"
reuse_manifest="${OCI_REUSE_MANIFEST:-}"
incremental_mode="${OCI_INCREMENTAL:-0}"
build_services_csv="${OCI_BUILD_SERVICES:-}"
skip_absent_optional="${OCI_SKIP_ABSENT_OPTIONAL:-0}"
push_concurrency="${OCI_PUSH_CONCURRENCY:-4}"
go_runtime_image="${GO_RUNTIME_IMAGE:-gcr.io/distroless/static-debian12:nonroot}"
fake_digest="${OCI_FAKE_DIGEST:-}"

if [[ ! -f "$services" ]]; then
  echo "error: services catalog is missing: $services" >&2
  exit 1
fi
if [[ -z "$registry" || "$registry" =~ [[:space:]] ]]; then
  echo "error: OCI_REGISTRY must be non-empty without whitespace (got '$registry')." >&2
  exit 1
fi
if [[ -z "$namespace" || "$namespace" =~ [[:space:]] ]]; then
  echo "error: OCI_NAMESPACE must be non-empty without whitespace (got '$namespace')." >&2
  exit 1
fi
if [[ "$push_images" != 0 && "$push_images" != 1 ]]; then
  echo "error: OCI_PUSH must be 0 or 1 (got '$push_images')." >&2
  exit 1
fi
if [[ "$incremental_mode" != 0 && "$incremental_mode" != 1 ]]; then
  echo "error: OCI_INCREMENTAL must be 0 or 1 (got '$incremental_mode')." >&2
  exit 1
fi
if [[ "$skip_absent_optional" != 0 && "$skip_absent_optional" != 1 ]]; then
  echo "error: OCI_SKIP_ABSENT_OPTIONAL must be 0 or 1 (got '$skip_absent_optional')." >&2
  exit 1
fi
if [[ ! "$push_concurrency" =~ ^[1-9][0-9]*$ ]]; then
  echo "error: OCI_PUSH_CONCURRENCY must be a positive integer (got '$push_concurrency')." >&2
  exit 1
fi
if [[ -n "$fake_digest" && ! "$fake_digest" =~ ^sha256:[0-9a-f]{64}$ ]]; then
  echo "error: OCI_FAKE_DIGEST must match sha256:<64-hex> (got '$fake_digest')." >&2
  exit 1
fi

revision="$(git rev-parse HEAD | tr -d '[:space:]')"
if [[ ! "$revision" =~ ^[0-9a-f]{40}$ ]]; then
  echo "error: expected 40-char git revision, got '$revision'." >&2
  exit 1
fi
short_revision="${revision:0:12}"
oci_tag="${OCI_TAG:-sha-${short_revision}}"
if [[ -z "$oci_tag" || "$oci_tag" =~ [[:space:]] ]]; then
  echo "error: OCI_TAG must be non-empty without whitespace (got '$oci_tag')." >&2
  exit 1
fi

if ! command -v docker >/dev/null 2>&1; then
  echo "error: docker is required to build OCI release images." >&2
  exit 1
fi

service_selected() {
  local target="$1"
  [[ -z "$services_filter" ]] && return 0
  local IFS=','
  local entry
  for entry in $services_filter; do
    entry="${entry#"${entry%%[![:space:]]*}"}"
    entry="${entry%"${entry##*[![:space:]]}"}"
    if [[ "$entry" == "$target" ]]; then
      return 0
    fi
  done
  return 1
}

repo_name_for_image() {
  local local_image="$1"
  local without_registry="$local_image"
  if [[ "$without_registry" == */* ]]; then
    without_registry="${without_registry#*/}"
  fi
  printf '%s\n' "${without_registry%%:*}"
}

resolve_pushed_digest_ref() {
  local oci_repo="$1"
  local tag_ref="$2"
  local digest_line=""
  if [[ "$push_images" == 1 ]]; then
    local candidate
    while IFS= read -r candidate; do
      [[ -z "$candidate" ]] && continue
      if [[ "$candidate" == "${oci_repo}@sha256:"* ]]; then
        digest_line="$candidate"
        break
      fi
    done < <(docker inspect --format '{{range .RepoDigests}}{{println .}}{{end}}' "$tag_ref" 2>/dev/null || true)
    if [[ ! "$digest_line" =~ ^[^[:space:]]+@sha256:[0-9a-f]{64}$ ]]; then
      echo "error: missing immutable sha256 RepoDigest for pushed image ${tag_ref}" >&2
      exit 1
    fi
    printf '%s\n' "$digest_line"
    return
  fi

  if [[ -n "$fake_digest" ]]; then
    printf '%s@%s\n' "$oci_repo" "$fake_digest"
    return
  fi
  local existing=""
  existing="$(docker inspect --format '{{index .RepoDigests 0}}' "$tag_ref" 2>/dev/null || true)"
  if [[ "$existing" =~ ^[^[:space:]]+@sha256:[0-9a-f]{64}$ ]]; then
    printf '%s\n' "$existing"
    return
  fi
  printf '%s\n' "$tag_ref"
}

bash "$root/deploy/platform/phase1/cd/build-release.sh" --check-migrator-list

work="$(mktemp -d)"
cleanup_work() {
  rm -rf "$work"
}
trap cleanup_work EXIT

rm -rf "$staging"
mkdir -p "$staging"
printf '%s\n' "$revision" > "$staging/REVISION"
git archive --format=tar -o "$staging/source.tar" HEAD
cp "$services" "$staging/services.tsv"
for script in apply-on-server.sh rebuild-deploy-base.sh rollback.sh smoke-release.sh prune-rollback-images.sh deploy.sh; do
  cp "$root/deploy/platform/phase1/cd/$script" "$staging/$script"
  chmod 755 "$staging/$script"
done

healthcheck_bin="$work/healthcheck"
shared_var_lib_hvac="$work/shared-var-lib-hvac"
shared_zoneinfo="$work/shared-zoneinfo"
healthcheck_built=0
var_lib_ready=0
go_runtime_ready=0
zero_mtime() {
  local target="$1"
  touch -d @0 "$target" 2>/dev/null || touch -t 197001010000 "$target" 2>/dev/null || true
}
ensure_go_toolchain() {
  if ! command -v go >/dev/null 2>&1; then
    echo "error: go is required on PATH to build OCI Go images." >&2
    exit 1
  fi
}
ensure_go_runtime() {
  if [[ "$go_runtime_ready" -eq 1 ]]; then
    return
  fi
  if ! docker image inspect "${go_runtime_image}" >/dev/null 2>&1; then
    docker pull --platform linux/amd64 "${go_runtime_image}" || true
  fi
  go_runtime_ready=1
}
ensure_healthcheck() {
  if [[ "$healthcheck_built" -eq 1 ]]; then
    return
  fi
  ensure_go_toolchain
  echo "building container healthcheck binary"
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -buildvcs=false -ldflags="-s -w" \
    -o "$healthcheck_bin" \
    ./tools/container-healthcheck/cmd/container-healthcheck/main.go
  zero_mtime "$healthcheck_bin"
  healthcheck_built=1
}
ensure_var_lib_hvac() {
  if [[ "$var_lib_ready" -eq 1 ]]; then
    return
  fi
  mkdir -p "$shared_var_lib_hvac/connectivity-spool" "$shared_var_lib_hvac/mqtt-crl" "$shared_var_lib_hvac/ota-packages"
  chmod 755 "$shared_var_lib_hvac" "$shared_var_lib_hvac/connectivity-spool" "$shared_var_lib_hvac/mqtt-crl" "$shared_var_lib_hvac/ota-packages"
  zero_mtime "$shared_var_lib_hvac/connectivity-spool"
  zero_mtime "$shared_var_lib_hvac/mqtt-crl"
  zero_mtime "$shared_var_lib_hvac/ota-packages"
  zero_mtime "$shared_var_lib_hvac"
  var_lib_ready=1
}

oci_push_attempts="${OCI_PUSH_ATTEMPTS:-3}"
oci_push_retry_sleep="${OCI_PUSH_RETRY_SLEEP:-5}"
if [[ ! "$oci_push_attempts" =~ ^[1-9][0-9]*$ ]]; then
  echo "error: OCI_PUSH_ATTEMPTS must be a positive integer (got '$oci_push_attempts')." >&2
  exit 1
fi
docker_push_with_retry() {
  local ref="$1"
  local attempt=1
  while true; do
    if docker push "$ref"; then
      return 0
    fi
    if (( attempt >= oci_push_attempts )); then
      echo "error: docker push failed for ${ref} after ${attempt} attempts" >&2
      return 1
    fi
    local delay=$(( oci_push_retry_sleep * attempt ))
    echo "warning: docker push failed for ${ref} (attempt ${attempt}/${oci_push_attempts}); retrying in ${delay}s" >&2
    sleep "$delay"
    attempt=$((attempt + 1))
  done
}

declare -A build_service_set=()
if [[ "$incremental_mode" == 1 && -n "$build_services_csv" ]]; then
  IFS=',' read -r -a _build_entries <<< "$build_services_csv"
  for _entry in "${_build_entries[@]}"; do
    _entry="${_entry#"${_entry%%[![:space:]]*}"}"
    _entry="${_entry%"${_entry##*[![:space:]]}"}"
    [[ -n "$_entry" ]] && build_service_set["$_entry"]=1
  done
fi

declare -A reuse_ref_by_service=()
if [[ -n "$reuse_manifest" && -f "$reuse_manifest" ]]; then
  while IFS=$'\t' read -r m_service _m_local m_oci_ref; do
    [[ -z "${m_service:-}" || "$m_service" == \#* ]] && continue
    if [[ -n "${m_oci_ref:-}" ]]; then
      reuse_ref_by_service["$m_service"]="$m_oci_ref"
    fi
  done < "$reuse_manifest"
fi

valid_reuse_digest_ref() {
  local svc="$1" oci_repo="$2"
  local cand="${reuse_ref_by_service[$svc]:-}"
  [[ -n "$cand" ]] || return 1
  local prefix="${oci_repo}@sha256:"
  if [[ "$cand" != "${prefix}"* ]]; then
    return 1
  fi
  local suffix="${cand#"$prefix"}"
  [[ "$suffix" =~ ^[0-9a-f]{64}$ ]] || return 1
  printf '%s\n' "$cand"
}

manifest_tmp="$work/image-manifest.tsv"
printf '# service\tlocal_image\toci_ref\n' > "$manifest_tmp"

declare -a planned_services=()
declare -a planned_locals=()
declare -a planned_repos=()
declare -a planned_tags=()
declare -a planned_refs=()
declare -a push_indices=()
built_count=0
reused_count=0

while IFS=$'\t' read -r service local_image kind required package <&3; do
  [[ -z "${service:-}" || "$service" == \#* ]] && continue
  if ! service_selected "$service"; then
    continue
  fi

  repo_name="$(repo_name_for_image "$local_image")"
  oci_repo="${registry}/${namespace}/${repo_name}"
  oci_tag_ref="${oci_repo}:${oci_tag}"

  should_build=1
  reused_ref=""
  if [[ "$incremental_mode" == 1 ]]; then
    if [[ -n "${build_service_set[$service]:-}" ]]; then
      should_build=1
    elif reused_ref="$(valid_reuse_digest_ref "$service" "$oci_repo" || true)" && [[ -n "$reused_ref" ]]; then
      should_build=0
    elif [[ "$required" != required && "$skip_absent_optional" == 1 ]]; then
      echo "== skipping absent optional OCI image ${service}"
      continue
    else
      echo "warning: no valid reuse digest for ${service}; building fresh OCI image" >&2
      should_build=1
    fi
  fi

  plan_idx="${#planned_services[@]}"
  planned_services+=("$service")
  planned_locals+=("$local_image")
  planned_repos+=("$oci_repo")
  planned_tags+=("$oci_tag_ref")

  if [[ "$should_build" == 0 ]]; then
    echo "== reusing OCI image ${service} -> ${reused_ref}"
    planned_refs+=("$reused_ref")
    reused_count=$((reused_count + 1))
    continue
  fi

  svc_ctx="$work/ctx-${service}"
  rm -rf "$svc_ctx"
  mkdir -p "$svc_ctx"

  echo "== building OCI image ${service} -> ${oci_tag_ref} (kind=${kind})"
  case "$kind" in
    go)
      if [[ ! -e "$package" ]]; then
        echo "error: Go package for $service does not exist: $package" >&2
        exit 1
      fi
      ensure_go_toolchain
      ensure_go_runtime
      ensure_healthcheck
      ensure_var_lib_hvac
      CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
        go build -trimpath -buildvcs=false -ldflags="-s -w" \
        -o "$svc_ctx/service" \
        "$package"
      cp -p "$healthcheck_bin" "$svc_ctx/healthcheck"
      chmod 755 "$svc_ctx/service" "$svc_ctx/healthcheck"
      zero_mtime "$svc_ctx/healthcheck"
      cp -a "$shared_var_lib_hvac" "$svc_ctx/var-lib-hvac"
      cat > "$svc_ctx/Dockerfile" <<EOF
FROM ${go_runtime_image}
WORKDIR /
EOF
      if [[ -d /usr/share/zoneinfo ]]; then
        if [[ ! -d "$shared_zoneinfo" ]]; then
          mkdir -p "$shared_zoneinfo"
          cp -a /usr/share/zoneinfo/. "$shared_zoneinfo/"
        fi
        mkdir -p "$svc_ctx/zoneinfo"
        cp -a "$shared_zoneinfo/." "$svc_ctx/zoneinfo/"
        echo "COPY zoneinfo /usr/share/zoneinfo" >> "$svc_ctx/Dockerfile"
      fi
      cat >> "$svc_ctx/Dockerfile" <<EOF
COPY --chown=65532:65532 var-lib-hvac /var/lib/hvac
COPY --chown=65532:65532 healthcheck /healthcheck
COPY --chown=65532:65532 service /service
LABEL org.opencontainers.image.title=${service}
LABEL org.opencontainers.image.revision=${revision}
USER 65532:65532
ENTRYPOINT ["/service"]
EOF
      DOCKER_BUILDKIT=1 docker build --platform linux/amd64 -t "$oci_tag_ref" "$svc_ctx"
      ;;
    static)
      if [[ ! -e "$package" ]]; then
        echo "error: static Go package for $service does not exist: $package" >&2
        exit 1
      fi
      ensure_go_toolchain
      ensure_go_runtime
      CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
        go build -trimpath -buildvcs=false -ldflags="-s -w" \
        -o "$svc_ctx/service" \
        "$package"
      chmod 755 "$svc_ctx/service"
      cat > "$svc_ctx/Dockerfile" <<EOF
FROM ${go_runtime_image}
LABEL org.opencontainers.image.title=${service}
LABEL org.opencontainers.image.revision=${revision}
COPY --chown=65532:65532 service /wecom-alert-adapter
USER 65532:65532
ENTRYPOINT ["/wecom-alert-adapter"]
EOF
      DOCKER_BUILDKIT=1 docker build --platform linux/amd64 -t "$oci_tag_ref" "$svc_ctx"
      ;;
    web)
      DOCKER_BUILDKIT=1 docker build --platform linux/amd64 \
        -f "$root/deploy/platform/phase1/nginx/Dockerfile" \
        --build-arg "HVAC_WEB_BUILD_ID=${short_revision}" \
        --build-arg "HVAC_WEB_GATEWAY_BASE_PATH=${HVAC_WEB_GATEWAY_BASE_PATH:-/api/v1}" \
        --build-arg "HVAC_WEB_REALTIME_PROTOCOL=${HVAC_WEB_REALTIME_PROTOCOL:-centrifugo-v1}" \
        --label "org.opencontainers.image.title=${service}" \
        --label "org.opencontainers.image.revision=${revision}" \
        -t "$oci_tag_ref" \
        "$root"
      ;;
    node)
      DOCKER_BUILDKIT=1 docker build --platform linux/amd64 \
        -f "$root/deploy/platform/phase1/images/operations-agent.Dockerfile" \
        --label "org.opencontainers.image.title=${service}" \
        --label "org.opencontainers.image.revision=${revision}" \
        -t "$oci_tag_ref" \
        "$root"
      ;;
    migrator)
      DOCKER_BUILDKIT=1 docker build --platform linux/amd64 \
        -f "$root/deploy/platform/phase1/migrations/Dockerfile" \
        --label "org.opencontainers.image.title=${service}" \
        --label "org.opencontainers.image.revision=${revision}" \
        -t "$oci_tag_ref" \
        "$root"
      ;;
    mqtt)
      DOCKER_BUILDKIT=1 docker build --platform linux/amd64 \
        -f "$root/deploy/platform/phase1/mqtt/Dockerfile" \
        --label "org.opencontainers.image.title=${service}" \
        --label "org.opencontainers.image.revision=${revision}" \
        -t "$oci_tag_ref" \
        "$root"
      ;;
    python)
      if [[ -z "$package" || ! -f "$root/$package/Dockerfile" ]]; then
        echo "error: Python service Dockerfile missing for $service ($package)" >&2
        exit 1
      fi
      DOCKER_BUILDKIT=1 docker build --platform linux/amd64 \
        -f "$root/$package/Dockerfile" \
        --label "org.opencontainers.image.title=${service}" \
        --label "org.opencontainers.image.revision=${revision}" \
        -t "$oci_tag_ref" \
        "$root"
      ;;
    *)
      echo "error: unknown service kind '$kind' for $service" >&2
      exit 1
      ;;
  esac
  rm -rf "$svc_ctx"

  planned_refs+=("")
  push_indices+=("$plan_idx")
  built_count=$((built_count + 1))
done 3< "$services"

if [[ "${#planned_services[@]}" -eq 0 ]]; then
  echo "error: no services matched OCI_SERVICES_FILTER='$services_filter'." >&2
  exit 1
fi

if [[ "${#push_indices[@]}" -gt 0 ]]; then
  if [[ "$push_images" != 1 || "${#push_indices[@]}" -le 1 || "$push_concurrency" -le 1 ]]; then
    for idx in "${push_indices[@]}"; do
      if [[ "$push_images" == 1 ]]; then
        echo "pushing ${planned_tags[$idx]}"
        docker_push_with_retry "${planned_tags[$idx]}"
      fi
      planned_refs[$idx]="$(resolve_pushed_digest_ref "${planned_repos[$idx]}" "${planned_tags[$idx]}")"
    done
  else
    push_dir="$work/push"
    mkdir -p "$push_dir"
    declare -a active_pids=()
    declare -A pid_to_idx=()
    declare -A idx_push_rc=()

    reap_done_pushes() {
      local remaining=()
      local pid="" r_idx="" rc=0
      for pid in "${active_pids[@]+"${active_pids[@]}"}"; do
        r_idx="${pid_to_idx[$pid]}"
        if [[ -f "$push_dir/${r_idx}.done" ]]; then
          set +e
          wait "$pid"
          rc=$?
          set -e
          idx_push_rc["$r_idx"]="$rc"
        else
          remaining+=("$pid")
        fi
      done
      active_pids=("${remaining[@]+"${remaining[@]}"}")
    }

    echo "== pushing ${#push_indices[@]} OCI images (concurrency=${push_concurrency})"
    for idx in "${push_indices[@]}"; do
      while (( "${#active_pids[@]}" >= push_concurrency )); do
        reap_done_pushes
        if (( "${#active_pids[@]}" >= push_concurrency )); then
          sleep 0.1
        fi
      done

      p_repo="${planned_repos[$idx]}"
      p_tag="${planned_tags[$idx]}"
      p_log="$push_dir/${idx}.log"
      p_digest="$push_dir/${idx}.digest"
      p_done="$push_dir/${idx}.done"
      (
        p_rc=0
        {
          echo "pushing ${p_tag}"
          if ! docker_push_with_retry "$p_tag"; then
            p_rc=1
          elif ! ( resolve_pushed_digest_ref "$p_repo" "$p_tag" > "$p_digest" ); then
            p_rc=1
          fi
        } > "$p_log" 2>&1 || p_rc=1
        : > "$p_done"
        exit "$p_rc"
      ) &
      pid=$!
      active_pids+=("$pid")
      pid_to_idx["$pid"]="$idx"
    done

    for pid in "${active_pids[@]+"${active_pids[@]}"}"; do
      r_idx="${pid_to_idx[$pid]}"
      set +e
      wait "$pid"
      rc=$?
      set -e
      idx_push_rc["$r_idx"]="$rc"
    done

    failed_push=0
    for idx in "${push_indices[@]}"; do
      cat "$push_dir/${idx}.log"
      if [[ "${idx_push_rc[$idx]:-1}" -ne 0 ]]; then
        echo "error: parallel OCI push failed for ${planned_services[$idx]} (${planned_tags[$idx]})" >&2
        failed_push=1
      elif [[ ! -s "$push_dir/${idx}.digest" ]]; then
        echo "error: missing resolved digest for ${planned_services[$idx]} (${planned_tags[$idx]})" >&2
        failed_push=1
      else
        planned_refs[$idx]="$(tr -d '[:space:]' < "$push_dir/${idx}.digest")"
      fi
    done
    if [[ "$failed_push" -ne 0 ]]; then
      exit 1
    fi
  fi
fi

for idx in "${!planned_services[@]}"; do
  if [[ -z "${planned_refs[$idx]}" ]]; then
    echo "error: missing final OCI reference for ${planned_services[$idx]}" >&2
    exit 1
  fi
  printf '%s\t%s\t%s\n' "${planned_services[$idx]}" "${planned_locals[$idx]}" "${planned_refs[$idx]}" >> "$manifest_tmp"
done

cp "$manifest_tmp" "$staging/image-manifest.tsv"
echo "OCI control-only release staged at $staging (services=${#planned_services[@]}, built=${built_count}, reused=${reused_count}, tag=${oci_tag}, revision=${short_revision})"
