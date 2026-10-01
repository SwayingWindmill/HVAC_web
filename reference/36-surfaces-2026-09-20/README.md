# 36 Surface Frontend Historical Source Snapshot — 2026-09-20

This directory is an **immutable historical source snapshot** copied from the original Windows worktree:

- source root: `E:\Code\HVAC_web`
- WSL source path: `/mnt/e/Code/HVAC_web`
- source Git HEAD: `38830a57858c914f3885c49631a39171b7f4ad95`
- source branch: `feat/virtual-central-plant-phase1-20260828`
- copied on: 2026-09-23
- purpose: preserve the actual 36-Surface implementation state that existed in the Windows worktree before the 36 → 10 Workspace consolidation.

## Authority warning

This snapshot is **implementation provenance, not final visual authority**.

The 36-Surface frontend was developed in an uncommitted working tree and continued to change after the Windows → WSL migration. Some files in this 2026-09-20 snapshot therefore preserve intermediate visual compositions that were subsequently rejected during the 2026-09-20/21 global Table, Card, copy and detail-page cleanup. Surface 06/07 are a concrete example: their historical source is useful for domain structure and implementation lineage, but contains Card-heavy layouts that must not be restored as the final design.

**This directory is not production source and must not be imported by the running application.**  
Production code stays under `apps/hvac-web/src`.

When consolidating 36 Surfaces into 10 Workspaces, use evidence in this order:

1. the corresponding 36-Surface specification and its accepted information architecture;
2. later project-wide design decisions in `DESIGN.md` and the shadcn/tablecn Block contracts;
3. this historical implementation snapshot for domain behavior, route lineage and implementation details;
4. browser-reviewed current production implementation.

Do not copy a historical component wholesale when it conflicts with a later accepted design decision. In particular, do not restore Card-wrapped page tables, Card walls, duplicate filtering controls, obsolete split inspectors, old Ant UI, or retired compatibility routes.

## Surface → actual implementation mapping

| ID | Surface | Primary implementation source |
|---|---|---|
| 01 | 企业总览 | `features/portfolio/PortfolioOverviewWorkspace.tsx` |
| 02 | 站点对标 | `features/benchmarking/SiteBenchmarkingWorkspace.tsx` |
| 03 | 站点总览 | `features/overview/Overview.tsx` |
| 04 | 系统运行 | `features/system-operations/OperationsConsole.tsx`, `SystemOperations.tsx` |
| 05 | 趋势分析 | `features/trends/TrendAnalysisDashboard.tsx`, `TrendAnalysis.tsx` |
| 06 | 设备中心 | `features/assets-workspace/AssetsConsole.tsx`, `AssetsWorkspace.tsx` |
| 07 | 设备详情 | `features/assets-workspace/DeviceDetailConsole.tsx`, `AssetDeviceDetail.tsx` |
| 08 | 舒适与室内环境 | `features/comfort/ComfortDashboard.tsx`, `ComfortWorkspace.tsx` |
| 09 | 告警中心 | `features/alarms/AlarmCommandDesk.tsx`, `AlarmCenterWorkbench.tsx` |
| 10 | 诊断中心 | `features/diagnostics/DiagnosticsFddConsole.tsx`, `DiagnosticsWorkspace.tsx` |
| 11 | 工单中心 | `features/work-orders/WorkOrders.tsx` |
| 12 | 工单详情 | `features/work-orders/WorkOrderDetailWorkspace.tsx` |
| 13 | 功能验证 / 持续调试 | `features/verifications/VerificationsWorkspace.tsx` |
| 14 | 能源分析 | `features/energy/EnergyAnalyticsDashboard.tsx`, `EnergyAnalytics.tsx` |
| 15 | 需求、负荷与柔性分析 | `features/demand/DemandAnalyticsDashboard.tsx` |
| 16 | 效率分析 | `features/efficiency/EfficiencyAnalyticsDashboard.tsx` |
| 17 | 能源评审 | `features/energy-review/EnergyReviewWorkspace.tsx` |
| 18 | 账单、成本与电价 | `features/billing/BillingCostDashboard.tsx` |
| 19 | 碳排放 | `features/carbon/CarbonEmissionsDashboard.tsx` |
| 20 | 分布式能源与柔性 | `features/der/DistributedEnergyWorkspace.tsx` |
| 21 | 节能机会 | `features/opportunities/OpportunitiesWorkspace.tsx` |
| 22 | 优化方案 | `features/optimization/OptimizationPlansConsole.tsx` |
| 23 | 目标与行动计划 | `features/action-plans/ActionPlansWorkspace.tsx` |
| 24 | 节能量验证（M&V） | `features/mv/MeasurementVerificationWorkspace.tsx` |
| 25 | 控制中心 | `features/control/ControlOptimizationConsole.tsx`, `ControlCenter.tsx` |
| 26 | 策略中心 | `features/strategies/StrategiesConsole.tsx` |
| 27 | 策略详情 / 仿真 / 审批 | `features/strategies/StrategyDetailWorkspace.tsx` |
| 28 | 执行记录 | `features/executions/ExecutionsLedger.tsx` |
| 29 | 报告中心 | `features/reports/ReportsHub.tsx` |
| 30 | 管理评审 | `features/management-reviews/ManagementReviewWorkspace.tsx` |
| 31 | 数据质量 | `features/data-quality/DataQualityWorkspace.tsx` |
| 32 | 计量与语义模型 | `features/model/MeteringSemanticModelWorkspace.tsx` |
| 33 | 规则与通知 | `features/rules/RulesWorkspace.tsx`, notification support in `features/notifications/` |
| 34 | 集成管理 | `features/integrations/IntegrationsWorkspace.tsx` |
| 35 | 站点与系统配置 | `features/system-settings/SiteSystemSettingsWorkspace.tsx` |
| 36 | 用户、权限与审计 | `features/access-control/AccessControlAuditWorkspace.tsx` |

The canonical 36-Surface routing contract is preserved at:

- `apps/hvac-web/src/app/surface-catalog.ts`
- `apps/hvac-web/src/routes/`

The corresponding design specifications are preserved at:

- `docs/product/surface-specifications/01-*.md` through `36-*.md`
- `docs/product/surface-catalog-final-review-2026-09-15.md`
- `docs/product/visual-reframes/`

## Consolidation source mapping

The 10 Workspace design must use these source Surfaces:

- 总览 ← 01, 02, 03
- 运行 ← 04, 05, 08, 25
- 设备 ← 06, 07
- 告警与诊断 ← 09, 10
- 工单与验证 ← 11, 12, 13
- 能源与绩效 ← 14–20
- 改进 ← 21–24, 30
- 自动化 ← 25–28
- 报告 ← 29
- 设置 ← 31–36

This mapping is a **design inheritance contract**, not merely a list of features.
