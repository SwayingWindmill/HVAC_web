# 泉来禾智慧能源平台

本仓库采用单仓库、多运行时结构。根目录只负责编排、共享文档和跨层验证，不承载具体应用源码。

## 目录结构

```text
apps/
  hvac-web/                  生产 Web adapter（Vite + React）

services/
  platform-gateway/          S0 公共 HTTP 入口（Go）
  iam-service/               S0 私有身份服务（Go，mTLS）
  oidc-test-provider/        S0 确定性 OIDC 测试提供者（Go）

libs/
  identitycontext/           受约束委托与 actor chain 类型
  oidctest/                  OIDC 测试 fixture
  testpki/                   临时测试 CA 与 Workload 证书

contracts/
  http/                      OpenAPI 权威契约与生成工具锁

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
npm run dev:s0-gateway      # 启动 HVAC Web + Go Platform Gateway
npm run dev:s0-auth         # 启动 HTTPS Web、OIDC、Gateway 与 mTLS IAM
npm run dev:energyagent     # 启动 Web、Copilot Runtime 和 EnergyAgent
npm run contracts:check     # 校验 OpenAPI 生成产物无漂移
npm run test:gateway        # Gateway Go 黑盒测试
npm run test:identity       # OIDC、委托、IAM、撤销与 Gateway 身份测试
npm run build:gateway       # 独立构建 Gateway 二进制
npm run build:iam           # 独立构建 IAM 二进制
npm run audit:platform-gateway
npm run audit:auth-principal
npm run lint
npm run build
npm run verify:ai-runtime
npm run verify:energyagent-stack
```

EnergyAgent 模型配置放在 `agents/energy-agent/.env.local`，或通过进程环境变量提供。不得提交密钥。

## 依赖所有权

- 根目录 `package.json`：HVAC Web、Copilot Runtime、契约生成与仓库编排所需 Node 依赖。
- `services/platform-gateway/go.mod`：Gateway 独立 Go module。
- `services/iam-service/go.mod`：私有 IAM 独立 Go module。
- `services/oidc-test-provider/go.mod`：本地/测试 OIDC fixture 独立 Go module。
- `libs/*/go.mod`：委托、OIDC fixture 与测试 PKI 的窄接口模块；根目录 `go.work` 只负责编排。
- `contracts/http/tooling.lock.json`：契约生成器、模板、Go、Node 与运行时校验版本锁。
- `agents/energy-agent/pyproject.toml`：Python Agent 依赖，使用 `uv.lock` 锁定。
- `references/energy-agent-next/package.json`：参考 Next.js adapter 依赖，使用 `pnpm-lock.yaml` 锁定。

不同 adapter 可以使用不同 React 或 CopilotKit 版本；跨进程兼容性由 Runtime 与协议验证保证，而不是通过强制统一所有依赖版本保证。
