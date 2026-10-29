# 告警处理手册

Prometheus 的告警规则在 `infra/observability/alerts/`，每条规则的 `runbook` 链接到本文的一节（`npm run observability:phase1:check` 检查每个链接都能找到标题）。改标题时，同时改告警规则里的链接。

## 先说清楚的几条

- 指标的标签只能是有限的几种取值（操作、结果、原因类别、依赖等）。租户、站点、网关、设备、点位、指令、告警、请求、追踪、消息的编号不进 Prometheus 标签，要查具体对象，用受控的日志、追踪、操作记录或数据库。指标目录在 `deploy/observability/phase1-metric-catalog.v1.json`，新标签合并前必须是有限取值。
- 日志、追踪和导出的报告里不能出现：授权头、Cookie、CSRF、令牌、原始的推送游标和频道、读数的值、数据源凭据、密码和密钥。需要关联时只用环境密钥算出的 HMAC 编号（`request_ref`、`event_ref`、`subscription_ref`、`revision_ref`）。
- 诊断端口默认只绑本机，只提供 `/health/live`、`/health/ready`、`/metrics` 和 `/diagnostics`，不返回凭据、连接串、证书、授权、会话 cookie 或原始错误。
- 处理告警时，不能靠关掉双向 TLS、放宽 MQTT 主题权限、关掉 QoS 1 或手工确认来「消除」告警，也不能手工删数据库文件、改只追加的记录。

信号大致这样串起来：

```text
现场网关（EG8200 / Node-RED）
  -> 本地待发队列和 MQTT 连接：hvac_edge_mqtt_*
  -> 云端接入服务：hvac_mqtt_*
  -> 遥测接收：hvac_s2_source_lag_seconds
  -> 读数质量：hvac_s2_data_quality_records_total
  -> 实时推送：hvac_s2_outbox_* / hvac_s2_publication_*

指令接口 -> hvac_command_http_*
  -> 下发：hvac_command_dispatch_results_total
  -> 核对：hvac_command_verifications_total
  -> 核对耗时：hvac_command_verification_duration_seconds

告警接口 -> hvac_alarm_http_*
```

还没有指标的东西，不在这里假装有：Modbus 采集轮询、网关的 CPU 和内存、按点位应有的上报频率算的完整率。

## 网关和接入

### 网关的 MQTT 断开

告警：`EdgeMQTTPublisherDisconnected`。

1. 看 `hvac_edge_mqtt_connected` 和连接结果的计数。
2. 重启发布程序之前，先看网关本地的待发队列。
3. 检查 MQTT 服务器能不能连上、客户端证书是否有效、按网关划分的主题权限。
4. 云端接入服务正常，不代表网关这边连着。

### 网关的待发队列过高

告警：`EdgeStoreForwardQueueHigh`、`EdgeStoreForwardQueueRejected`。

1. 看 `hvac_edge_mqtt_queue_bytes`、`hvac_edge_mqtt_queue_capacity_bytes`、`hvac_edge_mqtt_queue_utilization_ratio`。
2. 网关断网时占用在涨：先恢复网络和 MQTT 服务器的连接。
3. 网关连着、占用还在涨：看云端接入服务的队列压力和遥测接收延迟。
4. `hvac_edge_mqtt_queue_limit_rejections_total > 0` 表示队列已经满了、数据有丢的风险，按需要立即处理的告警对待。
5. 加大队列要先看容量和磁盘余量，不能代替修好堵住的投递链路。

### 接入服务的 MQTT 断开

告警：`MQTTTelemetryAdapterDisconnected`。

1. 看 `hvac_mqtt_connected` 和 `hvac_mqtt_subscribed`。
2. 看 `hvac_mqtt_connections_total{outcome="failed"}` 和 `hvac_mqtt_disconnections_total`。
3. 检查 Mosquitto 是否正常、证书是否有效、主题权限配置。
4. 重启任何组件之前，先看网关的待发队列。

### 接入服务的处理队列过高

告警：`MQTTProcessingQueueHigh`。

