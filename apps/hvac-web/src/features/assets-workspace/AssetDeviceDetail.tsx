import { useMemo, useState } from 'react';
import { Link } from '@tanstack/react-router';
import type { ColumnDef } from '@tanstack/react-table';
import { parseAsStringEnum, useQueryState } from 'nuqs';
import {
  Activity,
  AlertTriangle,
  ArrowLeft,
  ArrowRight,
  Boxes,
  Building2,
  Calendar,
  CheckCircle2,
  Clock,
  Cpu,
  ExternalLink,
  Flame,
  Gauge,
  Layers,
  MapPin,
  Radio,
  RefreshCw,
  Search,
  Server,
  ShieldCheck,
  SlidersHorizontal,
  Snowflake,
  TrendingUp,
  Zap,
} from 'lucide-react';
import { DataTableBlock } from '@/blocks/data-table';
import type { CurrentPrincipalResponse, Site, TelemetryPoint } from '@/api/generated/platformGateway.gen';
import type { HvacRouterContext } from '@/app/router-context';
import { Main } from '@/components/layout/Main';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';

import {
  DataTable,
  DataTableAdvancedToolbar,
  DataTableFilterList,
  DataTablePagination,
  DataTableSortList,
  type DataTableFeatures,
} from '@/components/data-table';
import { StatusBadge } from '@/components/status-badge';
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyMedia } from '@/components/ui/empty';
import { InputGroup, InputGroupAddon, InputGroupInput } from '@/components/ui/input-group';
import { Skeleton } from '@/components/ui/skeleton';
import { assetsDeviceTypeLabel, type AssetsDeviceRow, type AssetsPointView } from '@/features/assets/model';
import { useDataTable } from '@/hooks/use-data-table';
import type { ExtendedColumnFilter, JoinOperator } from '@/lib/data-table-types';
import { getFiltersStateParser } from '@/lib/parsers';
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

interface AssetDeviceDetailProps {
  readonly site: Readonly<Site>;
  readonly principal: CurrentPrincipalResponse;
  readonly runtime: HvacRouterContext['runtime'];
  readonly deviceId: string;
  readonly onBack: () => void;
}

interface EngineeringPointRow {
  readonly point: TelemetryPoint;
  readonly current: AssetsPointView | null;
  readonly pointType: TelemetryPoint['pointType'];
  readonly capability: 'writable' | 'readonly';
  readonly qualityStatus: string;
}

const ENGINEERING_CAPABILITY_OPTIONS = [
  { label: '可写', value: 'writable' },
  { label: '只读', value: 'readonly' },
] as const;
const ENGINEERING_QUALITY_OPTIONS = [
  { label: '良好', value: 'GOOD' },
  { label: '部分有效', value: 'PARTIAL' },
  { label: '估算', value: 'ESTIMATED' },
  { label: '人工', value: 'MANUAL' },
  { label: '延迟', value: 'STALE' },
  { label: '无效', value: 'INVALID' },
  { label: '无当前值', value: 'MISSING' },
] as const;
const ENGINEERING_FILTER_COLUMN_IDS = ['pointType', 'capability', 'qualityStatus'] as const;
const ENGINEERING_FILTERS_QUERY_KEY = 'engineeringPointFilters';
const ENGINEERING_JOIN_OPERATOR_QUERY_KEY = 'engineeringPointJoinOperator';

const POINT_TYPE_LABELS: Readonly<Record<TelemetryPoint['pointType'], string>> = Object.freeze({
  TELEMETRY: '遥测',
  COUNTER: '累计量',
  STATE: '状态',
  SETTING: '设定',
  COMMAND: '命令',
});

const ENGINEERING_POINT_TYPE_OPTIONS = Object.entries(POINT_TYPE_LABELS).map(([value, label]) => ({ value, label }));

const DEVICE_STATUS_LABELS: Readonly<Record<AssetsDeviceRow['device']['status'], string>> = Object.freeze({
  ACTIVE: '启用',
  INACTIVE: '停用',
  RETIRED: '退役',
});

