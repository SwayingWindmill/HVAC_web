# 全局能源 Dashboard 工作区合同 · 2026-09-30

状态：IMPLEMENTED / DESKTOP REVIEW。适用本轮全局导航入口，不替换专业站点子路由的业务 owner。

依据：当前用户提供的两份前端改造与信息架构文档及“现有页面非权威，可最大限度修改”授权；PRODUCT.md 的收益/舒适/安全事实；v3 页面职责与 global navigation context 契约；Shadcn Application System。原页面实现只提供数据字段候选，不提供版式权威。本合同从这些上游职责重新生成。

| 入口 | 唯一主要任务 | 当前结构 | 业务状态边界 |
| --- | --- | --- | --- |
| /optimization/opportunities | 比较改善优先级并查看证据 | 系统潜力/推进分布、收益排序账本、证据与经济性检查器 | 潜力不是核证收益 |
| /optimization/projects | 查看改善推进与责任 | 账本/阶段看板、里程碑、工程与核算交接 | 不模拟新建/派工/阶段提交 |
| /optimization/verification | 解释项目报告期的节省量 | 基线/实际、月度差额、调整与模型质量、核算 CSV | 不伪造签发与认证 |
| /energy-analysis/consumption | 解释期间电量与成本 | 电量/费用/逐日账本，分时费用对比、系统费用、日详情 | 同期间日、分时、系统汇总必须对账 |
| /energy-analysis/load-demand | 比较需量与契约及削峰资源 | 15分钟需量/瞬时负荷、契约线、dataZoom、持续曲线、约束检查器 | 功率不是电量；可评估不等于执行授权 |
| /energy-analysis/breakdown | 定位分项电量的归属 | 同边界 Sankey、分项占比与电量、计量账本 | 不把制冷量/散热量混作电量平衡 |
| /energy-analysis/benchmarking | 比较同口径效率与气象影响 | EUI/COP 散点与同类对象账本、工程参考、气象调整曲线 | 工程参考不是合规认证，气象调整不是实施节能 |
| /operations/realtime | 查看当前系统与设备工况 | 冷机负荷、水侧工况、运行快照、控制回路对照 | 不从单个快照制造历史或实时在线状态 |
| /operations/systems-devices | 找到设备和所属上下文 | 系统/空间/计量视角、筛选账本、资产/工况检查器 | 设备页不直接改设定值 |
| /operations/alarms | 调查异常与诊断证据 | 紧急优先账本、级别/复发分布、证据/假设/记录 | 确认独立于解决；候选独立于已确认根因 |
| /operations/work-center | 查看处理进度与责任 | 工单账本/进度看板、检查项、备件、执行记录 | 完成检查项不等于功能/节能验证 |
| /operations/control | 解释策略、指令与保护边界 | 执行账本、策略参数、安全条件、请求/读回检查器 | 接收、读回一致、结果验证分别呈现 |
| /reports | 查找可追查的报告记录 | 报告资料卡、源分析入口、内容/期间/责任与数据文件 | 数据导出不冒充正式 PDF/分发 |
| /settings/ | 理解站点运行与分析口径 | 站点上下文、电价表/图 | 未绑定的计量、天气、日历和审批保持未知 |

全页采用紧凑任务标题、整体指标带、24px 页面/20px 模块间距、最大1600px内容宽度；只使用承担任务的图表。默认账本 tablecn + 当前 TanStack Table v9；普通图 shadcn Chart/Recharts；需量和能流 ECharts。详情使用非模态 Sheet，保留主结果上下文；输入/状态/视图/期间位于 Router search；查询按 scope/period 隔离。

本批原有实现是前端评审工作区，不能作为真实链路验证证据。正式聚合 owner 未接入时显示未接入状态，不使用示例回退。上海运行样本不能用于其他站点。正式配置写入、控制下发、审批、报告签发/分发仍由相应 owner 提供权威能力后实现。DEV 模式本身不代表数据是示例；真实后端页面不得因开发环境展示示例标识。

2026-10-01 节能主线：三个页面共用既有模拟 owner 组合；机会 `inspect` → 项目 `opportunity` / `inspect` → 验证 `project` / `record` / `inspect` 保持准确关联。详情刷新和浏览器历史恢复筛选与对象；不存在或错配记录明确提示，未测量项目只展示计划。模拟实施时间、报告期间、测量边界与复核日期已核对。来源与验证见 `docs/architecture/optimization-flow-source-review-2026-10-01.md`。

源码比较与明确 ADOPT/ADAPT/REJECT 见 `docs/design-system/remaining-workspaces-redesign-2026-09-30.md`，版本与许可证见 `docs/architecture/energy-dashboard-source-review-2026-09-30.md`。86 个被替换的旧组合与查询文件经 TypeScript module resolver 确认无外部引用后移出活动源树；不保留视觉兼容层。
# 2026-10-01 用户补充要求

用户明确要求采用 #346 所描述的真实前后端链路；该问题中的过时参数和页面方案不约束当前实现。模拟源是 EG8200 / Virtual Plant 的物理设备输入，经 Edge/MQTT、Telemetry、Alarm、FDD 和 Work Order 的实际 owner 形成业务事实。浏览器静态 fixture、sessionStorage 操作重放和前端自行恢复告警不能作为该链路的实现或证据，现有运维评审模拟需要移除。

站点范围来自当前用户授权的 Registry 站点，不能将未知范围默认为上海样本。设备在线状态、工况值、时间、质量和新鲜度来自 Snapshot + Stream。确认告警独立于告警恢复；工单完成独立于物理故障解除；解除物理扰动后必须通过新观测、Alarm owner 的恢复事实及保留的 FDD / 工单历史验证闭环。检查项仅展示 owner 实际提供的事实，不创建仅在浏览器中生效的任务。详情/来源保存在 Router search，查询和提交结果归 TanStack Query。当前接入进度和已核实的合并取舍见 `docs/architecture/live-chain-merge-review-2026-10-01.md`。

适量使用 Lucide，按任务借鉴 arham 各页组合而非所有页统一 KPI + 卡片 + 表格。报表中心采用报告资料库：每份报告唯一主卡展示期间、负责人、生成时间、内容目录和查看入口，详情继续负责报告内容与真实可用的数据下载，不再同时重述为功能介绍卡和对象账本。实时运行按设备类别分组；工作中心完成进度只使用已有检查项事实。来源取舍见 `docs/design-system/workspace-layout-refinement-2026-10-01.md`。

