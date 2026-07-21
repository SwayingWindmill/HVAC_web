# 07 — Tenant, migration, failure and rollback gates

**What to build:** turn S1 security, capacity, migration and recovery requirements into blocking automated release gates. Exercise the complete public API and browser journey against two Organizations, cross-organization SiteBinding, PostgreSQL RLS, Legacy Shadow/rollback, IAM/Core failures, architecture-scale Registry data and isolated backup restore. The gate must produce machine-readable evidence and preserve every zero invariant.

**Blocked by:** 02 — IAM Registry-read authorization projection; 03 — Core Registry read model and PostgreSQL isolation; 04 — Legacy mapping, backfill and quarantine; 05 — Gateway Registry API and generated clients; 06 — HVAC Web real Organization/Site/Device pages.

**Status:** ready-after-spec-approval

- [ ] A single documented command runs contract, IAM, Core, Gateway, browser, migration, RLS, observability, capacity, restore and rollback gates.
- [ ] The tenant matrix contains at least two owning Organizations, multiple Sites, a cross-organization SiteBinding, explicit deny, revoked access and no-access principals.
- [ ] Cross-Organization and unauthorized sibling-Site successful reads are zero across every list/detail endpoint.
- [ ] Existence disclosure through status, Problem Details, cursor reuse, count, pagination metadata or defined timing buckets is zero.
- [ ] Forged Organization/Site/role/admin/scope headers and wrong-audience/stale/revoked grants are rejected and audited.
- [ ] Logto Organization/role/custom claims cannot directly grant HVAC Registry access; claim/platform-policy mismatches follow platform IAM and produce safe reconciliation evidence.
- [ ] Logto login, JWKS rotation, disabled user, logout, Management API outage and identity-reconciliation conflict paths fail explicitly without widening Scope.
- [ ] Tests intentionally remove an application predicate and independently prove RLS still blocks the unauthorized row.
- [ ] Tests intentionally alter RLS in an isolated fixture and independently prove application predicates still block unauthorized rows.
- [ ] Cursor tests cover tampering, Scope/filter/route mismatch, unsupported query revision, limit abuse and authorization changes between pages.
- [ ] Migration tests prove ambiguous or duplicate Legacy records remain quarantined and never appear in public Registry responses.
- [ ] Instrumentation proves Registry reads trigger zero ThingsBoard full-sync or direct ThingsBoard calls.
- [ ] Shadow Compare proves one authoritative response, bounded resource use and zero tolerated authorization differences.
- [ ] Route canary and rollback are executed by revision; rollback affects future reads only and never enables Legacy writes or double writes.
- [ ] Failure injection covers IAM database, Core PostgreSQL, Legacy, Shadow worker, Audit, invalid certificates, route registry and service restarts.
- [ ] Dependency failures return explicit safe states and never widen tenant Scope or expose internal addresses, stack traces or credentials.
- [ ] Capacity evidence uses up to 300 Sites and 600,000 Devices, exercises a controlled 2,000 QPS peak, and records P50/P95/P99, error rate, pool saturation, RLS overhead and query plans.
- [ ] Representative list/detail reads meet P95 ≤ 300 ms and P99 ≤ 1 s in the declared staging profile or block release with measured evidence.
- [ ] An isolated restore rebuilds IAM/Core S1 data, mappings, provenance and Quarantine and reruns tenant and hash verification.
- [ ] Repository, image, log, trace, metric, event and report scans find zero cookies, tokens, grants or integration secrets.
- [ ] Machine-readable reports include environment, fixtures, versions, run/trace IDs, start/end time, raw evidence references and conclusion.