function formatTimestamp(value: string | null | undefined, timezone: string): string {
  if (!value) return '—';
  return new Intl.DateTimeFormat('zh-CN', {
    timeZone: timezone,
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  }).format(new Date(value));
}

function formatInterval(milliseconds: number): string {
  if (milliseconds >= 60_000 && milliseconds % 60_000 === 0) return `${milliseconds / 60_000} 分钟`;
  if (milliseconds >= 1000 && milliseconds % 1000 === 0) return `${milliseconds / 1000} 秒`;
  return `${milliseconds} ms`;
}

function pointQualityLabel(point: AssetsPointView | null): string {
  if (!point || point.state !== 'PRESENT') return '无当前值';
  if (point.quality === 'GOOD') return '良好';
  if (point.quality === 'PARTIAL') return '部分有效';
  if (point.quality === 'ESTIMATED') return '估算';
  if (point.quality === 'MANUAL') return '人工';
  if (point.quality === 'STALE') return '延迟';
  if (point.quality === 'INVALID') return '无效';
  return '无质量结论';
}

function matchesEngineeringAdvancedFilter(
  row: EngineeringPointRow,
  filter: ExtendedColumnFilter<EngineeringPointRow>,
) {
  const actual = filter.id === 'pointType'
    ? row.pointType
    : filter.id === 'capability'
      ? row.capability
      : filter.id === 'qualityStatus'
        ? row.qualityStatus
        : '';
  if (!actual) return true;
  const values = Array.isArray(filter.value) ? filter.value : [filter.value];

  switch (filter.operator) {
    case 'eq':
    case 'inArray':
      return values.includes(actual);
    case 'ne':
    case 'notInArray':
      return !values.includes(actual);
    case 'isEmpty':
      return actual.length === 0;
    case 'isNotEmpty':
      return actual.length > 0;
    default:
      return false;
  }
}

function getPointMetricIcon(label: string) {
  const norm = label.toLowerCase();
  if (norm.includes('功率') || norm.includes('power')) return Zap;
  if (norm.includes('cop') || norm.includes('能效')) return TrendingUp;
  if (norm.includes('冷') || norm.includes('cooling')) return Snowflake;
  if (norm.includes('热') || norm.includes('heat')) return Flame;
  if (norm.includes('温') || norm.includes('temp')) return Gauge;
  if (norm.includes('状态') || norm.includes('state')) return Activity;
  return Cpu;
}

function Loading() {
  return (
    <Main fluid className="space-y-5">
      <div className="flex justify-end gap-2"><Skeleton className="h-9 w-28" /><Skeleton className="h-9 w-32" /><Skeleton className="h-9 w-24" /></div>
      <Skeleton className="h-36 w-full rounded-lg" />
      <Skeleton className="h-24 w-full rounded-lg" />
      <div className="grid gap-4 xl:grid-cols-[minmax(0,1.5fr)_minmax(340px,.7fr)]"><Skeleton className="h-[480px] rounded-lg" /><Skeleton className="h-[360px] rounded-lg" /></div>
    </Main>
  );
}

