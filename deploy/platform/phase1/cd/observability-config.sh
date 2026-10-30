#!/usr/bin/env bash
# Make a deploy actually apply changed Prometheus and Alertmanager config.
#
# Compose bind-mounts prometheus.yaml, alertmanager.yaml and the two render scripts as single files.
# Extracting source.tar replaces those files (new inode), so a running container keeps reading the old
# copy, and `compose up -d` does not recreate it because the compose definition did not change.
# This helper validates the release's config before it is extracted, then restarts the affected
# containers after the deploy, which re-binds the mounts by path, and waits until each one has loaded it.
#
# Usage:
#   observability-config.sh check <source.tar> <platform-dir> <compose-project>
#     Before extraction. Validates the release config of every service that needs applying with the
#     promtool / amtool shipped in the running image. Exits non-zero, touching nothing, when it is invalid.
#   observability-config.sh sync <source.tar> <platform-dir>
#     Right after extraction. Deletes files under the mirrored directories (see mirrored_dirs) that are
#     not in source.tar, so a rule or dashboard deleted from the repo is deleted on the host too.
#     Extracting a tar only adds and overwrites; without this a removed alerts/*.yaml stays loaded.
#   observability-config.sh apply <platform-dir> <compose-project>
#     After the deploy. Restarts the services whose config differs from the last applied one, waits for
#     /-/ready and <prefix>_config_last_reload_successful == 1, then records what was applied.
#
# A service "needs applying" when the sha256 of its config files differs from the one recorded in
# <platform-dir>/observability-applied.tsv by the last successful apply. With no record yet, every
# running service is applied once. Services with no running container are skipped.
set -euo pipefail

ready_seconds="${HVAC_OBSERVABILITY_READY_SECONDS:-180}"
poll_seconds="${HVAC_OBSERVABILITY_POLL_SECONDS:-2}"
services=(prometheus alertmanager)
# Directories the release owns completely, relative to the platform directory. Everything else in the
# platform tree (runtime/, environments/*.env, backups, state files) is never touched by sync.
# Alertmanager has no templates directory; its config is single files, already replaced by extraction.
mirrored_dirs=(
  infra/observability/alerts
  infra/observability/dashboards
  deploy/platform/phase1/observability/grafana/provisioning
)

usage() {
  echo "error: usage: observability-config.sh check <source.tar> <platform-dir> <compose-project>" >&2
  echo "       observability-config.sh sync <source.tar> <platform-dir>" >&2
  echo "       observability-config.sh apply <platform-dir> <compose-project>" >&2
  exit 1
}

# Config files of one service, relative to the platform directory, sorted.
config_files() {
  local root="$1"
  local service="$2"
  case "$service" in
    prometheus)
      printf '%s\n' deploy/platform/phase1/observability/prometheus.yaml
      if [[ -d "$root/infra/observability/alerts" ]]; then
        (cd "$root" && find infra/observability/alerts -maxdepth 1 -type f -name '*.yaml' | LC_ALL=C sort)
      fi
      ;;
    alertmanager)
      printf '%s\n' \
        deploy/platform/phase1/observability/alertmanager.yaml \
        deploy/platform/phase1/observability/render-alertmanager-config.sh \
        deploy/platform/phase1/observability/render-and-run.sh
      ;;
  esac
}

fingerprint() {
  local root="$1"
  local service="$2"
  local file
  while IFS= read -r file; do
    if [[ -f "$root/$file" ]]; then
      printf '%s  %s\n' "$(sha256sum < "$root/$file" | cut -d' ' -f1)" "$file"
    else
      printf 'missing  %s\n' "$file"
    fi
  done < <(config_files "$root" "$service") | sha256sum | cut -d' ' -f1
}

applied_fingerprint() {
  local state="$1"
  local service="$2"
  if [[ -f "$state" ]]; then
    awk -F '\t' -v s="$service" '$1 == s { print $2 }' "$state" | tail -n 1
  fi
}

container_of() {
  local project="$1"
  local service="$2"
  docker ps -q \
    --filter "label=com.docker.compose.project=$project" \
    --filter "label=com.docker.compose.service=$service" </dev/null | head -n 1
}

