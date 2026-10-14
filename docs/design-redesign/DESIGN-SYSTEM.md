# 泉来禾 HVAC 设计系统 1.0

来源：画布「定稿」页的总览（B × C 视觉语言），是唯一的视觉基准。可视版本在同一画布的「设计系统」页，共 8 块画板，每块都有浅色和深色两版。可以直接替换进代码的令牌在 [`design-tokens.css`](design-tokens.css)。

本文档是设计阶段的产出，还没有接入代码。实施时把令牌替换进 `apps/hvac-web/src/global.css`，再按第 4 节逐个组件调整；`DESIGN.md` 的重写与第一个实施 PR 一起提交。

## 1. 原则

- **令牌名沿用 shadcn/ui 变量**。`--background`、`--primary`、`--muted-foreground` 等名字不变，已安装的组件不用改代码。新增变量都标为“扩展”，在 `@theme inline` 里登记后，就能使用 Tailwind 类名（例如 `bg-primary-soft`）。
- **组件只做组合和定制，不重写**。所有组件都来自项目已安装的 shadcn/ui（radix-nova 风格）和现有封装；外部注册表只新增 3 个组件（见 4.3）。
- **只有一个强调色**：靛蓝。它只用于四类地方：可点击元素、选中与焦点状态、主数据序列、导航当前项。
- **语义色只表达状态**，并且同时用形状编码：◆ 严重、▲ 主要、● 次要、■ 提示、○ 信息。
- **真实性优先**：
  - 缺数据显示“—”并写明原因，不显示 0；
  - 时间轴连续；
  - 未完成的时段用虚线；
  - 示例数据要标“示意”；
  - 不在界面上显示 UUID、trace ID 或内部枚举。
- **每个事实只有一个归属页**：
  - 其他页面只放摘要，并链接到归属页；
  - 同一页内，一个数字只出现一次；
  - 两种呈现冲突时，保留更漂亮、更直观的图（例如分项能耗保留桑基图，去掉构成条）；
  - 图表与表格：分析页按成熟产品（GA4、Amplitude、Search Console、AWS Cost Explorer）的做法“图在上、表在下”同时展示，但表格只放图上没有的维度和指标，可排序、可导出；只是把图换成数字时，用图表自带的“表格视图”（Grafana、PostHog 的做法）；
  - 说明性文字收进 ⓘ 悬停提示。
  - 各类事实的归属见下表。

  | 事实 | 归属页 | 其他页面的用法 |
  |---|---|---|
  | 冷站实时功率、制冷量、COP、环路工况 | 实时运行 | 总览只放摘要 |
  | 单台设备的读数、点位、工单 | 设备 | 实时运行只放“主要设备 n / n 运行”链接 |
  | 期间用电、供冷、能效、电费 | 能耗分析 | 总览只放今日摘要 |
  | 用电的分项构成 | 分项能耗 | 不再重复总用电的变化 |
  | 需量 | 需量分析 | 负荷预测只对照本月最大需量 |
  | 告警、工单、指令的数量 | 各自列表的筛选页签 | 侧栏角标与之一致 |
- **文案用日常说法**：界面上用值班人员平时说的词，专业名词（如 IPMVP、R²）只放在 ⓘ 提示里。常用替换如下：

  | 不用 | 改用 |
  |---|---|
  | 台账 | 列表、节能措施 |
  | 核算 | 统计 |
  | 测量边界 | 计量电表 |
  | 分表 / 总表 | 设备电表 / 总电表 |
  | 未分项 | 未单独计量 |
  | 基线检验 | 对比基线是否可靠 |
  | 回执 | 返回结果 |
  | 待核对 | 待现场确认 |
  | 工况 | 环境、读数 |
  | 计收 | 计费 |
  | 认领 | 分配 |
  | 区间未计入 | 缺 N 段数据 |
- **只面向桌面**：基准宽度 1440，最小支持 1280。

