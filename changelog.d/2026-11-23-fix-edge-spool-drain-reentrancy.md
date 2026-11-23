---
date: 2026-11-23
category: 修复
---

- 修复 `hvac-edge-gateway` MQTT 连接状态回调内同步调用 `spool.Drain` 引发的无限重入发布风暴，避免离线缓存存在积压时阻塞冷站 RS-485 轮询循环。
