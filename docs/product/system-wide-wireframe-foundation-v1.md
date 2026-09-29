# System-wide Wireframe Foundation v1

> 状态：ACTIVE / FOUNDATION IMPLEMENTED  
> 日期：2026-09-15  
> 范围：Canonical Surface Catalog、Application Shell、导航投影、共享页面 Anatomy、03 站点总览基准 Wireframe、Browser Review。

## 1. 目标

本阶段不再依据旧前端页面反推产品结构，也不保留旧 Dashboard / Monitor / Assets / Optimize / Settlement 等历史信息架构作为设计约束。

新的实现权威顺序固定为：

```text
01–36 Surface Specifications
→ Global Navigation / Context / Interaction Contract
→ shadcn/ui current component semantics
→ satnaing/shadcn-admin application composition
→ DESIGN.md
→ domain truth / capability / permission projection
→ implementation
```

旧前端仅可作为数据 API、runtime、capability、owner 边界的工程证据，不作为页面结构、路由命名、导航分组或视觉设计的权威。

## 2. 外部实现参考

本阶段固定参考：

- shadcn/ui Sidebar：`https://ui.shadcn.com/docs/components/sidebar`
- shadcn/ui Dashboard 示例：`https://ui.shadcn.com/blocks`
- shadcn/ui 组件文档：`https://ui.shadcn.com/docs/components`
- satnaing/shadcn-admin：`https://github.com/satnaing/shadcn-admin`

采用原则：

```text
学习并采用
- SidebarProvider / Sidebar / SidebarInset / SidebarTrigger
- SidebarHeader / Content / Group / Menu / Rail
- desktop icon collapse + mobile Sheet
- compact sticky Header
- workspace/site switcher
- command search
- px-4 py-6 / desktop px-6 的主内容密度
- shadcn neutral application grammar
- Radix focus / dialog / dropdown semantics

不复制
- 示例业务路由
- 示例 Dashboard KPI
- 示例数据模型
- 与能源业务无关的页面分类
- shadcn-admin 的任何业务演示内容
```

shadcn-admin 是应用布局与组合参考，不是产品模板依赖；shadcn/ui 是源码级组件规则和交互语义基线。

## 3. Canonical Surface Catalog

代码权威：

`apps/hvac-web/src/app/surface-catalog.ts`

Catalog 对 01–36 每个 Surface 明确定义：

```text
id
business title
short navigation label
group
scope
canonical route
detail routes
navigation ownership
surface pattern
```

Surface Pattern 固定为七类：

```text
overview
ledger
analysis
engineering
detail
control
governance
```

这不是七套视觉主题，而是七种任务结构。所有 Pattern 继续使用同一个 Shadcn Application System。

## 4. 导航所有权

导航不再由散落在 TanStack Route 文件中的历史 `staticData.navigation` 定义。

新链路是：

```text
Surface Catalog
+
当前实施成熟度
+
Site Context
+
Capability / Permission
↓
Navigation Projection
↓
Sidebar
```

当前 Foundation 阶段只有已经完成新 Wireframe 的 Surface 才进入 `WIREFRAME_READY_SURFACE_IDS`。

当前已通过 Wireframe + Browser Review：

```text
03 站点总览
04 系统运行
09 告警中心
14 能源分析
25 控制中心
```

这意味着旧页面即使仍暂时存在于代码和历史 URL 中，也不会因为“代码还在”而自动重新成为新产品导航的一部分。

后续每完成一张正式 Wireframe 并通过浏览器验收，再把对应 Surface 加入 ready set。

## 5. Application Shell v1

新的 Shell 采用 shadcn Sidebar 组合语法：

```text
SidebarProvider
├── AppSidebar
│   ├── SidebarHeader
│   │   └── Product / Site Switcher
│   ├── SidebarContent
│   │   └── grouped SidebarMenu
│   └── SidebarRail
└── SidebarInset
    ├── AppHeader
    └── Route-owned Surface
```

Desktop：

- Sidebar：240px；
- icon collapse：48px；
- Header：56px；
- Main：`px-4 py-6`，desktop `px-6`；
- 普通 Surface 最大宽度约 1600px；
- Command Search 使用 Dialog 语义；
- Site Switcher 使用 Dropdown Menu；
- `Ctrl/Cmd + B` 切换 Sidebar；
- `Ctrl/Cmd + K` 打开 Command Search。

