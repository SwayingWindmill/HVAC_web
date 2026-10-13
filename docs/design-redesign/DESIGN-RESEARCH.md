# 产品设计研究

日期：2026-10-13 · 阶段：设计研究（第二阶段）

前置文档：[CURRENT-AUDIT.md](CURRENT-AUDIT.md)、[COMPONENT-INVENTORY.md](COMPONENT-INVENTORY.md)。产出：本文、[DESIGN-DIRECTIONS.md](DESIGN-DIRECTIONS.md)、[DESIGN-CONSTRAINTS.md](DESIGN-CONSTRAINTS.md)。

本阶段只做研究，没有修改 UI 代码或业务逻辑。

---

## 0. 方法与证据说明

### 0.1 来源

- **Mobbin MCP 不可用**：账号未开通付费方案，`search_screens`、`search_sections`、`search_flows` 都返回同一个错误，见上一轮记录。本研究**没有使用 Mobbin**。
- **改用各产品的官方文档和设计规范页**，因为这些页面嵌有官方发布的产品截图。抓取方式：
  - 无头 Chromium 打开页面，读出 `<img>` 的原始地址和 `alt`；
  - 下载原图逐张目视检查；
  - 文字结论来自同页正文，通过浏览器或 curl 读取。
- 下文每条证据都给出**页面 URL** 和**原图 URL**。截图没有入库（仓库不提交二进制），需要时可以按 URL 重新下载。

### 0.2 证据等级

| 等级 | 含义 | 本文涉及 |
| --- | --- | --- |
| A：真实产品截图 | 官方文档里的产品界面截图，可能带示例数据 | Datadog、Grafana、Vercel、Linear |
| B：官方示意 / 样例应用 | 官方设计规范里的界面示意或示例应用截图，代表平台推荐的模式，但不是某个真实账户的页面 | Stripe Apps 的设计规范页 |
| C：只有文字 | 官方文档正文对交互的描述，没有对应截图 | Linear Peek、Linear Triage 快捷键、Grafana 空值处理 |

### 0.3 尝试过、但没有拿到产品界面的来源

不作为证据使用，如实记录：

| 来源 | 结果 |
| --- | --- |
| Samsara 产品页（设备监控） | 只有营销照片和合成图（例如人物照片上叠一个指标框），不是产品界面；平台页加载超时 |
| Willow 官网（楼宇运营 AI 平台） | 只有建筑渲染图、客户 logo 和白皮书封面 |
| KODE Labs（KODE OS） | 页面被 Cloudflare 人机验证拦截（“Just a moment...”）。按安全规则没有尝试绕过 |
| Attio 官网 | 首页只有客户案例封面图 |
| incident.io 文档首页 | 只有营销插图（Slack 输入框），不是应用界面 |
| `.tmp_refs/` 里的 4 张图 | 是 Willow 和 KODE Labs 的视频缩略图，不是产品界面。它们说明你关注的是**智慧楼宇运营平台**这一赛道，但不能当作设计证据 |

**结论**：同赛道的楼宇 / 能源运营平台（Willow、KODE Labs 等）在公开渠道拿不到可核实的产品截图。如果你有它们的产品截图或账号，可以补充进来，用来校准“行业相关性”这一维。

---

## 1. 研究对象与选择理由

我们的产品是**冷站运行监视 + 能耗与电费分析 + 异常处置（告警 / 诊断 / 工单）+ 控制审批 + 节能核算**的 B 端操作平台，用户包括值班人员、暖通工程师、能源管理人员和审批人员。选择对象时看的是“哪个产品解决过我们同类的界面问题”，而不是知名度。

