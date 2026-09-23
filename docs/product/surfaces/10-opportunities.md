# 10 节能机会 — Surface Brief

**Status: DESIGN REVIEW**  
**Route intent:** `/sites/$siteId/opportunities`

**Review evidence:** `scripts/fixtures/remaining-pages-review` contains the new evidence/risk/benefit-led Savings Opportunities design review surface. It is browser-rendered for prioritization and Inspector review, but is not yet the production Opportunities surface or production-data integration.

## User job
Prioritize credible energy-saving opportunities, understand evidence/risk/expected benefit, and decide which ideas deserve engineering review or an optimization plan.

## Questions
- Which opportunities are currently supported by evidence?
- What operating condition creates the opportunity?
- What is the expected energy/cost effect and how was it calculated?
- What systems/equipment are affected?
- What constraints or risks could invalidate the action?
- Has the opportunity already been reviewed, rejected, planned or executed?

## Information hierarchy
```text
Opportunity facts strip
Search + system / type / status / risk / benefit filters
Opportunity ledger
  opportunity / evidence scope / expected benefit / risk / status / next action
Context Inspector
  verified evidence
  calculation basis
  affected objects
  constraints / risk
  suggested change
  review history
  create/open optimization plan
```

## Interaction model
- Ledger-first for prioritization.
- Expected savings are shown only when produced by an authoritative deterministic/model owner with a named basis.
- “Opportunity” is not “approved control action”.
- AI may explain or draft, but cannot silently turn a suggestion into expected verified savings.
- Creating an Optimization Plan is a deliberate transition into Phase 2 workflow.

## Component mapping
TanStack Table v9, Badge for status/risk category, Input Group + filters, same-page Inspector, compact evidence blocks, ECharts only when trend evidence materially supports the opportunity.

## URL state
Status, system, opportunity type, risk, minimum benefit, sort and selected opportunity may be durable state. Full plan identity belongs to Optimization Plan route.

## Browser acceptance
- Expected benefit and risk are visible enough to prioritize without opening every row.
- Calculation basis is discoverable and never reduced to an unexplained magic number.
- Proposed action is visually distinct from current authoritative control state.
- Opportunities without sufficient evidence are not styled as ready-to-execute recommendations.
