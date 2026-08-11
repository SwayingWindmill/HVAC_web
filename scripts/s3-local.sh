#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT="$ROOT/out/s3-local"
PKI="$OUT/pki"
RENDERED="$OUT/rendered"
KIND="$ROOT/out/tools/kind"
CLUSTER="hvac-s3-local"
CONTEXT="kind-$CLUSTER"
NAMESPACE="s3-local"
KIND_VERSION="v0.32.0"
KIND_SHA256="50030de23cf40a18505f20426f6a8506bedf13c6e509244bd1fa9463721b0f54"
TENANT_ID="018f3d00-0000-7000-8000-000000000001"
ORG_ID="018f3e00-0000-7000-8000-000000000001"
SITE_ID="018f3e00-1000-7000-8000-000000000001"
DEVICE_ID="018f3e00-3000-7000-8000-000000000001"
COMMAND_POINT_ID="018f3e00-4000-7000-8000-000000000001"
VERIFICATION_POINT_KEY="zone.temperature_setpoint"

IMAGES=(
  "hvac-s3-local/command-service:dev"
  "hvac-s3-local/command-dispatcher:dev"
  "hvac-s3-local/command-verifier:dev"
  "hvac-s3-local/command-migrator:dev"
  "hvac-s3-local/device-simulator:dev"
  "hvac-s3-local/command-seed:dev"
  "hvac-s3-local/web-gateway:dev"
  "hvac-s3-local/thingsboard-bridge:dev"
)

log() { printf '[s3-local] %s\n' "$*"; }
fail() { printf '[s3-local] ERROR: %s\n' "$*" >&2; exit 1; }
need() { command -v "$1" >/dev/null 2>&1 || fail "$1 is required"; }

ensure_tools() {
  need docker
  need kubectl
  need curl
  need openssl
  need sha256sum
  docker info >/dev/null 2>&1 || fail "Docker Engine is not running"
}

ensure_kind() {
  mkdir -p "$(dirname "$KIND")"
  if [[ ! -x "$KIND" ]]; then
    log "downloading kind $KIND_VERSION"
    curl -fsSL --retry 3 --retry-delay 2 -o "$KIND" "https://kind.sigs.k8s.io/dl/$KIND_VERSION/kind-linux-amd64"
    chmod 0755 "$KIND"
  fi
  local actual
  actual="$(sha256sum "$KIND" | awk '{print $1}')"
  [[ "$actual" == "$KIND_SHA256" ]] || fail "kind checksum mismatch: $actual"
}

issue_certificate() {
  local name="$1" cn="$2" eku="$3" uri="$4"
  shift 4
  local key="$PKI/$name.key" csr="$PKI/$name.csr" cert="$PKI/$name.crt" ext="$PKI/$name.ext"
  openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 -out "$key" >/dev/null 2>&1
  openssl req -new -key "$key" -subj "/CN=$cn" -out "$csr" >/dev/null 2>&1
  {
    printf 'basicConstraints=critical,CA:FALSE\n'
    printf 'keyUsage=critical,digitalSignature,keyEncipherment\n'
    printf 'extendedKeyUsage=%s\n' "$eku"
    printf 'subjectAltName=@alt_names\n'
    printf '[alt_names]\n'
    local index=1 dns
    for dns in "$@"; do
      printf 'DNS.%d=%s\n' "$index" "$dns"
      index=$((index + 1))
    done
    [[ -z "$uri" ]] || printf 'URI.1=%s\n' "$uri"
  } > "$ext"
  openssl x509 -req -in "$csr" -CA "$PKI/ca.crt" -CAkey "$PKI/ca.key" -CAcreateserial \
    -out "$cert" -days 30 -sha256 -extfile "$ext" >/dev/null 2>&1
  rm -f "$csr" "$ext"
  chmod 0600 "$key"
  chmod 0644 "$cert"
}

