---
date: 2026-11-23
category: 新增
---
- 为周期休眠/低功耗设备新增 `SLEEPING`（休眠中）在网状态：数据库与 S2 遥测契约支持 `sleep_cycle_seconds` 与 `EXPLICIT_SLEEP` 信号，设备标签设置 `sleep_cycle_seconds` 后可自动同步休眠窗口并在运行监视页面展示为「休眠中」，不再误判为离线或异常，超时未唤醒再转离线。
