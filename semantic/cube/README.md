# Cube Core semantic model

This directory contains the internal Cube Core scaffold for HVAC analytics.
Cube is not a public application endpoint: only `telemetry-query-service` may
call it.

## First semantic model

`energy_usage` maps to the planned ClickHouse table:

```text
analytics.energy_interval_facts
```

The table must be a normalized, additive interval fact model with at least:

| Column | Meaning |
|---|---|
| `organization_id` | Immutable tenant boundary |
| `site_id` | Authorized Site boundary |
| `energy_type` | Canonical energy type; first slice is `electricity` |
| `period_start` | Inclusive interval start |
| `period_end` | Exclusive interval end |
| `quality` | `VALID`, `SUSPECT` or `INVALID` |
| `energy_kwh` | Additive normalized interval energy |
| `observation_count` | Number of contributing observations |

The Cube model must not point directly at raw EAV-style telemetry observations.
Meter reset detection, cumulative-to-interval conversion, canonical units,
late-data correction and quality classification belong upstream in the
ClickHouse read-model pipeline.

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

The ClickHouse account configured for Cube must be read-only. Cube
pre-aggregations are intentionally disabled in this initial slice; the upstream
read-model pipeline remains authoritative for durable hour/day/month facts.

## Local scaffold

Copy `local.env.example` to a local untracked environment file, replace the
placeholders, and run:

```text
docker compose --env-file <local-env-file> -f semantic/cube/compose.yaml up
```

The compose file pins Cube Core and binds port 4000 to loopback only. It will not
be functional until the planned ClickHouse table and read-only account exist.
