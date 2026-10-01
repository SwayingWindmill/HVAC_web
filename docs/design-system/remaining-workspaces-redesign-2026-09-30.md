# 其他 Workspace 的产品重构方向

日期：2026-09-30。依据：用户本轮提供的《智慧能源管理系统前端改造方案》《前端信息架构设计》与后续明确授权。现有页面没有视觉权威，允许重新划分信息责任；首页已完成，不重新堆回重复指标。

## 全产品任务责任

| 页面 | 核心问题 | 主组件与图表 |
| --- | --- | --- |
| 节能机会 | 哪项改善值得先做，证据与风险是什么 | 按收益排序的 tablecn 账本、系统潜力横向条形图、成本/回收期分析、证据 Sheet |
| 节能项目 | 谁在推进，卡在哪里 | 项目账本与阶段分布、时间线、责任与里程碑 |
| 节能验证 | 实施后到底省了多少 | 同边界基线/实际、调整项、模型证据与报告入口 |
| 能耗与成本 | 用多少、花多少、何时变化 | 电量/成本切换的主趋势、分时成本对比、子系统排序、日明细 |
| 负荷与需量 | 何时达到峰值、是否越界 | 工程负荷曲线与需量阈值、峰值窗口、削峰机会 |
| 分项与能流 | 能量用在哪里、是否守恒 | 分项树/条形排序、Sankey、可追查明细 |
| 绩效与对标 | 同口径表现如何 | 效率趋势、标准比较、可解释 peer scatter 与排名 |
| 实时运行 | 哪个系统偏离、应先调查什么 | 系统示意、关键趋势、设备矩阵、上下文检查 |
| 系统与设备 | 定位对象与关联事实 | Context Explorer、运营账本、对象详情 |
| 异常与告警 | 哪件异常影响最大 | 等级/影响分布、带责任的告警账本、证据与处理 |
| 工作中心 | 我该完成什么、是否逾期 | List/Kanban、责任与截止时间、执行记录 |
| 策略与控制 | 策略是否有效、安全可执行 | 策略账本、执行记录、趋势；真实授权后执行 |
| 报告 | 哪份结果可交付、来源是否可信 | 报告账本、期间/范围/验证状态、真实导出 |
| 设置 | 怎样正确配置对象与规则 | 分组配置表单与可理解的错误，不做指标拼盘 |

## 当前用户审美与交互要求

桌面内容上限1600px，统一24px页面间距、20px模块间距。标题直接命名任务，避免重复面包屑、长副标题与灰色解释段落。每项事实有唯一主要位置。图表必须能回答业务问题；不为填空添加装饰进度、评分和预测。来源只标识一次，估算不冒充核证。详情与筛选具有清楚职责，同一结果不设多个同义入口。

筛选、分析期间、排序、业务视图属 TanStack Router；数据属 Query；表格使用现有 v9-native DataTable。生产不把模拟数据当事实，不能从本地 toast 宣称派工或下发控制。DEV/review 使用显式数据源；未接入能力明确不可执行。

## 源码取舍

沿用 `energy-dashboard-source-review-2026-09-30.md` 的官方固定版本。新增阅读 satnaing v2.2.1 / 0217f8cb73af66f3cbf4141d5e0d00e1c5c30434 的 `src/features/tasks/components/tasks-table.tsx`：ADAPT URL 筛选/分页与 UI 状态分离、工具条+表格的组合；REJECT v8 useReactTable、模板任务业务与模拟提交。
补充本地 e16c87f 的 tasks-mutate-drawer.test.tsx：阅读名称/输入/验证/关闭行为测试，不能据此称固定tag具备该测试。本轮不复制源代码。项目 `use-data-table.ts`、data-table-features.ts、data-table.tsx、pagination 与 Chart 为实现候选，先对照再用；表格无需重复原语。
Kiranism与arham使用既定源审查的图表/feature/error/指标组合；不迁入 Next.js/Base UI。ThingsBoard 固定 chart subscription、OpenEMS 固定 energy 与 null测试、MyEMS固定 spacesaving 参数/报告边界作为分析事实比较：ADAPT 同范围/期间及功率/电量分离，REJECT 自造已认证节能与通用widget引擎。上游均未拥有本项目机会派工合同，不从模板假成功推导控制行为。
无新增基础框架或依赖；现有 ECharts 保留工程图职责，普通图使用 shadcn Chart + Recharts。参考文件的通用 ECharts-only/Zustand realtime建议不覆盖项目现行明确边界。

