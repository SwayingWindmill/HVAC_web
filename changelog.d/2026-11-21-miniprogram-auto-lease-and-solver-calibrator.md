---
date: 2026-11-21
category: 新增
---
- 为微信小程序端新增站点自动控制租约状态与人工退避保护提示横幅，并增加外部 Python 算法服务离线校准与标定参数加载能力：
  - 小程序「运行」实时视图与「设备详情」应急手调区并行拉取 `/api/v1/sites/:siteId/optimization/auto-control-lease`，自动展示「AI 自动托管中」「影子跟跑中」或优先级更高的「人工退避保护中」窗口与边界；
  - `services/ai-solver-service` 新增 `python3 -m ai_solver.trainer` 离线标定命令行，可直接消费负样本 `.jsonl` 数据生成带硬边界钳位的参数文件并通过 `AI_SOLVER_PROFILE_PATH` 注入求解器（`/readyz` 上报当前加载的 `calibrationProfileId`）。
