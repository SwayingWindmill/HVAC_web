# ADR 0023 — Cube is the semantic layer for energy reads

Status: accepted

Date: 2026-10-16

## Context

ADR 0008 put Cube Core between the analytics facts and `telemetry-query-service`, but in practice Cube serves one product: the Energy Series (electricity and cooling by Site, device and time bucket) behind 能耗分析, 总览 and the Operations Agent's energy Tool. Everything else that reads energy aggregates its own way:

- report-service sums `analytics.energy_interval_facts_visible` with its own SQL for the monthly report;
- the Web computes cost from hourly electricity and the Tariff, and COP from electricity and cooling, in the browser;
- the Cube model exposes internal identities (`point_id`, `sensor_id`) and has no consumer-facing views, so nothing it offers is usable by a model choosing what to query.

The same quantity therefore has more than one definition, and the Operations Agent, which should be able to ask for any governed energy measure, has only fixed Tools.

## Decision

Every read that aggregates energy for a person or for the Operations Agent goes through `telemetry-query-service` and Cube. Cube is where an energy measure is defined once.

- Cube exposes **views** as its only public surface, with Chinese titles and descriptions written for operators and for the Agent; cubes are internal (`public: false`). Internal identities stay out of views except the keys a consumer filters by (Site, equipment).
- Measures that are pure functions of energy facts live in Cube: electricity and cooling energy by scope and equipment, and COP (cooling ÷ electricity).
- report-service reads the Site's hourly electricity and cooling through `telemetry-query-service` as its own Workload Principal (ADR 0017), not through ClickHouse.
- The Operations Agent gets a governed semantic query Tool over the views (members whitelisted, Site forced by the Gateway grant) once the first assistant release ships; fixed Tools stay for questions that need more than one query.
- Readings history (raw device series) stays on the query service's history engine over `telemetry_history.observations`: high-cardinality raw series are not semantic measures.
- Owners that compute rather than present (metric worker, savings verification, settlement, forecast, fault diagnosis) keep reading their own inputs; they own those calculations.

## Considered Options

- **Remove Cube and let the query service read ClickHouse.** Rejected: one fewer container, but the Agent's open questions would need a semantic layer we would have to build ourselves, and definitions would keep spreading across services.
- **Cube Cloud's AI API / MCP server.** Not adopted: the platform runs on one server with Cube Core; the Agent reaches Cube only through the query service so the Gateway's per-operator grant and the data-quality metadata stay on every read.

## Consequences

- Cost needs the Tariff, which lives in PostgreSQL, so it cannot be a Cube measure over ClickHouse facts alone. Where cost is defined (browser, the `energy_cost` Metric or Settlement) is decided in its own ticket rather than here.
- Cube still runs with the in-memory cache and without pre-aggregations or Cube Store. At one Site ClickHouse answers directly within the request budget; add Cube Store and pre-aggregations when the query service's p95 for Energy Series exceeds its budget or more Sites make the facts large.
- A new energy measure is added to the Cube model and a view, then read through the query service; a service that writes its own SQL over the energy facts is a defect.
