# Shadcnblocks Source Review — 2026-09-22

> **Status:** SELECTED / APPLICATION-BLOCK SOURCE
> **Scope:** `apps/hvac-web`
> **Role:** optional shadcn-compatible application block and composition source above the primitive layer

## 1. Decision

Shadcnblocks is approved as an additional **application Block / composed-pattern source**.

It does not change the existing ownership model:

```text
shadcn/ui
  = primitive authority

tablecn
  = default operational ledger grammar

ReUI + Kibo UI + Dice UI
  = advanced interaction/application components

Shadcnblocks
  = application blocks and composed page sections

project blocks
  = small stable HVAC/product compositions

features
  = business semantics and workflow
```

Shadcnblocks must not introduce a second Button / Badge / Dialog / Table primitive family and must not replace the project's tablecn + TanStack Table v9 operational ledger.

## 2. Registry evidence

Official documentation currently publishes the registry configuration:

```json
{
  "registries": {
    "@shadcnblocks": "https://www.shadcnblocks.com/r/{style}/{name}"
  }
}
```

and documents installation with the official shadcn CLI:

```bash
npx shadcn@latest add @shadcnblocks/hero1
```

Project verification on 2026-09-22:

```bash
npx shadcn@latest add @shadcnblocks/hero1 --dry-run
```

resolved successfully under the project's `radix-nova` configuration and proposed:

- overwrite of the existing shadcn Badge and Button dependencies;
- creation of the requested block source;
- no runtime package framework.

Therefore Shadcnblocks follows the expected copy-and-own registry model.

The registry does **not** currently expose the generic shadcn CLI registry search endpoint used by `shadcn search @registry -q ...`; discovery should therefore happen on the Shadcnblocks catalog/site, followed by exact-item `add --dry-run --diff` verification.

## 3. Licensing boundary

Shadcnblocks includes both accessible and paid catalog content. Paid/pro content requires an account/API key and is subject to the Shadcnblocks commercial license.

Project rule:

- do not add secrets or API keys to repository configuration;
- do not copy paid/pro source unless the organization has valid access for that item;
- the registry entry may remain unauthenticated for public items;
- if authenticated access is later configured, the key stays in environment/secrets management, never in source control.

## 4. Preferred use

Evaluate Shadcnblocks for:

- dashboard/application section composition;
- application-shell composition references;
- chart groups;
- structured empty/content sections;
- complex card/list arrangements;
- page-level blocks where official shadcn blocks are too limited and an approved advanced component is unnecessary.

## 5. Do not use it for

Reject Shadcnblocks when it would:

- replace an existing official shadcn primitive with a styled duplicate;
- replace the tablecn operational DataTable stack;
- introduce a marketing/landing-page visual grammar into operator surfaces without product justification;
- copy an entire template or admin kit as HVAC IA;
- import decorative gradients, card walls, oversized hero anatomy, or other patterns that conflict with the product Surface Specification;
- create a second implementation for a capability already selected from ReUI/Kibo/Dice.

## 6. Adoption workflow

For every adopted Shadcnblocks item:

1. identify the exact item from the catalog;
2. run `npx shadcn@latest add @shadcnblocks/<item> --dry-run`;
3. inspect every proposed primitive overwrite;
4. run `--diff` for the requested block and affected project primitives;
5. reject primitive overwrites unless separately justified;
6. copy/adapt only the block source and necessary dependencies;
7. align copy, layout, tokens and business semantics with the current Surface Specification;
8. record the exact item and access/license status in the change.

## 7. Selection boundary

The project reuse ladder is now:

```text
existing project component
→ official shadcn primitive / official block
→ tablecn for operational ledger
→ approved domain component
→ ReUI / Kibo UI / Dice UI for advanced interaction
→ Shadcnblocks for composed application blocks
→ small project-owned composition
→ custom component only when the above cannot satisfy the task
```

This order is a responsibility boundary, not a visual ranking. When sources overlap, choose one implementation by semantic fit, accessibility, dependency weight, Radix compatibility and source quality.
