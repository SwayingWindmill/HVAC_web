# Connectivity

One Connectivity process serves every Tenant, Site and Gateway (ADR 0015).

```text
Gateway --MQTT v5 QoS 1, mTLS--> Mosquitto --> Connectivity --mTLS--> Telemetry Runtime
```

## Uplink

A Gateway's certificate CN is its Registry Device id. It publishes to:

```text
hvac/v1/{gatewayId}/up/telemetry
hvac/v1/{gatewayId}/up/event
```

The payloads are `contracts/mqtt/gateway-uplink.v2.schema.json`. Messages carry no Tenant or Site.

For each message Connectivity:

1. finds the Gateway in the Registry gateway directory (`core_registry.gateway_directory_v1`) and checks that it holds an active Gateway Credential;
2. resolves each Device by its source key (`gateway_device_source_keys_v1`) and each Point by its Point Code (`point_bindings_v1`), inside the Gateway's Tenant;
3. sends Telemetry the resolved Tenant, Site, Device and Point with every value. An unregistered Device or Point is still sent and Telemetry quarantines it with evidence.

A message from an unknown Gateway, from a Gateway without an active credential, or that cannot be parsed is written to `connectivity.uplink_quarantine` and acknowledged.

## Commands

When `COMMAND_RUNTIME_IN_PROCESS_ENABLED=true`, Connectivity also dispatches and verifies commands for every Tenant over one MQTT connection (identity `command-dispatcher`). It publishes to `hvac/v1/{gatewayId}/down/command` and reads every reply from `hvac/v1/+/up/reply`; a reply's Tenant comes from the Registry gateway directory.

Where a command goes comes from the Registry read port, inside the command's Tenant. Before anything is sent, a command is rejected with:

| Code | When |
| --- | --- |
| `COMMAND_ROUTE_NOT_FOUND` | the Device is not behind an active Gateway (`gateway_device_source_keys_v1`) |
| `COMMAND_CONTROL_DISABLED` | the command Point is not an active writable Point of the Device (`point_bindings_v1`) |
| `COMMAND_GATEWAY_CREDENTIAL_INACTIVE` | the Gateway holds no active Gateway Credential |

The device is addressed on the wire by its source key. If the command runtime is enabled but cannot start, the process exits.

## Ordering and backpressure

Each Gateway has its own queue and worker, created when its first message arrives. A message is acknowledged only after it is processed or quarantined. A transient failure is retried in place, so a Gateway's messages stay in order. When a Gateway's queue is full, Connectivity stops taking messages and the broker holds the backlog; nothing is dropped. `hvac_mqtt_gateway_queue_depth{gateway_id}` shows each Gateway's backlog.

## Broker

`infra/telemetry/mqtt/acl` confines every Gateway to `hvac/v1/{its CN}/up/#` and `hvac/v1/{its CN}/down/#` with static patterns, so adding a Gateway changes no broker configuration.

## Configuration

Environment variables, with defaults for the Phase 1 compose network:

| Variable | Default |
| --- | --- |
| `CONNECTIVITY_MQTT_URL` | `tls://mqtt-broker:8883` |
| `CONNECTIVITY_MQTT_CLIENT_ID` | `connectivity` |
| `CONNECTIVITY_TELEMETRY_URL` | `https://telemetry-runtime-service:8446` |
| `CONNECTIVITY_TLS_CERT` / `CONNECTIVITY_TLS_KEY` | `/run/hvac/pki/mqtt-telemetry-adapter/tls.*` |
| `CONNECTIVITY_CA` | `/run/hvac/pki/ca.crt` |
| `CONNECTIVITY_DATABASE_URL` | required |

| `COMMAND_RUNTIME_IN_PROCESS_ENABLED` | off; `true` runs the command runtime |
| `COMMAND_DISPATCHER_CERT` / `COMMAND_DISPATCHER_KEY` | `/run/hvac/pki/command-dispatcher/tls.*` (MQTT, command owner, dispatch safety) |
| `COMMAND_VERIFIER_CERT` / `COMMAND_VERIFIER_KEY` | `/run/hvac/pki/command-verifier/tls.*` (command owner, reported state) |
| `COMMAND_RUNTIME_CA` | `/run/hvac/pki/ca.crt` |
| `COMMAND_RUNTIME_URL` / `COMMAND_RUNTIME_SERVER_NAME` | `https://command-service:8447` / `command-service` |
| `COMMAND_TELEMETRY_URL` / `COMMAND_TELEMETRY_SERVER_NAME` | `https://telemetry-runtime-service:8446` / `telemetry-runtime-service` |

## Verification

```text
(cd modules/connectivity && go test ./...)
node scripts/run-s1-registry-postgres-tests.mjs   # includes the Connectivity store against the Registry read port
```
