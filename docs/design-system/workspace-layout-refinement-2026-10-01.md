# Workspace 布局与图标审核 · 2026-10-01

用户明确要求适当使用 Lucide，并将 arham 的不同页面布局作为本轮参考。继续按 PRODUCT → v3 页面架构 → 全局导航交互契约 → 当前 global-dashboard-workspaces spec → DESIGN 加载上下文；不继承隔离的旧页面视觉。

## 固定来源与实际阅读

arhamkhnz/next-shadcn-admin-dashboard 无官方 release/tag，沿用不可变官方提交 `7eedd776b7c089d01ba765b722c00b4efdbb3f5a`。本地 HEAD 已核对一致。MIT 许可沿用 2026-09-30 source review；没有复制上游源码。

读取 `src/app/(main)/dashboard/` 下 infrastructure、productivity、tasks、invoice、file-manager 的 `page.tsx`；固定提交的 `infrastructure/_components/project-environments.tsx`（1–180）、`productivity/_components/tasks-section.tsx`（1–140）、`invoice/_components/invoice.tsx`（1–110）；本地同提交 `file-manager/_components/file-list-view.tsx`、`productivity/_components/weekly-summary-card.tsx`。阅读官方仓库 README 所列页面和用途。未找到 `src` 下相关 `.test.*` / `.spec.*`，不将示例模板交互当作业务保证。

能源领域继续沿用 `remaining-workspaces-redesign-2026-09-30.md` 中 ThingsBoard v4.3.1.1 的告警状态/服务测试和官方文档、OpenEMS 2026.7.0 的控制通道/组件测试和架构文档、MyEMS v6.7.0 的报告边界审核。本轮补读 MyEMS 固定提交 `be6e6ce8ddeac57afb04bddb9621501fb555cab0` 的 `myems-api/reports` 目录与 `spacecost.py`（1–125）：空间归属、期间、基准期/报告期与导出职责。没有改写这些上游后端机制，未运行上游完整测试。最初请求的 `spaceenergy.py` 不存在，已通过目录确认正确文件；不存在的文件不计作证据。

## 取舍

| 来源行为 | 本地决定 |
| --- | --- |
| infrastructure 按环境分组展示运行事实 | ADAPT：运行快照按冷机、水泵、冷却塔分组，显示实际数量与设备类型图标；保留各类专用工况字段，待机不显示运行 COP |
| file-manager 用文件类型图标、对象元数据和视图组织资料 | ADAPT：报告资料库直接展示报告、期间、负责人、生成时间与目录。删除重复的两张功能介绍卡片和同一对象的账本重述；过滤、详情、来源跳转及已有 CSV 导出保留 |
| invoice 表单与预览分工 | ADAPT：报告目录与详情内容分工；REJECT：照搬并无真实发布 owner 的 Save/Send 行为或虚构正式文档 |
| productivity 的任务完成度与紧凑任务信息 | ADAPT：工作中心进度来自已有完成/总检查项事实；REJECT：前端 checkbox 假完成、无来源的周目标、鼓励语句 |
| 图标辅助动作/对象识别 | ADOPT：单一 Lucide 词汇，搜索使用既有 shadcn InputGroup，列表/看板、能源/成本、执行/策略/约束具有不同图标，动作保留文字与可访问名称 |
| 示例环境星标/大宽度基础设施表格 | REJECT：不引入内存业务写入或固定 1700px 表格导致的桌面拥挤 |
| MyEMS 费用报告按空间和明确期间核算 | ADAPT：报告卡片明示范围上下文、期间、分析来源；数据下载仍为明确的 review 示例，不冒充已发布结果 |

没有添加依赖、第二套 primitives、状态容器或 CI gate。概览不增加重复 KPI。图标使用 `aria-hidden`，业务动作保留文本；设备类型与告警状态不混用图标语义。最终接受依据是桌面渲染和实际交互，不是图标数量。

## 验证

14 个入口的桌面浏览器回归通过：12 页工作区回归加节能机会/能耗页专项回归。检查视图切换、证据详情、URL 搜索、范围隔离和 CSV 下载；报表资料库额外验证搜索、类型筛选、无结果状态。1440×900 首轮截图发现标签页图标换行和报告 Header 使用 grid 的问题，已修正；1920×1080 确认与报表 1440×900 最终截图通过。沿用既有检测器，本批 0 blocking / 0 advisory。不增加移动验收或永久测试 gate。
