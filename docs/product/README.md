# Product architecture authority

**Status: SELECTED — 2026-09-13 greenfield smart-energy product reset**

This directory defines the current product page architecture and page responsibilities for `apps/hvac-web`.

## Authority

For product/page design, use context in this order:

1. The user's latest explicit requirement.
2. `PRODUCT.md` for stable business truth, safety and workflow semantics.
3. `docs/product/smart-energy-system-page-architecture-v3-research-backed.md` for the research-backed 10-Workspace model, mature-product evidence, Surface placement and page-responsibility decisions.
4. `docs/product/global-navigation-context-interaction-contract-v2.md` for navigation projection, Workspace/View ownership, list-detail behavior, Inspector/Sheet/Dialog/Durable Route boundaries, and component-source selection rules.
5. `apps/hvac-web/src/app/workspace-catalog.ts` for the machine-readable 10-Workspace / 36-Surface target mapping.
6. The matching file under `docs/product/surface-specifications/01–36` for business/domain evidence and page-specific facts during migration; route/navigation placement is overridden by the v3/v2 authorities.
7. `docs/product/surface-catalog-final-review-2026-09-15.md` is historical evidence from the former 36-Surface planning stage, not current navigation authority.
7. `docs/product/content-design.md` for product language, headings, supporting copy, KPI labels, empty states and action labels.
8. `DESIGN.md` and `docs/design-system/shadcn-component-contract.md` for visual and component composition.
9. Current backend/API/domain contracts for what facts and actions actually exist.

`docs/product/smart-energy-system-page-architecture-v1.md`, `docs/product/smart-energy-system-page-architecture-v2.md`, `docs/product/global-navigation-context-interaction-contract-v1.md`, `docs/product/product-information-architecture-v2.md`, `docs/product/surface-catalog-final-review-2026-09-15.md`, and the existing files under `docs/product/surfaces/` are historical planning artifacts. They are **not current navigation/page-structure authority**. Current redesign work must start from the v3 Workspace blueprint and v2 interaction contract.

## Explicit non-authority

The following are **not** design inputs for the new page system unless the user explicitly re-approves a specific artifact:

- Existing frontend page composition.
- Existing routes and menu grouping.
- Historical Dashboard / Device Center / HVAC Monitor / Alarm Center screenshots or design packages.
- Ant Design / ProComponents-era page contracts.
- Previous card-wall, Drawer-first, fixed-workspace or big-screen composition.

Superseded visual packages should be deleted from the active tree rather than retained as design references. Git history is sufficient for archaeology when explicitly required.

## Working rule

Every new or redesigned page must show its design process explicitly:

`user job → questions to answer → information hierarchy → actions → URL/state ownership → component mapping → browser review`

Do not start from "what components do we already have?" or "what did the old page look like?".
