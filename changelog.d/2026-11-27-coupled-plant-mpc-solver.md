---
date: 2026-11-27
category: 改进
---
- 升级优化求解引擎为耦合灰盒模型预测控制（`builtin://coupled-plant-mpc-v1`），在 Go 优化服务中统一完成冷机单调 COP/EIR 曲线、定压+三次方变频水泵水力耦合、非线性冷却塔风机逼近温差与 24 小时分时电价热负荷动态规划求解，无需生产环境常驻独立 Python 求解容器；同时保持 Python `ai-solver-service` `/v1/solve` 协议同步升级与兼容。
