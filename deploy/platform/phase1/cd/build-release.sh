#!/usr/bin/env bash
# Build the artifacts the production host swaps into its local images.
# Go binaries match deploy/platform/phase1/images/go-service.Dockerfile.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../../.." && pwd)"
cd "$root"
staging="${PRODUCTION_RELEASE_DIR:-$root/out/production-release}"
services="$root/deploy/platform/phase1/cd/services.tsv"
dockerfile="$root/deploy/platform/phase1/migrations/Dockerfile"

collect_migrator_tree() {
  local dest_root="$1"
  local in_sources=0
  local copied=0
  local line rest src dest
  while IFS= read -r line || [[ -n "$line" ]]; do
    case "$line" in
      FROM\ *)
        if [[ "$in_sources" == 1 ]]; then
          break
        fi
        in_sources=1
        ;;
      COPY\ *)
        [[ "$in_sources" == 1 ]] || continue
        rest="${line#COPY }"
        src="${rest% *}"
        dest="${rest##* }"
        if [[ ! -f "$root/$src" ]]; then
          echo "error: migrator Dockerfile lists a missing file: $src" >&2
          exit 1
        fi
        mkdir -p "$dest_root/$(dirname "$dest")"
        cp "$root/$src" "$dest_root$dest"
        copied=$((copied + 1))
        ;;
    esac
  done < "$dockerfile"
  if [[ "$copied" -lt 10 ]]; then
    echo "error: migrator Dockerfile parser staged only $copied files" >&2
    exit 1
  fi
  if [[ ! -f "$dest_root/opt/hvac/migrations/migration-list.tsv" ]]; then
    echo "error: staged migrator tree is missing migration-list.tsv" >&2
    exit 1
  fi
  echo "staged $copied migrator files"
}

if [[ "${1:-}" == "--check-migrator-list" ]]; then
  tmp="$(mktemp -d)"
  collect_migrator_tree "$tmp"
  rm -rf "$tmp"
  exit 0
fi

if ! command -v go >/dev/null 2>&1; then
  echo "error: go is not on PATH. The production build uses the go.work toolchain (1.27.3)." >&2
  exit 1
fi

rm -rf "$staging"
mkdir -p "$staging/bins" "$staging/web" "$staging/agent" "$staging/migrator"

git rev-parse HEAD | tr -d '[:space:]' > "$staging/REVISION"
echo >> "$staging/REVISION"
git archive --format=tar -o "$staging/source.tar" HEAD
cp "$services" "$staging/services.tsv"
for script in apply-on-server.sh rollback.sh smoke-release.sh prune-rollback-images.sh; do
  cp "$root/deploy/platform/phase1/cd/$script" "$staging/$script"
  chmod 755 "$staging/$script"
done

echo "building container healthcheck"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -trimpath -buildvcs=false -ldflags="-s -w" \
  -o "$staging/bins/healthcheck" \
  ./tools/container-healthcheck/cmd/container-healthcheck/main.go

while IFS=$'\t' read -r service image kind required package <&3; do
  [[ -z "${service:-}" || "$service" == \#* ]] && continue
  if [[ "$kind" != go ]]; then
    continue
  fi
  if [[ ! -e "$package" ]]; then
    echo "error: Go package for $service does not exist: $package" >&2
    exit 1
  fi
  echo "building $service ($package)"
  mkdir -p "$staging/bins/$service"
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -buildvcs=false -ldflags="-s -w" \
    -o "$staging/bins/$service/service" \
    "$package"
done 3< "$services"

short_revision="$(git rev-parse --short=12 HEAD)"
export HVAC_WEB_BUILD_ID="$short_revision"
export HVAC_WEB_GATEWAY_BASE_PATH="${HVAC_WEB_GATEWAY_BASE_PATH:-/api/v1}"
export HVAC_WEB_REALTIME_PROTOCOL="${HVAC_WEB_REALTIME_PROTOCOL:-centrifugo-v1}"
echo "building web dist ($HVAC_WEB_BUILD_ID)"
npm ci
npm run build
test -f apps/hvac-web/dist/index.html
rm -rf "$staging/web/dist"
cp -a apps/hvac-web/dist "$staging/web/dist"
cp deploy/platform/phase1/nginx/nginx.conf "$staging/web/nginx.conf"

echo "building operations-agent-service"
npm --prefix services/operations-agent-service ci
npm --prefix services/operations-agent-service run build
npm --prefix services/operations-agent-service prune --omit=dev --no-audit --no-fund
test -f services/operations-agent-service/dist/bootstrap/main.js
cp services/operations-agent-service/package.json "$staging/agent/package.json"
rm -rf "$staging/agent/dist" "$staging/agent/node_modules"
cp -a services/operations-agent-service/dist "$staging/agent/dist"
cp -a services/operations-agent-service/node_modules "$staging/agent/node_modules"

collect_migrator_tree "$staging/migrator"
echo "production release staged at $staging ($(cat "$staging/REVISION"))"
