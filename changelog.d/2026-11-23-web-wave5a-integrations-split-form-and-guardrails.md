---
date: 2026-11-23
category: 改进
---
- 按域拆分接入与治理前端纯模型（电表计量核对、传感器标定、无线集中器状态与配置、共享治理字典与网关配置影子文案），消除两千行单体模型文件并提升按模块维护性。
- 将自动控制租约弹窗（`ArmLeaseDialog`）迁回 `react-hook-form` 与 `@/components/ui/field`，统一失焦后校验与输入修正清错，补齐 `aria-invalid` / `aria-describedby` 与失败聚焦路由。
- 新增 `nuqs` 仅限 `DataTable` 高级筛选内部使用的静态检查，以及前端源码单文件 600 行上限与存量文件行数棘轮门禁。
