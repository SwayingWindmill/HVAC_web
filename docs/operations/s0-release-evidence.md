# S0 integration, rollback and Release Evidence Bundle

> **Scope: CERTIFICATION_REFERENCE.** This document preserves the S0 production-shaped Kubernetes rollout/rollback proof. It does **not** define the canonical Phase 1 deployment. Phase 1 deployment remains Linux Server + Docker Compose under `deploy/platform/phase1/`; Kind/Kubernetes evidence here is retained only as rollout/certification reference for a later scale/HA stage.

## Purpose

Ticket 08 is the final S0 gate. It does not add a business domain. It integrates the foundation delivered by Tickets 01–07, executes a production-shaped rollout and rollback, and publishes a reproducible evidence bundle that a reviewer can validate without reconstructing the release from console output.

S0 is complete only when the generated in-toto-style statement has `predicate.status: passed`, all fourteen acceptance criteria are satisfied, all seven immutable images are represented, and the formal GitHub Actions run is successful.

## Reuse-first selection

The implementation evaluated maintained upstream projects before adding code:

| Candidate | Strengths | Costs or mismatch | Decision |
|---|---|---|---|
| `in-toto/attestation v1.2.0` | Standard Statement/Predicate model, active maintenance, interoperable evidence vocabulary | A full in-toto layout and verification service would duplicate GitHub Attestations and Cosign | Reuse the Statement v1 structure and predicate separation without adding a runtime dependency |
| `slsa-framework/slsa-verifier v2.7.1` | Apache-2.0, mature SLSA provenance verification | Existing BuildKit provenance, GitHub build attestations and Cosign verification already cover the release images | Do not add a second overlapping verifier to the default path |
| `oras-project/oras v1.3.3` | Apache-2.0, publishes arbitrary bundles as OCI artifacts | Adds registry artifact lifecycle, permissions and retention policy beyond the S0 acceptance requirement | Keep the evidence bundle as a GitHub Actions artifact; ORAS remains a later distribution option |
| `kubernetes-sigs/cli-utils v0.37.2` | Rich Kubernetes apply and status libraries | Too much integration surface for one Deployment rollout proof | Use native `kubectl rollout` operations |
| `kubernetes-sigs/kind v0.32.0` | Apache-2.0, official disposable Kubernetes clusters, deterministic CI use | Not a substitute for a cloud-specific staging environment | Reuse Kind for the blocking Kubernetes mechanics proof |
| `helm/kind-action v1.14.0` | Maintained GitHub Action that installs and starts Kind | GitHub Actions-specific | Pin commit `ef37e7f390d99f746eb8b610417061a60e82a6cc` in the formal release workflow |

The project-specific code is limited to evidence indexing, HVAC S0 acceptance assertions and rollout observations. It does not implement a generic attestation platform, Kubernetes controller or artifact registry.

## Evidence model

The checked-in architecture decision trace is:

```text
deploy/s0/release-evidence/acceptance-matrix.json
```

The generated bundle is:

```text
out/s0-release-evidence/release-evidence.intoto.json
```

It uses:

```json
{
  "_type": "https://in-toto.io/Statement/v1",
  "predicateType": "https://hvac.local/attestations/s0-release-evidence/v1"
}
```

The five production release images are the in-toto subjects. Test fixtures such as `oidc-test-provider` are excluded from the release subject set. The repository commit, ref and workflow run are recorded in the predicate source metadata. `SHA256SUMS` covers every retained evidence file in the bundle directory.

## Evidence inputs

The bundle indexes rather than duplicates large upstream artifacts. It records hashes, paths and workflow metadata for:

- contract generation, event compatibility and ownership locks;
- current-schema PostgreSQL transaction and restore evidence with no previous-writer runtime path;
- browser login, current Principal, logout and authorized Audit query;
- Go-owned route revision promotion and stale-revision rejection;
- two-Organization isolation, credential absence, Outbox recovery and Audit deduplication;
- W3C trace span coverage and observability-outage behavior;
- NetworkPolicy connectivity proof;
- real Kind rolling update and `kubectl rollout undo` observations;
- staging render receipt and migration file state;
- five Trivy embedded-secret JSON reports for production release images;
- five BuildKit SBOM/provenance build records for production release images;
- five Cosign verification results and GitHub build-attestation verification results;
- CodeQL, Gitleaks, dependency, vulnerability and production-license job status;
- delivery, observability, security and recovery runbooks;
- release approval state.

## Commands

Static assets and the architecture decision trace:

```bash
npm run release:evidence-assets
```

Deterministic availability model:

```bash
npm run audit:s0-rollout -- --report=out/s0-release-evidence/rollout-model-report.json
```

Real Kubernetes rollout proof, after Kind and `kubectl` are available:

```bash
npm run audit:s0-kind-rollout
```

Build the final bundle from downloaded image and security artifacts:

```bash
npm run release:evidence:build
```

For an offline audit replay, supply a previously reviewed workflow summary while preserving the source run metadata:

