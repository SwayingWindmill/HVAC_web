# Cube Core semantic model

This directory contains the internal Cube Core semantic layer for HVAC
analytics. Cube is not a public application endpoint: only
`telemetry-query-service` may call it.

## Pipeline position

```text
telemetry_history.observations
    -> analytics-read-model-projector
        -> analytics.energy_interval_facts
            -> Cube Core
                -> telemetry-query-service
```

Cube never reads raw EAV-style telemetry observations. Cumulative-meter reset
handling, conversion to additive interval energy, canonical units, quality,
dataset revision and data watermark are owned by the upstream Analytics Read
Model Projector.

## Energy Usage model

`energy_usage` maps to:

```text
analytics.energy_interval_facts
```

Important members include:

| Member | Meaning |
|---|---|
| `organization_id` | Immutable tenant boundary |
| `site_id` | Authorized Site boundary |
| `energy_type` | Canonical energy type; first slice is `electricity` |
| `period_start` | Inclusive source interval start |
| `period_end` | Exclusive source interval end and aggregation bucket dimension |
| `energy_kwh` | Additive normalized interval energy |
| `quality` | `VALID`, `SUSPECT` or `INVALID` |
| `observation_count` | Number of source observations contributing to the interval |
| `data_watermark` | Latest source sample covered by the fact |
| `dataset_revision` | Numeric source revision for the fact |

The Query Service groups by `period_end` at hour/day/month granularity. This
attributes the complete interval to the bucket containing its end. Cross-boundary
prorating is not part of the first slice.

The semantic model exposes separate energy measures for:

- `VALID` facts only;
- `VALID` and `SUSPECT` facts;
- quality counts;
- maximum data watermark;
- maximum dataset revision.

Cube pre-aggregations are intentionally disabled. ClickHouse facts remain the
durable authority, and Cube provides metric definitions, timezone-aware grouping
and access policy enforcement.

## Security

The Query Service creates a 30-second HS256 JWT containing:

- `organizationId`;
- `siteId` and `siteIds`;
- `principalId`;
- `policyRevision`;
- group `analytics_reader`.

`cube.py` maps the JWT security context to Cube groups. `energy_usage.yml`
applies deny-by-default access policies: only `analytics_reader` receives member
access, with row filters bound to `securityContext.organizationId` and
`securityContext.siteId`.

The ClickHouse account configured for Cube is `cube_analytics_reader`, which has
SELECT access to `analytics.energy_interval_facts` and no write access.

## Local scaffold

First start the S2 ClickHouse fixture, which initializes the raw history and
analytics schemas:

```bash
docker compose -f infra/s2-telemetry/compose.yaml up -d clickhouse
```

Copy `local.env.example` to a local untracked environment file, replace the Cube
API secret, and run:

```bash
docker compose --env-file <local-env-file> -f semantic/cube/compose.yaml up
```

The example connects from the Cube container to the loopback-published
ClickHouse HTTP port through `host.docker.internal`. Cube binds port 4000 to
loopback only.

The local ClickHouse reader has no password because the fixture is local-only.
The local single-instance Cube process uses the in-memory queue/cache driver and
has no pre-aggregations. Production must supply an authenticated read-only
ClickHouse identity, TLS and Cube Store for distributed queue/cache operation.

## Verification

```bash
npm run analytics:history:integration
docker compose --env-file semantic/cube/local.env.example -f semantic/cube/compose.yaml config
```
