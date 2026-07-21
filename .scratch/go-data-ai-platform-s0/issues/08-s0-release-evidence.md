# 08 — S0 integration, rollback and Release Evidence Bundle

**What to build:** integrate the complete S0 foundation, demonstrate the user and operator journeys, execute the full release gates and publish a reproducible evidence bundle that allows S1 implementation to begin without redefining platform conventions.

**Blocked by:** 01 — Contract-first Gateway bootstrap; 02 — Authenticated principal loop; 03 — Durable Session event to Audit Ledger; 04 — Versioned route and data ownership; 05 — End-to-end observability; 06 — Reproducible delivery and supply chain; 07 — Security and failure-injection release gates.

**Status:** completed

- [x] A browser can open platform status, authenticate, view current principal, logout and observe authorized audit history through Gateway.
- [x] A safe Legacy read path is routed through Gateway and can be rolled back by route revision without exposing Legacy.
- [x] The full contract, identity, tenant, event, audit, observability, security and failure suites pass from a clean environment.
- [x] Current and previous compatible contract/service versions interoperate through the rollback window.
- [x] Staging rolling update and rollback are executed and recorded.
- [x] Release Evidence Bundle includes versions, environment, fixtures, test outputs, traces, dashboards, failure evidence, SBOM, signatures, migration state, rollback results, runbooks and approvals.
- [x] Architecture Decision Trace maps every S0 acceptance criterion to source architecture decisions and implementation evidence.
- [x] Known limitations are explicit and do not include deferred security, tenant, audit or rollback failures.
- [x] Evidence proves zero cross-tenant success, zero credential leakage, zero duplicate audit effect and zero lost committed Session event in tested failures.
- [x] S0 public contracts and reusable libraries are documented for S1–S7 consumers.
- [x] NestJS remains Legacy Frozen and private; S0 completion does not authorize deletion.
- [x] No Organization/Device/Telemetry/Command/Schedule/AI business scope has leaked into S0.
- [x] The S0 tracker identifies ticket 01 as the initial frontier and records all dependency edges.
- [x] Final review explicitly declares S0 complete and S1 ready to enter implementation specification.

## Final evidence

- Implementation PR: `https://github.com/SwayingWindmill/HVAC_web/pull/27`
- Evidence-rendering correction PR: `https://github.com/SwayingWindmill/HVAC_web/pull/28`
- Final main commit: `4d4da9cfcc1abebe4a0d706292175856094b4ece`
- Formal release run: `https://github.com/SwayingWindmill/HVAC_web/actions/runs/29763231123`
- Release Evidence Bundle artifact: `s0-release-evidence-bundle` (`8469877292`)
- Machine-readable statement: `release-evidence.intoto.json`
- Integrity manifest: `SHA256SUMS`; all 11 listed files verified after download.
- Statement result: `passed`; approval eligibility: `true`; seven immutable image subjects; fourteen acceptance criteria.
- Zero invariants: cross-tenant successes `0`; credential leak findings `0`; duplicate Audit effects `0`; lost committed Session events `0`.

### Immutable release images

| Image | Digest |
|---|---|
| `audit-ledger-service` | `sha256:83c3645b78c14d0b88196ec181cf1d8d543d8858300071ed8ad7b1c7ee86a044` |
| `iam-service` | `sha256:f314d902f36b92235f55e9e5cc464fe360e2866d6eaa3ebaed416952e3481993` |
| `legacy-private` | `sha256:a82dd317099fb9ec3bdc56ab061444031fc25ee78aadb44eb2f87d70c29f3eee` |
| `oidc-test-provider` | `sha256:5538868ca00872714713967012d2f9d8700270e81fa73ca383228a807901987d` |
| `outbox-relay` | `sha256:761d97853c408ec32372ee5a18395f24845a5adab0a336921c9df42bd705df14` |
| `platform-gateway` | `sha256:5e1b2b01cea5d8a106a6d7c0c075c55f13054ecc428bb9fbe4c5a85add16f88f` |
| `s0-migrator` | `sha256:f5bfe5240a8c435184d6252aaa0b21866c5d36e393b41defa5ff9981cf94b0e0` |

## Completion declaration

S0 is complete. S1 is ready to enter implementation specification. This declaration does not authorize S1 implementation without its own accepted specification.