1. 对比 `hvac_mqtt_processing_queue_depth` 和 `hvac_mqtt_processing_queue_capacity`。
2. 看 `hvac_mqtt_message_retries_total`，是不是下游遥测有压力。
3. 看遥测 `dependency="mqtt"` 的接收延迟和 PostgreSQL 的依赖指标。
4. 队列涨是因为下游慢，就先恢复下游，再考虑加大队列；加大队列要重新做持续负载验证。

### 接入服务丢弃消息

告警：`MQTTTelemetryDropped`。

`outcome="dropped"` 只用于永远处理不了的消息，例如格式错误或无权的信封。看接入服务结构化日志里有限的错误原因，用受控的记录去关联。平时诊断不要把高频读数的完整内容打进日志。

### 接入服务反复重试

告警：`MQTTDownstreamRetrying`。

1. 看遥测是否可用、接收读数的耗时。
2. 看 PostgreSQL 和历史 / 待发事件这些依赖。
3. 确认重试能恢复、MQTT 队列没有被占满。临时失败时接入服务故意不确认消息、原地重试，不要为了消掉告警关掉 QoS 1 或手工确认。

### 收不到任何读数

告警：`TelemetryIngestStopped`：15 分钟没有收到任何读数。

按顺序查：网关（Node-RED 的状态和待发队列）、主机 8884 端口到 Mosquitto 的转发、`connectivity` 的日志、`telemetry-worker`。网关只是读不到现场总线（Modbus 超时）时，它仍然连着，但什么都不发。

### 读数接收延迟

告警：`S2IngestSourceLagHigh`。

检查 MQTT 数据源的适配、对账、隔离和数据源延迟。原始的设备、点位和值不进告警标签；具体对象通过操作记录和 HMAC 编号定位。

### 读数合格率低

告警：`S2DataGoodRateLow`。

`hvac_s2_data_quality_records_total` 记录有限的质量和接收结果，现在能报合格、可疑、拒收的比例。完整率和采样合规还算不出来，因为平台还不知道每个点位应有的上报频率。

合格率下降时：

1. 按质量和结果拆开看，不按设备或点位拆。
2. 看接收延迟和隔离比例。
3. 用操作记录和受控的数据库查询找出受影响的站点、设备、点位。
4. 先查点位对照和绑定版本，再当作传输丢失。

2026-10-15 松雅轩合格率降到约 33%，原因是网关的待发队列把没确认的记录反复重发、平台把重复的算作不合格；2026-10-16 网关改成一次采样只发一个信封后已经恢复（规格 `docs/specs/site-gateway-onboarding.md`「已知问题」）。

### 上游数据源不可用

告警：`S2UpstreamUnavailable`。处理方法同「读数接收延迟」。

### 在线状态判断延迟

告警：`S2PresenceLate`。处理方法同「读数接收延迟」。

## 实时推送

### 快照接口出错

告警：`S2SnapshotErrorBudgetBurn`。

检查网关和遥测、PostgreSQL、接口归属的版本和快照的数据时间。确认没有绕过权限；需要回退时只回退接口归属的版本，不做按请求的回退。

### 快照数据过旧

告警：`S2SnapshotAgeHigh`。处理方法同「快照接口出错」。

### 推送待发队列延迟

告警：`S2OutboxLagHigh`。

检查待发事件的租约、Redis、Centrifugo、推送的历史、服务端取消订阅和页面重新取快照。任何不明的补发或状态版本号的缺口，都强制页面重新取一次完整快照。

### 推送延迟

告警：`S2PublicationLagHigh`。处理方法同「推送待发队列延迟」。

### 断线补发失败

告警：`S2RecoveryFailureRateHigh`。处理方法同「推送待发队列延迟」。

### 订阅失败

告警：`S2SubscriptionFailure`。处理方法同「推送待发队列延迟」。

### 页面接收太慢被断开

告警：`S2SlowConsumerDisconnects`。处理方法同「推送待发队列延迟」。

### 撤销授权后仍有推送

告警：`S2PostRevocationDelivery`。按「安全计数不为零」处理：撤销授权之后还把数据推给了这个订阅，属于越权。

### 安全计数不为零

告警：`S2SecurityZeroInvariantViolation`、`S2RequestFallbackDetected`。告警等待时间为 0，一出现就报，主负责是平台安全；次负责分别是遥测和网关。