```bash
npm run release:evidence:build -- \
  --workflow-report=out/s0-release-replay/workflow-jobs.json \
  --source-run-id=<run-id> \
  --source-repository=<owner/repository> \
  --source-sha=<commit> \
  --source-ref=<refs/heads/main> \
  --source-workflow=<workflow-name>
```

The normal staging renderer remains strict. The bundle builder invokes its explicit evidence-only mode so the exact redaction marker can be rendered into temporary manifests; the receipt records that mode, and binding values or rendered manifests are deleted rather than included in the bundle.

The formal path is the `S0 Reproducible Delivery and Supply Chain` workflow. A release dispatch runs all clean-environment gates, builds and verifies seven images, performs the Kind rollout proof, verifies the image evidence again, renders staging with non-secret evidence bindings, and uploads `s0-release-evidence-bundle`.

## Staging rolling update and rollback proof

The Kind harness reads the checked-in `platform-gateway` Deployment and asserts that the source strategy remains:

- replicas: 2;
- `RollingUpdate`;
- `maxUnavailable: 0`;
- `maxSurge: 1`.

It creates a restricted namespace and a minimal non-domain workload with the same strategy. The harness then:

1. deploys revision `previous-compatible`;
2. waits for two available replicas;
3. updates the Pod template to revision `current`;
4. samples the Deployment and ReplicaSets until the rollout completes;
5. fails if available replicas ever fall below one or total replicas exceed the configured surge;
6. executes `kubectl rollout undo`;
7. samples the rollback until the previous revision is restored;
8. records rollout history, generations, revisions and every availability observation.

The harness proves Kubernetes rollout mechanics and the checked-in strategy. It does not claim to validate a particular cloud load balancer, managed identity provider or storage class.

## Compatibility and migration state

The rollback window is expand-contract:

- OpenAPI generated clients must match the checked-in contract;
- Protobuf and ownership lock files must remain compatible;
- previous-style Gateway, Outbox and Audit writes omit the new `traceparent` fields and rely on additive defaults;
- the migration image and SQL files are hashed in the bundle;
- application runtime identities do not receive DDL ownership;
- a rollback never reverses an expand migration during the compatibility window.

The staging render uses the seven verified image digests and synthetic evidence-only secret values. Binding values are not copied into the bundle; only the renderer receipt and rendered-manifest hashes are retained.

## Zero invariants

A passing bundle declares all of the following numeric values as zero:

- `crossTenantSuccesses`;
- `credentialLeakFindings`;
- `duplicateAuditEffects`;
- `lostCommittedSessionEvents`.

These values are derived from blocking assertions in the browser and failure-injection gates, not from an operator-entered approval field.

## Reusable S0 surface for S1–S7

Future slices must consume, rather than replace, these S0 conventions:

- public HTTP authority: `contracts/http/platform-gateway.openapi.yaml`;
- event authority: `contracts/events/session-audit.v1.proto` and its compatibility lock;
- identity and delegation: `libs/identitycontext`;
- route and data ownership: `libs/ownershipregistry` plus checked-in registries;
- transactional Session, Audit Intent and Outbox: `libs/sessionstore`;
- event metadata and hashing: `libs/sessionevent`;
- tracing and bounded telemetry export: `libs/observability`;
- Gateway-only browser ingress and RFC 9457-style Problem Details;
- signed digest-only staging images and separate migration identity.

A future slice may add Organization, Device, Telemetry, Command, Schedule or AI behavior only through a new specification. Ticket 08 does not introduce those implementations.

## Legacy retirement status

Legacy is retained only as historical migration evidence. It is not an active route owner, fallback, staging workload, local topology dependency or release image. The protected release gates verify that all active public routes remain owned by Go services.

## Known limitations

These limitations are explicit and are not deferred security, tenant, audit or rollback failures:

1. Kind validates Kubernetes Deployment, ReplicaSet and rollback mechanics; cloud-specific ingress, managed workload identity and persistent-volume behavior require the target environment's operational qualification.
2. The evidence staging render uses synthetic secret values and file paths. Production secret material remains outside source control and must be supplied by the approved release environment.
3. The local OIDC provider, Redpanda and telemetry collector are deterministic test implementations of production protocols, not production service selections.
4. GitHub Actions artifact retention is finite. Long-term regulated archival, WORM retention and OCI publication of the complete evidence bundle are later infrastructure concerns; image signatures and attestations remain registry-backed.
5. Complete legal-retention Audit archival is outside S0, as declared by the source architecture. The tested S0 Audit Ledger remains append-only and deduplicated for the delivered security events.

## Approval and S0 completion

The generated statement contains an `approval` object. Automation may set `eligible: true` only when every blocking criterion passes. Human approval is represented by the successful protected workflow and repository review history; the script does not fabricate a person or approval timestamp.

After the final main-branch workflow succeeds, Ticket 08 may be marked `completed`, the evidence run and artifact identifiers must be recorded in `.ai-bridge/agent-status.md`, and the final review may state:

> S0 is complete. S1 is ready to enter implementation specification. This declaration does not authorize S1 implementation without its own accepted specification.
