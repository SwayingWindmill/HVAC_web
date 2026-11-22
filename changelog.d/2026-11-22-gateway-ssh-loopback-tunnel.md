---
date: 2026-11-22
category: 新增
---
- 为现场网关反向隧道新增可选、仅绑定服务端本机回环的受控 SSH 运维通道（`CONNECTIVITY_TUNNEL_SSH_LOOPBACK_ADDR` 设为 `127.0.0.1:18822` 时生效，默认关闭），双端严格限制端口白名单并记录到 `gateway_tunnel_audit_events` 审计表，同时补充 `systemd` 按需启停模板。