| 产品 | 对应我们的哪部分 | 借鉴重点 | 证据 |
| --- | --- | --- | --- |
| **Datadog**（监控告警） | 告警、诊断、实时运行 | 监控列表的分面筛选、“无数据”独立状态、状态页头、事件叠加在时序图上、状态时间条 | A |
| **Grafana**（时序可视化） | 能耗、需量、负荷、设备趋势 | 线条插值方式、空值 / 缺口处理、告警规则列表 | A + C |
| **Vercel**（可观测性控制台） | 总览、能耗分析、排行 | 阶梯图、指标页签切换主图、未完成区段用虚线、行内比例条、Geist 色彩体系、有影响说明的确认弹窗 | A |
| **Stripe**（财务控制台） | 电费估算、节能核算、页面骨架 | “按问题选图”、固定图表高度、主数值在图上方、不加卡片外框的指标行、Chip 筛选、上下文抽屉与聚焦抽屉 | B |
| **Linear**（任务与分诊） | 工单、告警处置、通知 | 分组列表 + 组计数、筛选条、分诊收件箱与快捷键、Peek 预览、列表加右侧上下文栏、图表 + 透视表 | A + C |

**没有选 Notion 和 Airtable**：它们的核心是自由文档和通用表格建模，和“实时运行 + 证据 + 审批”的操作型工作流关系不大。表格密度和视图方面，Linear 与 Datadog 的参考价值更直接。**Attio** 的记录详情页模式可以参考设备详情，但这次没拿到可核实的截图，所以不纳入。

---

## 2. 证据清单（24 条）

