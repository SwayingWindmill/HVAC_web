# ADR 0008: Separate BI history queries from Telemetry Runtime

## Status

Accepted for the first BI history-query vertical slice.

## Context

`telemetry-runtime-service` is the current-state authority for accepted telemetry,
quality, freshness, presence and snapshot revision. BI workloads have a different
resource and failure profile: they can scan long time ranges, aggregate many
records, and evolve their metric definitions independently from ingestion.

Putting BI endpoints, Cube queries and exports into Telemetry Runtime would
couple analytical load to the latency and availability budget of real-time
telemetry. It would also mix current-state authority with historical derived
facts and encourage the browser or Gateway to depend directly on Cube members.

## Decision

Introduce an independent `telemetry-query-service` with the following boundary:

```text
Platform Gateway
    -> telemetry-query-service
        -> Cube Core
            -> ClickHouse analytics facts
```

The service owns product-level historical query contracts, query validation,
authorization scope binding, query budgets, the repository-managed Cube semantic
model definition and semantic-engine adaptation. It does not own telemetry
ingestion, current state, ClickHouse writes or historical data facts.

The first product contract is Energy Series:

```text
POST /internal/v1/analytics/energy-series
```

The route accepts Organization, Site, energy type, granularity, IANA timezone,
time range and quality policy. The complete normalized request is bound to a
short-lived Platform Gateway delegation grant by a SHA-256 scope digest.

The service translates the product request to a fixed Cube query. Callers cannot
supply arbitrary Cube measures, dimensions, filters or SQL. Cube receives a
separate 30-second JWT with Organization/Site security context and applies a
second deny-by-default access policy.

A small shared Go module, `libs/analyticsmodel`, owns the cross-process product
DTOs and validation semantics. The Query Service does not import any other
service implementation module.

## Reuse evaluation

The implementation considered three shapes:

- a custom Go-to-ClickHouse query layer, which is simple for a few fixed
  endpoints but would duplicate semantic metrics, time dimensions and access
  policy behavior as Dashboard, Energy and Cost expand;
- a UI-oriented BI server, which adds a second user-facing application and does
  not fit the platform-owned Gateway/BFF contract;
- Cube Core, a headless semantic layer with ClickHouse support, API-based query
  execution and declarative access policies.

Cube Core was selected as the replaceable internal semantic engine. The PoC pins
`cubejs/cube:v1.6.51`, keeps Cube inaccessible to browsers, and retains the Go
Query Service as the platform security and product-contract boundary. Project
specific code is limited to HVAC query semantics, delegation binding and the
Cube adapter.

## Data model

Cube reads the planned ClickHouse read model
`analytics.energy_interval_facts`, not raw telemetry observations. The upstream
history/read-model pipeline remains responsible for:

- cumulative-meter reset and rollback handling;
- conversion to additive interval energy;
- canonical units;
- late and corrected data revisions;
- quality classification;
- data and aggregate watermarks.

The Query Service boundary owns the semantic member definitions, while Cube Core
executes them and may provide optional cache acceleration. The upstream read-model
pipeline remains the authority for historical and aggregate facts stored in
ClickHouse.

## Consequences

Positive:

- BI failure and resource pressure are isolated from current-state telemetry;
- Dashboard and Energy can share versioned metric definitions;
- authorization is enforced at both service and semantic layers;
- Cube can be replaced behind `EnergySeriesEngine` without changing the public
  product contract;
- future export/cold-query work has a dedicated service boundary.

Costs:

- an additional Go service and Cube deployment must be operated;
- Gateway delegation support and a ClickHouse read model are still required;
- distributed tracing, timeout budgets and service health must cover one more
  network hop.

## Deferred work

This ADR does not activate a public route or frontend integration. Follow-up
work must add:

1. `analytics.energy_interval_facts` DDL and revision/watermark ownership;
2. the independent History Relay deployment and ClickHouse writer identity;
3. Gateway authorization and public Energy/Dashboard BFF routes;
4. local and production deployment assets;
5. browser acceptance tests and Mock-data retirement.
