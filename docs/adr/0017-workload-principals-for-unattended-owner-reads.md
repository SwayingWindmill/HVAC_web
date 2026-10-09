# ADR 0017 — Unattended workloads read owners as their own Workload Principal

Status: accepted

Date: 2026-10-08

An unattended workload that must read an owner's HTTP interface, such as the energy read-model projector resolving meter bindings from Registry, does so as its own Workload Principal. IAM keeps one principal per workload identity (issuer = the SPIFFE trust domain, subject = the workload's SPIFFE ID) with ordinary Tenant memberships and role bindings, evaluates it with the existing Registry authorization rules and signs a normal short-lived Registry grant whose presenter and subject are that same workload. The workload asks IAM over mutual TLS with no delegation header and refreshes the grant before it expires. A Registry grant is therefore exactly one of two shapes: delegated from a user session (session and parent token present), or a Workload Principal's own (no session, subject equals presenter). We chose this because the projector had only a static grant input that the 30-second grant lifetime always expires, so energy facts were produced in no environment, and because the energy content contract already requires a delegation grant rather than a borrowed UI session. Keeping the workload inside IAM preserves per-tenant revocation, policy-revision checks and decision audit at the Registry seam, which is how OAuth 2.0 client credentials (RFC 6749 §4.4), Keycloak service accounts and Kubernetes ServiceAccount tokens treat unattended clients.

## Considered Options

- **Trust the workload identity alone at Registry** (how the ThingsBoard rule engine and Istio `AuthorizationPolicy` work). Rejected: any compromise of the projector would read every Tenant on that route, and Registry would lose the revocation and audit it was designed around. Pipelines that receive already-resolved facts with the request (ADR 0016) keep using identity alone; this ADR applies only when a workload reads an owner itself.
- **A standing delegation from a tenant administrator** (RFC 8693 `may_act`). Rejected: it ties unattended processing to a person's account, so disabling that administrator silently stops energy facts.

## Consequences

- A workload co-located in another process presents that process's identity. In Phase 1 the projector runs inside the Telemetry Runtime, so the Workload Principal is `spiffe://hvac.local/telemetry-runtime-service`; when the projector runs alone it is `spiffe://hvac.local/analytics-read-model-projector`.
- Granting or revoking a Workload Principal's Tenant role binding is the only way to change what it can read; there is no static grant file.
