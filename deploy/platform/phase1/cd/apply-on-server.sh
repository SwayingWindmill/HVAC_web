#!/usr/bin/env bash
# Run on the production host. Swap new files into the local images, then restart.
# Images are never pulled. Each previous image is kept as <name>:before-<timestamp>.
set -euo pipefail

release="${1:-}"
if [[ -z "$release" || ! -d "$release" ]]; then
  echo "error: usage: apply-on-server.sh <release-directory>" >&2
  exit 1
fi

platform_dir="${HVAC_PLATFORM_DIR:-/opt/hvac-platform}"
compose_project="${HVAC_COMPOSE_PROJECT:-hvac-platform}"
deploy_script="${HVAC_DEPLOY_SCRIPT:-/opt/hvac-platform-tools/deploy.sh}"
stamp="$(date -u +%Y%m%dT%H%M%SZ)"

if ! command -v docker >/dev/null 2>&1; then
  echo "error: docker is not installed on the production host." >&2
  exit 1
fi
if [[ ! -f "$release/source.tar" || ! -f "$release/REVISION" || ! -f "$release/services.tsv" ]]; then
  echo "error: release directory is incomplete ($release)." >&2
  exit 1
fi
if [[ ! -s "$release/bins/healthcheck" ]]; then
  echo "error: release is missing the container healthcheck binary." >&2
  exit 1
fi

revision="$(tr -d '[:space:]' < "$release/REVISION")"
if [[ ! "$revision" =~ ^[0-9a-f]{40}$ ]]; then
  echo "error: REVISION is not a full git commit: $revision" >&2
  exit 1
fi

resolve_image() {
  local service="$1"
  local fallback="$2"
  local cid=""
  cid="$(docker ps -aq \
    --filter "label=com.docker.compose.project=${compose_project}" \
    --filter "label=com.docker.compose.service=${service}" </dev/null | head -n 1 || true)"
  if [[ -n "$cid" ]]; then
    docker inspect --format '{{.Config.Image}}' "$cid" </dev/null
    return
  fi
  if docker image inspect "$fallback" >/dev/null 2>&1 </dev/null; then
    printf '%s\n' "$fallback"
    return
  fi
  printf '\n'
}

rollback_tag() {
  local image="$1"
  local name="${image%:*}"
  if [[ "$image" != *:* || "$name" == "$image" ]]; then
    echo "error: refusing to retag an image without a name:tag reference: $image" >&2
    exit 1
  fi
  if [[ "$image" == *@* ]]; then
    echo "error: $image is pinned by digest. This deploy only replaces local name:tag images." >&2
    exit 1
  fi
  printf '%s:before-%s\n' "$name" "$stamp"
}

docker_build() {
  local tag="$1"
  local context="$2"
  DOCKER_BUILDKIT=1 docker build --pull=false -t "$tag" "$context"
}

declare -a planned_services=()
declare -a planned_images=()
declare -a planned_kinds=()
declare -a planned_previous=()