| ID | 产品 | 内容 | 页面 | 原图 |
| --- | --- | --- | --- | --- |
| R1 | Linear | 活动 issue 列表：顶部筛选 chip（“Assignee is any of 3 assignees”“Labels include Feature”），按状态分组，组头带计数（2 / 11），行内是编号、状态图标、标题和右侧标签 | https://linear.app/docs/filters | https://webassets.linear.app/images/ornj730p/production/26e1e266d76606fad8972baf4b57ed364aa2e4c1-2438x1126.png |
| R2 | Linear | Inbox：Priority 4 / Other 12 两个分段，每行是头像、对象、事件描述、状态图标和相对时间 | https://linear.app/docs/inbox | https://webassets.linear.app/images/ornj730p/production/b442b340278740de70919c868dce1afca4335fe7-3600x2080.png |
| R3 | Linear | Insights：堆叠柱图在上，同口径透视表在下，右侧是 Measure / Dimension / Segment 选择 | https://linear.app/docs/insights | https://webassets.linear.app/images/ornj730p/production/c845c78338ca581e6794e8aa2731d02bdd5b058f-2160x1327.png |
| R4 | Linear | Triage 收件箱：未规划的工作集中在一个队列。文档正文：接受按 1、拒绝按 2、标记重复按 3、暂缓按 H；暂缓到指定时间或有新动态时再出现 | https://linear.app/docs/triage | https://webassets.linear.app/images/ornj730p/production/30183e73c9ef009bb8cb14adbe97d9f34a5b9a69-2352x1632.png |
| R5 | Linear | 视图右侧上下文栏：Overview / Insights 页签、视图属性、按负责人 / 标签 / 项目的计数分布 | https://linear.app/docs/custom-views | https://webassets.linear.app/images/ornj730p/production/128bab75f8039c255acfb3ca833c057153ed2e1e-2160x1326.png |
| R6 | Linear | Peek（只有文字）：列表中按空格键预览选中对象，↑ / ↓ 切换相邻对象并同步更新预览，Esc 关闭；命令菜单中移动时也会自动预览 | https://linear.app/docs/peek | — |
| R7 | Stripe | 样例整页应用：页头是应用名、主操作和设置；下划线页签；“Date range”chip；四个指标是一行纯文字，没有卡片；主图下方是第二张图；右栏是“Open rentals”队列，带 Auto approved / Needs review / Rejected 状态 | https://docs.stripe.com/stripe-apps/patterns/full-page-apps | https://docs.stripecdn.com/8a9e806f8630691f96a7aca4e200b859c012b6148abdc70cfea91217f3603f6e.png |
| R8 | Stripe | 图表布局规范：截图里摘要指标带涨跌百分比，右栏是最近活动和快捷操作。正文规则见下方注 ① | https://docs.stripe.com/stripe-apps/patterns/chart-layout | https://docs.stripecdn.com/43922c31d00c9afc0fee2020607a97be43b903bff48f8fc86fa937e929a0f695.png |
| R9 | Stripe | 筛选 chip：未选时是虚线“+ Category”，选中后是“Status \| Available”加清除按钮，末尾“Clear filters”；表格没有外框 | https://docs.stripe.com/stripe-apps/patterns/filter-controls | https://docs.stripecdn.com/6aa914bcbe2132609871479e5e67b886c63fafcb258d861739a2fb9ee223a338.png |
| R10 | Stripe | ContextView 和 FocusView 两种抽屉，区别见下方注 ② | https://docs.stripe.com/stripe-apps/design | https://b.stripecdn.com/docs-statics-srv/assets/views-contextview.be48c893f02a6e6be989ec659ddd29a7.png ；https://b.stripecdn.com/docs-statics-srv/assets/views-focusview.3e3f40d737518fc2f2e083cc6395710c.png |
| R11 | Stripe | 详情页示意：面包屑 → 大标题 → 主操作 / 次操作 / 更多；正文是“Section”标题加内容块，没有卡片标题栏 | https://docs.stripe.com/stripe-apps/patterns/full-page-apps | https://docs.stripecdn.com/38feeb88bebd95b7cd3ec2b1b91f56b719777301b141171507898c250b67ffe1.png |
| R12 | Vercel | Observability：调用量用**阶梯面积图**，按 2XX / 4XX / 5XX 分色；旁边是错误率小图；下方路由表的数值单元格带比例底条 | https://vercel.com/docs/observability | https://7nyt0uhk7sse4zvn.public.blob.vercel-storage.com/docs-assets/static/docs/concepts/observability/O11y-Tab-Light.png |
| R13 | Vercel | Web Analytics：标题旁是“● 457 online”实时人数；右上是环境和时间范围选择；指标页签（Visitors / Page Views / Bounce Rate，带涨跌徽标）切换下方主图；**最后一段未完成数据画成虚线** | https://vercel.com/docs/analytics | https://7nyt0uhk7sse4zvn.public.blob.vercel-storage.com/front/docs/analytics/visitor-chart-light.png |
| R14 | Vercel | 排行面板：每行底部是浅灰比例条，数值右对齐，面板内有页签切换维度，底部有“View All” | https://vercel.com/docs/analytics | https://7nyt0uhk7sse4zvn.public.blob.vercel-storage.com/front/docs/observability/panels-light-mode.png |
| R15 | Vercel | 部署列表的状态多选筛选：每项带色点（Ready / Error / Building / Queued / Canceled），触发器显示已选数“4/5” | https://vercel.com/docs/deployments/managing-deployments | https://7nyt0uhk7sse4zvn.public.blob.vercel-storage.com/docs-assets/static/docs/concepts/deployments/filtering-deployments/filter-status-light.png |
| R16 | Vercel | “Redeploy to Production”确认框：说明会发生什么，并**列出受影响的域名**，然后才是确认按钮 | https://vercel.com/docs/deployments/managing-deployments | https://7nyt0uhk7sse4zvn.public.blob.vercel-storage.com/docs-assets/static/docs/concepts/deployments/redeploy-model-light.png |
| R17 | Vercel | Geist 色彩：10 个色阶 × 9 个色系，按序号定义职责，见下方注 ③ | https://vercel.com/geist/colors | 整页截图（无单图） |
| R18 | Datadog | Monitor 列表：左侧分面筛选，每个值带计数（Alert 285 / Warn 34 / **No Data 23** / OK 352）；顶部搜索语法；批量操作（Mute / Resolve / Edit Tags）；列设置开关；状态列是实心色块标签 | https://docs.datadoghq.com/monitors/manage/ | https://docs.dd-static.net/images/monitors/manage_monitor/monitor_page-2.93689f1aa9becb4587e011b9b43cd171.png |
| R19 | Datadog | Monitor 状态页头：顶部一条红色状态细线；ALERT 标签、静音、Resolve、创建时间、负责人、优先级 P1、团队、标签排成一行 | https://docs.datadoghq.com/monitors/status/status_page/ | https://docs.dd-static.net/images/monitors/status/status_page_header_banner.5692afc3e370b497777922bdf2c68617.png |
| R20 | Datadog | 评估图：顶部是全局状态时间条（红 / 绿），下面按分组排小倍数折线（越阈值的部分填红），右栏是分组数和查询口径 | https://docs.datadoghq.com/monitors/status/status_page/ | https://docs.dd-static.net/images/monitors/status/evaluated_data_graph_1.a4f38aabe4bea66c54c93640c35720aa.png |
| R21 | Datadog | 时序图事件叠加：部署事件画成竖线标记；另一张图顶部是事件密度条 | https://docs.datadoghq.com/dashboards/widgets/timeseries/ | https://docs.dd-static.net/images/dashboards/querying/event_overlay_example.7b8bdf995454a3db2da9bf1d3b2d0e9e.png |
| R22 | Grafana | 同一组数据的四种插值：Linear / Smooth / Step before / Step after。正文对空值的说明见下方注 ④ | https://grafana.com/docs/grafana/latest/panels-visualizations/visualizations/time-series/ | https://grafana.com/media/docs/grafana/panels-visualizations/screenshot-time-interpolation-v12.0.png |
| R23 | Grafana | Stat 面板：整块高饱和底色（橙、红、紫）加大数字和底部迷你曲线。**反例**，见 §4.6 | https://grafana.com/docs/grafana/latest/panels-visualizations/visualizations/stat/ | https://grafana.com/media/docs/grafana/panels-visualizations/screenshot-stat-visualization-v11.3.png |
| R24 | Grafana | 告警规则列表：按文件夹 / 分组折叠，每行是状态图标（正常 / 暂停 / 触发）、名称、摘要、标签数；行尾“More”菜单里有暂停评估、静默通知、复制链接、删除（红色） | https://grafana.com/docs/grafana/latest/alerting/monitor-status/view-alert-rules/ | https://grafana.com/media/docs/alerting/view-alert-rule-list-with-actions-2.png |

