# Legacy shared UI quarantine

This directory contains migration-era shared UI, including Ant Design / ProComponents-based components.

It is **not** the component source for new or materially redesigned Surfaces.

Current feature code should prefer:

- `@/components/ui/*` for shadcn primitives;
- domain-specific composition inside `features/<domain>`;
- `DESIGN.md` and `docs/design-system/shadcn-component-contract.md` for composition rules.

Do not copy page anatomy, KPI patterns, Drawer/Detail composition, chart wrappers, colors or spacing from this directory into a new Surface. Open these files only when migrating/removing an existing caller or recovering required business behavior. See `docs/design-system/legacy-ui-quarantine.md`.
