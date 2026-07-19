# 泉来禾智慧能源平台

本仓库采用单仓库、多运行时结构。根目录只负责编排、共享文档和跨层验证，不承载具体应用源码。

## 目录结构

```text
apps/
  hvac-web/                  生产 Web adapter（Vite + React）

runtimes/
  copilot-runtime/           CopilotKit Runtime module（Node.js）

agents/
  energy-agent/              EnergyAgent 领域与 LangGraph module（Python）

references/
  energy-agent-next/         原 Next.js Canvas，作为参考和验收 adapter

scripts/                     本地编排、验证和浏览器审计
docs/                        产品、架构和集成文档
```

## 生产运行链路

```text
apps/hvac-web
  -> /api/v1/copilotkit
runtimes/copilot-runtime
  -> sample_agent
agents/energy-agent
```

`references/energy-agent-next` 不参与 HVAC Web 的生产构建与部署。它保留完整 Investigation Canvas 和自定义 A2UI catalog，用于参考实现、协议验证与验收。

## 常用命令

```bash
npm run dev                 # 仅启动 HVAC Web
npm run dev:energyagent     # 启动 Web、Copilot Runtime 和 EnergyAgent
npm run lint
npm run build
npm run verify:ai-runtime
npm run verify:energyagent-stack
```

EnergyAgent 模型配置放在 `agents/energy-agent/.env.local`，或通过进程环境变量提供。不得提交密钥。

## 依赖所有权

- 根目录 `package.json`：HVAC Web、Copilot Runtime 与仓库编排所需 Node 依赖。
- `agents/energy-agent/pyproject.toml`：Python Agent 依赖，使用 `uv.lock` 锁定。
- `references/energy-agent-next/package.json`：参考 Next.js adapter 依赖，使用 `pnpm-lock.yaml` 锁定。

不同 adapter 可以使用不同 React 或 CopilotKit 版本；跨进程兼容性由 Runtime 与协议验证保证，而不是通过强制统一所有依赖版本保证。
