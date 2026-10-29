#!/usr/bin/env bash
# Point the running image tags back at a before-<stamp> set, then restart.
# With no argument, use the newest recorded deploy under $HVAC_PLATFORM_DIR/rollbacks.
set -euo pipefail

platform_dir="${HVAC_PLATFORM_DIR:-/opt/hvac-platform}"
deploy_script="${HVAC_DEPLOY_SCRIPT:-/opt/hvac-platform-tools/deploy.sh}"
stamp="${1:-}"

if [[ -z "$stamp" ]]; then
  if [[ ! -s "$platform_dir/rollbacks/latest" ]]; then
    echo "error: no rollback stamp was recorded under $platform_dir/rollbacks." >&2
    exit 1
  fi
  stamp="$(tr -d '[:space:]' < "$platform_dir/rollbacks/latest")"
fi
if [[ ! "$stamp" =~ ^[0-9]{8}T[0-9]{6}Z$ ]]; then
  echo "error: rollback stamp must look like 20261029T120000Z, got: $stamp" >&2
  exit 1
fi
manifest="$platform_dir/rollbacks/${stamp}.tsv"
if [[ ! -s "$manifest" ]]; then
  echo "error: rollback manifest is missing: $manifest" >&2
  exit 1
fi

echo "rolling back to before-${stamp}"
while IFS=$'\t' read -r image previous; do
  [[ -z "${image:-}" || "$image" == \#* ]] && continue
  if [[ -z "$previous" ]]; then
    echo "error: $image has no before-${stamp} tag in $manifest" >&2
    exit 1
  fi
  if ! docker image inspect "$previous" >/dev/null 2>&1; then
    echo "error: rollback image is gone: $previous" >&2
    exit 1
  fi
  docker tag "$previous" "$image"
  echo "restored $image from $previous"
done < "$manifest"

if [[ ! -e "$deploy_script" ]]; then
  echo "error: $deploy_script is missing. Images were retagged; restart was not run." >&2
  exit 1
fi
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
echo "rolled back to before-${stamp}"