## 2. 设计令牌

### 2.1 颜色

完整的值见 `design-tokens.css`。下表列出各组的用途。

| 组 | 变量 | 用途 |
|---|---|---|
| 表面（三层） | `background` → `card` → `popover`；`muted`、`secondary`、`accent` | 三层依次是画布、面板、浮层；表头与分段控件的底色；中性徽章；悬停行 |
| 文字（四级） | `foreground`、`secondary-foreground`、`muted-foreground`、`subtle-foreground`（扩展） | 依次为正文与数值、次级正文、标签与说明、单位/坐标轴/占位符 |
| 线与焦点 | `border`、`divider`（扩展）、`input`、`input-hover`（扩展）、`ring`、`ring-soft`（扩展） | 外框用 `border`，面板内分隔用 `divider`；焦点 = `ring` 边框 + 3px `ring-soft` 外圈 |
| 强调 | `primary`、`primary-hover`、`primary-soft`、`primary-line`（后三个为扩展） | 浅色值 `#3f5bd9`，深色值 `#7b93ff`；深色下主按钮文字改用深色 `#0b1030` |
| 语义 | `success`、`warning`、`destructive`，各自带 `-soft` 与 `-text` 变体 | 徽章用 soft 底色配深一级的文字；不使用实心彩色徽章 |
| 严重度 | `severity-critical / major / minor / warning / info` | 一定和形状一起使用 |
| 图表 | `chart-1..4`（分类）、`chart-5`（对照灰）、`chart-grid`、`chart-axis`、`chart-fill`、`chart-now`、`chart-inactive` | 详见 2.1.1 |
| 热力 | `heat-0..3` | 单一靛蓝色相，由浅到深；没有数据的格子留空 |
| 侧栏 | `sidebar*` | 当前项的底色是 `primary-soft`，文字是靛蓝 |

#### 2.1.1 图表分类色（已校验）

| | chart-1 | chart-2 | chart-3 | chart-4 | chart-5（对照） |
|---|---|---|---|---|---|
| 浅色 | `#3f5bd9` 靛蓝 | `#0e9384` 青 | `#8b5cf6` 紫 | `#d6409f` 粉 | `#c4c4c8` |
| 深色 | `#6f87f7` | `#16a394` | `#9a7cf2` | `#e05a9f` | `#52525b` |

- 两套颜色都用 dataviz 的 `validate_palette.js` 检查过，全部通过：
  - 浅色在白底上，深色在 `#141417` 上；
  - 检查项包括亮度带、彩度下限、相邻色的色盲 ΔE（最小 13–14）、正常视觉 ΔE、对比度。
- 原来的 `--chart-3` 是琥珀色，和“注意”状态色撞色，所以改成紫色，第 4 色用粉色补上。
- 分类色按固定顺序取用，不循环。第 5 类起并入“其他”。

### 2.2 字体

| 令牌 | 字号 / 行高 · 字重 · 字距 | 用途 |
|---|---|---|
| display | 26 / 32 · 700 · −3% | 页面标题，每页一个 |
| metric-lg | 24 / 29 · 600 · −3% | 主数值，例如指标页签、面板中的主数 |
| metric-md | 16 / 22 · 600 · −2% | 次数值，例如卡内小指标、详情里的事实 |
| title | 14 / 20 · 600 | 面板标题、小节标题 |
| body-strong | 13 / 20 · 600 | 列表项标题、表格主列 |
| body | 13 / 20 · 400 | 正文、表格、菜单；按钮用同字号，字重 500 |
| caption | 12 / 18 · 400 | 说明、元信息；字段标签用同字号，字重 500 |
| micro | 11 / 16 · 500 | 单位、坐标轴、徽章、键位 |

- 字体栈：`"Geist", "PingFang SC", "Microsoft YaHei", "Hiragino Sans GB", "Noto Sans CJK SC", sans-serif`。
  - 中文使用系统字体；拉丁字母和数字使用 Geist。
  - 时间、编号、读数对比、坐标轴使用 Geist Mono。
