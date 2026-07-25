# S2 Ticket 08 — Legacy shadow comparison and cohort routing

## Delivery boundary

Ticket 08 delivers the route-control and comparison assets needed before a production canary. It does **not** activate R1, R2, or R3 in the production route registry. `contracts/ownership/route-ownership.v1.json` remains `R0-contract-only` with all four S2 current-state routes disabled and production traffic at 0%.

The four surfaces form one atomic cohort group, `s2-current-state-v1`:

- single Device Snapshot;
- batch Snapshot;
- live subscription bootstrap;
- live Recovery Cursor checkpoint.

The business identity is the acting Organization plus the initiating principal subject. A browser cannot select an owner. For percentage phases, the Gateway-owned registry hashes the stable business identity with the cohort group and route revision. This makes single, batch, and live surfaces sticky to the same owner while ensuring a new route revision creates a new auditable cohort assignment.

## Phase revisions

`deploy/s2/shadow-routing-revisions.v1.json` freezes the adjacent sequence:

| Phase | Registry revision | Route revision | Public reader | S2 behavior |
| --- | ---: | ---: | --- | --- |
| R0 contract only | 7 | 1 | Legacy existing surface | Contracts and owner boundary only; new public routes disabled |
| R1 dark ingest | 8 | 2 | Legacy | Telemetry Runtime writes only its own `telemetry_runtime` schema |
| R2 shadow compare | 9 | 3 | Legacy | Offline comparator compares independently read Legacy and S2 state |
| R3 internal canary | 10 | 4 | Cohort owner | 1% internal cohort routes to Telemetry Runtime; remaining cohort routes to Legacy |
| R3 → R2 rollback | 11 | 5 | Legacy | Restore prior accepted owner, invalidate live sessions, require a fresh Snapshot |

Only adjacent phase transitions are accepted. A route policy change must advance both registry and route revisions. All routes in the cohort group must have identical phase, owner, rollout, compatibility mode, and route revision.

## Dark ingest isolation

R1/R2 preserve Legacy as the public current-state reader. Telemetry Runtime can write only its own S2 schema. The rollout plan explicitly freezes:

- zero cross-writes;
- zero shared caches;
- no request-level fallback;
- no browser-side owner selection;
- no Legacy current-state deletion;
- no historical telemetry ownership change.

The existing production registry remains R0, so Ticket 08 itself introduces no dark-ingest or canary traffic.

## Shadow comparator

`telemetry-shadow-comparator` is an offline CLI. It reads one bounded JSON input and writes one audit report. It imports generated S2 Snapshot DTOs and has no HTTP server, database driver, service identity, token signer, message client, Redis/Centrifugo client, ThingsBoard client, or route mutation API.

`deploy/s2/shadow-comparator-policy.v1.json` denies all ingress and egress and grants no database, publish, subscribe, authorization, mapping-repair, token-mint, route-change, or serving-path capability. Comparator failure blocks promotion evidence but cannot affect Legacy or S2 serving.

The report records:

- Legacy-to-S2 Device mapping mismatches;
- missing and extra Devices;
- overlapping accepted-value differences;
- timestamp differences relative to the configured sample interval;
- classified differences between Legacy `active` and S2 Presence, Availability, Display State, and stale/unknown semantics;
- zero-valued side-effect evidence.

Promotion thresholds are frozen:

- mapping mismatch: 0;
- missing/extra Device: 0;
- unmatched accepted values on either side: 0;
- accepted-value agreement across overlapping values: at least 99.9%;
- timestamp agreement within the expected sample interval: at least 99.5%;
- unclassified semantic differences: 0.

## Rollback and live sessions

`ownershipregistry.Manager.ReloadS2` is the only accepted path for changing an S2 policy once the previous phase is R3 or later. Ordinary `Reload` rejects such transitions.

Before the registry changes, the supplied live-session invalidator must successfully acknowledge a command containing:

- previous and next registry/route revisions;
- previous and next owners/phases;
- `disconnectOrExpire=true`;
- `freshSnapshotRequired=true`;
- `databaseAction=EXPAND_ONLY_NO_DOWN_MIGRATION`;
- an explicit rollback flag when the phase rank decreases.

If invalidation fails, the active route revision remains unchanged. A successful rollback restores the prior accepted owner, disconnects or expires affected live sessions, and forces the browser back through an authoritative Snapshot. Database rollback is forbidden; schema changes remain expand-only.

The production Runtime adapter for invalidating a concrete canary cohort is intentionally not activated in Ticket 08 because production remains R0. A future activation ticket must provide that adapter before R3 can be applied.

## Evidence

Run:

```bash
npm run s2:ticket-08
```

The suite verifies static invariants, all ownership transitions, the offline comparator, a real comparison fixture, 20,000 deterministic cohort identities across all four surfaces, revision-bound cohort reassignment, and R3-to-R2 rollback.

Evidence:

- `out/s2-ticket-08/shadow-routing.json`
- `out/s2-ticket-08/shadow-comparison.json`
- `out/s2-ticket-08/shadow-routing-harness.json`

## Out of scope

Ticket 08 does not activate production canary traffic, migrate HVAC Web pages, delete Legacy current-state code, change historical telemetry ownership, or permit the comparator to participate in serving decisions.
