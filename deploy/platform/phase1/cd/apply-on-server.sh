#!/usr/bin/env bash
# Run on the production host. Applies a staged release either by pulling immutable
# OCI digest references (`image-manifest.tsv`, zero `docker build` / `npm ci`) or by
# swapping new files into the local `<name>:deploy-base` images when `image-manifest.tsv`
# is absent. Each previous image is kept as `<name>:before-<timestamp>`.
# HVAC_REBUILD_DEPLOY_BASE=1 (the deploy workflow's rebuild_base input) only applies to
# tarball-swap releases and replaces every deploy-base with a verified one-layer copy first.
set -euo pipefail

release="${1:-}"
if [[ -z "$release" || ! -d "$release" ]]; then
  echo "error: usage: apply-on-server.sh <release-directory>" >&2
  exit 1
fi

platform_dir="${HVAC_PLATFORM_DIR:-/opt/hvac-platform}"
compose_project="${HVAC_COMPOSE_PROJECT:-hvac-platform}"
deploy_script="${HVAC_DEPLOY_SCRIPT:-}"
stamp="$(date -u +%Y%m%dT%H%M%SZ)"
rebuild_base="${HVAC_REBUILD_DEPLOY_BASE:-0}"
if [[ "$rebuild_base" != 0 && "$rebuild_base" != 1 ]]; then
  echo "error: HVAC_REBUILD_DEPLOY_BASE must be 0 or 1 (got '$rebuild_base')." >&2
  exit 1
fi

oci_mode=0
manifest_file="$release/image-manifest.tsv"
if [[ -s "$manifest_file" ]]; then
  oci_mode=1
fi

if ! command -v docker >/dev/null 2>&1; then
  echo "error: docker is not installed on the production host." >&2
  exit 1
fi
if [[ ! -f "$release/source.tar" || ! -f "$release/REVISION" || ! -f "$release/services.tsv" ]]; then
  echo "error: release directory is incomplete ($release)." >&2
  exit 1
fi
if [[ "$oci_mode" -ne 1 && ! -s "$release/bins/healthcheck" ]]; then
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

