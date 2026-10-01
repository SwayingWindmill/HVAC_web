# Dice UI Action Bar Source Review — 2026-09-24

## Scope

Reviewed source: `@diceui/action-bar` for raw Alarm multi-selection actions in the Issues Workspace.

Commands reviewed:

```text
npm run ui:shadcn -- view @diceui/action-bar
npm run ui:shadcn -- add @diceui/action-bar --dry-run
```

Dry-run impact:

- adds `src/components/ui/action-bar.tsx`;
- adds `use-as-ref` and compose-ref helpers;
- proposes overwriting the project's existing `use-isomorphic-layout-effect`;
- proposes `cn` and `radix-ui` dependencies even though equivalent project infrastructure already exists.

## Decision

**ADAPT / copy-and-own**, not direct install.

Project-owned adapted files:

```text
apps/hvac-web/src/components/ui/action-bar.tsx
apps/hvac-web/src/hooks/use-as-ref.ts
apps/hvac-web/src/lib/compose-refs.ts
```

The adaptation keeps Dice UI's useful interaction model: portal positioning, Escape close, focus management and arrow/Home/End keyboard movement, while reusing the project's existing Button, Radix runtime, `cn` and isomorphic layout-effect.

## Product boundary

The Action Bar is approved only for **raw Alarm multi-selection**, not for the grouped problem queue.

Approved target actions are:

- 批量确认；
- 批量指派；
- 批量搁置；
- 导出所选。

However, the bar must not be shown merely to advertise unavailable actions. In the current frontend build it remains unmounted because only export is immediately complete; a floating multi-select workflow for one export action adds more complexity than value.

When real bulk commands become available, the Action Bar may be activated without redesign. The frontend must call one authoritative bulk command per user operation and must not loop over single-Alarm mutations to simulate bulk handling.

Required backend command behavior:

- per-item result for partial failure;
- idempotency;
- authorization evaluated for the selected scope;
- audit evidence;
- stable semantics for mixed-state selections.

## UI language

Operator-facing labels must remain plain Chinese. Do not expose terms such as capability intersection, bulk mutation, selection model or ActionBar in the page.