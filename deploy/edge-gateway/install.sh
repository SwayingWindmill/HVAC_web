#!/usr/bin/env bash
set -euo pipefail

BIN_SRC="${1:-}"
BASE_DIR="${BASE_DIR:-/opt/hvac-edge-gateway}"
INSTALL_SYSTEMD="${INSTALL_SYSTEMD:-1}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

if [[ -z "${BIN_SRC}" || ! -f "${BIN_SRC}" ]]; then
  echo "usage: $0 <path-to-edge-gateway-binary>" >&2
  exit 2
fi

mkdir -p "${BASE_DIR}/state" "${BASE_DIR}/ota/slots" "${BASE_DIR}/ota/downloads"
chmod 750 "${BASE_DIR}/state" "${BASE_DIR}/ota"

install -m 0755 "${BIN_SRC}" "${BASE_DIR}/ota/slots/slot-a.tmp"
mv -f "${BASE_DIR}/ota/slots/slot-a.tmp" "${BASE_DIR}/ota/slots/slot-a"
ln -sfn "slots/slot-a" "${BASE_DIR}/ota/current.tmp"
mv -f "${BASE_DIR}/ota/current.tmp" "${BASE_DIR}/ota/current"

if [[ -f "${BASE_DIR}/config.json" ]]; then
  "${BASE_DIR}/ota/current" --validate-config --config "${BASE_DIR}/config.json"
fi

if [[ "${INSTALL_SYSTEMD}" == "1" ]]; then
  install -m 0644 "${SCRIPT_DIR}/hvac-edge-gateway.service" /etc/systemd/system/hvac-edge-gateway.service
  systemctl daemon-reload
  systemctl enable hvac-edge-gateway.service
  echo "Installed systemd unit /etc/systemd/system/hvac-edge-gateway.service"
fi