下面这些计数必须一直是 0：跨租户或跨站点推送、泄露隐藏的设备、伪造的范围或频道、无权的点位、撤销后仍推送、推送游标被重放或扩大范围、不是负责方的写入、按请求退回旧系统或假数据、没发现的状态版本号缺口。任何一个不是 0，立刻查是哪条推送或请求、哪个范围，修好之前不要恢复相关的推送路径。

## 依赖

### 历史读数写不进 ClickHouse

没有单独的告警；日志里 `telemetry-worker` 每 5 秒记一次 `telemetry_history_projection_failed`，设备历史曲线和用电量都停在最后写进去的时间。

写历史的程序一直重试同一批，直到 ClickHouse 收下（批次按顺序写，后面的批次在它后面排队），原因修好之后会自己补上，不用改库。日志里的错误是脱敏的，原因去 ClickHouse 查：`SELECT event_time, exception FROM system.query_log WHERE user = 'telemetry_history_writer' AND exception != '' ORDER BY event_time DESC LIMIT 5`。排队的批次和失败次数在 `telemetry_runtime.telemetry_history_outbox`（`delivery_state`、`attempts`、`last_error_code`）。2026-10-19 的原因是主机磁盘满了（见「主机磁盘和 PostgreSQL 的 WAL」）。

### Redis 不可用

告警：`S2RedisUnavailable`。依赖故障的告警按主、次负责人分别通知。Redis 只做缓存和推送，可以重建；它坏了不能触发退回旧系统或假数据。

### PostgreSQL 不可用

告警：`S2PostgresUnavailable`。处理方法同「Redis 不可用」：恢复数据库，不退回旧系统或假数据。

### 指标导出失败

告警：`S2ExporterFailure`。

指标和追踪的导出用有上限的异步队列。收集器不可用、导出超时或队列满时，业务事务照常完成，只是导出失败或丢弃的计数增加；不同步重试，不阻塞数据库事务，最后的错误只记一个代码。

1. 确认公开和内部的业务接口都正常（导出明确不阻塞业务）。
2. 看 `/diagnostics` 里 `droppedSpans`、`failedExports` 是否在涨。
3. 恢复固定版本的 OpenTelemetry 收集器，确认导出失败的计数不再增加。
4. 加大队列要先评估内存。

### 追踪收集器不可用

告警：`S0TelemetryExportFailures`。处理方法同「指标导出失败」。

## 能耗

### 用电量计算停住

告警：`AnalyticsEnergyProjectionFailing`：把电表读数算成用电量的程序（在 `telemetry-worker` 里）已经失败 15 分钟、一直在重试同一批，所以所有电表都没有新的用电量，能耗页面停在最后算出的那一段。

日志里的错误是脱敏的（`analytics_energy_projection_failed`）。从处理进度（`analytics.energy_projection_checkpoints`）和 `telemetry_history.counter_arrivals` 接下来的几行找出是哪一批，再写个测试复现。2026-10-14 的原因是有一个累计读数点位没有计量绑定（#551）。

## 指令和告警接口

### 指令核对失败

告警：`CommandVerificationFailureRateHigh`。

1. 按结果看 `hvac_command_verifications_total`。
2. 把读回的值不对（`mismatch`）、超时（`timed_out`）、无法判断（`inconclusive`）和接口出错分开。
3. 在受控的地方按 `command_id` 查指令记录和下发证据；不要把 `command_id` 加进 Prometheus。
4. 顺着这条链查：授权 → 下发 → 网关的证据 → 设备确认 → 读回状态 → 核对。

### 指令核对超时

告警：`CommandVerificationTimeoutRateHigh`。

1. 看 MQTT、网关和设备是否可用。
2. 看读回状态的新鲜度和快照的数据时间。
3. 拿 `hvac_command_verification_duration_seconds` 的 P95/P99 和核对时限比。
4. 超时是一个控制结果，不只是一次接口 5xx。

### 指令接口出错

告警：`CommandAPI5xxHigh`。

