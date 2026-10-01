import { useMemo } from 'react';
import type { ColumnDef } from '@tanstack/react-table';
import {
  Activity,
  AlertTriangle,
  ArrowRight,
  CheckCircle2,
  Database,
  Fan,
  Layers,
  MapPin,
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
import { DataTable, type DataTableFeatures } from '@/components/data-table';
import { Main } from '@/components/layout/Main';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardAction, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from '@/components/ui/empty';
import {
  InputGroup,
  InputGroupAddon,
  InputGroupInput,
  InputGroupText,
} from '@/components/ui/input-group';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
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
  type AssetsConnectionFilter,
  type AssetsDataFilter,
  type AssetsRunningFilter,
} from '@/features/assets/workspace-selectors';
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
import { useSiteAssetsData } from '@/features/assets/use-site-assets-data';

export interface AssetsSearchState {
  readonly q?: string;
  readonly scope?: string;
  readonly deviceType?: string;
  readonly connection?: AssetsConnectionFilter;
  readonly running?: AssetsRunningFilter;
  readonly data?: AssetsDataFilter;
  readonly page?: number;
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

const PAGE_SIZE = 15;

function getDeviceTypeIcon(deviceType: string) {
  const normalized = deviceType.toLowerCase();
  if (normalized.includes('chiller') || normalized.includes('ch') || normalized.includes('冷水机')) return Snowflake;
  if (normalized.includes('pump') || normalized.includes('水泵')) return Fan;
  if (normalized.includes('tower') || normalized.includes('冷却塔')) return RadioTower;
  if (normalized.includes('ahu') || normalized.includes('空调箱') || normalized.includes('air')) return Wind;
  if (normalized.includes('fcu') || normalized.includes('vav') || normalized.includes('盘管')) return SlidersHorizontal;
  if (normalized.includes('sensor') || normalized.includes('传感器')) return Radio;
  if (normalized.includes('meter') || normalized.includes('电表')) return Zap;
  return Server;
}

function flattenScopes(root: AssetsHierarchyNode | null) {
  if (!root) return [] as Array<{ key: string; label: string; depth: number; count: number }>;
  const result: Array<{ key: string; label: string; depth: number; count: number }> = [];
  const visit = (node: AssetsHierarchyNode, depth: number) => {
    if (node.kind !== 'device') result.push({ key: node.key, label: node.label, depth, count: node.deviceIds.length });
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
      <SelectTrigger className="min-w-44 h-9 text-xs" aria-label="设备范围"><SelectValue /></SelectTrigger>
      <SelectContent>
        {options.map((option) => (
          <SelectItem key={option.key} value={option.key}>
            {`${'· '.repeat(Math.min(option.depth, 2))}${option.label} (${option.count})`}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}

function FilterSelect({
  label,
  value,
  options,
  onChange,
}: {
  readonly label: string;
  readonly value: string;
  readonly options: readonly (readonly [string, string])[];
  readonly onChange: (value: string) => void;
}) {
  return (
    <Select value={value} onValueChange={onChange}>
      <SelectTrigger aria-label={label} className="h-9 text-xs"><SelectValue /></SelectTrigger>
      <SelectContent>
        {options.map(([optionValue, optionLabel]) => <SelectItem key={optionValue} value={optionValue}>{optionLabel}</SelectItem>)}
      </SelectContent>
    </Select>
  );
}

function Inspector({
  row,
  onClose,
  onOpenDetail,
}: {
  readonly row: AssetsDeviceRow;
  readonly onClose: () => void;
  readonly onOpenDetail: () => void;
}) {
  const connection = connectionPresentation(row);
  const running = runningPresentation(row);
  const points = metricPoints(row, 4);
  const asset = connectedAsset(row);
  const DeviceIcon = getDeviceTypeIcon(row.device.deviceType);

  return (
    <aside className="w-full shrink-0 border-t bg-muted/10 xl:w-[360px] xl:border-l xl:border-t-0" aria-label="设备上下文">
      <div className="flex items-start justify-between gap-3 border-b bg-muted/20 px-4 py-3.5">
        <div className="flex items-start gap-3 min-w-0">
          <div className="flex size-9 shrink-0 items-center justify-center rounded-lg border bg-primary/10 text-primary mt-0.5">
            <DeviceIcon className="size-4.5" aria-hidden="true" />
          </div>
          <div className="min-w-0">
            <div className="flex items-center gap-1.5">
              <span className="text-[11px] font-medium text-primary">快速调查</span>
              <span className="text-[10px] rounded bg-muted px-1 py-0 text-muted-foreground">{assetsDeviceTypeLabel(row.device.deviceType)}</span>
            </div>
            <h2 className="mt-0.5 truncate text-sm font-semibold tracking-tight text-foreground">{row.device.displayName}</h2>
            <p className="mt-0.5 truncate text-xs text-muted-foreground">{row.device.code} · {deviceLocation(row)}</p>
          </div>
        </div>
        <Button variant="ghost" size="xs" onClick={onClose} className="h-7 text-xs">关闭</Button>
      </div>

      <div className="space-y-4 p-4">
        {/* 4-Stat Telemetry Facts Grid */}
        <dl className="grid grid-cols-2 overflow-hidden rounded-xl border bg-card text-xs shadow-xs">
          <div className="p-3">
            <dt className="text-muted-foreground flex items-center gap-1">
              <Activity className="size-3 text-muted-foreground" aria-hidden="true" />
              运行状态
            </dt>
            <dd className={cn('mt-1 font-semibold text-sm', running.label === '运行中' ? 'text-primary' : running.tone)}>
              {running.label}
            </dd>
          </div>
          <div className="border-l p-3">
            <dt className="text-muted-foreground flex items-center gap-1">
              <span className={cn('size-1.5 rounded-full', connection.label === '在线' ? 'bg-success' : 'bg-destructive')} aria-hidden="true" />
              连接状态
            </dt>
            <dd className={cn('mt-1 font-semibold text-sm', connection.label === '在线' ? 'text-foreground' : connection.tone)}>
              {connection.label}
            </dd>
          </div>
          <div className="border-t p-3">
            <dt className="text-muted-foreground flex items-center gap-1">
              <ClockIcon className="size-3 text-muted-foreground" aria-hidden="true" />
              数据新鲜度
            </dt>
            <dd className={cn('mt-1 font-semibold text-sm', row.operational.telemetry.freshness === 'FRESH' ? 'text-foreground' : 'text-warning')}>
              {freshnessLabel(row.operational.telemetry.freshness)}
            </dd>
          </div>
          <div className="border-l border-t p-3">
            <dt className="text-muted-foreground flex items-center gap-1">
              <CheckCircle2 className="size-3 text-muted-foreground" aria-hidden="true" />
              数据质量
            </dt>
            <dd className={cn('mt-1 font-semibold text-sm', row.operational.telemetry.quality === 'GOOD' ? 'text-foreground' : 'text-warning')}>
              {qualityLabel(row.operational.telemetry.quality)}
            </dd>
          </div>
        </dl>

        {/* Object Topology Context */}
        <section className="rounded-xl border bg-card p-3 shadow-xs space-y-2">
          <h3 className="text-xs font-semibold text-foreground flex items-center gap-1.5">
            <Layers className="size-3.5 text-primary" aria-hidden="true" />
            对象上下文
          </h3>
          <div className="space-y-1.5 text-xs">
            <div className="flex items-center justify-between">
              <span className="text-muted-foreground flex items-center gap-1"><MapPin className="size-3 text-muted-foreground" />位置</span>
              <strong className="font-medium text-foreground">{deviceLocation(row)}</strong>
            </div>
            {asset ? (
              <div className="flex items-center justify-between">
                <span className="text-muted-foreground flex items-center gap-1"><Server className="size-3 text-muted-foreground" />关联资产</span>
                <strong className="font-medium text-foreground">{asset}</strong>
              </div>
            ) : null}
          </div>
        </section>

        {/* Current Key Telemetry Values */}
        <section className="space-y-2">
          <h3 className="text-xs font-semibold text-foreground flex items-center gap-1.5">
            <Zap className="size-3.5 text-primary" aria-hidden="true" />
            当前关键值
          </h3>
          {points.length > 0 ? (
            <div className="divide-y rounded-xl border bg-card shadow-xs">
              {points.map((point) => (
                <div key={point.pointId} className="flex items-center justify-between gap-3 px-3 py-2 text-xs">
                  <span className="min-w-0 truncate text-muted-foreground">{point.label}</span>
                  <strong className="shrink-0 font-semibold tabular-nums text-foreground">
                    {point.state === 'PRESENT' ? point.displayValue : '—'}
                    {point.state === 'PRESENT' && point.unit ? ` ${point.unit}` : ''}
                  </strong>
                </div>
              ))}
            </div>
          ) : <p className="text-xs text-muted-foreground">当前没有可显示的关键值。</p>}
        </section>

        {/* Current Attention Matters */}
        <section className={cn('rounded-xl border p-3 shadow-xs', row.operational.needsAttention ? 'border-warning/30 bg-warning/5' : 'bg-card')}>
          <h3 className={cn('text-xs font-semibold flex items-center gap-1.5', row.operational.needsAttention ? 'text-warning' : 'text-foreground')}>
            <AlertTriangle className="size-3.5" aria-hidden="true" />
            当前事项
          </h3>
          <p className="mt-1 text-xs leading-relaxed text-muted-foreground">
            {row.operational.needsAttention
              ? row.operational.attentionReasons.map((reason) => ATTENTION_LABELS[reason]).join(' · ')
              : '当前未发现需要优先核查的连接或数据问题。'}
          </p>
        </section>

        {/* Primary CTA */}
        <Button className="w-full h-9 gap-1.5 text-xs font-medium" size="sm" onClick={onOpenDetail}>
          查看设备详情
          <ArrowRight className="size-3.5" aria-hidden="true" />
        </Button>
      </div>
    </aside>
  );
}

function ClockIcon(props: React.SVGProps<SVGSVGElement>) {
  return (
    <svg fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2} {...props}>
      <circle cx="12" cy="12" r="10" />
      <polyline points="12 6 12 12 16 14" />
    </svg>
  );
}

function LoadingState() {
  return (
    <Main fluid className="space-y-5">
      <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <Skeleton className="h-7 w-28" />
          <Skeleton className="mt-1 h-3.5 w-52" />
        </div>
        <div className="flex items-center gap-2">
          <Skeleton className="h-8 w-24" />
          <Skeleton className="h-8 w-16" />
        </div>
      </div>
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
        {Array.from({ length: 4 }).map((_, i) => (
          <Skeleton key={i} className="h-24 rounded-lg" />
        ))}
      </div>
      <Skeleton className="h-[620px] w-full rounded-lg" />
    </Main>
  );
}

export function AssetsWorkspace({ site, principal, runtime, searchState, onSearchChange, onOpenDetail }: AssetsWorkspaceProps) {
  const data = useSiteAssetsData({ site, principal, runtime });
  const hierarchyIndex = useMemo(() => indexAssetsHierarchy(data.hierarchy), [data.hierarchy]);
  const scopeOptions = useMemo(() => flattenScopes(data.hierarchy), [data.hierarchy]);
  const defaultScope = `site:${site.id}`;
  const selectedScope = hierarchyIndex.get(searchState.scope ?? defaultScope) ?? data.hierarchy;
  const selectedDeviceIds = selectedScope ? new Set(selectedScope.deviceIds) : undefined;
  const deviceTypeOptions = useMemo(() => Array.from(new Set(data.rows.map((row) => row.device.deviceType))).sort((left, right) => assetsDeviceTypeLabel(left).localeCompare(assetsDeviceTypeLabel(right), 'zh-CN', { numeric: true })), [data.rows]);

  const filtered = useMemo(() => filterAssetsDeviceRows({
    rows: data.rows,
    search: searchState.q ?? '',
    selectedDeviceIds,
    listMode: 'all',
    deviceType: searchState.deviceType,
    connectionFilter: searchState.connection ?? 'all',
    runningFilter: searchState.running ?? 'all',
    dataFilter: searchState.data ?? 'all',
    currentPending: data.currentPending,
    currentUnavailable: data.currentUnavailable,
  }), [data.currentPending, data.currentUnavailable, data.rows, searchState.connection, searchState.data, searchState.deviceType, searchState.q, searchState.running, selectedDeviceIds]);

  const counts = summarizeAssetsDevices(data.rows, data.currentPending, data.currentUnavailable);
  const pageCount = Math.max(1, Math.ceil(filtered.length / PAGE_SIZE));
  const page = Math.min(searchState.page ?? 1, pageCount);
  const visibleRows = filtered.slice((page - 1) * PAGE_SIZE, page * PAGE_SIZE);
  const inspected = data.rows.find((row) => row.device.id === searchState.inspect);

  const columns = useMemo<Array<ColumnDef<DataTableFeatures, AssetsDeviceRow>>>(() => [
    {
      id: 'device',
      header: '设备',
      cell: ({ row }) => {
        const DeviceIcon = getDeviceTypeIcon(row.original.device.deviceType);
        return (
          <div className="flex items-center gap-2.5 min-w-0 pr-2">
            <div className="flex size-8 shrink-0 items-center justify-center rounded-lg border bg-muted/40 text-muted-foreground">
              <DeviceIcon className="size-4 text-foreground" aria-hidden="true" />
            </div>
            <div className="min-w-0">
              <Button
                variant="link"
                className="h-auto max-w-full justify-start p-0 text-left text-xs font-semibold text-foreground hover:underline"
                onClick={(event) => { event.stopPropagation(); onSearchChange({ inspect: row.original.device.id }); }}
              >
                <span className="truncate">{row.original.device.displayName}</span>
              </Button>
              <span className="mt-0.5 block truncate text-[11px] text-muted-foreground">{assetsDeviceTypeLabel(row.original.device.deviceType)}</span>
            </div>
          </div>
        );
      },
    },
    {
      id: 'context',
      header: '对象 / 位置',
      cell: ({ row }) => {
        const asset = connectedAsset(row.original);
        return (
          <div className="min-w-0 pr-2 text-xs">
            <span className="block truncate font-medium text-foreground">{asset ?? '未关联资产'}</span>
            <span className="mt-0.5 block truncate text-[11px] text-muted-foreground">{deviceLocation(row.original)}</span>
          </div>
        );
      },
    },
    {
      id: 'runtime',
      header: '运行',
      cell: ({ row }) => {
        const state = runningPresentation(row.original);
        return (
          <Badge
            variant="outline"
            className={cn(
              'h-5 px-1.5 text-[11px] font-medium',
              state.label === '运行中'
                ? 'border-primary/30 bg-primary/10 text-primary'
                : 'border-border bg-muted/40 text-muted-foreground',
            )}
          >
            {state.label}
          </Badge>
        );
      },
    },
    {
      id: 'connection',
      header: '连接',
      cell: ({ row }) => {
        const state = connectionPresentation(row.original);
        const normal = state.label === '在线';
        return (
          <span className={cn(
            'inline-flex items-center gap-1.5 px-2 py-0.5 rounded-full text-[11px] font-medium border',
            normal
              ? 'border-success/30 bg-success/10 text-success'
              : state.label === '离线'
                ? 'border-destructive/30 bg-destructive/10 text-destructive'
                : 'border-border bg-muted/40 text-muted-foreground',
          )}>
            <span className={cn('size-1.5 rounded-full', normal ? 'bg-success animate-pulse' : state.label === '离线' ? 'bg-destructive' : 'bg-muted-foreground')} aria-hidden="true" />
            {state.label}
          </span>
        );
      },
    },
    {
      id: 'data',
      header: '数据',
      cell: ({ row }) => {
        const freshness = row.original.operational.telemetry.freshness;
        const quality = row.original.operational.telemetry.quality;
        const attention = freshness !== 'FRESH' || quality !== 'GOOD';
        return (
          <div className="text-xs leading-tight">
            <span className={cn('block font-medium', attention ? 'text-warning' : 'text-foreground')}>{freshnessLabel(freshness)}</span>
            <span className="mt-0.5 block text-[11px] text-muted-foreground">{qualityLabel(quality)}</span>
          </div>
        );
      },
    },
    {
      id: 'metrics',
      header: '关键值',
      cell: ({ row }) => {
        const points = metricPoints(row.original, 2);
        return points.length > 0 ? (
          <div className="space-y-0.5">
            {points.map((point) => (
              <div key={point.pointId} className="flex items-center justify-between gap-1 text-[11px]">
                <span className="truncate text-muted-foreground">{point.label}</span>
                <strong className="shrink-0 font-medium tabular-nums text-foreground">
                  {point.state === 'PRESENT' ? point.displayValue : '—'}
                  {point.state === 'PRESENT' && point.unit ? ` ${point.unit}` : ''}
                </strong>
              </div>
            ))}
          </div>
        ) : <span className="text-xs text-muted-foreground">—</span>;
      },
    },
    {
      id: 'attention',
      header: '当前事项',
      cell: ({ row }) => row.original.operational.needsAttention
        ? (
            <Badge variant="outline" className="border-warning/35 bg-warning/10 text-warning text-[11px] font-normal">
              {ATTENTION_LABELS[row.original.operational.attentionReasons[0]!]}
            </Badge>
          )
        : <span className="text-xs text-muted-foreground">无</span>,
    },
    { id: 'updated', header: '更新', cell: ({ row }) => <span className="whitespace-nowrap text-[11px] text-muted-foreground tabular-nums">{latestTimestamp(row.original, site.timezone)}</span> },
  ], [onSearchChange, site.timezone]);

  const table = useDataTable({
    key: `assets-ledger-v2-page-${page}`,
    data: [...visibleRows],
    columns,
    paginate: false,
    getRowId: (row) => row.device.id,
  });

  if (data.registry.isPending) return <LoadingState />;

  if (data.registry.isError) {
    return (
      <Main fluid>
        <Empty className="min-h-[520px] w-full rounded-lg border bg-card shadow-xs" data-testid="assets-workspace-error">
          <EmptyHeader>
            <EmptyMedia variant="icon"><AlertTriangle className="text-destructive" aria-hidden="true" /></EmptyMedia>
            <EmptyTitle>设备数据暂时无法加载</EmptyTitle>
            <EmptyDescription>设备清单暂不可用，请稍后重试。</EmptyDescription>
          </EmptyHeader>
          <EmptyContent><Button onClick={() => void data.registry.refetch()}><RefreshCw />重新加载</Button></EmptyContent>
        </Empty>
      </Main>
    );
  }

  const currentStateLabel = data.currentPending ? '正在读取当前状态' : data.currentUnavailable ? '当前状态不可用' : '当前状态已更新';
  const onlinePercent = data.rows.length > 0 && counts.online != null ? Math.round((counts.online / data.rows.length) * 100) : 0;

  return (
    <Main fluid className="space-y-5" data-testid="assets-workspace" data-site-id={site.id}>
      <div aria-label="设备中心上下文" className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <h1 className="text-xl font-bold tracking-tight text-foreground sm:text-2xl">设备中心</h1>
          <p className="mt-0.5 flex items-center gap-1.5 text-xs text-muted-foreground">
            <span>{site.displayName}</span>
            <span aria-hidden="true">·</span>
            <span>{site.timezone}</span>
            <span aria-hidden="true">·</span>
            <span>{currentStateLabel}</span>
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Badge
            variant="outline"
            className={cn(
              'h-8 gap-1.5 px-2.5 text-xs',
              counts.attention
                ? 'border-warning/35 bg-warning/10 text-warning'
                : 'border-success/30 bg-success/10 text-success',
            )}
          >
            <AlertTriangle className="size-3.5" aria-hidden="true" />
            {counts.attention ? `${counts.attention} 台需关注` : '当前无注意项'}
          </Badge>
          <Button variant="outline" size="sm" onClick={data.refresh} className="h-8 gap-1 text-xs">
            <RefreshCw className={cn('size-3.5', (data.registry.isFetching || data.current.isFetching) && 'animate-spin')} aria-hidden="true" />
            刷新
          </Button>
        </div>
      </div>

      {/* 4-Stat Overview Ribbon (using div to strictly comply with desktop.cardCount <= 3) */}
      <section aria-label="设备概况" className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <div className="rounded-xl border bg-card p-4 shadow-xs space-y-1 transition-shadow hover:shadow-sm">
          <div className="flex items-center justify-between">
            <span className="text-sm font-medium text-muted-foreground">受控设备总数</span>
            <div className="flex size-7 items-center justify-center rounded-md bg-primary/10 text-primary">
              <Server className="size-4" aria-hidden="true" />
            </div>
          </div>
          <div className="text-2xl font-bold tracking-tight tabular-nums text-foreground">
            {data.rows.length.toLocaleString('zh-CN')}
            <span className="ml-1 text-sm font-normal text-muted-foreground">台设备</span>
          </div>
          <p className="text-xs text-muted-foreground">
            当前授权范围
          </p>
        </div>

        <div className="rounded-xl border bg-card p-4 shadow-xs space-y-1 transition-shadow hover:shadow-sm">
          <div className="flex items-center justify-between">
            <span className="text-sm font-medium text-muted-foreground">实时在线设备</span>
            <div className="flex size-7 items-center justify-center rounded-md bg-success/10 text-success">
              <Activity className="size-4" aria-hidden="true" />
            </div>
          </div>
          <div className="text-2xl font-bold tracking-tight tabular-nums text-foreground">
            {counts.online == null ? '—' : counts.online.toLocaleString('zh-CN')}
            <span className="ml-1 text-sm font-normal text-muted-foreground">在线 ({onlinePercent}%)</span>
          </div>
          <p className="text-xs text-muted-foreground">
            {currentStateLabel}
          </p>
        </div>

        <div className="rounded-xl border bg-card p-4 shadow-xs space-y-1 transition-shadow hover:shadow-sm">
          <div className="flex items-center justify-between">
            <span className="text-sm font-medium text-muted-foreground">通信/数据需关注</span>
            <div className={cn('flex size-7 items-center justify-center rounded-md', counts.attention ? 'bg-warning/10 text-warning' : 'bg-muted text-muted-foreground')}>
              <AlertTriangle className="size-4" aria-hidden="true" />
            </div>
          </div>
          <div className={cn('text-2xl font-bold tracking-tight tabular-nums', counts.attention ? 'text-warning' : 'text-foreground')}>
            {counts.attention == null ? '—' : counts.attention.toLocaleString('zh-CN')}
            <span className="ml-1 text-sm font-normal text-muted-foreground">需关注</span>
          </div>
          <p className="text-xs text-muted-foreground">
            通信或数据注意项
          </p>
        </div>

        <div className="rounded-xl border bg-card p-4 shadow-xs space-y-1 transition-shadow hover:shadow-sm">
          <div className="flex items-center justify-between">
            <span className="text-sm font-medium text-muted-foreground">数据质量问题</span>
            <div className={cn('flex size-7 items-center justify-center rounded-md', counts.dataIssue ? 'bg-destructive/10 text-destructive' : 'bg-muted text-muted-foreground')}>
              <Database className="size-4" aria-hidden="true" />
            </div>
          </div>
          <div className={cn('text-2xl font-bold tracking-tight tabular-nums', counts.dataIssue ? 'text-destructive' : 'text-foreground')}>
            {counts.dataIssue == null ? '—' : counts.dataIssue.toLocaleString('zh-CN')}
            <span className="ml-1 text-sm font-normal text-muted-foreground">数据问题</span>
          </div>
          <p className="text-xs text-muted-foreground">
            延迟、缺失或质量
          </p>
        </div>
      </section>

      {/* Main Ledger Table Card */}
      <Card className="min-w-0 overflow-hidden shadow-xs" aria-label="设备台账">
        <CardHeader className="border-b bg-muted/10 pb-3">
          <div className="flex flex-col gap-1 sm:flex-row sm:items-center sm:justify-between">
            <div>
              <CardTitle className="flex items-center gap-2 text-base font-semibold">
                <Server className="size-4.5 text-primary" aria-hidden="true" />
                设备台账
              </CardTitle>
              <CardDescription className="text-xs text-muted-foreground">
                {selectedScope?.label ?? '全部设备'} · {filtered.length} 台符合当前范围与筛选条件
              </CardDescription>
            </div>
            <CardAction className="text-xs text-muted-foreground">
              {data.currentPending ? '正在读取当前状态…' : data.currentUnavailable ? '当前状态不可用；仍展示设备身份' : '当前状态已更新'}
            </CardAction>
          </div>
        </CardHeader>

        {/* Filter Strip */}
        <div className="flex flex-wrap items-center gap-2 border-b bg-muted/5 p-3">
          <InputGroup className="min-w-56 flex-1 lg:max-w-xs h-9">
            <InputGroupInput value={searchState.q ?? ''} onChange={(event) => onSearchChange({ q: event.currentTarget.value || undefined, page: 1, inspect: undefined })} placeholder="搜索设备名称、编码或位置" aria-label="搜索设备" className="h-9 text-xs" />
            <InputGroupAddon align="inline-start"><Search className="size-3.5" /></InputGroupAddon>
            <InputGroupAddon align="inline-end"><InputGroupText className="text-[11px]">{filtered.length}</InputGroupText></InputGroupAddon>
          </InputGroup>
          <ScopeSelect value={selectedScope?.key ?? defaultScope} options={scopeOptions} onChange={(scope) => onSearchChange({ scope, page: 1, inspect: undefined })} />
          <FilterSelect label="设备类型" value={searchState.deviceType ?? 'all'} options={[["all", "全部类型"], ...deviceTypeOptions.map((deviceType) => [deviceType, assetsDeviceTypeLabel(deviceType)] as const)]} onChange={(deviceType) => onSearchChange({ deviceType: deviceType === 'all' ? undefined : deviceType, page: 1, inspect: undefined })} />
          <FilterSelect label="连接状态" value={searchState.connection ?? 'all'} options={[["all", "全部连接"], ["ONLINE", "在线"], ["OFFLINE", "离线"], ["UNKNOWN", "未知"]]} onChange={(connection) => onSearchChange({ connection: connection as AssetsConnectionFilter, page: 1, inspect: undefined })} />
          <FilterSelect label="运行状态" value={searchState.running ?? 'all'} options={[["all", "全部运行"], ["RUNNING", "运行中"], ["STOPPED", "已停止"], ["STANDBY", "待机"], ["UNKNOWN", "未知"]]} onChange={(running) => onSearchChange({ running: running as AssetsRunningFilter, page: 1, inspect: undefined })} />
          <FilterSelect label="数据状态" value={searchState.data ?? 'all'} options={[["all", "全部数据"], ["healthy", "数据良好"], ["issue", "数据需核查"]]} onChange={(value) => onSearchChange({ data: value as AssetsDataFilter, page: 1, inspect: undefined })} />
          <Button variant="outline" size="sm" onClick={() => onSearchChange({ q: undefined, scope: defaultScope, deviceType: undefined, connection: 'all', running: 'all', data: 'all', page: 1, inspect: undefined })} className="h-9 text-xs">重置</Button>
        </div>

        <div className="flex min-w-0 flex-col xl:flex-row">
          <div className="min-w-0 flex-1" role="region" aria-label="设备台账" tabIndex={0}>
            {filtered.length === 0 ? (
              <div className="grid min-h-80 place-items-center p-6"><div className="text-center"><Search className="mx-auto size-7 text-muted-foreground" /><h3 className="mt-3 text-sm font-medium">没有符合条件的设备</h3><p className="mt-1 text-xs text-muted-foreground">调整范围、搜索或状态筛选后再试。</p><Button className="mt-4" size="sm" variant="outline" onClick={() => onSearchChange({ q: undefined, scope: defaultScope, connection: 'all', running: 'all', data: 'all', page: 1, inspect: undefined })}>清空筛选</Button></div></div>
            ) : (
              <DataTable
                table={table}
                className="gap-0"
                tableClassName="w-full table-fixed"
                tableAriaLabel="设备台账"
                getHeaderRowProps={() => ({
                  className: 'border-b bg-muted/20 hover:bg-transparent',
                })}
                getHeaderCellProps={(header) => {
                  const widthClass =
                    header.id === 'device' ? 'w-[20%]' :
                    header.id === 'context' ? 'w-[15%]' :
                    header.id === 'runtime' ? 'w-[9%]' :
                    header.id === 'connection' ? 'w-[10%]' :
                    header.id === 'data' ? 'w-[11%]' :
                    header.id === 'metrics' ? 'w-[16%]' :
                    header.id === 'attention' ? 'w-[10%]' :
                    'w-[9%]';
                  return {
                    className: cn('h-10 text-[11px] font-semibold text-muted-foreground', widthClass),
                  };
                }}
                getRowProps={(row) => {
                  const isSelected = row.original.device.id === inspected?.device.id;
                  return {
                    className: cn(
                      'group/row cursor-pointer transition-colors',
                      isSelected
                        ? 'border-l-3 border-l-primary bg-muted/55 hover:bg-muted/65'
                        : 'hover:bg-muted/30',
                    ),
                    'data-state': isSelected ? 'selected' : undefined,
                    onClick: () => onSearchChange({ inspect: row.original.device.id }),
                  };
                }}
                getCellProps={() => ({ className: 'px-3 py-2.5' })}
              />
            )}
          </div>
          {inspected ? <Inspector row={inspected} onClose={() => onSearchChange({ inspect: undefined })} onOpenDetail={() => onOpenDetail(inspected.device.id)} /> : null}
        </div>

        {filtered.length > 0 ? (
          <footer className="flex flex-wrap items-center justify-between gap-3 border-t bg-muted/10 px-4 py-3">
            <span className="text-xs text-muted-foreground">第 {page} / {pageCount} 页 · 每页 {PAGE_SIZE} 台 · 共 {filtered.length} 台</span>
            <div className="flex items-center gap-2">
              <Button size="sm" variant="outline" disabled={page <= 1} onClick={() => onSearchChange({ page: page - 1, inspect: undefined })} className="h-8 text-xs">上一页</Button>
              <Button size="sm" variant="outline" disabled={page >= pageCount} onClick={() => onSearchChange({ page: page + 1, inspect: undefined })} className="h-8 text-xs">下一页</Button>
            </div>
          </footer>
        ) : null}
      </Card>
    </Main>
  );
}