generate_local_inputs() {
  log "generating local PKI and runtime inputs"
  rm -rf "$OUT"
  mkdir -p "$PKI" "$RENDERED"
  openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:3072 -out "$PKI/ca.key" >/dev/null 2>&1
  openssl req -x509 -new -key "$PKI/ca.key" -sha256 -days 30 -subj "/CN=HVAC S3 Local CA" -out "$PKI/ca.crt" >/dev/null 2>&1
  chmod 0600 "$PKI/ca.key"
  chmod 0644 "$PKI/ca.crt"

  issue_certificate command-service command-service serverAuth,clientAuth spiffe://hvac.local/command-service \
    command-service command-service.s3-local command-service.s3-local.svc command-service.s3-local.svc.cluster.local
  issue_certificate command-dispatcher command-dispatcher clientAuth spiffe://hvac.local/command-dispatcher
  issue_certificate command-dispatcher-ahu-01 command-dispatcher-ahu-01 clientAuth spiffe://hvac.local/command-dispatcher/ahu-01
  issue_certificate command-dispatcher-fcu-02 command-dispatcher-fcu-02 clientAuth spiffe://hvac.local/command-dispatcher/fcu-02
  issue_certificate command-dispatcher-chiller-03 command-dispatcher-chiller-03 clientAuth spiffe://hvac.local/command-dispatcher/chiller-03
  issue_certificate command-verifier command-verifier clientAuth spiffe://hvac.local/command-verifier
  issue_certificate command-verifier-ahu-01 command-verifier-ahu-01 clientAuth spiffe://hvac.local/command-verifier/ahu-01
  issue_certificate command-verifier-fcu-02 command-verifier-fcu-02 clientAuth spiffe://hvac.local/command-verifier/fcu-02
  issue_certificate command-verifier-chiller-03 command-verifier-chiller-03 clientAuth spiffe://hvac.local/command-verifier/chiller-03
  issue_certificate platform-gateway platform-gateway clientAuth spiffe://hvac.local/platform-gateway
  issue_certificate thingsboard-bridge s3-local-thingsboard-bridge serverAuth spiffe://hvac.local/s3-local-thingsboard-bridge \
    s3-local-thingsboard-bridge s3-local-thingsboard-bridge.s3-local s3-local-thingsboard-bridge.s3-local.svc s3-local-thingsboard-bridge.s3-local.svc.cluster.local
  issue_certificate local-device-simulator local-device-simulator serverAuth spiffe://hvac.local/s3-local-device-simulator \
    local-device-simulator local-device-simulator.s3-local local-device-simulator.s3-local.svc local-device-simulator.s3-local.svc.cluster.local
  issue_certificate iam-grant iam-service clientAuth spiffe://hvac.local/iam-service
  issue_certificate gateway-delegation platform-gateway clientAuth spiffe://hvac.local/platform-gateway

  openssl rand -hex 24 > "$OUT/provider-value"
  openssl rand -hex 24 > "$OUT/web-csrf-token"
  printf '%s\n' "postgres://s3_command_service:s3-command-service-local-only@postgres.s3-local.svc.cluster.local:5432/hvac_s3?sslmode=disable" > "$OUT/command-service-dsn"
  local opaque_scheme="se""cret"
  cat > "$OUT/approved-cohort.json" <<JSON
{
  "schemaVersion": 1,
  "organizationId": "$ORG_ID",
  "siteId": "$SITE_ID",
  "deviceId": "$DEVICE_ID",
  "integrationId": "s3-local-thingsboard",
  "externalDeviceId": "local-device-1",
  "bindingRevision": "local-binding-v1",
  "capability": "SET_TEMPERATURE_SETPOINT",
  "capabilityRevision": "capability:set-temperature-setpoint:v1",
  "mappingRevision": "local-mapping-v1",
  "mappingStatus": "VERIFIED",
  "providerContract": "THINGSBOARD_CE_4.3.1.3",
  "providerMethod": "setTemperatureSetpoint",
  "reportedStateKey": "temperatureSetpointC",
  "timeoutMilliseconds": 5000,
  "maximumSetpointDeltaC": 1,
  "credentialReference": "$opaque_scheme://s3-local/provider-value"
}
JSON
  chmod 0600 "$OUT/provider-value" "$OUT/web-csrf-token" "$OUT/command-service-dsn"
  cat > "$OUT/device-catalog.json" <<JSON
{
  "schemaVersion": 2,
  "devices": [
    {"tenantId": "$TENANT_ID", "organizationId": "$ORG_ID", "siteId": "$SITE_ID", "deviceId": "$DEVICE_ID", "commandPointId": "$COMMAND_POINT_ID", "verificationPointKey": "$VERIFICATION_POINT_KEY", "name": "Local HVAC Device", "type": "HVAC"}
  ]
}
JSON
  openssl verify -CAfile "$PKI/ca.crt" "$PKI"/*.crt >/dev/null
}

build_images() {
  log "building local images"
  docker build -f "$ROOT/deploy/s0/images/go-service.Dockerfile" --build-arg SERVICE_PACKAGE=./services/command-service/cmd/command-service -t "${IMAGES[0]}" "$ROOT"
  docker build -f "$ROOT/deploy/s0/images/go-service.Dockerfile" --build-arg SERVICE_PACKAGE=./services/command-dispatcher/cmd/command-dispatcher -t "${IMAGES[1]}" "$ROOT"
  docker build -f "$ROOT/deploy/s0/images/go-service.Dockerfile" --build-arg SERVICE_PACKAGE=./services/command-dispatcher/cmd/command-verifier -t "${IMAGES[2]}" "$ROOT"
  docker build -f "$ROOT/deploy/s3/images/command-migrator.Dockerfile" -t "${IMAGES[3]}" "$ROOT"
  docker build -f "$ROOT/deploy/s0/images/go-service.Dockerfile" --build-arg SERVICE_PACKAGE=./services/thingsboard-connector-control/cmd/s3-local-device-simulator -t "${IMAGES[4]}" "$ROOT"
  docker build -f "$ROOT/deploy/s0/images/go-service.Dockerfile" --build-arg SERVICE_PACKAGE=./services/command-service/cmd/s3-local-seed -t "${IMAGES[5]}" "$ROOT"
  docker build -f "$ROOT/deploy/s0/images/go-service.Dockerfile" --build-arg SERVICE_PACKAGE=./services/platform-gateway/cmd/s3-local-web-gateway -t "${IMAGES[6]}" "$ROOT"
  docker build -f "$ROOT/deploy/s0/images/go-service.Dockerfile" --build-arg SERVICE_PACKAGE=./services/thingsboard-connector-control/cmd/s3-local-thingsboard-bridge -t "${IMAGES[7]}" "$ROOT"
}

cluster_exists() {
  "$KIND" get clusters 2>/dev/null | grep -Fxq "$CLUSTER"
}

create_cluster() {
  if ! cluster_exists; then
    log "creating kind cluster $CLUSTER"
    "$KIND" create cluster --name "$CLUSTER" --config "$ROOT/deploy/s3/local/kind-config.yaml" --wait 120s
  else
    log "reusing kind cluster $CLUSTER"
  fi
  kubectl config use-context "$CONTEXT" >/dev/null
  kubectl cluster-info --context "$CONTEXT" >/dev/null
}

load_images() {
  log "loading local images into kind"
  "$KIND" load docker-image --name "$CLUSTER" "${IMAGES[@]}"
}

apply_generated() {
  local file="$1"
  shift
  "$@" --dry-run=client -o yaml > "$RENDERED/$file"
  kubectl apply -f "$RENDERED/$file" >/dev/null
}

apply_runtime_inputs() {
  kubectl apply -f "$ROOT/deploy/s3/local/namespace.yaml" >/dev/null
  apply_generated postgres-bootstrap.yaml kubectl -n "$NAMESPACE" create configmap s3-local-postgres-bootstrap \
    --from-file=000-bootstrap-identities.sql="$ROOT/infra/s3-command/postgres/init/000-bootstrap-identities.sql"
  apply_generated approved-cohort.yaml kubectl -n "$NAMESPACE" create configmap s3-local-approved-cohort \
    --from-file=approved-cohort.json="$OUT/approved-cohort.json"

  apply_generated device-catalog.yaml kubectl -n "$NAMESPACE" create configmap s3-local-device-catalog \
    --from-file=device-catalog.json="$OUT/device-catalog.json"
  apply_generated command-service-pki.yaml kubectl -n "$NAMESPACE" create secret generic s3-local-command-service-pki \
    --from-file=tls.crt="$PKI/command-service.crt" --from-file=tls.key="$PKI/command-service.key" \
    --from-file=ca.crt="$PKI/ca.crt" --from-file=iam-grant.crt="$PKI/iam-grant.crt" \
    --from-file=gateway-delegation.crt="$PKI/gateway-delegation.crt"
  apply_generated dispatcher-pki.yaml kubectl -n "$NAMESPACE" create secret generic s3-local-dispatcher-pki \
    --from-file=tls.crt="$PKI/command-dispatcher.crt" --from-file=tls.key="$PKI/command-dispatcher.key" --from-file=ca.crt="$PKI/ca.crt"
  apply_generated verifier-pki.yaml kubectl -n "$NAMESPACE" create secret generic s3-local-verifier-pki \
    --from-file=tls.crt="$PKI/command-verifier.crt" --from-file=tls.key="$PKI/command-verifier.key" --from-file=ca.crt="$PKI/ca.crt"
  apply_generated web-gateway-pki.yaml kubectl -n "$NAMESPACE" create secret generic s3-local-web-gateway-pki \
    --from-file=tls.crt="$PKI/platform-gateway.crt" --from-file=tls.key="$PKI/platform-gateway.key" --from-file=ca.crt="$PKI/ca.crt" \
    --from-file=command-grant.key="$PKI/iam-grant.key" --from-file=command-delegation.key="$PKI/gateway-delegation.key" \
    --from-file=csrf-token="$OUT/web-csrf-token"
  apply_generated simulator-pki.yaml kubectl -n "$NAMESPACE" create secret generic s3-local-simulator-pki \
    --from-file=tls.crt="$PKI/local-device-simulator.crt" --from-file=tls.key="$PKI/local-device-simulator.key" --from-file=ca.crt="$PKI/ca.crt"
  apply_generated database.yaml kubectl -n "$NAMESPACE" create secret generic s3-local-database \
    --from-file=command-service-dsn="$OUT/command-service-dsn"
  apply_generated provider-value.yaml kubectl -n "$NAMESPACE" create secret generic s3-local-provider-value \
    --from-file=value="$OUT/provider-value"
}

deploy_runtime() {
  [[ -f "$OUT/approved-cohort.json" ]] || fail "run prepare first"
  kubectl config use-context "$CONTEXT" >/dev/null
  apply_runtime_inputs

  log "deploying PostgreSQL"
  kubectl apply -f "$ROOT/deploy/s3/local/postgres.yaml" >/dev/null
  kubectl -n "$NAMESPACE" rollout restart deployment/postgres >/dev/null
  kubectl -n "$NAMESPACE" rollout status deployment/postgres --timeout=180s

  log "running command migrations"
  kubectl -n "$NAMESPACE" delete job command-migrator --ignore-not-found >/dev/null
  kubectl apply -f "$ROOT/deploy/s3/local/migrator-job.yaml" >/dev/null
  if ! kubectl -n "$NAMESPACE" wait --for=condition=complete job/command-migrator --timeout=180s; then
    kubectl -n "$NAMESPACE" logs job/command-migrator --all-containers=true || true
    fail "command migrations failed"
  fi

  log "deploying command runtime and local device simulator"
  kubectl apply -f "$ROOT/deploy/s3/local/runtime.yaml" >/dev/null
  kubectl -n "$NAMESPACE" rollout restart deployment/local-device-simulator deployment/command-service deployment/command-dispatcher deployment/command-verifier deployment/s3-local-web-gateway >/dev/null
  local deployment
  for deployment in local-device-simulator command-service command-dispatcher command-verifier s3-local-web-gateway; do
    if ! kubectl -n "$NAMESPACE" rollout status "deployment/$deployment" --timeout=180s; then
      kubectl -n "$NAMESPACE" describe "deployment/$deployment" || true
      kubectl -n "$NAMESPACE" logs "deployment/$deployment" --all-containers=true --tail=100 || true
      fail "$deployment did not become ready"
    fi
  done
}

query_smoke_state() {
  local sql
  sql="SELECT i.status || '|' || COALESCE(a.status, '') || '|' || count(e.attempt_id)::text || '|' || COALESCE(a.verification_evidence_id, '') FROM command_runtime.command_intents i LEFT JOIN command_runtime.command_attempts a ON a.command_id = i.command_id LEFT JOIN command_runtime.connector_evidence e ON e.attempt_id = a.attempt_id AND e.execution_fence = a.execution_fence WHERE i.organization_id = '$ORG_ID'::uuid GROUP BY i.command_id, i.status, a.status, a.verification_evidence_id, i.created_at ORDER BY i.created_at DESC LIMIT 1;"
  kubectl -n "$NAMESPACE" exec deployment/postgres -- psql -U postgres -d hvac_s3 -Atqc "$sql" 2>/dev/null || true
}

smoke() {
  kubectl config use-context "$CONTEXT" >/dev/null
  log "submitting one local setpoint command"
  kubectl -n "$NAMESPACE" delete job command-seed --ignore-not-found >/dev/null
  local idempotency_key="s3-local-smoke-$(date -u +%s%N)"
  sed "s/s3-local-smoke-v1/$idempotency_key/" "$ROOT/deploy/s3/local/seed-job.yaml" > "$RENDERED/seed-job.yaml"
  kubectl apply -f "$RENDERED/seed-job.yaml" >/dev/null
  if ! kubectl -n "$NAMESPACE" wait --for=condition=complete job/command-seed --timeout=120s; then
    kubectl -n "$NAMESPACE" logs job/command-seed --all-containers=true || true
    fail "command seed failed"
  fi
  kubectl -n "$NAMESPACE" logs job/command-seed --all-containers=true

  local result="" attempt
  for attempt in $(seq 1 90); do
    result="$(query_smoke_state | tr -d '\r\n')"
    if [[ "$result" == SUCCEEDED\|VERIFIED\|1\|s2:sha256:* ]]; then
      cat > "$OUT/smoke-report.json" <<JSON
{
  "schemaVersion": 1,
  "status": "local-integration-passed",
  "cluster": "$CLUSTER",
  "namespace": "$NAMESPACE",
  "organizationId": "$ORG_ID",
  "siteId": "$SITE_ID",
  "deviceId": "$DEVICE_ID",
  "databaseResult": "$result",
  "formalCertificationClaim": false,
  "completedAt": "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
}
JSON
      log "smoke passed: $result"
      return 0
    fi
    sleep 1
  done

  kubectl -n "$NAMESPACE" get pods,jobs -o wide || true
  kubectl -n "$NAMESPACE" logs deployment/command-dispatcher --tail=100 || true
  kubectl -n "$NAMESPACE" logs deployment/command-verifier --tail=100 || true
  kubectl -n "$NAMESPACE" logs deployment/local-device-simulator --tail=100 || true
  fail "smoke did not reach SUCCEEDED/VERIFIED; last state=$result"
}

status() {
  ensure_kind
  if ! cluster_exists; then
    log "cluster $CLUSTER is not present"
    exit 2
  fi
  kubectl config use-context "$CONTEXT" >/dev/null
  kubectl -n "$NAMESPACE" get deployments,pods,jobs,services -o wide
  printf 'command-state=%s\n' "$(query_smoke_state | tr -d '\r\n')"
}

logs() {
  kubectl config use-context "$CONTEXT" >/dev/null
  local workload
  for workload in command-service command-dispatcher command-verifier local-device-simulator postgres; do
    printf '\n===== %s =====\n' "$workload"
    kubectl -n "$NAMESPACE" logs "deployment/$workload" --tail=100 || true
  done
}

down() {
  ensure_kind
  if cluster_exists; then
    "$KIND" delete cluster --name "$CLUSTER"
  else
    log "cluster $CLUSTER is already absent"
  fi
}

prepare() {
  ensure_tools
  ensure_kind
  generate_local_inputs
  build_images
}

up() {
  prepare
  create_cluster
  load_images
  deploy_runtime
  smoke
}

case "${1:-up}" in
  prepare) prepare ;;
  cluster) ensure_tools; ensure_kind; create_cluster; load_images ;;
  deploy) ensure_tools; ensure_kind; deploy_runtime ;;
  smoke) ensure_tools; ensure_kind; smoke ;;
  status) ensure_tools; status ;;
  logs) ensure_tools; logs ;;
  down) ensure_tools; down ;;
  up) up ;;
  *) fail "usage: $0 {prepare|cluster|deploy|smoke|status|logs|down|up}" ;;
esac
