# ADR 0008: Modular BI history pipeline and query boundary

## Status

Accepted and implemented for the first electricity Energy Series vertical slice.

## Context

`telemetry-runtime-service` is the current-state authority for accepted telemetry,
quality, freshness, presence and snapshot revision. Historical persistence,
analytical read-model construction and BI queries have different resource,
failure and release profiles:

- history delivery must survive retries without losing accepted observations;
- cumulative meter readings require domain conversion before they become
  additive energy facts;
- BI queries can scan long ranges and must not consume the latency budget of
  real-time telemetry ingestion;
- semantic metric definitions must remain versioned and reusable across
  Dashboard, Energy and future Cost products.

Putting all of these responsibilities into Telemetry Runtime would mix
current-state authority, asynchronous projection, analytical fact ownership and
user-facing queries in one failure domain.

## Decision

Use one BI history functional domain with separate modules and runtime units:

```text
Telemetry Runtime
    -> PostgreSQL telemetry history outbox
        -> telemetry-history-projector
            -> ClickHouse telemetry_history.observations
                -> analytics-read-model-projector
                    -> ClickHouse analytics.energy_interval_facts
                        -> Cube Core
                            -> telemetry-query-service
                                -> Platform Gateway
                                    -> Dashboard / Energy
```

### Telemetry Runtime

Telemetry Runtime owns telemetry acceptance, validation, ordering, quality,
current state and the transactionally written history outbox. It does not write
ClickHouse directly and does not execute BI queries.

### Telemetry History Projector

`telemetry-history-projector` is an independent process and image, currently
built from the `telemetry-runtime-service` Go module. It claims the PostgreSQL
history outbox and writes immutable accepted/rejected observation evidence to
`telemetry_history.observations`.

It owns the raw historical projection but does not interpret cumulative meters
or write analytical facts.

### Analytics Read Model Projector

`analytics-read-model-projector` is an independent Go module, process and image.
It reads accepted cumulative electricity-meter observations and writes additive
interval facts to `analytics.energy_interval_facts`.

The first supported source contract is deliberately narrow:

```text
telemetry_key = hvac_meter.energy
unit          = kWh
energy_type   = electricity
```

For adjacent readings on the same Organization, Site, Device and telemetry key:

- a non-negative difference becomes interval `energy_kwh`;
- source `SUSPECT` quality propagates to the interval;
- a meter rollback or reset produces a zero-energy `SUSPECT` interval with
  reason `METER_RESET_OR_ROLLBACK`, never a negative energy value;
- a negative cumulative reading produces an `INVALID` zero-energy interval;
- the current observation ID is the fact identity;
- the source offset is the dataset revision;
- the current sampled time is the authoritative data watermark.

The reader uses a fixed ClickHouse window query and an anti-join against the
fact table. The writer inserts JSONEachRow batches with a deterministic
ClickHouse deduplication token. Re-running the projector therefore does not
create a second fact for the same current observation.

Intervals are attributed to the business bucket containing `period_end`.
Prorating an interval across an hour/day/month boundary is deferred until a
product requirement needs that additional complexity.

### Telemetry Query Service

`telemetry-query-service` owns product-level historical query contracts, query
validation, authorization scope binding, query budgets, the repository-managed
Cube semantic model and semantic-engine adaptation.

The first internal contracts are:

```text
POST /internal/v1/analytics/energy-series
POST /internal/v1/telemetry/device-history
```

Each complete normalized request is bound to a short-lived Platform Gateway
delegation grant by a SHA-256 scope digest. The service accepts only fixed
product contracts and never accepts arbitrary Cube members, SQL, database names
or table names. Energy Series uses Cube Core. Device History uses a dedicated
least-privilege ClickHouse reader restricted to `telemetry_history.observations`.

The service translates the product request to a fixed Cube query. Cube receives
a separate 30-second JWT containing Organization, Site, Principal and policy
revision. Cube applies a second deny-by-default Organization/Site access policy.

Energy Series executes two fixed Cube queries under one 16-second budget:

1. a time-series query using the requested inclusive `from` and exclusive `to`;
   because ClickHouse facts use millisecond precision while Cube date ranges are
   inclusive, the adapter sends `to - 1ms`;
2. a metadata query without a time range but with the same Organization, Site
   and energy-type filters.

Responses expose authoritative read-model metadata:

- `dataWatermark` and `aggregateWatermark` come from the metadata query's maximum
  fact watermark, not from Cube cache refresh time or only the requested rows;
- `datasetRevision` combines the configured model revision prefix with the
  maximum numeric fact revision;
- `partial` is true when the global fact watermark does not cover the requested
  exclusive end or any requested local hour/day/month bucket has no returned
  fact row;
