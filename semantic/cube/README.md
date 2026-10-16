# Cube Core semantic model

This directory contains the internal Cube Core semantic layer for HVAC
analytics. Cube is not a public application endpoint: only
`telemetry-query-service` may call it.

## Pipeline position

```text
telemetry_history.observations
    -> analytics-read-model-projector
        -> analytics.energy_interval_facts_visible
            -> Cube Core
                -> telemetry-query-service
```

Cube never reads raw EAV-style telemetry observations. Cumulative-meter reset
handling, conversion to additive interval energy, canonical units, quality,
dataset revision and data watermark are owned by the upstream Analytics Read
Model Projector.

## Energy model: views over internal cubes

Views are Cube's only public surface (ADR 0023). `telemetry-query-service` queries
them, and the Operations Agent will choose among their members; every member has
a Chinese title and description for both.

| View | Cube (internal) | Rows | Members beyond the shared ones |
|---|---|---|---|
| `site_energy` | `site_energy_facts` | SITE facts: the Site's total | `electricity_valid_kwh`, `cooling_valid_kwh`, `cop` (cooling ÷ electricity in the same bucket) |
| `equipment_energy` | `equipment_energy_facts` | BRANCH facts: equipment sub-meters, already inside the total | `equipment_id` (the metered equipment) |

Both cubes extend `energy_facts` (`cubes/energy_facts.yml`) over
`analytics.energy_interval_facts_visible`, and each fixes its accounting scope
in its own SQL, so a view cannot mix the total with its sub-meters. Shared
members: `energy_valid_kwh`, `energy_valid_and_suspect_kwh`, the three quality
counts, `max_data_watermark`, `max_dataset_revision`, `tenant_id`, `site_id`,
`energy_type`, `quality`, `period_start`, `period_end`. Meter, point and sensor
identities are not in the model.

The Query Service groups by `period_end` at hour/day/month granularity, which
attributes the complete interval to the bucket containing its end.

Cube pre-aggregations are intentionally disabled. ClickHouse facts remain the
durable authority, while the visible view applies latest-revision-wins before
Cube provides metric definitions, timezone-aware grouping and access policy
enforcement.

## Security

The Query Service creates a 30-second HS256 JWT containing:

- `tenantId`;
- `siteId` and `siteIds`;
- `principalId`;
- `policyRevision`;
- group `analytics_reader`.

`cube.py` maps the JWT security context to Cube groups. The views
(`views/energy.yml`) carry deny-by-default access policies: only
`analytics_reader` receives member access, with row filters bound to
`securityContext.tenantId` and `securityContext.siteId`. The cubes are
`public: false`, so a query naming a cube member is rejected.

The ClickHouse account configured for Cube is `cube_analytics_reader`, which has
SELECT access to `analytics.energy_interval_facts_visible` and no write access.

## Local scaffold

First start the S2 ClickHouse fixture, which initializes the raw history and
analytics schemas:

```bash
docker compose -f infra/telemetry/compose.yaml up -d clickhouse
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
