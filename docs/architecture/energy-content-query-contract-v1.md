# Wayfinder #307：Backend Energy Content 查询契约

状态：DECIDED / SOURCE-BACKED  
审查日期：2026-08-25  
范围：冻结 Backend 面向 UI、Energy Processing 以及后续 Report/Cost/Carbon 的只读查询边界。不实现 Registry API、Energy Processing Projector、ClickHouse schema 或 UI 页面。

## 1. 冻结结论

当前 Backend 不增加一个包揽 Meter、Space、Asset、Telemetry、Cost 和 Report 的“通用 Energy Cube”。本阶段保留两个职责清晰的查询面：

1. **Energy Series Query**：面向 Energy Management 的产品查询，当前查询主体固定为 `Tenant + Site`，沿用现有 electricity、hour/day/month、IANA timezone、质量策略、watermark、partial 和 dataset revision 合同。
2. **Energy Content Query**：面向 Administration 和 Energy Processing 的能源内容读取，提供 Meter、MeterBinding 以及按事件时间解析 released Binding 的只读合同。它读取 Registry 的发布内容，不执行时序聚合，也不替代 Telemetry Query。

因此，首个纵向切片的调用关系固定为：

```text
UI / BFF
  ├─ Energy Series ──> Platform Gateway ──> Telemetry Query Service ──> Energy read model / Cube
  └─ Energy Content ─> Platform Gateway ──> Platform Core ──> S1 Registry read boundary

Energy Processing ──(private released-content adapter)──> Platform Core / S1 Registry
```

Energy Series 继续按 Site 返回聚合结果；Meter/MeterBinding 是解释、管理和处理事实的内容资源，不成为 Energy Series 的任意 subject union。

## 2. 源码证据

### 2.1 HVAC 当前实现

| 证据 | 已核对的事实 | 对本契约的影响 |
| --- | --- | --- |
| [`libs/analyticsmodel/energy.go`](../../libs/analyticsmodel/energy.go) | `EnergySeriesQuery` 只有 Tenant、Site、energyType、granularity、timezone、from/to、qualityPolicy；energy type 当前只允许 electricity，粒度只允许 hour/day/month。 | 不增加 Space/Asset/Meter subject 字段；现有 Energy Series 合同保持闭合。 |
| [`contracts/http/analytics-energy-public.openapi.yaml`](../../contracts/http/analytics-energy-public.openapi.yaml) | 浏览器只能通过 `POST /api/v1/analytics/energy-series` 查询；时间是 inclusive-from/exclusive-to；返回 quality summary、watermark、partial、datasetRevision。 | Gateway 继续是唯一浏览器入口；这些字段是当前能量查询的权威 freshness/provenance 表达。 |
| [`services/platform-gateway/internal/gateway/analytics.go`](../../services/platform-gateway/internal/gateway/analytics.go) | Gateway 以 `analytics.energy-series.read` 授权，授权 scope 绑定 Tenant、Site 和规范化 query digest；不把 Cookie、CSRF、Tenant 或 Site header 转发给 Query Service。 | Energy Series 与 Registry Content 使用同样的 BFF → IAM → 内部 delegation 形态，但拥有各自 action 和下游。 |
| [`services/telemetry-query-service/README.md`](../../services/telemetry-query-service/README.md) | Query Service 拥有 bounded historical telemetry/analytics API；不拥有 Registry、Presence、Energy Fact 构造或任意 Cube/SQL。 | Energy Content 不放进 Telemetry Query；Query Service 只消费已形成的 Energy Processing read model。 |
| [`services/platform-core-service/README.md`](../../services/platform-core-service/README.md) | Core 是私有 S1 Registry read boundary，使用 mTLS、`X-Delegation-Grant`、RLS、Tenant/Site scope 和签名 keyset cursor；当前公开的 Core 路由还没有 Meter/MeterBinding。 | 新 Content route 必须延续 Core 的边界；当前项目需要新增能力，而不是假设它已经存在。 |
| [`services/platform-core-service/internal/core/validation.go`](../../services/platform-core-service/internal/core/validation.go)、[`cursor.go`](../../services/platform-core-service/internal/core/cursor.go) | Registry list 默认 limit 为 50、最大 200；cursor 是绑定 route、action、scope、policy revision 的 HMAC keyset cursor，现有排序语义为 `(displayName,id)`。 | Content list 使用相同的 opaque cursor 语义；不引入 offset pagination。具体 Binding 排序需在实现票据中扩展 cursor payload。 |
| [`libs/registryauth/registry.go`](../../libs/registryauth/registry.go) | 当前 action 只有 `site.*`、`asset.*`、`device.*`、`device-binding.list` 等；`registry.read` 只允许这些现有读动作。 | Meter 和 MeterBinding 需要新增明确的 read/list actions，不能复用过宽的 `registry.read` 或错误的 `binding.write`。 |
| [`apps/hvac-web/src/api/energy-analytics.ts`](../../apps/hvac-web/src/api/energy-analytics.ts)、[`apps/hvac-web/src/real/EnergyAnalytics.tsx`](../../apps/hvac-web/src/real/EnergyAnalytics.tsx) | UI schema 与真实工作区都按 Site 查询，只显示 requested/actual granularity、quality、partial、watermark、dataset revision，并由客户端计算 stale presentation state。 | UI 不依赖一个未存在的 Content freshness 字段；Registry revision 与 Energy dataset revision 分开表达。 |
| [`infra/s1-registry/postgres/init/009a-energy-topology-metering-v2.sql`](../../infra/s1-registry/postgres/init/009a-energy-topology-metering-v2.sql) | `energy_meters` 是物理 Meter 身份；`meter_bindings` 绑定 topology、edge、energy type、meter、device、counter point、role、direction、priority、effective interval、version、status、revision；发布状态包含 RELEASED/ACTIVE，且已有 PRIMARY overlap 约束。 | Content 返回发布版本和事件时间字段；Processing 必须按 released snapshot 和半开区间解析，不按当前最新行猜测。 |
| [`docs/architecture/energy-fact-meter-binding-contract-v1.md`](energy-fact-meter-binding-contract-v1.md) | #306 已冻结 `effective_from <= sampled_at < effective_to`、无匹配不生成 Fact、歧义不静默选择、Fact provenance 和 revision 规则。 | #307 只把这些规则转成读取契约，不重新定义 Fact。 |

