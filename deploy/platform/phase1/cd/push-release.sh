#!/usr/bin/env bash
# Send a staged production release to the Phase 1 server and run apply-on-server.sh.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../../.." && pwd)"
staging="${PRODUCTION_RELEASE_DIR:-$root/out/production-release}"

credential_help() {
  cat >&2 <<'EOF'
error: production deploy needs an SSH credential.

Set one of these GitHub Actions secrets on SwayingWindmill/HVAC_web:
  DEPLOY_SSH_KEY       private key for the deploy user (preferred; unencrypted OpenSSH or PEM)
  DEPLOY_SSH_PASSWORD  password for the same user, used only when DEPLOY_SSH_KEY is unset

Optional, with these defaults when the secret is unset:
  DEPLOY_SSH_USER  ubuntu
  DEPLOY_SSH_HOST  139.199.58.47
  DEPLOY_SSH_PORT  22
EOF
}

check_credentials() {
  if [[ -z "${DEPLOY_SSH_KEY:-}" && -z "${DEPLOY_SSH_PASSWORD:-}" ]]; then
    credential_help
    exit 1
  fi
  local port="${DEPLOY_SSH_PORT:-22}"
  if [[ ! "$port" =~ ^[0-9]+$ ]] || (( port < 1 || port > 65535 )); then
    echo "error: DEPLOY_SSH_PORT must be a TCP port from 1 to 65535. Unset it to use 22." >&2
    exit 1
  fi
  local host="${DEPLOY_SSH_HOST:-139.199.58.47}"
  local user="${DEPLOY_SSH_USER:-ubuntu}"
  if [[ -z "$host" || -z "$user" ]]; then
    echo "error: DEPLOY_SSH_HOST and DEPLOY_SSH_USER must not be empty. Unset them to use 139.199.58.47 and ubuntu." >&2
    exit 1
  fi
}

if [[ "${1:-}" == "--check-credentials" ]]; then
  check_credentials
  echo "Deploy SSH credentials are present (host ${DEPLOY_SSH_HOST:-139.199.58.47}, user ${DEPLOY_SSH_USER:-ubuntu}, port ${DEPLOY_SSH_PORT:-22})."
  exit 0
fi

check_credentials
if [[ ! -f "$staging/source.tar" || ! -f "$staging/apply-on-server.sh" ]]; then
  echo "error: $staging is not a staged release. Run deploy/platform/phase1/cd/build-release.sh first." >&2
  exit 1
fi

host="${DEPLOY_SSH_HOST:-139.199.58.47}"
user="${DEPLOY_SSH_USER:-ubuntu}"
port="${DEPLOY_SSH_PORT:-22}"
revision="$(tr -d '[:space:]' < "$staging/REVISION")"
remote="/tmp/hvac-production-release-${revision}"
target="${user}@${host}"

ssh_cmd=(
  ssh
  -p "$port"
  -o StrictHostKeyChecking=accept-new
  -o ConnectTimeout=20
  -o ServerAliveInterval=30
  -o ServerAliveCountMax=40
)
key_file=""
cleanup_key() {
  if [[ -n "$key_file" ]]; then
    rm -f "$key_file"
  fi
}
trap cleanup_key EXIT

if [[ -n "${DEPLOY_SSH_KEY:-}" ]]; then
  key_file="$(mktemp)"
  chmod 600 "$key_file"
  DEPLOY_KEY_FILE="$key_file" python3 - <<'PY'
import os
from pathlib import Path
key = os.environ["DEPLOY_SSH_KEY"]
path = Path(os.environ["DEPLOY_KEY_FILE"])
if "\\n" in key and "\n" not in key.strip():
    key = key.replace("\\n", "\n")
if not key.endswith("\n"):
    key += "\n"
path.write_text(key)
PY
  ssh_cmd+=(-i "$key_file" -o IdentitiesOnly=yes)
  remote_ssh() { "${ssh_cmd[@]}" "$@"; }
else
  if ! command -v sshpass >/dev/null 2>&1; then
    sudo apt-get update -y
    sudo apt-get install -y sshpass
  fi
  remote_ssh() { sshpass -p "$DEPLOY_SSH_PASSWORD" "${ssh_cmd[@]}" "$@"; }
fi

echo "uploading release $revision to ${host}"
remote_ssh "$target" "rm -rf '$remote' && mkdir -p '$remote'"
tar -C "$staging" -cf - . | remote_ssh "$target" "tar -C '$remote' -xf -"

status=0
remote_ssh "$target" "bash '$remote/apply-on-server.sh' '$remote'" || status=$?
if [[ "$status" -ne 0 ]]; then
  remote_ssh "$target" "rm -rf '$remote'" || true
  exit "$status"
fi
# The server already checked local nginx and container health. This call leaves the runner,
# so it sees the public address the way an operator does.
if ! bash "$root/deploy/platform/phase1/cd/smoke-release.sh"; then
  echo "error: public smoke failed. Rolling the server back to the before-* images." >&2
  remote_ssh "$target" "bash '${HVAC_PLATFORM_DIR:-/opt/hvac-platform}/deploy/platform/phase1/cd/rollback.sh'" || status=$?
  remote_ssh "$target" "rm -rf '$remote'" || true
  exit 1
fi
remote_ssh "$target" "rm -rf '$remote'" || true
exit 0
