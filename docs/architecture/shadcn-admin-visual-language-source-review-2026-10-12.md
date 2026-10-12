# shadcn admin visual language — source review (2026-10-12)

The operations console borrows its visual language from three shadcn admin templates. This
records what was adopted, adapted or rejected, pinned to the commits that were read.

| Repo | Commit | Read for |
| --- | --- | --- |
| [arhamkhnz/next-shadcn-admin-dashboard](https://github.com/arhamkhnz/next-shadcn-admin-dashboard) | `2614ff080ae7` (2026-10-12) | shell, KPI strip, infrastructure/analytics page anatomy, status vocabulary |
| [satnaing/shadcn-admin](https://github.com/satnaing/shadcn-admin) | `e16c87f213a5` (2026-06-11) | team switcher, search trigger, data-table toolbar, thin scrollbars |
| [Kiranism/next-shadcn-dashboard-starter](https://github.com/Kiranism/next-shadcn-dashboard-starter) | `7705dfc0d138` (2026-08-25) | section cards, chart skeletons, page container |

All three are built on the same radix-nova shadcn primitives as `apps/hvac-web`, so the
adoption is in composition and tokens, not in new dependencies.

## Adopted

- **Inset shell on a neutral canvas.** Sidebar `variant="inset"`, the page panel bordered
  (`peer-data-[variant=inset]:border`) with `shadow-xs`; `html`/`body` take the sidebar tone so
  the panel reads as the surface (NSAD `dashboard/layout.tsx`).
- **Sticky blurred top bar**, `h-12 border-b bg-background/80 backdrop-blur-md`; it keeps only
  global affordances: sidebar trigger, breadcrumbs, search, theme, notifications (NSAD).
- **Team switcher → site switcher.** The working Site sits in the sidebar header as a
  `SidebarMenuButton size="lg"` with the product mark, two-line label and `ChevronsUpDown`
  (SA `team-switcher.tsx`); the Site is the operator's scope the way a team is in SA.
- **Search trigger** as a muted outline button with a `Kbd` chip (SA `search.tsx`); the command
  palette also finds the Site's equipment.
- **Cards** keep radix-nova `ring-1 ring-foreground/10` and gain `shadow-xs` (NSAD KPI cards).
- **KPI strip**: one bordered surface split by hairlines, label `text-sm` muted, value
  medium-weight and tight (NSAD `analytics-kpi-strip.tsx`). Already our `MetricGrid`; the two
  other KPI widgets were removed in its favour.
- **Status vocabulary**: tinted label `rounded-md px-1.5` with a still `size-1.5` dot, no border
  (NSAD infrastructure status cells). Only an active alarm keeps a pulse.
- **Header meta chips**: outline `rounded-md` chip with a dot or icon beside the title (NSAD
  infrastructure header) → `PageMeta`.
- **Line tabs** for page sections (NSAD finance), used on 系统管理.
- **Table header** `bg-muted/50`, `h-10`, roomier cells; tables sit flush inside their card.
- **Gradient area** for the selected series of tabbed charts (`chart-area-interactive`), with a
  quieter 28%→2% ramp than the template's 100%→10% — an operator reads values, not mood.

## Adapted

- **Page title** stays `font-semibold` (NSAD uses `font-medium`): CJK glyphs at medium weight
  read thin next to Geist digits.
- **Brand colour** stays the product teal for primary actions; the templates' neutral
  `primary` would erase the product identity. Charts keep the product's blue-led palette.
- **Section cards** (gradient `from-primary/5` KPI tiles) were not used for the main KPIs: on a
  dense operations page the hairline strip carries four facts with less chrome. The pattern is
  available for marketing-like summaries if one appears.

## Rejected

- **Theme presets, font switcher, layout customizer** (NSAD `LayoutControls`, SA
  `ConfigDrawer`): a configuration surface for operators adds choices without changing what
  they can do.
- **Mobile layouts and responsive breakpoints**: the console is desktop-only (decision of
  2026-10-12); pages are verified at 1440px.
- **Template data and demo pages**: pages show only real owner data (CLAUDE.md).