while IFS=$'\t' read -r service image kind required package <&3; do
  [[ -z "${service:-}" || "$service" == \#* ]] && continue
  resolved="$(resolve_image "$service" "$image")"
  if [[ -z "$resolved" && "$kind" == static ]]; then
    resolved="$image"
  fi
  if [[ -z "$resolved" ]]; then
    if [[ "$required" == required ]]; then
      echo "error: no local image for ${service}." >&2
      echo "Looked for a ${compose_project} container and for ${image}. This deploy does not docker pull." >&2
      exit 1
    fi
    echo "skip ${service}: ${image} is not on this host"
    continue
  fi
  previous=""
  if docker image inspect "$resolved" >/dev/null 2>&1 </dev/null; then
    previous="$(rollback_tag "$resolved")"
  elif [[ "$kind" != static ]]; then
    echo "error: refusing to replace ${resolved}; the image is not present." >&2
    exit 1
  fi
  planned_services+=("$service")
  planned_images+=("$resolved")
  planned_kinds+=("$kind")
  planned_previous+=("$previous")
done 3< "$release/services.tsv"

if [[ "${#planned_services[@]}" -eq 0 ]]; then
  echo "error: no images were selected." >&2
  exit 1
fi

declare -a candidates=()
cleanup_candidates() {
  local candidate
  for candidate in "${candidates[@]+"${candidates[@]}"}"; do
    docker image rm "$candidate" >/dev/null 2>&1 || true
  done
}
trap cleanup_candidates EXIT

for index in "${!planned_services[@]}"; do
  service="${planned_services[$index]}"
  image="${planned_images[$index]}"
  kind="${planned_kinds[$index]}"
  candidate="${image%:*}:candidate-${stamp}"
  candidates+=("$candidate")
  context="$(mktemp -d)"
  case "$kind" in
    go)
      if [[ ! -s "$release/bins/$service/service" ]]; then
        echo "error: release is missing the $service binary." >&2
        exit 1
      fi
      cp "$release/bins/$service/service" "$context/service"
      cp "$release/bins/healthcheck" "$context/healthcheck"
      chmod 755 "$context/service" "$context/healthcheck"
      cat > "$context/Dockerfile" <<EOF
FROM ${image}
COPY --chown=65532:65532 service /service
COPY --chown=65532:65532 healthcheck /healthcheck
EOF
      ;;
    web)
      if [[ ! -f "$release/web/dist/index.html" || ! -f "$release/web/nginx.conf" ]]; then
        echo "error: release is missing the web dist or nginx config." >&2
        exit 1
      fi
      mkdir -p "$context/dist"
      cp -a "$release/web/dist/." "$context/dist/"
      cp "$release/web/nginx.conf" "$context/nginx.conf"
      cat > "$context/Dockerfile" <<EOF
FROM ${image}
RUN rm -rf /usr/share/nginx/html && mkdir -p /usr/share/nginx/html
COPY dist/ /usr/share/nginx/html/
COPY nginx.conf /etc/nginx/conf.d/default.conf
EOF
      ;;
    node)
      # The running image is node:22-bookworm-slim, which already has npm.
      # Install production dependencies here so the release does not upload node_modules.
      if [[ ! -f "$release/agent/dist/bootstrap/main.js" || ! -f "$release/agent/package.json" || ! -f "$release/agent/package-lock.json" ]]; then
        echo "error: release is missing the operations-agent build or lockfile." >&2
        exit 1
      fi
      if [[ -d "$release/agent/node_modules" ]]; then
        echo "error: release still contains agent/node_modules." >&2
        exit 1
      fi
      mkdir -p "$context/dist"
      cp "$release/agent/package.json" "$context/package.json"
      cp "$release/agent/package-lock.json" "$context/package-lock.json"
      cp -a "$release/agent/dist/." "$context/dist/"
      cat > "$context/Dockerfile" <<EOF
FROM ${image}
USER root
WORKDIR /app
COPY package.json package-lock.json ./
COPY dist ./dist
RUN rm -rf /app/node_modules \
 && npm ci --omit=dev --no-audit --no-fund --fetch-retries=5 \
 && npm cache clean --force \
 && rm -rf /root/.npm \
 && chown -R node:node /app
USER node
EOF
      ;;
    static)
      if [[ "$service" != wecom-alert-adapter ]]; then
        echo "error: static images are only defined for wecom-alert-adapter (got $service)." >&2
        exit 1
      fi
      if [[ ! -s "$release/bins/$service/service" ]]; then
        echo "error: release is missing the $service binary." >&2
        exit 1
      fi
      base="$(resolve_image alertmanager prom/alertmanager:v0.27.0)"
      if [[ -z "$base" ]]; then
        echo "error: $service is built FROM the local alertmanager image, which is not on this host." >&2
        exit 1
      fi
      cp "$release/bins/$service/service" "$context/service"
      chmod 755 "$context/service"
      cat > "$context/Dockerfile" <<EOF
