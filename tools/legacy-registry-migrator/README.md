# 旧登记数据迁移工具（legacy-registry-migrator）

当初把旧系统的站点、资产、设备一次性迁进平台登记库用的离线工具。现在的平台运行时不用它，它也不在 `go.work` 里；它的迁移测试仍在 `npm run registry:integration` 里跑。

它只通过受限的 `s1_migration_operator` 角色写库，从不直接连旧系统的数据库。

## 输入

一行一个 JSON。租户是外层字段，不是被迁移的对象。能迁的对象按依赖顺序是：站点、资产、设备。

```json
{"tenantId":"018f1d00-0000-7000-8000-000000000001","kind":"ASSET","sourceSystem":"legacy-hvac-backend","sourceTable":"asset","sourceKey":"ahu-42","sourceWatermark":"2026-07-22T00:00:00Z","sourceRowHash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","transformationVersion":"s1-v1","batchId":"registry-2026-07-22","code":"ahu-42","displayName":"AHU 42","status":"ACTIVE","resourceType":"AHU","siteRef":{"sourceSystem":"legacy-hvac-backend","sourceTable":"site","sourceKey":"north-campus"}}
```

资产和设备必须带 `siteRef`。`ORGANIZATION`、`AREA`、`EQUIPMENT` 不是能迁的对象类型；旧系统的名字只能作为来源记录留在 `sourceTable` / `sourceKey` 里，不决定迁成哪种对象。

不认识的字段、过大的记录、不是 SHA-256 的哈希、NUL 字节、嵌套太深的附加信息、可能带凭据的附加字段名，一律拒绝。

## 导入

数据库连接必须用专门的 `s1_legacy_migration_service` 登录。每个事务里切到 `s1_migration_operator`，确认当前角色后才写。

```bash
legacy-registry-migrator apply \
  --dsn "$S1_LEGACY_MIGRATION_DSN" \
  --input normalized-registry.jsonl
```

按（来源系统、来源表、来源键）加锁。同样的内容再导一次，记为「跳过」；已经核实过的数据来源哈希变了，放进隔离区，不覆盖现有的业务数据。

## 处理隔离区

要一个打开着的隔离记录编号（UUIDv7），和恰好一条同一来源的、改正过的记录：

```bash
legacy-registry-migrator resolve \
  --dsn "$S1_LEGACY_MIGRATION_DSN" \
  --quarantine-id 01900000-0000-7000-8000-000000000001 \
  --action apply \
  --input corrected-record.jsonl
```

`--action retire` 建一个已退役的对象并关闭这条来源对照。两种操作都可以重复执行，都会追加不可改的来源记录。

## 保证

- 对外的编号由平台生成（UUIDv7）；旧系统和 ThingsBoard 的编号只留在内部。
- 每条记录用一个有限的可串行化事务和事务级的咨询锁。
- 对照的状态只有：已发现、已对照、已核实、已隔离、已退役。
- 隔离中的对照没有目标对象编号，平常的登记读取看不到。
- 迁移登录在切换到操作角色之前，读不到 IAM 数据和登记的业务表。
- 平台运行时读登记数据时从不调用这个工具，也不做同步或双写。
