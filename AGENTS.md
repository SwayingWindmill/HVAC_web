- Do not preserve backward compatibility. Remove obsolete paths instead of
  adding compatibility layers, fallbacks, or migrations.
- Read `GLOSSARY.md` for domain terminology and the relevant `docs/adr/` decisions
  before exploring a domain. Skill configuration lives in `docs/agents/`.
- Choose the simplest implementation that fully meets the current
  requirements. Avoid speculative abstractions, configuration, and
  indirection.
- Grow the system in layers. Start from the smallest version that works end
  to end, and add each new capability on top of a product that already
  works. Never trade a working product for unfinished complexity.
- Keep components modular and concerns clearly separated.
- Prefer established, well-maintained libraries when they reduce overall
  complexity or improve reliability. Do not reimplement common
  functionality without a clear reason.
- Lean on the dependencies already in the project before writing your own
  implementation or adding packages. Do not assume a library lacks a
  capability without checking its documentation and types.
- Make architectural decisions for the long term. Do not accept a stopgap
  that only works for now and is meant to be replaced later.
- Reuse-first implementation: before introducing new infrastructure, libraries,
  or tooling, search GitHub and the existing dependency set for maintained
  solutions that satisfy the requirement; document the choice and pin the selected version or commit.
- Source-first reference implementation rule: when a target architecture decision
  is derived from an external reference implementation, pin an official upstream
  release/tag and commit, then read the relevant official source code, upstream
  tests, and official documentation before implementing or refactoring that
  concern. This rule also applies retroactively to local modules that were already
  written before the source review: existing code has no incumbency preference and
  remains UNVERIFIED until compared against the pinned reference implementation.
  Do not implement or retain an adopted mechanism from prose or architecture
  diagrams alone. Record the reviewed upstream files and the resulting
  ADOPT/ADAPT/REJECT decisions in the project architecture source-review record.
  If local behavior materially conflicts with the reference implementation and
  there is no documented, evidence-backed reason that the local behavior is safer,
  simpler, more maintainable, or required by HVAC/domain constraints, the pinned
  reference implementation behavior wins and the local code must be refactored to
  match it. Preserve project-specific differences only when that justification is
  explicit and reviewed. Do not copy upstream source verbatim unless license and
  provenance have been explicitly reviewed.
- Standing energy-platform comparison rule: ThingsBoard, OpenEMS, and MyEMS are
  mandatory reference candidates for product, Web, backend, deployment, identity,
  authorization, telemetry, alarms, assets/Registry, reporting, integration, and
  energy-management features that overlap their real capabilities. Before making
  a material implementation or refactor decision in one of those areas, inspect
  the relevant official source, tests, and documentation from the applicable
  projects and compare their actual behavior with this repository. Do not assume
  the current HVAC_web implementation is preferable merely because it already
  exists. Keep it only when the source review shows it is simpler, safer, more
  maintainable, or required by explicit HVAC/domain constraints; otherwise adapt
  the stronger established design. Record the concrete upstream files reviewed
  and the resulting ADOPT/ADAPT/REJECT decision instead of relying on reputation,
  README-level descriptions, or architectural intuition.
- No meaningless tests. Add or retain a test only when it protects a concrete
  current product contract, safety invariant, data invariant, authorization
  boundary, externally observable behavior, or a previously observed realistic
  regression. Do not add tests merely for coverage, trivial getters/setters,
  implementation details, duplicate permutations, exhaustive fixture combinations,
  obsolete compatibility behavior, or assertions that cannot catch a meaningful
  product failure. When a contract changes, update or delete stale tests instead of
  adding compatibility code to satisfy them. Prefer the smallest direct behavioral
  test set that proves the required behavior.
- No speculative defensive programming. Add a validation, guard, fallback, retry,
  default, coercion, recovery branch, or exception handler only for a concrete
  reachable failure mode at a real trust boundary, external I/O boundary,
  persistence-corruption boundary, concurrency boundary, or safety-critical
  invariant. Do not defend against impossible states already excluded by trusted
  types, schemas, database constraints, or an upstream owner contract. Do not add
  redundant validation at every layer, silent fallbacks, catch-and-ignore logic,
  permissive defaults, or "just in case" branches. Prefer a clear failure over
  masking an invalid state. Every defensive branch must have an identifiable
  failure mode that justifies its existence; otherwise remove it.