FROM ${base}
COPY --chown=65532:65532 service /wecom-alert-adapter
USER 65532:65532
ENTRYPOINT ["/wecom-alert-adapter"]
EOF
      ;;
    migrator)
      if [[ ! -f "$release/migrator/opt/hvac/migrations/migration-list.tsv" ]]; then
        echo "error: release is missing the migrator files." >&2
        exit 1
      fi
      mkdir -p "$context/opt"
      cp -a "$release/migrator/opt/." "$context/opt/"
      cat > "$context/Dockerfile" <<EOF
FROM ${image}
COPY opt/hvac /opt/hvac
RUN chmod 0555 /opt/hvac/migrations/run-phase1-migrations.sh /opt/hvac/migrations/verify-phase1-schema.sh
EOF
      ;;
    *)
      echo "error: unknown image kind '$kind' for $service" >&2
      exit 1
      ;;
  esac
  echo "building $candidate from $image"
  if ! docker_build "$candidate" "$context"; then
    rm -rf "$context"
    exit 1
  fi
  rm -rf "$context"
done

mkdir -p "$platform_dir"
tar -C "$release" -xf "$release/source.tar" --strip-components=4 deploy/platform/phase1/cd/observability-config.sh
bash "$release/observability-config.sh" check "$release/source.tar" "$platform_dir" "$compose_project"
tar -C "$platform_dir" -xf "$release/source.tar"
printf '%s\n' "$revision" > "$platform_dir/REVISION"
echo "updated $platform_dir to $revision"

for index in "${!planned_services[@]}"; do
  if [[ -n "${planned_previous[$index]}" ]]; then
    docker tag "${planned_images[$index]}" "${planned_previous[$index]}"
  fi
  docker tag "${candidates[$index]}" "${planned_images[$index]}"
  if [[ -n "${planned_previous[$index]}" ]]; then
    echo "replaced ${planned_images[$index]} (previous ${planned_previous[$index]})"
  else
    echo "created ${planned_images[$index]}"
  fi
done
trap - EXIT
cleanup_candidates

script_dir="$release"
if [[ ! -x "$script_dir/rollback.sh" || ! -x "$script_dir/smoke-release.sh" || ! -x "$script_dir/prune-rollback-images.sh" ]]; then
  script_dir="$platform_dir/deploy/platform/phase1/cd"
fi
mkdir -p "$platform_dir/rollbacks"
manifest="$platform_dir/rollbacks/${stamp}.tsv"
: > "$manifest"
for index in "${!planned_services[@]}"; do
  printf '%s\t%s\n' "${planned_images[$index]}" "${planned_previous[$index]}" >> "$manifest"
done
printf '%s\n' "$stamp" > "$platform_dir/rollbacks/latest"

run_deploy() {
  if [[ ! -e "$deploy_script" ]]; then
    echo "error: $deploy_script is missing." >&2
    echo "The production host keeps the Phase 1 restart script there. Images were updated; previous tags are before-${stamp}." >&2
    return 1
  fi
  # deploy.sh chmods runtime files owned by container users. The SSH user owns the tree; that chmod needs root.
  if [[ "$(id -u)" -eq 0 ]]; then
    if [[ -x "$deploy_script" ]]; then
      "$deploy_script"
    else
      bash "$deploy_script"
    fi
  elif [[ -x "$deploy_script" ]]; then
    sudo -n "$deploy_script"
  else
    sudo -n bash "$deploy_script"
  fi
}

rollback_now() {
  echo "error: $1 Rolling back to before-${stamp}." >&2
  HVAC_PLATFORM_DIR="$platform_dir" HVAC_DEPLOY_SCRIPT="$deploy_script" bash "$script_dir/rollback.sh" "$stamp" || true
}

if ! run_deploy; then
  rollback_now "restart failed."
  exit 1
fi
if ! SMOKE_RESOLVE_IP=127.0.0.1 SMOKE_CHECK_CONTAINERS=1 bash "$script_dir/smoke-release.sh"; then
  rollback_now "smoke checks failed."
  exit 1
fi
bash "$release/observability-config.sh" apply "$platform_dir" "$compose_project"
bash "$script_dir/prune-rollback-images.sh" 2

echo "deployed $revision"
echo "rollback tags use before-${stamp}"
