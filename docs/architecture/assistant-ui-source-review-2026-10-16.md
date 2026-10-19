# assistant-ui 调研（2026-10-16）

## 看了什么

AI 运维助手面板（#577）需要一个对话界面：逐字显示的文字、一次回答里读数据的步骤、结论卡片、追问建议和「停止」按钮。看了：

- `@assistant-ui/react` 0.15.29 和 `@assistant-ui/react-markdown` 0.14.23（加 `remark-gfm` 4.0.1），在 `apps/hvac-web/package.json` 里固定确切版本；
- shadcn 组件库条目 `https://r.assistant-ui.com/thread.json` 和它带进来的组件（`markdown-text`、`tool-fallback`、`tool-group`、`reasoning`、`attachment`、`image`、`file`、`follow-up-suggestions`、`tooltip-icon-button`），用 `npx shadcn add` 加入，所有覆盖现有 `components/ui/*` 的提示都选了「不覆盖」。

也看了 ReUI 的 AI 对话组件，需要付费授权，没用。

## 决定

**改造后使用**：运行时和基础组件作为依赖引入；组件库里的界面代码复制过来，归我们自己维护。

- **运行时**：在我们自己的助手会话状态上用 `useExternalStoreRuntime`。对话保存在服务端（`operations-agent-service`）；页面把会话快照和实时事件整理成消息（`features/assistant/thread-model.ts`），再交给 assistant-ui。assistant-ui 自带的会话列表、云服务、传输和模型适配都不用。
- **保留并改了样式的**：`markdown-text.tsx`（「复制」按钮改成中文）、`tooltip-icon-button.tsx` 和 `hooks/use-copy-to-clipboard.ts`，放在 `components/assistant-ui/elements/`。
- **重写的**：对话界面本身，即 `features/assistant/components/AssistantThread.tsx`，用 assistant-ui 的基础组件（`ThreadPrimitive`、`MessagePrimitive.GroupedParts`、`ComposerPrimitive`、`ThreadPrimitive.Suggestion`）搭。读数据的步骤、结论卡片和补充信息的提问是按设计系统写的项目组件。
- **不要的**：附件、图片、文件、媒体播放、推理过程、语音和听写、编辑消息、分支、反馈和导出。面板只用平台数据回答，也不显示模型的推理，这些都用不上。`tw-shimmer` 和组件库加进 `global.css` 的样式也一起去掉了。

## 升级时

复制过来的文件没有自己的版本号。升级 `@assistant-ui/react` 时，把 `markdown-text.tsx` 和组件库里的条目对比一下，再跑 `npm run web:e2e -- assistant.spec.ts`。
