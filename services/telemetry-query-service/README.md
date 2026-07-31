# Telemetry Query Service

`telemetry-query-service` is the platform-owned history and BI query boundary.
It is separate from `telemetry-runtime-service`, the raw History Projector and
the Analytics Read Model Projector so analytical queries cannot consume the
latency and availability budget of current-state telemetry ingestion.

## Pipeline position

```text
Telemetry Runtime
    -> telemetry-history-projector
        -> telemetry_history.observations
            -> analytics-read-model-projector
                -> analytics.energy_interval_facts
                    -> Cube Core
                        -> telemetry-query-service
                            -> Platform Gateway
                            -> Operations Agent
```

The Query Service reads no database directly. It calls Cube Core through an
internal HTTP adapter and returns product-level DTOs.

## Energy Series contract

The first vertical slice exposes:

```text
POST /internal/v1/analytics/energy-series
```

The service:

- accepts only the fixed Energy Series product contract;
- rejects arbitrary Cube members, SQL, dimensions and measures;
- validates UUIDv7 Organization/Site scope, IANA timezone, granularity, quality
  policy and a maximum 366-day query range;
- requires an allowlisted Platform Gateway or Operations Agent mTLS SPIFFE identity;
- verifies a Gateway-issued short-lived delegation grant whose trusted issuer and actual executing workload are both explicit and whose Scope is bound to the complete query digest;
- generates a separate 30-second Cube JWT with Organization/Site security
  context;
- translates the product query to a fixed Cube `/load` query;
- returns requested/actual granularity, data and aggregate watermarks, dataset
  revision, partial state and quality counts.

It does not ingest telemetry, own Presence/Freshness, write ClickHouse, construct
energy intervals, execute commands or expose Cube directly to browsers.

## Authoritative metadata

Cube queries `analytics.energy_interval_facts`, which contains a watermark and
numeric revision for every additive interval fact.

The adapter executes two fixed Cube queries under one 16-second budget:

- a time-series query for the requested range;
- a global metadata query with the same Organization, Site and energy-type
  filters but no time dimension.

The product contract uses inclusive `from` and exclusive `to`. Cube date ranges
are inclusive, so the adapter sends `to - 1ms`, matching the ClickHouse
`DateTime64(3)` fact precision. Requests shorter than 1ms are rejected.

The response metadata is derived as follows:

- `dataWatermark`: maximum fact watermark from the global metadata query;
- `aggregateWatermark`: the same value for the current direct-fact aggregation;
- `datasetRevision`: `<QUERY_DATASET_REVISION>:<maximum fact revision>`;
- `partial`: true when the global watermark is before the requested exclusive
  end or any requested local hour/day/month bucket has no returned fact row;
- a site with no facts uses an `:empty` revision suffix and remains partial;
- a covered watermark alone never hides gaps in the requested bucket sequence.

Cube cache refresh timestamps are never presented as data watermarks.

## Security boundary

The browser never calls this service or Cube directly.

```text
Browser -> Platform Gateway -> Telemetry Query Service -> Cube Core
                       \\-> Operations Agent -> Telemetry Query Service -> Cube Core
```

The service verifies:

1. the caller's mTLS certificate against the Gateway/Operations Agent presenter allowlist;
2. a short-lived Gateway-signed Delegation Grant;
3. that the grant issuer is Gateway and `executingService` equals the actual mTLS presenter;
4. the fixed Energy Series action;
5. the SHA-256 digest of the complete normalized product query.

It rejects caller-supplied identity or scope headers. Cube receives a second
short-lived token and applies Organization/Site row-level access policy.

## Required environment

| Variable | Purpose |
|---|---|
| `QUERY_TLS_CERT` | Service TLS certificate |
| `QUERY_TLS_KEY` | Service TLS private key |
| `QUERY_CLIENT_CA` | CA used to verify calling workloads |
| `QUERY_GATEWAY_DELEGATION_CERT` | Public certificate used to verify Gateway delegation grants |
| `QUERY_CUBE_ENDPOINT` | Cube origin, for example `http://cube:4000` |
| `QUERY_CUBE_API_SECRET` | Minimum 32-byte HS256 key for short-lived internal Cube JWTs |
| `QUERY_DATASET_REVISION` | Semantic/read-model schema revision prefix, for example `energy-interval:v1` |

Optional variables:

| Variable | Default |
|---|---|
| `QUERY_SERVICE_ADDR` | `127.0.0.1:18447` |
| `QUERY_DIAGNOSTICS_ADDR` | `127.0.0.1:19088` |
| `QUERY_DELEGATION_ISSUER_SPIFFE` | `spiffe://hvac.local/platform-gateway` |
| `QUERY_ALLOWED_WORKLOAD_SPIFFE` | `spiffe://hvac.local/platform-gateway` |
| `QUERY_OPERATIONS_AGENT_SPIFFE` | `spiffe://hvac.local/operations-agent-service` |
| `QUERY_AUDIENCE` | `telemetry-query-service` |
| `QUERY_CUBE_CA` | System trust store when omitted |

## Verification

```bash
npm run test:analytics
npm run test:analytics-gateway
npm run build:telemetry-query
```

The tests cover fixed-member Cube translation, tenant filters, quality-specific
measures, authoritative watermarks/revisions, partial-state calculation, response
budgets, mTLS identity, Delegation Grant scope binding and hostile header
rejection.

## Current limitations

This slice does not yet add:

- a public Gateway route;
- Dashboard/Energy frontend integration;
- export jobs or cold queries;
- tariff, cost, baseline or comparison models;
- water, gas or cooling-energy product contracts.
