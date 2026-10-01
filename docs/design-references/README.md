# Design references

`DESIGN.md` at the repository root is the only authoritative product design guide.

Files under this directory are **reference-only**. They may inform information density, hierarchy, navigation, motion restraint, and interaction polish, but they must not replace the泉来禾 product semantics, the current Shadcn Application System, or the rules in the root guide. They must not be combined with historical Ant Design / ProComponents page anatomy. Before using any reference, apply `docs/design-system/legacy-ui-quarantine.md`; quarantined screenshots and historical UI packages are not visual authority unless the user explicitly re-approves that specific reference.

## Linear

`linear/DESIGN.md` was retrieved with:

```bash
npx getdesign@latest add linear.app --out docs/design-references/linear/DESIGN.md
```

Use it selectively for compact navigation, single-line information hierarchy, subtle borders, restrained hover states, and keyboard-friendly interaction feedback. Do not copy its lavender brand color, dark marketing canvas, typography identity, or project-management terminology into the product.
