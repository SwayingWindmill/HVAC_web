#!/bin/sh
# Alertmanager entrypoint. Regenerates the config from the secrets directory, then starts Alertmanager.
# An empty secrets directory keeps the committed "none" receiver, so an unconfigured host still starts.
set -eu
secrets="${ALERT_RECEIVER_DIR:-/etc/alertmanager/secrets}"
template="${ALERTMANAGER_UNCONFIGURED_FILE:-/etc/alertmanager/unconfigured.yml}"
out=/tmp/alertmanager.generated.yml
mkdir -p "$secrets"
/bin/sh /etc/alertmanager/render-alertmanager-config.sh "$secrets" "$out" "$template"
exec /bin/alertmanager --config.file="$out" "$@"