- No gate inflation. Protect each current architectural/product invariant with the
  smallest authoritative gate that can fail for a meaningful reason. Do not add
  a new permanent CI gate, package script chain, snapshot gate, or ticket/stage
  gate when an existing domain/invariant gate can own the check. Temporary
  certification or migration gates must be removed or folded into the stable
  domain task matrix after their purpose is complete. Avoid running unrelated
  domains "just in case"; affected-path classification should select only the
  capabilities whose contracts can actually change.
- Frontend product-design workflow: for material operator-facing UI work, first load
  durable context in this order: `PRODUCT.md`, `docs/product/smart-energy-system-page-architecture-v3-research-backed.md`,
  `docs/product/global-navigation-context-interaction-contract-v2.md`, then the matching current spec under `docs/product/surface-specifications/`, then `DESIGN.md`. Page-specific specs must be regenerated from those authorities rather than inherited from historical surfaces. The audited v1 blueprint, old
  `docs/product/product-information-architecture-v2.md` and existing `docs/product/surfaces/*.md` set are
  historical planning artifacts and must not constrain the greenfield page system. Only after that load a
  matching `.impeccable/surfaces/*.md` contract when it has been explicitly refreshed for the
  current product blueprint. Historical surface contracts do not override the new page map. The
  2026-09-13 redesign selected **Shadcn Application System**; read
  `docs/design-system/shadcn-redesign-2026-09-13.md` for the active decision,
  `docs/design-system/hvac-application-ui-specification.md` for Breadcrumb/Page Header,
  Surface archetypes, tablecn/dashboardcn boundaries, and the ReUI/Kibo UI/Dice UI advanced-component layer,
  plus `docs/design-system/shadcn-component-contract.md` for component selection/composition.
  Use `docs/architecture/shadcn-tablecn-reui-source-review-2026-09-20.md` for tablecn/ReUI,
  `docs/architecture/kibo-diceui-advanced-components-source-review-2026-09-20.md` for Kibo/Dice,
  `docs/architecture/dashboardcn-source-review-2026-09-24.md` for dashboard analytics components, and
  `docs/architecture/shadcnblocks-source-review-2026-09-22.md` for Shadcnblocks before adopting upstream source. Then read `docs/design-system/legacy-ui-quarantine.md` before any repository-wide visual
  search or incumbent-page inspection. Files and paths listed there are migration/history
  sources only: do not use their page anatomy, component composition, screenshots, CSS,
  spacing, palette, KPI arrangement, or interaction structure as design evidence for a new
  Surface. Superseded Control Desk / Ant-era design records should be removed rather than retained as active-tree visual references; use Git history if archaeology is explicitly needed. Implement the
  shadcn/ui + satnaing/shadcn-admin + tablecn + ReUI/Kibo UI/Dice UI + dashboardcn + Shadcnblocks selection grammar directly;
  do not preserve Control Desk v1, old Ant/ProComponents composition, old boxed Ant PageHeader,
  fixed-workspace, card-wall, Drawer/Splitter progression, or any other visual
  compatibility layer. Next load the skills: the `impeccable` Claude Code plugin (pbakaus/impeccable,
  installed from its marketplace) as the primary product-interface workflow, `.agents/skills/frontend-design/SKILL.md` for visual
  craft, and `.agents/skills/web-design-guidelines/SKILL.md` for the final Web UX/a11y
  review. Within Impeccable, route the work deliberately: `critique` an incumbent
  surface, `shape` when information responsibility is wrong, `distill` before adding
  visual treatment, use `layout` / `typeset` / `quieter` for targeted refinement,
  then `harden` / `audit` / `polish` as appropriate. The user does not need to name
  these commands when the intent is clear. See
  `docs/architecture/frontend-design-skill-source-review.md` for provenance.