### 2.2 三个参考项目的实际源码结论

固定版本和逐文件记录见 [`wayfinder-energy-reference-source-review-2026-08.md`](../research/wayfinder-energy-reference-source-review-2026-08.md)。本票据只吸收与查询边界直接相关的机制：

| 参考项目 | 源码事实 | HVAC 决策 |
| --- | --- | --- |
| ThingsBoard CE v4.3.1.1，`c2a52e46c44e308ddee430e7266b8e10eddde9c4` | Latest、History、实体关系和固定产品查询是不同职责；Dashboard 组合能力不等于让每个客户端直接执行任意时序查询。 | **ADAPT**：保留 Site/Entity scope、固定查询和 BFF 边界；不复制 ThingsBoard 通用 Telemetry runtime 或任意 Dashboard 查询引擎。 |
| OpenEMS 2026.7.0，`2e2792d59fc5ba3b99ce3cf98d15081c0a74895e` | Timedata 提供历史时间数据，Channel/Doc 保留类型、单位和自描述；它不是当前 HVAC 的 Meter/Binding Registry。 | **ADAPT/DEFER**：借鉴 typed content 和历史读取分离；Edge Channel、Cycle、Controller 不进入当前 Backend Content API。 |
| MyEMS v6.7.0，`be6e6ce8ddeac57afb04bddb9621501fb555cab0` | Admin/Web 分开，Meter、Virtual Meter、cleaning、normalization、aggregation 具有不同职责，数据处理链不是一个查询接口。 | **ADAPT**：保留能源内容与处理链分离、Admin 管理内容；首个切片不提前开放 Virtual Meter、Tariff、Carbon 或任意报表维度。 |

## 3. 查询主体和服务职责

### 3.1 Energy Series

首个切片的 Energy Series 请求继续使用现有合同：

```json
{
  "tenantId": "...",
  "siteId": "...",
  "energyType": "electricity",
  "granularity": "hour",
  "timezone": "Asia/Shanghai",
  "from": "2026-01-01T00:00:00Z",
  "to": "2026-01-02T00:00:00Z",
  "qualityPolicy": "VALID_AND_SUSPECT"
}
```

这里的 `siteId` 是查询主体，不是“先查全部 Meter 再由 UI 相加”的约定。首个切片由已发布的 PRIMARY electricity Binding 形成 Fact，再由 Query Service 按 Site 聚合。

当前不接受以下请求字段：

- `meterId`、`meterBindingId`、`spaceId`、`assetId` 作为 Energy Series 的可选 subject；
- 任意 dimensions/measures、Cube SQL、筛选表达式或用户传入的公式；
- week/year 粒度；当前 UI 旧页面存在这些展示概念，但真实 `EnergyAnalytics` schema 不支持它们。

