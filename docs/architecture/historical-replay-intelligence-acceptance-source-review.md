# Historical Replay intelligence acceptance source review

Issue: #348

Parent specification: #331

Date: 2026-08-29

## Scope

#348 adds acceptance evidence only. It must prove that Historical Replay history can flow through the existing Energy/Metric/Forecast/Optimization owner boundaries without adding a Replay-specific business shortcut or changing any production owner contract.

The overlapping upstream behavior was rechecked against the pinned source records used by #343, #344, and #345. Existing HVAC_web implementation was not treated as authority for the acceptance design.

## Pinned upstream evidence and decisions

| Project | Pinned source evidence | Acceptance decision |
| --- | --- | --- |
| ThingsBoard CE | `v4.3.1.1` / `c2a52e46c44e308ddee430e7266b8e10eddde9c4`: `BaseTimeseriesService.java`, `SqlLatestInsertTsRepository.java`, `SqlTimeseriesLatestDaoTest.java`, `TelemetryController.java` | **ADOPT** explicit History versus Latest semantics and bounded server-side historical reads. **ADAPT** the acceptance chain so Replay first becomes canonical Telemetry History and downstream owners subsequently read their own authoritative inputs. **REJECT** direct Replay writes to Metric, Forecast, Optimization, Latest, Presence, or any caller-controlled replay flag on live ingest. |
| OpenEMS | Historical Timedata/resend source pinned for #343 at `2026.7.0` / `2e2792d`; prediction ownership pinned for #344; optimizer context source/tests pinned for #345 | **ADOPT** separate historical recovery from live state, typed owner-side historical providers, and optimizer-owner construction of a bounded context from predictions/current state/constraints. **ADAPT** those seams into Telemetry History -> Metric facts -> immutable Forecast input -> sealed Optimization input. **REJECT** copying OSGi/component infrastructure, using replay as a live Channel update, or defaulting missing prediction/input state. |
| MyEMS | `v6.7.0` / `be6e6ce8ddeac57afb04bddb9621501fb555cab0`: database/cleaning/normalization/aggregation and report history paths reviewed for #343/#344; optimization overlap adjudicated in #345 | **ADOPT** preserved historical facts feeding later processing rather than replacing Current/latest truth. **ADAPT** historical processing into the existing HVAC_web owner chain. **REJECT** direct database schedule/optimization truth, replay-time business diagnosis, and mutation of raw history to manufacture downstream results. |

## #348 acceptance boundary

The acceptance chain is intentionally:

```text
Historical Replay admission
  -> Telemetry PostgreSQL source observation / durable History outbox
  -> ClickHouse telemetry_history.observations
  -> Energy owner counter projection
  -> Metric owner calculations / metric_result_facts
  -> Forecast owner server-created immutable input + Forecast publication
  -> Optimization owner BUILDING -> SEALED input + Recommendation publication
```

The tracer also establishes normal current Telemetry observations before Replay and proves the Replay portion leaves Latest, Presence, Device Snapshot, and Business Revision unchanged.

A deliberately late Replay observation is admitted after newer offsets. Downstream Metric preparation must still group/read by `sampled_at` event time, so transport/import order cannot redefine the historical period.

## ADOPT / ADAPT / REJECT summary

- **ADOPT** owner-separated historical/current semantics, deterministic historical identity, explicit event-time reads, immutable Forecast input lineage, and sealed Optimization input lineage.
- **ADAPT** prior isolated #343/#344/#345 integration proofs into one cross-owner acceptance tracer sharing the same Tenant/Site/Device/Point lineage.
- **ADAPT** Energy counter evidence as a second independent downstream proof that Replay History is usable without Replay owning Energy facts.
- **REJECT** any production `if source_path == HISTORY_REPLAY` branch inside Energy, Metric, Forecast, or Optimization.
- **REJECT** hand-inserted Metric facts or Forecast snapshots as sufficient #348 evidence.
- **REJECT** Replay-authored current Optimization baseline telemetry; chilled-water supply and zone temperature remain normal Current Telemetry inputs.
- **REJECT** direct Command Intent creation. The final Optimization result remains a `DRAFT` Recommendation with no executable command authority.

## Verification contract

`npm run historical-replay:intelligence:acceptance` starts isolated Telemetry PostgreSQL, Registry PostgreSQL, and ClickHouse instances and runs the controlled Plant/Scenario proof plus five focused real-store tracers in sequence:

1. Canonical Plant/Scenario runner produces a deterministic replay request sequence for the same dataset identity.
2. Replay admission / idempotency / late event-time evidence and unchanged Current truth.
3. Counter History -> Energy interval facts.
4. Replay Point History -> Metric facts, including daily energy and energy cost owner outputs.
5. Replay-derived Metric facts -> server-owned day-ahead Forecast input/job -> 96 persisted Forecast points.
6. Persisted Forecast + authoritative Metric/Current Telemetry + Registry constraints -> sealed Optimization input -> non-executable Recommendation.

The command emits `out/historical-replay/intelligence-acceptance.json` as machine-readable evidence. A failed environment or assertion leaves `status: failed`; it is not treated as acceptance success.