**注**

① R8 正文规则：

- 按用户的问题选图：“在涨还是跌”用折线，“每类有多少”用柱，“总量怎么构成”用单条比例条，“快速看趋势”用迷你线；
- 图表高度由容器固定：模块内 180px，详情或全宽 320px，同一行的图保持同高；
- 先给当前数值，再给趋势；
- 一行放 2 张图各占 1/2，3 张各占 1/3，或者主图 2/3 配辅图 1/3。

② R10 正文：ContextView 是默认的上下文抽屉，和页面内容并排显示；FocusView 用于需要从头做完的任务，会加阻断式遮罩。拿不准时默认用 ContextView。

③ R17 正文：

- 1–3 号是组件背景（默认、悬停、激活）；
- 4–6 号是边框（默认、悬停、激活），默认用实色灰；
- 7–8 号是高对比背景；
- 9–10 号是文字和图标；
- 另有规则：“每条边框只画一次”。

④ R22 正文：“Connect null values” 控制缺口如何显示，可以设为 Never（不连接），也可以设一个阈值，超过阈值的缺口就不再连接。

---

## 3. 七个设计维度的研究

每个维度按三部分写：参考产品怎么做 → 我们现在的问题 → 可以借鉴的做法。

### 3.1 整体布局与导航架构

**参考**

- Stripe（R7、R11）：侧栏分三层：常用对象（Home / Balances / Transactions / Customers）、个人快捷方式（Shortcuts）、可折叠的产品分组（Payments / Billing / Reporting…）。顶部是全局搜索和通知。页面内用**下划线页签**切换同一业务对象的几个视图（Overview / Members / Rewards / Activity）。
- Linear（R1、R5）：页头只有视图名和收藏；筛选、显示方式、通知设置在同一条工具栏；右侧上下文栏可以开关。
- Datadog（R18）：模块内用顶部页签（List / Triggered / Downtimes / Quality / Analytics），左侧分面面板可以隐藏。

**我们**：侧栏 15 个平铺入口，“能源”组 8 项；能耗 / 分项 / 需量 / 预测是 4 个独立页面，各自有期间控件；平台功能没有稳定入口（审计 §2.2）。

**借鉴**

- 同一业务对象的平级视图收进**页内页签**，侧栏只保留一级对象。例如“能源”可以收成一个模块，内部用页签切换用电 / 分项 / 需量 / 预测，这样侧栏能从 15 项降到 8 项左右。
- 侧栏加“快捷方式”层（个人常用、收藏的视图）。
- 平台功能（通知、集成、系统管理）放进侧栏底部固定区，不再藏在用户菜单里。

