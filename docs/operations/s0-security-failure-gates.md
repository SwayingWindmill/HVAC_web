# S0 security, tenant and failure-injection release gates

## Reuse decisions

Ticket 07 follows a reuse-first rule. General-purpose failure injection, Kubernetes connectivity analysis and image secret scanning are delegated to maintained upstream projects rather than implemented as HVAC-specific frameworks.

| Capability | Selected project | Pin | License | Decision |
| --- | --- | --- | --- | --- |
| Deterministic TCP dependency failures | `Shopify/toxiproxy` | `v2.12.0`, OCI digest `sha256:9378ed52a28bc50edc1350f936f518f31fa95f0d15917d6eb40b8e376d1a214e` | MIT | Used for PostgreSQL availability tests. |
| Kubernetes connectivity proof | `np-guard/netpol-analyzer` | `v1.4.4` | Apache-2.0 | Evaluates checked-in NetworkPolicy against browser, service and private-IP probes. |
| Published-image secret scan | `aquasecurity/trivy-action` | `v0.36.0`, GitHub-verified commit `ed142fd0673e97e23eac54620cfb913e5ce36c25` | Apache-2.0 | Scans every immutable GHCR image digest before signing. |

Testcontainers was considered but rejected because the existing Docker Compose lifecycle already provides isolated real PostgreSQL, Redpanda and observability dependencies. Chaos Mesh was considered for staging but is unnecessary for the deterministic CI matrix and would add a Kubernetes operator. Schemathesis remains suitable for future broad OpenAPI fuzzing, but it cannot replace the project-specific OIDC, delegation and Organization-boundary assertions.

## Release gate

Run the complete local gate with:

```text
npm run security:gates
```

The command runs static asset validation, negative identity and tenant tests, NetworkPolicy analysis, real PostgreSQL durability tests and the production-shaped browser failure matrix. It writes machine-readable results under `out/s0-security/`.

GitHub Actions runs the same gate in the `security-failure-gates` job and uploads `s0-security-gate-results`. Signed image publication depends on this job. Each published image is additionally scanned by Trivy for embedded secrets before Cosign signing and GitHub attestation.

## Failure matrix

The authoritative mapping is `tests/s0-security/security-failure-matrix.json`. Important runtime cases include:

- Two Organizations with user and administrative principals, plus distinct SPIFFE service identities.
- Cross-Organization audit queries and administrative Session revocation return indistinguishable not-found results; no route-diagnostics endpoint is exposed by the public Gateway contract.
- Forged identity headers, invalid mTLS identities, invalid delegation, invalid OIDC, CSRF, Origin and revoked Session paths fail closed.
- Browser-originated traffic reaches only Gateway; IAM, Audit, PostgreSQL and Redpanda are denied.
- Gateway egress to metadata address `169.254.169.254` and private DNS-rebinding target `10.0.0.1` is denied.
- Toxiproxy produces an explicit PostgreSQL-unavailable state without leaking internal details.
- Broker outage, Outbox backlog, forced Audit consumer termination and forced Relay termination recover without losing committed Session events or duplicating audit effects.
- Observability export failure remains non-blocking.
- Go route revision rollback remains rejected and auditable.

## Evidence

Machine-readable artifacts:

- `out/s0-security/security-failure-gate-report.json` — aggregate command results and durations.
- `out/s0-security/network-policy-report.json` — individual allowed/denied connectivity decisions.
- `trivy-secrets-<image>.json` — one GitHub Actions artifact per immutable image.

Code-level evidence remains in the existing generated-contract, OIDC, IAM, Audit, Inbox/Outbox, ownership and public Problem Details tests. The browser matrix begins at the public Gateway seam and observes the durable database, broker, audit and telemetry effects.