- 所有数字开启 `tabular-nums`。
- 数值后的单位降为 12 / 500 / `subtle-foreground`，与数值间隔 4px。

### 2.3 间距

以 4px 为基数，对应 Tailwind 刻度。

| 值 | 用途 |
|---|---|
| 2 | 分段控件内边距 |
| 4 | 数值与单位之间 |
| 6 | 图标与文字之间、字段标签与控件之间 |
| 8 | 按钮组、徽章组 |
| 10 | 输入框水平内边距 |
| 12 | 按钮水平内边距 |
| 16 | 面板内边距、面板之间、表单字段之间 |
| 20 | 对话框内边距 |
| 24 | 区块之间 |
| 28 | 主内容区左右边距 |

定稿左栏面板内边距画的是 18px，实施时统一为 16px。

### 2.4 圆角

| 档位 | 值 | 用途 |
|---|---|---|
| sm | 4 | 热力格、kbd |
| md | 6 | 徽章、菜单项、分段项、Tooltip |
| lg | 8 | 按钮、输入框、卡内块、数据提示 |
| xl | 10 | 面板、卡片、菜单、Popover |
| 2xl | 12 | 对话框、命令面板、Sheet |
| full | — | 计数、头像、状态胶囊、开关 |

嵌套时内层比外层小一档。

### 2.5 边框与阴影

- **第 1 层**：面板、描边按钮、输入框，用 `border` + `shadow-xs`。
- **第 2 层**：菜单、Popover、图表提示，用 `shadow-md`。
- **第 3 层**：对话框、Sheet、命令面板，用 `shadow-lg` + 遮罩 `overlay`。
- **深色模式**：`shadow-xs` 为空，层次靠边框和三层底色区分。
- **实施时改名**：`design-tokens.css` 里运行时变量改名为 `--elevation-*`，由 `@theme inline` 映射成 `shadow-*`，避免自引用。

### 2.6 布局网格

- **应用框架**：
  - 顶栏 52px；
  - 侧栏 216px，收起后 56px；
  - 主区内边距：上 22、左右 28、下 24。
- **内容区**：12 列，列间距 16px；右栏固定 352px；面板之间横竖都间隔 16px。
- **浮层宽度**：
  - 详情 Sheet 480px，宽版 640px；
  - 对话框 440px（确认类）或 560px（表单类）；
  - 命令面板 560px。

### 2.7 组件尺寸

| 部件 | 尺寸 | 说明 |
|---|---|---|
| 控件 sm / md / lg | 28 / 32 / 36 | 默认 32 |
| 图标 | 16 / 15 / 14 / 13 | 依次用于导航、按钮、行内、链接箭头 |
| 顶栏 / 面板标题栏 | 52 / 48 | |
| 侧栏项 | 32 | 圆角 7 |
| 线型页签 / 分段控件 | 36 / 30 | |
| 指标页签 | ≈72 | |
| 表头 / 行 / 密集行 | 36 / 44 / 36 | |
| 双行列表项 | ≈56 | |
| 徽章 / 计数 / 胶囊 | 20 / 18 / 22 | |
| 复选框 / 单选 / 开关 | 16 / 16 / 32×18 | 可点区域至少 24×24 |
| 头像 | 28 | |
| 菜单项 / 命令项 | 32 / 36 | |

### 2.8 动效

| 令牌 | 值 | 用途 |
|---|---|---|
| instant | 100ms | 悬停、按下时的颜色变化 |
| fast | 150ms | Tooltip、Popover、菜单：淡入，并从 0.96 缩放到 1 |
| base | 200ms | 对话框：淡入并上移 4px；页签指示条 |
| slow | 250ms | Sheet 侧滑、侧栏收起与展开 |
| ease-out / ease-in | `(0.2,0.8,0.2,1)` / `(0.4,0,1,1)` | 进入 / 退出；退出时长为进入的 0.75 倍 |