function RecentEvidence({ row, site }: { readonly row: AssetsDeviceRow; readonly site: Readonly<Site> }) {
  const evidence = row.representativePoints
    .filter((point) => point.state === 'PRESENT' && point.sampledAt)
    .sort((left, right) => Date.parse(right.sampledAt ?? '') - Date.parse(left.sampledAt ?? ''))
    .slice(0, 3);

  return (
    <section id="evidence" className="scroll-mt-24 border-t pt-5" aria-labelledby="device-recent-evidence">
      <div className="flex items-center justify-between gap-4">
        <h2 id="device-recent-evidence" className="flex items-center gap-2 text-sm font-semibold">
          <Clock className="size-4 text-muted-foreground" aria-hidden="true" />
          最近证据
        </h2>
        <Button variant="ghost" size="xs" asChild className="gap-1 text-xs">
          <Link
            to="/sites/$siteId/trends"
            params={{ siteId: site.id }}
            search={{ series: evidence.length ? evidence.map((point) => point.pointId).join(',') : undefined }}
          >
            查看完整趋势 <ArrowRight className="size-3" />
          </Link>
        </Button>
      </div>
      {evidence.length > 0 ? (
        <div className="mt-3 divide-y border-y">
          {evidence.map((point) => {
            const MetricIcon = getPointMetricIcon(point.label);
            return (
              <div key={point.pointId} className="flex items-center justify-between gap-4 py-3">
                <div className="flex min-w-0 items-center gap-2.5">
                  <MetricIcon className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
                  <div className="min-w-0">
                    <strong className="block truncate text-sm font-medium text-foreground">{point.label}</strong>
                    <span className="mt-0.5 block text-xs text-muted-foreground">
                      {formatTimestamp(point.sampledAt, site.timezone)} · {pointQualityLabel(point)}
                    </span>
                  </div>
                </div>
                <strong className="shrink-0 text-sm font-semibold tabular-nums text-foreground">
                  {point.displayValue}{point.unit ? ` ${point.unit}` : ''}
                </strong>
              </div>
            );
          })}
        </div>
      ) : <div className="mt-3 border-y py-5 text-sm text-muted-foreground">当前没有可展示的最近证据。</div>}
    </section>
  );
}

function Relationships({ row, site }: { readonly row: AssetsDeviceRow; readonly site: Readonly<Site> }) {
  const assetNames = row.binding.state === 'bound'
    ? [row.binding.asset.displayName]
    : row.binding.state === 'multi-bound'
      ? row.binding.bindings.map((binding) => binding.asset.displayName)
      : [];
  const sensorCount = new Set(row.telemetryPoints.flatMap((point) => point.sensorId ? [point.sensorId] : [])).size;

  const relationshipRows = [
    { label: '站点', value: site.displayName, icon: Building2 },
    { label: '空间位置', value: deviceLocation(row), icon: MapPin },
    { label: '关联资产', value: assetNames.length ? assetNames.join('、') : '未关联', icon: Server },
    { label: '物理传感器', value: `${sensorCount} 个`, icon: Radio },
    { label: '注册点位', value: `${row.registeredPointCount} 个`, icon: Boxes },
  ];

  return (
    <section id="relationships" className="scroll-mt-24 border-t pt-5" aria-labelledby="device-relationships">
      <h2 id="device-relationships" className="flex items-center gap-2 text-sm font-semibold">
        <Layers className="size-4 text-muted-foreground" aria-hidden="true" />
        对象关系
      </h2>
      <dl className="mt-3 grid border-y text-sm md:grid-cols-2">
        {relationshipRows.map(({ label, value, icon: Icon }, index) => (
          <div
            key={label}
            className={cn(
              'flex min-w-0 items-center justify-between gap-4 py-3',
              index % 2 === 0 ? 'md:pr-6' : 'md:border-l md:pl-6',
              index >= 2 && 'border-t',
              index === relationshipRows.length - 1 && relationshipRows.length % 2 === 1 && 'md:col-span-2 md:border-l-0 md:pl-0',
            )}
          >
            <dt className="flex shrink-0 items-center gap-2 text-muted-foreground">
              <Icon className="size-4" aria-hidden="true" />
              {label}
            </dt>
            <dd className="min-w-0 truncate text-right font-medium text-foreground">{value}</dd>
          </div>
        ))}
      </dl>
    </section>
  );
}

