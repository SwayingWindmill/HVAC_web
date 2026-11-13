# Energy billing

The `/energy-analysis/billing` route manages monthly electricity budgets, utility settlement bills (`专变核对` or cross-month whole-building allocation), chiller-plant variance reconciliation, and archived report integration.

## Sub-features

- `billing-budget-and-settlement` edits monthly budgets and utility bills in dialogs (`UtilityBillDialog`), computes variance vs metered electricity, and archives reconciled bills.
- `billing-allocation-modes` handles both dedicated transformer verification (`专变核对`) and whole-building cross-month prorating (`整栋总表分摊`).
- `billing-layout-1440` renders overview KPIs, tables, and settlement tabs with zero horizontal overflow at `1440×900`.

## How to get to it (user POV)

- Open `能源` → `电费账单` in the sidebar, or navigate to `/energy-analysis/billing?site=<SITE_ID>&month=2026-10`.

## Driving it with Playwright

Preconditions:

- Tier 1: run `CI=1 PLAYWRIGHT_PORT=5194 npm run web:e2e -- --workers=1 e2e/billing.spec.ts` (injects priced daily electricity via `gateway.energySeriesHandler`).

- **Drive budget, settlement bill entry, and report sidecar at 1440px.** Open `/energy-analysis/billing?site=${SITE_ID}&month=2026-10` at `1440×900`, verify heading `电费账单`, test whole-building allocation, chiller variance calculation, and archive persistence with zero horizontal overflow.
- **Proof.** Capture pass output and `1440×900` screenshot under `.scratch/verify/<slug>/`.

## Gotchas

- `billing.spec.ts` injects full daily TOU cost buckets (`SUPER_PEAK`, `PEAK`, `FLAT`, `VALLEY`) so bill reconciliation variance assertions stay deterministic across dates.
