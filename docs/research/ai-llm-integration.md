# AI 中心 LLM 接入方案调研（#17）

> Part of #13 · 调研 `/ai` LLM 对话助手的接入方案
> 调研日期：2026-07-08 · 星标数据取自当日 GitHub

## 1. 背景与硬约束

| 项 | 现状 | 对方案的含义 |
|---|---|---|
| `/ai` 定位 | **只读 LLM 对话助手**，不触发任何设备下发（守住「人不下发」红线） | 端点绝不能暴露写/命令通道 |
| 前端栈 | React 18 + Vite + TS + **AntD v5**（非 Next.js，非 shadcn/Tailwind） | 聊天 UI 库必须框架无关、不与 AntD 打架 |
| 后端 | NestJS `hvac-backend`（可加 `/ai/chat` 端点） | 端点由后端持有 LLM key 最安全 |
| 用户原则 | 优先成熟模块、不重复造轮子；mock-first（`USE_MOCK`） | v1 用 mock 端点，真实 LLM 仅后端切换 |
| 地图 #13 范围 | `/ai` v1 基于 mock-first 验证 | 调研先定**契约**，实现按 mock 走 |

## 2. GitHub 成熟项目扫描

### 2.1 前端聊天 UI 库（直接决定 /ai 页面怎么写）

| 项目 | ★ | 适配度 | 结论 |
|---|---|---|---|
| **vercel/ai** | 25.4k | `useChat` 客户端 hook **框架无关**，可对接任意 SSE 端点；不绑定 Next.js | ✅ **首选** |
| **nlkitai/nlux** | 1.4k | 轻量、adapter 模式（可接自定义后端）、React/Next/纯 JS 通吃 | ✅ 轻量备选（依赖更小） |
| assistant-ui/assistant-ui | 11.0k | 假设 shadcn/Tailwind 组件体系，**与 AntD 设计系统冲突** | ⚠️ 不推荐 |
| langchain-ai/langchainjs | 17.9k | 是 agent 框架不是 UI；含 tool/function calling，**正是红线风险源** | ❌ 过度 + 有红线风险 |

### 2.2 自托管 LLM 应用平台（若不想自己写 LLM 胶水 / 需要 RAG）

| 项目 | ★ | 说明 |
|---|---|---|
| langgenius/dify | 148k | 生产级 agent 工作流 + RAG 知识库 + 对外 chat API |
| open-webui/open-webui | 144.7k | 友好 AI 界面（Ollama / OpenAI API 兼容） |
| ChatGPTNextWeb/NextChat | 88.4k | 轻量快，一键部署 |
| lobehub/lobe-chat | 79.6k | 多 provider 的 AI 客户端，可自托管 |
| FlowiseAI/Flowise | 54.4k | 可视化 LLM agent / flow |

### 2.3 后端接 LLM（NestJS 侧）

- **官方 provider SDK**（OpenAI SDK / Anthropic SDK / DeepSeek 兼容 OpenAI 协议）：最成熟、最少意外，NestJS 里直接 `import` 调用即可。
- Vercel AI SDK 也可在 Node 侧用（provider-agnostic），但与 NestJS 集成需手写 controller 转发 SSE，性价比不如直接用官方 SDK。

## 3. 三个核心问题的回答

### Q1：v1 用哪种 LLM 端点？

**推荐：后端代理端点 `POST /api/v1/ai/chat`（SSE 流式）**，由 `hvac-backend` 服务端调用 OpenAI 兼容 provider（DeepSeek / OpenAI / Anthropic）。

理由：
1. **API key 留在服务端**，前端永远不碰 key（安全基线）。
2. 端点**只做 chat**，从根上保证只读（见 Q3）。
3. 与现有 `REST_BASE=/api/v1` + `X-Site-Id` 鉴权体系一致。
4. **v1 用 mock 实现该端点**（返回 canned HVAC 应答 + 注入真实遥测），保持 mock-first 可离线演示；真实 LLM 接入时只换后端实现，**前端零改动**。

备选（外部服务 Dify / Flowise）：仅当后续要做「对 HVAC 手册 / 规程的 RAG 问答」才引入，v1 不必要（运维重、收益低）。

### Q2：上下文如何注入实时 HVAC 数据？

**v1 = 直接拼接遥测摘要（context stuffing）**：把 `TelemetryClient` 当前快照（6 台设备的 power / cop / load / 供水回水温度 / 负荷率 + 时间窗）格式化成一段 `system` / `context` 文本，随消息一起发给模型。

理由：
- 确定性强、零额外检索服务、零成本。
- 完全满足「问答 HVAC 实时数据」的 v1 目标。
- **避免 RAG / 工具调用**（后者是红线风险来源，见 Q3）。
- 未来若要做「基于历史曲线 / 手册的问答」，再升级 Dify RAG 或后端 tool-call 取时序——但 tool-call 必须**禁用所有写类工具**。

### Q3：只读红线如何保证？

三层保证，缺一不可：
1. **端点层**：provider 调用**不启用 function / tool calling**，模型只能返回文本 → 物理上无法触发任何动作。
2. **前端层**：`/ai` 组件零写接口、零下发按钮，只渲染对话。
3. **后端层**：`/ai/chat` 不暴露任何 command / schedule / device-write 路由，与现有下发链路（`internal/commands`）物理隔离。

→ 即使模型「想」下发，也没有任何通道。

## 4. v1 推荐方案（落地结论）

| 维度 | 决策 |
|---|---|
| 端点契约 | `POST /api/v1/ai/chat`（SSE，OpenAI 兼容流格式），body `{messages, siteId?}`；**v1 用 mock 实现** |
| 前端库 | **Vercel AI SDK `useChat`**（首选，25.4k★，框架无关）或轻量 **nlux**（1.4k★，adapter 模式，若想最小依赖）；两者都对接我们自己的 `/ai/chat` SSE，不依赖 Next.js |
| 上下文注入 | 直接注入 `TelemetryClient` 实时快照摘要 |
| 红线保证 | 端点禁用 tool calling + 前端无写入口 + 与下发链路隔离 |
| 与 #13 一致 | v1 mock 端点可离线演示；真实 LLM 仅在后端切换，前端不变 |

## 5. 对 #20（/ai 页面实装）的约束

- 页面只消费 `/ai/chat` 契约；具体端点实现（mock / real）由本调研决定，实装按 mock-first。
- 预设 3+ 建议问题（如「当前总功率多少？」「哪台设备 COP 最低？」「有什么节能建议？」）。
- **绝不出现下发 / 写操作入口**（只读红线）。

## 6. 参考链接

- vercel/ai — https://github.com/vercel/ai
- nlkitai/nlux — https://github.com/nlkitai/nlux
- assistant-ui/assistant-ui — https://github.com/assistant-ui/assistant-ui
- langchain-ai/langchainjs — https://github.com/langchain-ai/langchainjs
- langgenius/dify — https://github.com/langgenius/dify
- FlowiseAI/Flowise — https://github.com/FlowiseAI/Flowise
- open-webui/open-webui — https://github.com/open-webui/open-webui
- lobehub/lobe-chat — https://github.com/lobehub/lobe-chat
- ChatGPTNextWeb/NextChat — https://github.com/ChatGPTNextWeb/NextChat