function EngineeringPoints({ row }: { readonly row: AssetsDeviceRow }) {
  const [query, setQuery] = useState('');
  const [advancedFilters] = useQueryState(
    ENGINEERING_FILTERS_QUERY_KEY,
    getFiltersStateParser<EngineeringPointRow>([...ENGINEERING_FILTER_COLUMN_IDS]).withDefault([]),
  );
  const [joinOperator] = useQueryState(
    ENGINEERING_JOIN_OPERATOR_QUERY_KEY,
    parseAsStringEnum<JoinOperator>(['and', 'or']).withDefault('and'),
  );

  const rows = useMemo<EngineeringPointRow[]>(() => {
    const normalized = query.trim().toLocaleLowerCase('zh-CN');
    return row.telemetryPoints
      .map((point) => {
        const current = row.operational.points.find((candidate) => candidate.key === point.pointCode) ?? null;
        return {
          point,
          current,
          pointType: point.pointType,
          capability: point.writable ? 'writable' : 'readonly',
          qualityStatus: current?.state === 'PRESENT' ? (current.quality ?? 'MISSING') : 'MISSING',
        } satisfies EngineeringPointRow;
      })
      .filter((engineeringPoint) => {
        const matchesSearch = !normalized || `${engineeringPoint.point.displayName} ${engineeringPoint.point.pointCode}`.toLocaleLowerCase('zh-CN').includes(normalized);
        const filterMatches = advancedFilters.map((filter) => matchesEngineeringAdvancedFilter(engineeringPoint, filter));
        const matchesAdvancedFilters = advancedFilters.length === 0 || (joinOperator === 'or'
          ? filterMatches.some(Boolean)
          : filterMatches.every(Boolean));
        return matchesSearch && matchesAdvancedFilters;
      });
  }, [advancedFilters, joinOperator, query, row.operational.points, row.telemetryPoints]);

  const columns = useMemo<Array<ColumnDef<DataTableFeatures, EngineeringPointRow>>>(() => [
    {
      id: 'point',
      accessorFn: (engineeringPoint) => engineeringPoint.point.displayName,
      meta: { label: '点位' },
      header: '点位',
      cell: ({ row: tableRow }) => (
        <div className="min-w-0 pr-2">
          <strong className="block truncate text-xs font-semibold text-foreground">{tableRow.original.point.displayName}</strong>
          <span className="mt-0.5 block truncate font-mono text-[11px] text-muted-foreground">{tableRow.original.point.pointCode}</span>
        </div>
      ),
    },
    {
      id: 'pointType',
      accessorFn: (engineeringPoint) => engineeringPoint.pointType,
      enableColumnFilter: true,
      meta: { label: '点位类型', variant: 'select', options: ENGINEERING_POINT_TYPE_OPTIONS },
      header: '类型',
      cell: ({ row: tableRow }) => (
        <StatusBadge tone="neutral">
          {POINT_TYPE_LABELS[tableRow.original.point.pointType]}
        </StatusBadge>
      ),
    },
    {
      id: 'current',
      header: '当前值',
      cell: ({ row: tableRow }) => {
        const current = tableRow.original.current;
        return (
          <span className="whitespace-nowrap font-mono text-xs font-semibold tabular-nums text-foreground">
            {current?.state === 'PRESENT' ? `${current.displayValue}${current.unit ? ` ${current.unit}` : ''}` : '—'}
          </span>
        );
      },
    },
    {
      id: 'qualityStatus',
      accessorFn: (engineeringPoint) => engineeringPoint.qualityStatus,
      enableColumnFilter: true,
      meta: { label: '质量', variant: 'select', options: [...ENGINEERING_QUALITY_OPTIONS] },
      header: '质量',
      cell: ({ row: tableRow }) => (
        <span className="text-xs text-muted-foreground">{pointQualityLabel(tableRow.original.current)}</span>
      ),
    },
    {
      id: 'sample',
      accessorFn: (engineeringPoint) => engineeringPoint.point.sampleIntervalMs,
      meta: { label: '采样周期' },
      header: '采样周期',
      cell: ({ row: tableRow }) => (
        <span className="whitespace-nowrap font-mono text-xs text-muted-foreground tabular-nums">{formatInterval(tableRow.original.point.sampleIntervalMs)}</span>
      ),
    },
    {
      id: 'capability',
      accessorFn: (engineeringPoint) => engineeringPoint.capability,
      enableColumnFilter: true,
      meta: { label: '源端能力', variant: 'select', options: [...ENGINEERING_CAPABILITY_OPTIONS] },
      header: '源端能力',
      cell: ({ row: tableRow }) => (
        <StatusBadge tone={tableRow.original.point.writable ? 'warning' : 'neutral'}>
          {tableRow.original.point.writable ? '可写' : '只读'}
        </StatusBadge>
      ),
    },
  ], []);

  const table = useDataTable({
    key: 'device-engineering-points',
    data: rows,
    columns,
    pageSize: 10,
    getRowId: (engineeringPoint) => engineeringPoint.point.id,
    meta: {
      queryKeys: {
        page: 'engineeringPointPage',
        perPage: 'engineeringPointPerPage',
        sort: 'engineeringPointSort',
        filters: ENGINEERING_FILTERS_QUERY_KEY,
        joinOperator: ENGINEERING_JOIN_OPERATOR_QUERY_KEY,
      },
    },
  });


  return (
    <section id="points" className="scroll-mt-24 border-t pt-5" aria-label="工程点位">
      <DataTableBlock
        title={<span className="flex items-center gap-2"><Cpu className="size-4 text-muted-foreground" aria-hidden="true" />工程点位</span>}
        description="已登记点位及当前观测；源端可写能力不等于当前控制权限。"
      >
        <DataTable
          table={table}
          role="region"
          aria-label="工程点位表格，可横向滚动"
          tabIndex={0}
          tableAriaLabel="工程点位"
          empty="没有符合搜索条件的点位。"
          getHeaderRowProps={() => ({
            className: 'bg-muted/20',
          })}
          getHeaderCellProps={() => ({
            className: 'whitespace-nowrap',
          })}
          getRowProps={() => ({ className: 'hover:bg-muted/30' })}
          getCellProps={() => ({ className: 'py-2.5' })}
          footer={<DataTablePagination table={table} totalRows={rows.length} />}
        >
          <DataTableAdvancedToolbar table={table}>
            <InputGroup className="h-8 max-w-sm">
              <InputGroupInput value={query} onChange={(event) => { setQuery(event.currentTarget.value); table.setPageIndex(0); }} placeholder="搜索点位名称或编码" aria-label="搜索工程点位" className="h-8 text-xs" />
              <InputGroupAddon align="inline-start"><Search className="size-3.5" /></InputGroupAddon>
            </InputGroup>
            <DataTableSortList table={table} />
            <DataTableFilterList table={table} />
          </DataTableAdvancedToolbar>
        </DataTable>
      </DataTableBlock>
    </section>
  );
}

