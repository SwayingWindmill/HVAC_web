# MQTT Telemetry Adapter

This service is the MQTT transport adapter for S2 Telemetry Runtime.

```text
EG8200 / Edge Gateway
    -> MQTT v5 QoS 1
        -> Eclipse Mosquitto
            -> mqtt-telemetry-adapter
                -> mTLS S2 source observation endpoint
                    -> telemetry-runtime-service
```

MQTT is transport only. Device/Point mapping, ordering, deduplication, quarantine, current-state revision, Point/Sensor identity and history ownership remain in S2 Telemetry Runtime.

## Topic contract

Telemetry uses exactly:

```text
energy/v1/{tenantId}/{siteId}/{gatewayId}/telemetry
```

The adapter subscribes to `energy/v1/+/+/+/telemetry`, but every message is checked against a configured `gatewayId -> tenantId/siteId` binding before it can reach S2. The tenant, site and gateway values inside the envelope must exactly match the MQTT topic.

The payload schema is `contracts/mqtt/energy-telemetry-envelope.v1.schema.json`.

Each Point owns its own `sampledAt` and `sequence`. The adapter turns that into an S2 Source Position:

```text
partition = mqtt:{gatewayId}:{externalDeviceId}:{telemetryKey}
offset    = point.sequence
eventId   = deterministic UUIDv7(sampledAt, partition, sequence, value)
```

That makes MQTT QoS retransmission and reconnect delivery reuse S2's existing duplicate and out-of-order semantics instead of adding a second dedup store.

Envelope v1 accepts `quality=GOOD` only. Edge fault quality such as TIMEOUT/OFFLINE/PARSE_ERROR is deliberately not coerced to GOOD; it requires a versioned source-quality extension to the S2 observation contract.

## Security

The local Mosquitto profile in `infra/s2-telemetry/mqtt` requires TLS 1.3 client certificates and disables anonymous access. Mosquitto uses certificate Common Name as the ACL username:

- `EG8200-COMMERCIAL-001` may publish only its configured Tenant/Site/Gateway telemetry topic.
- `mqtt-telemetry-adapter` may subscribe only to the configured central-plant telemetry topic family.

The adapter separately uses its SPIFFE client certificate when calling S2. S2 must authorize:

```text
spiffe://hvac.local/mqtt-telemetry-adapter
```

for the configured integration instance.

## Reliability

The adapter uses MQTT QoS 1 and manual acknowledgements. It acknowledges a broker delivery only after all contained Point observations receive durable S2 receipts. Temporary S2 failures are retried in place with bounded exponential backoff while the MQTT delivery remains unacknowledged; reconnect redelivery remains an additional recovery path. Invalid, malformed or unauthorized MQTT messages are treated as permanent poison messages, logged, and acknowledged after rejection so they cannot block the durable session forever.

The EG8200 MQTT publisher uses Paho's persistent file queue. It can continue queueing publishes while disconnected and drains them after reconnect. `maximumQueueBytes` is checked before enqueue so the local store cannot grow without bound.

## Local configuration

Adapter example:

```text
services/mqtt-telemetry-adapter/configs/central-plant.local.example.json
```

Gateway publisher example:

```text
tools/eg8200-simulator/configs/central-plant.mqtt.local.example.json
```

The central-plant PKI generator emits:

```text
mqtt-broker-cert.pem / mqtt-broker-key.pem
mqtt-adapter-cert.pem / mqtt-adapter-key.pem
mqtt-gateway-cert.pem / mqtt-gateway-key.pem
ca.pem
```

## Verification

```text
npm run test:mqtt-telemetry-adapter
npm run build:mqtt-telemetry-adapter
npm run build:eg8200-mqtt-publisher
```

The Docker-backed Mosquitto integration gate is executed separately because it requires the WSL Linux Docker toolchain.