- 读数和金额直接替换，不做数字滚动。
- 图表不做入场生长动画；切换指标时整图淡入 150ms。
- 开启 `prefers-reduced-motion` 时去掉位移和缩放，只保留透明度变化，脉冲点停止。
- 实现方式：shadcn 已经带有 tw-animate-css 的 `data-[state]` 进出场类，只需要改时长和曲线。

## 3. 状态约定

所有交互组件都按同一组状态设计，画板上的状态列也按这个顺序排列：

| 状态 | 表现 |
|---|---|
| 默认 | — |
| 悬停 | 背景变为 `accent`，或边框变为 `input-hover` |
| 按下 | 背景加深一级，或出现内阴影 |
| 焦点 | 只在键盘焦点时出现：`ring` 边框 + 3px `ring-soft` |
| 禁用 | 透明度 50%，并写明不可用的原因 |
| 加载 | 宽度不变，文字改为进行时，配旋转图标 |
| 错误 | `destructive` 边框 + 浅红外圈，下方一句“怎么改” |

## 4. 组件目录

### 4.1 已安装组件（只改令牌与变体）

| 组件 | 来源（项目内） | 定制要点 |
|---|---|---|
| Button | `ui/button` | `default` 改为靛蓝；`outline` 加 shadow-xs；不使用 `secondary`；圆角 8。面板内操作用“文字 ›”链接，不用按钮 |
| Input / Textarea / InputGroup | `ui/input`、`ui/textarea`、`ui/input-group` | 数值输入带单位后缀并用等宽数字；只读状态用 muted 底，不借用禁用样式 |
| Select | `ui/select` | 用于少量固定选项；超过 8 项改用 Combobox |
| Checkbox / RadioGroup / Switch | `ui/checkbox`、`ui/radio-group`、`ui/switch` | 开关即时生效；需要“保存”才生效的设置用复选框；半选只出现在表格的全选框 |
| Tabs | `ui/tabs`（line 变体） | 页面级视图切换，状态写进 URL |
| ToggleGroup | `ui/toggle-group` | 分段控件，用于面板内筛选同一份数据 |
| 指标页签 | `components/metric-tabs-chart-card` | 标签 12 + 数值 24，选中项底边 2px 靛蓝 |
| Badge / Status / DeltaBadge | `ui/badge`、`ui/status`、`ui/delta-badge` | soft 变体；严重度带形状；变化徽章按“好坏”着色，不按“升降”着色 |
| Tooltip / HoverCard | `ui/tooltip`、`ui/hover-card` | 简单提示用反色样式；口径和公式说明放在 ⓘ 图标里 |
| Dialog / AlertDialog / Sheet | `ui/dialog`、`ui/alert-dialog`、`ui/sheet` | 对象详情一律用 Sheet，由 `?inspect=` 打开；危险操作需要勾选确认后才能提交 |
| Popover / DropdownMenu | `ui/popover`、`ui/dropdown-menu` | 筛选改动先暂存，点“应用”才生效；危险菜单项放最后，红字并带省略号 |
| Calendar（日期选择） | `ui/calendar` + `ui/popover` | 预设放在左侧；不能选未来日期；按站点时区 |
| Command | `ui/command` | Ctrl K；范围限当前站点；结果按“页面 → 对象 → 操作”分组 |
| Sidebar | `ui/sidebar` | `collapsible="icon"`；计数徽章只用于需要人处理的事项 |
| Breadcrumb / 页头 | `ui/breadcrumb`、`blocks/page-header` | 面包屑只在管理类二级页面使用 |
| Data Table | `components/data-table`（tablecn 来源） | 行高 44（密集 36）；筛选、排序、分页、列显示都写进 URL；选中时出现批量操作条 |
| Pagination | `components/data-table/data-table-pagination` | 显示“共 N 条”、每页条数、页码 |
| Chart | `ui/chart`、`ui/composed-chart`、`ui/heatmap-chart`、`ui/sparkline`、`ui/bar-list`、`ui/distribution-bar` | 一张图只用一个单位；不做双 Y 轴；缺数据用斜线占位；同时提供表格视图 |
| Metric Card | `ui/kpi-card`、`ui/metric-value` | 五种状态：正常、无可比数据、缺数据、加载、出错 |
| Field（表单） | `ui/field` + react-hook-form + zod | 标签在上；错误信息替换说明文字；失焦时校验 |
| Toast | `ui/sonner` | 成功提示 4 秒后消失；错误提示不自动消失，并提供重试 |
| Alert / Empty / Skeleton / Spinner / Progress | 同名组件 | 空状态区分“筛选后为空”和“本来就没有” |