function DeviceMetadata({ row, timezone }: { readonly row: AssetsDeviceRow; readonly timezone: string }) {
  return (
    <section id="metadata" className="scroll-mt-24 border-t pt-5" aria-labelledby="device-metadata">
      <details>
        <summary className="flex cursor-pointer list-none items-center justify-between gap-4 py-1">
          <h2 id="device-metadata" className="flex items-center gap-2 text-sm font-semibold">
            <ShieldCheck className="size-4 text-muted-foreground" aria-hidden="true" />
            设备资料与来源
          </h2>
          <span className="text-xs text-muted-foreground">展开</span>
        </summary>
        <dl className="mt-3 grid border-y text-sm md:grid-cols-2">
          <div className="flex items-center justify-between gap-4 py-3 md:pr-6">
            <dt className="text-muted-foreground">设备编码</dt>
            <dd className="font-mono font-medium text-foreground">{row.device.code}</dd>
          </div>
          <div className="flex items-center justify-between gap-4 border-t py-3 md:border-l md:border-t-0 md:pl-6">
            <dt className="text-muted-foreground">生命周期</dt>
            <dd><StatusBadge tone="success">{DEVICE_STATUS_LABELS[row.device.status]}</StatusBadge></dd>
          </div>
          <div className="flex items-center justify-between gap-4 border-t py-3 md:pr-6">
            <dt className="flex items-center gap-1.5 text-muted-foreground"><Calendar className="size-3.5" />登记时间</dt>
            <dd className="font-medium tabular-nums text-foreground">{formatTimestamp(row.device.createdAt, timezone)}</dd>
          </div>
          <div className="flex items-center justify-between gap-4 border-t py-3 md:border-l md:pl-6">
            <dt className="flex items-center gap-1.5 text-muted-foreground"><Clock className="size-3.5" />资料更新</dt>
            <dd className="font-medium tabular-nums text-foreground">{formatTimestamp(row.device.updatedAt, timezone)}</dd>
          </div>
        </dl>
      </details>
    </section>
  );
}

