#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
VERSION="${1:-${EDGE_GATEWAY_VERSION:-0.1.0}}"
OUT_DIR="${OUT_DIR:-${ROOT_DIR}/out}"
OUT_BIN="${OUT_DIR}/edge-gateway-linux-armv7"

mkdir -p "${OUT_DIR}"
cd "${ROOT_DIR}"

CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build \
  -trimpath \
  -buildvcs=false \
  -ldflags="-s -w -X main.version=${VERSION}" \
  -o "${OUT_BIN}" \
  ./cmd/edge-gateway

sha256sum "${OUT_BIN}" > "${OUT_BIN}.sha256"
echo "Built ARMv7 binary: ${OUT_BIN} (version=${VERSION})"
