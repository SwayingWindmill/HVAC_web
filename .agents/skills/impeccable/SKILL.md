---
name: impeccable
description: Deeply integrated Impeccable workflow for HVAC Web frontend design, redesign, critique, audit, layout, typography, distillation, hardening, polish, and visual verification. Use for any operator-facing React SPA surface; not for backend-only work.
metadata:
  upstream: pbakaus/impeccable
  upstream-skill-version: "4.1.2"
  detector-cli: "impeccable@3.5.0"
  upstream-skill: .agents/skills/impeccable/SKILL.md
  mode: Operate
  adaptation: HVAC Web product + React SPA / shadcn profile
  license: Apache-2.0
---

# Impeccable — HVAC Web Deep Integration

Impeccable is part of the frontend development workflow in this repository. It is not an optional inspiration pack and it is not a replacement for product truth, the target React SPA architecture, or rendered evidence.

HVAC Web authenticated application surfaces are **Operate** interfaces: operators and engineers are completing tasks. Scanability, consistency, familiar controls, semantic state separation, and the real operating scene outrank spectacle.

## 0. Required context before UI work

For any material frontend task, read in this order before editing:

1. `PRODUCT.md` — durable product truth.
2. `DESIGN.md` — current design status, invariants, and implementation constraints.
3. `docs/design-system/shadcn-redesign-2026-09-13.md` — the active decision selecting the Shadcn Application System; the 2026-09-12 Control Desk reset is historical only.
4. The matching `.impeccable/surfaces/*.md` contract when one exists.
5. The target component, its CSS/tokens, and the shared components it composes.
6. Only the visual references explicitly approved for the current design direction.

Legacy Device Center, HVAC Monitor, Alarm Center, Dashboard and other image packages are historical/business-anatomy evidence during the Design Reset. Do not promote them back to visual authority unless the user explicitly approves them again.

Do not treat the existing implementation as design authority. Do not treat Impeccable detector output as product authority either.

## 1. Authority order

Resolve design conflicts in this order:

1. Explicit current user requirement.
2. Real product/domain truth and safety semantics in `PRODUCT.md`.
3. The currently selected global design direction and explicitly approved current-surface references.
4. The matching Surface contract for business/interaction requirements.
5. `DESIGN.md`, `docs/architecture/smart-energy-react-spa-frontend-architecture.md`, official shadcn/Radix and TanStack/ECharts semantics, and shared components/tokens.
6. Impeccable craft guidance and detector findings.
7. External/legacy reference material.
8. Existing page-specific CSS/markup.

Never invent device state, alarms, diagnosis, telemetry, permissions, controls, savings, or model confidence to make a layout look complete.

## 2. Command routing

Use the upstream Impeccable vocabulary as the working method. The user does not need to type the command name when intent is clear.

### Build / restructure

- `shape` — use before code when module structure, information architecture, workflows, or progressive disclosure are still wrong/unclear.
- `document` — reconcile durable design-system truth into `DESIGN.md`; do not run as a side effect of ordinary styling.
- `extract` — move genuinely repeated tokens/components into the design system after a recurring pattern is proven.

### Evaluate

- `critique` — first pass for an existing surface that “still feels wrong”; judge hierarchy, clarity, cognitive load, grouping, content responsibility, and task flow before micro-CSS.
- `audit` — technical Web quality: accessibility, semantics, focus, keyboard, responsive behavior, overflow, performance-sensitive interaction, broken assets.

### Refine

- `distill` — remove duplicate copy, duplicate counts, unnecessary tags, decorative wrappers, or detail that belongs to progressive disclosure.
- `quieter` — reduce equal-weight status color, excessive borders/shadows, over-emphasis, and component-library noise.
- `bolder` — only when hierarchy is too weak after content is correct; never add marketing spectacle to an Operate surface.
- `polish` — final alignment and system-consistency pass after product structure is correct.
- `harden` — errors, loading/empty states, long text, i18n, unavailable/offline states, and realistic edge cases.