function ProfessionalExits({ row, site }: { readonly row: AssetsDeviceRow; readonly site: Readonly<Site> }) {
  const series = metricPoints(row, 4).map((point) => point.pointId).join(',');
  return (
    <section className="border-t pt-5" aria-labelledby="device-professional-exits">
      <h2 id="device-professional-exits" className="flex items-center gap-2 text-sm font-semibold">
        <ExternalLink className="size-4 text-muted-foreground" aria-hidden="true" />
        继续调查
      </h2>
      <div className="mt-3 flex flex-wrap gap-2">
        <Button variant="outline" size="sm" asChild>
          <Link to="/sites/$siteId/trends" params={{ siteId: site.id }} search={{ series: series || undefined }}>
            <TrendingUp className="size-3.5" />趋势
          </Link>
        </Button>
        <Button variant="outline" size="sm" asChild>
          <Link to="/sites/$siteId/alarms" params={{ siteId: site.id }} search={{ view: 'active', source: 'device-detail', device: row.device.displayName, deviceId: row.device.id }}>
            <AlertTriangle className="size-3.5" />告警
          </Link>
        </Button>
        <Button variant="outline" size="sm" asChild>
          <Link to="/sites/$siteId/work-orders" params={{ siteId: site.id }} search={{ source: 'device-detail', device: row.device.id }}>
            <SlidersHorizontal className="size-3.5" />工单
          </Link>
        </Button>
        <Button variant="outline" size="sm" asChild>
          <Link to="/sites/$siteId/control" params={{ siteId: site.id }}>
            <Cpu className="size-3.5" />控制
          </Link>
        </Button>
      </div>
    </section>
  );
}

