# Telemetry Query Service

`telemetry-query-service` is the platform-owned history and BI query boundary.
It is intentionally separate from `telemetry-runtime-service` so analytical
queries cannot consume the latency and availability budget of current-state
telemetry ingestion.

## Responsibilities

The first vertical slice exposes one internal product query:

```text
POST /internal/v1/analytics/energy-series
```

The service:

- accepts only the fixed Energy Series product contract;
- rejects arbitrary Cube members, SQL, dimensions and measures;
- validates UUIDv7 organization/site scope, IANA timezone, granularity, quality
  policy and a maximum 366-day query range;
- requires a trusted Platform Gateway mTLS SPIFFE identity;
- verifies a short-lived delegation grant bound to the complete query digest;
- generates a separate short-lived Cube JWT with Organization/Site security
  context;
- translates the product query to a fixed Cube `/load` query;
- returns explicit granularity, dataset revision, partial and quality metadata;
- reserves data and aggregate watermark fields but leaves them absent until the
  ClickHouse read model exposes authoritative watermarks; responses remain
  `partial=true` during that interval.

It does not ingest telemetry, own Presence/Freshness, write ClickHouse, execute
commands, or expose Cube directly to browsers.

## Dependency direction

```text
Platform Gateway
    -> Telemetry Query Service
        -> Cube Core
            -> ClickHouse analytics facts
```

`telemetry-query-service` depends only on shared contracts and Cube's internal
HTTP API. It does not import another service implementation module.

## Required environment

| Variable | Purpose |
|---|---|
| `QUERY_TLS_CERT` | Service TLS certificate |
| `QUERY_TLS_KEY` | Service TLS private key |
| `QUERY_CLIENT_CA` | CA used to verify calling workloads |
| `QUERY_GATEWAY_DELEGATION_CERT` | Public certificate used to verify Gateway delegation grants |
| `QUERY_CUBE_ENDPOINT` | Cube origin, for example `http://cube:4000` |
| `QUERY_CUBE_API_SECRET` | Shared HS256 key used only for short-lived internal Cube JWTs; minimum 32 bytes |
| `QUERY_DATASET_REVISION` | Explicit revision of the Energy Usage read model |

Optional variables:

| Variable | Default |
|---|---|
| `QUERY_SERVICE_ADDR` | `127.0.0.1:18447` |
| `QUERY_DIAGNOSTICS_ADDR` | `127.0.0.1:19087` |
| `QUERY_ALLOWED_WORKLOAD_SPIFFE` | `spiffe://hvac.local/platform-gateway` |
| `QUERY_AUDIENCE` | `telemetry-query-service` |
| `QUERY_CUBE_CA` | System trust store when omitted |

## Current limitations

This ticket establishes the service and semantic-query boundary only. It does
not yet add:

- a public Gateway route;
- the ClickHouse `analytics.energy_interval_facts` table;
- independent data and aggregate watermark facts;
- Dashboard/Energy frontend integration;
- export jobs, cold queries, baseline or cost models.

Until the ClickHouse read model exists, the Cube scaffold is a contract target
and is not expected to return production data.