### Targeted craft

- `layout` — spacing, alignment, container geometry, scan rhythm, scroll ownership, responsive structure.
- `typeset` — hierarchy, size, weight, line-height, numeric alignment, truncation, units.
- `clarify` — labels/copy that are ambiguous or implementation-oriented.
- `adapt` — responsive/device adaptation.
- `optimize` — UI performance only when a real performance problem exists.
- `animate` / `delight` / `overdrive` — rarely appropriate for authenticated HVAC Operate surfaces; use only when explicitly justified by product behavior.

## 3. Default workflow for existing HVAC Web surfaces

When improving an existing page:

1. **Critique** the rendered/current structure: identify the 3–7 highest-impact defects, not a long aesthetic wishlist.
2. **Shape** only if module/content responsibility or navigation/progressive disclosure is wrong.
3. **Distill** before adding decoration.
4. Use **layout/typeset/quieter** as targeted fixes.
5. Implement with shadcn/ui primitives, Tailwind CSS, TanStack Router/Query/Table, React Hook Form/Zod, Apache ECharts, and existing proven domain components/tokens.
6. Run the focused Impeccable detector command.
7. Render the actual page and inspect the target viewport.
8. Batch visible defects and fix them together.
9. Run focused product tests/typecheck/build needed by the changed contract.
10. Run the `web-design-guidelines` review for final Web UX/accessibility semantics.
11. Perform at most one confirming visual pass unless a real regression remains or the user asks for more iteration.

A successful build is not visual acceptance. Detector success is not visual acceptance. DOM geometry alone is not visual acceptance.

## 4. Operate-mode craft floor

The Shadcn Application System is now the selected visual language. Craft guidance must deepen current shadcn/ui and satnaing/shadcn-admin application grammar without restoring the superseded Control Desk or Ant/ProComponents geometry, and without inventing a second visual language:

- a scan path that matches the operator's current task;
- clear distinction between context, evidence, decision and action;
- information density justified by the workflow rather than template defaults;
- operational values whose units, freshness and quality remain legible;
- state emphasis proportional to business meaning;
- progressive disclosure only where it reduces real cognitive load;
- explicit scroll ownership when overflow exists;
- conventional interaction semantics unless an HVAC-specific interaction provides clear value.

Avoid product defects rather than enforcing one visual style:

- presenting semantically different states as if they were the same fact;
- decorative UI that competes with critical operating evidence;
- duplicate scope/count/status information with no new decision value;
- technical metadata or service errors repeated into every business object;
- fake zero for unknown/unavailable data;
- visual elements that imply interaction but do nothing;
- CSS selectors broad enough to unintentionally style nested third-party or shared primitives.

Card usage, radius, elevation, surface treatment, content width, scroll ownership and navigation geometry follow the Shadcn Application rules in `DESIGN.md`; individual Surfaces may refine them only for a real task-specific reason.

When styling composed primitives, prefer direct-child or specifically scoped selectors/classes. Do not let page-level utility or selector rules leak into nested shared components; component ownership should stay explicit rather than relying on broad descendant selectors.

## 5. Semantic separation is mandatory

Running state, connectivity, diagnosis/FDD, alarm state/severity, telemetry freshness/quality, maintenance state, and command/control state are independent dimensions.

Differentiate through information hierarchy and component vocabulary before adding color. Example: running Tag + connectivity Badge + diagnosis treatment is preferable to three same-weight pills.

Offline does not mean fault. Stale data does not mean alarm. Data-quality degradation does not mean diagnosis.

## 6. Detector integration

`scripts/run-impeccable-design-audit.mjs` prefers a locally installed official Impeccable CLI (`impeccable@3.5.0`). When that binary is not available, it falls back to the repository's zero-network `scripts/impeccable-hvac-detector.mjs` adapter so edit/Stop feedback remains deterministic in offline/WSL environments. The adapter enforces a narrow set of high-confidence HVAC operator-UI anti-patterns and keeps softer typography/elevation observations advisory; migration-specific Ant checks may remain temporarily until the corresponding legacy code is deleted, but they are not target design rules.

