#!/usr/bin/env bash
# Check the public site after a deploy.
# On the server, set SMOKE_RESOLVE_IP=127.0.0.1 and SMOKE_CHECK_CONTAINERS=1.
# From GitHub Actions, leave both unset so this hits the real public address.
set -euo pipefail

origin="${HVAC_PUBLIC_ORIGIN:-https://hvac.swayingwindmill.online}"
host="${HVAC_PUBLIC_HOST:-hvac.swayingwindmill.online}"
attempts="${SMOKE_ATTEMPTS:-12}"
pause="${SMOKE_SLEEP:-10}"
resolve="${SMOKE_RESOLVE_IP:-}"

curl_get() {
  local url="$1"
  local -a args=(curl -fsS --max-time 15)
  if [[ -n "$resolve" ]]; then
    args+=(--resolve "${host}:443:${resolve}")
  fi
  "${args[@]}" "$url"
}

containers_ready() {
  [[ "${SMOKE_CHECK_CONTAINERS:-}" == 1 ]] || return 0
  local line
  while IFS= read -r line; do
    [[ -z "$line" ]] && continue
    case "$line" in
      *Restarting*|*unhealthy*|*"health: starting"*)
        echo "waiting on container: $line" >&2
        return 1
        ;;
    esac
  done < <(docker ps -a --filter "label=com.docker.compose.project=${HVAC_COMPOSE_PROJECT:-hvac-platform}" --format '{{.Names}} {{.Status}}')
  return 0
}

attempt=1
while (( attempt <= attempts )); do
  health="$(curl_get "${origin}/healthz" || true)"
  sign_in="$(curl_get "${origin}/sign-in" >/dev/null && echo ok || true)"
  login="$(curl_get "${origin}/login" >/dev/null && echo ok || true)"
  oidc="$(curl_get "${origin}/identity/.well-known/openid-configuration" || true)"
  if [[ "$health" == "ok" || "$health" == $'ok\n' ]] && [[ "$sign_in" == ok && "$login" == ok ]] && [[ "$oidc" == *'"issuer"'* ]] && containers_ready; then
    echo "smoke passed for ${origin}"
    exit 0
  fi
  echo "smoke attempt ${attempt}/${attempts} not ready yet" >&2
  attempt=$((attempt + 1))
  if (( attempt <= attempts )); then
    sleep "$pause"
  fi
done

echo "error: smoke checks failed for ${origin}" >&2
echo "healthz=$(printf '%s' "$health" | tr '\n' ' ')" >&2
exit 1