这不是永久否定 Meter、Space 或 Asset 维度，而是把它们留给有事实模型、权限模型和产品用途证明的后续 Report/Cost/Carbon 查询合同。

### 3.2 Energy Content

Energy Content 的资源只读合同分为三类：

1. **Meter list/detail**：Administration 读取物理计量身份、设备关联、energy type 和生命周期状态。
2. **MeterBinding list/detail**：Administration 和 Energy Management 读取 topology、edge、Meter、Device、Counter Point、role、direction、priority、effective interval、version、status、revision。
3. **Binding resolution**：Energy Processing 按 Tenant/Site、Device、Counter Point 和 observation `sampled_at` 解析 released Binding；这是私有 Backend 处理输入，不向浏览器暴露。

Resolver 的结果固定为：

- **MATCH**：恰好一个 released/active Binding 满足 `effective_from <= sampled_at < effective_to`；返回 Binding 快照及其 Meter/Topology/Point 语义。
- **NO_MATCH**：没有匹配 Binding；返回明确的无匹配结果，Energy Processing 不生成 Fact，不把它当成系统故障重试。
- **AMBIGUOUS**：有多个匹配 Binding；返回领域冲突，Energy Processing 不生成 Fact，不选择“最新”或“当前”配置。

`RELEASED/ACTIVE` 的具体发布选择由 Registry 的发布事务提供；Resolver 不在查询层自己推断“最后更新的一行”。

## 4. 路由、授权和数据流

### 4.1 浏览器公共路由

以下是后续实现必须遵守的公共路径。它们都经过 Platform Gateway；浏览器不直接访问 Core、Query Service、Postgres 或 Cube。

| 用途 | 方法和路径 | Gateway action | 下游 owner |
| --- | --- | --- | --- |
| 站点 Meter 列表 | `GET /api/v1/sites/{siteId}/energy-meters` | `energy-meter.list` | Platform Core / S1 Registry |
| Meter 详情 | `GET /api/v1/energy-meters/{meterId}` | `energy-meter.read` | Platform Core / S1 Registry |
| 站点 Binding 列表 | `GET /api/v1/sites/{siteId}/meter-bindings` | `meter-binding.list` | Platform Core / S1 Registry |
| Binding 详情 | `GET /api/v1/meter-bindings/{bindingId}` | `meter-binding.read` | Platform Core / S1 Registry |

内部对应路径固定采用现有 Core route 形态：

```text
GET /internal/v1/registry/sites/{siteId}/energy-meters
GET /internal/v1/registry/energy-meters/{meterId}
GET /internal/v1/registry/sites/{siteId}/meter-bindings
GET /internal/v1/registry/meter-bindings/{bindingId}
```

Energy Processing 的 resolver 是另一个内部只读操作：

```text
GET /internal/v1/registry/sites/{siteId}/meter-bindings/resolve
  ?deviceId=...&pointId=...&sampledAt=...
```

它不能通过公共 Gateway 暴露，也不能使用列表接口取一页再由调用方猜测绑定。该 route 的实现需要在 #310 的实现规格中和 Core 的 route parser、cursor、RLS 查询一起落地；本票据冻结的是输入、发布范围和 MATCH/NO_MATCH/AMBIGUOUS 语义。

授权边界：

- Gateway 先验证 BFF session、Origin/CSRF、Tenant context 和 IAM decision；`siteId` 必须是授权决策允许的 Site。
- Gateway 给 Core 的 delegation 只包含对应 action、Tenant 和 Site scope；Detail ID 不得扩大授权范围。
- Core 继续执行 mTLS workload allowlist、grant 验签、grant status、RLS 和 Tenant/Site predicate；Core 不信任浏览器传入的 Tenant、Site 或用户身份 header。
- Energy Processing resolver 使用服务间 mTLS + delegation grant，不借用 UI session，也不允许从 resolver 访问任意 Registry 表。
- 需要新增 `energy-meter.list/read` 和 `meter-binding.list/read` 具体动作，并同步 `registryauth.Action.Valid/SiteScoped/ActionAllows`、IAM capability catalog、ownership lock 和 OpenAPI；不能复用当前过宽或语义不匹配的动作。

### 4.2 查询职责禁止穿透

