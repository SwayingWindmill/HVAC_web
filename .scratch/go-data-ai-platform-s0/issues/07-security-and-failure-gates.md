# 07 — Security, tenant and failure-injection release gates

**What to build:** run a production-shaped negative and failure matrix against the completed S0 stack, proving that identity, tenant, delivery and Legacy boundaries fail safely and recover without accepted-event loss or duplicate audit effects.

**Blocked by:** 03 — Durable Session event to Audit Ledger; 04 — Versioned route and data ownership; 05 — End-to-end observability; 06 — Reproducible delivery and supply chain.

**Status:** completed

- [x] Test fixtures contain two Organizations, authorized/unauthorized users and distinct service identities.
- [x] Cross-Organization session, route diagnostics and audit access successes are zero.
- [x] Forged principal/Organization/Site headers, invalid mTLS identities and wrong/expired delegation grants are rejected.
- [x] Invalid issuer/audience/token type, revoked session, CSRF and Origin cases are rejected through Gateway.
- [x] NetworkPolicy proves the browser cannot reach IAM, Audit, database, broker or Legacy directly.
- [x] Default-deny egress and SSRF/DNS-rebinding tests block private and metadata targets.
- [x] PostgreSQL failure, broker failure, Outbox backlog, audit consumer crash and Legacy timeout produce safe explicit states.
- [x] Broker/consumer restart and repeated messages produce one audit result and no lost committed Session event.
- [x] Graceful and forced process restarts do not corrupt Inbox/Outbox state.
- [x] Observability outage does not block core behavior.
- [x] Seeded tokens/secrets are absent from repository, image, logs, traces, metrics, events and database diagnostic fields.
- [x] Public errors contain no stack trace, internal address or cross-tenant detail.
- [x] Route ownership conflict and owner revision rollback fail closed and generate security/operational evidence.
- [x] The release gate is executable by CI or a documented staging job and emits machine-readable results.

## Completion evidence

- PR: `https://github.com/SwayingWindmill/HVAC_web/pull/24`
- Merge commit: `d346d1122a2dbd1e25a01623215a5a96540dbe53`
- Main push security and supply-chain run: `https://github.com/SwayingWindmill/HVAC_web/actions/runs/29752821098`
- Final release run: `https://github.com/SwayingWindmill/HVAC_web/actions/runs/29752935721`
- The final release run passed the complete machine-readable security/failure gate and all seven image jobs. Every immutable image passed Trivy embedded-secret scanning, uploaded its JSON report, completed Cosign keyless signing and verification, and published GitHub build provenance.
- Reused and pinned upstream implementations: `Shopify/toxiproxy@v2.12.0`, `np-guard/netpol-analyzer@v1.4.4`, and `aquasecurity/trivy-action@ed142fd0673e97e23eac54620cfb913e5ce36c25`.
