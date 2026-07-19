# EnergyAgent

泉来禾智慧能源平台的 Python LangGraph module。

该目录是 EnergyAgent 领域与工作流的唯一规范实现，包含：

- `src/energy`：确定性能耗数据与计算；
- `src/investigation`：Investigation 状态与不变量；
- `src/workflow`：Analysis Scope 与 LangGraph 工作流；
- `src/a2ui`：固定 Result Surface catalog；
- `tests`：领域、工作流、韧性与协议测试。

从仓库根目录运行：

```bash
uv sync --project agents/energy-agent
npm run dev:energy-agent
npm run verify:energyagent-stack
```

模型密钥放在本目录的 `.env.local`，或通过进程环境变量提供。