validate_prometheus() {
  local stage="$1"
  local image="$2"
  docker run --rm --pull never --network none --user "$(id -u):$(id -g)" \
    --entrypoint /bin/promtool \
    -v "$stage/deploy/platform/phase1/observability/prometheus.yaml:/etc/prometheus/prometheus.yml:ro" \
    -v "$stage/infra/observability/alerts:/etc/prometheus/alerts:ro" \
    "$image" check config /etc/prometheus/prometheus.yml </dev/null
}

validate_alertmanager() {
  local stage="$1"
  local image="$2"
  local secrets="$3"
  local obs="$stage/deploy/platform/phase1/observability"
  local args=(
    docker run --rm --pull never --network none --user "$(id -u):$(id -g)" --entrypoint /bin/sh
    -v "$obs/alertmanager.yaml:/etc/alertmanager/unconfigured.yml:ro"
    -v "$obs/render-alertmanager-config.sh:/etc/alertmanager/render-alertmanager-config.sh:ro"
  )
  if [[ -n "$secrets" ]]; then
    args+=(-v "$secrets:/etc/alertmanager/secrets:ro")
  fi
  # shellcheck disable=SC2016 # the script runs inside the container.
  # Render exactly as render-and-run.sh does (receiver files included), then check the rendered config.
  # The renderer and amtool print a summary only, never a secret.
  "${args[@]}" "$image" -c '
    set -eu
    mkdir -p /tmp/secrets
    secrets=/etc/alertmanager/secrets
    [ -d "$secrets" ] || secrets=/tmp/secrets
    /bin/sh /etc/alertmanager/render-alertmanager-config.sh "$secrets" /tmp/alertmanager.yml /etc/alertmanager/unconfigured.yml
    /bin/amtool check-config /tmp/alertmanager.yml
  ' </dev/null
}

run_check() {
  local source_tar="$1"
  local platform_dir="$2"
  local project="$3"
  local state="$platform_dir/observability-applied.tsv"
  local stage
  stage="$(mktemp -d)"
  # shellcheck disable=SC2064
  trap "rm -rf '$stage'" RETURN
  tar -C "$stage" -xf "$source_tar" --wildcards \
    'deploy/platform/phase1/observability/*' 'infra/observability/alerts/*'
  chmod -R a+rX "$stage"

  local service cid image secrets
  for service in "${services[@]}"; do
    if [[ "$(fingerprint "$stage" "$service")" == "$(applied_fingerprint "$state" "$service")" ]]; then
      echo "observability: $service config unchanged"
      continue
    fi
    cid="$(container_of "$project" "$service")"
    if [[ -z "$cid" ]]; then
      echo "observability: $service is not running; nothing to validate"
      continue
    fi
    image="$(docker inspect -f '{{.Config.Image}}' "$cid" </dev/null)"
    echo "observability: validating $service config with $image"
    case "$service" in
      prometheus)
        if ! validate_prometheus "$stage" "$image"; then
          echo "error: the release's Prometheus config or alert rules are invalid; nothing was deployed." >&2
          return 1
        fi
        ;;
      alertmanager)
        secrets="$(docker inspect -f '{{range .Mounts}}{{if eq .Destination "/etc/alertmanager/secrets"}}{{.Source}}{{end}}{{end}}' "$cid" </dev/null)"
        if ! validate_alertmanager "$stage" "$image" "$secrets"; then
          echo "error: the release's Alertmanager config is invalid; nothing was deployed." >&2
          return 1
        fi
        ;;
    esac
  done
}