Available focused commands:

```text
npm run web:design:changed
npm run web:design:device-center
npm run web:design:detect
```

Use `web:design:changed` after a normal meaningful frontend batch. Use the surface-specific command while iterating on Device Center. Use the full Web scan deliberately, not on every tiny edit.

Detector findings are **advisory evidence**:

- Fix a finding when it identifies a real product/design defect.
- If a finding conflicts with approved product/reference/design-system intent, keep the intended design and scope/document the exception instead of contorting the UI.
- Do not add compatibility CSS or meaningless markup solely to silence a detector.
- Do not promote this detector into a broad permanent CI gate without a concrete product invariant; this repository forbids gate inflation.

## 7. Codex design hook

`.codex/hooks.json` runs the project wrapper after UI edits and on Stop.

The hook:

- scans only HVAC Web source files;
- uses the pinned official Impeccable detector;
- is advisory and exits without blocking edits;
- scans changed UI files on the deep pass rather than the whole repository;
- guards Stop-hook re-entry;
- is cross-platform Node code rather than POSIX-only shell glue;
- respects `.impeccable/config.json` `hook.enabled`.

Codex requires project hook trust/approval in the client before it executes.

## 8. Design context artifacts

Tracked project artifacts:

- `PRODUCT.md` — durable product truth.
- `DESIGN.md` — current durable Shadcn Application design authority and invariants.
- `docs/design-system/shadcn-redesign-2026-09-13.md` — active visual/application decision; the 2026-09-12 Control Desk record is historical.
- `.impeccable/config.json` — shared detector/hook configuration.
- `.impeccable/surfaces/*.md` — surface-specific product/interaction contracts and, after selection, approved surface direction.

Runtime screenshots/caches/review working files under `.impeccable/` are gitignored.

Do not overwrite PRODUCT/DESIGN context during a normal page task. Extend or reconcile them only when the product/design truth genuinely changes.

## 9. Device Center during Design Reset

For `apps/hvac-web/src/features/devices`:

- Treat `.impeccable/surfaces/device-center.md` as a business/interaction contract, not a frozen composition.
- Preserve independent runtime, connectivity, freshness/quality, diagnosis, alarm, maintenance and control semantics.
- Do not reintroduce device ID/path/point count/registry metadata as permanent operator copy without a task reason.
- Preserve durable Device detail addressability and Browser Back context.
- Keep missing/offline/unavailable values truthful.
- Do not assume fixed viewport, scope tree, KPI strip, Card grid, Sheet, Drawer or Splitter until the selected design direction explicitly chooses them.

## 10. Review questions before completion

- Can the operator understand the surface purpose and abnormal state in one scan?
- Does every module contain only information needed at that level?
- Is abnormal state easier to find than healthy background state?
- Are related controls adjacent and unrelated concerns separated?
- Did we remove repeated metadata before adding new decoration?
- Do text and controls have real breathing room from container boundaries in the rendered page?
- Are nested component selectors scoped so parent styles cannot leak into child Cards/Tables/Drawers?
- Does the page still work with long values, `—`, offline, stale, loading, empty, and error states?
- Is the scroll owner correct for the selected workspace model?
- Did a real browser render materially match the currently approved design direction or current-surface reference, rather than a superseded screenshot?

## Upstream provenance

Deep project adaptation of `pbakaus/impeccable` Skill `4.1.2` (Apache-2.0). Detector execution is pinned separately to npm `impeccable@3.5.0`. The local profile intentionally narrows the upstream's broad creative range to enterprise HVAC Operate surfaces while preserving its command vocabulary, product/design context model, critique/audit workflow, anti-pattern detector, bounded visual verification, and hook-driven feedback model.
