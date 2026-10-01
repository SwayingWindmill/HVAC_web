---
name: web-design-guidelines
description: Project-pinned Vercel Web Interface Guidelines review profile for HVAC Web. Use after implementing or materially changing UI to audit accessibility, semantics, focus, keyboard behavior, forms, responsive behavior, typography, motion, performance-sensitive interaction, and web UX quality.
metadata:
  upstream: vercel-labs/agent-skills
  upstream-skill: skills/web-design-guidelines/SKILL.md
  upstream-version: "1.0.0"
  guidelines-source: vercel-labs/web-interface-guidelines
  adaptation: locally pinned HVAC Web review profile
  license: MIT
---

# Web Design Guidelines — HVAC Web Review Profile

This is the repository-local review profile derived from Vercel's `web-design-guidelines` skill and Web Interface Guidelines.

Unlike the upstream skill, **do not fetch mutable review instructions from `main` during execution**. HVAC Web requires reproducible external guidance. Update this local profile deliberately through source review when upstream guidance changes materially.

Use this skill primarily as the final UI review pass after `impeccable` and `frontend-design` have shaped the surface.

## Review scope

Audit the changed surface and the shared components it materially affects. Report concrete problems with file/line or component references when possible. Do not inflate review into unrelated application-wide cleanup.

## Accessibility and semantics

Verify that:

- interactive controls use semantic elements whenever possible;
- icon-only buttons have accessible names;
- labels are programmatically associated with inputs;
- status is not conveyed by color alone;
- heading levels reflect the actual information hierarchy;
- tables preserve table semantics and meaningful headers;
- disabled controls communicate the reason when the user needs to understand it;
- Alerts, dialogs, Drawers, popovers, and confirmations expose understandable text to assistive technology;
- live status changes are announced only when doing so is genuinely useful, not for noisy telemetry churn.

## Keyboard and focus

Verify that:

- every actionable element is keyboard reachable;
- visible focus is preserved with `:focus-visible` or equivalent behavior from the adopted shadcn/Radix primitive;
- card click targets that behave like links/buttons provide correct keyboard activation semantics;
- opening a modal/Drawer/popover places focus predictably when appropriate;
- closing temporary UI returns focus to the trigger when the interaction model requires it;
- focus is not trapped outside an actual modal context;
- row/card selection and row/card navigation are separate when both exist.

## Forms and commands

For filters, settings, and remote-control flows:

- use explicit labels or clear placeholders where the relationship is unambiguous;
- do not rely on placeholder text as the only persistent label for complex forms;
- preserve typed values on validation failure;
- show errors near the field/action that owns them;
- make destructive or safety-sensitive actions explicit;
- confirmation copy states what will happen, not generic "Are you sure?" wording;
- submit buttons reflect the actual action (for example, "下发启动指令" rather than "确定") when safety or clarity benefits;
- required reasons/justifications are visibly required before submission;
- loading/submitting state prevents accidental duplicate submission without inventing retries.

## Typography and content

Check that:

- labels are concise and specific;
- operational values use tabular numerals where changing widths would hurt scanning;
- ellipsis is used intentionally and full values remain discoverable when necessary;
- units stay visually attached to values;
- copy uses the repository's Chinese product terminology consistently;
- errors explain the next useful action when one exists;
- technical transport/storage errors are not exposed as user-facing product language unless the target user needs them.

## Responsive and viewport behavior

Verify the intended layout rather than assuming every page should scroll:

- fixed operator workspaces stay within the viewport when that is the product contract;
- document-level scrolling is absent when scrolling belongs to asset trees, tables, timelines, or detail panes;
- scroll containers have clear ownership and do not create nested-scroll traps without need;
- controls do not wrap into unusable two-line toolbars at supported widths;
- tables, card grids, Drawers, and Splitters degrade intentionally at narrower breakpoints;
- content is not clipped merely to satisfy a no-scroll assertion.

## Motion and feedback

- Respect reduced-motion preferences for nonessential motion.
- Prefer transform/opacity for animation where animation is needed.
- Avoid decorative motion in high-frequency operational surfaces.
- Hover is supplemental; essential actions must not depend on hover.
- Loading, Empty, Error, Offline, Unavailable, and Permission-denied states are distinct when their recovery paths differ.

## Images and icons

- Product images and logos have stable dimensions to avoid layout shift.
- Meaningful images have appropriate alternative text; decorative graphics do not create noisy announcements.
- Do not use a broken/missing asset fallback as an accepted visual state.
- Prefer the existing icon system over one-off SVG decoration when the icon already exists.

## Performance-sensitive UI

For dense operational surfaces:

- avoid re-rendering large lists for unrelated state changes;
- avoid layout reads/writes in hot interaction loops;
- keep expensive chart or telemetry work out of render paths where possible;
- virtualize only when the actual dataset/render cost justifies it;
- do not add animation or visual effects that materially degrade scan/interaction performance;
- avoid loading large visual dependencies for a small decorative result.

Performance optimization must protect a concrete user-visible problem; do not add speculative complexity.

## Interaction clarity

Review for common admin-product failures:

- one click must not ambiguously mean both select and open;
- related controls should be spatially grouped;
- repeated information should have one authoritative location;
- transient inspection uses temporary UI; durable/navigation-worthy detail uses a route;
- page-level service failures should not be repeated on every card as if each device failed independently;
- offline, fault, stale data, unknown result, and permission denial remain semantically distinct.

## Output format

When this skill is used for an audit, report only actionable findings, ordered by severity:

- `BLOCKER` — inaccessible, unsafe, impossible to operate, data/action semantics wrong.
- `HIGH` — major UX/focus/responsive/state defect.
- `MEDIUM` — meaningful clarity or consistency issue.
- `LOW` — worthwhile polish that does not block the task.

Prefer `path:line — finding — recommended correction` when line information is available. If no meaningful finding exists, say so rather than inventing one.

## Relationship to other project skills

Use the project frontend sequence:

1. `impeccable` for module/content hierarchy and product-UI critique.
2. `frontend-design` for visual direction, typography, spacing, density, and composition.
3. `web-design-guidelines` for final web UX/accessibility/interaction audit.

shadcn/ui primitives, TanStack Router/Query/Table, React Hook Form, Zod, and Apache ECharts are the implementation vocabulary and should be checked against their official behavior when relevant. Legacy Ant/Pro usage is migration code, not a design authority.

## Upstream provenance

Adapted from Vercel's `vercel-labs/agent-skills` `web-design-guidelines` skill, version `1.0.0`, and the associated `vercel-labs/web-interface-guidelines` project (MIT). This repository intentionally vendors a stable local review profile instead of executing upstream's mutable `main`-branch fetch on every audit.