| 组件 | 可以做什么 | 明确禁止 |
| --- | --- | --- |
| Platform Gateway | BFF session、CSRF/Origin、IAM、Tenant/Site scope、限流、错误映射、schema envelope | 读取 Registry DB、执行 Cube/SQL、拼接 Energy Fact |
| Platform Core / Energy Content | S1 Registry 的 Meter/Binding/Topology 只读、发布版本和事件时间解析 | 生成 Counter delta、聚合 Energy Series、读 ClickHouse 历史 |
| Telemetry Query Service | 读取 Energy Processing read model，执行固定 Energy Series 聚合，返回 watermark/partial/dataset revision | 读取或修改 Registry、构造 Fact、暴露任意 Cube/SQL |
| Energy Processing | 读取 resolver 结果，按 #306 形成 Fact、质量和 provenance | 用 telemetry key 推断 Meter、读当前/latest 代替 released Binding、静默选择歧义 Binding |
| UI | 调 Gateway 的公共合同，展示 Content revision 和 Energy dataset metadata | 直接调用 Core/Query/Cube，自己计算跨 Meter 的权威能耗、把 stale presentation 当成数据事实 |

## 5. 请求与响应形状

### 5.1 Meter

列表和详情共享同一个 Meter 表示；列表只允许字段裁剪，不改变语义：

```json
{
  "id": "...",
  "tenantId": "...",
  "siteId": "...",
  "meterCode": "grid_import",
  "displayName": "Grid Import",
  "deviceId": "...",
  "energyTypeId": "...",
  "status": "ACTIVE",
  "revision": 7
}
```

字段直接对应 `energy_meters` 的身份和生命周期字段。`revision` 是 Registry content revision，不是 Energy Series 的 `datasetRevision`。

### 5.2 MeterBinding

```json
{
  "id": "...",
  "tenantId": "...",
  "siteId": "...",
  "topologyVersionId": "...",
  "energyEdgeId": "...",
  "energyTypeId": "...",
  "meterId": "...",
  "deviceId": "...",
  "pointId": "...",
  "pointType": "COUNTER",
  "meterRole": "PRIMARY",
  "direction": "IMPORT",
  "priority": 0,
  "effectiveFrom": "2026-01-01T00:00:00.000Z",
  "effectiveTo": null,
  "version": 3,
  "status": "ACTIVE",
  "revision": 12
}
```

列表响应沿用现有 Registry collection：

```json
{
  "items": [],
  "nextCursor": "...",
  "hasMore": false
}
```

`cursor` 是 opaque、签名、keyset cursor。默认 `limit=50`，最大 `limit=200`，并绑定 route、action、Tenant、Site scope、授权 policy revision 和查询过滤条件。Meter 列表沿用 `(displayName,id)`；Binding 列表需要在实现时采用包含稳定 ID 的 Binding 专用排序和 cursor payload，不能复用错误的字段名掩盖排序差异，也不能退回 offset。

### 5.3 Energy Series metadata

Energy Series 继续使用现有 response，不把 Registry revision 混进来：

- `dataWatermark` / `aggregateWatermark`：Energy Fact 数据覆盖位置；
- `datasetRevision`：当前查询数据集 revision；
- `partial`：数据尚未覆盖请求结束或请求 bucket 存在缺口；
- `qualitySummary`：按现有 quality policy 返回的 VALID/SUSPECT/INVALID 计数。

UI 的 stale 是基于 watermark 和粒度的展示判断，不是新的 Backend 字段。Registry Content 不返回 data watermark，也不把 `updatedAt` 伪装成时序 freshness。

## 6. 时间、版本和质量语义

### 6.1 时间

- Energy Series 使用 UTC wire instant、`from` inclusive、`to` exclusive 和调用方显式传入的 IANA timezone；不默认 `Local`，不由 Backend 猜站点时区。
- MeterBinding 使用 Registry `timestamptz` 的 UTC wire 表示；Resolver 使用 observation 的 `sampled_at` 做半开区间匹配。
- 粒度和本地 bucket 只属于 Energy Series；Content list/detail 不接受任意时间粒度参数。

### 6.2 版本

- Meter/MeterBinding `revision` 和 `version` 是 Registry 内容语义：revision 表示资源变更版本，version 表示 Meter/Topology 业务版本。
- `status` 是发布生命周期，不是查询层的“最新”排序依据；Energy Processing 读取发布事务确认的 released/active 内容。
- Energy `datasetRevision` 只由 Energy Processing/Query Service 的事实数据集产生；不等于 Registry revision、Topology version 或 rebuild generation。

### 6.3 质量和部分数据

- Energy Series 只接受现有 `VALID_ONLY` 或 `VALID_AND_SUSPECT`；质量过滤在 Query Service 的固定合同内完成。
- `partial=true` 不等价于数据无效；UI 必须同时显示质量汇总和 watermark。
- Content resolver 的 `NO_MATCH` 和 `AMBIGUOUS` 是绑定解析结果，不应被 UI 伪装为零能耗。
- Resolver 不把 Current/Latest 当作 Counter 输入；这与 #305/#306 的生命周期和事实溯源决策一致。