# Candidates are built FROM a stable <name>:deploy-base, never FROM the running image, so the
# swapped files do not pile up as new layers on every deploy. Each candidate records its base's
# image ID in the label below. An existing deploy-base tag wins, so a rollback to an image built on
# an older base does not bring that base back. Without the tag the label is used, and a running image
# without that label (built before this, or by hand) or whose recorded base is gone becomes the base
# once. Prints "<base tag><TAB><base image id>".
base_label=org.hvac.deploy-base
stable_base() {
  local image="$1"
  local base="${image%:*}:deploy-base"
  local recorded=""
  if recorded="$(docker image inspect --format '{{.Id}}' "$base" </dev/null 2>/dev/null)" && [[ -n "$recorded" ]]; then
    printf '%s\t%s\n' "$base" "$recorded"
    return
  fi
  recorded="$(docker image inspect --format "{{index .Config.Labels \"${base_label}\"}}" "$image" </dev/null 2>/dev/null || true)"
  if [[ "$recorded" == "<no value>" ]]; then
    recorded=""
  fi
  if [[ -n "$recorded" ]] && docker image inspect "$recorded" >/dev/null 2>&1 </dev/null; then
    docker tag "$recorded" "$base" </dev/null
  else
    echo "stable base: ${base} is the running ${image} from now on" >&2
    docker tag "$image" "$base" </dev/null
    recorded="$(docker image inspect --format '{{.Id}}' "$base" </dev/null)"
  fi
  printf '%s\t%s\n' "$base" "$recorded"
}

declare -A oci_ref_by_service=()
if [[ "$oci_mode" == 1 ]]; then
  while IFS=$'\t' read -r m_service m_local m_oci_ref <&4; do
    [[ -z "${m_service:-}" || "$m_service" == \#* ]] && continue
    if [[ -z "${m_oci_ref:-}" || ! "$m_oci_ref" =~ ^[^[:space:]]+@sha256:[0-9a-f]{64}$ ]]; then
      echo "error: image-manifest.tsv requires an immutable @sha256 digest for ${m_service}: '${m_oci_ref:-}'." >&2
      exit 1
    fi
    oci_ref_by_service["$m_service"]="$m_oci_ref"
  done 4< "$manifest_file"
  if [[ "${#oci_ref_by_service[@]}" -eq 0 ]]; then
    echo "error: image-manifest.tsv contains no OCI image entries." >&2
    exit 1
  fi
fi

declare -a planned_services=()
declare -a planned_images=()
declare -a planned_kinds=()
declare -a planned_previous=()
declare -a planned_oci_refs=()
declare -a planned_changed=()

while IFS=$'\t' read -r service image kind required package <&3; do
  [[ -z "${service:-}" || "$service" == \#* ]] && continue
  if [[ "$oci_mode" == 1 ]]; then
    oci_ref="${oci_ref_by_service[$service]:-}"
    if [[ -z "$oci_ref" ]]; then
      if [[ "$required" == required ]]; then
        echo "error: image-manifest.tsv is missing required service ${service}." >&2
        exit 1
      fi
      echo "skip ${service}: omitted from image-manifest.tsv"
      continue
    fi
    resolved="$(resolve_image "$service" "$image")"
    if [[ -z "$resolved" && "$required" != required && "${OCI_PULL_OPTIONAL:-0}" != 1 ]]; then
      echo "skip ${service}: optional service is not installed on this host"
      continue
    fi
    if [[ -z "$resolved" ]]; then
      resolved="$image"
    fi
    previous=""
    changed=1
    if docker image inspect "$resolved" >/dev/null 2>&1 </dev/null; then
      if docker image inspect --format '{{range .RepoDigests}}{{println .}}{{end}}' "$resolved" </dev/null 2>/dev/null | grep -Fxq "$oci_ref"; then
        changed=0
      else
        previous="$(rollback_tag "$resolved")"
      fi
    fi
    planned_services+=("$service")
    planned_images+=("$resolved")
    planned_kinds+=("$kind")
    planned_previous+=("$previous")
    planned_oci_refs+=("$oci_ref")
    planned_changed+=("$changed")
    continue
  fi

  if [[ "$kind" == mqtt ]]; then
    resolved="$image"
  else
    resolved="$(resolve_image "$service" "$image")"
    if [[ -z "$resolved" && "$kind" == static ]]; then
      resolved="$image"
    fi
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
  elif [[ "$kind" != static && "$kind" != mqtt ]]; then
    echo "error: refusing to replace ${resolved}; the image is not present." >&2
    exit 1
  fi
  planned_services+=("$service")
  planned_images+=("$resolved")
  planned_kinds+=("$kind")
  planned_previous+=("$previous")
  planned_oci_refs+=("")
  planned_changed+=(1)
done 3< "$release/services.tsv"

if [[ "${#planned_services[@]}" -eq 0 ]]; then
  echo "error: no images were selected." >&2
  exit 1
fi

declare -a candidates=()
declare -a staged_bases=()
cleanup_candidates() {
  local candidate
  for candidate in "${candidates[@]+"${candidates[@]}"}" "${staged_bases[@]+"${staged_bases[@]}"}"; do
    docker image rm "$candidate" >/dev/null 2>&1 || true
  done
}
trap cleanup_candidates EXIT

oci_pull_attempts="${OCI_PULL_ATTEMPTS:-3}"
oci_pull_retry_sleep="${OCI_PULL_RETRY_SLEEP:-3}"
docker_pull_with_retry() {
  local ref="$1"
  local attempt=1
  while true; do
    if docker pull "$ref" </dev/null; then
      return 0
    fi
    if (( attempt >= oci_pull_attempts )); then
      return 1
    fi
    local delay=$(( oci_pull_retry_sleep * attempt ))
    echo "warning: docker pull failed for ${ref} (attempt ${attempt}/${oci_pull_attempts}); retrying in ${delay}s" >&2
    sleep "$delay"
    attempt=$((attempt + 1))
  done
}

if [[ "$oci_mode" == 1 ]]; then
  any_oci_changed=0
  for index in "${!planned_services[@]}"; do
    if [[ "${planned_changed[$index]}" == 1 ]]; then
      any_oci_changed=1
      break
    fi
  done
  if [[ "$any_oci_changed" == 1 && -n "${OCI_REGISTRY_LOGIN_CMD:-}" ]]; then
    bash -c "${OCI_REGISTRY_LOGIN_CMD}"
  fi
  for index in "${!planned_services[@]}"; do
    service="${planned_services[$index]}"
    oci_ref="${planned_oci_refs[$index]}"
    if [[ "${planned_changed[$index]}" == 0 ]]; then
      echo "unchanged ${service}: already at ${oci_ref}"
      continue
    fi
    echo "pulling ${service}: ${oci_ref}"
    if ! docker_pull_with_retry "$oci_ref"; then
      echo "error: failed to pull OCI image for ${service} (${oci_ref}); nothing on host was changed." >&2
      exit 1
    fi
  done
else
# rebuild_base: build and verify every new base under <name>:rebuilt-<stamp> first, and move the
# deploy-base tags only when all of them match. The old bases are then untagged (image prune removes
# them after a healthy deploy); before-<stamp> tags and rollback are not touched.
if [[ "$rebuild_base" == 1 ]]; then
  rebuild_script="$release/rebuild-deploy-base.sh"
  if [[ ! -f "$rebuild_script" ]]; then
    echo "error: release is missing rebuild-deploy-base.sh." >&2
    exit 1
  fi
  declare -a rebuilt=()
  for index in "${!planned_services[@]}"; do
    [[ "${planned_kinds[$index]}" == static || "${planned_kinds[$index]}" == mqtt ]] && continue
    image="${planned_images[$index]}"
    staged="${image%:*}:rebuilt-${stamp}"
    staged_bases+=("$staged")
    if ! bash "$rebuild_script" "$image" "${planned_kinds[$index]}" "$staged" </dev/null; then
      echo "error: could not rebuild the deploy base of ${planned_services[$index]}; nothing was changed." >&2
      exit 1
    fi
    rebuilt+=("$index")
  done
  for index in "${rebuilt[@]+"${rebuilt[@]}"}"; do
    image="${planned_images[$index]}"
    docker tag "${image%:*}:rebuilt-${stamp}" "${image%:*}:deploy-base"
    echo "deploy base: ${image%:*}:deploy-base rebuilt from the running $image"
  done
fi

for index in "${!planned_services[@]}"; do
  service="${planned_services[$index]}"
  image="${planned_images[$index]}"
  kind="${planned_kinds[$index]}"
  candidate="${image%:*}:candidate-${stamp}"
  candidates+=("$candidate")
  from="$image"
  base_id=""
  if [[ "$kind" != static && "$kind" != mqtt ]]; then
    stable="$(stable_base "$image")"
    from="${stable%%$'\t'*}"
    base_id="${stable#*$'\t'}"
    if [[ -z "$base_id" || "$base_id" == "$stable" ]]; then
      echo "error: could not resolve a stable base image for $service." >&2
      exit 1
    fi
  fi
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
FROM ${from}
LABEL ${base_label}=${base_id}
COPY --chown=65532:65532 service /service
COPY --chown=65532:65532 healthcheck /healthcheck
EOF
      if [[ "$service" == connectivity ]]; then
        mkdir -p "$context/spool" "$context/ota-packages"
        cat >> "$context/Dockerfile" <<'EOF'
COPY --chown=65532:65532 spool /var/lib/hvac/connectivity-spool
COPY --chown=65532:65532 ota-packages /var/lib/hvac/ota-packages
EOF
      fi
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
FROM ${from}
LABEL ${base_label}=${base_id}
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
FROM ${from}
LABEL ${base_label}=${base_id}
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
      from="$(resolve_image alertmanager prom/alertmanager:v0.27.0)"
      if [[ -z "$from" ]]; then
        echo "error: $service is built FROM the local alertmanager image, which is not on this host." >&2
        exit 1
      fi
      cp "$release/bins/$service/service" "$context/service"
      chmod 755 "$context/service"
      cat > "$context/Dockerfile" <<EOF
FROM ${from}
COPY --chown=65532:65532 service /wecom-alert-adapter
USER 65532:65532
ENTRYPOINT ["/wecom-alert-adapter"]
EOF
      ;;
    mqtt)
      if [[ ! -s "$release/mqtt/mosquitto_hvac_crl.so" ]]; then
        echo "error: release is missing mqtt/mosquitto_hvac_crl.so." >&2
        exit 1
      fi
      from=""
      if docker image inspect eclipse-mosquitto:2.1.2-alpine >/dev/null 2>&1 </dev/null; then
        from="eclipse-mosquitto:2.1.2-alpine"
      else
        candidate_from="$(resolve_image "$service" eclipse-mosquitto:2.1.2-alpine)"
        if [[ -n "$candidate_from" && "$candidate_from" != "$image" ]]; then
          from="$candidate_from"
        fi
      fi
      if [[ -z "$from" ]]; then
        echo "error: $service is built FROM the local eclipse-mosquitto:2.1.2-alpine image, which is not on this host." >&2
        exit 1
      fi
      cp "$release/mqtt/mosquitto_hvac_crl.so" "$context/mosquitto_hvac_crl.so"
      chmod 755 "$context/mosquitto_hvac_crl.so"
      cat > "$context/Dockerfile" <<EOF
FROM ${from}
COPY --chown=0:0 mosquitto_hvac_crl.so /usr/lib/mosquitto_hvac_crl.so
RUN chmod 0755 /usr/lib/mosquitto_hvac_crl.so
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
FROM ${from}
LABEL ${base_label}=${base_id}
COPY opt/hvac /opt/hvac
RUN chmod 0555 /opt/hvac/migrations/run-phase1-migrations.sh /opt/hvac/migrations/verify-phase1-schema.sh
EOF
      ;;
    python)
      if [[ ! -f "$release/python/$service/ai_solver/main.py" || ! -f "$release/python/$service/pyproject.toml" ]]; then
        echo "error: release is missing python solver files for $service." >&2
        exit 1
      fi
      mkdir -p "$context/ai_solver"
      cp -a "$release/python/$service/ai_solver/." "$context/ai_solver/"
      cp "$release/python/$service/pyproject.toml" "$context/pyproject.toml"
      cat > "$context/Dockerfile" <<EOF
FROM ${from}
LABEL ${base_label}=${base_id}
USER root
WORKDIR /app
RUN rm -rf /app/ai_solver
COPY --chown=10001:10001 ai_solver ./ai_solver
COPY --chown=10001:10001 pyproject.toml ./pyproject.toml
USER 10001
EOF
      ;;
    *)
      echo "error: unknown image kind '$kind' for $service" >&2
      exit 1
      ;;
  esac
  echo "building $candidate from $from"
  if ! docker_build "$candidate" "$context"; then
    rm -rf "$context"
    exit 1
  fi
  rm -rf "$context"
done
fi

mkdir -p "$platform_dir"
tar -C "$release" -xf "$release/source.tar" --strip-components=4 deploy/platform/phase1/cd/observability-config.sh
bash "$release/observability-config.sh" check "$release/source.tar" "$platform_dir" "$compose_project"
tar -C "$platform_dir" -xf "$release/source.tar"
# Extraction only adds and overwrites. Delete alert rules and dashboards removed from the repository,
# so Prometheus does not keep loading them (check above validated exactly the release's set).
bash "$release/observability-config.sh" sync "$release/source.tar" "$platform_dir"
printf '%s\n' "$revision" > "$platform_dir/REVISION"
echo "updated $platform_dir to $revision"

for index in "${!planned_services[@]}"; do
  if [[ "$oci_mode" == 1 && "${planned_changed[$index]}" == 0 ]]; then
    echo "kept ${planned_images[$index]} (${planned_oci_refs[$index]})"
    continue
  fi
  if [[ -n "${planned_previous[$index]}" ]]; then
    docker tag "${planned_images[$index]}" "${planned_previous[$index]}"
  fi
  if [[ "$oci_mode" == 1 ]]; then
    docker tag "${planned_oci_refs[$index]}" "${planned_images[$index]}"
  else
    docker tag "${candidates[$index]}" "${planned_images[$index]}"
  fi
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
  if [[ "$oci_mode" == 1 && ( "${planned_changed[$index]}" == 0 || -z "${planned_previous[$index]}" ) ]]; then
    continue
  fi
  printf '%s\t%s\n' "${planned_images[$index]}" "${planned_previous[$index]}" >> "$manifest"
done
if [[ "$oci_mode" != 1 || -s "$manifest" ]]; then
  printf '%s\n' "$stamp" > "$platform_dir/rollbacks/latest"
else
  rm -f "$manifest"
fi

# The restart script: the deploy.sh in the unpacked platform tree, else the host-only copy it replaced.
# Under sudo the tree copy is used only when sudo already allows running it without a password.
default_deploy_script() {
  local bundled="$platform_dir/deploy/platform/phase1/cd/deploy.sh"
  if [[ -f "$bundled" && -x "$bundled" ]] && { [[ "$(id -u)" -eq 0 ]] || sudo -n -l "$bundled" >/dev/null 2>&1; }; then
    printf '%s\n' "$bundled"
  else
    printf '%s\n' /opt/hvac-platform-tools/deploy.sh
  fi
}
# Resolved after source.tar is unpacked, so a release brings its own restart script.
if [[ -z "$deploy_script" ]]; then
  deploy_script="$(default_deploy_script)"
fi
echo "restart script: $deploy_script"

run_deploy() {
  if [[ ! -e "$deploy_script" ]]; then
    echo "error: $deploy_script is missing." >&2
    echo "The release tree ships deploy/platform/phase1/cd/deploy.sh; the host fallback is /opt/hvac-platform-tools/deploy.sh. Images were updated; previous tags are before-${stamp}." >&2
    return 1
  fi
  # deploy.sh chmods runtime files owned by container users. The SSH user owns the tree; that chmod needs root.
  if [[ "$(id -u)" -eq 0 ]]; then
    if [[ -x "$deploy_script" ]]; then
      "$deploy_script" "$revision"
    else
      bash "$deploy_script" "$revision"
    fi
  elif [[ -x "$deploy_script" ]]; then
    sudo -n "$deploy_script" "$revision"
  else
    sudo -n bash "$deploy_script" "$revision"
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
if [[ "$oci_mode" == 1 ]]; then
  deployed_manifest_tmp="$(mktemp "$platform_dir/.deployed-image-manifest.XXXXXX")"
  {
    printf '# revision=%s\n' "$revision"
    cat "$manifest_file"
  } > "$deployed_manifest_tmp"
  mv -f "$deployed_manifest_tmp" "$platform_dir/deployed-image-manifest.tsv"
fi
bash "$release/observability-config.sh" apply "$platform_dir" "$compose_project"
if [[ "$oci_mode" == 1 ]]; then
  bash "$script_dir/prune-rollback-images.sh" 2 --prune-deploy-base
else
  bash "$script_dir/prune-rollback-images.sh" 2
fi

echo "deployed $revision"
echo "rollback tags use before-${stamp}"