### 3.2 Dashboard 信息层级与数据密度

**参考**

- Stripe（R7、R8）：指标是**一行纯文字**（标签 + 数值 + 涨跌），没有卡片边框，紧挨着“Date range”控件；主图在下；右侧 1/3 是“需要处理的事”（Open rentals、Needs review）和快捷操作。规范明确要求先给数值再给趋势。
- Vercel（R13）：3 个指标做成页签，点哪个，下面的大图就换成哪个；标题旁是实时人数。指标、图、时间范围合成一个整体。
- Datadog（R20）：先给全局状态时间条，再给按分组的小倍数图，口径在右栏说明。

**我们**：13 页都是“4~5 格指标卡条 → 2/3 + 1/3 卡片 → 表”；指标卡底部两行灰字；同一个数在一页里出现 3~4 次（审计 §5.1）。

**借鉴**

- 首页做成**“左主图 + 右待处理栏”**：左 2/3 是今日用能（指标页签 + 主图，我们的 `MetricTabsChartCard` 方向本来就对），右 1/3 是“需要处理”的合并队列（告警、诊断、工单、待审批指令），每条带状态和时间。
- 去掉指标卡外框，改成和时间范围同一行的纯文字指标；说明文字只在异常时出现。
- 每个页面只放一个主角：实时运行是系统图，能耗是主图，告警是队列。

### 3.3 字体排版、留白与间距

**参考**

- Stripe（R7、R8）：页面标题很大很粗（约是正文的 2 倍）；区块标题直接写在内容上方（“Program summary”“Recent activity”），不放在卡片标题栏里；指标数值比标签大一档，涨跌用小号彩色字紧跟数值。
- Linear（R1、R2）：只靠字重和灰度两三个层级区分，不用额外的字号；组头和行之间用浅背景区分。
- Vercel（R13、R14）：大数字和徽标同一基线；列表行高约 32px，数值右对齐。

**我们**：大数字有 28 / 30 / 18 / 14px 四种；中文落到系统回退字体，粗细区分弱；所有块等距 24px，没有分组感（审计 §5.3、§5.4）。

**借鉴**

- 收敛成**少数几个字号档**：页面标题、区块标题、正文、辅助、主数值、次数值。每档的用途固定。
- 区块标题去掉卡片外壳，直接放在内容上方。
- 间距分两档：组内 12–16px，组间 32–40px，用间距表达分组，而不是每块都加边框。
- 中文要定一个能正确表现字重的字体，否则字重层级在 Windows 上会失效（这需要你决定，见 DESIGN-DIRECTIONS §6）。

### 3.4 表格、图表、筛选与操作工具栏

**表格**

- Datadog（R18）：左侧分面带计数，顶部搜索，表头上方是批量操作，列设置是开关列表；“No Data”是独立状态。
- Linear（R1）：按状态分组的列表，组头有计数，行高紧凑，次要属性靠右以标签显示。
- Stripe（R9）：表格没有外框，只有行分隔线；筛选是 chip。
- Vercel（R12、R14）：数值单元格内有比例底条，排名和数值同时可读。

**图表**

- Grafana（R22）：插值方式是显式选项，阶梯插值适合“区间值”；缺口默认可以选择不连接。
- Vercel（R12、R13）：区间累计量用阶梯图；未完成的最后一段用虚线。
- Datadog（R20、R21）：状态时间条、事件竖线叠加、越阈值部分填色。
- Stripe（R8）：按问题选图，固定高度，同一行同高。

**筛选与工具栏**

- 未选筛选是虚线“+ 属性”，选中后是“属性 | 值 ×”（R9）；多选带色点和已选计数（R15）。
- 视图 / 显示设置和筛选分开（R5 的 Display 按钮）。

**我们**：表格三种写法；视图切换四种；图表是分类时间轴并且平滑插值（P0）；“最大需量”用红色（审计 §5.6、§5.7）。

**借鉴**

