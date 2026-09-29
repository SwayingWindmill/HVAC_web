# Design system assets

The repository root `DESIGN.md` is the product-wide visual authority. This directory contains the active shadcn design decisions, the component contract, historical decisions, and design-gate assets.

## Active authority

Read these together for frontend UI work:

1. `PRODUCT.md` — business truth, safety, data semantics, workflow constraints.
2. `DESIGN.md` — current Shadcn Application System and product-wide visual language.
3. `shadcn-redesign-2026-09-13.md` — decision record that supersedes the earlier Control Desk visual direction.
4. `hvac-application-ui-specification.md` — application-level Breadcrumb/Page Header grammar, Surface archetypes, tablecn and ReUI/Kibo UI/Dice UI selection boundaries, chart/table rules and UI Definition of Done.
5. `shadcn-component-contract.md` — component selection and semantic composition rules for Card, Data Table/Data Grid, Field, Input Group, Tabs, Dialog, AlertDialog, Sheet, Badge and related primitives.
6. `../architecture/shadcn-tablecn-reui-source-review-2026-09-20.md` — source review and ADOPT/ADAPT/REJECT decisions for tablecn and ReUI.
7. `../architecture/kibo-diceui-advanced-components-source-review-2026-09-20.md` — source review for Kibo UI / Dice UI and shared advanced-component selection rules.
8. `legacy-ui-quarantine.md` — mandatory isolation rules for historical Ant/Pro pages, screenshots, source reviews and migration-only runtime code.
9. `apps/hvac-web/components.json` — exact local shadcn registry/base configuration (`radix-nova`, neutral tokens, Lucide).

Superseded Control Desk / Ant-era design records are removed from the active tree instead of being kept as reference material. Git history is the archaeology path. Any legacy visual source named by `legacy-ui-quarantine.md` must not be reintroduced as current design evidence.

## Production component system

Production frontend UI is based on:

- Tailwind CSS v4;
- project-owned `apps/hvac-web/src/components/ui` shadcn source components;
- Radix component base as selected by `components.json`;
- unified `radix-ui` primitive package inside UI primitives;
- Lucide product icons;
- TanStack Router / Query / Table v9;
- tablecn as the default operational Data Table / ledger composition reference, implemented in the project v9-native table layer;
- ReUI, Kibo UI and Dice UI as the approved advanced application-component source layer when base shadcn/ui and the table layer do not cover a justified interaction;
- shadcn Chart + Recharts for ordinary application charts;
- Apache ECharts for engineering-grade dense/interactive analytics;
- X6/G6 only for genuine engineering graph roles.

Ant Design, ProComponents and Ant Design Charts are migration-only historical dependencies on surfaces that have not yet been migrated. They are not design authority and must not be wrapped in a compatibility layer.

## Component policy

Use `shadcn-component-contract.md` before introducing a new UI pattern.

Key rules:

- business features import `@/components/ui/*`, not primitive-library internals;
- do not mix Base UI `render` APIs into the current Radix project; Radix composition uses `asChild`;
- operational Data Tables use tablecn grammar on the project TanStack Table v9 layer and remain domain-specific;
- ReUI Data Grid is reserved for genuinely advanced grid tasks and must be source/API reviewed before adoption;
- ReUI and Dice UI primitive-specific components use the Radix flavor/path only;
- Kibo UI components require per-component primitive/headless dependency review before adoption;
- overlapping ReUI/Kibo/Dice capabilities resolve to one selected project implementation;
- forms use Field grammar for label/control/description/error;
- Input Group owns search icons, result counts, units and inline input actions when they belong inside the control boundary;
- Dialog is for short modal tasks, AlertDialog for consequential confirmation, Sheet for genuine transient auxiliary content, and durable complex detail uses a Route;
- Card is a semantic section, not a default wrapper for every block;
- standard pages use Tailwind composition before feature-specific CSS.

## Preview assets

Any static previews under this directory are reference artifacts only. They are not production components and must not be imported by the application. Current browser acceptance should be performed against the real Vite application, not a static HTML preview.

## Automated checks

```bash
npm run design:test
npm run design:check
npm run web:design:changed
```

The design gate verifies current shadcn authority, architecture boundaries, the component contract and rejection of legacy Ant/Control Desk authority language. Browser review remains required for migrated surfaces because static checks cannot validate density, hierarchy or responsive composition.

## Primary references

- https://ui.shadcn.com/docs/components
- https://ui.shadcn.com/docs/components/radix/data-table
- https://ui.shadcn.com/docs/components/radix/field
- https://ui.shadcn.com/docs/components/radix/input-group
- https://ui.shadcn.com/docs/components/radix/dialog
- https://ui.shadcn.com/docs/components/radix/alert-dialog
- https://ui.shadcn.com/docs/components/radix/card
- https://ui.shadcn.com/docs/changelog/2026-02-radix-ui
- https://ui.shadcn.com/docs/changelog/2026-07-base-ui-default
- https://github.com/satnaing/shadcn-admin
- https://github.com/sadmann7/tablecn
- https://github.com/keenthemes/reui
- https://reui.io/components
- https://github.com/shadcnblocks/kibo
- https://www.kibo-ui.com/
- https://github.com/sadmann7/diceui
- https://diceui.com/docs/introduction
