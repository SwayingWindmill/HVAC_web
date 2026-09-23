// @surface-card-table-exception 06 — preserve reviewed Surface 06 ledger Card/table anatomy.
import { useEffect, useMemo, useState } from 'react';
import type { ColumnDef } from '@tanstack/react-table';
import {
  Activity,
  AlertTriangle,
  ArrowUpRight,
  Box,
  CheckCircle2,
  Fan,
  GripVertical,
  Radio,
  RadioTower,
  RefreshCw,
  Search,
  Server,
  SlidersHorizontal,
  Snowflake,
  Wind,
  Zap,
} from 'lucide-react';

import type { CurrentPrincipalResponse, Site } from '@/api/generated/platformGateway.gen';
import type { HvacRouterContext } from '@/app/router-context';
import {
  DataTable,
  DataTableAdvancedToolbar,
  DataTableFilterList,
  DataTablePagination,
  DataTableSortList,
  type DataTableFeatures,
} from '@/components/data-table';
import { Main } from '@/components/layout/Main';
import { StatusBadge } from '@/components/status-badge';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Checkbox } from '@/components/ui/checkbox';
import {
  Empty,
  EmptyContent,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from '@/components/ui/empty';
import { Input } from '@/components/ui/input';
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet';
import { Skeleton } from '@/components/ui/skeleton';
import {
  assetsDeviceTypeLabel,
  type AssetsDeviceRow,
  type AssetsHierarchyNode,
} from '@/features/assets/model';
import {
  filterAssetsDeviceRows,
  indexAssetsHierarchy,
  summarizeAssetsDevices,
} from '@/features/assets/workspace-selectors';
import { useSiteAssetsData } from '@/features/assets/use-site-assets-data';
import { useDataTable } from '@/hooks/use-data-table';
import { cn } from '@/lib/utils';
import {
  ATTENTION_LABELS,
  connectedAsset,
  connectionPresentation,
  deviceLocation,
  freshnessLabel,
  latestTimestamp,
  metricPoints,
  qualityLabel,
  runningPresentation,
} from './presentation';

export interface AssetsSearchState {
  readonly q?: string;
  readonly scope?: string;
  readonly inspect?: string;
}

interface AssetsWorkspaceProps {
  readonly site: Readonly<Site>;
  readonly principal: CurrentPrincipalResponse;
  readonly runtime: HvacRouterContext['runtime'];
  readonly searchState: AssetsSearchState;
  readonly onSearchChange: (patch: Partial<AssetsSearchState>) => void;
  readonly onOpenDetail: (deviceId: string) => void;
}

const PAGE_SIZE = 10;

function getDeviceTypeIcon(deviceType: string) {
  const normalized = deviceType.toLowerCase();
  if (normalized.includes('chiller') || normalized.includes('冷水机')) return Snowflake;
  if (normalized.includes('pump') || normalized.includes('水泵')) return Fan;
  if (normalized.includes('tower') || normalized.includes('冷却塔')) return RadioTower;
  if (normalized.includes('ahu') || normalized.includes('空调箱') || normalized.includes('air')) return Wind;
  if (normalized.includes('fcu') || normalized.includes('vav') || normalized.includes('盘管')) return SlidersHorizontal;
  if (normalized.includes('sensor') || normalized.includes('传感器')) return Radio;
  if (normalized.includes('meter') || normalized.includes('电表')) return Zap;
  return Server;
}

function primaryLoadPoint(row: AssetsDeviceRow) {
  const points = metricPoints(row, 12);
  return points.find((point) => /功率|power/i.test(point.label)) ?? points[0] ?? null;
}

function telemetrySummary(row: AssetsDeviceRow) {
  return metricPoints(row, 4)
    .filter((point) => point.state === 'PRESENT')
    .slice(0, 2)
    .map((point) => `${point.label} ${point.displayValue}${point.unit ? ` ${point.unit}` : ''}`)
    .join(' · ');
}

function flattenScopes(root: AssetsHierarchyNode | null) {
  if (!root) return [] as Array<{ key: string; label: string; depth: number; count: number }>;
  const result: Array<{ key: string; label: string; depth: number; count: number }> = [];
  const visit = (node: AssetsHierarchyNode, depth: number) => {
    if (node.kind !== 'device') {
      result.push({ key: node.key, label: node.label, depth, count: node.deviceIds.length });
    }
    node.children.forEach((child) => visit(child, node.kind === 'device' ? depth : depth + 1));
  };
  visit(root, 0);
  return result;
}

function ScopeSelect({
  value,
  options,
  onChange,
}: {
  readonly value: string;
  readonly options: readonly { key: string; label: string; depth: number; count: number }[];
  readonly onChange: (value: string) => void;
}) {
  return (
    <Select value={value} onValueChange={onChange}>
      <SelectTrigger className="h-9 min-w-44 font-normal" aria-label="设备范围">
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        <SelectGroup>
          {options.map((option) => (
            <SelectItem key={option.key} value={option.key}>
              {option.depth > 0 ? `${'· '.repeat(Math.min(option.depth, 2))}${option.label}` : option.label}
              <span className="ml-1 text-muted-foreground">({option.count})</span>
            </SelectItem>
          ))}
        </SelectGroup>
      </SelectContent>
    </Select>
  );
}

function useDesktopQuickPreview() {
  const [isDesktop, setIsDesktop] = useState(() =>
    typeof window !== 'undefined' && window.matchMedia('(min-width: 1280px)').matches
  );

  useEffect(() => {
    const media = window.matchMedia('(min-width: 1280px)');
    const sync = () => setIsDesktop(media.matches);
    media.addEventListener('change', sync);
    sync();
    return () => media.removeEventListener('change', sync);
  }, []);

  return isDesktop;
}

function RunningBadge({ row }: { readonly row: AssetsDeviceRow }) {
  const running = runningPresentation(row);
  return (
    <StatusBadge
      label={running.label}
      tone={running.label === '运行中' ? 'in-progress' : 'neutral'}
      className="h-6 px-2 text-[11px]"
    />
  );
}

function ConnectionBadge({ row }: { readonly row: AssetsDeviceRow }) {
  const connection = connectionPresentation(row);
  return (
    <StatusBadge
      label={connection.label}
      tone={connection.label === '在线' ? 'success' : connection.label === '离线' ? 'destructive' : 'neutral'}
      className="h-6 px-2 text-[11px]"
    />
  );
}

function DeviceInspectorContent({
  row,
  timezone,
  onOpenDetail,
}: {
  readonly row: AssetsDeviceRow;
  readonly timezone: string;
  readonly onOpenDetail: () => void;
}) {
  const points = metricPoints(row, 4);
  const asset = connectedAsset(row);
  const DeviceIcon = getDeviceTypeIcon(row.device.deviceType);
  const attentionReasons = row.operational.attentionReasons.map((reason) => ATTENTION_LABELS[reason]);
  const freshness = freshnessLabel(row.operational.telemetry.freshness);
  const quality = qualityLabel(row.operational.telemetry.quality);

  return (
    <div className="flex h-full min-h-0 flex-col bg-background" aria-label="设备快速查看">
      <div className="border-b px-5 py-5 pr-12">
        <div className="flex items-start gap-3">
          <DeviceIcon className="mt-0.5 size-5 shrink-0 text-muted-foreground" aria-hidden="true" />
          <div className="min-w-0 flex-1">
            <div className="flex min-w-0 items-center gap-2">
              <h2 className="truncate text-base font-semibold tracking-tight">{row.device.displayName}</h2>
              <Badge variant="secondary" className="shrink-0 font-normal">
                {assetsDeviceTypeLabel(row.device.deviceType)}
              </Badge>
            </div>
            <p className="mt-1 truncate text-xs text-muted-foreground">
              {row.device.code} · {deviceLocation(row)}
            </p>
          </div>
        </div>

        <div className="mt-4 flex flex-wrap items-center gap-1.5">
          <RunningBadge row={row} />
          <ConnectionBadge row={row} />
        </div>
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto">
        <section className="px-5 py-4">
          <h3 className="text-xs font-medium text-muted-foreground">当前状态</h3>
          <dl className="mt-2 divide-y text-sm">
            <div className="flex items-center justify-between gap-4 py-2.5">
              <dt className="text-muted-foreground">数据新鲜度</dt>
              <dd className="font-medium">{freshness}</dd>
            </div>
            <div className="flex items-center justify-between gap-4 py-2.5">
              <dt className="text-muted-foreground">数据质量</dt>
              <dd className="font-medium">{quality}</dd>
            </div>
            <div className="flex items-center justify-between gap-4 py-2.5">
              <dt className="text-muted-foreground">最近更新</dt>
              <dd className="text-right font-medium tabular-nums">{latestTimestamp(row, timezone)}</dd>
            </div>
          </dl>
        </section>

        <section className="border-t px-5 py-4">
          <h3 className="text-xs font-medium text-muted-foreground">关键值</h3>
          {points.length > 0 ? (
            <dl className="mt-2 divide-y text-sm">
              {points.map((point) => (
                <div key={point.pointId} className="flex items-center justify-between gap-4 py-2.5">
                  <dt className="min-w-0 truncate text-muted-foreground">{point.label}</dt>
                  <dd className="shrink-0 font-medium tabular-nums">
                    {point.state === 'PRESENT' ? point.displayValue : '—'}
                    {point.state === 'PRESENT' && point.unit ? ` ${point.unit}` : ''}
                  </dd>
                </div>
              ))}
            </dl>
          ) : (
            <p className="mt-2 text-sm text-muted-foreground">当前没有可显示的关键值。</p>
          )}
        </section>

        <section className="border-t px-5 py-4">
          <h3 className="text-xs font-medium text-muted-foreground">当前事项</h3>
          {attentionReasons.length > 0 ? (
            <ul className="mt-2 space-y-2 text-sm">
              {attentionReasons.slice(0, 4).map((reason) => (
                <li key={reason} className="flex items-start gap-2">
                  <AlertTriangle className="mt-0.5 size-4 shrink-0 text-warning" aria-hidden="true" />
                  <span>{reason}</span>
                </li>
              ))}
            </ul>
          ) : (
            <div className="mt-2 flex items-center gap-2 text-sm text-muted-foreground">
              <CheckCircle2 className="size-4 text-success" aria-hidden="true" />
              当前没有需要优先处理的事项
            </div>
          )}
        </section>

        <section className="border-t px-5 py-4">
          <h3 className="text-xs font-medium text-muted-foreground">对象上下文</h3>
          <dl className="mt-2 divide-y text-sm">
            <div className="flex items-center justify-between gap-4 py-2.5">
              <dt className="text-muted-foreground">位置</dt>
              <dd className="text-right font-medium">{deviceLocation(row)}</dd>
            </div>
            <div className="flex items-center justify-between gap-4 py-2.5">
              <dt className="text-muted-foreground">关联资产</dt>
              <dd className="text-right font-medium">{asset ?? '未关联'}</dd>
            </div>
          </dl>
        </section>
      </div>

      <div className="border-t p-4">
        <Button variant="outline" className="w-full justify-between" onClick={onOpenDetail}>
          打开完整设备详情
          <ArrowUpRight aria-hidden="true" />
        </Button>
      </div>
    </div>
  );
}

function LoadingState() {
  return (
    <Main fluid className="space-y-4">
      <div className="flex items-center justify-between gap-4">
        <Skeleton className="h-5 w-72" />
        <Skeleton className="h-8 w-24" />
      </div>
      <div className="flex gap-2">
        <Skeleton className="h-9 w-72" />
        <Skeleton className="h-9 w-44" />
        <Skeleton className="h-9 w-20" />
        <Skeleton className="h-9 w-20" />
      </div>
      <Skeleton className="h-[650px] w-full rounded-md" />
    </Main>
  );
}

export function AssetsWorkspace({
  site,
  principal,
  runtime,
  searchState,
  onSearchChange,
  onOpenDetail,
}: AssetsWorkspaceProps) {
  const data = useSiteAssetsData({ site, principal, runtime });
  const hierarchyIndex = useMemo(() => indexAssetsHierarchy(data.hierarchy), [data.hierarchy]);
  const scopeOptions = useMemo(() => flattenScopes(data.hierarchy), [data.hierarchy]);
  const defaultScope = `site:${site.id}`;
  const selectedScope = hierarchyIndex.get(searchState.scope ?? defaultScope) ?? data.hierarchy;
  const selectedDeviceIds = selectedScope ? new Set(selectedScope.deviceIds) : undefined;
  const isDesktopQuickPreview = useDesktopQuickPreview();

  const baseRows = useMemo(() => filterAssetsDeviceRows({
    rows: data.rows,
    search: searchState.q ?? '',
    selectedDeviceIds,
    listMode: 'all',
    connectionFilter: 'all',
    runningFilter: 'all',
    dataFilter: 'all',
    currentPending: data.currentPending,
    currentUnavailable: data.currentUnavailable,
  }), [
    data.currentPending,
    data.currentUnavailable,
    data.rows,
    searchState.q,
    selectedDeviceIds,
  ]);

  const counts = summarizeAssetsDevices(data.rows, data.currentPending, data.currentUnavailable);
  const inspected = data.rows.find((row) => row.device.id === searchState.inspect);

  const deviceTypeOptions = useMemo(() => (
    Array.from(new Set(data.rows.map((row) => row.device.deviceType)))
      .sort((left, right) => assetsDeviceTypeLabel(left).localeCompare(assetsDeviceTypeLabel(right), 'zh-CN', { numeric: true }))
      .map((value) => ({ value, label: assetsDeviceTypeLabel(value) }))
  ), [data.rows]);

  const columns = useMemo<Array<ColumnDef<DataTableFeatures, AssetsDeviceRow>>>(() => [
    {
      id: 'select',
      header: ({ table }) => (
        <Checkbox
          checked={table.getIsAllPageRowsSelected() ? true : table.getIsSomePageRowsSelected() ? 'indeterminate' : false}
          onCheckedChange={(checked) => table.toggleAllPageRowsSelected(Boolean(checked))}
          aria-label="全选设备"
        />
      ),
      cell: ({ row }) => (
        <Checkbox
          checked={row.getIsSelected()}
          onCheckedChange={(checked) => row.toggleSelected(Boolean(checked))}
          aria-label={`选择设备 ${row.original.device.code}`}
          onClick={(event) => event.stopPropagation()}
        />
      ),
      enableSorting: false,
      enableHiding: false,
    },
    {
      id: 'drag',
      header: '',
      cell: () => <GripVertical className="size-3.5 text-muted-foreground/40" aria-hidden="true" />,
      enableSorting: false,
      enableHiding: false,
    },
    {
      id: 'code',
      accessorFn: (row) => row.device.code,
      enableSorting: true,
      meta: { label: '设备编号' },
      header: '设备编号',
      cell: ({ row }) => <span className="font-mono text-xs font-medium text-foreground">{row.original.device.code}</span>,
    },
    {
      id: 'name',
      accessorFn: (row) => row.device.displayName,
      enableSorting: true,
      meta: { label: '设备名称 / 分项' },
      header: '设备名称 / 分项',
      cell: ({ row }) => {
        const DeviceIcon = getDeviceTypeIcon(row.original.device.deviceType);
        return (
          <div className="flex min-w-0 items-center gap-2">
            <div className="flex size-6 shrink-0 items-center justify-center rounded-md border bg-muted/40 text-muted-foreground">
              <DeviceIcon className="size-3" aria-hidden="true" />
            </div>
            <div className="min-w-0">
              <div className="truncate text-xs font-medium text-foreground">{row.original.device.displayName}</div>
              <div className="mt-0.5 truncate text-[11px] text-muted-foreground">{assetsDeviceTypeLabel(row.original.device.deviceType)}</div>
            </div>
          </div>
        );
      },
    },
    {
      id: 'location',
      accessorFn: (row) => deviceLocation(row),
      enableSorting: true,
      meta: { label: '物理空间 / 机房' },
      header: '物理空间 / 机房',
      cell: ({ row }) => <span className="text-xs text-muted-foreground">{deviceLocation(row.original)}</span>,
    },
    {
      id: 'running',
      accessorFn: (row) => runningPresentation(row).label,
      meta: { label: '运行工况' },
      header: '运行工况',
      cell: ({ row }) => {
        const running = runningPresentation(row.original);
        return (
          <StatusBadge
            label={running.label}
            tone={running.label === '运行中' ? 'in-progress' : running.label === '未知' ? 'warning' : 'neutral'}
            className="h-6 px-2 text-[11px]"
          />
        );
      },
    },
    {
      id: 'load',
      accessorFn: (row) => primaryLoadPoint(row)?.displayValue ?? '',
      enableSorting: false,
      meta: { label: '实时负荷' },
      header: '实时负荷',
      cell: ({ row }) => {
        const point = primaryLoadPoint(row.original);
        if (!point || point.state !== 'PRESENT') return <span className="text-xs text-muted-foreground">—</span>;
        return (
          <div className="min-w-24 text-xs">
            <div className="font-medium tabular-nums text-foreground">
              {point.displayValue}{point.unit ? ` ${point.unit}` : ''}
            </div>
            <div className="mt-0.5 text-[11px] text-muted-foreground">负荷率 —</div>
          </div>
        );
      },
    },
    {
      id: 'telemetry',
      accessorFn: (row) => telemetrySummary(row),
      enableSorting: false,
      meta: { label: '关键遥测指标' },
      header: '关键遥测指标',
      cell: ({ row }) => {
        const summary = telemetrySummary(row.original);
        return <span className="line-clamp-2 text-xs text-muted-foreground">{summary || '—'}</span>;
      },
    },
    {
      id: 'health',
      enableSorting: false,
      meta: { label: '健康评分' },
      header: '健康评分',
      cell: () => (
        <div className="text-center text-xs">
          <span className="font-medium text-foreground">—</span>
          <span className="ml-1 text-[10px] text-muted-foreground">未提供</span>
        </div>
      ),
    },
    {
      id: 'actions',
      header: '操作',
      enableSorting: false,
      enableHiding: false,
      cell: ({ row }) => (
        <Button
          variant="ghost"
          size="sm"
          className="h-7 px-2 text-xs text-muted-foreground hover:text-foreground"
          onClick={(event) => {
            event.stopPropagation();
            onOpenDetail(row.original.device.id);
          }}
        >
          <ArrowUpRight className="size-3.5" aria-hidden="true" />
          查看详情
        </Button>
      ),
    },
    {
      id: 'deviceTypeFilter',
      enableSorting: false,
      accessorFn: (row) => row.device.deviceType,
      enableColumnFilter: true,
      enableHiding: false,
      meta: { label: '设备类型', variant: 'select', options: deviceTypeOptions },
      header: () => null,
      cell: () => null,
    },
    {
      id: 'connectionFilter',
      enableSorting: false,
      accessorFn: (row) => connectionPresentation(row).label,
      enableColumnFilter: true,
      enableHiding: false,
      meta: {
        label: '连接状态',
        variant: 'select',
        options: [
          { value: '在线', label: '在线' },
          { value: '离线', label: '离线' },
          { value: '未知', label: '未知' },
        ],
      },
      header: () => null,
      cell: () => null,
    },
    {
      id: 'runningFilter',
      enableSorting: false,
      accessorFn: (row) => runningPresentation(row).label,
      enableColumnFilter: true,
      enableHiding: false,
      meta: {
        label: '运行状态',
        variant: 'select',
        options: [
          { value: '运行中', label: '运行中' },
          { value: '已停止', label: '已停止' },
          { value: '待机', label: '待机' },
          { value: '未知', label: '未知' },
        ],
      },
      header: () => null,
      cell: () => null,
    },
    {
      id: 'dataFilter',
      enableSorting: false,
      accessorFn: (row) => (
        row.operational.telemetry.freshness === 'FRESH' && row.operational.telemetry.quality === 'GOOD'
          ? 'healthy'
          : 'issue'
      ),
      enableColumnFilter: true,
      enableHiding: false,
      meta: {
        label: '数据状态',
        variant: 'select',
        options: [
          { value: 'healthy', label: '数据正常' },
          { value: 'issue', label: '数据需核查' },
        ],
      },
      header: () => null,
      cell: () => null,
    },
  ], [deviceTypeOptions, onOpenDetail]);

  const table = useDataTable({
    key: 'surface-06-device-workspace-v3',
    data: [...baseRows],
    columns,
    pageSize: PAGE_SIZE,
    getRowId: (row) => row.device.id,
    meta: {
      queryKeys: {
        page: 'devicePage',
        perPage: 'devicePerPage',
        sort: 'deviceSort',
        filters: 'deviceFilters',
        joinOperator: 'deviceJoin',
      },
    },
  });

  if (data.registry.isPending) return <LoadingState />;

  if (data.registry.isError) {
    return (
      <Main fluid>
        <Empty className="min-h-[520px] w-full border" data-testid="assets-workspace-error">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <AlertTriangle className="text-destructive" aria-hidden="true" />
            </EmptyMedia>
            <EmptyTitle>设备数据暂时无法加载</EmptyTitle>
            <EmptyDescription>设备清单暂不可用，请稍后重试。</EmptyDescription>
          </EmptyHeader>
          <EmptyContent>
            <Button onClick={() => void data.registry.refetch()}>
              <RefreshCw />
              重新加载
            </Button>
          </EmptyContent>
        </Empty>
      </Main>
    );
  }

  const currentStateLabel = data.currentPending
    ? '正在读取当前状态'
    : data.currentUnavailable
      ? '当前状态不可用'
      : '当前状态已更新';
  const onlineCount = counts.online ?? 0;
  const onlineRate = data.rows.length > 0 ? (onlineCount / data.rows.length) * 100 : 0;

  const tableContent = (
    <Card className="min-w-0 shadow-xs" aria-label="设备运行台账">
      <CardHeader className="space-y-3 border-b pb-3">
        <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
          <CardTitle className="text-base font-semibold">设备运行台账</CardTitle>
          <ScopeSelect
            value={selectedScope?.key ?? defaultScope}
            options={scopeOptions}
            onChange={(scope) => {
              onSearchChange({ scope, inspect: undefined });
              table.setPageIndex(0);
            }}
          />
        </div>
        <DataTableAdvancedToolbar table={table} className="p-0">
          <div className="relative min-w-64 flex-1 lg:max-w-sm">
            <Search className="pointer-events-none absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
            <Input
              className="h-8 pl-8 text-xs"
              placeholder="搜索设备名称、编号或位置..."
              value={searchState.q ?? ''}
              onChange={(event) => {
                onSearchChange({ q: event.currentTarget.value || undefined, inspect: undefined });
                table.setPageIndex(0);
              }}
            />
          </div>
          <DataTableSortList table={table} />
          <DataTableFilterList table={table} />
        </DataTableAdvancedToolbar>
      </CardHeader>
      <CardContent className="p-0">
        <DataTable
          table={table}
          className="gap-0"
          tableClassName="min-w-[1180px]"
          tableAriaLabel="设备运行台账"
          empty="未找到匹配的设备"
          getHeaderRowProps={() => ({ className: 'bg-muted/20 hover:bg-transparent' })}
          getHeaderCellProps={(header) => ({
            className:
              header.id === 'select' ? 'w-10 px-3 text-center' :
              header.id === 'drag' ? 'w-6 px-0' :
              header.id === 'code' ? 'w-28 text-xs font-medium' :
              header.id === 'name' ? 'w-[250px] text-xs font-medium' :
              header.id === 'location' ? 'w-[180px] text-xs font-medium' :
              header.id === 'running' ? 'w-[100px] text-xs font-medium' :
              header.id === 'load' ? 'w-[120px] text-xs font-medium' :
              header.id === 'telemetry' ? 'w-[220px] text-xs font-medium' :
              header.id === 'health' ? 'w-[110px] text-center text-xs font-medium' :
              header.id === 'actions' ? 'w-[100px] text-right text-xs font-medium' :
              'hidden',
          })}
          getRowProps={(row) => {
            const isSelected = row.original.device.id === inspected?.device.id;
            return {
              className: cn(
                'cursor-pointer text-xs transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring',
                isSelected ? 'bg-muted/60 hover:bg-muted/70' : 'hover:bg-muted/40',
              ),
              'data-state': isSelected ? 'selected' : undefined,
              'aria-selected': isSelected,
              tabIndex: 0,
              onClick: () => onSearchChange({ inspect: row.original.device.id }),
              onKeyDown: (event) => {
                if (event.key !== 'Enter' && event.key !== ' ') return;
                event.preventDefault();
                onSearchChange({ inspect: row.original.device.id });
              },
            };
          }}
          getCellProps={(cell) => ({
            className:
              cell.column.id.endsWith('Filter') ? 'hidden' :
              cell.column.id === 'select' ? 'w-10 px-3 text-center' :
              cell.column.id === 'drag' ? 'w-6 px-0' :
              cell.column.id === 'health' ? 'text-center' :
              cell.column.id === 'actions' ? 'text-right' :
              'py-2.5',
          })}
          footer={<DataTablePagination table={table} totalRows={baseRows.length} />}
        />
      </CardContent>
    </Card>
  );

  return (
    <Main fluid className="space-y-6 pb-16" data-testid="assets-workspace" data-site-id={site.id}>
      <header className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <div className="flex flex-wrap items-center gap-2.5">
            <h1 className="text-2xl font-bold tracking-tight text-foreground">设备中心</h1>
            <Badge variant="outline" className="text-xs font-normal">在册 {data.rows.length} 台设备</Badge>
          </div>
          <p className="mt-1 text-sm text-muted-foreground">
            {site.displayName} · {site.timezone} · 全站机电设备运行状态与关键遥测台账
          </p>
        </div>
        <div className="flex shrink-0 items-center gap-3">
          <span className="text-xs text-muted-foreground">{currentStateLabel}</span>
          <Button
            variant="outline"
            size="sm"
            onClick={data.refresh}
            disabled={data.registry.isFetching || data.current.isFetching}
          >
            <RefreshCw
              className={cn((data.registry.isFetching || data.current.isFetching) && 'animate-spin')}
              aria-hidden="true"
            />
            刷新状态
          </Button>
        </div>
      </header>

      <section className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4" aria-label="设备概况卡片">
        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">受控设备总数</CardTitle>
            <Box className="size-4 text-muted-foreground" aria-hidden="true" />
          </CardHeader>
          <CardContent className="space-y-1">
            <div className="text-2xl font-bold tracking-tight tabular-nums">{data.rows.length} <span className="text-xs font-normal text-muted-foreground">台</span></div>
            <p className="text-xs text-muted-foreground">当前站点设备清单</p>
          </CardContent>
        </Card>
        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">实时在线率</CardTitle>
            <Activity className="size-4 text-muted-foreground" aria-hidden="true" />
          </CardHeader>
          <CardContent className="space-y-1">
            <div className="text-2xl font-bold tracking-tight tabular-nums">{onlineRate.toFixed(1)}%</div>
            <p className="text-xs text-muted-foreground">{onlineCount} 在线 · {Math.max(data.rows.length - onlineCount, 0)} 非在线</p>
          </CardContent>
        </Card>
        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">需关注设备</CardTitle>
            <AlertTriangle className="size-4 text-muted-foreground" aria-hidden="true" />
          </CardHeader>
          <CardContent className="space-y-1">
            <div className="text-2xl font-bold tracking-tight tabular-nums">{counts.attention ?? '—'} <span className="text-xs font-normal text-muted-foreground">台</span></div>
            <p className="text-xs text-muted-foreground">连接、运行或遥测存在当前事项</p>
          </CardContent>
        </Card>
        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">数据质量问题</CardTitle>
            <Radio className="size-4 text-muted-foreground" aria-hidden="true" />
          </CardHeader>
          <CardContent className="space-y-1">
            <div className="text-2xl font-bold tracking-tight tabular-nums">{counts.dataIssue ?? '—'} <span className="text-xs font-normal text-muted-foreground">台</span></div>
            <p className="text-xs text-muted-foreground">新鲜度或质量需要核查</p>
          </CardContent>
        </Card>
      </section>

      <div className="min-w-0" role="region" aria-label="设备" tabIndex={0}>
        {tableContent}
      </div>

      {inspected ? (
        <Sheet
          open
          modal={!isDesktopQuickPreview}
          onOpenChange={(open) => {
            if (!open) onSearchChange({ inspect: undefined });
          }}
        >
          <SheetContent
            className="w-full overflow-hidden p-0 sm:max-w-[540px]! xl:max-w-[440px]!"
            showOverlay={!isDesktopQuickPreview}
            onOpenAutoFocus={(event) => {
              if (isDesktopQuickPreview) event.preventDefault();
            }}
            onInteractOutside={(event) => {
              if (isDesktopQuickPreview) event.preventDefault();
            }}
          >
            <SheetHeader className="sr-only">
              <SheetTitle>{inspected.device.displayName}</SheetTitle>
              <SheetDescription>快速查看设备当前状态、关键值和需要关注的事项。</SheetDescription>
            </SheetHeader>
            <DeviceInspectorContent
              row={inspected}
              timezone={site.timezone}
              onOpenDetail={() => onOpenDetail(inspected.device.id)}
            />
          </SheetContent>
        </Sheet>
      ) : null}
    </Main>
  );
}
