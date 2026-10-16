# Cube Views Source Review — 2026-10-17

## Scope

#571 makes views Cube's only public surface (ADR 0023). Checked against the pinned image
`cubejs/cube:v1.6.51` (schema-compiler 1.6.51) and the current docs
(docs.cube.dev: data access policies, view reference).

## Findings

- **Views and hidden cubes.** A view with `cubes: [{ join_path, includes }]` exposes members of a
  `public: false` cube; querying the cube's members directly is rejected. Verified in
  `scripts/run-analytics-cube-tests.mjs` against the real image.
- **Access policies on views.** For a view, only the view's member-level rules apply, while
  row-level filters of the view and of the underlying cubes are combined. The row filters
  (`tenant_id`, `site_id`) must be members the view includes.
- **`default_filters` on views.** Present in 1.6.51 (`CubeValidator` `defaultFilters`,
  `CubeEvaluator.prepareViewFilters`), resolved for the Rust planner's filter dispatch.
- **Measures in views.** A view only re-exposes cube members; a calculated measure such as COP
  is defined on a cube (`type: number`, referencing two measures) and included by the view.

## Decision

- **ADOPT** views as the public surface, cubes `public: false`, access policies on the views.
- **ADAPT** the scope split: `site_energy_facts` and `equipment_energy_facts` extend one internal
  `energy_facts` cube and fix `accounting_scope` in their own SQL. This holds whichever SQL
  planner Cube uses.
- **REJECT** `default_filters` for the scope split: the scope is a fact of the data, not a
  default, and in 1.6.51 the filter path is tied to the Rust planner, which this deployment
  does not enable.
- COP lives on `site_energy_facts` only: cooling is measured for the Site, not per sub-meter.
