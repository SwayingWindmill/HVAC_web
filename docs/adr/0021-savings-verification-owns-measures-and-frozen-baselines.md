# ADR 0021 — Savings verification owns measures and frozen baselines

Status: accepted

Date: 2026-10-12

## Decision

Savings verification (M&V, #484, spec `docs/specs/savings-verification.md`) is its own owner: `mv-service`, a workload beside Forecast, Optimization and FDD.

**What it owns.**
- The Site's ledger of energy conservation measures (节能措施).
- Each measure's baseline model. It is fitted on confirmation of implementation and frozen once the first day's savings are published.
- Each day's verified result for each measure: hours, adjusted baseline, actual energy, avoided energy and avoided cost.
- All of this lives in `core_registry` tables written only by the `mv_runtime` role, under the same Tenant/Site row-level security as the other intelligence owners.

**What it reads.**
- Hourly plant electricity and cooling come from released Metric results (`hourly_energy`, `hourly_cooling`). They are DELTA Metrics on the HVAC meter's energy counter and the BTU meter's cooling counter. mv-service reads them from ClickHouse with its own read-only account (`mv_service_reader`), the way Optimization reads `daily_energy`.
- A measure bounded by one machine's sub-meter (IPMVP Option B, added 2026-10-13) reads that device's `device_hourly_energy`, a DELTA Metric on the sub-meter's counter, against the plant's `hourly_cooling`. Each verified day also records the plant's `hourly_energy` over the same hours, so a Site's savings are read against the whole plant.
- The runtime role reads the Site's timezone, which verified days are split on.
- It also reads the import Tariff in effect, to price each avoided hour. Hours are priced on the Tariff Version's own timezone, the way Settlement prices them. When no Tariff is assigned, energy is still verified and the hours stay unpriced.
- A measure may cite the executed command it came from. This is a reference into Command Governance's records, not a key. mv-service does not check the command, and the page resolves it from the Site's command list. An executed optimization recommendation is cited through the command it produced.

**How it runs.**
- A daily `MV_EVALUATION` Scheduler job verifies the Site's implemented measures, claimed through `libs/schedulerjob` like FDD.
- Platform Gateway exposes measures under `/api/v1/sites/{siteId}/savings/…`. Reads need `site.read`. Writes (register, implement, refit, end) need the new IAM capability `savings.manage`.

We chose a separate owner because "verified savings" is an audit claim (PRODUCT.md §4.7: 已验证收益). The record that states a saving should not be the same owner that proposed the change (Optimization). A frozen baseline must not move when later data changes, so the frontend cannot compute savings on the fly.

## Considered Options

- **Inside optimization-service.** Rejected. Proposing a change and verifying its outcome would share one owner, which blurs the audit boundary.
- **Compute in the browser from the energy series.** Rejected. Nothing would be frozen, a published saving could change after the fact, and nothing could be reviewed later.
- **Read raw `analytics.energy_interval_facts`.** Rejected. It would duplicate the revision and quality semantics the Metric engine already applies. Settlement also consumes released Metric results, so M&V and billing stay on one basis.

## Consequences

- Two hourly Metrics per Site must be released and scheduled before a measure can be verified. The local seed provides them.
- `mv_service_reader` can read every Tenant's Metric results. The query scopes it to the Site, so the account sees only metric result tables and cannot write.
- Measures overlap only on different machines' sub-meters: two on one device, or any beside one on the plant's total meter, would count the same energy. The service and a table trigger enforce it.
- Each sub-metered device needs its own released `device_hourly_energy` binding before it can bound a measure.
