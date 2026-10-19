# 登录会话和操作记录

登录会话怎么存进 PostgreSQL，以及登录、退出、吊销这些安全事件怎么可靠地写进只追加的操作记录。

第一阶段的 Compose 里没有 Kafka：会话和「待发事件」都照常写进网关的数据库；把待发事件经 Kafka 送到操作记录服务这一段（`outbox-relay` → Kafka → `audit-ledger-service` 的接收程序），只在单独运行这些服务时才有。

## 整条链

```text
浏览器
  HTTPS + 不透明的登录会话 cookie
      |
      v
平台网关
  一个 PostgreSQL SERIALIZABLE 事务
    会话状态
    操作记录意图
    待发事件（outbox）
      |
      v
outbox-relay
  要消息总线确认收到
      |
      v
Kafka（Redpanda）
  主题 control.security.session.v1
      |
      v
操作记录服务（audit-ledger-service）
  收件表（去重）
  每个租户的哈希链头
  只追加的操作记录
      |
      v
网关公开的、按租户隔离的操作记录查询
```

浏览器访问不到 PostgreSQL、Kafka、IAM 和操作记录服务。网关访问 IAM 和操作记录服务都用 TLS 1.3 双向认证，各自签一张限定对象、动作和范围的授权。

## 会话修改是一个事务

新建会话、退出、管理员吊销，都用一个 PostgreSQL `SERIALIZABLE` 事务，提交前必须同时写好三样：

1. 会话状态；
2. 不可改的操作记录意图；
3. Protobuf 格式的待发事件。

事务提交之后，才下发 cookie 或返回修改成功。写完状态、写完意图、提交前或唯一约束检查时出错，整个事务回滚。不存在一个外面看得到、却没有对应操作记录意图和待发事件的会话。

身份服务的令牌和会话的 CSRF 密钥在会话表里用 AES-GCM 加密。浏览器的会话 cookie 值在进入待发事件、Kafka 或操作记录之前，先换算成一个单独加盐的 SHA-256 编号。操作记录意图和待发事件里只有：操作人、租户、动作、结果、策略、关联编号、脱敏后的编号和哈希。

## 有版本的事件格式

格式定义在 `contracts/events/session-audit.v1.proto`，兼容锁在 `contracts/events/session-audit.v1.lock.json`。`npm run events:check` 拒绝改动已有的消息名、字段名、字段编号、字段类型、包名或 Go 包名：要做不兼容的改动，就新开一个有版本的消息或主题，不能悄悄改。

`SessionAuditEventV1` 带：固定的消息编号和格式版本；分区键；聚合类型、编号和单调递增的版本；发生和生成时间；关联、因果和追踪编号；发起的用户和执行的服务身份（分开记）；当前租户；动作、结果和策略版本；精简后的内容哈希和会话状态。

仓库自己的编码器输出确定性的 Protobuf 字节。测试保证序列化之前，授权凭据、身份服务的凭据、cookie 和授权内容都被拒绝。

## 待发事件的投递：至少一次

`outbox-relay` 用 `FOR UPDATE SKIP LOCKED` 领待发事件，带一个有时限的租约：

- 消息总线出错：释放租约，记一个安全的错误代码，事件保持待发；
- 消息总线确认收到之后，才标记为已发；
- 总线确认了、数据库标记失败：租约到期后会再发一次。

最后这种情况是故意的「至少一次」，不保证只送一次。

## 收件表去重

操作记录的接收程序取消息时不自动提交位置。在一个 PostgreSQL 事务里：

1. 设置这个事务的租户（行级隔离）；
2. 写入收件记录：消息编号、在消息总线里的位置、Protobuf 信封的哈希；
3. 锁住这个租户的哈希链头；
4. 追加一条操作记录；
5. 前移哈希链头。

数据库事务提交之后，才提交消息总线的位置。

同一个消息编号、Protobuf 字节也一样的，确认收到但不再追加记录；编号一样、字节不同的，作为信封冲突拒绝。测试在接收程序重启前后重复投递，并把已发的待发事件改回待发，所有情况最后都只有一条操作记录。

## 只追加的操作记录

操作记录包含完整的操作人、租户、动作、结果、策略、关联和聚合信息。每个租户有自己的一条哈希链：

```text
record_hash = SHA-256(上一条的 record_hash || 0x00 || protobuf 内容)
```

第一条从 64 个 0 开始。数据库触发器拒绝对操作记录的 `UPDATE` 和 `DELETE`。

## 按租户隔离

操作记录的表强制 PostgreSQL 行级隔离，运行时只在当前事务里设置 `app.tenant_id`。

公开的查询：

```text
GET /api/v1/audit/session-events/{messageId}
```

网关先经 IAM 验证登录会话，再另签一张操作记录的授权，限定：对象 `audit-ledger-service`；动作 `audit:read`；范围 `tenant:<当前租户>`；当前发起的人、策略版本和会话到期时间。

别的租户的人登录后来查，得到的 `AUDIT_RECORD_NOT_FOUND` 和查一个不存在的编号一样，不透露记录是否存在。

## 数据库身份

本地和 CI 建四个非超级用户、不拥有表结构的运行角色：

- `gateway_runtime`：会话、操作记录意图和待发事件的事务写入；
- `gateway_relay_runtime`：只能领待发事件、改投递状态；
- `audit_consumer_runtime`：在行级隔离下写收件表、哈希链头和只追加的操作记录；
- `audit_query_runtime`：只能按租户读操作记录。

集成测试证明：网关写不了操作记录的表，投递程序改不了会话状态，接收程序写不了网关的表，查询角色写不了收件表。

## 测试

```bash
npm run events:check
npm run contracts:check
npm run test:durable-unit
npm run test:durable-postgres
npm run build:energy-api
npm run build:audit-owner
npm run build:outbox-relay
npm run lint
npm run build
npm run audit:security-failure
```

浏览器检查（需要 Docker）验证：生成的客户端里的操作记录界面状态；持久会话的创建和公开查询；完整的操作人和租户信息；内容和哈希链正确；投递程序和操作记录服务重启；重复的 Protobuf 重放最后只有一条记录；别的租户查不到；消息总线中断时会话事务仍能提交；中断期间看得到待发事件；Kafka 恢复后只有一条记录；浏览器直接访问操作记录服务失败。
