#!/bin/sh
# Write an Alertmanager config from host files. No secrets are printed.
# Usage: render-alertmanager-config.sh <secrets-dir> <output-file> <unconfigured-template>
# When neither WeCom nor SMTP is present, the template (receiver "none") is copied through.
set -eu

secrets="${1:?secrets directory}"
out="${2:?output file}"
template="${3:?unconfigured template}"

wecom_file="$secrets/wecom-webhook-url"
smtp_env="$secrets/smtp.env"
smtp_pass="$secrets/smtp-password"

if [ ! -f "$template" ]; then
  echo "error: unconfigured alertmanager template is missing: $template" >&2
  exit 1
fi

has_wecom=0
if [ -s "$wecom_file" ]; then
  url=$(tr -d '\r\n' < "$wecom_file")
  if ! printf '%s' "$url" | grep -Eq '^https://qyapi\.weixin\.qq\.com/cgi-bin/webhook/send\?key=[A-Za-z0-9_-]+$'; then
    echo "error: wecom-webhook-url must be one line, a https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key= URL" >&2
    exit 1
  fi
  has_wecom=1
fi

field() {
  key="$1"
  file="$2"
  if [ ! -f "$file" ]; then
    return 1
  fi
  grep -E "^${key}=" "$file" | head -n 1 | cut -d= -f2- | tr -d '\r'
}

has_smtp=0
if [ -f "$smtp_env" ]; then
  smarthost=$(field SMTP_SMARTHOST "$smtp_env" || true)
  from=$(field SMTP_FROM "$smtp_env" || true)
  username=$(field SMTP_AUTH_USERNAME "$smtp_env" || true)
  to=$(field SMTP_TO "$smtp_env" || true)
  require_tls=$(field SMTP_REQUIRE_TLS "$smtp_env" || true)
  if [ -z "$require_tls" ]; then
    require_tls=false
  fi
  if ! printf '%s' "$smarthost" | grep -Eq '^[A-Za-z0-9.-]+:[0-9]+$'; then
    echo "error: SMTP_SMARTHOST must look like smtp.example.com:465" >&2
    exit 1
  fi
  if ! printf '%s' "$from" | grep -Eq '^[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}$'; then
    echo "error: SMTP_FROM must be one email address" >&2
    exit 1
  fi
  if ! printf '%s' "$username" | grep -Eq '^[A-Za-z0-9._%+-@]+$'; then
    echo "error: SMTP_AUTH_USERNAME has characters this renderer will not quote" >&2
    exit 1
  fi
  if ! printf '%s' "$to" | grep -Eq '^[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}$'; then
    echo "error: SMTP_TO must be one email address" >&2
    exit 1
  fi
  case "$require_tls" in
    true|false) ;;
    *) echo "error: SMTP_REQUIRE_TLS must be true or false" >&2; exit 1 ;;
  esac
  if [ ! -s "$smtp_pass" ]; then
    echo "error: smtp.env is present but smtp-password is missing or empty" >&2
    exit 1
  fi
  has_smtp=1
fi

if [ "$has_wecom" -eq 0 ] && [ "$has_smtp" -eq 0 ]; then
  cp "$template" "$out"
  echo "alertmanager receiver: none (no wecom-webhook-url or smtp.env)"
  exit 0
fi

{
  if [ "$has_smtp" -eq 1 ]; then
    cat <<EOF
global:
  smtp_smarthost: "${smarthost}"
  smtp_from: "${from}"
  smtp_auth_username: "${username}"
  smtp_auth_password_file: "${smtp_pass}"
  smtp_require_tls: ${require_tls}
EOF
  fi
  cat <<'EOF'
route:
  receiver: notify
  group_by: ["alertname", "severity"]
  group_wait: 30s
  group_interval: 5m
  repeat_interval: 4h
  routes:
    - matchers: ['severity=~"page|critical"']
      receiver: notify
      repeat_interval: 1h
receivers:
  - name: notify
EOF
  if [ "$has_wecom" -eq 1 ]; then
    cat <<'EOF'
    webhook_configs:
      - url: http://wecom-alert-adapter:9095/alertmanager
        send_resolved: true
EOF
  fi
  if [ "$has_smtp" -eq 1 ]; then
    cat <<EOF
    email_configs:
      - to: "${to}"
        send_resolved: true
EOF
  fi
} > "$out"

echo "alertmanager receiver: notify (wecom=${has_wecom} smtp=${has_smtp})"
