# Platform component POCs

These experiments evaluate reusable platform components without changing an accepted slice specification or production route. They are disposable integration spikes, not production dependencies.

Executed evidence and provisional decisions: `results.md`.

## Scope

| Component | Evaluated boundary | Explicitly not delegated |
|---|---|---|
| Envoy Gateway | Kubernetes Gateway API, ingress TLS, route matching and traffic policy | BFF Session, Principal Context, Problem Details, business route ownership or authorization |
| Debezium | One-way PostgreSQL row-change capture from a Legacy fixture | Domain transformation authority, reverse synchronization, business dual writes or cutover ownership |
| Redpanda Connect | Filtering and transforming non-authoritative CDC events | Organization/Site authorization, Registry truth, TelemetryPoint semantics or Command handling |
| Centrifugo | Authenticated realtime connection, publication, history/recovery transport and metrics | REST Snapshot authority, cursor semantics, resource authorization, command submission or durable business state |
| River | Transactional, unique, owner-local PostgreSQL jobs | Cross-service workflow authority, Command retries, device side effects or a shared platform queue |

## Locked versions

`versions.lock.json` is the authority for evaluated upstream versions, licenses and container digests. The POC deliberately tests Debezium and Redpanda Connect against the Redpanda broker version already used by S0 rather than introducing a broker upgrade as a hidden prerequisite.

## Acceptance gates

### Envoy Gateway

- A real Kind cluster installs the locked Envoy Gateway release.
- A public `HTTPRoute` reaches only the Go-edge fixture.
- A private Legacy fixture is not routable through an unregistered path.
- Route changes and rollback use Gateway API resources; no business identity headers are synthesized by Envoy.
- Result: adopt only as the Kubernetes traffic layer under `platform-gateway`.

### Debezium

- Initial snapshot and insert/update/delete events from `public.legacy_registry` reach the existing Kafka-compatible broker.
- Restart resumes from offsets without creating a reverse write path.
- Only the selected table is captured.
- Result: adopt for Legacy snapshot-plus-single-direction CDC only after operational and schema-history review.

### Redpanda Connect

- It consumes the Debezium topic, applies a bounded Bloblang mapping and writes to the evidence sink.
- The transformed record preserves source operation, source table and source timestamp.
- It does not invent platform Organization, Site, Equipment or Device IDs.
- Its license and per-connector availability are reviewed before production adoption.
- Result: use only for non-authoritative integration pipelines where a project-owned service adds no safety value.

### Centrifugo

- An HMAC-authenticated client connects, subscribes, receives a publication and can recover retained history.
- The server exposes Prometheus metrics.
- The POC keeps channel authorization and tokens outside the browser fixture source.
- Result: adopt only if S2 can layer platform-owned Snapshot/Cursor/Scope semantics without duplicating most of the server.

### River

- A domain row and a job are inserted in one PostgreSQL transaction.
- Rollback leaves neither row nor job.
- Duplicate unique job insertion produces one runnable job.
- A worker restart completes the committed job without a duplicate business effect.
- Result: adopt only inside a service-owned database for background work with no external side effect ambiguity.

## Commands

```bash
npm run pocs:components:check
npm run pocs:components:docker
npm run pocs:components:envoy
npm run pocs:components
```

`pocs:components:docker` requires Docker. `pocs:components:envoy` requires `kind` and `kubectl`; CI installs them at locked versions. All commands write machine-readable evidence below `out/platform-component-pocs/` and clean their temporary resources unless `--cleanup=false` is supplied.

## Decision rule

A successful POC is not an adoption decision. Production use requires a slice-specific ADR that records the exact retained boundary, security controls, ownership, failure modes, upgrade path, license review and rollback. A component is rejected when integration code recreates most of its behavior, when it becomes a second business authority, or when its operational footprint exceeds the project-specific code it replaces.
