# Forecast authoritative input source review

Issue: #344

Parent specification: #331

This record captures the implementation-time source review for the Forecast-owned input preparation path. Existing HVAC_web code was not treated as authoritative; the implementation was compared directly with the pinned upstream source before the preparation boundary was changed.

## Pinned references

| Project | Pinned revision | Official source reviewed | Decision |
| --- | --- | --- | --- |
| ThingsBoard CE | `v4.3.1.1` / `c2a52e46c44e308ddee430e7266b8e10eddde9c4` | `application/src/main/java/org/thingsboard/server/controller/TelemetryController.java`, especially the server-side historical `getTimeseries` path with explicit `startTs` / `endTs` | **ADOPT** owner-side historical reads and explicit bounded time windows. **ADAPT** the entity/key query boundary to Forecast's released deployment/model/feature/metric lineage. **REJECT** caller-supplied historical samples as Forecast truth. |
| OpenEMS | `2026.7.0` / `2e2792d59fc5ba3b99ce3cf98d15081c0a74895e` | `io.openems.edge.predictor.profileclusteringmodel/.../PredictorProfileClusteringModelImpl.java`; `.../training/TrainingRunnable.java`; predictor contexts backed by the injected `Timedata` owner | **ADOPT** typed historical-provider ownership, server-created prediction/training context and explicit failure when usable model/input state is unavailable. **ADAPT** this into HVAC_web's PostgreSQL lineage plus immutable Forecast input snapshot rather than OpenEMS component/OSGi infrastructure. **REJECT** copying the OSGi/component framework or allowing an external simulator/browser to construct predictor samples. |
| MyEMS | `v6.7.0` / `be6e6ce8ddeac57afb04bddb9621501fb555cab0` | `myems-api/reports/metertrend.py`; `myems-api/core/utilities.py` | **ADOPT** server-side historical database reads, UTC normalization and half-open interval treatment (`from <= t < to`). **ADAPT** report-oriented history assembly into deterministic Metric fact normalization and immutable Forecast provenance. **REJECT** report-cache SHA-256 keys as Forecast snapshot identity; Forecast checksum covers the frozen authoritative input and lineage instead. |

## HVAC_web implementation decision

Forecast preparation is owned by `forecast-service`. A caller may identify only the requested tenant/site subject and Forecast target. The service resolves the active Forecast deployment and its immutable model/feature/topology/dataset lineage from Registry, reads the dataset's authoritative Metric Version history from `analytics.metric_result_facts`, normalizes revisions deterministically, and freezes the facts into `forecast_input_snapshots` with a SHA-256 checksum.

The same PostgreSQL transaction creates the domain `forecast_jobs` row and the `FORECAST_RUN` scheduler job. The scheduler payload contains only server-created Forecast job/result identities; it never contains observations or an input snapshot body. The worker reloads the referenced immutable snapshot, recomputes its checksum, derives its execution observations from the frozen feature values and then runs the existing Forecast engine.

Required target history is fail-closed. The current baseline Forecast engine needs at least four target facts to produce a non-fallback trend, so preparation rejects fewer than four authoritative target facts rather than entering the engine's fallback path. Missing authoritative data is therefore an explicit preparation-unavailable state, not a browser fixture, simulator value or compatibility fallback.

`weather_issue_time` remains `NULL` unless a released Forecast feature contract references an authoritative weather input. The existing database field is nullable and #344 does not introduce a synthetic weather source merely to populate provenance.

## Integration evidence

`npm run analytics:history:integration` now includes a focused tracer using real Registry PostgreSQL RLS and real ClickHouse tables/users. It seeds released Forecast lineage plus authoritative Metric result facts, then proves:

1. Forecast preparation can read the released lineage and authoritative Metric history.
2. An immutable input snapshot/checksum and Forecast/scheduler jobs are server-created.
3. The worker scheduler payload carries identity only and reloads the frozen snapshot.
4. Forecast publication reaches `analytics.forecast_series` through the existing sink/publication contract.
5. The existing latest-Forecast read model returns the result with the exact server-created `input_snapshot_id` and `forecast_job_id`.

No Historical Replay write into Forecast tables is introduced.
