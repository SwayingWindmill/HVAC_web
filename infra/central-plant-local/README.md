# Central Plant Local Stack

This stack runs the real central-plant telemetry path on isolated loopback ports:

```text
EG8200 MQTT publisher -> Eclipse Mosquitto -> mqtt-telemetry-adapter -> S2 Telemetry Runtime
                                                -> PostgreSQL / ClickHouse -> Platform Gateway -> HVAC Web Real
```

## Requirements

- Docker Desktop with Linux containers
- Node.js compatible with the repository toolchain
- Go 1.25 or later
- Microsoft Edge, Chrome, or Chromium for the browser smoke test

No fixed service port is required. The launcher allocates loopback ports dynamically and uses isolated Docker Compose projects.

## Start the stack

```bash
npm run central-plant:local
```

The command prints the HVAC Web Real URL and MQTT broker endpoint after all dependencies are ready. It keeps running until `Ctrl+C`, then removes its containers, networks, volumes, and child processes.

## Run the closed-loop smoke test

```bash
npm run central-plant:local:smoke
```

The stack uses the V2 central-plant Point contract as the source for the seven Device identities and 48 telemetry Points. Live data enters S2 only through the mTLS MQTT path. Historical aggregate HVAC Energy is seeded directly into the local Analytics fact model so the local fixture does not need a second provider-specific replay path.

The smoke flow verifies the real Site/Registry/Telemetry/Gateway/Web path and writes its result to `out/central-plant-local/smoke-report.json`. Runtime resources are removed before the command exits.

## Security and isolation

Generated certificates, MQTT client state, runtime configuration, and local-only database credentials are kept below `out/central-plant-local` or inside the isolated Compose projects. These assets are excluded from Git, and logs do not print credentials or private keys.

The production Route Ownership Registry remains unchanged. The launcher writes a local copy below `out/` and enables the S2 telemetry routes only for this isolated topology.

The MQTT transport reuses the same EG8200 point scheduler and S2 integration instance. `npm run test:mqtt-telemetry-adapter` provides the focused Go verification; `npm run s2:mqtt:integration` is the optional Docker-backed broker/mTLS/ACL integration check.
