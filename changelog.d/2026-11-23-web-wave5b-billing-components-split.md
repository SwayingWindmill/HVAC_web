---
date: 2026-11-23
category: 改进
---
- 拆分电费账单页面中的超长组件（`AccountingConfigTab.tsx`、`UtilityBillDialog.tsx`、`BillReconciliationSheet.tsx`）为职责单一的子模块，使账单功能目录全部组件文件控制在 600 行以内，并同步收紧 `scripts/web-file-lines-baseline.json` 棘轮基线（24 降至 21）。
- 将站点节假日配置、新增电表倍率版本与终止倍率版本弹窗统一接入 `react-hook-form` 与字段级无障碍错误联动（失焦校验、修改即清错、`aria-invalid` 与仅在报错时挂载 `aria-describedby`），并补充结算电费单表单契约单元测试。
