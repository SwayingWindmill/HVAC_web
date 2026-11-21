---
date: 2026-11-21
category: 改进
---
- 补齐设备接入与下发控制网关的安全性、多租户调度与配置影子闭环能力（涉及 `modules/connectivity`、`modules/command`、`modules/registry`、`libs/commandmodel`、`libs/edgecontrol`、`cmd/connectivity`、`tools/eg8200-simulator`、`edge/node-red/`、`deploy/platform/phase1/nginx/`、`infra/telemetry/mqtt/` 与迁移 `command-due-work-tenants.sql`）：
  - **MQTT 接入限流与最小权限收口**：在 `infra/telemetry/mqtt/mosquitto.conf` 配置单机最大连接数 `2048`、保活上限 `120` 秒、单连接在途消息上限 `32` 与系统日志主题输出；在 `infra/telemetry/mqtt/acl` 收紧 `mqtt-telemetry-adapter` 的订阅范围，移除控制回复主题 `hvac/v1/+/up/reply` 读权限并新增配置影子同步主题 `down/shadow`（写）与 `up/shadow`（读）及 `$SYS/broker/log/N`（读）。
  - **代理断线通知、独立可选 mTLS 检查网关与客户端持有证明**：`modules/connectivity/pkg/adapter/broker_log.go` 解析 `$SYS/broker/log/N` 断线通知联动触发网关离线与超时清理；新增可选 `mqttguard` 检查服务（`CONNECTIVITY_MQTT_GUARD_ADDR`）向边缘网关签发并强制校验短暂上游凭证、在吊销证书时立即断开旧连接；`deploy/platform/phase1/nginx/nginx.conf`、`cert_pop.go`、`eg8200-simulator` 与 `edge/node-red/` 增加 `X-Gateway-Cert-Pop-*` 请求签名与防重放校验，并收紧 Nginx 代理头校验。
  - **多租户控制指令唤醒与解耦执行池**：`modules/command` 与 `cmd/connectivity` 改用轻量租户发现视图与即时通知信号替代固定租户轮询，将指令下发线程池（`16` 并发）与读后核对线程池（`8` 并发）拆分解耦，并在 `mqttconnector` 中打通全链路 `Tracer` 追踪片段。
  - **版本前向兼容、强类型元信息与模板自动生成影子驱动**：上行编解码对 `schemaVersion: "2.0"` 维持严格字段检查，同时放行 `2.1` 及以上小版本的未知扩展字段；用 `commandmodel.ParseCommandPointMetadata*` 与 `edgecontrol.ParseEdgePointSourceMetadata` 替代多处原始字典类型断言；模板激活支持将 `modbusDriver` 随 `registry.template-assignment.changed` 与 `registry.binding.rebound` 异步写入目标网关的期望配置影子（未变更不递增修订号）。
  - **配置影子 MQTT 双向同步**：定义遵循 `edge-replication-envelope.v1.schema.json` 的 `CHANGE_BATCH`（`hvac/v1/{gatewayId}/down/shadow`）与 `CHANGE_ACK`（`hvac/v1/{gatewayId}/up/shadow`）报文及 `hvac_mqtt_shadow_replication_total` 监控指标；接入服务在期望配置更新后主动下发同步通知并在收到边缘结果后记录在线与指标，模拟器同步订阅下发主题完成校验、应用与结果上报。
