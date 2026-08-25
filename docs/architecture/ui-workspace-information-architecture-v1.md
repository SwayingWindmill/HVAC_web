# Wayfinder #308：三个工作空间的 UI 信息架构

状态：DECIDED / SOURCE-BACKED  
审查日期：2026-08-25  
范围：冻结 Operations、Energy Management、Administration 的低保真信息架构、上下文层级、真实数据状态和事实/建议/动作边界。不实现页面、不新增 API、不修改现有路由或部署。

## 1. 冻结结论

HVAC UI 采用三个业务工作空间，外加一个不属于业务工作空间的全屏 Presentation surface：

```text
Tenant / Platform
├─ Operations          当前运行、资产、告警、诊断、工单、受治理控制
├─ Energy Management   能源事实、趋势、质量、计量来源、基线、成本、碳和优化建议
└─ Administration      Tenant/Site、Registry、Meter/MeterBinding、用户权限、规则、审计

Presentation surface: Big Screen / 投屏
```

这三个工作空间不是把现有菜单简单改名，而是按数据 owner 和用户动作重排：

- Operations 读取运行事实并处理运维事件；它可以发起 Command Preview，但不能绕过审批和反馈验证直接控制设备。
- Energy Management 读取固定 Energy Query 和 Energy Content 合同；它展示能源事实、质量和版本，不在浏览器里重新计算权威能耗。
- Administration 管理配置和发布内容；Meter、MeterBinding、Topology、用户、权限、规则和审计在这里进入治理流程。
- Big Screen 只是一种展示壳，不能产生新的事实、查询主体或控制权限。

当前 Real Mode 的 Site scope、Principal/capability、realtime status 和“不可用不伪造”的状态规则保留；Demo Mode 的旧菜单结构不享有默认正确性。

## 2. 源码证据

### 2.1 HVAC Web 当前真实实现

| 文件 | 源码事实 | 本票据裁决 |
| --- | --- | --- |
| [`real/SiteScopedShell.tsx`](../../apps/hvac-web/src/real/SiteScopedShell.tsx) | Real 导航按 Site 构建，当前包含 Dashboard、Assets、Energy、Forecast、Control、Optimize、FDD、Alarms、Work Orders、AI、Cost、Settlement 和 Big Screen；有 Site chooser/switcher。 | 保留 Site 是必须上下文；重排为三个工作空间，不把每个能力继续平铺为同级菜单。 |
| [`real/RealShellChrome.tsx`](../../apps/hvac-web/src/real/RealShellChrome.tsx) | Header 展示 Tenant、Site switcher、实时订阅状态、诊断信息和 logout；站点切换有未保存 draft 确认、purge、重建 protected scope。 | Adopt 现有 Shell；Workspace 切换不能绕过 Site scope、draft guard 或 realtime cleanup。 |
| [`real/RealDashboard.tsx`](../../apps/hvac-web/src/real/RealDashboard.tsx) | Dashboard 使用 `SiteDashboardSummary`，展示实时功率、COP、今日能耗、节能、设备 Population、告警和行动入口；显式标记 `READY/PARTIAL/STALE/UNAVAILABLE` 等状态。 | Operations 首页继续作为运行事实入口；不把成本、优化和 AI 预留块伪装成已接入指标。 |
| [`real/assets/RealAssetsWorkspace.tsx`](../../apps/hvac-web/src/real/assets/RealAssetsWorkspace.tsx) | 真实 Asset Model 有 Space → Asset → Device Endpoint → Sensor/Point 层级，提供树导航、Asset/Device/Point 台账、搜索、关注筛选、详情 Drawer 和当前状态。 | Adopt 资产层级和独立详情；Space/Asset 选择只改变资产上下文，不能自动变成 Energy Series 的 subject。 |
| [`real/EnergyAnalytics.tsx`](../../apps/hvac-web/src/real/EnergyAnalytics.tsx)、[`real/energy-workspace.ts`](../../apps/hvac-web/src/real/energy-workspace.ts) | Energy 查询使用 Site、时区、quality policy、from/to；日/周/月/年是 UI 时间窗口，映射到 hour/day/month 查询粒度；页面显示 total、比较、quality、partial、stale、watermark 和 dataset revision。 | 保留窗口式分析；把“周/年”理解为日期窗口而不是新增 Backend granularity；Energy 页面必须继续消费固定查询合同。 |
| [`api/energy-analytics.ts`](../../apps/hvac-web/src/api/energy-analytics.ts) | Zod schema 只允许 electricity、hour/day/month、IANA timezone、VALID_ONLY/VALID_AND_SUSPECT；响应无效时不使用缓存或推断值。 | 状态和查询约束进入 Energy Management 的共享 UI contract；不在页面层增加 Meter/Space/Asset 任意过滤。 |
| [`real/RealSystemManagement.tsx`](../../apps/hvac-web/src/real/RealSystemManagement.tsx)、[`real/registry-admin/RegistryAdministration.tsx`](../../apps/hvac-web/src/real/registry-admin/RegistryAdministration.tsx) | System 页面已有治理概览、用户与角色、站点与租户、Registry 管理、数据接入、规则和审计 Tabs；Registry 已有 Site 选择、资源与绑定、Template Revision、Import/Export 和 dirty draft guard。 | Administration 以现有 System/Registry owner 为基础，新增 Meter/MeterBinding 管理，不另造 Energy Settings 页面。 |
| [`components/PageState.tsx`](../../apps/hvac-web/src/components/PageState.tsx)、[`real/realtime-status.ts`](../../apps/hvac-web/src/real/realtime-status.ts) | 旧页面有 403、readonly、empty、loading、error；Real realtime 有 idle/connecting/live/reconnecting/resync-required/unavailable，并把传输故障与设备离线分开。 | 统一真实状态语言；权限、能力未接入、数据缺口、传输故障和质量问题不能合并成一个“暂无数据”。 |
| [`layout/AppShell.tsx`](../../apps/hvac-web/src/layout/AppShell.tsx)、[`store/ui.ts`](../../apps/hvac-web/src/store/ui.ts) | Demo 菜单把 `/assets` 等放入“运营管理”，把 `/energy`、`/cost`、`/ai` 放入“分析中心”，另有“展示”和旧角色/demoMode。 | 标记为 LOCAL-CHANGE：Demo 菜单不是 Real IA 基线；最终迁移以 Real Shell 和本票据为准，删除 obsolete 平铺路径而不是长期双轨兼容。 |

