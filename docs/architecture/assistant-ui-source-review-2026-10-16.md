# assistant-ui Source Review — 2026-10-16

## Scope

The AI 运维助手 panel (#577) needs a chat thread: streamed text, the Tool steps of a Run, a finding, follow-up questions and stop. Reviewed:

- `@assistant-ui/react` 0.15.29 and `@assistant-ui/react-markdown` 0.14.23 (with `remark-gfm` 4.0.1), pinned exactly in `apps/hvac-web/package.json`;
- the shadcn registry item `https://r.assistant-ui.com/thread.json` and what it pulls in (`markdown-text`, `tool-fallback`, `tool-group`, `reasoning`, `attachment`, `image`, `file`, `follow-up-suggestions`, `tooltip-icon-button`), added with `npx shadcn add`, declining every overwrite of existing `components/ui/*`.

ReUI's AI chat blocks were also looked at; they need a paid licence, so they were not used.

## Decision

**ADAPT.** The runtime and primitives are adopted as a dependency; the registry UI is copied and owned.

- **Runtime:** `useExternalStoreRuntime` over our own Agent Session state. The server keeps the conversation (`operations-agent-service`); the page folds the Session snapshot and its live events into messages (`features/assistant/thread-model.ts`) and hands them to assistant-ui. assistant-ui's own thread list, cloud, transport and model adapters are not used.
- **Kept from the registry, restyled:** `markdown-text.tsx` (Chinese copy label), `tooltip-icon-button.tsx` and `hooks/use-copy-to-clipboard.ts`, under `components/assistant-ui/elements/`.
- **Rewritten:** the thread itself, as `features/assistant/components/AssistantThread.tsx`, on assistant-ui primitives (`ThreadPrimitive`, `MessagePrimitive.GroupedParts`, `ComposerPrimitive`, `ThreadPrimitive.Suggestion`). Tool steps, the finding card and the input request are project components in the design system.
- **Rejected:** attachments, images, files, media player, reasoning display, voice and dictation, message editing, branches, feedback and export. The panel answers from platform data only and the model's reasoning is not shown, so none of them has a use. `tw-shimmer` and the registry's `global.css` additions went with them.

## Upgrading

The copied files carry no registry version of their own; on an upgrade of `@assistant-ui/react`, compare `markdown-text.tsx` with the registry item and rerun `npm run web:e2e -- assistant.spec.ts`.
