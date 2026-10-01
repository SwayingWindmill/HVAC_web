# Primary checkout live-chain recovery — 2026-10-01

The active Git branch is `codex/dashboard-live-chain-20261001`. The primary checkout remains `/mnt/e/Code/HVAC_web`; no project commands run from the historical acceptance worktree.

## Preserved state and configuration

The existing `hvac-phase1-local` PostgreSQL, ClickHouse, Redis and MQTT named volumes remain in place. No database reset, historical Alarm deletion or queue deletion was performed. The separate `hvac-phase1-dev` environment was not reconfigured.

Certificates, role credentials and configuration from the existing acceptance runtime were copied to the new ignored directory `deploy/platform/phase1/runtime/live-chain-20261001`. The original files remain. Secrets must not be added to Git or included in logs. The copied configuration uses the existing public origin `https://localhost:9443`; it does not use the development environment's credentials.

Docker Desktop originally had default-distribution WSL integration disabled. The user explicitly approved enabling it for Ubuntu-24.04. This setting is now enabled. Project containers were gracefully stopped before the Desktop restart. Subsequent project commands use the Linux Docker CLI and its integrated Unix socket.

The first restore attempt through the guest proxy did not translate WSL bind paths correctly and failed on a file mount. The successful restore used the normal WSL integration and a local ignored Compose overlay pointing certificate mounts at the primary checkout.

## Observed recovery evidence

- PostgreSQL, ClickHouse, Redis, Identity, Telemetry, FDD and Gateway passed their existing healthchecks.
- Web, MQTT and Centrifugo started; Gateway public health returned HTTP 200.
- Telemetry logs show the History projector and Alarm evaluation relay started.
- The old simulator and IoT containers were stopped explicitly. Their queues remain. They must not be treated as a current running acquisition chain.

## Remaining acceptance work

Runtime recovery is not certification of the complete business chain. A current, paced simulator run must preserve measurement sequence continuity and distinguish current observations from offline backlog. Then verify physical disturbance, current telemetry quality/freshness, authoritative Alarm and FDD evidence, operator Work Order actions and physical recovery.

Existing global operations pages still contain browser-session simulation; the corrected product spec requires production owners for this validation. The previous acceptance record also reports historical oversized Alarm responses and Work Order ledger visibility defects. Reproduce these against the recovered services before deciding whether they remain present; do not delete historical data to conceal them.

Merge validation passed for the affected Simulator, Telemetry, Gateway, Work Order and FDD Go packages; telemetry-worker compiled; frontend TypeScript checking, 23 gate-classification/task-matrix tests and the existing Phase 1 deployment check passed. Unconfigured database integration tests and the full browser fault-to-recovery chain are not claimed as passed.
