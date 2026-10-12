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
- The import Tariff's periods are read with the runtime role, to price each avoided hour.

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
- IPMVP Option C with whole-plant boundaries means at most one measure per Site may report at a time.
