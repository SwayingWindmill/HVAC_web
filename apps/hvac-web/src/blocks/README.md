# 应用组合块（blocks）

`src/blocks` 是用 shadcn/ui 组件拼好的、各页面共用的组合。设计规则见根目录 `DESIGN.md`。

## 分层

- `components/ui`：shadcn/ui 的基础组件。
- `components/data-table`：表格能力和 TanStack Table 的接入。
- `blocks`：标准的页面组合，只管版式，不管业务数据和状态。
- `features`：业务相关的列、筛选、查询、操作和流程。

## 有哪些

| 组合 | 用途 |
| --- | --- |
| `Main` + `PageHeader` / `PageMeta`（`page-header`） | 每个页面的开头：一个 `h1`、可选的一行说明（范围、数据时间或任务），标题旁的 `meta` 和右边的操作。平铺的语义标签，没有卡片、边框和背景。页面不自己渲染 `<main>`，应用框架的 `SidebarInset` 就是页面的主区域 |
| `Panel` / `PanelHeader` / `PanelLink` / `PanelCount`（`panel`） | 唯一的白色面板，标题栏高 48px；数字放在面板里作为一条分隔的数字条 |
| `StateCard`（`state`） | 空、失败、无权限、未接入，以及路由级的状态页和 404 |
| `MetricValue`（`metric-card`） | 显示一个数：没有数据时写「暂无数据」而不是 0，数据过期时变淡 |
| `FactTile`（`fact-list`） | 一个「标签 + 值」的小格子。`className` 作用在值上；要跨列，外面包一层 `<div className="col-span-…">` |
| `DataTableBlock`（`data-table`） | 页面、标签页级别的表格 |

不要再另做数字条、KPI 卡或状态胶囊；需要时扩展上面这些。

## 表格

页面、标签页级别的表格用 `DataTableBlock`：

```tsx
<DataTableBlock title="工单" description="当前站点的工单，可排序、筛选和选择显示的列。" actions={...}>
  <DataTable table={table} footer={...}>
    <DataTableAdvancedToolbar table={table}>
      ...
    </DataTableAdvancedToolbar>
  </DataTable>
</DataTableBlock>
```

- 标题写业务对象（「工单」），不写「台账」「列表」「明细」这类容器名（`npm run design:check` 会检查）。
- 它不管列、筛选、分页状态、查询和行操作，这些留在 feature 里。
- 不要再在外面包一层卡片，页面级的表格本身就是一个完整的区域。

面板或侧栏里的小块数据，不要用第二种表格模式：

- 小的只读对比、参数、依据表：直接用 shadcn 的 `Table`。
- 左边选对象、右边看详情：用 shadcn 的 `ItemGroup` / `Item`。
- 按时间排的执行或变更记录：真是事件时用时间线。
- 表格内编辑、大量数据的虚拟滚动、固定列、树形表格：先看 ReUI / tablecn 的 Data Grid，再考虑自己写。

一个面板和它里面的小块内容只能有一层外框，不要「卡片 → 圆角边框 → 表格」或「卡片 → DataTable」这样嵌套。
