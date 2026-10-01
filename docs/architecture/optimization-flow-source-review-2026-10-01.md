# 节能机会 → 项目 → 验证主线源码审核

范围：用户授权使用现有模拟数据完成节能主线。复用原机会、项目、M&V fixtures；不新增虚构生产 endpoint、审批成功或设备执行接口。

## 参考与取舍

- ThingsBoard v4.3.1.1 / `c2a52e46c44e308ddee430e7266b8e10eddde9c4`：读取 `common/data/src/main/java/org/thingsboard/server/common/data/relation/EntityRelation.java`（25–95）及 `dao/src/test/java/org/thingsboard/server/dao/service/RelationServiceTest.java`（40–100）。关系有明确 from/to/type；测试区分方向及不存在的关系。**ADAPT**：只通过原 owner 的 `sourceOpportunityCode`、`sourceProjectCode` 对应既有业务对象，不按标题相似度匹配，不在缺少关联时选择第一项冒充目标。**REJECT**：为当前三段主线引入通用关系图服务、关系编辑器或泛型注册表。官方资料及既定范围见 `thingsboard-source-review.md`。
- MyEMS v6.7.0 / `be6e6ce8ddeac57afb04bddb9621501fb555cab0`：补读 `myems-api/reports/spacesaving.py`（65–125），明确空间、基准期、报告期和期间类型参数。**ADAPT**：在关联项目与核算页保持报告期间、测量边界一致，显示实际核算负责人；无测量记录只显示计划。**REJECT**：复制其数据库/认证实现或以年度估算代替报告期节省量。官方文档与其他 source review 见 `myems-source-review.md`、`energy-dashboard-source-review-2026-09-30.md`。此次未发现/采用该报告切片的专门上游自动化测试，不将源码参数检查冒充完整核算保证。
- OpenEMS 2026.7.0 / `2e2792d`：沿用既定能源通道、能量/功率分离与写通道的源码/测试/文档审核，本次不改变测量传输或控制执行。**REJECT**：将不存在的写回、控制执行或收益复核模拟为生产结果。本轮是前端 owner 关联与样本校正，没有采用新的 OpenEMS 写机制。
- TanStack Query/Router：沿用项目既定版本和架构，将组合读模型保存在 Query，详情对象、关联来源、结果对象与筛选保存在 Router search；不增加全局 store，也不保留选中项的本地镜像。

未复制上游代码，未运行上游完整测试。当前业务主线的领域名称、阶段和关联来自既有 HVAC owner；外部参考用于比较关系语义和期间责任，不作为节能项目/M&V 状态的权威。

## 实现

`features/optimization-flow/api/optimization-flow.ts` 为三个页面提供一次同 scope 的组合读取，数据仍来自已有 fixtures。仅 review/DEV 的上海站点提供该模拟组合；其他范围明确显示缺少数据，生产未接入时不回退到样本。

机会页 `inspect` 选择证据；项目页 `opportunity` 约束来源、`inspect` 选择项目；验证页 `project` 约束来源项目、`record` 选择报告、`inspect` 打开核算方法。旧 `project=mv-*` 的错误参数语义直接移除，不做兼容。刷新和浏览器历史保留 URL 状态；跨页按钮只链接准确对象，没有关系时明确“尚未立项”或“尚未开始测量”。无效/跨项目记录不静默回退到其他图表。

模拟数据修正：实施/调试在测量前完成；完成项目对应已复核记录；验证计划与核算报告使用同期间、边界和方法；复核日期不得早于报告期结束。预算、年度潜力和报告期差额仍为不同事实。验证阶段不计入已完成项目。

## 验证

既有能源测试增加一个当前合同测试，18/18 通过；覆盖来源记录存在、调试先于测量、期间/边界/方法一致、阶段与复核关系、报告期差额对账，以及已复核记录的日期约束。没有新永久 gate。

Linux Playwright 实际走通机会 → 关联项目 → 指定报告 → 实施项目 → 来源机会；验证详情刷新、历史返回恢复筛选、无测量项目、错配报告、缺失详情和跨站点隔离。1440×900 桌面审查项目详情和验证分析；截图等待 Sheet 入场结束。类型检查、窄范围检测器通过（0 blocking / 0 advisory）。不新增移动验收。