Narrow / mobile：

- Sidebar 切换为 Sheet；
- 页面主体不依赖桌面侧栏宽度；
- 不通过缩放桌面布局制造“移动端”；
- 不允许页面产生横向滚动才能完成主要判断。

## 6. Shared Surface Anatomy

新增共享结构：

- `components/layout/Main.tsx`
- `components/layout/PageIntro.tsx`

统一页面语法：

```text
Page Intro
├── current scope / context
├── business title
├── one-line purpose
├── freshness / period / source context
└── local actions

Main Workspace
├── authoritative summary
├── primary evidence / ledger / canvas
├── contextual evidence
└── explicit next action
```

禁止恢复 boxed PageHeader，也不建立吞掉 Surface 差异的 mega page component。

## 7. 03 站点总览基准 Wireframe

03 被定义为 `Site Attention Router`，不是 KPI Dashboard。

当前顺序：

```text
站点上下文 + Page Intro
↓
关键事实条
- 重要活动告警
- 需要关注的工作
- 当前功率
- 关键数据可信度
↓
优先处理 | 当前运行
↓
能源与绩效
↓
数据可信度
```

首屏目标：

用户在一个桌面视口中至少能够回答：

1. 当前看的是哪个站点；
2. 有没有重要告警或需要关注的工作；
3. 当前系统主要设备群怎么运行；
4. 数据是否足够可信；
5. 下一步应该进入告警、工单、系统运行还是能源分析。

### 不做的事情

03 当前明确不做：

- KPI Card Wall；
- opaque health score；
- 把 offline 当 fault；
- 把 stale 当 offline；
- 把当前快照差异拼成“最近事件”；
- 在没有明确 baseline 定义时伪造节能趋势；
- 在首页复制完整告警中心、工单中心、设备中心或能源分析。

`最近变化` 只有在存在可确认发生时间与业务含义的 authoritative event owner 时才进入页面；当前没有该 owner，因此模块不展示。

能源比较曲线只有在时间范围、baseline/comparison 口径和数据质量语义明确后才展示；当前保留真实的当前功率、本业务日用能、COP 与分项用能，不用伪曲线填充视觉空间。

## 8. Content Design

本 Foundation 延续全局用户可理解性规则：

```text
用户首先看到：
范围
事实
原因
可信度
下一步

用户默认不看到：
UUID
traceId
raw enum
schema revision
projection/read-model 名称
内部 transport 错误
```

关键状态保持严格独立：

```text
Offline ≠ Fault
Stale ≠ Offline
Missing ≠ Zero
Unknown ≠ Healthy
ACK ≠ Cleared / Verified
Approved ≠ Executed
```

## 9. Browser Review Gate

Review 脚本：

`scripts/run-overview-browser-review.mjs`

Fixture：

`scripts/fixtures/overview-review/main.tsx`

该 Review 不再渲染旧 System Operations 页面作为设计权威，而是渲染：

```text
new SidebarProvider
+ new AppSidebar
+ new SidebarInset
+ new AppHeader
+ Surface 03
```

当前实际验证结果：

```text
Desktop 1672 × 941
Sidebar width = 240px
Header height = 56px
Priority / Current Operation 在首屏
Ant DOM = 0
Runtime errors = 0

Narrow 720 × 900
Navigation = mobile Sheet
horizontal overflow = 0
Ant DOM = 0
```

截图输出：

```text
out/overview-review/overview-desktop.png
out/overview-review/overview-narrow.png
```

## 10. 后续推进规则

后续校准 Surface 按：

```text
25 控制中心
31 数据质量
01 企业总览
```

但每一张都必须：

1. 先依据自己的 Surface Specification 做 Wireframe；
2. 使用同一 Shell / Page Intro / Context 语言；
3. 按自己的任务 Pattern 组织，而不是复制 03；
4. 通过真实浏览器 desktop + narrow review；
5. 通过 accessibility / focus / semantic review；
6. 通过后才进入 `WIREFRAME_READY_SURFACE_IDS`；
7. 不为了保留旧页面而建立 compatibility adapter。

因此下一步进入 **25 控制中心 Wireframe**，用 safety-critical control workspace 类型检验当前 Foundation 是否能承载控制意图、前置条件、风险确认、执行反馈、回退路径和审计证据，同时保持“控制动作与监控事实分离”的原则。
