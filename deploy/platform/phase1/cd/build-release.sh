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

# The release carries the lockfile and the built JavaScript. The server image
# runs npm ci --omit=dev. node_modules, sourcemaps, and declarations stay here.
stage_agent() {
  local src="$1"
  local dest="$2"
  if [[ ! -f "$src/package.json" || ! -f "$src/package-lock.json" || ! -f "$src/dist/bootstrap/main.js" ]]; then
    echo "error: operations-agent source is missing package.json, package-lock.json, or dist/bootstrap/main.js" >&2
    exit 1
  fi
  mkdir -p "$dest"
  rm -rf "$dest/dist" "$dest/node_modules" "$dest/.cache"
  cp "$src/package.json" "$dest/package.json"
  cp "$src/package-lock.json" "$dest/package-lock.json"
  mkdir -p "$dest/dist"
  cp -a "$src/dist/." "$dest/dist/"
  find "$dest" -depth \( -type d -name node_modules -o -type d -name .cache \) -exec rm -rf {} +
  find "$dest" -type f \( -name '*.map' -o -name '*.d.ts' -o -name '*.tsbuildinfo' \) -delete
  if [[ -e "$dest/node_modules" || -e "$dest/dist/node_modules" ]]; then
    echo "error: staged agent still has node_modules" >&2
    exit 1
  fi
  if find "$dest" -type f \( -name '*.map' -o -name '*.d.ts' -o -name '*.tsbuildinfo' \) | grep -q .; then
    echo "error: staged agent still has sourcemaps or declarations" >&2
    exit 1
  fi
}

if [[ "${1:-}" == "--stage-agent" ]]; then
  if [[ $# -ne 3 ]]; then
    echo "error: usage: build-release.sh --stage-agent SRC DEST" >&2
    exit 1
  fi
  stage_agent "$2" "$3"
  exit 0
fi

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
for script in apply-on-server.sh rebuild-deploy-base.sh rollback.sh smoke-release.sh prune-rollback-images.sh deploy.sh; do
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
  if [[ "$kind" != go && "$kind" != static ]]; then
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
find "$staging/web" -type f \( -name '*.map' -o -name '*.tsbuildinfo' \) -delete
find "$staging/web" -depth \( -type d -name node_modules -o -type d -name .cache \) -exec rm -rf {} +
cp deploy/platform/phase1/nginx/nginx.conf "$staging/web/nginx.conf"

echo "building operations-agent-service"
npm --prefix services/operations-agent-service ci
npm --prefix services/operations-agent-service run build
stage_agent "$root/services/operations-agent-service" "$staging/agent"

collect_migrator_tree "$staging/migrator"
echo "staged source.tar $(wc -c < "$staging/source.tar" | tr -d '[:space:]') bytes"
echo "staged bins $(du -sb "$staging/bins" | awk '{print $1}') bytes"
echo "staged web $(du -sb "$staging/web" | awk '{print $1}') bytes"
echo "staged agent $(du -sb "$staging/agent" | awk '{print $1}') bytes (lockfile and dist, no node_modules)"
echo "staged migrator $(du -sb "$staging/migrator" | awk '{print $1}') bytes"
echo "production release staged at $staging ($(cat "$staging/REVISION"))"
