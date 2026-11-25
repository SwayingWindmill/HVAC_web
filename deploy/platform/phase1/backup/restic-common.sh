#!/usr/bin/env bash
# Shared loader and validation for Phase 1 restic offsite backup and restore drill scripts.
# Sourced by sync-offsite-restic.sh and run-offsite-restore-drill.sh.

restic_load_offsite_env() {
  local default_env="/etc/hvac/backup-offsite.env"
  local explicit_env="${OFFSITE_BACKUP_ENV_FILE:-}"
  local env_file=""
  if [[ -n "$explicit_env" ]]; then
    if [[ ! -f "$explicit_env" ]]; then
      echo "error: OFFSITE_BACKUP_ENV_FILE does not exist: $explicit_env" >&2
      return 1
    fi
    env_file="$explicit_env"
  elif [[ -f "$default_env" ]]; then
    env_file="$default_env"
  fi

  if [[ -n "$env_file" ]]; then
    local mode=""
    mode="$(stat -c '%a' "$env_file" 2>/dev/null || stat -f '%Lp' "$env_file" 2>/dev/null || true)"
    if [[ ! "$mode" =~ ^[0-7]{3,4}$ ]]; then
      echo "error: unable to inspect file permissions of $env_file" >&2
      return 1
    fi
    if [[ "${OFFSITE_ALLOW_INSECURE_ENV_PERMS:-0}" != "1" ]] && (( (8#$mode & 077) != 0 )); then
      echo "error: $env_file must not be group/world-accessible (mode $mode; run chmod 600 $env_file)" >&2
      return 1
    fi
    set -a
    # shellcheck disable=SC1090
    source "$env_file"
    set +a
  fi

  if [[ -z "${RESTIC_REPOSITORY:-}" ]]; then
    echo "error: RESTIC_REPOSITORY is required (configure it in $default_env or OFFSITE_BACKUP_ENV_FILE)." >&2
    return 1
  fi
  if [[ -z "${RESTIC_PASSWORD:-}" && -z "${RESTIC_PASSWORD_FILE:-}" ]]; then
    echo "error: RESTIC_PASSWORD or RESTIC_PASSWORD_FILE is required." >&2
    return 1
  fi
  if [[ -n "${RESTIC_PASSWORD_FILE:-}" && ! -r "$RESTIC_PASSWORD_FILE" ]]; then
    echo "error: RESTIC_PASSWORD_FILE is not readable: $RESTIC_PASSWORD_FILE" >&2
    return 1
  fi
  if ! command -v restic >/dev/null 2>&1; then
    echo "error: restic is not installed on this host." >&2
    return 1
  fi

  RESTIC_COMMON_OPTS=()
  if [[ "$RESTIC_REPOSITORY" == s3:* ]]; then
    if [[ "$RESTIC_REPOSITORY" == *myqcloud.com* || "${RESTIC_S3_BUCKET_LOOKUP:-}" == "dns" ]]; then
      RESTIC_COMMON_OPTS+=(-o s3.bucket-lookup=dns)
    elif [[ -n "${RESTIC_S3_BUCKET_LOOKUP:-}" ]]; then
      RESTIC_COMMON_OPTS+=(-o "s3.bucket-lookup=${RESTIC_S3_BUCKET_LOOKUP}")
    fi
  fi
  export RESTIC_REPOSITORY
  if [[ -n "${AWS_DEFAULT_REGION:-}" ]]; then
    export AWS_DEFAULT_REGION
  elif [[ "$RESTIC_REPOSITORY" =~ cos\.([a-z0-9-]+)\.myqcloud\.com ]]; then
    export AWS_DEFAULT_REGION="${BASH_REMATCH[1]}"
  fi
  if [[ -z "${RESTIC_CACHE_DIR:-}" && -z "${XDG_CACHE_HOME:-}" && -z "${HOME:-}" ]]; then
    export RESTIC_CACHE_DIR="/tmp/hvac-restic-cache"
  fi
}

restic_has_timestamp_dir() {
  local dir="$1"
  local child
  [[ -d "$dir" ]] || return 1
  while IFS= read -r child; do
    if [[ "$child" =~ ^[0-9]{8}T[0-9]{6}Z$ ]]; then
      return 0
    fi
  done < <(find "$dir" -mindepth 1 -maxdepth 1 -type d -printf '%f\n' 2>/dev/null || true)
  return 1
}