### 2.2 三个参考项目的实际 UI 源码

固定提交和总研究记录见 [`wayfinder-energy-reference-source-review-2026-08.md`](../research/wayfinder-energy-reference-source-review-2026-08.md)。本票据实际核对了以下生产源码：

| 项目 | 固定提交与源码事实 | HVAC 决策 |
| --- | --- | --- |
| ThingsBoard CE | [DashboardPageComponent](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/ui-ngx/src/app/modules/home/components/dashboard-page/dashboard-page.component.ts) 持有 DashboardConfiguration、Layout/Breakpoint、Widget、Alias、State、编辑态和 readonly；[EntityDataSubscription](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/ui-ngx/src/app/core/api/entity-data-subscription.ts) 同时组织 history、Latest、aggregation、comparison 和 WebSocket update。 | **ADAPT**：吸收“组合视图”和“事实数据订阅独立于 Widget”的思想；不引入通用 Dashboard runtime，不让 Widget 配置成为能源事实或 Energy Query。 |
| OpenEMS | 本地 `E:\Code\openems` checkout 实际核对的固定提交源码：[app-routing.module.ts](https://github.com/OpenEMS/openems/blob/2e2792d59fc5ba3b99ce3cf98d15081c0a74895e/ui/src/app/app-routing.module.ts) 把 `/overview` 与 `/device/:edgeId` 分开，Edge 下再进入 `live`、`settings`、`favorites`、`profile`；[app.component.html](https://github.com/OpenEMS/openems/blob/2e2792d59fc5ba3b99ce3cf98d15081c0a74895e/ui/src/app/app.component.html) 依据 NavigationService 在侧栏、底部栏和移动菜单间切换；[overview.component.ts](https://github.com/OpenEMS/openems/blob/2e2792d59fc5ba3b99ce3cf98d15081c0a74895e/ui/src/app/index/overview/overview.component.ts) 以分页 Edge 列表为入口；[navigation.service.ts](https://github.com/OpenEMS/openems/blob/2e2792d59fc5ba3b99ce3cf98d15081c0a74895e/ui/src/app/shared/components/navigation/service/navigation.service.ts) 根据当前 Edge config 和用户动态生成 NavigationTree。 | **DEFER/ADAPT LATER**：保留未来 Edge 入口与运行/设置分离的启发；当前 Backend/UI 不把 Edge、Channel、Cycle、Controller 混入三个工作空间。 |
| MyEMS | [myems-web/src/routes.js](https://github.com/MyEMS/myems/blob/be6e6ce8ddeac57afb04bddb9621501fb555cab0/myems-web/src/routes.js) 按 Space、Equipment、Meter、Tenant、Store、Shopfloor 等对象复制 energy/carbon/cost/saving/plan/prediction/comparison 页面；[MeterController](https://github.com/MyEMS/myems/blob/be6e6ce8ddeac57afb04bddb9621501fb555cab0/myems-admin/app/controllers/settings/meter/meter.controller.js) 维护 Meter 树、搜索、子表、CRUD、导入导出和复制；[MeterService](https://github.com/MyEMS/myems/blob/be6e6ce8ddeac57afb04bddb9621501fb555cab0/myems-admin/app/services/settings/meter/meter.service.js) 通过独立 API wrapper 访问这些操作。 | **ADOPT/REJECT**：采用 Admin/Web 分离和 Meter 内容管理；拒绝按每个对象复制一整套页面，先用固定 Site Energy Query + 资产上下文，后续维度只有在 Backend 合同毕业后再增加。 |

## 3. 低保真总结构

### 3.1 共同 Shell

```text
┌────────────────────────────────────────────────────────────────────┐
│ Tenant · Site switcher · realtime status · user / diagnostics       │
├───────────────┬────────────────────────────────────────────────────┤
│ Workspace nav  │ Breadcrumb / Site · page title · source revision   │
│                ├────────────────────────────────────────────────────┤
│ Operations     │ Summary / filters / permitted actions              │
│ Energy Mgmt    ├────────────────────────────────────────────────────┤
│ Administration │ authoritative content: table / chart / detail      │
│                ├────────────────────────────────────────────────────┤
│                │ state strip: access · loading · partial · stale...  │
└───────────────┴────────────────────────────────────────────────────┘
```

共同 Shell 规则：

- Tenant 来自可信 Principal，不让用户在页面中自由切换 Tenant。
- Site 是当前阶段所有业务页面的必选 scope；Site switcher 只展示授权 Site，切换时沿用现有 draft confirmation、取消旧请求、清理 protected scope 和重新激活流程。
- Header 的 realtime status 只表达订阅链路；它不能替代 Energy `dataWatermark`/`aggregateWatermark`，也不能把“实时链路断开”翻译成“设备离线”。
- 每个页面 header 需要能回答“当前 Site、查询/内容来源、版本或水位线、是否可执行动作”；不把内部 Principal/Policy 诊断信息扩散到业务卡片。
- 页面状态优先于空图表：没有数据时显示 `NO_DATA/EMPTY`，不可用显示原因和 retryability，未接入显示合同缺口，不用 0、模拟曲线或旧缓存填满版面。

### 3.2 目标导航树

```text
Operations
├─ 运行总览                         /sites/:siteId/dashboard
├─ 资产与设备                       /sites/:siteId/assets
│  └─ Asset / Device / Sensor / Point 详情 Drawer 或详情页
├─ 告警                             /sites/:siteId/alarms
├─ FDD 诊断                          /sites/:siteId/fdd
├─ 工单                             /sites/:siteId/work-orders
├─ AI 调查                          /sites/:siteId/operations
└─ 控制预览与执行                   /sites/:siteId/control

Energy Management
├─ 能耗分析                         /sites/:siteId/energy
│  ├─ Site Energy Series
│  ├─ 质量 / partial / watermark / dataset revision
│  └─ Meter / Binding 来源摘要（只读内容入口）
├─ 预测与基线                       /sites/:siteId/forecast
├─ 成本与绩效                       /sites/:siteId/cost
├─ 结算与对账                       /sites/:siteId/settlement
└─ 优化建议                         /sites/:siteId/optimize

Administration
├─ 治理概览                         /system?tab=overview
├─ Tenant / Site / 用户 / 角色       /system?tab=site|users
├─ Registry 资源与绑定               /system?tab=registry
│  ├─ Space / Asset / Device / Point
│  ├─ Energy Meter
│  ├─ MeterBinding / Topology version
│  └─ Template / Import-Export / draft protection
├─ 规则与自动化                     /system?tab=rules
├─ 数据接入                         /system?tab=integrations
└─ 审计                             /system?tab=audit

Presentation surface
└─ 运行大屏                         /sites/:siteId/bigscreen
```

这里的路径是现有 Real route 的信息架构映射，不批准本票据内的 URL migration。实现时可以先保持现有 path，在 navigation manifest 中切换 workspace owner；旧 Demo `/dashboard`、`/assets`、`/energy` 等路径不是第二套业务合同。

## 4. 上下文层级

### 4.1 Tenant、Site、Space、Asset

| 层级 | UI 作用 | 当前数据依据 | 限制 |
| --- | --- | --- | --- |
| Tenant | 安全和权限根上下文，显示在可信 Shell 中 | Principal context、IAM capabilities | 不可由页面输入覆盖；不能跨 Tenant 组合数据。 |
| Site | 当前阶段所有 Operations/Energy/Content 查询的必选 scope | Registry Site、SiteDashboardSummary、Energy Series | Site switch 触发 protected scope 清理；未授权 Site 不显示其存在原因。 |
| Space | Asset Model 的层级导航和区域语境 | Real Assets 的 hierarchy tree、Space children | 当前不改变 Energy Series subject；不能因为 UI 有 Space tree 就请求 Space 能耗。 |
| Asset | 设备/建筑实体的运行和详情入口 | Asset Model、Device Binding、Telemetry snapshot | 进入 Asset detail 后可读 Device/Sensor/Point 当前状态；Energy 仍使用已冻结 Site Query。 |
| Meter / Binding | Energy 来源解释、配置管理和处理 provenance | #307 Energy Content、Registry Meter/MeterBinding | Administration 管理；Energy Management 只读显示来源摘要，Processing 使用私有 resolver。 |

### 4.2 Energy 页面中的内容上下文

Energy Management 首页在当前切片只显示：

- 当前 Site 和 Site timezone；
- 查询时间窗口和 UI period preset；
- 当前 energy type `electricity`、quality policy；
- 返回粒度、total/比较结果、quality summary；
- `dataWatermark`、`aggregateWatermark`、`datasetRevision`、`partial/stale`；
- 已发布 PRIMARY Meter/Binding 的只读来源摘要，包括 Registry revision、Topology/Binding version、effective interval 和状态。

它不显示一个“Space/Meter/Asset 任意选择器”然后声称结果已经是该实体的权威能耗。后续要支持 Space、Asset、Meter subject，必须先新增对应 Backend 查询合同、授权 scope 和事实维度。

## 5. 三个工作空间的页面边界

### 5.1 Operations

Operations 的首页是 `运行总览`，不是全平台 KPI 墙：

```text
运行总览
├─ 关键运行事实：功率、COP、设备 Population、活动告警
├─ 需要关注：离线/陈旧/未知设备、活动告警、FDD finding
├─ 快速入口：资产、告警、FDD、工单、AI 调查
└─ 受治理动作：进入 Control Preview / Optimization Review
```

页面只显示当前 `SiteDashboardSummary`、Asset Model、Alarm/FDD/Work Order read model 已返回的事实。优化推荐、AI 分析、FDD 结论显示为建议/证据对象，并带其来源窗口、revision、风险和下一步；它们不能直接改变设备状态。

`资产与设备` 保留当前三栏骨架：左侧 Space/Asset hierarchy，中间可切换 Asset/Device Endpoint/Point 台账，右侧详情和实时状态。该页面是 Operations 的运行入口，不承担 MeterBinding 配置；Energy Meter 只在 Administration 管理，详情中可显示“关联计量来源”只读链接。

`告警/FDD/工单/AI 调查` 保持对象分离：Alarm 是运行事件，FDD Finding 是带证据的诊断事实，Work Order 是执行治理对象，AI Investigation 是可审计调查过程。不能用一个“异常列表”吞掉这些不同生命周期。

`控制预览与执行` 只允许：目标选择 → 当前反馈读取 → 请求/范围/风险预览 → 人工确认 → Command 生命周期 → 反馈验证。Dashboard、Energy 图表和 AI 建议都只能跳转到这里，不能在卡片里直接下发控制。

### 5.2 Energy Management

```text
能耗分析
├─ 条件栏：period、anchor、timezone、quality policy
├─ 来源栏：Site + PRIMARY Meter/Binding 摘要 + Registry revision
├─ KPI：周期总电能、比较、平均/峰谷、质量汇总
├─ 趋势：Site Energy Series，缺失 bucket 保持空
├─ 完整性：partial、data/aggregate watermark、dataset revision、stale
└─ 下钻：只进入已支持的时间窗口或 Administration 内容详情
```

日/周/月/年是前端窗口：day 通过 hour 查询，week/month 通过 day 查询，year 通过 month 查询。UI 不将这些窗口名误写成新的 API granularity。

`预测与基线`、`成本与绩效`、`结算与对账` 和 `优化建议` 可以保留入口，但在各自 Backend read model 尚未闭合时必须显示 `NOT_INTEGRATED`，并明确需要的合同字段。它们不能用 Energy Series 总量乘演示电价、浏览器公式或空值填成 0。

优化建议属于 Energy Management 的建议页面；被批准的动作再转入 Operations 的 Control Preview。建议页面必须展示收益、约束、不确定性、风险、回滚和验证计划，和当前 `RealOptimizePage` 的证据边界保持一致。

### 5.3 Administration

Administration 保持 Tenant/Platform scope 的治理入口，Site 作为选择器而不是隐含的全局变量：

```text
Registry 管理
├─ Site / Space / Asset / Device / Point
├─ Energy Meter
│  ├─ list/detail
│  ├─ device + energy type
│  └─ ACTIVE / INACTIVE / RETIRED
├─ MeterBinding
│  ├─ topology / edge / meter / device / point
│  ├─ PRIMARY / CHECK / MONITORING / BACKUP
│  ├─ effective interval / version / status / revision
│  └─ released content 只读检查与 draft/release 治理
└─ Import / Export / Template / unsaved draft guard
```

Meter/MeterBinding 页面只使用 #307 冻结的 Content API：签名 keyset cursor、授权 action、Registry revision 和 status/effective time。列表不能用 offset，也不能把当前 UI 表格里的本地分页当作后端授权分页。

Administration 还保留用户/角色、数据接入、规则和审计 Tabs。规则编辑必须显示 draft、validation、revision、simulation/evidence 和 release/retire 状态；审计记录只显示服务器返回的事实。浏览器不得创建本地“成功”状态。

## 6. 真实数据状态语言

### 6.1 通用状态

| 状态 | 页面表达 | 用户动作 |
| --- | --- | --- |
| `FORBIDDEN` / `NOT_AUTHORIZED` | 页面存在但当前 Principal 无权读取/执行；不泄露资源是否存在 | 返回已授权入口；不自动调用接口。 |
| `LOADING` / `CHECKING` | 明确说明正在读取哪个权威 owner | 等待；不展示旧缓存或模拟值。 |
| `EMPTY` / `NO_DATA` | 当前 scope 没有返回事实；数值显示 `—` 或空状态，不显示 0 | 调整已支持的时间/筛选，或进入配置/接入说明。 |
| `PARTIAL` | 显示成功部分和缺口范围；图表缺 bucket 保持空 | 查看水位线/质量，必要时刷新。 |
| `STALE` | 显示最后数据水位和请求结束时间的关系；不是“设备离线” | 刷新或转到数据接入/运行状态。 |
| `SUSPECT` | 显示可疑质量数量和 quality policy | 切换允许的质量策略或查看来源，不把可疑值标成正常。 |
| `UNAVAILABLE` | 显示 owner、Problem code、traceId 和 retryable；仅 retryable 才显示重试 | 重试或联系 owner；不自动降级为 Mock。 |
| `NOT_INTEGRATED` | 显示缺少的 Backend read model/字段 | 返回可用页面；不渲染静态假图表。 |

### 6.2 Energy 和 Content 专属状态

- Energy Series 的 `partial`、`qualitySummary`、`dataWatermark`、`aggregateWatermark`、`datasetRevision` 是权威数据元数据；页面不得用前端刷新时间代替 watermark。
- Energy Content 的 `revision/version/status/effectiveFrom/effectiveTo` 是 Registry 内容元数据；它不代表时序 freshness。
- Resolver `NO_MATCH` 在 Energy Processing 侧表示不生成 Fact；在 UI 中可显示为“当前时间没有适用 Binding”，不能转成 0 kWh。
- Resolver `AMBIGUOUS` 表示发布内容冲突；Administration 显示冲突的 Binding/Topology 版本并进入修复流程，Energy 页面停止给出看似确定的来源结论。
- `RELEASED/ACTIVE/SUPERSEDED` 是配置发布状态；UI 不通过“最后更新时间”推断当前有效版本。

## 7. 事实、建议和待审批动作

| UI 内容 | 类型 | 允许呈现 | 禁止行为 |
| --- | --- | --- | --- |
| SiteDashboardSummary、Energy Series、Asset Model、Device Snapshot、Meter/MeterBinding | 事实/权威投影 | 展示值、状态、来源、revision/watermark、缺口 | 浏览器重算权威总量、用零填充、跨 Site 拼接。 |
| FDD Finding、Optimization Recommendation、Forecast Snapshot、AI Investigation | 建议/证据 | 展示评估窗口、证据、模型/规则 revision、不确定性、风险、下一步 | 伪装成 Alarm、Command 或已执行结果。 |
| Command Preview、Work Order、Rule Release、Registry Release | 待审批/治理动作 | 展示目标、权限、diff、验证、审批人、生命周期和审计引用 | 在 Dashboard/Energy 卡片中直接执行；本地写成功状态。 |
| Big Screen | 展示 surface | 读取已存在的权威 summary，并显示整体状态 | 创建新的指标口径、独立缓存或隐藏 partial/stale。 |

## 8. 对当前项目的直接修改要求

这些是从源码冲突中得出的 LOCAL-CHANGE，不在本票据内实现：

1. Real navigation 需要把当前平铺的 `site-forecast/site-cost/site-settlement/site-optimize` 归入 Energy Management，把 `site-control` 归入 Operations；`site-bigscreen` 保持 Presentation surface，不作为第四业务工作空间。
2. 当前 Demo `AppShell`/`store/ui` 的角色和 demoMode 不能继续作为 Real IA；逐步迁移后删除 obsolete 的旧入口，不添加兼容双路由。
3. `RegistryAdministration` 是 Administration 的正确 owner；Meter/MeterBinding 不应新建一个独立 Energy 页面或被塞进 Energy Series chart filter。
4. Real Assets 的 Space/Asset hierarchy 继续做资产上下文；Energy 查询不因 UI 下钻而自动增加 Space/Asset subject。
5. 旧页面的 `forecast/cost/settlement` 必须继续使用 `NOT_INTEGRATED`/真实 read model 状态，直到对应 Backend 合同完成；不因 IA 规划而填充静态 KPI。
6. UI 需要抽出一套共享状态呈现规则，使 `quality/partial/stale/watermark/datasetRevision`、Registry `revision/status` 和权限/不可用状态在三个工作空间一致，但不新增一个包揽所有领域的前端状态模型。

## 9. 后续边界

本票据冻结信息架构，不实现 UI。下一步需要单独决定：

- #310：Energy Slice 真实实现规格、Schema、resolver、Fact projector 和最小验收门禁；
- 未来 UI implementation ticket：只实现已有 API 的 Energy Content client、Administration Meter/Binding list/detail 和 Energy source summary；
- Cost/Carbon/Baseline/Report/Virtual Meter：先完成各自 Backend 查询合同，再进入 Energy Management 页面，不复制 MyEMS 的全量对象菜单。

明确拒绝：不复制 ThingsBoard 的任意 Widget/Dashboard runtime，不复制 MyEMS 按对象复制全套 energy/carbon/cost 页面，不把 OpenEMS Edge 的 Live/Settings/Channel 导航引入当前 Backend/UI，也不把 Big Screen 作为新的数据 owner。
