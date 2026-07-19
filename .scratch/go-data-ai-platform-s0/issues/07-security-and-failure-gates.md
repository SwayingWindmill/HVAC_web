# 07 — Security, tenant and failure-injection release gates

**What to build:** run a production-shaped negative and failure matrix against the completed S0 stack, proving that identity, tenant, delivery and Legacy boundaries fail safely and recover without accepted-event loss or duplicate audit effects.

**Blocked by:** 03 — Durable Session event to Audit Ledger; 04 — Versioned route and data ownership; 05 — End-to-end observability; 06 — Reproducible delivery and supply chain.

**Status:** ready-for-agent

- [ ] Test fixtures contain two Organizations, authorized/unauthorized users and distinct service identities.
- [ ] Cross-Organization session, route diagnostics and audit access successes are zero.
- [ ] Forged principal/Organization/Site headers, invalid mTLS identities and wrong/expired delegation grants are rejected.
- [ ] Invalid issuer/audience/token type, revoked session, CSRF and Origin cases are rejected through Gateway.
- [ ] NetworkPolicy proves the browser cannot reach IAM, Audit, database, broker or Legacy directly.
- [ ] Default-deny egress and SSRF/DNS-rebinding tests block private and metadata targets.
- [ ] PostgreSQL failure, broker failure, Outbox backlog, audit consumer crash and Legacy timeout produce safe explicit states.
- [ ] Broker/consumer restart and repeated messages produce one audit result and no lost committed Session event.
- [ ] Graceful and forced process restarts do not corrupt Inbox/Outbox state.
- [ ] Observability outage does not block core behavior.
- [ ] Seeded tokens/secrets are absent from repository, image, logs, traces, metrics, events and database diagnostic fields.
- [ ] Public errors contain no stack trace, internal address or cross-tenant detail.
- [ ] Route ownership conflict and owner revision rollback fail closed and generate security/operational evidence.
- [ ] The release gate is executable by CI or a documented staging job and emits machine-readable results.