# Make each mirrored directory hold exactly the release's files. Only regular files and symlinks
# inside the directory are deleted (find does not follow symlinks, so nothing outside is reached),
# then directories left empty below it. Skipped, with a warning, when the release has no files under
# that directory (a truncated or foreign tar must not empty it) or when the directory, or a parent of
# it, is a symlink that leads outside the platform tree.
run_sync() {
  local source_tar="$1"
  local platform_dir="${2%/}"
  local listing
  listing="$(mktemp)"
  # shellcheck disable=SC2064
  trap "rm -f '$listing'" RETURN
  tar -tf "$source_tar" | sed 's#^\./##' | LC_ALL=C sort -u > "$listing"

  local dir target path rel removed
  for dir in "${mirrored_dirs[@]}"; do
    target="$platform_dir/$dir"
    if ! awk -v p="$dir/" 'index($0, p) == 1 && length($0) > length(p) && substr($0, length($0)) != "/" { found = 1; exit } END { exit !found }' "$listing"; then
      echo "observability: the release has no files under $dir; left as is"
      continue
    fi
    if [[ -L "$target" || ! -d "$target" || "$(cd "$target" && pwd -P)" != "$(cd "$platform_dir" && pwd -P)/$dir" ]]; then
      echo "warning: $target is not a plain directory; stale files there were not removed" >&2
      continue
    fi
    removed=0
    while IFS= read -r -d '' path; do
      rel="${path#"$platform_dir/"}"
      if ! grep -Fxq -- "$rel" "$listing"; then
        rm -f -- "$path"
        echo "observability: removed $rel (deleted from the repository)"
        removed=$((removed + 1))
      fi
    done < <(find "$target" -mindepth 1 \( -type f -o -type l \) -print0)
    find "$target" -mindepth 1 -depth -type d -empty -delete
    echo "observability: $dir mirrors the release ($removed stale file(s) removed)"
  done
}

metrics_port() {
  case "$1" in
    prometheus) echo 9090 ;;
    alertmanager) echo 9093 ;;
  esac
}

wait_loaded() {
  local service="$1"
  local cid="$2"
  local port metric deadline
  port="$(metrics_port "$service")"
  metric="${service}_config_last_reload_successful"
  deadline=$((SECONDS + ready_seconds))
  while :; do
    if docker exec "$cid" wget -q -T 5 -O - "http://127.0.0.1:$port/-/ready" </dev/null >/dev/null 2>&1; then
      if docker exec "$cid" wget -q -T 5 -O - "http://127.0.0.1:$port/metrics" </dev/null 2>/dev/null \
        | grep -Eq "^${metric} 1(\\.0+)?\$"; then
        echo "observability: $service is ready and $metric is 1"
        return 0
      fi
    fi
    if ((SECONDS >= deadline)); then
      echo "error: $service did not become ready with $metric == 1 within ${ready_seconds}s." >&2
      echo "Check: docker logs $cid --since 10m" >&2
      return 1
    fi
    sleep "$poll_seconds"
  done
}

run_apply() {
  local platform_dir="$1"
  local project="$2"
  local state="$platform_dir/observability-applied.tsv"
  local service cid current failed=0
  local next
  next="$(mktemp)"
  if [[ -f "$state" ]]; then
    cp "$state" "$next"
  fi
  for service in "${services[@]}"; do
    current="$(fingerprint "$platform_dir" "$service")"
    if [[ "$current" == "$(applied_fingerprint "$state" "$service")" ]]; then
      continue
    fi
    cid="$(container_of "$project" "$service")"
    if [[ -z "$cid" ]]; then
      echo "observability: $service is not running; skipped"
      continue
    fi
    echo "observability: restarting $service to load the new config"
    docker restart "$cid" </dev/null >/dev/null
    if wait_loaded "$service" "$cid"; then
      awk -F '\t' -v s="$service" '$1 != s' "$next" > "$next.tmp" || true
      printf '%s\t%s\n' "$service" "$current" >> "$next.tmp"
      mv "$next.tmp" "$next"
    else
      failed=1
    fi
  done
  mv "$next" "$state"
  chmod 644 "$state"
  if ((failed)); then
    echo "error: the release is deployed, but Prometheus/Alertmanager did not load its config. See docs/operations/deploy.md." >&2
    return 1
  fi
}

mode="${1:-}"
case "$mode" in
  check)
    [[ $# -eq 4 && -f "$2" && -d "$3" ]] || usage
    run_check "$2" "$3" "$4"
    ;;
  sync)
    [[ $# -eq 3 && -f "$2" && -d "$3" ]] || usage
    run_sync "$2" "$3"
    ;;
  apply)
    [[ $# -eq 3 && -d "$2" ]] || usage
    run_apply "$2" "$3"
    ;;
  *)
    usage
    ;;
esac
