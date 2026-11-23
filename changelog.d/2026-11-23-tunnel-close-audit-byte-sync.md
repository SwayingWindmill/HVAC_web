---
date: 2026-11-23
category: 修复
---
- 修复反代隧道（`tunnel.Session`）关闭审计事件与发送帧计数之间的并发竞争（在采集 `BytesTX` 前与 `sendFrame` 写锁同步），防止快速断连时 `SESSION_CLOSED` 审计记录偶发漏计已发送字节；同步消除 `edgedaemon` 隧道客户端测试等待 Hub 会话就绪的竞态条件。
