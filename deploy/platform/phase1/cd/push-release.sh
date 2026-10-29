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
# One attempt must finish well under the old 50-minute trickle. Three attempts still fit
# in the Deploy over SSH step timeout.
upload_timeout="${DEPLOY_UPLOAD_TIMEOUT_SECONDS:-600}"
upload_attempts="${DEPLOY_UPLOAD_ATTEMPTS:-3}"
upload_retry_sleep="${DEPLOY_UPLOAD_RETRY_SLEEP:-3}"
if [[ ! "$upload_timeout" =~ ^[0-9]+$ ]] || (( upload_timeout < 1 )); then
  echo "error: DEPLOY_UPLOAD_TIMEOUT_SECONDS must be a positive number of seconds." >&2
  exit 1
fi
if [[ ! "$upload_attempts" =~ ^[0-9]+$ ]] || (( upload_attempts < 1 )); then
  echo "error: DEPLOY_UPLOAD_ATTEMPTS must be a positive integer." >&2
  exit 1
fi

ssh_cmd=(
  ssh
  -p "$port"
  -o StrictHostKeyChecking=accept-new
  -o ConnectTimeout=20
  -o ServerAliveInterval=15
  -o ServerAliveCountMax=8
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

if ! command -v timeout >/dev/null 2>&1; then
  echo "error: timeout(1) is required so a stalled upload cannot run until the job limit." >&2
  exit 1
fi

# One compressed archive of the whole release, including operations-agent node_modules.
# zstd when both ends have it (the production host does); otherwise gzip, which is enough
# to stop a cross-border link trickling one small file at a time.
codec=gzip
if command -v zstd >/dev/null 2>&1 && zstd --version >/dev/null 2>&1 \
  && remote_ssh "$target" "command -v zstd >/dev/null 2>&1"; then
  codec=zstd
fi
echo "release upload codec: ${codec} (attempts ${upload_attempts}, timeout ${upload_timeout}s)"

archive="$(mktemp)"
remove_archive() {
  rm -f "$archive"
}
trap 'remove_archive; cleanup_key' EXIT

if [[ "$codec" == zstd ]]; then
  tar -C "$staging" -cf - . | zstd -T0 -3 -c > "$archive"
  ext=zst
  decode="zstd -dc"
else
  tar -C "$staging" -cf - . | gzip -1 -c > "$archive"
  ext=gz
  decode="gzip -dc"
fi
bytes="$(wc -c < "$archive" | tr -d '[:space:]')"
echo "release archive: ${bytes} bytes (${codec}), including agent/node_modules when present"

remote_cleanup() {
  remote_ssh "$target" "rm -rf '$remote'" || true
}

upload_once() {
  local attempt="$1"
  local file="$remote/release.tar.$ext"
  local start elapsed rate rc
  remote_ssh "$target" "rm -rf '$remote' && mkdir -p '$remote'"
  echo "upload attempt ${attempt}/${upload_attempts}: ${codec} ${bytes} bytes -> ${host}:${file}"
  start=$SECONDS
  set +e
  # Without --foreground, timeout puts the uploader in its own process group and kills
  # ssh as well. Otherwise a stalled remote cat keeps the connection open after the timer fires.
  timeout "$upload_timeout" python3 - "$archive" "$file" "$target" "${ssh_cmd[@]}" <<'PY'
import os
import shlex
import subprocess
import sys
import time

archive, dest, target, *ssh = sys.argv[1:]
remote = "cat > " + shlex.quote(dest)
proc = subprocess.Popen(ssh + [target, remote], stdin=subprocess.PIPE)
if proc.stdin is None:
    sys.exit(1)
sent = 0
total = os.path.getsize(archive)
started = time.monotonic()
last = started
with open(archive, "rb") as handle:
    while True:
        chunk = handle.read(256 * 1024)
        if not chunk:
            break
        try:
            proc.stdin.write(chunk)
            proc.stdin.flush()
        except BrokenPipeError:
            proc.stdin.close()
            proc.wait()
            print(f"upload stopped after {sent}/{total} bytes", file=sys.stderr, flush=True)
            sys.exit(proc.returncode or 1)
        sent += len(chunk)
        now = time.monotonic()
        if now - last >= 5 or sent == total:
            elapsed = max(now - started, 1e-6)
            print(
                f"upload progress {sent}/{total} bytes ({sent / elapsed / 1e6:.2f} MB/s)",
                file=sys.stderr,
                flush=True,
            )
            last = now
proc.stdin.close()
sys.exit(proc.wait())
PY
  rc=$?
  set -e
  elapsed=$((SECONDS - start))
  if [[ "$rc" -ne 0 ]]; then
    if [[ "$rc" -eq 124 ]]; then
      echo "error: upload attempt ${attempt}/${upload_attempts} timed out after ${elapsed}s (${bytes} bytes, ${codec})" >&2
    else
      echo "error: upload attempt ${attempt}/${upload_attempts} failed after ${elapsed}s exit ${rc} (${bytes} bytes, ${codec})" >&2
    fi
    remote_cleanup
    return 1
  fi
  rate=$((bytes / (elapsed > 0 ? elapsed : 1)))
  echo "upload attempt ${attempt}/${upload_attempts}: sent ${bytes} bytes in ${elapsed}s (${rate} B/s)"
  if ! remote_ssh "$target" "$decode $(printf '%q' "$file") | tar -C $(printf '%q' "$remote") -xf - && rm -f $(printf '%q' "$file")"; then
    echo "error: extracting the ${codec} archive failed; not applying" >&2
    remote_cleanup
    return 1
  fi
  if ! remote_ssh "$target" "test -s '$remote/apply-on-server.sh' && test -s '$remote/source.tar'"; then
    echo "error: extracted release is incomplete; not applying" >&2
    remote_cleanup
    return 1
  fi
  return 0
}

ready=0
attempt=1
while (( attempt <= upload_attempts )); do
  if upload_once "$attempt"; then
    ready=1
    break
  fi
  echo "error: upload attempt ${attempt}/${upload_attempts} did not finish; ${remote} removed; apply-on-server.sh was not run" >&2
  if (( attempt < upload_attempts )); then
    sleep "$upload_retry_sleep"
  fi
  attempt=$((attempt + 1))
done
if [[ "$ready" -ne 1 ]]; then
  remote_cleanup
  echo "error: giving up after ${upload_attempts} upload attempts. apply-on-server.sh was not run." >&2
  exit 1
fi

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
