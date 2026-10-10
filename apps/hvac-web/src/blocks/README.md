# Application Blocks

`src/blocks` contains reusable application-level compositions built from the project's shadcn/ui primitives and domain components.

## Layering

- `components/ui`: shadcn/ui primitives.
- `components/data-table`: reusable table capabilities and TanStack Table integration.
- `blocks`: standardized application compositions. Blocks own layout grammar, not business data or feature state.
- `features`: business-specific columns, filters, queries, actions and workflows.

## Data table block

Use `DataTableBlock` for page-, tab- or workspace-level tables.

Canonical composition:

```tsx
<DataTableBlock
  title="工单台账"
  description="使用排序、高级筛选和列显示控制当前站点工单。"
  actions={...}
>
  <DataTable table={table} footer={...}>
    <DataTableAdvancedToolbar table={table}>
      ...
    </DataTableAdvancedToolbar>
  </DataTable>
</DataTableBlock>
```

The block intentionally does not own columns, filters, pagination state, queries or row actions. Those stay in the feature.

Do not wrap `DataTableBlock` in a Card. A page-level DataTable is already the bounded surface.

## Page header block

Every route page starts with `PageHeader` inside the `Main` container: one `h1`, an optional supporting line (scope, freshness or task), optional `meta` beside the title and local `actions`. It is flat semantic markup with no Card, border or background. Pages never render their own `<main>`; the shell's `SidebarInset` is the page landmark.

```tsx
<Main className="space-y-5">
  <PageHeader title="能耗与成本" description="中央机房 · 按站点当地时间统计" actions={...} />
  ...
</Main>
```

## Fact strip block

Use `FactStrip` for a small set of genuinely peer-level operational facts that benefit from rapid horizontal scanning.

```tsx
<FactStrip
  items={[
    { label: '当前功率', value: '504', suffix: 'kW' },
    { label: '系统 COP', value: '5.80' },
  ]}
/>
```

Rules:

- the facts must be semantically peer-level; do not force unrelated scope, KPI, process and risk facts into one strip;
- feature code owns labels, values, units, tones and truth;
- the block owns only responsive layout and visual grammar;
- do not recreate another metric/KPI strip in a feature CSS file.

## Compact data inside existing surfaces

Do not use a second DataTable mode for compact detail content.

- Small read-only comparison / parameter / evidence matrices inside Card, Sheet, Drawer or `details`: use shadcn `Table` directly.
- Master-detail object selectors: use shadcn `ItemGroup / Item`.
- Chronological execution or change history: prefer a Timeline when the data is genuinely event-oriented.
- Spreadsheet editing, heavy virtualization, pinned columns or tree/grid behavior: review ReUI / Tablecn Data Grid before adding custom behavior.

A parent surface plus its compact child content must have one complete outer boundary. Do not add `Card → rounded border wrapper → Table` or `Card → DataTable` nesting.
