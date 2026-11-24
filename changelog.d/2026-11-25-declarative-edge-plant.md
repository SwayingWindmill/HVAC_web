---
date: 2026-11-25
category: 改进
---

- `edge-gateway` 现场冷站采集、热量表虚拟计算、无线集中器分发展示与顺控 DO 启停执行器改为由 `fieldPlant` / `fieldPlantFile`（如 `deploy/sites/songyaxuan.edge-plant.json`）声明式配置驱动，并支持通过平台配置影子 `pointMappings` 热更新温度点位复制路由，新站点接入无需修改 Go 代码。
