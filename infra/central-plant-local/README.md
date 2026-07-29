# Central Plant Local Stack

This stack runs the real central-plant telemetry path on isolated loopback ports:

```text
EG8200 simulator -> ThingsBoard -> telemetry adapter -> S2 Telemetry Runtime
                  -> PostgreSQL -> Platform Gateway -> HVAC Web Real
```

## Requirements

- Docker Desktop with Linux containers
- Node.js compatible with the repository toolchain
- Go 1.25 or later
- Microsoft Edge, Chrome, or Chromium for the browser smoke test

No fixed service port is required. The launcher allocates loopback ports dynamically and uses an isolated Docker Compose project.

## Start the stack

```bash
npm run central-plant:local
```

The command prints the HVAC Web Real and ThingsBoard URLs after all dependencies are ready. It keeps running until `Ctrl+C`, then removes its containers, networks, volumes, and child processes.

## Run the closed-loop smoke test

```bash
npm run central-plant:local:smoke
```

The smoke test starts from empty databases and verifies:

- six ThingsBoard devices are provisioned with runtime-only credentials;
- all 44 configured telemetry points reach PostgreSQL-backed S2;
- all six S2 device snapshots are current and no observation is quarantined;
- the S2 realtime outbox publishes successfully and drains pending work;
- OIDC login reaches the Real Site Assets route;
- Registry returns the six central-plant devices;
- the CHILLER snapshot exposes fresh, good-quality COP, power, and cooling-capacity values through Platform Gateway.

The result is written to `out/central-plant-local/smoke-report.json` and all runtime resources are removed before the command exits.

## Security and isolation

Generated certificates, ThingsBoard tokens, tenant authorization, checkpoints, runtime configuration, and local-only database credentials are kept below `out/central-plant-local` or inside the isolated Compose project. These assets are excluded from Git, and logs do not print credentials or private keys.

The production Route Ownership Registry remains unchanged. The launcher writes a local copy below `out/` and enables the four S2 telemetry routes only for this isolated topology.
