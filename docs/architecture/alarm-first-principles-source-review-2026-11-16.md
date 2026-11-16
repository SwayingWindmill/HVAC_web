# 告警与遥测可信边界重构：对照 ThingsBoard、OpenEMS 与 MyEMS（2026-11-16）

松雅轩现场暴露出四类连锁误判：C/D 机组总馈线电表混入约 30 kW 水泵/冷却塔辅助负荷导致单泵循环误判为冷机开机、早间冷机刚启动时的热惯性滞后与 `5 分钟 / 1 分钟` 采样差触发瞬时温差倒挂和假高能效、冷冻水窄量程 `[3, 20] °C` 把 `2.8 °C` 真实过冷低温当成 `OUT_OF_RANGE` 拒收而不是发防冻告警，以及温控模块整机异常或冷量计派生节点把一条硬件/通信故障放大成几十条重复告警（#749）。

本文记录本次第一性原理重构时对三个固定版本参考项目的源码对照、采纳与舍弃决定。

## 参考版本与来源

- **ThingsBoard**：`v4.4`（commit `6d46786579c8b29caf5102f95ddb133674bed68b`，BUSL-1.1，仅作设计参照，不抄代码；见 `docs/architecture/references/thingsboard-v4.4.md`）。
- **OpenEMS**：`2026.9.0`（commit `14f0ded`，见 `docs/architecture/references/openems-2026.9.0.md`）。
- **MyEMS**：`v6.9.0`（commit `b360f5b`，见 `docs/architecture/references/myems-v6.9.0.md`）。

## 外部项目怎么做

### 1. 设备边界、点位语义与失真处理

- **OpenEMS 2026.9.0**：按组件能力接口（Nature）把电表（`ElectricityMeter`）与耗能设备分开建模（`io.openems.edge.common/…/OpenemsComponent.java`、`MeterJanitzaUmg96rmeImpl.java`）；Modbus 轮询失败时通过 `AbstractModbusElement.invalidate()`（`:208-213`）直接置空当前通道值，并由 `ModbusWorker` 汇总成单条组件级 `ModbusCommunicationFailed`（`OpenemsComponent.java:222-226`），防止多寄存器同时出错时刷出成片点位故障。但 OpenEMS 云端只有组件离线与整站最高等级汇总邮件（`SumStateHandler.java:121-122`），缺少冷机与辅助泵同馈线的负荷分离模型，也没有声明式的设备级告警收敛生命周期。
- **MyEMS v6.9.0**：通过实体表、虚拟表和离线表三分（`tbl_meters`、`tbl_virtual_meters`、`database/install/myems_system_db.sql:1082`、`:2738`），以及输入输出分离表（`tbl_equipments_meters.is_output`）避免汇总重复计入；清洗与小时化流水线用 `is_bad` 标记坏值，保留原始记录不删库（`clean_energy_value.py:4-6`）。但 MyEMS 把 `low_limit/high_limit` 和 FDD 门槛揉在同一张点表（`tbl_points:1763-1792`），缺失或超限时段直接往小时表写 `0`（`meter.py:398-405`、`:455-459`），社区版 `myems_fdd_db.tbl_rules` 也没有规则执行器和根因抑制实现。
- **ThingsBoard v4.4**：用 latest 表与历史表分存配合时间戳单调保护（`SqlLatestInsertTsRepository.java:25-61` 中的 `WHERE ts_kv_latest.ts <= ?`），并把告警定义成实体上的声明式 `ALARM` / `PROPAGATION` 计算字段（`CalculatedFieldType.java`、`AlarmCalculatedFieldConfiguration.java`），支持按关系传播和去重（`create_or_update_active_alarm`）。但它依赖无外键、无租户列的自由关系表 `relation`（`schema-entities.sql:521-531`），告警计算字段与规则链 JS/TBEL 两套机制并存，在多点模块整体掉线或跨设备启动稳定窗场景下需要靠自定义脚本兜底。

## 我们的方案：采纳、改造与拒绝