## 7. 错误语义

所有公共路由沿用当前 RFC 9457 风格 Problem Details：`type`、`title`、`status`、`code`、`detail`、`retryable`，可带 `traceId`/`instance`。

| 场景 | HTTP | code 方向 | retryable |
| --- | ---: | --- | --- |
| JSON、path、未知 query 参数或 cursor 格式错误 | 400 | `ENERGY_CONTENT_QUERY_INVALID` | false |
| 没有有效 session / Core grant / workload identity | 401 | 现有 identity/grant invalid code | false |
| 有身份但没有该 Site/resource action | 403（Gateway 内部可折叠为 404） | `ENERGY_CONTENT_ACCESS_DENIED` | false |
| 资源不存在或对调用者不可见 | 404 | `RESOURCE_NOT_FOUND` | false |
| 请求的 subject、时间范围或不支持的查询语义合法解析但不在产品合同中 | 422 | `ENERGY_CONTENT_UNSUPPORTED` | false |
| Released Content 在同一 observation 时间出现多个匹配 Binding | 409 | `ENERGY_BINDING_AMBIGUOUS` | false |
| Core/IAM/Query Service 暂时不可用 | 503 | `REGISTRY_UNAVAILABLE` / 对应现有 unavailable code | true |
| Core/Query 返回无法验证的内部响应 | 502 | `REGISTRY_UPSTREAM_INVALID` / `ANALYTICS_RESPONSE_INVALID` | false |
| Energy Series 查询超时 | 504 | 现有 `ANALYTICS_TIMEOUT` | true |

Resolver 的无匹配不是 HTTP 404：它是 `200` 的 `NO_MATCH` 领域结果，因为没有 Binding 是一个可记录、可修复的数据内容状态；详情路由查不到 Meter 或 Binding 仍按现有 Registry 资源语义返回 404。

## 8. 对当前项目的直接修改要求

当前项目不能以“已有 Energy Series 所以 Energy Content 已完成”结案，至少有以下明确的 LOCAL-CHANGE：

| 当前状态 | 源码证明 | 需要修改 |
| --- | --- | --- |
| Core 没有 Meter/MeterBinding route | Core README、`server.go` route switch 和现有 OpenAPI 只覆盖 Site/Asset/Device 等 | 新增 Energy Content read model/presenter、RLS query、private routes 和 contract tests。 |
| Registry auth 没有 Meter/MeterBinding read action | `libs/registryauth/registry.go` 的 action 常量和 `ActionAllows` | 新增四个具体读动作，并更新 capability/ownership/OpenAPI；不复用 `registry.read` 作为万能权限。 |
| Energy Fact 当前实现仍按 telemetry key/ACCEPTED-only 处理 | #306 记录的 projector、ClickHouse candidate 和现有测试 | #308/后续实现规格让 Processing 使用 canonical Counter + resolver + released Binding；本票据不直接改代码。 |
| Binding cursor 的现有通用实现只建模 displayName/id | `cursor.go` 的固定 order 与 `server.go` 的 collection encoder | Binding 实现必须提供真实稳定排序字段和对应 cursor payload，不能把 `meterRole` 写入 `displayName` 位置后宣称合同一致。 |
| UI 只有 Site Energy Series client | `energy-analytics.ts` 和 `EnergyAnalytics.tsx` schema/call path | Administration 增加 Meter/Binding client；Energy Management 只消费固定 Content + Series contract，不自建聚合。 |

## 9. 后续票据边界

本票据关闭查询合同问题，但不批准实现细节：

- #308 已冻结 UI 三工作空间和 Meter/Binding 管理信息架构，详见 [`ui-workspace-information-architecture-v1.md`](ui-workspace-information-architecture-v1.md)；
- #310：首个 Energy Slice 的 schema、resolver SQL、Fact projector、rebuild/acceptance；
- 后续：Report、Cost、Carbon、Baseline、Virtual Meter 和 Space/Asset 维度必须各自给出产品查询合同和权限范围；
- 后续：单节点/集群部署和 HA 不在本票据内，沿用当前部署架构基线。

本票据的关键拒绝项是：不开放任意 Cube/SQL，不把 Telemetry Query 变成 Registry owner，不把 Meter、Space、Asset 作为当前 Energy Series 的可选万能 subject，不用 Current/Latest 或“最新配置”掩盖能源事实的事件时间和发布版本语义。
