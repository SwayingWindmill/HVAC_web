---
date: 2026-11-27
category: 修复
---

- 接入优化服务边缘模型下发的 Ed25519 签名密钥配置与只读 `gateway-ca` 目录挂载，启动时按 `OPTIMIZATION_EDGE_MODEL_SIGNING_KEY_FILE` 加载 PKCS#8 私钥并在配置错误时快速失败。
- 对齐 `ai-solver` 离线校准训练器与优化服务治理飞轮的 5 类负样本枚举（`GUARDRAIL_CLAMPED`、`GUARDRAIL_VETOED`、`MANUAL_OVERRIDE`、`TRACKING_DEVIATION`、`MONITOR_TRIPPED`），清除失效旧枚举名并补齐跨语言 JSONL 契约测试。
