# 06 — Reproducible local/staging delivery and signed supply chain

**What to build:** let a developer start the complete S0 stack predictably and let a release engineer deploy the same signed artifacts to a production-shaped staging environment with probes, graceful shutdown, isolated identities and rollback.

**Blocked by:** 01 — Contract-first Gateway bootstrap; 02 — Authenticated principal loop; 03 — Durable Session event to Audit Ledger; 04 — Versioned route and data ownership.

**Status:** implemented-awaiting-release-workflow-verification

Local verification is complete. The release-only GHCR SBOM/provenance, GitHub attestation and keyless Cosign signing job still requires a tagged or manually dispatched GitHub Actions run with GitHub OIDC.

- [x] One documented command starts Gateway, IAM, Audit, PostgreSQL, Kafka-compatible broker, OIDC test provider and OpenTelemetry Collector.
- [x] Local/test require no production credentials and cannot reach production ThingsBoard or production webhooks.
- [x] Environment configuration is explicit and validates required issuer, audience, trust domain and endpoints.
- [x] Each binary has startup, readiness and liveness probes with non-cascading semantics.
- [x] SIGTERM performs bounded HTTP/RPC drain, stops new consumer work and preserves Outbox/Inbox correctness.
- [x] Runtime containers run non-root with minimal capabilities, resource limits and read-only root filesystem where practical.
- [x] ServiceAccounts and database runtime identities are service-specific.
- [x] Database migration identity is separate and application startup performs no destructive migration.
- [x] CI runs Go tests, frontend type/build, contract compatibility, generated-code diff, secret/SAST/dependency scans and license checks.
- [x] CI emits SBOM and build provenance and verifies signed immutable images.
- [x] Staging validates mTLS, NetworkPolicy, default-deny ingress/egress and private Legacy reachability.
- [x] A rolling update maintains safe availability and a previous compatible version rollback is demonstrated.
- [x] Expand-contract database compatibility is proven across the rollback window.
- [x] Deployment documentation records owners, required dependencies and recovery steps.
