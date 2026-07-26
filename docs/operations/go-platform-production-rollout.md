# Go Platform Production Cohort Rollout and Operational Hardening

This runbook implements ADR 0005 and defines the revised S7. `hvac-backend` is not a production dependency, fallback owner, migration source or rollback target.

## Scope

S7 promotes completed Go capabilities from contract-ready to operationally certified production. It does not migrate NestJS data or traffic.

The rollout applies independently to each capability group:

- S1 Registry reads;
- S2 telemetry Snapshot and live delivery;
- S3 governed Command routes after S3 is complete;
- S4–S6 capabilities only after their own release gates pass.

## Promotion phases

The machine-readable phase definition is `deploy/platform/production-rollout.v1.json`.

| Phase | Traffic | Minimum hold | Purpose |
|---|---:|---:|---|
| P0 Contract ready | 0% | none | Contracts, ownership, deployment and rollback assets exist. |
| P1 Internal synthetic | 1% | 2 h | Synthetic source and internal identities only. |
| P2 Internal Site | 5% | 4 h | One controlled internal Site. |
| P3 Limited production | 25% | 8 h | Bounded production cohort. |
| P4 Broad production | 50% | 12 h | Half-traffic capacity and failure observation. |
| P5 Primary | 100% | 24 h | Go capability is primary. |
| P6 Operationally certified | 100% | 7 d | Stable operations, restore and ownership acceptance. |

No phase promotion is automatic. Primary and secondary owners approve every promotion after reviewing evidence.

## Required gates

Every promotion must prove:

- exact tenant and resource authorization;
- zero cross-tenant successful access;
- one business writer for each authoritative state;
- capacity at the declared release envelope;
- single-Pod and single-AZ failure recovery;
- backup restore and projection rebuild;
- bounded route rollback;
- W3C trace continuity and secret-free telemetry;
- an owned alert and runbook for each hard invariant.

For command-capable slices, promotion additionally requires:

- zero duplicate device side effects;
- zero old-Fence execution;
- zero blind retry after `OUTCOME_UNKNOWN`;
- accepted Commands remain with their original owner during rollback;
- route rollback affects only future Commands.

## Rollback

The rollback target is the previous Go phase or a disabled capability. NestJS is not a rollback target.

The decision objective is five minutes and the route-revision objective is fifteen minutes. Data rollback and route rollback remain separate decisions. A rollback must not rewrite authoritative history or transfer already accepted Commands between owners.

## Historical assets

Legacy migration fixtures, S1 phase registries, S2 shadow comparator assets and old retirement evidence may remain for historical regression tests. They are not production prerequisites and must not introduce runtime credentials, DNS dependencies, database access or request fallback.

## Completion

S7 completes when:

1. P6 has satisfied its real hold period;
2. all hard-gate counters are zero;
3. capacity, failure injection, restore, rollback and security evidence is reproducible;
4. production deployment and disaster recovery do not require `hvac-backend`;
5. operational owners accept the service and runbooks;
6. `npm run platform:production-rollout:check` passes.
