# outbox-relay

把平台网关「待发事件表」（outbox）里已经提交的事件发到 Kafka 兼容的消息总线。它是单独的进程，不保存会话或操作记录。第一阶段的 Compose 里不运行它（没有 Kafka）。

## 怎么投递

每次用 `FOR UPDATE SKIP LOCKED` 领一条待发的事件，并带一个有时限的租约。

- 消息总线确认收到之后，才记下 `published_at`。
- 发送失败，事件保持待发，只记一个固定的错误代码。
- 总线确认之后、数据库还没标记之前出故障，可能会再发一次。
- 重复的消息由操作记录服务的收件表去掉。

也就是「至少送达一次」，不保证只送一次。

## Kafka 消息

- 主题：`control.security.session.v1`
- 键：`bff-session:<操作记录聚合编号的 sha256>`；浏览器会话 cookie 的值从不发出去
- 值：确定性的 `SessionAuditEventV1` Protobuf 字节
- 头：消息编号、格式版本、聚合版本、租户、内容类型

发送前先校验存下的 Protobuf 信封的哈希。

## 数据库身份

`gateway_relay_runtime` 只能查和改待发事件的投递字段，不能改会话状态、操作记录意图或操作记录表。

## 命令

```bash
npm run build:outbox-relay
npm run test:durable-unit
npm run audit:durable-session
```

故障和恢复的测试见 `docs/security/session-and-audit.md`。