- a site with no analytical facts remains explicitly partial, and a covered
  watermark never masks a gap in the requested bucket sequence.

A small shared Go module, `libs/analyticsmodel`, owns the cross-process product
DTOs and validation semantics. The Query Service does not import another
service implementation module.

### Platform Gateway Energy API

The browser-facing product route is:

```text
POST /api/v1/analytics/energy-series
```

The route requires an authenticated BFF Session, matching Origin and CSRF token.
The requested Organization must equal the Session acting Organization. Gateway
requests a dedicated IAM Analytics decision for the exact Site and action,
`analytics.energy-series.read`. IAM requires an active Organization membership
plus a same-Organization, exact Site Binding; an Organization-level Role alone
is not Site ownership proof. Explicit deny takes precedence. After an allow
decision, Gateway signs a short-lived delegation grant whose scope is the
normalized query SHA-256 digest and whose `principalId` is the immutable IAM
Principal ID, then calls Telemetry
Query Service over the existing workload mTLS identity.
Browser cookies, CSRF tokens and caller-supplied business-scope headers are never
forwarded to IAM or Query Service.

The public Device History route is `POST /api/v1/telemetry/device-series:query`.
Browser requests contain only Device ID, keys, UTC range and per-key point limit.
Gateway first requests the exact S2 `telemetry.history.read` Device/key decision,
then builds an internal query from the Session Acting Organization and IAM
Owning Organization/Site facts. A second grant binds the full query, including
the maximum 24-hour range and point limit, before Query Service reads history.

Gateway validates internal responses before returning them, preserves Dataset
Revision, Watermark, Partial and Quality metadata, applies `private, no-store`,
and maps timeout/unavailable/invalid-upstream states to bounded product errors.
Cube, ClickHouse and Telemetry Query Service remain private.

## Data ownership and identities

| Resource | Writer / owner |
|---|---|
| PostgreSQL telemetry history outbox | `telemetry-runtime-service` |
| `telemetry_history.observations` | `telemetry-history-projector` |
| `analytics.energy_interval_facts` | `analytics-read-model-projector` |
| Cube Energy Usage semantic model | `telemetry-query-service` boundary |
| Energy Series product contract | `telemetry-query-service` |
| Public Energy Series business ownership | `telemetry-query-service` |
| Public Energy Series ingress and BFF enforcement | `platform-gateway` |

ClickHouse access is separated by runtime identity:

- `analytics_projector_reader`: reads raw history and fact identities;
- `analytics_projector_writer`: inserts energy facts only;
- `cube_analytics_reader`: reads analytical facts only.

The no-password users in the local ClickHouse initialization are development
fixtures. Production deployment must provision authenticated identities and TLS
outside this repository fixture.

## Reuse evaluation

The implementation considered three shapes:

- a custom Go-to-ClickHouse query layer, which would duplicate semantic metrics,
  time dimensions and access policies as Dashboard, Energy and Cost expand;
- a UI-oriented BI server, which would introduce a second public application and
  bypass the platform Gateway/BFF contract;
- Cube Core, a headless semantic layer with ClickHouse support, API-based query
  execution and declarative access policies.

Cube Core was selected as the replaceable internal semantic engine. The local
scaffold pins `cubejs/cube:v1.6.51` by immutable image digest, keeps Cube
inaccessible to browsers, and
retains the Go Query Service as the platform security and product-contract
boundary. Project-specific code is limited to HVAC interval construction,
delegation binding and the Cube adapter.

## Consequences

Positive:

- raw history delivery, analytical transformation and BI query failures are
  isolated from current-state telemetry;
- every durable dataset has one writer;
- cumulative meter semantics are centralized before Cube aggregation;
- Dashboard and Energy can share versioned metric definitions;
- authorization is enforced at service and semantic layers;
- the semantic engine can be replaced behind `EnergySeriesEngine`.

Costs:

- three runtime units plus Cube must be operated and observed;
- the initial projector scans for unprojected candidates rather than maintaining
  a separate checkpoint table;
- the first model supports only the canonical cumulative HVAC electricity meter;
- distributed tracing and timeout budgets span additional network hops.

## Deferred work

The public Energy Series Gateway route is active. Follow-up work must add:

1. Dashboard/Energy frontend integration and mock-data retirement;
2. tariff, cost, baseline and comparison facts;
3. water, gas and cooling-energy read models;
4. correction/rebuild workflows for late historical amendments;
5. optional durable projector checkpoints when source volume makes anti-join
   discovery insufficient;
6. production ClickHouse identities, TLS and deployment manifests;
7. cross-boundary interval prorating when required by billing semantics.
