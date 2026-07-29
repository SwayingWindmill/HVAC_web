# S2 capacity, failure injection, and release evidence

Ticket: #70

## Release boundary

This ticket operationalizes `initial-production-release-envelope-v1`. It does not increase the frozen envelope and it does not promote a production cohort. The certified ceiling remains:

- 5,000 concurrent realtime connections;
- 50,000 exact-scope subscriptions;
- 2,000 Business Revisions per second;
- 60-minute steady-state and 15-minute 2× peak windows;
- at least 30% measured resource headroom;
- 256 KiB client queue, 64 KiB publication, 256 publications / 180 seconds history, and a 120-second Recovery Cursor.

## Two profiles

### Pull-request preflight

`npm run s2:capacity` performs a configuration-only clean-runner preflight. It loads and validates the frozen envelope, zero-tolerance contract, failure-scenario set, and formal-attestation requirements without generating modeled latency or resource measurements. Its report is explicitly marked:

- `certificationLevel: clean-runner-preflight`;
- `formalReleaseEligible: false`;
- `measurementSource: configuration-only-preflight`;
- `measured: false`, with `observed` and `zeroCounters` set to `null`.

A passing preflight is not production certification. It proves that the release envelope, bounds, scripts, reports, image definitions, rollback controls, and evidence verifier remain internally consistent; it does not prove that the capacity or failure targets were exercised.

### Formal certification

The `full` workflow profile requires JSON matching `deploy/s2/full-capacity-attestation.schema.json`. The attestation must contain:

- the exact repository SHA;
- at least a 60-minute steady-state and a 15-minute peak run;
- environment, fixture, and load descriptions;
- measured p99 latency, queue, CPU, memory, Redis, PostgreSQL pool, network, and headroom values;
- reconnect, slow-consumer, revocation, and all seven failure-scenario results;
- zero OOM, crash, unbounded queue, security-invariant, and Business Revision corruption counters;
- explicit manual approval and reviewer identity.

The formal path reads measured values from this approved attestation. It never substitutes modeled measurements. `build-s2-release-evidence.mjs --require-formal=true` rejects preflight capacity reports.

## Blocking workflow

`.github/workflows/s2-telemetry-release.yml` has nine blocking jobs:

1. `contracts-and-static`
2. `security-negative`
3. `postgres-integration`
4. `transport-integration`
5. `capacity-and-failure`
6. `browser-real-mode`
7. `production-images`
8. `kind-rollout-rollback`
9. `release-evidence`

The final job has explicit `needs` on every prior job. Missing reports, failed commands, failed image scans, non-root drift, incomplete Kind rollback, or an invalid capacity profile prevent the bundle from being created.

## Production images

The Telemetry Runtime, history projector, and migrator are separate immutable images:

- `deploy/s2/images/telemetry-runtime.Dockerfile` builds a static Go binary and runs it as distroless UID 65532;
- `deploy/s2/images/telemetry-history-projector.Dockerfile` builds the PostgreSQL-outbox-to-ClickHouse projector as a static Go binary and runs it as distroless UID 65532;
- `deploy/s2/images/telemetry-runtime-migrator.Dockerfile` runs only expand-only S2 migrations as the PostgreSQL non-root user;
- every preflight and formal image produces a Trivy CycloneDX SBOM and Buildx metadata with recorded SHA-256 digests;
- Trivy scans high and critical vulnerabilities plus embedded secrets;
- formal image publication additionally requires BuildKit SBOM, `provenance: mode=max`, immutable registry digest, and GitHub build provenance;
- preflight evidence is explicitly marked non-formal and cannot satisfy the formal bundle gate;
- image evidence records the digest, runtime user, scan counts, SBOM, Buildx metadata, and provenance mode.

No database down migration is part of rollback.

## Failure certification

The capacity report and companion reports cover:

- 10,000 clients reconnecting at 1,000 clients/s;
- 1% slow consumers with bounded disconnect behavior;
- history overflow and epoch reset requiring a fresh Snapshot;
- Centrifugo node loss;
- Redis and PostgreSQL failover;
- IAM outage and upstream outage with fail-closed behavior and no request fallback;
- 100 revocations/s with zero post-revocation delivery, stale Cursor acceptance, or retained browser Last Known state;
- 15-minute 2× peak with zero OOM, crash, unbounded queue, security violation, or business corruption.

## Kind rollout and rollback

`npm run audit:s2-kind-rollout` uses a disposable Kind cluster and a hardened two-replica control-plane fixture. The drill performs a rolling update, changes the route revision, then runs `rollout undo` and restores route revision R3 with `freshSnapshotRequired: true`.

The report proves:

- maxUnavailable is zero;
- both replicas become ready;
- rollout and rollback remain within 15 minutes;
- live sessions must disconnect or expire;
- a fresh Snapshot is mandatory after rollback;
- the database remains expand-contract compatible and performs no down migration.

The fixture validates rollout controls only. The separately built production images are validated by the image job.

## Offline release evidence

`npm run s2:release-evidence` requires every report named in `deploy/s2/release-gates.v1.json`, writes `release-evidence.intoto.json`, and creates `SHA256SUMS`. `npm run s2:release-evidence:verify` recalculates every digest and validates the in-toto-style statement without network access.

The bundle records the repository SHA, workflow run, image digests, environment/load evidence, SLO values, zero counters, security reports, metric cardinality, trace correlation, log redaction, shadow comparison, browser evidence, Kind rollback, SBOM, and provenance.

## Commands

```text
npm run s2:release:check
npm run s2:capacity
npm run s2:ticket-11
npm run audit:s2-kind-rollout
npm run s2:release-evidence
npm run s2:release-evidence:verify
```

Formal certification is started through workflow dispatch with `profile=full` and an approved `wall_clock_attestation_json`. A pull-request preflight must never be described as a formal production release certification.
