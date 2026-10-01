# AntV X6 / G6 graph visualization source review

## Scope

This record governs graph-engine choices for HVAC Web operator-facing topology, anomaly-path, energy-flow, and relationship-analysis diagrams.

## Selected versions

- `@antv/x6@3.1.8`
- `@antv/x6-react-shape@3.0.1`
- `@antv/g6@5.1.1`

These versions are intentionally pinned in the repository instead of floating to future minor releases without review.

## Upstream sources reviewed

### AntV G6 5.1.1

- Repository: `https://github.com/antvis/G6`, v5 branch.
- Public package/docs: `@antv/g6` 5.1.1.
- Reviewed repository structure: `packages/g6/src/` and `packages/g6/__tests__/`.
- Relevant source areas reviewed:
  - `packages/g6/src/runtime/` — Graph runtime/data/update lifecycle.
  - `packages/g6/src/elements/` — node/edge element implementation model.
  - `packages/g6/src/behaviors/` — interaction behavior model.
  - `packages/g6/src/layouts/` — graph layout registration/runtime.
  - `packages/g6/__tests__/unit/` — unit coverage for graph behavior/runtime.
  - `packages/g6/__tests__/demos/` and snapshots — rendered graph behavior/examples.
- Relevant official documentation reviewed:
  - Graph options and GraphData/NodeData/EdgeData.
  - Node dynamic style callbacks and labels.
  - Event constants / `NodeEvent.CLICK` and graph element data lookup.
  - Behavior system (`click-select`, hover, canvas interactions).
  - Dagre / AntV Dagre directed-graph layout.

Observed upstream issue relevant to restraint: current G6 5.1.1 has an open 2026 issue around `AutoAdaptLabel` first-render behavior/performance. HVAC Web therefore does not enable that behavior for the small deterministic energy graphs implemented here.

### AntV X6 3.1.8 / React Shape 3.0.1

- Official X6 documentation for React nodes and graph/node/edge interaction was reviewed before adopting the topology implementation.
- X6 is retained only for deterministic engineering topology where fixed placement, React equipment nodes, edge routing, selection, and device activation are primary.

## ADOPT / ADAPT / REJECT

### G6 — ADOPT for relationship/flow graphs

Adopt:

- GraphData as explicit nodes + directed edges.
- Built-in event model using official event constants rather than DOM guessing.
- Dynamic element styles from business data.
- Fixed/manual layout for small reference-driven HVAC energy-flow diagrams; Dagre remains available for future variable-size directed graphs.
- Canvas rendering for relation-centric operator diagrams where graph interaction is more important than chart axes.

Adapt:

- Use current project semantic CSS variables / shadcn theme tokens to derive node/edge colors rather than G6 built-in palettes/themes.
- Keep animations minimal/off on high-frequency operating surfaces.
- Disable drag/edit behaviors for read-oriented energy-flow views.
- Encode quantitative flow in edge width only when the input value is verified; never split aggregate cooling into regional values without an owner-backed allocation.
- Expose business actions outside the graph through project `components/ui` controls when keyboard/focus clarity is stronger than canvas-only interaction.

Reject:

- Force-directed layouts for fixed HVAC process diagrams; they destroy operator spatial memory.
- Decorative graph motion, graph editing, lasso/brush selection, auto-adapt-label, or WebGL merely because G6 supports them.
- Replacing normal shadcn Chart / Recharts or ECharts statistical visualizations with G6.

### X6 — ADOPT for deterministic HVAC topology

Adopt:

- React custom equipment nodes.
- Fixed reference-driven positions and explicit pipe edges.
- Node activation → device Drawer / cold-source / terminal route.
- Same topology foundation reused by normal and anomaly views.

Adapt:

- Read-only topology: equipment cannot be freely dragged or rewired by operators.
- Selection/fault state comes from real business state and alarm facts.
- Theme treatment follows HVAC Web/Ant tokens rather than a separate X6 visual theme.

Reject:

- Using X6 as a generic charting system.
- Topology editing interactions in the monitoring workspace.

## Engine-selection rule

- X6: fixed engineering topology / equipment process canvas.
- G6: relation/flow network where node-edge exploration is the main task.
- Ant Design Charts / AntV G2: quantitative statistical charts with axes, legends, series, annotations, and native statistical forms such as time series, scatter, column, and dual-axis charts.

This is an interaction-model decision, not a preference for one AntV library over another.
