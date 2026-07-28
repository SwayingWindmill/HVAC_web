# ThingsBoard Telemetry Adapter

The adapter closes the telemetry half of the virtual EG8200 validation loop:

```text
EG8200 simulator -> ThingsBoard time series -> this adapter -> S2 Telemetry Runtime
```

It reads raw device time series through the ThingsBoard tenant REST API, converts provider values into the S2 observation contract, and calls the internal S2 ThingsBoard acceptance endpoint over TLS 1.3 mutual TLS.

## Security boundary

The adapter does not call the S2 endpoint with caller-supplied identity headers. Its workload identity comes only from the client certificate URI SAN. The expected local identity is:

```text
spiffe://hvac.local/thingsboard-telemetry-adapter
```

The Telemetry Runtime must trust that identity for the configured integration instance:

```text
TELEMETRY_SOURCE_BINDINGS_JSON={"spiffe://hvac.local/thingsboard-telemetry-adapter":["018f3e00-0000-7000-8000-000000000101"]}
```

ThingsBoard tenant JWTs, client private keys, and upstream response bodies are never written to logs. Non-local ThingsBoard origins and all Telemetry Runtime origins require HTTPS.

## Incremental delivery semantics

Each configured ThingsBoard point is an independent source partition:

```text
thingsboard:<thingsBoardDeviceId>:<sourceKey>
```

The ThingsBoard sample timestamp in milliseconds is the source offset. The source event ID is a deterministic UUIDv7 derived from timestamp, partition, and normalized value. This provides stable retries and allows S2 to detect duplicates.

The checkpoint file is updated atomically after S2 returns a valid receipt. A failed S2 request does not advance the checkpoint. On restart, the adapter rereads only data after each stored offset. The ThingsBoard JWT file is also reread for every poll, so an external provisioning process can rotate the token without restarting the adapter.

## Configuration

`configs/central-plant.local.example.json` maps the six MVP devices:

- water-cooled chiller;
- chilled-water pump;
- cooling-water pump;
- cooling tower;
- aggregate HVAC electricity meter;
- chilled-water thermal energy meter.

Replace every example `thingsBoardDeviceId` with the actual ThingsBoard entity UUID. `externalId` must match the external identity registered in the S2 runtime binding. S2 field policies must use the configured telemetry keys, value types, and units; otherwise the runtime will quarantine observations.

The example uses these mounted paths:

```text
/run/secrets/thingsboard-jwt
/run/secrets/telemetry-ca.pem
/run/secrets/adapter-cert.pem
/run/secrets/adapter-key.pem
/var/lib/adapter/checkpoint.json
```

The adapter client certificate must contain exactly one SPIFFE URI SAN. The Telemetry Runtime server certificate must match `telemetryRuntime.serverName`.

## Run from source

```bash
export THINGSBOARD_TELEMETRY_ADAPTER_CONFIG="$PWD/services/thingsboard-telemetry-adapter/configs/central-plant.local.example.json"
go run ./services/thingsboard-telemetry-adapter/cmd/thingsboard-telemetry-adapter
```

Diagnostics default to port `19093`:

```text
GET /health/live
GET /health/ready
```

Readiness succeeds only after the latest full poll completed successfully. A later provider or S2 failure immediately makes readiness fail.

## Container image

```bash
npm run build:thingsboard-telemetry-adapter-image
```

Example container invocation:

```bash
docker run --rm \
  --add-host=host.docker.internal:host-gateway \
  -p 19093:19093 \
  -v "$PWD/out/vdev/adapter-config.json:/config/adapter.json:ro" \
  -v "$PWD/out/vdev/provider-authorization:/run/secrets/thingsboard-jwt:ro" \
  -v "$PWD/out/vdev/pki/ca.pem:/run/secrets/telemetry-ca.pem:ro" \
  -v "$PWD/out/vdev/pki/adapter-cert.pem:/run/secrets/adapter-cert.pem:ro" \
  -v "$PWD/out/vdev/pki/adapter-key.pem:/run/secrets/adapter-key.pem:ro" \
  -v vdev-telemetry-checkpoint:/var/lib/adapter \
  hvac/thingsboard-telemetry-adapter:local \
  -config /config/adapter.json
```

## Verification

```bash
npm run test:thingsboard-telemetry-adapter
npm run build:thingsboard-telemetry-adapter
npm run build:thingsboard-telemetry-adapter-image
```

The test suite covers strict configuration, ThingsBoard query shape and JWT handling, type normalization, deterministic source positions, atomic checkpoints, retry suppression, mTLS client authentication, SPIFFE identity, the exact S2 observation path, and a complete HTTP ThingsBoard-to-S2 poll.

## Current scope

This version uses polling because the existing local ThingsBoard profile already exposes tenant REST authentication and stored time series. The transport is isolated behind `TimeseriesSource`; a future MQTT or Rule Engine push adapter can reuse the same normalization, deterministic event identity, checkpoint, and S2 client layers.