export function AssetDeviceDetail({ site, principal, runtime, deviceId, onBack }: AssetDeviceDetailProps) {
  const data = useSiteAssetsData({ site, principal, runtime });

  if (data.registry.isPending) return <Loading />;

  if (data.registry.isError) {
    return (
      <Main fluid>
        <Empty className="min-h-[520px] w-full rounded-lg border bg-card shadow-xs">
          <EmptyHeader>
            <EmptyMedia variant="icon"><AlertTriangle className="text-destructive" aria-hidden="true" /></EmptyMedia>
            <h1 className="text-xl font-semibold">设备详情暂时无法加载</h1>
            <EmptyDescription>设备身份信息暂不可用，请稍后重试。</EmptyDescription>
          </EmptyHeader>
          <EmptyContent><Button onClick={() => void data.registry.refetch()}><RefreshCw />重新加载</Button></EmptyContent>
        </Empty>
      </Main>
    );
  }

  const row = data.rows.find((candidate) => candidate.device.id === deviceId);
  if (!row) {
    return (
      <Main fluid>
        <Empty className="min-h-[520px] w-full rounded-lg border bg-card shadow-xs" data-testid="asset-device-not-visible">
          <EmptyHeader>
            <EmptyMedia variant="icon"><AlertTriangle className="text-warning" aria-hidden="true" /></EmptyMedia>
            <h1 className="text-xl font-semibold">设备不可见</h1>
            <EmptyDescription>该设备不在当前授权站点范围内，或已不在设备清单中。</EmptyDescription>
          </EmptyHeader>
          <EmptyContent><Button variant="outline" onClick={onBack}><ArrowLeft />返回设备</Button></EmptyContent>
        </Empty>
      </Main>
    );
  }

  const connection = connectionPresentation(row);
  const running = runningPresentation(row);
  const asset = connectedAsset(row);
  const points = metricPoints(row, 12);
  const currentStateNote = data.currentPending
    ? '正在读取当前状态'
    : data.currentUnavailable
      ? '当前状态不可用'
      : `最后更新 ${latestTimestamp(row, site.timezone)}`;

  const stateItems = [
    { label: '运行', value: running.label, icon: Activity, tone: running.label === '运行中' || running.label === '已停止' ? 'default' : 'warning' },
    { label: '连接', value: connection.label, icon: Radio, tone: connection.label === '在线' || connection.label === '不适用' ? 'default' : connection.label === '离线' ? 'critical' : 'warning' },
    { label: '新鲜度', value: freshnessLabel(row.operational.telemetry.freshness), icon: Clock, tone: row.operational.telemetry.freshness === 'FRESH' ? 'default' : 'warning' },
    { label: '质量', value: qualityLabel(row.operational.telemetry.quality), icon: CheckCircle2, tone: row.operational.telemetry.quality === 'GOOD' ? 'default' : 'warning' },
  ] as const;

  return (
    <Main fluid className="space-y-5" data-testid="asset-device-detail" data-device-visible="true">
      <header className="flex flex-col gap-3 border-b pb-4 sm:flex-row sm:items-start sm:justify-between" aria-label="设备身份">
        <div className="min-w-0">
          <div className="flex min-w-0 flex-wrap items-center gap-2.5">
            <h1 className="truncate text-xl font-semibold tracking-tight text-foreground sm:text-2xl">{row.device.displayName}</h1>
            <Badge variant="secondary" className="shrink-0 text-xs font-normal">
              {assetsDeviceTypeLabel(row.device.deviceType)}
            </Badge>
          </div>
          <p className="mt-1 flex flex-wrap items-center gap-x-1.5 gap-y-0.5 text-xs text-muted-foreground">
            {asset ? <><span>{asset}</span><span aria-hidden="true">·</span></> : null}
            <span>{deviceLocation(row)}</span>
            <span aria-hidden="true">·</span>
            <span>{site.displayName}</span>
            <span aria-hidden="true">·</span>
            <span>{currentStateNote}</span>
          </p>
        </div>
        <div className="flex shrink-0 items-center gap-2">
          <Button variant="outline" size="sm" onClick={onBack} className="h-8 gap-1.5 text-xs">
            <ArrowLeft className="size-3.5" aria-hidden="true" />
            返回设备
          </Button>
          <Button variant="outline" size="sm" onClick={data.refresh} className="h-8 gap-1.5 text-xs">
            <RefreshCw className={cn('size-3.5', (data.registry.isFetching || data.current.isFetching) && 'animate-spin')} aria-hidden="true" />
            刷新
          </Button>
        </div>
      </header>

      <section className="overflow-hidden rounded-md border" aria-label="设备独立状态">
        <div className="grid sm:grid-cols-2 lg:grid-cols-4 lg:divide-x">
          {stateItems.map((item, index) => {
            const Icon = item.icon;
            return (
              <div
                key={item.label}
                className={cn(
                  'flex min-w-0 items-center gap-2.5 px-3.5 py-2.5',
                  index > 0 && 'border-t sm:border-t-0',
                  index % 2 === 1 && 'sm:border-l lg:border-l-0',
                  index >= 2 && 'sm:border-t lg:border-t-0',
                )}
              >
                <Icon className={cn(
                  'size-4 shrink-0 text-muted-foreground',
                  item.tone === 'warning' && 'text-warning',
                  item.tone === 'critical' && 'text-destructive',
                )} aria-hidden="true" />
                <div className="min-w-0">
                  <div className="text-[11px] text-muted-foreground">{item.label}</div>
                  <div className={cn(
                    'truncate text-sm font-medium text-foreground',
                    item.tone === 'warning' && 'text-warning',
                    item.tone === 'critical' && 'text-destructive',
                  )}>{item.value}</div>
                </div>
              </div>
            );
          })}
        </div>
      </section>

      <nav className="flex gap-1 overflow-x-auto border-b" aria-label="设备详情分区">
        {[
          ['overview', '概览'],
          ['evidence', '证据'],
          ['relationships', '关系'],
          ['points', '工程点位'],
          ['metadata', '资料'],
        ].map(([id, label]) => (
          <a key={id} href={`#${id}`} className="shrink-0 border-b-2 border-transparent px-3 py-2 text-xs font-medium text-muted-foreground hover:border-foreground/30 hover:text-foreground">
            {label}
          </a>
        ))}
      </nav>

      <section id="overview" className="scroll-mt-24 space-y-6" aria-label="设备概览">
        <section aria-labelledby="device-current-operation">
          <h2 id="device-current-operation" className="flex items-center gap-2 text-sm font-semibold">
            <Server className="size-4 text-muted-foreground" aria-hidden="true" />
            当前运行
          </h2>
          {data.currentPending ? (
            <div className="mt-3 grid min-h-36 place-items-center border-y text-sm text-muted-foreground">正在读取当前测点…</div>
          ) : data.currentUnavailable ? (
            <div className="mt-3 grid min-h-36 place-items-center border-y px-6 text-center text-sm text-muted-foreground">当前遥测暂不可用，设备身份信息仍可查看。</div>
          ) : points.length > 0 ? (
            <div className="mt-3 grid overflow-hidden rounded-md border sm:grid-cols-2 lg:grid-cols-4">
              {points.slice(0, 8).map((point, index) => {
                const PointIcon = getPointMetricIcon(point.label);
                return (
                  <div
                    key={point.pointId}
                    className={cn(
                      'min-w-0 px-4 py-3.5',
                      index > 0 && 'border-t sm:border-t-0',
                      index % 2 === 1 && 'sm:border-l lg:border-l-0',
                      index >= 2 && 'sm:border-t lg:border-t-0',
                      index % 4 !== 0 && 'lg:border-l',
                      index >= 4 && 'lg:border-t',
                    )}
                  >
                    <div className="flex items-center justify-between gap-2">
                      <span className="truncate text-xs text-muted-foreground">{point.label}</span>
                      <PointIcon className="size-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
                    </div>
                    <div className="mt-1 flex min-w-0 items-baseline gap-1">
                      <strong className="truncate text-xl font-semibold tracking-tight tabular-nums">
                        {point.state === 'PRESENT' ? point.displayValue : '—'}
                      </strong>
                      {point.state === 'PRESENT' && point.unit ? <span className="text-xs text-muted-foreground">{point.unit}</span> : null}
                    </div>
                    <div className={cn(
                      'mt-1 truncate text-[11px] text-muted-foreground',
                      (point.quality !== 'GOOD' || point.freshness !== 'FRESH') && 'font-medium text-warning',
                    )}>
                      {point.sampledAt ? formatTimestamp(point.sampledAt, site.timezone) : freshnessLabel(point.freshness)} · {pointQualityLabel(point)}
                    </div>
                  </div>
                );
              })}
            </div>
          ) : <div className="mt-3 border-y py-8 text-sm text-muted-foreground">当前设备没有可展示的关键测点。</div>}
        </section>

        <section className="border-t pt-5" aria-labelledby="device-current-attention">
          <h2 id="device-current-attention" className="flex items-center gap-2 text-sm font-semibold">
            <AlertTriangle className={cn('size-4', row.operational.needsAttention ? 'text-warning' : 'text-muted-foreground')} aria-hidden="true" />
            当前事项
          </h2>
          {row.operational.needsAttention ? (
            <div className="mt-3 divide-y border-y">
              {row.operational.attentionReasons.map((reason) => (
                <div key={reason} className="flex items-center gap-2.5 py-3 text-sm">
                  <AlertTriangle className="size-4 shrink-0 text-warning" aria-hidden="true" />
                  <span>{ATTENTION_LABELS[reason]}</span>
                </div>
              ))}
            </div>
          ) : (
            <div className="mt-3 flex items-center gap-2 border-y py-3 text-sm text-muted-foreground">
              <CheckCircle2 className="size-4 text-success" aria-hidden="true" />
              当前没有需要优先核查的连接或数据问题
            </div>
          )}
        </section>
      </section>

      <RecentEvidence row={row} site={site} />
      <Relationships row={row} site={site} />
      <EngineeringPoints row={row} />
      <DeviceMetadata row={row} timezone={site.timezone} />
      <ProfessionalExits row={row} site={site} />
    </Main>
  );
}

