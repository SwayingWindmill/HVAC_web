# 运维模拟闭环源码审核

用户授权沿用现有模拟数据，补齐设备异常、告警调查、关联工单、检查项处理及恢复验证。当前实现只写入 DEV/review 上海站点的浏览器会话模拟记录，不调用生产写接口或物理控制。

## 参考与取舍

- ThingsBoard v4.3.1.1，提交 `c2a52e46c44e308ddee430e7266b8e10eddde9c4`：阅读 `common/data/src/main/java/org/thingsboard/server/common/data/alarm/AlarmStatus.java` 及 `dao/src/test/java/org/thingsboard/server/dao/service/AlarmServiceTest.java` 100–330。`testFindAlarm` 明确从 ACTIVE_UNACK 经 acknowledge 到 ACTIVE_ACK，再经 clear 到 CLEARED_ACK；官方 [Alarms 文档](https://thingsboard.io/docs/user-guide/alarms/) 区分活跃、确认和清除。**ADAPT** 独立的确认与恢复状态、设备精确来源关系；**REJECT** 确认即恢复、提交工单即清除。未复制源码，也未运行上游完整测试。
- OpenEMS 2026.7.0，`2e2792d`：沿用 `openems-source-review.md` 及 `remaining-workspaces-redesign-2026-09-30.md` 中 WriteChannel pending/consume/reset、AbstractOpenemsComponentTest 和官方 Edge architecture 的审查。**ADAPT** 作业、观测与结果责任分离；**REJECT** 将前端模拟回放作为现场控制或安全联锁证明。本轮不采用新的写通道机制。
- MyEMS v6.7.0，`be6e6ce8ddeac57afb04bddb9621501fb555cab0`：沿用 `myems-source-review.md` 的 FDD、资产、报告 owner 审查及已审查 `myems-api/reports/spacesaving.py`。**ADAPT** 对象、期间、证据归属可追查；**REJECT** 从能源报告推导本项目派工/恢复合同。该候选不提供本轮需要采纳的新工单状态机制，不宣称上游能源报告测试证明恢复可靠性。
- 现有 TanStack Query/Router、Zod、shadcn、Lucide、tablecn/v9 足够；不引入依赖或第二套 UI。详情、关联和筛选在 URL；组合模拟 owner 的结果在 Query；单次工况选择使用局部 state。沿用当前已批准工作区布局。

## 当前合同

三个页面统一设备身份；补齐告警/工单已经引用但资产样本缺少的设备，关系通过 deviceId 和 sourceAlarmCode，界面不显示内部标识。`inspect` 精确选对象，告警页 `device`、工单页 `alarm` 限定来源；无效详情明确提示，不偷选其他对象。

确认告警只记录接收责任。领取工单后才可记录检查项；全部检查项完成才可提交作业完成，受阻工单不能提交。工单完成使来源告警进入待验证，不改变设备恢复状态。独立的异常持续/维修后稳定样本观测按判据验证；失败保持异常，通过才解除来源告警。未配置判据的工单明确要求工程师补充测量方案。诊断测点保留原快照，恢复观测单独展示，不覆盖历史证据。

模拟恢复判据：冷机压力、过热度、供水温度依据既有诊断参考；蒸发温度补充 4–5℃评审区间；新风滤网压差依据原 180Pa 告警线。它们是明确的模拟方案，不构成生产验收标准。操作日志在 sessionStorage，刷新重放，同会话三个页面共享；重置删除该模拟日志，其他站点不复用上海数据。损坏的记录明确报错，禁止静默回退。

## 验证

既有 `scripts/test-operations-workspace.mjs` 增加一项行为合同：关联完整、确认独立、受阻/未完成检查项阻止提交、完成不恢复设备、失败/通过观测、领取未分派工单。无新增永久 gate。

Linux Playwright 实际走通设备 → 告警 → 工单、确认、检查项、完成、验证失败再通过；验证刷新保留、未分派、受阻、无效详情及跨站点隔离。桌面 1440×900 审查工作中心账本与恢复面板；原始操作时间改为本地时间，诊断历史快照与新观测分开展示。最终类型检查、生产构建及 13 项运维测试通过；窄范围检测 0 blocking / 0 advisory。
