# 03 站点与所选范围节能运营总览 Surface Specification

状态：SELECTED / BROWSER REVIEWED。日期：2026-09-30。
Route intent：`/overview`，使用应用全局所选范围；既有 `/sites/:siteId/overview` 为独立站点专业入口，本轮未重写该路由。
权威：PRODUCT.md → 当前页面架构 v3 → 全局导航上下文合同 v2 → 本规范 → DESIGN.md。
本次用户明确将首页定位为美观、高级、实用的节能运营 Dashboard；本规范替换本 Surface 历史 attention-router-only 及 wireframe 要求，不扩展到其他 Surface。

## Primary Job

能源经理在 30 秒内判断节能成果、运行策略、舒适度约束和下一步机会。首页连接专业工作区，业务执行与核证由相应 owner 完成。

## Primary Questions

1. 所选期间实际用了多少电？
2. 相对同口径基线省多少电、多少钱？
3. 策略贡献是否有证据，是否仅为估算？
4. 舒适度是否达标，监测覆盖如何？
5. 当前高后果异常是什么？
6. 哪些机会值得进一步分析？

## Entry Points

全局节能总览导航、专业页面返回、带 scope/period 的可分享 URL。

## Exit Paths

能源分析、优化验证、节能机会、策略、告警、系统运行等当前专业路由。保留全局所选范围，不在首页重复专业操作流。

## Route / URL State Ownership

`scope` 与 `period` 属于 TanStack Router；period 为 today/week/month/year，返回恢复选择。服务查询属 TanStack Query。详情开关为局部 UI 状态，范围/期间变化关闭旧详情。生产默认今日，示例默认本月。

## Screen Hierarchy

Header → 上下文与风险 → 四项成果带（实际、节省、节能率、费用）→ 全宽基线趋势 → 策略/舒适度 → 机会/关注 → 系统指标。成果只汇总一次；舒适合规与覆盖集中在约束卡。详细几何、去重审核与组件责任见 `docs/design-system/dashboard-layout-specification.md`。

## Priority Attention

高后果告警先于收益排名得到明确提示。关注项显示业务原因、位置、影响及可执行入口；不公开内部 UUID、原始枚举或追踪字段。

## Data Authority Contract

真实事实来自已有 dashboard-overview read model。当前生产 owner 没有历史聚合参数、基线方法、核证状态和舒适度覆盖率：页面明确展示缺失与不支持，不能模拟补值。
DEV/review 为显式示例，期间桶汇总与实际/基线/节省值一致，范围改变数据；不得作为生产接口失败 fallback。
趋势明确 kW/kWh；缺测不变零、不跨缺口连线。舒适度达标率与覆盖率分开。策略贡献为估算，不相加冒充全站认证收益。基线、实际、节省的计量边界与期间必须一致。
U.S. DOE FEMP 的测量与验证语义用于区分估算与核证；ISA-101 的运行任务原则用于异常优先与有效动作；ASHRAE Guideline 36 的工程语义用于策略与舒适度约束。这些依据不构成页面认证或具体控制算法实施声明。实际源比较见本轮 energy-dashboard-source-review。

## No Defensive Programming / No Compatibility Design

不添加模拟 fallback、重复状态、伪成功或旧视觉兼容层。不添加新 API/基础框架。外部 I/O 失败明确报错；未接入能力明确说明并提供可用下一步。

## Component Mapping

现有 AppShell；Header + shadcn Button/Badge；整体指标 strip；shadcn Chart/Recharts 双线；Card；精简只读机会 Table；非模态 Sheet；Alert/Skeleton。业务查询、投影与 UI 在 features/overview，路由只拥有 search 解析及组件接入。
tablecn 为完整运营账本标准，本页摘要表不重复筛选/分页系统。源 pin、许可证和 ADOPT/ADAPT/REJECT 见 `docs/architecture/energy-dashboard-source-review-2026-09-30.md`。

## Accessibility

期间按钮具 aria-pressed，刷新具明确名称；图表启用 accessibilityLayer，关键结果有文本对应。Sheet 可 Escape 关闭并恢复焦点。错误、未知及验证状态以文字表达，颜色不作为唯一载体。桌面验收范围不包含移动端。

## Browser Acceptance Criteria

在 1440×900 与 1920×1080 检查实际渲染层级、间距、密度与状态。
期间与站点切换更新数字和 URL；返回恢复期间；快速详情无背景遮罩或主页面挤压；CSV 含范围/来源/待验证；专业分析入口保留 scope。生产未支持期间及接口失败不可出现示例补值。
2026-09-30 本轮桌面自动交互与视觉检查已通过。截图在 out/energy-dashboard-1440.png、out/energy-dashboard-1920.png；真实历史聚合和核证证据接入尚未完成，不能称生产业务已全部验收。
