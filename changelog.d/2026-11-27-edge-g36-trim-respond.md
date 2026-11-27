---
date: 2026-11-27
category: 改进
---
- 升级边缘冷站群控器（`ChillerPlantClusterController`）与水利联锁控制器（`HydraulicInterlockController`）：修复低负荷下 `LoadLimitAddr` 30% 正反馈锁死问题，补齐冷却塔就地/远程（`LocalRemoteMode`）防冲突联锁保护，并将冷冻泵、冷却泵与冷却塔风机由开环线性映射升级为基于温差/压差/逼近度闭环反馈的 ASHRAE Guideline 36 Trim & Respond（带死区、防积分饱和、斜率限幅及缺传感器降级）。
