# Gateway revocation AlertDialog source review

Official upstream: shadcn-ui/ui, tag `shadcn@4.20.0`, peeled commit `9149c6b2afd30507c04af18ed61a3b7d4111fa27` (annotated tag object `30a863a3df25108555c1680f0628aa52129bd730`). Read the official checkout, not a mutable registry description.

Reviewed upstream files:

- `apps/v4/registry/bases/radix/ui/alert-dialog.tsx`: Root/Portal/Overlay, labelled Title/Description and explicit Action/Cancel composed with Button `asChild`.
- `apps/v4/content/docs/components/radix/alert-dialog.mdx`: interruption requiring an explicit user response, radix-nova demo and public primitive API links.
- `packages/shadcn/src/commands/add.test.ts` and `src/commands/registry/validate.test.ts`: component add orchestration and registry validation tests. These do not prove business revocation or focus behavior; the project browser test and real desktop review supply that evidence.
- `LICENSE.md`: MIT permission and notice retention; preserved at `apps/hvac-web/src/components/ui/LICENSE.shadcn` with provenance header in the copied component.

**ADOPT** official Radix semantics and the project's installed radix-nova style through pinned project CLI 4.20.0. **ADAPT** generated imports to project `cn`/Button and keep the confirmation open during an asynchronous mutation using `preventDefault`; cancellation performs no revoke request. **REJECT** Base UI variants, duplicate primitive families, hand-written modal focus management and browser `window.confirm` for permanent Gateway revocation. Registry authority and permanent impact come from #407/ADR 0015, not the component source.
