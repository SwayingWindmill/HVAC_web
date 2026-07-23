# S2 observability, capacity and rollout source review

Date: 2026-07-23

Issue: #52

## Scope

This note records the primary-source facts used to define the S2 release envelope and delivery gates. Numeric S2 targets are project decisions, not vendor promises.

## Centrifugo observability and bounded transport

Centrifugo exposes Prometheus metrics when `prometheus.enabled` is set and documents metrics for active connections, subscriptions, broker traffic, recover outcomes, recovered publication counts, proxy duration and server disconnects. It can export OpenTelemetry traces for HTTP and gRPC server API requests when explicitly enabled.

Sources:

- https://centrifugal.dev/docs/server/observability
- https://centrifugal.dev/docs/server/monitoring

The release gate therefore consumes Centrifugo's native transport metrics, but keeps business metrics and publication-to-Snapshot traces in `telemetry-runtime-service`. The locked OSS image still requires raw metric-name verification because the previous POC found documentation/version drift.

## Queue and recovery bounds

Centrifugo documents a per-client output queue in bytes (`client.queue_max_size`, default 1 MiB) and a bounded client history publication limit. History recovery is enabled only when both `history_size` and `history_ttl` are positive. History is explicitly an ephemeral cache, can be truncated or lost, and is not authoritative. A valid recovery requires both a matching epoch and a fully retained offset gap; otherwise the application must load authoritative state.

Sources:

- https://centrifugal.dev/docs/server/configuration
- https://centrifugal.dev/docs/server/history_and_recovery
- https://centrifugal.dev/docs/server/cache_recovery

The S2 release contract therefore sets a smaller initial queue budget, bounds publication size and recovery history, and treats every unsuccessful transport recovery or Business Revision gap as Snapshot fallback.

## Redis engine boundary

Centrifugo's memory engine is single-node and loses history on restart. The Redis engine stores history outside the Centrifugo process, so history survives a Centrifugo restart subject to Redis persistence and availability. This changes transport continuity only; it does not make history durable business state.

Sources:

- https://centrifugal.dev/docs/server/engines
- https://centrifugal.dev/docs/getting-started/design

The S2 release topology therefore requires a dedicated Redis-backed broker and N+1 transport proof, while PostgreSQL `telemetry_runtime` remains authoritative.

## Metrics cardinality and sensitive dimensions

Prometheus recommends bounded label cardinality, discourages dimensions with unbounded values such as user IDs, and recommends names with base-unit suffixes. It also recommends exporting event timestamps instead of continuously updated "time since" gauges.

Sources:

- https://prometheus.io/docs/practices/instrumentation/
- https://prometheus.io/docs/practices/naming/
- https://prometheus.io/docs/practices/rules/
- https://prometheus.io/docs/practices/alerting/

S2 metrics therefore use only bounded labels such as operation, outcome, reason family, phase and cohort. Organization, Site, Device, subscription, cursor, channel, key and revision are not Prometheus labels.

## Trace propagation and Baggage safety

OpenTelemetry defines HTTP and messaging semantic conventions and separates context propagation from signal attributes. OpenTelemetry also warns that Baggage is automatically propagated in headers, can reach unintended resources and has no built-in integrity protection.

Sources:

- https://opentelemetry.io/docs/specs/semconv/http/
- https://opentelemetry.io/docs/specs/semconv/messaging/
- https://opentelemetry.io/docs/concepts/signals/traces/
- https://opentelemetry.io/docs/concepts/signals/baggage/

S2 propagates W3C Trace Context and explicit signed delegation. It does not place tenant, Device, subscription, cursor or telemetry values in Baggage. Correlation identifiers beyond trace/request IDs are recorded only as bounded HMAC-derived references in logs and spans, with raw platform IDs retained only in the access-controlled audit ledger where required.

## Project decisions derived from the sources

The following are S2 project decisions rather than upstream defaults:

- a versioned Release Envelope rather than an unbounded scale claim;
- a 256 KiB initial client output queue;
- 256 publications and 180 seconds of transport history;
- a 120-second maximum Recovery Cursor lifetime;
- Snapshot-authoritative fallback on any recovery uncertainty;
- no request-level fallback to Legacy or Mock;
- side-effect-free shadow comparison;
- zero-tolerance security invariants;
- promotion by measured SLO/error-budget evidence and explicit rollback gates.
