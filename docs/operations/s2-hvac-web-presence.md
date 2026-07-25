# S2 HVAC Web Presence 与 Latest Telemetry Runbook

## 目的

Ticket 09 将 HVAC Web 的真实 Registry Device 页面接入 S2 current-state 公共契约。列表、详情和实时更新必须共享同一个 `DeviceObservationSnapshot` 状态模型，不允许用 Registry lifecycle、浏览器请求时间、Legacy token、ThingsBoard 直连、Socket.IO 或 Mock 数据推断设备当前状态。

## 读取边界

### 可见 Device 列表

- 仅对当前页面已加载并经过 UI 筛选的 Device 发起 bounded batch。
- 单批最多 100 个 Device。
- 每个 target 的 `keys` 必须为空数组；列表执行 Presence-only current-state 读取，不读取未展示的遥测点位。
- batch 必须保持 Organization、Site 和 Device scope 一致，并校验返回顺序、request ID 和 Device ID。
- partial result 按 Device 独立呈现：成功项继续显示权威 Snapshot，失败项显示 `UNAVAILABLE`，不得回退到其他数据源。

### Device 详情

详情只请求状态卡实际展示的 exact keys：

- `temperature`
- `humidity`
- `setpoint`
- `power`

Feature code 只能调用 `TelemetryLiveClient`，不得直接实例化或访问 Centrifugo transport。首次状态来自权威 Snapshot；后续 publication 只有在 revision 连续时才能更新同一 Snapshot 模型。

## 状态语义

UI 必须明确区分：

- `ONLINE`：权威 Presence 当前在线。
- `OFFLINE`：权威 Presence 当前离线。
- `STALE`：存在 Last Known 值，但 freshness 已过期。
- `UNKNOWN`：权威 evaluator 无法判断 Presence。
- `UNAVAILABLE`：平台 current-state evaluator 或其 owner dependency 当前不可用。
- `MISSING`：该 key 没有可接受值；绝不补零。
- `SUSPECT`：值存在，但质量策略标记为可疑，必须显示 reason。
- `revoked`：Device 已撤权或不可见；浏览器状态和 Last Known 必须清除。

所有 Last Known 值必须显示其原始 `sampledAt`。不得使用 Registry `updatedAt`、HTTP 响应时间或浏览器当前时间替代采样时间。

## 恢复与降级

- reconnect：保留最后一个完整 Snapshot，显示 transport degraded，不混合尚未连续应用的 publication。
- revision gap：显示需要重新同步；重新获取权威 Snapshot 后才恢复 current 状态。
- transport outage：明确显示实时 transport 暂不可用，可保留仍获授权的 Last Known Snapshot，但不得调用 Legacy/Mock fallback。
- platform `UNAVAILABLE`：与 transport degraded 分开显示，并展示 availability reason。

## 浏览器缓存清理

下列事件必须调用统一 purge：

- Organization 切换；
- Site 切换；
- route cohort/revision 变化；
- logout 成功；
- live session 进入 `revoked`；
- Registry 完整刷新后 Device 不再可见；
- Snapshot 或 live bootstrap 返回 `RESOURCE_NOT_FOUND`。

Purge 同时执行：

1. `TelemetryLiveClient.purge()`，撤销 session 并清除 recovery store；
2. 删除 `s2-current` React Query 数据；
3. 删除不可再访问 Device 的详情 URL 和页面状态。

## 禁止项

生产 real mode 禁止：

- `getToken()`、localStorage bearer token 或自定义 Authorization header；
- `X-Organization-ID`、`X-Site-ID`、role/admin 等浏览器权威 header；
- `/ws/telemetry`、Socket.IO、ThingsBoard direct access；
- Legacy current-state request fallback；
- Mock business data fallback；
- 未请求 key 进入 store 或 publication；
- MISSING 值补零；
- Snapshot 与存在 revision gap 的 publication 混合成伪 current state。

## 可运行证据

```bash
npm run s2:hvac-web:check
npm run s2:hvac-web:browser
npm run s2:ticket-09
```

Ticket 09 输出：

- `out/s2-ticket-09/hvac-web-presence.json`
- `out/s2-ticket-09/browser-journey.json`
- `out/s2-ticket-09/network-audit.json`
- `out/s2-ticket-09/state-rendering.json`

浏览器 journey 必须覆盖：两个 Organization、sibling Site、partial batch、live update、reconnect、revision gap、transport outage、revocation、route cohort purge、a11y 和 no-fallback 网络审计。

## 发布判定

仅在以下条件全部满足时允许合并：

- Production build 与 TypeScript 检查通过；
- 生成的 S2 HTTP 契约未漂移；
- Ticket 07 live client 和 Ticket 08 routing 门禁通过；
- 浏览器 journey 结论为 `passed`；
- 所有 forbidden header、Legacy/Mock route、ThingsBoard、Socket.IO 调用计数为 0；
- MISSING 补零、混合 Snapshot/publication frame、请求时间伪造采样时间计数为 0。
