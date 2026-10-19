# 操作记录（audit）

保存登录、退出、会话吊销这类安全相关的操作记录，只追加、不修改，也不能删。它不面向浏览器，不含任何冷站业务数据。服务名是 `audit-ledger-service`。

## 做什么

- 接收会话事件 `control.security.session.v1`，解出带版本号的 Protobuf 信封。
- 在一个 PostgreSQL 事务里写三样东西：收件记录（用来去重）、这个租户的哈希链头、操作记录本身。事务提交之后，才确认消息已经处理。
- 同一个消息编号、内容也一样的，当作重复，不再写；编号一样、内容不同的，拒绝。
- 记录只能追加；数据库按租户做行级隔离，一个租户读不到另一个租户的记录。
- 提供一个内部查询接口（双向 TLS）。

从 Kafka 接收事件这条路，只在单独运行 `audit-owner`（`modules/audit/cmd/audit-owner`，需要配置 `CONTROL_BACKBONE_BROKERS`）时使用；第一阶段的 Compose 里没有 Kafka。

## 内部查询接口

```text
GET /internal/v1/audit/session-events/{messageId}
```

调用方必须：

- 持有平台网关的服务证书；
- 带一张用这个证书的密钥签的授权，对象是 `audit-ledger-service`，动作 `audit:read`，范围 `tenant:<租户 id>`；
- 这张授权来自 IAM 判定此人有「查看操作记录」权限（`audit.read`）。操作记录服务不看角色名。

记录不存在和属于别的租户，都返回 `AUDIT_RECORD_NOT_FOUND`，不透露记录是否存在。

## 数据库身份

接收事件用 `audit_consumer_runtime`，查询接口用 `audit_query_runtime`。两者都不拥有表结构，也不能写网关的表。

## 命令

```bash
npm run build:audit-owner
npm run test:durable-unit
npm run test:durable-postgres
npm run audit:durable-session
```

完整的事务、投递、行级隔离和哈希链设计见 `docs/security/session-and-audit.md`。
