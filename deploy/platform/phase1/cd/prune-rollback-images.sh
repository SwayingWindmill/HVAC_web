#!/usr/bin/env bash
# After a successful deploy, keep the newest N before-<stamp> sets and drop dangling images
# plus build cache older than a day. Never removes a tag that is still the running image name.
set -euo pipefail

keep="${1:-2}"
if [[ ! "$keep" =~ ^[0-9]+$ ]] || (( keep < 1 )); then
  echo "error: keep count must be a positive integer" >&2
  exit 1
fi

mapfile -t stamps < <(docker images --format '{{.Tag}}' | sed -n 's/^before-//p' | sort -u)
if (( ${#stamps[@]} > keep )); then
  drop=$(( ${#stamps[@]} - keep ))
  for stamp in "${stamps[@]:0:drop}"; do
    while IFS=$'\t' read -r repository tag; do
      if [[ "$tag" == "before-${stamp}" && -n "$repository" && "$repository" != "<none>" ]]; then
        echo "removing ${repository}:before-${stamp}"
        docker image rm "${repository}:before-${stamp}" >/dev/null 2>&1 || echo "left ${repository}:before-${stamp} in place"
      fi
    done < <(docker images --format '{{.Repository}}\t{{.Tag}}')
  done
else
  echo "rollback sets kept: ${#stamps[@]} (limit ${keep})"
fi

docker image prune -f >/dev/null
docker builder prune -f --filter until=24h >/dev/null || echo "build cache prune skipped"
echo "pruned rollback images down to ${keep} set(s)"
