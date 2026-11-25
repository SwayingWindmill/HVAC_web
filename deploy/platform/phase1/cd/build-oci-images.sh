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
healthcheck_built=0
go_runtime_ready=0
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
  healthcheck_built=1
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

manifest_tmp="$work/image-manifest.tsv"
printf '# service\tlocal_image\toci_ref\n' > "$manifest_tmp"
built_count=0

while IFS=$'\t' read -r service local_image kind required package <&3; do
  [[ -z "${service:-}" || "$service" == \#* ]] && continue
  if ! service_selected "$service"; then
    continue
  fi

  repo_name="$(repo_name_for_image "$local_image")"
  oci_repo="${registry}/${namespace}/${repo_name}"
  oci_tag_ref="${oci_repo}:${oci_tag}"
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
      CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
        go build -trimpath -buildvcs=false -ldflags="-s -w" \
        -o "$svc_ctx/service" \
        "$package"
      cp "$healthcheck_bin" "$svc_ctx/healthcheck"
      chmod 755 "$svc_ctx/service" "$svc_ctx/healthcheck"
      mkdir -p "$svc_ctx/spool" "$svc_ctx/mqtt-crl" "$svc_ctx/ota-packages"
      cat > "$svc_ctx/Dockerfile" <<EOF
FROM ${go_runtime_image}
LABEL org.opencontainers.image.title=${service}
LABEL org.opencontainers.image.revision=${revision}
WORKDIR /
EOF
      if [[ -d /usr/share/zoneinfo ]]; then
        mkdir -p "$svc_ctx/zoneinfo"
        cp -a /usr/share/zoneinfo/. "$svc_ctx/zoneinfo/"
        echo "COPY zoneinfo /usr/share/zoneinfo" >> "$svc_ctx/Dockerfile"
      fi
      cat >> "$svc_ctx/Dockerfile" <<'EOF'
COPY --chown=65532:65532 service /service
COPY --chown=65532:65532 healthcheck /healthcheck
COPY --chown=65532:65532 spool /var/lib/hvac/connectivity-spool
COPY --chown=65532:65532 mqtt-crl /var/lib/hvac/mqtt-crl
COPY --chown=65532:65532 ota-packages /var/lib/hvac/ota-packages
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

  if [[ "$push_images" == 1 ]]; then
    echo "pushing ${oci_tag_ref}"
    docker_push_with_retry "$oci_tag_ref"
  fi
  oci_ref="$(resolve_pushed_digest_ref "$oci_repo" "$oci_tag_ref")"
  printf '%s\t%s\t%s\n' "$service" "$local_image" "$oci_ref" >> "$manifest_tmp"
  built_count=$((built_count + 1))
done 3< "$services"

if [[ "$built_count" -eq 0 ]]; then
  echo "error: no services matched OCI_SERVICES_FILTER='$services_filter'." >&2
  exit 1
fi

cp "$manifest_tmp" "$staging/image-manifest.tsv"
echo "OCI control-only release staged at $staging (services=$built_count, tag=${oci_tag}, revision=${short_revision})"
