# S2 安全负向、可观测性与脱敏 Runbook

## 目标

Ticket 10 把 S2 的安全零不变量、低基数 metrics、W3C trace correlation、HMAC reference、告警 ownership 和 observability outage 行为固化为自动门禁。任何报告都由测试与 harness 生成，不允许人工填写零值。

## security zero invariant

以下计数必须始终为 0：跨 Organization/Site delivery、hidden Device disclosure、forged scope/channel、unauthorized key、post-revocation delivery、Cursor replay/scope expansion、non-owner write、Legacy/Mock request fallback、未检测的 Business Revision gap。

任一计数非零时立即触发 `S2SecurityZeroInvariantViolation`；该告警 `for: 0m`，不等待 error-budget window。Primary owner 为 platform-security，secondary owner 为 telemetry-platform。

## 低基数 metrics

指标目录位于 `deploy/s2/observability/metric-catalog.v1.json`。每个 family 必须满足：

- `hvac_s2_` namespace；
- counter 使用 `_total`，时间使用 `_seconds`，比例使用 `_ratio`；
- label 只能来自 operation、outcome、reason_family、phase、cohort、dependency、transport、route_stage 等有界集合；
- Organization、Site、Device、subscription、Cursor、Revision、key、value、token、channel 和请求/追踪原始 ID 不得成为 label；
- 每个 family 有显式 series budget，最大组合和 observed cardinality 都不得超预算。

## HMAC correlation reference

Operational logs、spans 和 evidence 只允许环境 HMAC-SHA256 派生 reference：`request_ref`、`event_ref`、`subscription_ref`、`revision_ref`。HMAC key 至少 32 bytes，不写入日志、trace、artifact 或 image。原始 ID 只在访问受控的 Audit Ledger 中按业务要求保存。

禁止在 Baggage 中传播 tenant、Device、subscription、Cursor、Revision 或 telemetry value。允许 W3C `traceparent`/`tracestate`。

## 脱敏

下列原始字段或内容在 operational log、span、artifact 和 image 中必须为零：Authorization、Cookie/Set-Cookie、CSRF、token、raw Cursor、channel、telemetry value、source credential、password 和 secret。验证器同时检查字段名与常见值 marker；敏感关联仅使用 HMAC reference。

## trace correlation

证据链必须包含同一 Trace ID 下的：

1. platform-gateway；
2. iam-service；
3. telemetry-runtime-service；
4. outbox-relay；
5. centrifugo-api；
6. telemetry-live-client；
7. audit-ledger-service。

parent span 必须连续，并携带 request/event/subscription/revision HMAC reference。不得携带 raw channel、Cursor、value 或身份授权材料。

## collector / metrics backend outage

Exporter 使用有界异步队列。Collector 不可用、export timeout 或 queue 满时：

- 业务 transaction 必须继续完成；
- Export failure/drop 只增加低基数 exporter metric；
- 不同步重试、不阻塞数据库 transaction；
- last error 只记录枚举 code，不记录 endpoint credential 或 payload；
- outage report 必须证明 business transaction 未阻断且敏感值出现次数为 0。

## 告警处理

### snapshot-slo / snapshot-age

检查 Gateway 与 Telemetry Runtime、PostgreSQL owner、route stage 和 Snapshot age。确认不存在 authority bypass；需要回退时仅执行 route revision rollback，不做 request-level Legacy fallback。

### ingest-lag / presence-lateness / upstream

检查 ThingsBoard / MQTT source adapter、reconciliation、quarantine 和 source lag。原始 Device/key/value 不进入告警 label，详细对象通过 Audit Ledger 与 HMAC reference 定位。

### outbox-lag / publication-lag / recovery / subscription / revocation / slow-consumers

检查 outbox lease、Redis、Centrifugo、publication history、server-side unsubscribe 和 client Snapshot recovery。任何 unknown recovery 或 Revision gap 强制 fresh Snapshot。

### redis / postgres / exporter

依赖故障告警分别路由到 primary/secondary owner。Exporter 故障不得升级为业务失败；Redis/PostgreSQL 故障不得触发 Legacy/Mock fallback。

## 可运行门禁

```bash
npm run s2:security-observability:check
npm run s2:security-negative
npm run s2:observability:harness
npm run s2:security-observability
```

输出：

- `out/s2-security-observability/security-negative-report.json`
- `out/s2-security-observability/zero-invariant-report.json`
- `out/s2-security-observability/metric-cardinality-report.json`
- `out/s2-security-observability/trace-correlation-report.json`
- `out/s2-security-observability/log-redaction-report.json`
- `out/s2-security-observability/alert-rule-validation-report.json`
- `out/s2-security-observability/observability-outage-report.json`

相同报告同步写入 `out/s2-release-evidence/`，供后续容量与 release ticket 复用。