1. 按接口拆 `hvac_command_http_requests_total`。
2. 按出错的操作看 PostgreSQL、IAM 和遥测这些依赖。
3. 用 W3C 追踪编号跨服务查。
4. 预期内的 4xx（无权、风险超限被拒）不算服务故障。

### 告警接口出错

告警：`AlarmAPI5xxHigh`。

1. 按告警接口拆开看。
2. 看告警的 PostgreSQL 访问和响应校验错误。
3. 需要时用追踪编号关联到平台网关和操作记录。

### 告警操作被拒绝

告警：`AlarmMutationRejectionRateHigh`。

4xx 多，可能是用户越权，也可能是策略和客户端对不上。按操作（确认、指派、屏蔽、关闭等）看分布，再看授权和操作记录。只是 4xx 涨，不要找数据库的人。

## 主机磁盘和 PostgreSQL 的 WAL

`Phase1HostDiskUsageWarning`（超过 80%）、`Phase1HostDiskUsageHigh`（超过 85%，要有人看到）、`Phase1HostDiskUsageCritical`（超过 90%）按容量问题处理，不是手工删数据库文件的理由。`Phase1HostDiskWillFillWithin24Hours` 按最近两小时的剩余空间趋势，预计 24 小时内会写满就提前报警：在到 80% 之前，先找增长最快的持久数据。Prometheus 自己的历史最多留 15 天，并且块数据不超过 2GB（`--storage.tsdb.retention.size=2GB`），避免它自己把盘写满。

1. 本机 PostgreSQL：对比数据目录和 `pg_wal` 的大小。`pg_wal` 比表数据大很多，多半是 WAL 回收被堵住了。
2. 开着 `POSTGRES_ARCHIVE_MODE=on` 时，看 `pg_stat_archiver` 和归档目录。归档反复失败要在归档目录那边修，绝不能手工删 `pg_wal` 里的文件。
3. 开发和测试默认关掉 WAL 归档；预发和正式环境只有在有可写的、容量受管理的、和数据卷分开的存储时才打开。

服务器上运行时数据的清理（#553）会删已送达的历史事件和 7 天前的原始读数，见 `docs/HANDOFF.md`。

### 主机指标采集中断

告警：`Phase1HostMetricsUnavailable`。`node-exporter` 两分钟没有被抓到。先看这个容器在不在、`/host/textfile` 目录在不在（目录不存在时它会起不来），再看 Prometheus 能不能解析 `node-exporter:9100`。

### 主机内存和交换分区

告警：`Phase1HostMemoryAvailableLow`（可用内存连续 15 分钟低于 200MiB）、`Phase1HostSwapPressure`（有交换分区且已用超过一半，连续 15 分钟）。

这台机器大约 3.6GiB 内存，容器限额加起来比物理内存大，所以不要把「用了 80%」当成故障。只有可用内存掉到 200MiB 以下，或交换分区用过一半，才说明已经在挤。

1. `docker stats --no-stream` 看哪个容器的内存最高。
2. 先停不在对外服务的容器（模拟器、Tempo）。Tempo 只在 `observability-full` 档，这台正式机器不要开这一档。
3. 不要为了腾内存去删 PostgreSQL 或 ClickHouse 的数据目录。

### 容器反复重启或不健康

告警：`Phase1ContainerRestartLoop`（30 分钟内重启超过 3 次）、`Phase1ContainerUnhealthy`（健康检查失败满 5 分钟）、`Phase1ContainerMetricsMissing`（主机脚本读不到 Docker）。

指标由主机上的 `hvac-host-metrics.timer` 写进 node-exporter 的 textfile 目录，一分钟一次。

1. `docker ps -a --filter label=com.docker.compose.project=hvac-platform` 看是哪一个服务。
2. `docker logs --tail 80 <容器>`。日志里的 `error` 字段是 `[REDACTED]`，要看原因就按 `AGENTS.md` 写一个直接调用那个函数的测试，或临时打 `[DEBUG-xxxx]`。
3. 健康检查失败而进程还在：多半是依赖（数据库、证书）没准备好，先看同时间的数据库和证书告警。
4. 不要为了消掉重启次数把 `restart: unless-stopped` 改掉。

### 备份太旧