| 主题 | 采纳与改造 | 拒绝与原因 | 本次落地位置 |
| --- | --- | --- | --- |
| 电气馈线与冷机本体边界 | 借鉴 OpenEMS 把电表与工艺对象分离的思路，在资产铭牌新增 `feederAuxiliaryPowerKw`，把 C/D 电表（`127.5 kW` 冷机 + `30 kW` 定频水泵和冷却塔）与 A/B 压缩机专用表区分开；边缘包络脚本改为按表声明启停迟滞 `[57.75, 45] kW`（C/D）与 `[12.75, 5] kW`（A/B），网页排班偏差检测优先信 `run_state`。 | 拒绝只靠调高全局启停功率一个数字来掩盖 C/D 的辅助水泵负荷，因为这会把 A/B 低负荷运行状态（`15.8 kW` 起）一起吞掉。 | `libs/nameplate/nameplate.go`、`edge/node-red/songyaxuan-platform-envelope.js`、`services/fdd-service/internal/fdd/gates.go`、`services/optimization-service/internal/optimization/staging.go`、`apps/hvac-web/src/features/schedules/schedule-model.ts` |
| 物理真实量程 vs 过程运行告警 | 借鉴 MyEMS「保留原始值、用质量码区分可信度」原则，把 `qualityProfiles` 放宽为物理真实上下限（如冷冻水 `[-2, 40] °C`），让 `2.8 °C` 低温冷冻水按 `GOOD` 落库，再由独立过程规则 `chiller-leaving-low`（运行中出水 `< 4.0 °C` 持续 180 秒，严重度 `CRITICAL`）报警；同时修复冷启动纯拒收点（`ONLY_REJECTED_CANDIDATES`）把 `OUT_OF_RANGE` 透传给告警评估器。 | 拒绝 MyEMS 把工艺期望范围直接写成清洗拒收门槛的做法，也拒绝超限后写 `0` 或静默吞数。真实过冷风险必须进入过程防冻保护。 | `deploy/sites/songyaxuan.site.json`、`deploy/sites/songyaxuan.alarm-rules.json`、`modules/alarm/pkg/alarmservice/telemetry_evaluation.go`、`modules/alarm/pkg/alarmservice/evaluator.go` |
| 状态持续时间、同伴新鲜度与开机稳定窗 | 借鉴 ThingsBoard latest 单调更新思路，把 `unchanged_since` 从数值读数扩充到字符串/布尔状态（如 `run_state`），并在遥测质量评估 `loadQualityContext` / `loadPhysicsPeers` 关联 `freshness_policies.fresh_within_seconds`，拒绝把过期同伴值当现行状态；在 `PhysicsRule` 与平线检测新增 `stabilizeSeconds` / `flatlineStabilizeSeconds`（`600 秒`），越过开机热平衡期才查温差方向和卡住，同时保持 `PHYSICS_POWER`（`0 秒`）瞬时拦截不变。 | 拒绝在告警里用脚本硬算延时，也拒绝把所有物理规则一刀切延后：电气超铭牌不需要等水循环热平衡，只在温差与能效类检查加稳定窗。 | `modules/telemetry/pkg/telemetry/ingest.go`、`modules/telemetry/pkg/telemetry/ingest_store.go`、`modules/telemetry/pkg/telemetry/quality.go` |
| 整机根因收敛与派生去重（#749） | 借鉴 OpenEMS「总线从站故障汇总为一条组件故障」原则，当非派生设备上有 `>= 2` 个质量监测点位同时不可信（`OUT_OF_RANGE` / `SENTINEL` / `SOURCE_UNTRUSTED`）持续 `180 秒`，发 1 条 `device-untrusted/<dev>`（`MAJOR`）并在条件树（`joinConditions` 8 路折叠）直接抑制同设备所有逐点 `suspect`、`readings-not-updated` 及通知路由（`rootCauseRules` 按 `scopeSameDevice` 隔离设备作用域）；派生计算设备（`derive`：`btu-calc` / `meter-total`）保留遥测质量标记 `SUSPECT`，停止额外重复发 `suspect` 与 `btu-chw-direction` 告警。 | 拒绝 ThingsBoard 依赖无约束 `relation` 图做多跳传播树的做法（沿用 ADR 0031 决定），也不把不同设备的故障混在全站根因里互相静音（`device-untrusted/meter-uid5` 不会压掉 `temperature-uid20` 告警）。 | `modules/alarm/pkg/alarmrules/rules.go`、`modules/alarm/pkg/alarmservice/site_rules_http.go`、`modules/alarm/pkg/alarmservice/unevaluable_rules.go`、`services/notification-service/pkg/notificationservice/alarm_delivery_routing.go`、`services/notification-service/pkg/notificationservice/alarm_delivery.go` |

## 本次顺带消掉的两处契约缺口

1. `site_rules_http.go` 的 `RuleCondition.validate()` 遗漏了 `ConditionReasons` 分支，导致带有原因谓词的站级规则无法通过 HTTP 列表校验。现已统一复用 `validConditionReason` 校验函数，并在 OpenAPI 与前后端代码生成中补齐 `SOURCE_UNTRUSTED`。
2. 温度或通道触发 `SENTINEL`（`38.4`）或网关置非 0 质量位（`SOURCE_UNTRUSTED`）时，旧逻辑会在 30 分钟后叠加触发 `FLATLINE`，且停滞后又通过 `NO_DATA` 再触发一条 `readings-not-updated`。重构后这两类不可信状态不再叠记 `FLATLINE`，`readings-not-updated` 传感器叶子也显式排除已由 `suspect` 或 `device-untrusted` 覆盖的不可信通道。