工程图新增具体源审查：Apache ECharts 官方 6.1.0 / c5a48f5f97d23e5379720870b8444cd05b50ffb4（现有安装版本）。阅读官方 test/sankey.html 的 nodes/links/gradient 与 resize 用法、安装包 lib/chart/sankey/sankeyLayout.js 的 computeNodeValues/布局、lib/core/echarts.js 的 init/dispose、types 的 SankeySeriesOption，以及官方 handbook Chart Container and Size。ADOPT 容器尺寸初始化、ResizeObserver 与销毁、typed series/dataZoom；ADAPT 电量单边界 Sankey，不使用原示例的电量→制冷量→散热量链条或伪平衡达标。上游 Sankey 文件是视觉测试用例，本轮不宣称执行了上游套件；许可证 Apache-2.0，不复制其源代码。

## 实施验收

### 运行与配置源审查补充

- ThingsBoard v4.3.1.1 / c2a52e46c44e308ddee430e7266b8e10eddde9c4：本轮阅读 `common/data/.../alarm/AlarmStatus.java` 全文、`dao/src/test/.../AlarmServiceTest.java` 1–140 的创建/读取断言及官方 Alarms 文档。ADOPT 确认与清除独立，ADAPT 中文问题处理/诊断状态，REJECT “确认即解决”以及前端内存伪确认。告警样本的发生次数与问题条数分开；不从热力图或汇总样本捏造实时历史。
- OpenEMS 2026.7.0 / 2e2792d：本轮阅读 `io.openems.edge.common/src/.../channel/WriteChannel.java` 的 pending write / consume-and-reset / callback 契约以及 `.../test/.../component/AbstractOpenemsComponentTest.java`；结合既定 review 007、官方 Edge architecture 文档和已审查 Channel/number-utils 测试。该 component 测试只验证身份/属性，不证明写入成功。ADAPT 将请求、现场接收、读回一致、结果验证独立呈现；REJECT 在浏览器里更改设定值或将 ACK 标成验证完成。本批不改执行机制，也不宣称安全联锁经过新认证。
- MyEMS 固定 v6.7.0 的 `reports/spacesaving.py`、既定 source-review 的 report/tariff/calendar ownership：ADAPT 报告期间、边界与数据来源可回查；配置与分析分工。REJECT 没有正式 artifact 的“下载 PDF”、没有分发 owner 的“发送邮件”、各页面电价口径漂移，以及模板内存模拟保存。此轮只有前端评审读模型，不改变上述 backend ownership。
- 无新增依赖。多个已实施页面证明同样需要标题、整体指标带和加载/不可用状态，抽出三种展示组合 `workspace-parts.tsx`；不增加统一业务 API、store 或 CRUD 层。

### 当前页面合同

新全局导航页面的形态由本记录职责矩阵与本轮用户输入生成，原 Surface 规格的业务不变量仍有效，但不继续继承其历史版式。专业 `/sites/:siteId/...` 页面和平台设置子路由保持各自 owner，本批不以新评审数据替换其生产合同。

除首页现有 owner 外，本轮全局 Workspace 聚合接口未接入生产。DEV / frontend review 明确标记示例；正式运行返回可见的未接入状态，不把模拟数据作为请求失败回退。运行类示例只属于上海站点，切换其他范围不能沿用上海的设备、告警、指令与工单。配置中的计量、天气、日历和审批未绑定时保留未知，不能通过一个假保存制造生效记录。

每批完成真实桌面渲染、焦点/详情/筛选/返回/下载检查与窄范围 detector。只保留保护真实合同的检查，构建不代表真实后端聚合或业务写入已经接通。

实施结果：侧栏其余14个入口的前端组合已替换，账本统一经过现有 DataTableBlock；工程图完成 ECharts 生命周期与可见契约阈值，比较图具备单位/图例，禁用普通图不必要的入场动画。旧组合与查询86个文件经 TypeScript parser/module resolver逐项核对无外部引用后移出活动源树，原内容在 out/ 下归档以备恢复；内存模拟设备设定值修改接口已删除。

验证：能源合同17项、设计合同2项通过；最终 Web typecheck 与生产构建通过；窄范围 detector 为0 blocking / 0 advisory。1440×900与1920×1080的14个入口完成桌面检查，覆盖 URL 期间/筛选、各业务标签、非模态详情、范围隔离、看板及真实CSV下载，无浏览器异常或页面横向溢出。渲染中发现的对标浮点坐标、契约线不可见、费用汇总舍入差异和缺少比较图图例已修正。证据位于 out/*-redesign-1440.png、out/*-redesign-1920.png 与 out/workspaces-final-build.log；不增加永久 CI 门禁。