- 台账统一成一种：**分组列表 + 组计数 + chip 筛选 + 列设置**，不加外框。
- 告警页加 Datadog 式**左侧分面**（严重度、状态、设备类别带计数）。
- 能耗类累计量统一用**阶梯图或柱图 + 连续时间轴 + 缺口断开 + 未完成段虚线**。
- 在能耗、设备趋势图上**叠加事件**（控制指令下发、设定值变更、告警起止）。这对节能核算特别有价值：“8 °C 设定值是什么时候改的”能直接在曲线上看到。
- 排行用“行内比例条”，取代现在三四种不同的排行写法。

### 3.5 色彩、边框、阴影和视觉语言

**参考**

- Vercel Geist（R17）：每个色系 10 阶，序号对应职责（背景 / 边框 / 文字）；边框只画一次；阴影要半透明。界面整体是灰阶，蓝色用在数据和链接，红 / 琥珀 / 绿只表示状态（R12 的 2XX / 4XX / 5XX）。
- Stripe（R7、R9）：白底、细分隔线，几乎没有卡片阴影；品牌紫只用于选中页签、链接和主按钮；状态用浅底彩色字标签。
- Linear（R1）：深色界面，状态完全靠图标形状 + 颜色（空心圆、半圆、对勾），不依赖文字底色。
- Datadog（R18、R19）：状态用实心色块标签，信息很密，视觉也最“重”。
- 反例 Grafana Stat（R23）：整块高饱和底色，适合大屏，不适合办公桌面上的长时间工作。

**我们**：青绿既是品牌色又是信息状态和“推荐值”；两套状态色；语义色被挪用（审计 §5.5）。

**借鉴**

- 采用 **Geist 式的序号化色阶**来组织 token：1–3 背景，4–6 边框，9–10 文字。品牌色、数据色、状态色各自独立。
- 状态采用 **“形状 + 颜色”** 双编码（Linear 式状态图标），满足“不只靠颜色”的可达性要求，也能区分“离线 / 故障 / 未知”。
- 边框和阴影减量：区块之间靠留白和一条分隔线，只有浮层（Sheet、弹窗、下拉）有阴影。

### 3.6 用户操作流程与交互反馈

**参考**

- Linear Triage（R4）：队列 + 每条四个动作（接受 / 拒绝 / 重复 / 暂缓），都有单键快捷键；暂缓到时间或有新动态时再出现。
- Linear Peek（R6）：列表中按空格预览，↑ / ↓ 连续浏览，不离开列表。
- Stripe 抽屉（R10）：查看上下文用并排的非阻断抽屉；需要完整做完的任务用阻断式聚焦抽屉。
- Vercel 确认（R16）：确认框写明会发生什么、影响哪些对象，再给确认按钮。
- Grafana / Datadog（R24、R19）：对象的危险操作收在“More”里，用红色区分；状态页头把当前状态和可用动作放在一行。

**我们**：

- 告警的确认 / 认领 / 搁置、诊断发现、工单、待审批指令分散在 4 页；
- 详情侧栏盖住了表格右侧的列；
- 停机确认没有影响说明，也没有危险样式（审计 §5.8）。

**借鉴**

- 把“需要人处理的东西”（未确认告警、新诊断发现、待审批指令、待处理工单、结果不明的指令）合成一个**分诊队列**。每条给出明确动作（确认、认领、转工单、搁置到…），并支持键盘。
- 详情改成 **ContextView 式并排侧栏**：打开时表格让出宽度，或侧栏只覆盖不重要的列。用 ↑ / ↓ 在相邻对象间切换。
- **控制确认改成 FocusView 式**：阻断遮罩，列出对象、当前值 → 目标值、受影响设备和预计冷量变化、风险等级、是否需要审批；主按钮按风险着色。
- 每个对象详情顶部都用 **Datadog 式状态页头**：一行写清当前状态、负责人、时间和可用动作。

### 3.7 多页面之间的设计系统一致性

**参考**

- Stripe Apps 明确限制自定义样式，让所有应用遵守同一套组件和模式（R10 正文：“Custom styling of UI elements is intentionally limited”）。图表规范给出具体高度和栅格分数（R8）。
- Vercel Geist 把颜色职责写成规则，而不是一张色卡（R17）。
- Linear 的列表、分组、筛选、显示设置在所有视图中完全一致（R1、R5）。

**我们**：同一种“主数值”有四种字号，表格有三种，视图切换有四种（审计 §5.7）。

**借鉴**

