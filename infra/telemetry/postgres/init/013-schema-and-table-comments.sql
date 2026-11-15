\set ON_ERROR_STOP on

BEGIN;

SET LOCAL ROLE s2_telemetry_migrator;

COMMENT ON SCHEMA telemetry_runtime IS '实时读数接收、在离线判定、质量评估与订阅分发域，由遥测运行时服务负责写入。';
COMMENT ON TABLE telemetry_runtime.device_observation_snapshots IS '按设备汇总的观测可用性、就绪度与综合展示状态评估快照，由遥测运行时服务更新。';
COMMENT ON TABLE telemetry_runtime.device_presence IS '设备在线、离线评估状态、最后活跃时间与最近已知事实，由遥测运行时服务更新。';
COMMENT ON TABLE telemetry_runtime.devices IS '遥测运行时本地维护的设备所属租户与站点作用域索引，用于写入鉴权与多租户隔离。';
COMMENT ON TABLE telemetry_runtime.freshness_policies IS '按设备与点位键配置的新鲜度判定超时阈值及单点质量检查规则，由遥测运行时服务维护。';
COMMENT ON TABLE telemetry_runtime.iam_revocation_cursors IS '遥测运行时追赶 IAM 权限吊销事件的消费序列号游标，由遥测运行时服务更新。';
COMMENT ON TABLE telemetry_runtime.ingest_quarantine IS '未匹配注册表或校验异常而被隔离的原始点位读数记录，由遥测运行时服务写入。';
COMMENT ON TABLE telemetry_runtime.latest_accepted_telemetry IS '每个设备点位最新一条通过校验接收的有效点位读数、新鲜度与质量原因，由遥测运行时服务更新。';
COMMENT ON TABLE telemetry_runtime.observation_coverage IS '设备连续观测覆盖起止状态及中断原因标记，由遥测运行时服务更新。';
COMMENT ON TABLE telemetry_runtime.points IS '遥测运行时本地索引的设备点位唯一标识、数值类型与计量单位定义。';
COMMENT ON TABLE telemetry_runtime.presence_policies IS '按设备配置的在线判定窗口、离线判定期限与覆盖率要求，由遥测运行时服务维护。';
COMMENT ON TABLE telemetry_runtime.presence_signals IS '来自站点网关或上游通道的设备在线信号流水，由遥测运行时服务写入。';
COMMENT ON TABLE telemetry_runtime.probe_offset_estimates IS '冷站供回水配对温度探头空闲时段零点偏差估计值与均值统计，仅作人工核对参考且不自动回写读数，由遥测运行时服务写入。';
COMMENT ON TABLE telemetry_runtime.probe_pair_windows IS '冷站配对温度探头尚未结束的空闲窗口统计状态，窗口结束后删除并生成探头零点估计，不直接修改读数，由遥测运行时服务更新。';
COMMENT ON TABLE telemetry_runtime.probe_pairs IS '站点供回水配对温度探头的配对关系与空闲零点估计配置参数，由遥测运行时服务维护。';
COMMENT ON TABLE telemetry_runtime.recovery_cursors IS '实时读数订阅客户端断线重连时的传输偏移量恢复游标，由遥测运行时服务维护。';
COMMENT ON TABLE telemetry_runtime.source_delivery_evidence IS '来自站点网关或上游通道的重复及乱序投递、重放原因与载荷摘要证据流水，由遥测运行时服务写入。';
COMMENT ON TABLE telemetry_runtime.source_observations IS '从上游协议通道接收的原始点位读数流水及受理判定标记，由遥测运行时服务写入。';
COMMENT ON TABLE telemetry_runtime.source_positions IS '各上游数据源分区的当前消费偏移量与最新事件位点，由遥测运行时服务更新。';
COMMENT ON TABLE telemetry_runtime.telemetry_history_outbox IS '待写入时序历史库的历史读数批次发件箱，由遥测历史写入服务消费。';
COMMENT ON TABLE telemetry_runtime.telemetry_publication_outbox IS '向实时推送中继和订阅消费者分发的读数变更事件发件箱，由遥测中继服务消费。';
COMMENT ON TABLE telemetry_runtime.telemetry_subscriptions IS '客户端建立的设备点位实时读数订阅授权作用域与生命周期状态，由遥测中继服务维护。';

RESET ROLE;

COMMIT;
