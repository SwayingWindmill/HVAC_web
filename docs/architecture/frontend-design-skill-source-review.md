# Frontend design skill source review

Status: CURRENT / DESIGN-RESET RECONCILED  
Last reconciled: 2026-09-12

## 1. Scope

This record fixes provenance and adoption boundaries for the repository-local frontend design skills under `.agents/skills/`.

The skills are methods for shaping, reviewing and auditing operator-facing Web UI. They are not product truth and they do not choose the final visual direction.

During the 2026-09-12 Design Reset, authority is:

1. current explicit user direction;
2. `PRODUCT.md` product truth and safety semantics;
3. the selected global design direction and explicitly approved current-surface references;
4. Surface product/interaction contracts;
5. `DESIGN.md` and frontend architecture;
6. skill guidance;
7. legacy/external references;
8. incumbent implementation.

Legacy Device Center, HVAC Monitor, Dashboard and Alarm reference packages are historical/business-anatomy evidence unless explicitly re-approved.

## 2. pbakaus/impeccable — ADAPT

- Upstream: https://github.com/pbakaus/impeccable
- Reviewed skill: upstream `.agents/skills/impeccable/SKILL.md`
- Reviewed public skill version: `4.1.2`
- Reviewed detector CLI: npm `impeccable@3.5.0`
- License: Apache-2.0
- Local skill: `.agents/skills/impeccable/SKILL.md`

### ADOPT

- Explicit product/design context before UI work.
- `shape`, `critique`, `distill`, `layout`, `typeset`, `harden`, `audit`, `polish` as a task-routing vocabulary.
- Information responsibility and task flow before decoration.
- Browser-rendered visual review instead of treating a build as visual acceptance.
- Advisory detector feedback rather than detector output as product authority.
- Deliberate extraction of recurring design-system patterns after they are proven.

### ADAPT

- HVAC surfaces are operational products with data truth, state semantics and control safety requirements.
- During Design Reset, the skill must describe quality properties rather than silently freeze radius, elevation, Card usage, navigation geometry, scroll model or a specific aesthetic.
- Surface contracts own business/interaction invariants; final composition is frozen only after a direction is selected.
- Detector results remain advisory and must not force legacy visual rules back into the product.

### REJECT

- Treating an upstream aesthetic preference as higher authority than current product/design direction.
- Treating an old screenshot as permanent source of truth.
- Adding fake business data to satisfy composition.
- Promoting the detector into a broad permanent CI gate without a concrete invariant.

## 3. anthropics/skills frontend-design — ADAPT

- Upstream: https://github.com/anthropics/skills
- Reviewed skill: `skills/frontend-design/SKILL.md`
- License: see upstream `skills/frontend-design/LICENSE.txt`
- Local skill: `.agents/skills/frontend-design/SKILL.md`

### ADOPT

- Choose a deliberate visual direction grounded in subject, user and task.
- Avoid generic template aesthetics.
- Establish hierarchy through composition, typography, spacing and state emphasis before micro-decoration.
- Review the actual rendered interface.

### ADAPT

- The global direction may be control-desk, canvas-led, ledger/inspector-led or another explicitly selected composition.
- Typography, density, color, radius, elevation and scrolling remain open during Design Reset rather than inherited from the incumbent UI.
- Distinctiveness should come from a coherent product point of view, not from blindly copying SaaS or HMI conventions.

### REJECT

- A hardcoded “industrial calm” palette or visual treatment imposed by the skill itself.
- Automatic bans or adoption of gradients, shadows, glass, large radii, cards or other purely aesthetic devices before the direction is selected.
- Incumbent AppShell geometry as a default.

## 4. vercel-labs web-design-guidelines — ADAPT / PIN LOCALLY

- Upstream skill: https://github.com/vercel-labs/agent-skills/tree/main/skills/web-design-guidelines
- Upstream skill version: `1.0.0`
- Guidelines project: https://github.com/vercel-labs/web-interface-guidelines
- License: MIT
- Local skill: `.agents/skills/web-design-guidelines/SKILL.md`

### ADOPT

- Accessibility and semantic review.
- Keyboard and focus behavior.
- Forms and confirmation quality.
- Responsive / viewport and overflow review.
- Typography/content legibility.
- Motion and feedback review.
- Performance-sensitive interaction review.

### ADAPT

- The repository vendors a stable local review profile rather than loading mutable remote instructions during every audit.
- Viewport/scroll review checks the selected workspace contract; it does not assume that every page scrolls or every page is fixed.
- Device/alarm/control state semantics remain HVAC-specific product truth.

### REJECT

- Mutable upstream `main` as runtime design authority.
- Generic responsive advice overriding the project's explicitly scoped desktop acceptance work.

## 5. Implementation vocabulary after the architecture reset

The design skills do not choose the component stack. The current frontend architecture does:

```text
React 19
Vite
TypeScript
Tailwind CSS v4
shadcn/ui / Radix baseline
TanStack Router
TanStack Query
TanStack Table
React Hook Form
Zod
Apache ECharts
Lucide
```

X6 and G6 remain task-specific graph engines where fixed engineering topology or dynamic relationships genuinely require them.

Ant Design, ProComponents and Ant Design Charts are migration sources only, not target implementation vocabulary.

## 6. Project workflow under Shadcn Application System

For material operator-facing frontend work:

1. Read `PRODUCT.md`.
2. Read `DESIGN.md`.
3. Read `docs/design-system/shadcn-redesign-2026-09-13.md` as the active visual/application decision; treat the 2026-09-12 Control Desk reset as historical only.
4. Read the matching `.impeccable/surfaces/*.md` contract.
5. Load `impeccable`, `frontend-design`, then `web-design-guidelines` as appropriate.
6. The selected global direction is Shadcn Application System. Deeply reference current shadcn/ui component grammar and satnaing/shadcn-admin application composition; do not preserve Control Desk v1 or Ant/ProComponents geometry through compatibility UI.
7. Implement using current React SPA architecture and the surface-specific shadcn composition.
8. Render the real page at the approved desktop viewport and review against the selected direction / explicitly approved current-surface reference.
9. Run only focused product/architecture tests and advisory design review needed by the changed contract.

## 7. Device Center reconciliation

The previous source review treated `docs/smart_energy_device_center_final_package/` as a standing surface-specific visual authority and fixed a no-document-scroll workspace plus Drawer → Splitter → full-detail progression.

Those **visual conclusions are superseded** by the 2026-09-12 Design Reset.

Still retained:

- independent device-state semantics;
- business-only operator information;
- durable first-class Device detail;
- context-preserving investigation;
- owner/state boundaries;
- real browser visual verification.

Reopened:

- fixed vs document scrolling;
- scope navigation form;
- card/table/ledger/matrix default;
- KPI placement;
- inspector/Sheet/dock/split pattern;
- exact density and visual tokens.

## 8. Provenance rule

Changing the final visual direction does not require pretending earlier source reviews never existed. Historical reviews remain useful evidence, but any earlier ADOPT/ADAPT conclusion that conflicts with current explicit user direction or the current `DESIGN.md` must be marked superseded rather than preserved through compatibility UI.