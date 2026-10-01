import { useMemo } from 'react';
import type { ColumnDef } from '@tanstack/react-table';
import { AlertTriangle, Search, ShieldCheck } from 'lucide-react';
import { DataTableBlock } from '@/blocks/data-table';
import { FactStrip } from '@/blocks/fact-strip';
import { Main } from '@/components/layout/Main';
import { StatusBadge } from '@/components/status-badge';
import { PageIntro } from '@/components/layout/PageIntro';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { DataTable, type DataTableFeatures } from '@/components/data-table';
import { useDataTable } from '@/hooks/use-data-table';

const rows = [
  { object: '冷机 1', state: '运行', evidence: '反馈新鲜 · 质量良好', action: '查看详情' },
  { object: '冷冻水泵群', state: '需要关注', evidence: '1 项反馈已陈旧', action: '查看证据' },
  { object: '冷却塔风机群', state: '未知', evidence: '当前状态暂不可用', action: '查看原因' },
] as const;

export function UiPatternGallery() {
  type GalleryRow = (typeof rows)[number];

  const columns = useMemo<Array<ColumnDef<DataTableFeatures, GalleryRow>>>(() => [
    { id: 'object', header: '对象', cell: ({ row }) => <span className="font-medium">{row.original.object}</span> },
    { id: 'state', header: '状态', cell: ({ row }) => row.original.state },
    { id: 'evidence', header: '证据', cell: ({ row }) => <span className="text-muted-foreground">{row.original.evidence}</span> },
    { id: 'action', header: '下一步', cell: ({ row }) => <Button size="sm" variant="ghost">{row.original.action}</Button>, enableSorting: false },
  ], []);

  const table = useDataTable({
    key: 'ui-pattern-gallery-table',
    data: [...rows],
    columns,
    paginate: false,
    getRowId: (row) => row.object,
  });

  return (
    <Main className="space-y-6">
      <PageIntro
        context="设计系统 · 当前权威"
        title="Shadcn Application System"
        description="仅用于检查当前 shadcn/ui 组件语法、密度、信息层级和状态表达。旧 Ant Design / ProComponents 页面、截图和组件画廊不属于当前视觉权威。"
        actions={<Button size="sm">主要动作</Button>}
      />

      <Alert>
        <ShieldCheck aria-hidden="true" />
        <AlertTitle>当前设计入口已隔离旧 UI</AlertTitle>
        <AlertDescription>
          新 Surface 只从 PRODUCT、当前 Surface Specification、DESIGN、shadcn component contract 与明确批准的当前参考出发。
        </AlertDescription>
      </Alert>

      <FactStrip
        ariaLabel="标准事实条示例"
        items={[
          { label: '当前功率', value: '504', suffix: 'kW', detail: '站点当前值' },
          { label: '系统 COP', value: '5.80', detail: '当前运行点', tone: 'positive' },
          { label: '活动告警', value: '2', detail: '1 项需要优先处理', tone: 'warning' },
          { label: '数据状态', value: '可信', detail: '最近 5 分钟', tone: 'accent' },
        ]}
      />

      <section className="grid gap-4 lg:grid-cols-3" aria-label="组件语法示例">
        <Card>
          <CardHeader>
            <CardTitle>语义区块</CardTitle>
            <CardDescription>Card 只用于完整语义 section，不作为所有内容的默认外壳。</CardDescription>
            <CardAction><Badge variant="outline">当前</Badge></CardAction>
          </CardHeader>
          <CardContent className="space-y-3 text-sm">
            <div className="flex items-center justify-between border-b pb-3">
              <span className="text-muted-foreground">当前功率</span>
              <strong className="tabular-nums">504 kW</strong>
            </div>
            <div className="flex items-center justify-between">
              <span className="text-muted-foreground">数据状态</span>
              <StatusBadge tone="success">新鲜</StatusBadge>
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>搜索与动作</CardTitle>
            <CardDescription>紧凑控件、明确动词和对象，不复刻旧 PageHeader 工具条。</CardDescription>
          </CardHeader>
          <CardContent className="space-y-3">
            <div className="relative">
              <Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" aria-hidden="true" />
              <Input className="pl-9" placeholder="搜索设备或业务对象" aria-label="搜索设备或业务对象" />
            </div>
            <div className="flex flex-wrap gap-2">
              <Button size="sm">创建工单</Button>
              <Button size="sm" variant="outline">查看证据</Button>
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>未知与不可用</CardTitle>
            <CardDescription>未知保持未知；异常状态提供事实、原因和下一步。</CardDescription>
          </CardHeader>
          <CardContent>
            <div className="flex gap-3 rounded-md border p-3 text-sm">
              <AlertTriangle className="mt-0.5 size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
              <div>
                <div className="font-medium">实时状态暂不可用</div>
                <p className="mt-1 text-xs leading-5 text-muted-foreground">最后确认状态仍可查看；不会把缓存值当作当前事实。</p>
              </div>
            </div>
          </CardContent>
        </Card>
      </section>

      <DataTableBlock
        title="数据浏览"
        description="页面级数据使用统一 DataTableBlock，不额外套 Card。"
        actions={<Badge variant="secondary">3 项</Badge>}
      >
        <DataTable
          table={table}
          tableAriaLabel="数据浏览示例"
          getHeaderCellProps={(header) => ({
            className: header.id === 'action' ? 'text-right' : undefined,
          })}
          getCellProps={(cell) => ({
            className: cell.column.id === 'action' ? 'text-right' : undefined,
          })}
        />
      </DataTableBlock>
    </Main>
  );
}
