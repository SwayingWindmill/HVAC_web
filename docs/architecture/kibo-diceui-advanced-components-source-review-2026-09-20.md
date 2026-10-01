# Kibo UI + Dice UI Advanced Application Component Source Review — 2026-09-20

> **Status:** SELECTED / ACTIVE SOURCE REVIEW  
> **Scope:** `apps/hvac-web` advanced shadcn-compatible application components  
> **Related authorities:** `DESIGN.md`, `docs/design-system/hvac-application-ui-specification.md`, `docs/design-system/shadcn-component-contract.md`

## 1. Decision

Kibo UI and Dice UI are approved as **advanced application component sources above shadcn/ui**.

They join ReUI in the complex application-component layer:

```text
shadcn/ui
  = primitive / token / accessibility baseline

tablecn
  = specialized operational Data Table / ledger grammar

ReUI + Kibo UI + Dice UI
  = advanced application components and composed interaction patterns

project domain components
  = HVAC / energy-specific semantics
```

This is not permission to run multiple competing primitive systems. shadcn/ui remains the primitive authority.

## 2. Sources reviewed

### Kibo UI

Reviewed:

- https://github.com/shadcnblocks/kibo
- https://www.kibo-ui.com/docs
- https://www.kibo-ui.com/docs/setup
- https://www.kibo-ui.com/docs/usage
- https://www.kibo-ui.com/components/gantt
- https://www.kibo-ui.com/components/editor
- https://www.kibo-ui.com/components/table

Kibo UI describes itself as a custom registry of composable, accessible, extensible components designed for shadcn/ui. It uses shadcn CSS variables and copy-and-own source, and focuses on richer components that wrap more complex logic/headless libraries.

Useful candidate capabilities for this project include:

- Gantt / scheduling;
- Calendar;
- rich text Editor;
- Dropzone / file workflow;
- code / content presentation;
- richer application blocks;
- other complex controls not provided by base shadcn/ui.

Kibo UI also ships a Table, but **Kibo Table is not a replacement for the project operational ledger architecture**. Ordinary Devices / Alarms / Work Orders / Audit ledgers continue to use tablecn grammar + the project TanStack Table v9 layer.

Kibo currently documents React 18+ and shadcn CSS-variable theming, which are compatible with this project's React 19 and CSS-variable theme baseline. Exact component dependencies still require component-level source review before adoption.

### Dice UI

Reviewed:

- https://github.com/sadmann7/diceui
- https://diceui.com/docs/introduction
- https://diceui.com/docs/components
- https://github.com/sadmann7/diceui/blob/main/CONTRIBUTING.md
- https://diceui.com/docs/components/base/kanban
- https://diceui.com/docs/components/base/sortable
- https://diceui.com/docs/components/base/editable
- https://diceui.com/docs/components/base/tour

Dice UI is a copy-paste shadcn-compatible component collection focused on composable, accessible advanced interactions. Its current catalog includes components such as:

- Editable;
- File Upload;
- Kanban;
- Sortable;
- Timeline;
- Tour;
- Selection Toolbar;
- Action Bar;
- Tags Input;
- Mention;
- Media Player;
- Cropper;
- Color Picker;
- Time Picker;
- Stepper;
- Listbox;
- Responsive Dialog.

Dice UI maintains separate Radix and Base UI registry paths. Because this project uses `radix-nova`, **Dice UI adoption must use the Radix source path** unless the project later makes an explicit primitive-base decision.

## 3. ADOPT

### 3.1 Kibo UI

Adopt Kibo UI as a preferred source for functionally rich components that are expensive or error-prone to recreate locally, especially:

- scheduling / Gantt;
- rich content editing;
- dropzone/file interaction;
- calendar-like composed views;
- complex application blocks.

### 3.2 Dice UI

Adopt Dice UI as a preferred source for advanced interaction primitives/composites, especially:

- accessible drag-and-drop / Sortable;
- Kanban;
- inline Editable;
- selection/action toolbars;
- guided Tour;
- tags/mention/listbox inputs;
- rich file/media interaction;
- advanced small controls not present in shadcn/ui.

### 3.3 Copy-and-own model

Both projects are used in the shadcn registry spirit:

1. inspect the exact upstream source;
2. pin the reviewed tag/commit;
3. copy only the needed component and its real dependencies;
4. align it to project tokens and Radix composition;
5. keep the source locally owned;
6. remove competing local implementations rather than adding compatibility wrappers.

## 4. ADAPT

### 4.1 Overlapping components

Where multiple advanced libraries expose the same capability, selection is **per capability**, not by library preference.

Evaluate:

```text
interaction semantics
→ accessibility / keyboard model
→ dependency weight
→ source clarity / tests
→ compatibility with radix-nova
→ fit with project state ownership
→ visual/token fit
```

Then select one implementation for the project task.

Examples:

- Kanban may exist in ReUI and Dice UI;
- Gantt may exist in ReUI and Kibo UI;
- Table may exist in Kibo UI while tablecn owns the normal operational ledger;
- Timeline/Stepper may exist across several sources.

Do not keep two generic implementations for the same project role.

### 4.2 Primitive-base compatibility

Dice UI explicitly supports Radix and Base UI variants; use **Radix**.

Kibo UI is shadcn-compatible and may wrap additional headless libraries. Before adoption, inspect the selected source to ensure it does not introduce incompatible Base UI composition or duplicate project primitives.

### 4.3 State ownership

Advanced components do not change project state boundaries:

- shareable filter/view state → TanStack Router search params;
- server data → TanStack Query;
- form state → React Hook Form;
- local transient interaction → local React state;
- shared client-only state → Zustand only when proven necessary.

A third-party component may own internal interaction mechanics, but not duplicate authoritative application state.

## 5. REJECT

Reject:

- using Kibo/Dice examples as product IA;
- introducing a second Button / Select / Dialog / Popover / Tooltip family;
- replacing tablecn operational ledgers with Kibo Table because it is visually richer;
- using Base UI Dice components in the current Radix application;
- installing an entire component suite when only one component is needed;
- compatibility wrappers switching between ReUI / Kibo / Dice implementations;
- parallel generic Kanban/Gantt/Timeline implementations with the same product responsibility;
- copying unreviewed `main` source into production.

## 6. Selection ladder

The project selection ladder is now:

```text
1. Existing project component
2. shadcn/ui primitive
3. shadcn official block
4. tablecn/project DataTable layer when tabular
5. approved project domain component
6. Advanced application layer:
   - ReUI
   - Kibo UI
   - Dice UI
7. Small project-owned composition
8. Custom component only when the above cannot satisfy the task
```

The order inside step 6 is not a ranking. Choose by task fit and reviewed source quality.

## 7. Source pin policy

This document approves architectural participation only.

Before production adoption of a Kibo UI or Dice UI component, the implementation change must record:

- exact upstream repository;
- exact tag or commit;
- exact source files reviewed;
- relevant upstream tests/docs reviewed;
- dependencies introduced;
- ADOPT / ADAPT / REJECT rationale;
- why the selected implementation wins over overlapping ReUI/Kibo/Dice candidates.

No component is considered adopted merely because the project is listed in this source review.