### 4.2 状态与结构规则

- **列表项**（需要处理、节能措施、命令结果）统一用两行语法：标题 + 时间或金额；类型 · 对象 · 状态 + 读数。
- **列表溢出**：放不下时不在卡片里滚动，底部写“还有 N 条 · 前往 … ›”。
- **对话框**：标题写成问题；主按钮写动词，不写“确定”；提交过程中不能关闭。
- **错误文案**：只写业务事实和下一步怎么做，不出现状态码或内部名称。

### 4.3 需要新增的外部组件

只新增以下 3 个，都通过 shadcn CLI 从 `components.json` 已登记的注册表安装：

| 组件 | 注册表 | 用途 | 不选其他方案的理由 |
|---|---|---|---|
| Filters | `@reui/filters` | 台账筛选条，形式为“字段 · 条件 · 值”胶囊 | 现有 `data-table-filter-list` 偏高级筛选；台账顶部需要更紧凑、可一眼读出当前条件的胶囊形式 |
| Choicebox | `@kibo-ui/choicebox` | 带说明的卡片单选（例如测量边界） | RadioGroup 无法承载说明文字 |
| Combobox | `@diceui/combobox` | 设备等长列表的搜索选择 | 也可以用现有 `command` + `popover` 自己组合；diceui 自带分组和虚拟滚动 |

评估过但不采用：

| 组件 | 不采用的原因 |
|---|---|
| `@reui/data-grid`、`@tablecn/data-grid` | 只在超过 1 万行或需要单元格编辑时再引入 |
| `@kibo-ui/contribution-graph` | 现有 `heatmap-chart` 已经够用 |
| `@kibo-ui/table` | 和已有的 tablecn 方案重复 |

## 5. 与现有代码的差异（实施清单）

1. **主色**：`global.css` 的主色从青绿 `#087f76` 改为靛蓝 `#3f5bd9`；`ring` 和 `sidebar-primary` 跟着改。
2. **画布与面板分层**：`--background` 从纯白改为 `#f7f7f8`，`--card` 保持白色，形成画布和面板两层。
3. **图表色**：`--chart-1..5` 换成 2.1.1 的已校验色；`--information` 并入 `--primary`。
4. **严重度变量**：名字保留，深色值按本系统调整；新增 `-soft` / `-text` 变体。
5. **扩展变量**：新增 2.1 中标为扩展的变量，以及 `--elevation-*`、`--radius-2xl`、`--ease-*`。
6. **组件替换**：DataTable 的筛选列表换成 ReUI Filters；节能“测量边界”改用 Choicebox。
7. **深色检查**：深色模式下主按钮文字为深色，实施后要用对比度检查确认。

## 6. 待确认

- 定稿中左栏面板内边距 18px 统一为 16px（视觉差异 2px）。
- 深色模式图表主色 `#6f87f7` 比定稿深色画板里的折线 `#7b93ff` 略深，这是为了与其余三色处在同一亮度带。
- 第二个站点的名称“B 区机房”是示意。
