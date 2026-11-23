### Added
- 端侧 AI 模型与控制超参版本托管及热更新工作台：在 `core_registry.mlops_*` 扩展 `EDGE_CONTROL` / `EDGE_FDD` 元数据域与 RLS 读写策略，打通 `optimization-service` -> `connectivity` 配置影子 -> `edge-gateway` Ed25519/SHA-256 验签与物理护栏热替换 -> 影子偏差监测自动回退闭环，并在 `/system?tab=governance&section=edge-models` 提供版本登记、结构化超参差异对比、审批下发与一键回退界面。（#921）