- Impeccable detector integration is advisory evidence, not a new product authority
  or blanket CI gate. After a meaningful Web UI batch, run the narrowest applicable
  detector, assess findings against product truth, the currently selected design
  direction and `DESIGN.md`, and fix only real defects. Detector success never
  substitutes for real browser visual review or focused product tests.
- Frontend reference rule: only references explicitly approved for the current design
  direction can be visual authority. For the active 36-Surface → 10-Workspace consolidation,
  the completed 36-Surface system has now been explicitly re-approved as the design mother
  library: use the current `docs/product/surface-specifications/*.md` plus any PROMOTED /
  BROWSER REVIEWED visual-reframe records to inherit page archetype, information hierarchy,
  table/detail/inspector grammar, status semantics, and professional actions. Consolidation
  changes IA, navigation level, and responsibility ownership; it does not silently redesign
  an already accepted Surface. Later cross-Surface standards such as DataTableBlock +
  tablecn/TanStack and the current shadcn application shell layer on top of that inheritance.
  The much earlier Ant / ProComponents / Control Desk pages, quarantined legacy screenshots,
  CSS and historical image packages remain business-anatomy / migration evidence only and
  are not re-approved. Do not treat the current implementation, an arbitrary previous
  screenshot, shadcn-admin, or another external product as design authority merely because
  it already exists.
- Operator-visible business-facts rule: operator-facing production UI must show only
  business facts, states, impact, time, reasons, responsibility, and executable actions
  needed for the current task. Internal tracking and implementation fields such as UUIDs,
  trace/correlation IDs, resourceId/actorId/sourceReference, schema or contract names,
  revision/digest/terminal codes, internal node/definition IDs, raw technical enums, and
  value-object versions must stay in data models, URLs, requests, logs, audit storage, and
  developer tools rather than the main operator UI. Translate internal values into human
  business language when they have product meaning, and never fabricate business IDs by
  truncating or decorating internal UUIDs. This is a standing rule across Dashboard,
  Device Center, Alarm Center, Work Orders, Energy, Control, FDD, and future operator
  surfaces.
- Repository runtime authority: the primary working directory is `E:\Code\HVAC_web`, accessed by WSL as `/mnt/e/Code/HVAC_web`. Edit, install dependencies, build, test, and run this same checkout; do not switch to or synchronize from the old `/home/haozhang/code/HVAC_web` copy. Local development, CI, Node/npm, Go, Docker, browser automation, code generation, type checking, tests, and builds remain Linux-authoritative: on Windows hosts, run project commands through WSL 2 in `/mnt/e/Code/HVAC_web`. Windows Node/npm, Git Bash as a project shell, `.cmd`/`.exe` tool fallbacks, `process.platform === 'win32'` compatibility branches, Windows-only CI runners, and Windows browser/runtime fallbacks are not supported and must not be introduced. Windows desktop tooling may edit the primary directory directly; project commands execute in Linux.
- Frontend architecture authority: `docs/architecture/smart-energy-react-spa-frontend-architecture.md`
  is the implementation architecture baseline. The Web stack is React 19 + Vite +
  TypeScript + Tailwind CSS + shadcn/ui + TanStack Router + TanStack Query + TanStack
  Table v9 + React Hook Form + Zod + Zustand only when shared client state is proven +
  Recharts through shadcn Chart for ordinary application charts + Apache ECharts for
  engineering analytics + date-fns + WebSocket + Vitest + Playwright. tablecn is the
  default ledger composition reference; ReUI, Kibo UI and Dice UI are approved copy-and-own
  advanced application-component sources above shadcn/ui; dashboardcn is the approved focused
  dashboard/analytics composition source; Shadcnblocks is an approved application-block/composed-pattern source. None creates a second primitive
  framework or parallel implementation family. Ant Design,
  ProComponents, Ant Design Charts, React Router, and legacy compatibility abstractions
  are migration sources only and must not be used for new implementation.
- Frontend state boundaries are mandatory: URL/search state belongs to TanStack Router;
  server state belongs to TanStack Query; form state belongs to React Hook Form; local
  UI state defaults to `useState`/`useReducer`; Zustand is reserved for genuine shared
  client-only state; realtime transport/state belongs to the realtime layer. Do not
  mirror one fact into multiple state systems.