- 设计系统按**页面原型**定义，不只是组件库。原型包括：总览、监视、分析、队列、台账、详情、操作、文档。每种原型有固定骨架、允许的组件和图表高度。
- 组件只保留一种做法，用 lint 或 `design:check` 防止回退（现在 `scripts/test-real-design-tokens.mjs` 已经在检查 token 对比度，可以沿用这个思路）。

---

## 4. 结合项目审计的诊断

### 4.1 当前设计的明显不足（按影响排序）

1. 每页同一副骨架：指标卡条、卡片栅格、表，导致没有页面主角（审计 §6.1）。
2. 图表不真实：分类时间轴、平滑插值、缺口连线、不完整时段（P0）。
3. 需要处理的事分散在 4 页，总览只是各取前 5 条。
4. 视觉语言是 shadcn 默认值换了主色：没有自己的色阶、状态语言、数值排版。
5. 组件不一致：表格、切换、状态徽标、指标各有多种写法。
6. 高后果操作（控制）和普通操作的视觉、交互没有区别。

### 4.2 布局与信息架构问题

| 问题 | 参考解法 |
| --- | --- |
| 侧栏 15 项，“能源”8 项平铺 | 一级对象 + 页内页签（R7、R11） |
| 平台功能无入口，对标页丢失站点上下文 | 侧栏分区：站点工作区 / 平台；平台页显示“全部站点”范围 |
| 待处理事项分散 | 统一分诊队列（R4、R2） |
| 同一个数重复出现 3~4 次 | 每页一个主角，指标只出现一次（R7、R13） |
| 详情侧栏遮挡台账 | 并排上下文栏（R5、R10） |
| 1280 退成单列 | 固定的 2/3 + 1/3 栅格，在 1280 下仍保持两栏（R8 的分数栅格） |
| 能耗 4 页期间控件各不相同 | 模块级共享的时间范围控件（R13） |

### 4.3 视觉设计问题

| 问题 | 参考解法 |
| --- | --- |
| 品牌色和状态色混用 | 职责分离的色阶（R17） |
| 两套状态色，状态只靠颜色 | 形状 + 颜色的状态图标（R1、R24） |
| 卡片过多，卡套卡 | 区块标题 + 留白 + 分隔线（R7、R9、R11） |
| 字号不收敛，中文字重弱 | 少量字号档 + 选定中文字体（R7、R13） |
| 图表是 Recharts 默认皮肤 | 阶梯 / 柱、事件叠加、未完成段虚线（R12、R13、R21、R22） |
| 空状态千篇一律 | 区分“无事项 / 不可用 / 未接入”，并给正向信息 |

### 4.4 可以保留的组件

COMPONENT-INVENTORY 里的组件，按研究结论分三类：

- **保留，只换皮**：`DataTable` 及工具栏、`FactList` / `FactRow`、`Sheet`、`Dialog`、`Command`、`Timeline`、`Item`、`DeltaBadge`、`Skeleton`，以及 `ScopeSwitcher`、`CommandSearch`、`NavigationProgress`、`ThemeGate` 的行为。
- **保留职责，重做结构**：
  - `MetricTabsChartCard`：方向和 Vercel R13 一致，但要换成连续时间轴和阶梯 / 柱图；
  - `PlantDiagram`：保留工程规则；
  - `PageHeader`：改成“标题 + 实时状态 + 时间范围 + 主操作”一行；
  - `Status`：改成形状 + 颜色。
- **淘汰或合并**：
  - `MetricGrid` / `MetricCard` 作为每页标配的用法取消；
  - `StatusBadge` 和 5 个领域徽标合并进 `Status`；
  - `BarList`、分项排行、对标排名合并为一个“行内比例条排行”；
  - 优化约束滑轨和诊断阈值轨合并为一个范围标尺；
  - 零引用组件删除。

### 4.5 需要结构性重设计的页面