告警：`Phase1BackupTooOld`（成功时间早于 26 小时）、`Phase1BackupMissing`（还没有一份）、`Phase1BackupMetricMissing`（三小时都没有这个指标）。

夜间备份是 `hvac-backup.timer`（每天 03:30，上海时间）。成功之后 `run-scheduled-backups.sh` 把当前时间写到运行目录的 `observability/postgres-backup.success` 和 `observability/clickhouse-backup.success`。还没有这两份标记时，指标退回用备份目录里最新一份的修改时间；目录也没有就是 0。

26 小时是「每天一次，再留两小时」。例如昨天 03:30 成功，今天 05:30 还没有新的成功标记，就会报。

1. `systemctl status hvac-backup.timer hvac-backup.service` 看昨晚有没有跑、有没有失败。
2. 失败日志在 `journalctl -u hvac-backup.service -n 80`。
3. 磁盘满是常见原因，先处理「主机磁盘和 PostgreSQL 的 WAL」。
4. 指标一直缺失：确认 `hvac-host-metrics.timer` 已安装并在跑，而且 node-exporter 挂了 `observability/textfile`。

### 公网入口和证书

告警：`Phase1PublicEndpointDown`（`/healthz`、`/sign-in` 或 `/identity/.well-known/openid-configuration` 两分钟没有 200）、`Phase1TlsCertificateExpiring`（证书剩余不到 14 天）、`Phase1TlsCertificateExpiresSoon`（不到 3 天）。

探测从 `blackbox-exporter` 访问 `https://hvac.swayingwindmill.online`。登录页是 `/sign-in`，不是 `/login`。

1. 在服务器上 `curl -fsS --resolve hvac.swayingwindmill.online:443:127.0.0.1 https://hvac.swayingwindmill.online/healthz`，期望正文是 `ok`。
2. 证书是 Let's Encrypt。看到期日：`echo | openssl s_client -servername hvac.swayingwindmill.online -connect 127.0.0.1:443 2>/dev/null | openssl x509 -noout -enddate`。
3. 续期由主机上的证书工具完成（仓库里没有 certbot 配置）。续期后把新的 `fullchain.pem` 和私钥放到主机 nginx 正在用的目录，再重载主机 nginx。不要把私钥提交进仓库。
4. 只换了容器里的网页、没动主机 nginx 时，公网探测失败要先看主机 nginx 到 `127.0.0.1:18443` 的转发。

### PostgreSQL 进程不可用

告警：`Phase1PostgresDown`。`postgres-exporter` 抓不到，或它连不上数据库（`pg_up == 0`）。这和「PostgreSQL 不可用」（业务进程报的依赖）不是同一条：那条看的是遥测进程的探测，这条看的是数据库自己。

1. `docker ps --filter name=postgres` 看容器在不在、是不是在重启。
2. 日志里出现 `No space left on device` 或 `PANIC`：先按「主机磁盘和 PostgreSQL 的 WAL」腾出空间，再启动。不要手工删 `pg_wal` 里的文件。
3. 导出器在、数据库不在：看 `DATA_SOURCE_URI` 是不是还指向 `postgres:5432`。密码用的是 `POSTGRES_ADMIN_PASSWORD`，由导出器自己做转义，不写进仓库。

### ClickHouse 不可用

告警：`Phase1ClickHouseDown`。Prometheus 抓不到 ClickHouse HTTP 端口上的 `/metrics`。

1. `docker ps --filter name=clickhouse`。
2. 在 ClickHouse 容器里 `clickhouse-client --query 'SELECT 1'`。
3. 磁盘满时 ClickHouse 会拒绝写入。腾出空间后它会自己接受新的写入；历史读数的补写由遥测重试，不用改库。
4. 指标挂在已经给内部服务用的 8123 端口上，没有单独映射到主机。

### Redis 进程不可用

告警：`Phase1RedisDown`。Redis 只做缓存和实时推送，坏了可以重建，不能用旧数据或假数据顶上。设备当前读数在 Redis 没有时会改读 PostgreSQL 里的快照。

1. `docker ps --filter name=redis`，`docker exec <redis> redis-cli ping` 期望 `PONG`。
2. 这台机器上的 Redis 没有密码。导出器地址是 `redis://redis:6379`。

