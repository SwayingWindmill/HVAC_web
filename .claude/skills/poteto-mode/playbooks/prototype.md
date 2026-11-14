### Prototype

**You own the design decision, not the code. The prototype is a throwaway instrument. The real build follows Feature.**

The one playbook where smallest-change and production polish invert: prioritize speed over polish and zero planning so you resolve design uncertainty cheaply. Propose alternatives the user didn't ask for and discard weak ones quickly.

1. Scope the exact decision the prototype exists to settle (layout, interaction flow, density, latency behavior, or library fit). If there is no open design fork, skip prototyping and route to [`feature.md`](feature.md).
2. Gather references when the design space is open (existing `DESIGN.md` patterns, `apps/hvac-web` components, or external reference patterns) before building.
3. Build throwaway in an isolated scratch directory under `.scratch/` (never inside production `apps/` or `modules/` unless a temporary sandbox branch). Use minimal HTML/CSS/JS or a tiny standalone script with zero tests or abstractions.
4. When comparing alternatives in UI, put them behind a quick toggle/switcher so the user compares variants side-by-side (`exhaust the design space`).
5. Verify on the matching surface: screenshot variants at `1440x900` in Playwright (via [`verify`](../../verify/SKILL.md) patterns) or run and log real timing/output for behavioral prototypes.
6. Present alternatives, tradeoffs, and a concrete recommendation. Keep the throwaway prototype clearly labeled as scratch, then build the chosen direction cleanly via [`feature.md`](feature.md) (or [`architect`](../../architect/SKILL.md)).

**Reply:** variants explored, visual/runtime evidence, tradeoffs, recommendation, and `.scratch/` path (noting it is throwaway).