| 页面 | 为什么 | 方向 |
| --- | --- | --- |
| 总览 | 现在是各模块前 5 条的拼贴 | 左主图 + 右分诊队列；指标一行文字（R7、R13） |
| 告警 + 故障诊断 + 工单 + 通知 | 待处理事项分散，空数据时满屏空白 | 合并为“待处理”分诊工作区，带分面和键盘（R4、R18、R2） |
| 能耗 / 分项 / 需量 / 预测 | 4 页同构，期间控件不统一，图表不真实 | 合并为“能源”一个模块，用页签切换视图；共享时间范围；图表按 §1.3 约束重做（R12、R13、R22） |
| 控制指令 | 高后果操作没有区分；卡套卡 | 设备列表 + 聚焦式确认（R16、R10） |
| 实时运行 | 同一个数出现 4 次 | 系统图做主角，快照表做证据，指标条删掉 |
| 设备 | 没用 DataTable，详情遮挡 | 分组台账 + 并排详情（R1、R5） |

**只需换皮的页面**：优化建议（结构合理）、节能项目、月度报告（文档型）、站点对标、集成管理、系统管理。

### 4.6 不应该照搬的地方

- **Linear 的深色界面和紫色品牌**：我们的默认场景是白天办公室，并且状态色要承载告警语义。Linear 的低对比深灰会削弱告警的可见性。
- **Grafana Stat 的整块高饱和底色（R23）**：适合大屏，在桌面工作台上会造成色彩疲劳，也违反“状态色只表达状态”。
- **Datadog 的信息密度上限**：Datadog 的用户是 SRE，我们的值班人员需要更强的中文可读性；只取它的分面、事件叠加和状态时间条，不照搬它的视觉重量和缩写化文案。
- **Stripe 的涨跌一律绿色（R7、R8）**：能耗“上升”通常是坏事。涨跌方向的好坏必须按指标定义（`DeltaBadge` 已经支持 `invertDelta`，要保留）。
- **Vercel 的英文全大写列标签（R14 的“VISITORS”）**：中文界面不适用。
- **Stripe 的品牌色抽屉页头（R10）**：那是第三方应用的识别色。我们只有一个产品，不需要。
- **任何产品的示例数据和英文术语**：只借模式，不借内容。

---

## 5. 模式目录（供方向选择和后续设计引用）

| # | 模式 | 来源 | 用在我们哪里 |
| --- | --- | --- | --- |
| P1 | 指标页签切换主图 | R13 | 总览、能耗 |
| P2 | 不完整区段用虚线 | R13 | 所有含“今天 / 本月”的时间图 |
| P3 | 阶梯图表示区间累计量 | R12、R22 | 逐时 / 逐 15 分钟用电、需量 |
| P4 | 缺口不连接 | R22 | 所有时序图 |
| P5 | 事件叠加在时序图上 | R21 | 能耗、设备趋势、节能核算（控制指令、设定变更、告警） |
| P6 | 状态时间条 | R20 | 设备运行状态、站点在线状态 |
| P7 | 分面筛选 + 计数 | R18 | 告警、设备、诊断 |
| P8 | Chip 筛选（建议态 / 已选态） | R9、R1 | 所有台账 |
| P9 | 分组列表 + 组计数 | R1 | 设备（按系统）、工单（按状态）、告警（按严重度） |
| P10 | 分诊队列 + 单键动作 + 暂缓 | R4 | 待处理工作区 |
| P11 | 收件箱分段（重要 / 其他） | R2 | 通知 |
| P12 | 列表中预览 + ↑↓ 连续浏览 | R6 | 告警、工单、设备 |
| P13 | 并排上下文栏 | R5、R10 | 对象详情 |
| P14 | 聚焦式确认（阻断 + 影响清单） | R10、R16 | 控制下发、凭据吊销 |
| P15 | 状态页头（状态 + 负责人 + 动作一行） | R19 | 告警、工单、设备详情 |
| P16 | 指标一行纯文字 + 时间范围 chip | R7、R8 | 总览、各分析页 |
| P17 | 右栏“需要处理” | R7 | 总览 |
| P18 | 行内比例条排行 | R12、R14 | 分项排行、对标、告警设备排行 |
| P19 | 图表 + 同口径透视表 | R3 | 能耗明细、月度报告、对标 |
| P20 | 序号化色阶 token | R17 | 设计系统 |
| P21 | 形状 + 颜色的状态图标 | R1、R24 | 所有状态 |
| P22 | 固定图表高度档 + 分数栅格 | R8 | 所有分析页 |