### MQTT 服务器连不上

告警：`Phase1MqttDown`。从 `blackbox-exporter` 连 `mqtt-broker:8883` 的 TCP 失败。没开 `integration` 档位的环境会一直亮着这条，正式机器开着。

1. `docker ps --filter name=mqtt`。
2. 现场网关走主机 `8884`，由主机 nginx 转到 Mosquitto。容器端口通、网关仍不上报时，再看主机 `8884` 和证书。
3. 接入服务自己的「接入服务的 MQTT 断开」仍然有效：那条看的是接入进程，这条看的是 Mosquitto 端口。

### 平台进程没有指标

告警：`Phase1CoreProcessDown`（平台网关、身份、调度、维护、遥测、指标，两分钟）、`Phase1ProcessDown`（接入、预测、优化、故障诊断、节能验证、报告、AI 助手、Centrifugo，五分钟，警告）。

抓的是各进程诊断端口上的 `/metrics`。预测、优化、故障诊断、节能验证、报告的诊断端口同时提供 `/healthz` 和 `/metrics`。AI 助手的诊断端口提供 `/health/ready` 和 `/metrics`。

1. `docker ps` 看对应容器是不是 `unhealthy` 或 `Restarting`。
2. 没启用 `intelligence` 或 `integration` 时，对应的警告会一直在。正式机器这两档都开着。
3. 核心进程的告警是要有人看到的（`page`）。先恢复进程，再看它依赖的数据库告警。

### 定时任务

告警：`SchedulerNotRunning`、`JobQueueAgeTooHigh`、`DeadJobDetected`、`RepeatedJobFailure`、`LeaseExpirySpike`。

调度器只负责到点把任务交给执行的程序。队列变老或任务进了 DEAD，先看调度器日志和它的 PostgreSQL，再看对应的执行程序（指标、预测、优化）。不要在库里把 DEAD 改回 READY 来消告警；先把失败的原因修掉。

演示站点的定时任务是停用的（模拟器没开）。那不会让调度器本身的 `up == 0`。

## 告警怎么送到人

Prometheus 把告警交给 Alertmanager。Alertmanager 和把告警转成企业微信消息的 `wecom-alert-adapter` 在 `alert-egress` 上，只有它们能访问外网。仓库里的 `deploy/platform/phase1/observability/alertmanager.yaml` 仍然是空接收方（`receiver: none`）。容器启动时用 `render-alertmanager-config.sh` 看运行目录里的文件：什么都没放，就原样用这个空接收方，告警留在 Alertmanager 里，不发出去。

密钥放在 `${PHASE1_RUNTIME_DIR}/alert-receiver/`（正式机器上是 `deploy/platform/phase1/runtime/server/alert-receiver/`）。这个目录在被忽略的 `runtime/` 下面，不要提交。样例在 `deploy/platform/phase1/observability/alert-receiver.example/`。

企业微信群机器人：把样例 `wecom-webhook-url.example` 复制成 `wecom-webhook-url`，里面只留一行 `https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=...`。适配器每次发送时读这个文件，Alertmanager 的配置里只有内网地址 `http://wecom-alert-adapter:9095/alertmanager`，不会出现 key。

邮件：把 `smtp.env.example` 复制成 `smtp.env`，把 `smtp-password.example` 复制成 `smtp-password`（文件内容就是密码，不要加引号）。465 端口一连上就是 TLS，样例里 `SMTP_REQUIRE_TLS=false` 就是这种连法。密码只出现在 `smtp-password` 里，生成的配置用 `smtp_auth_password_file` 指过去。

两个都配了就两条一起发。只配一个就只发那一个。改完文件后重建这两个容器：

```bash
node scripts/phase1-wsl-compose.mjs --integration --intelligence up -d --no-deps alertmanager wecom-alert-adapter
```

`page` 和 `critical` 每小时再提醒一次，其他严重程度每 4 小时一次。恢复时也会发。

Prometheus 的抓取配置用仓库里的 `deploy/platform/phase1/observability/prometheus.yaml`。`wsl.override.yaml` 把这份和告警规则目录挂进容器，不再读运行目录里手改的那份。
