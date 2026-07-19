# 04 — Versioned route and data ownership with controlled Legacy proxy

**What to build:** place all public routing behind a versioned Route Ownership Registry and declare unique writers through a Data Ownership Registry. Demonstrate one Go-owned path and one safe Legacy-owned read path through Gateway, with a private NestJS target and an anti-corruption conversion.

**Blocked by:** 03 — Durable Session event to Audit Ledger.

**Status:** ready-for-agent

- [ ] Route entries declare method/path, owner, revision, rollout/cohort policy, compatibility mode and allowed scope dimensions.
- [ ] Gateway route resolution is deterministic and records the applied policy revision.
- [ ] Missing, conflicting or regressed route ownership fails closed.
- [ ] Stable cohort routing uses a server-derived business key, not an untrusted cookie.
- [ ] One safe Legacy read path is proxied only through Gateway to a private NestJS service.
- [ ] Gateway sends only a restricted internal principal/delegation context to Legacy.
- [ ] Legacy cannot receive browser cookies, refresh tokens or forged identity headers.
- [ ] Anti-corruption conversion contains legacy IDs and response shapes at the Gateway boundary.
- [ ] A Legacy timeout or circuit-open condition returns stable Problem Details and does not bypass to direct access.
- [ ] Route decision and policy change events are auditable.
- [ ] Data Ownership Registry declares one writer for every S0 schema, event family and projection.
- [ ] CI rejects duplicate writers, forbidden cross-service database access and owner revision rollback.
- [ ] Route rollback changes only future requests and is covered by a black-box test.
- [ ] NestJS remains Legacy Frozen; this ticket adds no new Legacy business feature.
