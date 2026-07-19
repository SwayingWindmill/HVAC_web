# 08 — S0 integration, rollback and Release Evidence Bundle

**What to build:** integrate the complete S0 foundation, demonstrate the user and operator journeys, execute the full release gates and publish a reproducible evidence bundle that allows S1 implementation to begin without redefining platform conventions.

**Blocked by:** 01 — Contract-first Gateway bootstrap; 02 — Authenticated principal loop; 03 — Durable Session event to Audit Ledger; 04 — Versioned route and data ownership; 05 — End-to-end observability; 06 — Reproducible delivery and supply chain; 07 — Security and failure-injection release gates.

**Status:** ready-for-agent

- [ ] A browser can open platform status, authenticate, view current principal, logout and observe authorized audit history through Gateway.
- [ ] A safe Legacy read path is routed through Gateway and can be rolled back by route revision without exposing Legacy.
- [ ] The full contract, identity, tenant, event, audit, observability, security and failure suites pass from a clean environment.
- [ ] Current and previous compatible contract/service versions interoperate through the rollback window.
- [ ] Staging rolling update and rollback are executed and recorded.
- [ ] Release Evidence Bundle includes versions, environment, fixtures, test outputs, traces, dashboards, failure evidence, SBOM, signatures, migration state, rollback results, runbooks and approvals.
- [ ] Architecture Decision Trace maps every S0 acceptance criterion to source architecture decisions and implementation evidence.
- [ ] Known limitations are explicit and do not include deferred security, tenant, audit or rollback failures.
- [ ] Evidence proves zero cross-tenant success, zero credential leakage, zero duplicate audit effect and zero lost committed Session event in tested failures.
- [ ] S0 public contracts and reusable libraries are documented for S1–S7 consumers.
- [ ] NestJS remains Legacy Frozen and private; S0 completion does not authorize deletion.
- [ ] No Organization/Device/Telemetry/Command/Schedule/AI business scope has leaked into S0.
- [ ] The S0 tracker identifies ticket 01 as the initial frontier and records all dependency edges.
- [ ] Final review explicitly declares S0 complete and S1 ready to enter implementation specification.
