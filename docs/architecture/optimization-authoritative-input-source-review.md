# Optimization authoritative input source review

Issue: #345

Parent specification: #331

Date: 2026-08-29

This record captures the implementation-time source review for the Optimization-owned input preparation path. Existing HVAC_web code was not treated as authoritative; the implementation was checked against pinned upstream source, tests where the upstream project exposes the overlapping behavior, and official project documentation before the preparation boundary was finalized.

## Pinned references

| Project | Pinned revision | Official source/tests/docs reviewed | Decision |
| --- | --- | --- | --- |
| OpenEMS | `df53f1670ed9b1a782c6c215082a375d5dd4b55e` | `io.openems.edge.energy.api/.../simulation/GlobalOptimizationContext.java`, `GocBuilder.java`, `io.openems.edge.energy.api/test/.../GlobalOptimizationContextTest.java`, `io.openems.edge.energy/readme.adoc` | **ADOPT/ADAPT** optimizer-owner construction of one bounded context from authoritative predictions, tariffs, current equipment state and limits. Map that idea to HVAC_web's immutable Registry/Forecast/Metric/Telemetry lineage and `BUILDING -> SEALED` snapshot. **REJECT** OpenEMS component/OSGi infrastructure and missing-prediction defaults such as production falling back to zero; missing or bad HVAC_web input blocks sealing. |
| ThingsBoard CE | `v4.3.1.3` / `105351615126682762caf849619f0ea02df1faf3` | `rule-engine/rule-engine-components/.../rpc/TbSendRPCRequestNode.java`; upstream `TbSendRPCRequestNodeTest.java` identified in the same pinned tree; node annotation links the official RPC action-node documentation | **ADAPT** the strict separation between a computed decision and the device execution/RPC boundary. Optimization publishes a governed Recommendation and never becomes the device-command authority. **REJECT** using Rule Engine/RPC semantics as an Optimization input owner or bypassing the existing Command Governance approval, revalidation, lease/fence and verification path. |
| MyEMS | `51972b1bb807e47c86feca443b53a560d985adcc` | `myems-api/reports/energystoragepowerstationdetailsschedule.py`, repository README/data-flow documentation; no automated test covering this overlapping community schedule/report seam was present in the pinned tree | **ADAPT** the separation between stored schedule/report data and later equipment-control surfaces. **REJECT** direct database schedule rows as an Optimization truth/dispatch seam. The pinned community source does not expose a reusable AI-optimization implementation; the README identifies AI optimization as an optional enterprise capability, so no unavailable implementation is inferred or copied. |

## HVAC_web implementation decision

Optimization preparation is owned by `optimization-service`. A public caller supplies only the requested `siteId`; Platform Gateway authorizes that Site and forwards the minimal request with the authenticated Tenant context. Caller-authored baselines, constraints, snapshot identifiers, forecasts, tariffs or solver parameters are rejected rather than treated as input truth.

For each preparation, Optimization resolves the effective RELEASED HVAC Optimization Policy, current ACTIVE Topology Version, a PERSISTED SITE_LOAD Forecast Snapshot whose frozen Forecast input uses that exact Topology Version and covers the full Optimization horizon, an optional matching PV Forecast Snapshot, one released Site-import Tariff assignment/version that uses the same Topology and covers the complete horizon, and the active immutable Optimization model Deployment Revision.

Dynamic baseline state is read from authoritative owner data rather than the caller. The current baseline freezes the latest Metric facts for daily energy and energy cost plus the latest canonical Telemetry observations for chilled-water supply temperature and zone temperature, including their immutable result/observation, Metric Version, Point revision, source-event, source-partition and source-offset evidence. The reader deliberately does not filter to `GOOD` before selecting the latest fact: a newer non-GOOD fact or observation blocks preparation instead of silently falling back to an older GOOD value.

The released Optimization Policy owns the current solver's explicit comfort/safety limits, input mapping and response-model contract. The same released policy must explicitly carry maintenance constraints and manual locks because the current schema has no separate authoritative maintenance/manual-lock snapshot owner. The current HVAC solver supports only empty maintenance and manual-lock sets; any active value blocks sealing. No empty default is synthesized when the policy omits those fields.

The service first inserts an `optimization_input_snapshots` row as `BUILDING`. Only after all required lineage and current-state evidence have been resolved and validated does it calculate the SHA-256 checksum and transition that exact row to `SEALED`. In the same transaction it creates the Optimization Run and `OPTIMIZATION_RUN` scheduler job. The scheduler payload contains only server-created run and input-snapshot identities.

The worker accepts only that identity payload, reloads the matching `SEALED` snapshot under tenant/site RLS, recomputes the checksum, reconstructs the existing HVAC solver request from the frozen snapshot, and then invokes the existing Recommendation solver/publication path. The resulting Recommendation remains `DRAFT` with no Command Intent. Any later physical action must still pass the existing independent current-state revalidation and Command Governance path; Optimization does not write Virtual Plant state, Command state, MQTT, or the retired ESS DispatchPlan surface.

## Integration evidence

`npm run analytics:history:integration` includes a focused #345 tracer using real Registry PostgreSQL RLS and real ClickHouse owner tables. It proves that:

1. a traceable Forecast result plus released Registry topology/tariff/policy/model lineage can be selected by Optimization;
2. authoritative Metric and Telemetry state is read without caller-authored values;
3. the service creates and seals the immutable Optimization input snapshot and creates an identity-only scheduler job;
4. the worker reloads and checksum-validates that `SEALED` snapshot before solver execution;
5. the existing solver publishes a Recommendation linked to the exact server-created input snapshot; and
6. the Recommendation remains non-executable (`DRAFT`, no Command Intent), preserving the Command Governance boundary.

`npm run s1:registry:postgres` separately verifies the least-privilege Registry/RLS surface, including read-only access to Forecast lineage and no write access to the retired ESS DispatchPlan/InputResource surface.
