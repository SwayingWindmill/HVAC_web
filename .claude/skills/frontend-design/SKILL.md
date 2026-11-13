---
name: frontend-design
description: Project-adapted frontend-design guidance for creating distinctive, intentional, production-grade HVAC Web interfaces without generic template aesthetics. Use when building or reshaping UI, especially when a surface needs a clearer visual point of view, stronger hierarchy, typography, spacing, or composition.
metadata:
  upstream: anthropics/skills
  upstream-skill: skills/frontend-design/SKILL.md
  adaptation: HVAC Web enterprise-product profile
  license: upstream LICENSE.txt
---

# Frontend Design — HVAC Web Profile

Use this skill after the surface's information architecture is understood. It owns visual direction and composition; it does not override product truth, domain semantics, the currently selected design direction and explicitly approved current-surface references, the target React SPA architecture, standard shadcn interaction semantics, or accessibility requirements. The active 2026-09-13 Shadcn Application System uses current shadcn/ui plus satnaing/shadcn-admin as the primary application-design references; legacy screenshots, Control Desk v1 and incumbent layouts are research evidence only unless the user explicitly re-approves them.

## Core rule

Avoid generic "admin template" and generic "AI-generated SaaS" aesthetics. Make deliberate choices that fit an industrial energy-management product and the specific surface being built.

For HVAC Web, distinctiveness should usually come from **clarity, density, alignment, typography, state hierarchy, and confident restraint**, not decorative novelty.

## Ground the design in the subject

Before editing, establish:

- Who is operating this surface?
- What are they trying to notice or do in the next few seconds?
- What information is safety/operations critical?
- What reference image, existing product surface, or design-system decision is the visual authority?
- What should explicitly *not* be copied from the incumbent implementation?

Do not design an abstract dashboard. Design the actual device center, alarm workspace, energy analytics page, command confirmation flow, or maintenance view.

## Make one coherent visual decision

Choose a clear visual logic for the surface and carry it through consistently. The selected global direction is the Shadcn Application System; start from current shadcn/ui component grammar, satnaing/shadcn-admin application composition, and the surface's real task—never from the incumbent implementation.

A coherent surface can be canvas-led, ledger-led, analysis-led, inspector-led, or another approved composition; the important requirement is that its geometry, hierarchy, density and interaction all support the same job.

Do not mix several visual worlds in one screen without a deliberate product reason.

## Hierarchy before decoration

Establish hierarchy with this order:

1. module placement and size;
2. typography and numerical emphasis;
3. spacing and alignment;
4. component choice;
5. color/state emphasis;
6. border, radius, and elevation details.

If the page still feels wrong at steps 1–3, do not try to fix it with shadows, gradients, icons, or illustrations.

## Typography

- Typography follows the Shadcn Application hierarchy and scale in `DESIGN.md`; deviations require a real surface-specific need.
- Prefer a small number of type levels with obvious purpose.
- Operational metrics should use tabular numerals where alignment matters.
- Device names, object identities and section headings should be easy to scan without competing with the task's primary evidence.
- Tertiary text must remain legible; do not solve hierarchy by making important context unreadably faint.
- Avoid permanently visible explanatory prose when the control or label already communicates the same thing.

## Composition and density

- Spacing rhythm and density are open until the selected direction freezes them.
- Align repeated values, labels and controls to stable visual structures appropriate to the chosen workspace.
- Grouping boundaries should correspond to information responsibility, not component-library defaults.
- Keep related controls spatially close.
- Avoid arbitrary empty space produced by default component padding or placeholder content.
- Document scrolling, internal scrolling and fixed workspaces are explicit design decisions; none is the default during Design Reset.

## Color and emphasis

- Use the neutral Shadcn Application palette and independent business semantic colors from `DESIGN.md`; component defaults do not redefine product state semantics.
- State/severity color must preserve business semantics and accessibility.
- Healthy/background information should not compete with critical alarm, risk, active selection or primary action.
- Decorative color should never make unrelated modules look semantically urgent.
- Gradients, glow, glass, strong shadows or other treatments are design choices to evaluate deliberately, not automatically banned or adopted by a skill rule.

## Components

Use shadcn/ui primitives and the target React SPA stack as the implementation vocabulary. Do not rebuild standard controls simply to make them look custom; reserve project-owned components for proven energy-domain semantics and composed product patterns.

Choose components according to interaction semantics rather than the previous page pattern:

- bounded state labels may use text, icon, badge/tag-like treatment, or another approved encoding;
- page/module-level actionable conditions need an explicit alerting treatment appropriate to the chosen design;
- temporary inspection may use Sheet, side inspector, dock, split pane, popover or another approved pattern;
- scan-heavy comparison generally benefits from row/ledger/table structures;
- Card is available when it represents a meaningful discrete object or grouping, but it is neither required nor forbidden as a universal rule.

## Anti-template review

Before finishing, look specifically for:

- every section wrapped in a Card;
- rounded-square icon tiles repeated everywhere;
- large hero-like page headers in an operational tool;
- excessive pill controls;
- colorful KPI icon mosaics;
- weak hierarchy compensated by extra labels;
- identical styling for semantically different status dimensions;
- decorative device graphics that reduce the space available for real values;
- layouts that could belong to any SaaS product with only the nouns changed.

If any appear, redesign the hierarchy rather than merely restyling them.

## Visual verification

For visual work, inspect the actual rendered page at the target viewport. Compare against the currently selected global direction and any explicitly approved current-surface reference; during Design Reset do not compare against superseded screenshots as if they were targets. Review:

- information responsibility and module boundaries;
- horizontal and vertical geometry;
- density;
- scan path;
- typography hierarchy;
- state emphasis;
- whitespace distribution;
- control grouping and interaction affordance.

Do not declare a design visually complete solely because TypeScript, tests, or a production build passes.

## Relationship to other project skills

Use the project frontend sequence:

1. `impeccable` — shape information architecture and remove product-UI anti-patterns.
2. `frontend-design` — establish/refine the visual hierarchy and composition.
3. `web-design-guidelines` — audit accessibility, interaction, forms, focus, web semantics, motion, responsiveness, and implementation quality.

## Upstream provenance

Adapted for this repository from Anthropic's `frontend-design` skill in `anthropics/skills`. The upstream skill emphasizes deliberate visual direction and avoiding templated defaults; this adaptation constrains that guidance to enterprise HVAC/energy-management surfaces and the repository's React SPA architecture based on shadcn/ui, TanStack, and Apache ECharts.
