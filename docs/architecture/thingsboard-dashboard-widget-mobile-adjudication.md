# ThingsBoard CE Dashboard、Widget、SCADA 与移动端裁决

状态：`D07_ADJUDICATION_COMPLETE`

审查票：[审查 Dashboard、Widget、移动端与展示扩展](https://github.com/SwayingWindmill/HVAC_web/issues/246)

本文裁决 ThingsBoard CE v4.3.1.1 的 Dashboard CRUD/分配/公开/导入导出、State、Layout、Entity Alias、Time Window、Widget、Widget Bundle、自定义 HTML/CSS/JavaScript、Resource、SCADA Symbol、移动端 Dashboard、Mobile App/Bundle/OAuth/QR 与展示扩展，并将其与 HVAC Web 当前 Real Mode 做源码级反向审查。

本文不假设 ThingsBoard 或本地实现已经正确，也不在本票内改变运行时产品行为。Demo Mode 不进入产品功能裁决；它只作为 Real Build 隔离是否有效的负面证据。

裁决词汇：

- `ADOPT`：行为和边界可直接成为目标设计；
- `ADAPT`：吸收模式，但按 HVAC Domain、安全、质量和数据权威重做；
- `KEEP`：本地实现有明确场景、正确性或安全证据，应保留；
- `REPLACE`：本地或上游行为存在实质冲突，应由目标行为替换；
- `REJECT`：明确不进入本项目；
- `DEFER`：有潜在价值，但当前没有产品或运行证据支持实施。

## 1. 执行结论

客观结论不是“本地固定页面优于 ThingsBoard”，也不是“ThingsBoard 功能更多所以应照搬”。两边解决的问题不同，且两边都有必须拒绝或重构的行为。

### 1.1 ThingsBoard 值得吸收的部分

1. **Dashboard 的组合词汇成熟。** State、Layout、Breakpoint、Entity Alias、Datasource、Time Window、Action 形成完整的钻取和复用模型，比本地把所有页面结构直接写进一个 React 组件更系统。
2. **展示配置与运行数据分离。** Dashboard JSON 保存布局和引用；Widget 通过 Datasource/Entity Query 读取数据。该分离模式值得吸收，但 Dashboard JSON 不能成为业务数据权威。
3. **Widget 生命周期和异常占位成熟。** Widget 初始化、订阅、时间窗更新、取消订阅、销毁、缺失类型和编译失败均有明确路径。
4. **响应式布局能力比本地完整。** 每个 Dashboard State 可有 Default/SCADA/Divider Layout，并可针对系统或自定义 Breakpoint 配置不同位置、尺寸、隐藏和移动端顺序。
5. **资源预算入口值得参考。** Server Time、最大 Datapoint、租户实体数量、分页、Widget Bundle、资源库和移动端可见页为通用平台提供了可运营边界。
6. **Dashboard 分配、Home Dashboard、移动端排序和隐藏**解决了不同用户入口和设备形态下的展示组织问题。
7. **SCADA Symbol 的 SVG + Metadata + Behavior 参数模型**适合表达设备图元与运行状态之间的绑定，比在大屏中硬编码一套 3D 场景更可维护。

### 1.2 ThingsBoard 不适合直接复制的部分

1. **配置校验过浅。** 固定源码中的 `DashboardDataValidator` 只校验 Title、Tenant 和租户数量，没有校验 State、Layout、Alias、Widget、Datasource、Action 或资源引用的完整性。Dashboard 配置仍是开放 `JsonNode`/`[key: string]: any`。
2. **自定义 Widget 是同源代码执行面。** `compileTbFunction` 使用 `new Function(...)`；Widget Controller、数据后处理、条件函数、Widget Action 和 Mobile Action 都可在浏览器执行自定义 JavaScript，并可加载外部 JavaScript/CSS 或扩展模块。
3. **Widget Type 服务端只要求 Descriptor 非空。** `WidgetTypeDataValidator` 不对 Controller Script、HTML、CSS、URL、模块权限或可调用 API 做安全语义验证。
4. **通用 Control/SCADA 可直接 RPC、写 Attribute 或写 Telemetry。** 这绕过 HVAC Command Intent、Preview、Risk、Step-up、Approval、Fence、Dispatch 和 Verification 状态机。
5. **公开 Dashboard 的匿名链接不适合作为生产运营默认能力。** 即使 Dashboard 只读，底层 Device/Asset 也需公开，扩大了数据暴露面；公开链接也不是人员授权、审计和最小 Site Scope 的替代品。
6. **Entity Alias 很灵活，但容易成为隐藏的查询语言。** 关系、类型和状态参数解析若没有 Site 强约束，容易让展示配置扩大资源发现范围、产生高代价查询或在迁移后静默变空。
7. **SCADA 固定坐标和通用写行为不等于安全 HMI。** 它适合绘图，但不能直接成为 HVAC 控制面。
8. **移动 App Secret 以明文字段持久化。** 固定源码中的 `MobileAppEntity.appSecret` 直接保存并查询，不能复制为本项目移动认证设计。

### 1.3 本地 Real Mode 应保留的部分

1. `RealApp -> SiteScopedShell -> ReadySiteSurface` 的 Real Build、认证、UUIDv7 Site、`site.read`、Capability 和资源不可见性边界应 `KEEP`。
2. Session、Policy Revision、Tenant、Site 组成 Protected Scope，切换时清理 Query/临时状态，应 `KEEP`。
3. Real Dashboard 不用 Demo/Mock 填空，未接入 KPI 显示 `—` 并写明原因，应 `KEEP`。
4. Device Presence 将 `OFFLINE`、`STALE`、`UNKNOWN`、`UNAVAILABLE`、`NOT_APPLICABLE` 分开，Energy 将 `EMPTY`、`PARTIAL`、`STALE`、`SUSPECT` 分开，应 `KEEP`。
5. Dashboard、BigScreen 均保持只读；控制留在显式 Command Domain，应 `KEEP`。
6. Real Dependency Graph 和 Bundle Gate 已阻断 Demo Dashboard、Demo BigScreen、Mock API 和本地角色模拟进入 Real Build，应 `KEEP`。

### 1.4 本地 Real Mode 必须重构的部分

1. **伪实时标签。** `RealDashboard` 没有建立实时订阅，却显示“实时连接 · HH:mm:ss 更新”。该时间只是前端 `asOf`，不是连接状态，也不是数据 Watermark，必须 `REPLACE`。
2. **“今日能耗”口径错误。** 当前查询是 `asOf - 24h -> asOf` 的滚动 24 小时，不是 Site 时区的本地日，必须 `REPLACE`。
3. **前端枚举设备计算站点 KPI。** 页面最多加载两页、截取 100 台设备，再计算“系统可用率”和设备类型分布。即使 `inventoryPartial` 被标记，数值仍以站点级语言展示，必须 `REPLACE` 为后端 Business Projection。
4. **空数据被宣称健康。** `businessState === EMPTY` 时仍显示绿色“整体健康”；这直接违反 `Missing ≠ 0 ≠ Healthy`，必须 `REPLACE`。
5. **质量降级没有进入页面总状态。** Energy 的 `STALE`/`SUSPECT` 只写在单卡片脚注，页面仍可能是 `READY`/“整体健康”，必须 `REPLACE`。
6. **无权限或部分样本仍可出现“设备在线状态稳定”。** `canListDevices=false`、Presence Partial 或 Inventory Partial 时，只要样本内 Attention 为 0，Focus Item 就显示稳定，必须 `REPLACE`。
7. **更新时间不是数据时间。** KPI 没有使用 Response `aggregateWatermark`、`dataWatermark`、Presence `evaluatedAt` 或 Realtime `lastMessageAt`；必须按指标显示权威时间与质量。
8. **BigScreen 不是已实现能力。** 它安全地显示空值，但没有读取 Registry、S2、Energy、Alarm 或 Dashboard Read Model，不能宣称“运行大屏已接入”。它必须与 Dashboard 共用 Read Model，不能拥有独立静态数据集。
9. **页面级测试不足。** 当前测试覆盖 Projection 状态与 Site 路由，但未覆盖伪实时标签、Site 本地日、部分样本、EMPTY 健康、BigScreen 数据复用和质量传播。
10. **固定页面的导航/筛选状态不足。** Site 在 URL 中，但 Dashboard State、Device Drill-down、Time Range 和 Quality Policy 尚未成为可分享、可回溯的 URL Context。

因此 D07 的最终裁决是：**保留本地 Real Mode 的安全边界和失败关闭语义；立即替换 Dashboard 的事实性误导和浏览器聚合；吸收 ThingsBoard 的 State、Datasource、Time Window、Breakpoint、Widget 生命周期和 SCADA 元数据模式；拒绝任意脚本、外部资源、通用 RPC 和匿名生产 Dashboard；在没有租户自定义面板产品证据前，不建设完整低代码 Dashboard 平台。**

## 2. 固定证据基线

| 证据 | 固定值 |
| --- | --- |
| 官方仓库 | `thingsboard/thingsboard` |
| 版本 | `v4.3.1.1` |
| 提交 | `c2a52e46c44e308ddee430e7266b8e10eddde9c4` |
| 许可证 | Apache-2.0 |
| 本地只读源码 | `C:\Users\HaoZhang\AppData\Local\Temp\thingsboard-v4.3.1.1-src` |
| 全功能目录 | `contracts/architecture/thingsboard-ce-capability-inventory.v1.json` |

上游行为以固定提交的源码、测试、DDL 和配置为准；官方文档用于确认公开产品入口，不覆盖源码行为。

主要上游源码入口：

- [Dashboard](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/common/data/src/main/java/org/thingsboard/server/common/data/Dashboard.java)、[DashboardController](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/application/src/main/java/org/thingsboard/server/controller/DashboardController.java)、[DashboardServiceImpl](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/dao/src/main/java/org/thingsboard/server/dao/dashboard/DashboardServiceImpl.java) 与 [DashboardDataValidator](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/dao/src/main/java/org/thingsboard/server/dao/service/validator/DashboardDataValidator.java)；
- [dashboard.models.ts](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/ui-ngx/src/app/shared/models/dashboard.models.ts)、[alias-controller.ts](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/ui-ngx/src/app/core/api/alias-controller.ts) 与 [widget-subscription.ts](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/ui-ngx/src/app/core/api/widget-subscription.ts)；
- [WidgetType](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/common/data/src/main/java/org/thingsboard/server/common/data/widget/WidgetType.java)、[WidgetTypeController](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/application/src/main/java/org/thingsboard/server/controller/WidgetTypeController.java)、[WidgetsBundleController](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/application/src/main/java/org/thingsboard/server/controller/WidgetsBundleController.java) 与 [WidgetTypeDataValidator](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/dao/src/main/java/org/thingsboard/server/dao/service/validator/WidgetTypeDataValidator.java)；
- [js-function.models.ts](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/ui-ngx/src/app/shared/models/js-function.models.ts)、[widget-component.service.ts](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/ui-ngx/src/app/modules/home/components/widget/widget-component.service.ts) 与 [widget.component.ts](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/ui-ngx/src/app/modules/home/components/widget/widget.component.ts)；
- [MobileApp](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/common/data/src/main/java/org/thingsboard/server/common/data/mobile/app/MobileApp.java)、[MobileAppBundle](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/common/data/src/main/java/org/thingsboard/server/common/data/mobile/bundle/MobileAppBundle.java) 与 [MobileAppController](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/application/src/main/java/org/thingsboard/server/controller/MobileAppController.java)；
- 官方入口：[Dashboards](https://thingsboard.io/docs/user-guide/dashboards/)、[Widgets](https://thingsboard.io/docs/user-guide/widgets/)、[Layouts](https://thingsboard.io/docs/user-guide/layouts/)、[SCADA](https://thingsboard.io/docs/user-guide/scada/)、[Mobile App Center](https://thingsboard.io/docs/user-guide/mobile-app-center/)。

主要本地证据：

- `apps/hvac-web/src/real/RealDashboard.tsx`、`dashboard-projection.ts`、`real-dashboard.css`；
- `apps/hvac-web/src/real/RealProductPages.tsx` 与 `apps/hvac-web/src/styles/real-product-pages.css`；
- `apps/hvac-web/src/real/SiteScopedShell.tsx`、`RealShellChrome.tsx`、`site-routing.ts`；
- `apps/hvac-web/src/api/registry.ts`、`telemetry-current.ts`、`energy-analytics.ts`；
- `scripts/test-real-dashboard-projection.mjs`、`test-rms-site-routing.mjs`；
- `scripts/rms-real-build-audit-lib.mjs`、`check-rms-real-bundle.mjs`；
- `docs/operations/hvac-web-real-integration-decisions.md` 与 `docs/adr/0007-hvac-web-real-mode-data-authority.md`；
- 用户提供的《智慧能源系统前端交互与能源控制 UX 规范 V1.1》和《智慧能源系统前端工程架构与实现设计 V1》。

## 3. 文档约束对齐

两份用户提供文档在本票中是产品/工程要求，不是执行命令。其与 D07 直接相关的冻结约束如下：

| 规范要求 | 对 D07 的约束 |
| --- | --- |
| Dashboard 回答状态、风险、能耗、费用和设备是否正常 | Dashboard 需要业务 Read Model，不能只展示设备枚举和空卡片 |
| KPI 至少包含名称、数值、单位、状态、更新时间、趋势 | 每个 KPI 必须携带自己的质量和数据时间；页面刷新时间不能替代数据时间 |
| `Missing ≠ STALE ≠ INVALID ≠ 0` | EMPTY、PARTIAL、STALE、SUSPECT 不得被折叠为绿色健康 |
| `REST = Snapshot/Query/Recovery`，`WebSocket = Incremental Realtime` | 只有成功完成 Snapshot + Subscription，且恢复后 Reconcile 完成，才可显示 Live/Connected |
| Slow Data 用 Query；Fast Data 用 Snapshot + Realtime | Dashboard Summary 应分慢速 KPI 和快速运行状态，不能全靠浏览器轮询 |
| Business Aggregation 在 Backend | 设备在线率、今日能耗、COP、成本、告警和质量均不得由页面拼底层 Point/设备全集 |
| Site、Device、Time Range 保持 URL Context | Dashboard Drill-down/时间窗应可分享和回溯 |
| 大屏只读、高密度、远距离可读、默认不提供控制 | BigScreen 只能消费权威 Read Model，不得内嵌通用 RPC |
| Desktop First；Laptop/Large Desktop/Wall Display 优先 | 当前不要求完整原生移动端；大屏可以保持 16:9/横屏，不必为手机重排 |
| 如果未来支持移动端，高风险控制默认限制，只提供查看/ACK | Native Mobile 与 WebView 不能继承完整 Command/RPC 能力 |
| 实时只推当前可见/订阅数据，切页及时 Unsubscribe | Widget/页面订阅必须有 Scope、预算、生命周期和清理 |

已有 `docs/operations/hvac-web-real-integration-decisions.md` 的 Q6/Q12 与本裁决一致：Dashboard 只基于已接入数据的真实摘要；BigScreen 只消费 Dashboard/Energy/Alarm 等相同 Read Model，不拥有独立数据集。

## 4. 参考项目功能、解决的问题与客观评价

### 4.1 Dashboard CRUD、分配、公开、Home、导入导出

ThingsBoard 将 Dashboard 拆成轻量 `DashboardInfo` 和带 Configuration JSON 的重对象。它支持创建、更新、删除、分页、客户分配、Public Customer、Home Dashboard、移动端隐藏/排序、Edge 分发及导入导出。完整 Dashboard 响应支持 Gzip，数据库行使用乐观版本。

它解决：

- 一个 Tenant 如何管理多套可视化；
- 不同 Customer/User 如何获得不同入口；
- 大配置如何传输、迁移和复用；
- Dashboard 被 Device/Asset Profile 引用时如何阻止破坏性删除。

优点是产品能力完整。缺点是 Dashboard Configuration 是大块开放 JSON，服务端没有对内部引用进行完整结构校验；“能保存”不代表“能运行”。

### 4.2 State 与 Action

State 是 Dashboard 内的命名视图，每个 State 拥有自己的 Layout。Action 可：

- 打开或更新当前 Dashboard State；
- 跳转其他 Dashboard；
- 打开 URL；
- 执行自定义 JavaScript/HTML Action；
- 调用移动端 Camera/QR/Location/Provision；
- 在 Map 中放置对象。

State 参数会编码进 URL Query，可形成 Drill-down 和返回历史。这一模式解决“从站点总览进入设备详情而不复制多个 Dashboard”。

可吸收的是显式 State、参数、历史和 URL 可恢复；不可吸收的是任意 URL、自定义脚本以及从展示动作直接进入控制。

### 4.3 Entity Alias 与 Datasource

Entity Alias 支持固定实体、实体列表、名称/类型、Dashboard State Entity、关系查询和多种搜索查询。客户端 `AliasController` 缓存解析结果、对 State Entity 变化失效，并通过服务端 `/api/entitiesQuery/find` 执行实体查询。Datasource 在解析后进入 Entity Data/Alarm Subscription。

它解决 Dashboard 在不同实体和 State 上复用的问题，也避免把固定 Device ID 写死在每个 Widget 中。

客观限制：

- Alias 本身是展示配置中的通用查询表达式，不表达 HVAC Site、Point、Metric Version 或 Quality Policy；
- 默认多实体 Page Size 可达 1024，复杂 Dashboard 可形成大量隐式查询；
- Alias 迁移、关系变化或权限变化后可能解析为空，且 Dashboard 配置校验不会提前证明其完整性；
- 服务端查询会执行授权，但本地目标仍需把 Site/Tenant 固定为不可由 Dashboard 配置扩大或覆盖的 Trusted Context。

### 4.4 Time Window 与 Subscription

ThingsBoard 提供 Dashboard/Widget 两级 Time Window、实时滚动窗口、固定历史窗口、Aggregation、Server Time Diff、最大 Datapoint 和 Widget Subscription 生命周期。Widget 在初始化时解析 Datasource，订阅 Entity Data/Alarm；时间窗改变时重订阅；销毁时 Unsubscribe/Destroy。

该模式对 HVAC 有直接价值：

- 统一 Time Window；
- 避免每个卡片创建独立 WebSocket；
- 切 State/页面后释放订阅；
- 用 Datapoint Limit 防止浏览器加载失控。

但本地应通过统一 Realtime Provider、短时 Subscription Capability、Site Scope、可见 Key 和 Recovery/Reconcile 实现，不能复制 ThingsBoard 的 Widget 自主订阅权限。

### 4.5 Widget Type、Bundle 和资源

Widget Type 用 FQN 标识，Descriptor 包含类型、默认配置、HTML、CSS、Controller Script、资源和设置表单；Widget Bundle 维护有序 Widget Type 集合。系统和 Tenant 可分别拥有 Widget Type/Bundle。

它解决：

- 可视化组件的发现、分类和复用；
- Widget 版本/别名和导入导出；
- Built-in 与 Tenant Extension 共存；
- 缺失或编译失败时显示错误 Widget，而不是让整个 Dashboard 崩溃。

但安全边界不可接受：

```text
Widget Descriptor
  -> HTML/CSS Dynamic Component
  -> Controller Script
  -> compileTbFunction
  -> new Function(...)
  -> same-origin Widget Context
  -> subscription / RPC / navigation / platform services
```

Resource 还可按 URL 加载 JavaScript/CSS 或平台扩展模块。它是完整插件系统，不是无害的 JSON 配置。

### 4.6 SCADA Symbol 与 SCADA Layout

ThingsBoard SCADA Layout 使用固定坐标 Canvas，适合流程图和设备拓扑；SCADA Symbol 是带 Metadata/Tag/Behavior 的 SVG。Behavior 可读取值、写 Attribute、写 Time Series、执行 RPC 或触发 Widget Action。

值得吸收：

- SVG 图元与数据绑定分离；
- 图元 Property、Value、Action 的 Metadata 描述；
- 高性能简化图元而非重 3D 渲染；
- Symbol 库、搜索标签、预览和错误占位。

不适合直接吸收：

- 上传 SVG 直接进入运行画布而没有本项目的 Sanitization/Signature Policy；
- Behavior 直接写 Attribute/Telemetry/RPC；
- 固定坐标画布被误用为手机响应式页面；
- 用视觉对象替代 Device/Point/Command 的耐久身份。

### 4.7 移动端

固定版本包含 Mobile App、Mobile App Bundle、Layout Pages、Home Dashboard、Dashboard Tile 顺序/隐藏、OAuth Client、QR 设置、版本/应用商店信息和 Mobile Action。移动 App 主要通过 WebView 打开平台 Dashboard。

它解决：

- Android/iOS App 与平台实例绑定；
- 同一 Dashboard 在移动端复用；
- 移动导航和设备 Profile 默认 Dashboard；
- Camera、QR、Location 和 Provision 等设备能力。

它不等于本项目当前需求：

- 工程规范明确 Desktop First；
- UX 只要求未来移动端默认限制高风险控制，可提供查看/ACK；
- 当前仓库没有 Native App、PWA Service Worker、Capacitor/Expo/React Native 交付链；
- ThingsBoard 的 App Secret 明文持久化不能复制；
- WebView 复用完整 Dashboard 会把自定义脚本和通用控制一起带入移动端。

因此 Native Mobile App Center、OAuth/QR 和 Mobile Action 当前全部 `DEFER`，不是缺陷修复优先项。

## 5. Domain 模型对照

### 5.1 ThingsBoard

```text
Dashboard
  ├─ Tenant / Assigned Customers / Public / Home
  ├─ Configuration JSON
  │   ├─ TimeWindow
  │   ├─ Settings / Dashboard CSS
  │   ├─ EntityAliases / Filters
  │   ├─ Widgets
  │   └─ States
  │       └─ Layouts(default | scada | divider)
  │           └─ Breakpoints + WidgetLayouts
  └─ Version / Resources

Widget
  ├─ WidgetType FQN
  ├─ Datasources / DataKeys / TargetDevice
  ├─ TimeWindow
  ├─ Settings / Style / Actions
  └─ Mobile visibility/order/height

WidgetType
  ├─ Descriptor(JSON)
  ├─ HTML / CSS / Controller Script
  ├─ Resources / Modules
  └─ Bundle Membership
```

优势是通用和可配置。代价是 Domain 语义主要藏在 JSON、Key 和脚本中；配置、查询、显示和控制扩展共享同一个高权限模型。

### 5.2 HVAC Web 当前

```text
Authenticated Real Shell
  └─ Trusted Tenant + Site Context
      ├─ RealDashboard (fixed React page)
      │   ├─ Registry Device pages
      │   ├─ Presence batch
      │   ├─ Energy Series query
      │   └─ browser projections
      └─ RealBigScreenPage (fixed presentation placeholder)

Authoritative Domains
  ├─ Registry: Site / Space / Asset / Device / Point
  ├─ S2: Snapshot / History / Realtime
  ├─ Energy/Metric: business aggregation + quality
  ├─ Alarm / Work Order
  ├─ Command: intent -> approval -> dispatch -> verify
  └─ Rule Engine: event evaluation and domain publication
```

本地没有 DashboardDefinition、DashboardState、EntityAlias、WidgetRegistry、WidgetBundle、Custom Widget、Mobile App/Bundle 或 SCADA Symbol Aggregate。缺少这些对象不自动代表架构错误：当前产品要求是固定运营工作台，不是租户低代码平台。

### 5.3 目标模型

当前阶段目标不新增通用 Dashboard 平台，而是先建立后端业务投影：

```text
SiteDashboardSummary
  ├─ tenantId + siteId
  ├─ asOf + generatedAt
  ├─ dataWatermark + aggregateWatermark
  ├─ completeness + qualitySummary + reasons
  ├─ devicePopulation
  │   ├─ registered
  │   ├─ applicable
  │   ├─ observable
  │   ├─ online/offline/stale/unknown/unavailable
  │   └─ denominatorPolicy
  ├─ slowMetrics
  │   ├─ siteLocalDayEnergy
  │   ├─ cost
  │   ├─ baseline/savings
  │   └─ COP
  ├─ fastMetrics
  │   ├─ currentPower
  │   ├─ criticalAssetState
  │   ├─ openAlarmSummary
  │   └─ realtimeRevision
  └─ links/actions (read-only navigation intents)
```

如果未来出现“Tenant Admin 自定义面板”的明确产品需求，再增加独立的 Presentation 模型：

```text
DashboardViewDefinition
  ├─ schemaVersion + revision + status(DRAFT/RELEASED/RETIRED)
  ├─ allowed Site scope / audience / capabilities
  ├─ states + typed URL parameters
  ├─ breakpoint layouts
  ├─ first-party WidgetManifest references
  ├─ typed ReadModelDatasource references
  └─ navigation intents
```

该对象只控制展示，不拥有 Metric、Alarm、Device、Command 或 Rule 的事实。

## 6. 核心流程映射

### 6.1 ThingsBoard Dashboard 运行流

```text
Load Dashboard JSON
  -> initialize State Controller
  -> resolve Entity Aliases
  -> create Layout for breakpoint
  -> resolve Widget Types/resources
  -> dynamically compile HTML/controller/functions
  -> resolve Datasources
  -> subscribe Entity Data/Alarm/RPC
  -> render
  -> Action changes State/URL or executes custom behavior
  -> destroy/unsubscribe on removal/navigation
```

该流程的生命周期完整，但把动态代码执行放在核心路径。

### 6.2 本地 Real Dashboard 现状

```text
Validated Site Route
  -> Registry device page 1..2
  -> truncate to 100 devices
  -> Presence batch for visible sample
  -> browser computes counts/availability/attention
  -> Energy query for rolling previous 24h
  -> browser sums returned hourly points
  -> fixed React page
```

安全隔离是正确的，业务聚合边界不是。

### 6.3 目标 Dashboard 运行流

```text
Validated Site Route + Capability
  -> GET SiteDashboardSummary Snapshot
  -> render slow + fast fields with per-field quality/watermark
  -> subscribe only approved fast projection channels
  -> incremental update by Business Revision
  -> on reconnect: REST Reconcile
  -> resume Live only after reconcile
  -> BigScreen consumes the same summary/subscription
  -> page leave/site switch/policy change: unsubscribe + purge
```

Dashboard/BigScreen 不自行列举设备全集，不解析底层 Point，也不发送 Command。

### 6.4 目标 Drill-down

ThingsBoard State Pattern 映射为普通、可审计的 Router State：

```text
/sites/{siteId}/dashboard?view=operations&time=site-day
  -> /sites/{siteId}/assets/{equipmentId}?tab=telemetry&range=PT24H
```

参数必须经过 Schema 校验；Tenant/Site 从 Trusted Route/Session 获得，不能从 Widget/Dashboard JSON 覆盖。

## 7. 异常与边界处理

### 7.1 ThingsBoard 中值得保留的异常模式

- Dashboard 轻量信息与重配置分开，重响应可 Gzip；
- Entity Query 分页；
- Widget 缺失、资源加载失败、脚本或 HTML 编译失败时使用 Missing/Error Widget；
- Widget 销毁时停止 Entity/Alarm Subscription；
- Dashboard 删除遇到 Device/Asset Profile 引用时返回明确约束错误；
- JPA `@Version` 提供并发更新保护；
- Mobile App 发布前校验 Store Metadata；
- System/Tenant Widget Type 和 Bundle 作用域分开。

### 7.2 ThingsBoard 中不可依赖的异常模式

- Dashboard Validator 不检查 Configuration 内部结构和引用；
- Widget Validator 不检查脚本能力、URL、HTML、CSS、外部资源或 API 权限；
- 自定义函数异常主要写 Console，无法形成平台级、租户级可审计失败事实；
- Public Dashboard 需要单独公开底层实体，容易产生配置与数据授权错配；
- Alias 解析为空通常在运行时才暴露；
- 外部 Resource 失败可显示错误，但供应链、来源和完整性不能靠错误占位解决；
- 移动 App Secret 明文存储。

### 7.3 本地必须新增的边界

- `NO_DATA`、`PARTIAL`、`STALE`、`SUSPECT`、`UNAVAILABLE` 与 `HEALTHY` 互斥，不得通过颜色或默认分支折叠；
- Site Dashboard Summary 必须显式返回 Denominator Policy；不可用/不适用设备不能被页面擅自纳入或排除在线率；
- Summary 必须明确是完整站点、授权子集还是样本；只有完整且质量合格时才显示站点比例；
- `generatedAt`、`asOf`、`dataWatermark`、`aggregateWatermark` 和 `lastRealtimeMessageAt` 分开；
- Site 本地日由后端按 IANA Timezone 和 Calendar Boundary 计算；
- Dashboard 失败不得回退 Demo/Mock/ThingsBoard；
- BigScreen 单个 Panel 失败可局部降级，但全局不能继续显示“系统正常”；
- 用户没有 `alarm.list`/`device.list` 时，应显示“无权限/不可统计”，不能显示稳定或 0；
- 任何展示动作进入控制前必须跳转到 Command Preview，由 Command Domain 重新授权和生成 Intent。

## 8. 本地源码级反向审查

### 8.1 Real Build 与 Site Scope

`SiteScopedShell` 在 READY 前验证 Site 路由和 `site.read`；无权与不可见资源不泄露 Site Context。BigScreen 虽绕过常规 Shell Chrome 以便全屏展示，仍位于已认证、已验证的 Site Shell 内。`ProtectedSiteRouteFrame` 注册按 Route/Site 清理的资源。

`rms-real-build-audit-lib.mjs` 明确禁止 Real Graph 到达 Demo Entry、Mock API、Demo Store、Demo Dashboard、Demo BigScreen 和本地角色模拟；Bundle Audit 还检查 Mock 业务 Marker。该隔离有测试和构建证据，应保留。

### 8.2 Device Summary

`RealDashboard` 最多取两页 Registry，再截断到 100 台 Device。`inventoryPartial` 能识别一部分截断情况，方向正确；但后续仍计算：

```text
availability = online / total
deviceTypeGroups = first 100 devices grouped in browser
```

并以“系统可用率”“冷源设备运行矩阵”展示。Partial 标签不能把样本比例变成站点事实。

此外：

- `NOT_APPLICABLE`、`UNAVAILABLE` 和 `UNKNOWN` 是否进入在线率分母由页面隐式决定；
- `device.list` 缺失时 `deviceProjection` 为空，Focus 仍可能显示“设备在线状态稳定”；
- Inventory/Presence Partial 但样本无 Attention 时也显示稳定；
- `deviceTypeGroups` 只显示前四种类型且不说明截断。

裁决：Device Projection 的状态分类 `KEEP`；站点聚合和文案 `REPLACE`。

### 8.3 Energy Summary

Energy Query 使用 `qualityPolicy=VALID_ONLY`、Site Timezone 和小时粒度，这是正确方向。`projectDashboardEnergy` 能区分 `EMPTY/PARTIAL/STALE/SUSPECT`，测试也覆盖有效 0 和这些状态，应保留。

但当前：

- `from = asOf - 24h`，却标“今日能耗”；
- 页面直接求和 Hour Points；
- `aggregateWatermark` 只参与 Projection State，不显示为 KPI Last Updated；
- `STALE/SUSPECT` 不进入全页 Business State；
- Energy `EMPTY` 与 Device `EMPTY` 的组合没有独立总状态模型。

裁决：Energy 状态投影 `KEEP`；时间口径、聚合 Owner、时间展示和总状态合成 `REPLACE`。

### 8.4 Realtime

Real Shell 已有统一 Realtime State；Assets 也有真实的 Device Realtime Hook 和状态组件。但 `RealDashboard` 没有消费 Shell Realtime State，也没有订阅 Dashboard Fast Projection。它仅在手动刷新时更新 `asOf`，却显示“实时连接”。

裁决：使用 Shell/Provider 的统一连接状态 `ADOPT`；Dashboard 自行声明实时 `REJECT`。在 Fast Projection 未接入前，页面只能显示“快照/查询时间”，不能显示 Live。

### 8.5 KPI 与质量 UX

`DashboardMetric` 只有 `good: boolean`，最终映射为 `is-good/is-bad`。这不足以表达规范中的 GOOD、PARTIAL、ESTIMATED、MANUAL、STALE、INVALID，也没有 Tooltip、来源和每卡更新时间。

裁决：当前简单布尔样式 `REPLACE` 为共享的 `MetricState + QualityBadge + Watermark + Source` 展示模型；未接入字段继续用 `—`，不能用红色暗示业务异常。

### 8.6 Business State

当前全页状态主要由 Registry/Presence/Query Error、Inventory Partial、Device Attention 和 Device Empty 推导。它没有组合 Energy `STALE/SUSPECT/EMPTY`，并把 `EMPTY` 显示为绿色“整体健康”。

裁决：`REPLACE` 为后端 Summary State 或明确的前端组合函数，并建立 Exhaustive Tests。`EMPTY` 表示没有可展示数据，不是健康结论。

### 8.7 BigScreen

`RealBigScreenPage` 的优点：

- 只读；
- Site-scoped；
- 强制 Dark Theme；
- 所有未接入值均为 `—`；
- 明确写出“不代表 0”和“不加载 Demo”。

缺口：

- 没有任何权威查询或实时订阅；
- `ProductBoundary state="READY"` 只是固定值；
- 场景切换只改变本地视觉属性，三个 Scene 没有不同数据语义；
- 标签写 Registry/S2/Energy，但没有调用这些 Read Model；
- CSS 最小宽度 1180/1060，窄屏横向滚动。对 16:9 Wall Display 合理，但不能算 Mobile 支持；
- 缺少 Panel 级质量、Watermark、Realtime 和降级状态。

裁决：当前是安全 Placeholder，不是完成品。后续只允许消费 Dashboard Summary/Alarm/Energy 等相同 Read Model。

### 8.8 响应式与移动端

Real Dashboard 使用 Ant `xs=24/xl=*`、共享 CSS 的 1199/767 Breakpoint，并为 Energy Bar 和 Actions 设置窄屏重排。它满足 Desktop First 下的基础 Web 响应式，不等于完整移动产品。

仓库没有 Native/PWA 交付链。根据规范，复杂控制和配置不要求手机优先。当前正确优先级是：

1. 保证常用只读 Dashboard/Alarm ACK 在小屏可读；
2. 保持控制、配置和大屏为 Desktop/Wall Display；
3. Native Mobile 有明确用户、设备能力、离线、推送和商店交付需求后再立项。

## 9. 全能力裁决矩阵

| ThingsBoard 能力 | 裁决 | 映射/理由 |
| --- | --- | --- |
| Dashboard CRUD | `DEFER` 通用平台；`KEEP` 固定产品路由 | 当前没有 Tenant 自定义 Dashboard 需求；先修真实 Dashboard |
| Dashboard Info/Heavy Config 分离 | `ADOPT` | 若未来有定义模型，列表不加载完整配置 |
| Dashboard 乐观版本 | `ADOPT` | Presentation Definition 必须 Revision/ETag 冲突检测 |
| Dashboard Import/Export | `DEFER` | 只有配置平台立项后才需要；必须 Schema Version、引用检查和 Dry-run |
| Customer Assignment | `ADAPT` | 映射到 Audience/Capability/Site，不引入 ThingsBoard Customer 身份模型 |
| Public Dashboard | `REJECT` 生产运营默认 | 不匿名公开实时运营/控制数据；未来公共状态页应是独立、最小化、过期的发布物 |
| Home Dashboard | `ADAPT` | 可按用户/角色设置默认 Landing Route，不复制大块 Dashboard JSON |
| Dashboard State | `ADAPT` | 使用 Typed Router State + URL Context，不使用不透明脚本状态 |
| Layout/Breakpoint | `ADOPT` 模式 | 继续 CSS/Ant 响应式；有配置需求时使用版本化布局 Schema |
| SCADA Layout | `DEFER` 编辑器；`ADAPT` 只读拓扑 | 固定画布适合冷站流程图，不承担控制 |
| Entity Alias | `ADAPT` | 改为后端解析的 Typed Resource Selector；Tenant/Site 不可覆盖 |
| Dashboard Filter | `ADAPT` | Typed Filter + URL State；禁止任意实体发现查询 |
| Dashboard Time Window | `ADOPT` | Site Timezone、Calendar/Duration 语义、Datapoint Budget、URL 可恢复 |
| Widget Datasource | `ADAPT` | 只引用业务 Read Model/Metric，不直接拼 30 个 Point |
| Telemetry Widget | `ADAPT` | Snapshot + Realtime + Reconcile + Quality/Watermark |
| Alarm Widget | `ADAPT` | 读取 Alarm Domain；ACK 走显式 Mutation/Capability/Audit |
| RPC/Control Widget | `REJECT` | 所有控制进入 Command Domain，不从 Widget 直发 RPC |
| Static/Markdown Widget | `ADAPT` | 只允许 Sanitized Markdown/First-party Content，不允许任意 HTML Script |
| Map Widget | `DEFER` | 当前 HVAC 冷站没有地理调度证据；若有园区地图需求另立项 |
| Widget Action: State/Route | `ADOPT` 模式 | 仅 Allowlisted Internal Navigation Intent |
| Widget Action: URL | `REJECT` 默认 | 外链需域名 Allowlist、提示和审计；核心运营面不接受任意 URL |
| Widget Action: Custom JS/HTML | `REJECT` | 同源任意代码执行 |
| Widget Type FQN/Manifest | `ADAPT` | 若需要配置，只允许 First-party、Build-time Registry、签名版本 |
| Widget Bundle | `DEFER` | 组件分类可由代码模块完成；低代码平台立项后再持久化 |
| External JS/CSS Resource | `REJECT` | 供应链、CSP、完整性和同源权限风险 |
| Missing/Error Widget | `ADOPT` | 单卡故障隔离并显示可行动错误 |
| Subscription Lifecycle | `ADOPT` | Init/Update/Unsubscribe/Destroy + Scope Purge + Budget |
| Max Datapoints | `ADOPT` | 后端聚合和前端点数预算均需显式 |
| Image/Resource Library | `ADAPT` | 只允许受扫描、Content-addressed、带用途和 Tenant Scope 的资源 |
| SCADA Symbol Library | `ADAPT` | Sanitized SVG + Metadata + Read-only Behavior；控制只生成 Navigation Intent |
| SCADA RPC/Attribute/TS Write | `REJECT` | 绕过 Command/Metric/Telemetry Authority |
| Dashboard CSS | `REJECT` Tenant 任意 CSS | First-party Theme Token/受限样式 Schema |
| Mobile Dashboard hide/order | `ADAPT` | Web Navigation 可配置时使用；当前固定路由继续代码拥有 |
| Mobile App/Bundle | `DEFER` | 无 Native 产品和交付证据 |
| Mobile OAuth/QR | `DEFER`，禁止复制 Secret 存储 | 应复用生产 OIDC/PKCE/Universal Link，并重新做 Threat Model |
| Camera/QR/Location/Provision Action | `DEFER` | 需明确 Native Use Case 和单独 Capability |
| Mobile 高风险控制 | `REJECT` 默认 | 规范只允许查看/ACK；控制另需风险评估与 Step-up |
| White Label/Reporting 扩展 | `DEFER/OUT_OF_SCOPE` | 固定 CE 源码不足，不能做源码级裁决 |

## 10. 值得吸收的 Pattern

### 10.1 State、Datasource、Layout 正交

不要让页面组件同时拥有“选择了谁、查什么、怎么排版、点击做什么”。即使暂不做配置平台，也应在代码中分开：

```text
Route Context
  -> Read Model Query
  -> View State
  -> Presentation Component
  -> Navigation Intent
```

### 10.2 Read Model Datasource，而不是 Point Alias

ThingsBoard 的 Alias 解决复用问题，但 HVAC 应把复用层提升到业务对象：

```text
site-dashboard-summary:v1
equipment-health-summary:v1
energy-site-day:v1
alarm-open-summary:v1
```

而不是把 Point Key 数组放入 Widget 配置。

### 10.3 明确 Widget 生命周期

每个数据组件应有：

- Query/Subscription 输入；
- Init/Loading/Ready/Partial/Stale/Error/Empty；
- Scope Change；
- Time Window Change；
- Reconcile；
- Unsubscribe/Destroy；
- Datapoint/Update Rate Budget。

### 10.4 Missing/Error Component

单个面板失败时不拖垮全页，但必须显示：影响范围、质量状态、数据时间、可重试性和 Request ID。不能只在 Console 记录。

### 10.5 Breakpoint 作为显式设计资产

当前 CSS Media Query 可以继续，但关键页面应建立 Laptop/Large Desktop/Wall Display/Small Read-only 的视觉回归矩阵。BigScreen 的固定最小宽度应被定义为 Wall Display Contract，而不是被误报为通用响应式。

### 10.6 SVG Metadata，而不是硬编码 3D

若建设冷站拓扑，优先使用经过 Sanitization 的轻量 SVG Symbol + 明确状态参数；不要先建设重 3D 场景。设备点击只进入详情或 Command Preview，图元自身不直接控制。

## 11. 不适合本项目的部分

### 11.1 浏览器任意脚本平台

本项目的 Runtime 页面可读取 Site 设备、遥测、告警并接近控制入口。把租户脚本放入同源 Context 会使 CSP、Code Review、审计、权限和供应链边界失效。即使只有 Tenant Admin 能编辑，也不等于脚本安全。

### 11.2 Dashboard 作为业务权威

Dashboard Definition 只能引用数据。Energy、COP、Cost、Savings、Alarm、Availability、Rule Result 和 Command Status 必须由各 Domain 产生。任何“为了让卡片显示”而在 Browser 或 Dashboard JSON 中发明算法都应拒绝。

### 11.3 展示面直接控制

ThingsBoard 的 RPC Widget 和 SCADA Behavior 对通用 IoT 很方便，但 HVAC 控制要求 Preview、风险等级、Step-up、Approval、Idempotency、Dispatch、ACK、Verification、Unknown 和 Audit。通用 Widget 不拥有这些状态机。

### 11.4 无需求的平台化

当前固定产品页面能更直接地满足运营任务。立刻引入 Dashboard CRUD、Widget Editor、Bundle、Import/Export、Version、Resource、SCADA Editor 和 Mobile Center 会显著扩大测试和安全面，却没有用户自定义 Dashboard 的明确需求。

## 12. 映射到目标设计

### 12.1 P0：先纠正事实性错误

1. 将“实时连接 · asOf 更新”替换为真实 Shell Realtime State；未接入 Fast Subscription 时显示 Snapshot/Query。
2. 将“今日能耗”改为 Site Local Calendar Day，或在后端能力未完成前改名“过去 24 小时”。优先目标是前者。
3. Partial Inventory/Presence 时不显示站点在线率；后端返回完整 Population/Denominator 后再恢复。
4. `EMPTY`、无权限、Partial、Stale、Suspect 不得显示“整体健康”或“状态稳定”。
5. KPI 显示各自 Watermark、Quality、Completeness 和 Source；页面刷新时间单独显示。
6. 为以上语义补页面级和 Projection 组合测试。

### 12.2 P1：建立 Dashboard Summary Read Model

1. 后端拥有 Site Dashboard Summary 及版本化契约。
2. Slow KPI 使用 Query；Fast KPI 使用 Snapshot + Realtime + Reconcile。
3. Dashboard 与 BigScreen 共用 Query Key、Summary Contract、Quality Model 和 Subscription。
4. Alarm、Work Order、FDD、Optimization 未接入时保持独立 `NOT_INTEGRATED`，不互相冒充。
5. 添加 Resource Budget：最大字段、最大时间范围、最大 Datapoints、刷新频率和实时更新批量间隔。

### 12.3 P1：吸收 State/Time Window/Navigation

1. 使用 Typed URL Context 表达 View、Device、Time Range 和 Tab。
2. Drill-down 通过 Router 和 Capability 进入既有产品页面。
3. 只允许内部 Navigation Intent；外链需显式 Allowlist。
4. Site/Tenant 永远来自可信 Route/Session，不允许 Presentation 配置覆盖。

### 12.4 P2：有产品证据后才建设可配置展示

启动条件至少包括：

- 已确认 Tenant Admin 需要自行编排 Dashboard；
- 至少三种不能用固定产品页面满足的独立布局；
- 定义了 Draft/Release/Rollback/Ownership/Audit；
- 定义了 First-party Widget Manifest 和 Schema；
- 完成 CSP、Resource、Import、SVG 和权限 Threat Model；
- 有性能预算和视觉回归环境。

满足后可实现声明式 `DashboardViewDefinition`，仍不得加入任意 JavaScript/HTML/CSS、外部资源或通用 RPC。

### 12.5 P2/DEFER：SCADA 与移动端

- 冷站拓扑需求成立时，先做只读 SVG Runtime 和固定图元库，再评估 Editor；
- Native Mobile 需求成立时，先定义用户旅程、Offline/Push、认证、设备能力和只读/ACK Capability；
- 不从 ThingsBoard 复制 App Secret、WebView 全权限 Dashboard 或 Mobile RPC；
- BigScreen 继续按 Wall Display 交付，不把手机支持作为其验收条件。

## 13. 与 Rule Engine、Command 和数据架构的边界

完整 Rule Engine 后续可以产生：

- Alarm Publication；
- Rule Evaluation/Debug Event；
- FDD/Optimization Evidence；
- Dashboard Read Model 所需的摘要事件。

但 Dashboard/Widget 不执行 Rule Engine 脚本，不保存 Rule Definition，不直接写 Telemetry，也不下发控制。

```text
Rule Engine -> Domain Publication -> Read Model -> Dashboard
Dashboard Action -> Command Preview -> Command Domain
```

这与 `docs/architecture/thingsboard-pattern-adoption.md` 的现有边界一致：展示配置不是业务数据权威，Command 能力保持 Allowlist、Snapshot、Fence 和独立 Verification。

## 14. 实施前的验收门槛

### Dashboard Truthfulness Gate

- 不存在无真实 Subscription 的 “Live/实时连接” 标签；
- Site 本地日有 DST/Timezone 测试；
- `EMPTY/PARTIAL/STALE/SUSPECT/UNAVAILABLE` 不显示 Healthy；
- 不完整 Population 不显示站点比例；
- 每个非空 KPI 有 Unit、Quality、Watermark、Source；
- Missing 不显示 0；
- 无权限不显示稳定结论。

### Shared Read Model Gate

- Dashboard 与 BigScreen 使用同一 Contract；
- BigScreen 没有独立静态业务数值；
- Realtime 断线后先 REST Reconcile 再恢复 Live；
- Site/Policy 切换清理 Query、Subscription 和临时状态；
- Browser 不下载全量设备/Point 后聚合站点 KPI。

### Configurable Presentation Gate

- 只允许 First-party Widget；
- 无 `new Function`、`eval`、任意 HTML/CSS/JS 和外部 Resource；
- Schema Version、Revision、Draft/Release、Dry-run 和 Rollback 完整；
- Alias/Selector 后端解析且强制 Tenant/Site Scope；
- Import 对引用、权限、资源和预算做完整预检；
- 所有动作都是 Allowlisted Navigation Intent。

### SCADA/Mobile Gate

- SVG Sanitization、签名、大小和复杂度限制；
- SCADA 图元不能直接 RPC/写 Attribute/写 Telemetry；
- Mobile 默认只读/ACK；
- Native 认证不保存长期明文 App Secret；
- 高风险控制必须重新经过 Command/Step-up，不继承 Dashboard 权限。

## 15. 验证证据与当前基线缺口

本裁决只修改文档，不修改 Dashboard、BigScreen 或 Site Routing 运行时代码。验证结果如下：

| 检查 | 结果 | 说明 |
| --- | --- | --- |
| `npm run lint` | `PASS` | 当前前端静态检查通过 |
| `npm run rms:real:graph` | `PASS` | Real 入口共 88 个可达本地模块，未发现 Demo/Mock 越界 |
| Dashboard Projection 测试 | `PASS` | ONLINE/OFFLINE/STALE/UNKNOWN/UNAVAILABLE/NOT_APPLICABLE 与 Energy READY/EMPTY/PARTIAL/STALE/SUSPECT 现有测试通过 |
| `npm run real-dashboard:test` | `FAIL` | Projection 通过，但 Site Routing 11 项中 2 项失败 |

两项失败均是当前仓库已有的测试—契约漂移：

1. 测试仍断言 `decision.context.actingOrganizationId`，当前 `SiteContext` 只暴露 `context.site`，组织标识位于 `context.site.owningOrganizationId`；
2. 测试仍断言 Assets 路由返回 `equipmentId`，当前 `SiteRoutingDecision` 返回字段为 `assetId`。

`site-routing.ts` 与 `test-rms-site-routing.mjs` 在本次裁决中都没有改动。D07 不把测试失败错误归因于 ThingsBoard 对比，也不为使审查“全绿”而越界修改运行时；后续实现工单必须先决定当前字段命名契约并同步测试。页面级真值测试仍是缺口：当前没有测试证明“实时连接”、Site 本地日、完整 Population 分母、EMPTY/STALE/SUSPECT 健康状态和 BigScreen 共享 Read Model 的正确性。

## 16. 最终裁决摘要

| 区域 | 结论 |
| --- | --- |
| 本地 Real Shell/Site/Capability/Build 隔离 | `KEEP` |
| 本地失败关闭、空值不造数、质量状态分类 | `KEEP` |
| 本地伪实时、滚动 24h“今日”、样本站点比例、EMPTY 健康 | `REPLACE` |
| 本地 BigScreen | 安全 Placeholder；数据实现 `REPLACE` 为共享 Read Model |
| ThingsBoard State/Time Window/Breakpoint/Lifecycle | `ADOPT/ADAPT` |
| ThingsBoard Entity Alias/Datasource | `ADAPT` 为 Site-scoped Typed Read Model Selector |
| ThingsBoard Widget Bundle/Editor | `DEFER`，无当前产品证据 |
| ThingsBoard Custom JS/HTML/CSS/Resource | `REJECT` |
| ThingsBoard RPC/SCADA Write | `REJECT` |
| ThingsBoard SCADA Symbol Metadata | `ADAPT` 为 Sanitized Read-only SVG |
| ThingsBoard Public Dashboard | `REJECT` 作为生产运营默认能力 |
| ThingsBoard Mobile App Center/OAuth/QR/Actions | `DEFER`；安全机制重做 |
| White Label/Reporting | 固定 CE 证据不足，`OUT_OF_SCOPE/DEFER` |

D07 已形成目标行为和实施门槛，但没有修改运行时代码。后续实现必须先完成 P0 Truthfulness，再建设共享 Dashboard Summary；不能以“先做低代码平台”代替当前 Real Dashboard 的正确性修复。