- Frontend feature boundary rule: routes remain thin and own URL parsing, route metadata,
  auth/permission navigation checks, and route loading/error composition. Business logic,
  query options, mutations, schemas, and domain UI belong to `features/<domain>`. Do not
  add `pages/`, repositories, abstract API services, BaseCRUD/BaseFeature hierarchies,
  universal stores, or global event buses unless two concrete current use cases prove the
  abstraction is necessary.
- Frontend infrastructure rule: page/feature components do not call `fetch` directly.
  OpenAPI-generated clients are infrastructure; feature query/mutation code is the
  application-facing API boundary. API DTOs come from OpenAPI; hand-written TypeScript
  models are only for real view/domain models. Realtime pages use Snapshot + Stream and
  business components never create raw WebSocket connections.
- Frontend UI rule: shadcn/ui provides standard primitives; project code builds only
  energy-domain components and composed product patterns. `apps/hvac-web/components.json`
  currently selects `radix-nova`: keep Radix `asChild` composition, do not mix Base UI
  `render` APIs, and use the unified `radix-ui` package inside touched UI primitives.
  Feature code imports project `components/ui`, not primitive-library internals. Ordinary
  operational ledgers follow sadmann7/tablecn interaction/composition grammar on the
  project TanStack Table v9-native layer. ReUI, Kibo UI and Dice UI form the copy-and-own
  advanced application-component layer; dashboardcn is the focused analytics-composition source; Shadcnblocks is the approved application-block layer.
  Use Radix variants for ReUI/Dice UI, source-review
  Kibo component dependencies before adoption, and select only one implementation when
  capabilities overlap. The installed upstream ReUI skill is workflow guidance, not project
  authority: its generic recommendation to use ReUI Data Grid for sortable/filterable tables
  is overridden here for this product. Ordinary operational ledgers remain tablecn + the
  project TanStack Table v9 layer; ReUI Data Grid is allowed only for explicitly advanced
  spreadsheet/virtualized/tree-grid tasks. Do not let any source introduce duplicate
  Button/Select/Dialog/Table systems. Follow
  `docs/design-system/hvac-application-ui-specification.md` and
  `docs/design-system/shadcn-component-contract.md` for Page Header, Data Table/Data Grid,
  Field/Input Group, Card, Tabs, Dialog/AlertDialog/Sheet and detail-lifetime choices.
  Do not create compatibility wrappers that switch between Ant Design and shadcn, and do
  not preserve obsolete Ant component APIs. Lucide remains the single business icon
  vocabulary. Ordinary application charts use shadcn Chart + Recharts; high-density HVAC
  time-series, multi-axis, dataZoom/brush and other engineering analysis use Apache ECharts.
  X6/G6 may remain only where fixed engineering topology or relationship graphs genuinely
  require them.
- Frontend complexity rule: directories and abstractions must describe complexity that
  already exists. Do not pre-create empty `stores/`, `hooks/`, `schemas/`, `services/`,
  `repositories/`, `guards/`, or `bootstrap/` hierarchies. Query-key factories, ring
  buffers, runtime config, reference counting, advanced reconnect/replay, and complex
  permission machinery are introduced only when current requirements need them.
- Visual completion requires rendered evidence. After a meaningful frontend batch,
  inspect the actual page at the target desktop viewport and compare hierarchy, geometry,
  density, state emphasis and interaction against the currently approved design direction
  or current-surface reference when one exists. Legacy screenshots are not comparison
  targets during Design Reset unless explicitly re-approved. Batch visible defects, fix
  them, and perform at most one confirming visual pass unless the user asks for further
  iteration or a real regression remains. A green build alone is not visual approval.
- Mobile frontend review is out of scope by default for this project. Do not add or run
  mobile-specific visual, responsive, browser, DOM-geometry, overflow, or interaction
  checks unless the user explicitly asks for mobile support or names a mobile viewport.
  Frontend implementation and acceptance should target the approved desktop/reference
  viewport and any other non-mobile viewport explicitly required by the task. Do not
  treat generic responsive guidance from libraries, skills, or audit tools as authority
  to introduce mobile acceptance work on its own.
