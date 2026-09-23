# Legacy UI Quarantine

**Status: ACTIVE GUARDRAIL / 2026-09-16**

This document prevents historical UI material from re-entering current product design work.

## Current visual authority

For any new or materially redesigned operator-facing Surface, use only the current authority chain:

1. `PRODUCT.md` — business truth, safety, data semantics.
2. `docs/product/smart-energy-system-page-architecture-v3-research-backed.md` — research-backed Workspace map and Surface placement.
3. `docs/product/global-navigation-context-interaction-contract-v2.md` — navigation, context, list-detail and interaction ownership.
4. Matching `docs/product/surface-specifications/*.md` — Surface-specific product contract.
5. `docs/design-system/shadcn-redesign-2026-09-13.md` — selected visual direction.
6. `DESIGN.md` — product-wide visual grammar.
7. `docs/design-system/shadcn-component-contract.md` — component composition contract.
8. Current shadcn/ui + satnaing/shadcn-admin patterns, used as pattern references rather than product templates.

Historical implementations, screenshots, archived source reviews and migration-only runtime code are **not** design authority.

## Quarantined historical visual sources

Superseded Ant/Pro source reviews, old UI kits, old page/image packages, and the former Control Desk decision should be deleted from the active tree. Do not re-add them as design references. If historical archaeology is explicitly required, use Git history and extract business facts only.

Any screenshot or static preview created before the 2026-09-13 Shadcn Application System decision is non-authoritative unless the user explicitly re-approves that specific artifact for the current task.

## Quarantined runtime implementation sources

Until migration is complete, some source files still use Ant Design / ProComponents. Their continued existence is a migration fact, not a design signal.

For new Surface design and implementation, do not copy page anatomy, component composition or CSS from:

- legacy Ant wrappers under `apps/hvac-web/src/components/**`
- legacy/product fallback pages under `apps/hvac-web/src/features/product/**`
- Ant-based monitor/reference implementations under `apps/hvac-web/src/features/monitor/**`
- Ant-based registry/rule/system workbenches under `apps/hvac-web/src/features/system/**`
- any feature file importing `antd` or `@ant-design/pro-components`

`apps/hvac-web/src/shared/ui/**` has been pruned to current callers and no longer imports Ant/Pro. `@ant-design/charts` and the direct `@ant-design/icons` dependency have been removed and must not be reintroduced.

Those files may only be opened when the task is specifically to migrate/delete them, recover business behavior, or verify that a replacement preserves required semantics.

## Search and review rule

When researching or reviewing a new Surface:

- search current product specs and current shadcn design docs first;
- do not use repository-wide search hits from quarantined paths as visual evidence;
- do not infer a target layout from the current implementation;
- do not use the legacy UI pattern gallery, old Ant screenshots, or old ProComponents examples as comparison targets;
- if a legacy file is opened to recover business behavior, extract only domain facts and explicitly discard its visual structure;
- visual approval requires comparison against the current design contract, not comparison against the previous page.

## Migration rule

Ant Design and ProComponents remain installed only while unmigrated runtime callers still exist. Ant Design Charts and the direct Ant Design Icons dependency have already been removed. This does **not** permit new legacy UI usage.

Each migrated Surface must:

- have zero Ant/Pro runtime DOM in its accepted path;
- use project `components/ui` primitives and Tailwind composition;
- remove or detach legacy callers/CSS when they become unused;
- never introduce an Ant↔shadcn compatibility wrapper;
- never preserve old page anatomy merely to reduce migration effort.

Once the final runtime caller is migrated, remove the legacy packages and remaining quarantined implementation files.