# 节能运营首页 Source Review — 2026-09-30

范围：当前 /overview 首页的构图、图表、数据投影与动作真实性。用户明确指定三个本地参考仓库。

## 固定版本与实际阅读

| 上游 | 固定版本 | 阅读文件 |
| --- | --- | --- |
| satnaing/shadcn-admin | 官方 v2.2.1 / 0217f8cb73af66f3cbf4141d5e0d00e1c5c30434；本地补充 e16c87f213a5ba5e45964e9b67c792105ec74d26 | tagged src/features/dashboard/index.tsx、src/components/layout/main.tsx；本地 dashboard/components/analytics-chart.tsx、README.md、LICENSE、src/components/config-drawer.test.tsx |
| Kiranism/next-shadcn-dashboard-starter | 官方 v2.0.0 / 06e83c0b0e937f98184b343b92756ae0cadd7c92；本地补充 7705dfc0d13889e45c26a55ad5908da6a7a9a605 | tagged src/features/overview/components/area-graph.tsx；本地 overview/components/stats-error.tsx、README.md、package.json |
| arhamkhnz/next-shadcn-admin-dashboard | 官方无 release/tag（GitHub releases 和 git ls-remote --tags 均为空）；固定本地官方源码快照 7eedd776b7c089d01ba765b722c00b4efdbb3f5a | src/app/(main)/dashboard/analytics/page.tsx、analytics/_components/analytics-kpi-strip.tsx、finance/_components/overview-kpis.tsx、README.md、LICENSE、package.json |
| ThingsBoard | 既定 v4.3.1.1 / c2a52e46c44e308ddee430e7266b8e10eddde9c4 | ui-ngx/src/app/modules/home/components/widget/lib/chart/time-series-chart-widget.component.ts；官方 chart-widget 文档；既定 thingsboard-source-review.md |
| OpenEMS | 既定 2026.7.0 / 2e2792d | ui/src/app/edge/history/common/energy/energy.ts、energy/chart/chart.ts、ui/src/app/shared/utils/number/number-utils.spec.ts；官方 Edge architecture 文档；既定 openems-source-review.md |
| MyEMS | 既定 v6.7.0 / be6e6ce8ddeac57afb04bddb9621501fb555cab0 | myems-api/reports/spacesaving.py 的 reporting 参数/基准期与报告期处理（1–140 行）；reports 目录清单；既定 myems-source-review.md |

官方 tag 由只读 git ls-remote 校验；tag 源码通过 GitHub connector 读取，不更改三个本地参考仓库。arham 无可用 tag 属于上游事实，固定不可变提交作为例外，不能虚构 release。
未复制上游源代码。三个 UI 仓库均为 MIT；实现只吸收组合语法和职责，使用项目现有 primitives，没有引入 Next.js、Clerk、Base UI、SSR 或新依赖。

## 测试证据边界

三个模板相关首页没有找到专门的节能/收益或 Dashboard 行为测试，不能把模板静态示例视为生产事实保证。
satnaing 当前 config-drawer.test.tsx 说明浏览器角色定位、打开 Dialog 和状态恢复的集成测试方式，作为补充交互测试参考，不冒称 v2.2.1 的首页测试。
OpenEMS number-utils.spec.ts 明确 null 与数值（含零）的区别；只借鉴缺测语义，不移植其数值 helper 或防御分支。
ThingsBoard 图表组件的订阅/legend 更新、MyEMS 报告参数与计量范围是本轮参考切片；没有运行这些上游完整测试套件，相关后端实现不在本轮修改范围。

## 比较与决定

| 关注点 | 实际上游行为与本地差异 | 决定 |
| --- | --- | --- |
| 应用布局 | satnaing Header/Main 分工、紧凑标题、受限内容宽度；本地首页过长，所有模块等权 | ADAPT：保留现有 shadcn shell，首页 max-width、清楚层级、按内容高度组织；不复制 SaaS Revenue/Recent Sales |
| 成果指标 | arham analytics/finance 用一个整体 surface + 内部分隔呈现指标；本地六个独立卡片和重复火花线 | ADAPT：单层成果带，重点为节省量/费用，基线和舒适度常驻；没有照搬其多层 Card 结构 |
| 图表 | Kiranism ChartContainer/config/Tooltip/accessibilityLayer；本地直接 Recharts、四线和伪水气切换 | ADOPT：项目 shadcn Chart；ADAPT：同一计量关系的基线/实际双线，移除装饰性目标/预测及伪能源切换 |
| 数据组织 | Kiranism feature 组织和明确 error action；本地 period 被忽略、字段由 UI 写死 | ADAPT：feature 查询/投影、URL period、真实 loading/error/unsupported 状态；REJECT：Next SSR、Base UI render、nuqs 和额外服务抽象 |
| 时间与单位 | OpenEMS 明确 powerChannel/energyChannel，并从独立 energy response 获得累计能量；本地将功率/电量混在同一看板 | ADOPT：功率与期间电量分离；不从曲线差值或瞬时值伪造已验证节能量 |
| 图表数据 ownership | ThingsBoard chart 由 defaultSubscription 提供数据/legend，并更新图表，不自造资源事实 | ADAPT：真实数据来自已有平台 overview read model；示例仅限既有 review/DEV 环境，生产禁止模拟补值；REJECT：通用 widget 编辑器/Angular runtime |
| 节能报告 | MyEMS spacesaving 分开 base/reporting period、空间范围和能源类别；本地 period 无效 | ADAPT：示例按所选期间有独立数据，真实接口不支持的期间明确提示；基线方法/验证信息缺失则明确未知；不复制其 Python/API-key/数据库访问机制 |
| 控制与责任 | 模板按钮仅呈现外观，本地 alert 导出与本地状态派工会冒充业务结果 | REJECT：伪成功；首页只提供证据查看和专业工作区导航；摘要导出真实文件并标记示例/待验证 |

## 产品与工程边界

本轮实现用户指定的节能经营首页，不重写全部导航和业务模块。
真实平台现有 dashboard-overview 契约不接收 period、没有基线方法/认证/舒适度覆盖字段，不能假装支持。生产展示现有当前概况与未知项；示例模式用于完整 UI 评审且始终明确标记。
未来真实聚合期间/认证证据需扩展权威契约后接入，本轮不建立临时新 endpoint、兼容层或本地模拟 fallback。
安全风险优先于收益排序；舒适度合规与缺测覆盖分开，策略贡献估算不直接相加宣称全站验证收益。

## 本地实施验证

采用已安装 shadcn/Chart/Recharts 与 feature 查询边界，无新增依赖或框架。首页 URL、示例投影、CSV 的最小行为保护合并到现有能源测试（16/16）。Web 类型检查和生产构建通过，窄范围 Impeccable detector 0 blocking/0 advisory。Linux Playwright 验证期间、站点、返回、Sheet、CSV 与跨页 scope，桌面 1440×900/1920×1080 截图评审完成。上游测试仅用于行为源阅读，本轮未宣称执行上游完整测试集。
