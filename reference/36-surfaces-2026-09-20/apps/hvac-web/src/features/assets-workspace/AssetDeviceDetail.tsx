import { useMemo, useState } from 'react';
import { Link } from '@tanstack/react-router';
import type { ColumnDef } from '@tanstack/react-table';
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
import type { CurrentPrincipalResponse, Site, TelemetryPoint } from '@/api/generated/platformGateway.gen';
import type { HvacRouterContext } from '@/app/router-context';
import { IndependentStateStrip } from '@/components/layout/IndependentStateStrip';
import { Main } from '@/components/layout/Main';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Checkbox } from '@/components/ui/checkbox';
import {
  DataTable,
  DataTablePagination,
  DataTableViewOptions,
  StatusPillBadge,
  type DataTableFeatures,
} from '@/components/data-table';
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyMedia } from '@/components/ui/empty';
import { InputGroup, InputGroupAddon, InputGroupInput } from '@/components/ui/input-group';
import { Skeleton } from '@/components/ui/skeleton';
import { assetsDeviceTypeLabel, type AssetsDeviceRow, type AssetsPointView } from '@/features/assets/model';
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
}

const POINT_TYPE_LABELS: Readonly<Record<TelemetryPoint['pointType'], string>> = Object.freeze({
  TELEMETRY: '遥测',
  COUNTER: '累计量',
  STATE: '状态',
  SETTING: '设定',
  COMMAND: '命令',
});

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
    <Card size="sm" className="shadow-xs">
      <CardHeader className="border-b bg-muted/10 pb-3">
        <div className="flex items-center justify-between">
          <div>
            <CardTitle className="flex items-center gap-2 text-base font-semibold">
              <Clock className="size-4 text-primary" aria-hidden="true" />
              最近观测
            </CardTitle>
            <CardDescription className="text-xs text-muted-foreground">
              最近的关键测点观测；完整时间序列进入趋势分析。
            </CardDescription>
          </div>
          <CardAction>
            <Button variant="ghost" size="xs" asChild className="gap-1 text-xs">
              <Link
                to="/sites/$siteId/trends"
                params={{ siteId: site.id }}
                search={{ series: evidence.length ? evidence.map((point) => point.pointId).join(',') : undefined }}
              >
                查看趋势 <ArrowRight className="size-3" />
              </Link>
            </Button>
          </CardAction>
        </div>
      </CardHeader>
      <CardContent className="p-0">
        {evidence.length > 0 ? (
          <div className="divide-y">
            {evidence.map((point) => {
              const MetricIcon = getPointMetricIcon(point.label);
              return (
                <div key={point.pointId} className="flex items-center justify-between gap-4 px-4 py-3">
                  <div className="flex items-center gap-3 min-w-0">
                    <div className="flex size-8 shrink-0 items-center justify-center rounded-lg border bg-muted/40 text-muted-foreground">
                      <MetricIcon className="size-4 text-foreground" aria-hidden="true" />
                    </div>
                    <div className="min-w-0">
                      <strong className="block truncate text-xs font-semibold text-foreground">{point.label}</strong>
                      <span className="mt-0.5 block text-[11px] text-muted-foreground">
                        {formatTimestamp(point.sampledAt, site.timezone)} · {pointQualityLabel(point)}
                      </span>
                    </div>
                  </div>
                  <strong className="text-sm font-semibold tabular-nums text-foreground shrink-0">
                    {point.displayValue}{point.unit ? ` ${point.unit}` : ''}
                  </strong>
                </div>
              );
            })}
          </div>
        ) : <div className="px-4 py-6 text-sm text-muted-foreground">当前没有可展示的最近观测。</div>}
      </CardContent>
    </Card>
  );
}

function Relationships({ row, site }: { readonly row: AssetsDeviceRow; readonly site: Readonly<Site> }) {
  const assetNames = row.binding.state === 'bound'
    ? [row.binding.asset.displayName]
    : row.binding.state === 'multi-bound'
      ? row.binding.bindings.map((binding) => binding.asset.displayName)
      : [];
  const sensorCount = new Set(row.telemetryPoints.flatMap((point) => point.sensorId ? [point.sensorId] : [])).size;

  return (
    <Card size="sm" className="shadow-xs">
      <CardHeader className="border-b bg-muted/10 pb-3">
        <CardTitle className="flex items-center gap-2 text-base font-semibold">
          <Layers className="size-4 text-primary" aria-hidden="true" />
          对象关系
        </CardTitle>
        <CardDescription className="text-xs text-muted-foreground">当前设备在站点模型中的位置与关联对象。</CardDescription>
      </CardHeader>
      <CardContent className="p-0">
        <dl className="divide-y text-xs">
          <div className="flex items-center justify-between px-4 py-2.5">
            <dt className="text-muted-foreground flex items-center gap-1.5"><Building2 className="size-3.5 text-muted-foreground" />站点</dt>
            <dd className="font-semibold text-foreground">{site.displayName}</dd>
          </div>
          <div className="flex items-center justify-between px-4 py-2.5">
            <dt className="text-muted-foreground flex items-center gap-1.5"><MapPin className="size-3.5 text-muted-foreground" />空间位置</dt>
            <dd className="font-semibold text-foreground">{deviceLocation(row)}</dd>
          </div>
          <div className="flex items-center justify-between px-4 py-2.5">
            <dt className="text-muted-foreground flex items-center gap-1.5"><Server className="size-3.5 text-muted-foreground" />关联资产</dt>
            <dd className="font-semibold text-foreground">{assetNames.length ? assetNames.join('、') : '未关联'}</dd>
          </div>
          <div className="flex items-center justify-between px-4 py-2.5">
            <dt className="text-muted-foreground flex items-center gap-1.5"><Radio className="size-3.5 text-muted-foreground" />物理传感器</dt>
            <dd className="font-semibold tabular-nums text-foreground">{sensorCount} 个</dd>
          </div>
          <div className="flex items-center justify-between px-4 py-2.5">
            <dt className="text-muted-foreground flex items-center gap-1.5"><Boxes className="size-3.5 text-muted-foreground" />注册点位</dt>
            <dd className="font-semibold tabular-nums text-foreground">{row.registeredPointCount} 个</dd>
          </div>
        </dl>
      </CardContent>
    </Card>
  );
}

function EngineeringPoints({ row }: { readonly row: AssetsDeviceRow }) {
  const [query, setQuery] = useState('');

  const rows = useMemo<EngineeringPointRow[]>(() => {
    const normalized = query.trim().toLocaleLowerCase('zh-CN');
    return row.telemetryPoints
      .filter((point) => !normalized || `${point.displayName} ${point.pointCode}`.toLocaleLowerCase('zh-CN').includes(normalized))
      .map((point) => ({
        point,
        current: row.operational.points.find((current) => current.key === point.pointCode) ?? null,
      }));
  }, [query, row.operational.points, row.telemetryPoints]);

  const columns = useMemo<Array<ColumnDef<DataTableFeatures, EngineeringPointRow>>>(() => [
    {
      id: 'select',
      header: ({ table }) => (
        <Checkbox
          checked={
            table.getIsAllPageRowsSelected()
              ? true
              : table.getIsSomePageRowsSelected()
                ? 'indeterminate'
                : false
          }
          onCheckedChange={(checked) => table.toggleAllPageRowsSelected(Boolean(checked))}
          aria-label="全选"
        />
      ),
      cell: ({ row: tableRow }) => (
        <Checkbox
          checked={tableRow.getIsSelected()}
          onCheckedChange={(checked) => tableRow.toggleSelected(Boolean(checked))}
          aria-label="选择"
          onClick={(event) => event.stopPropagation()}
        />
      ),
      enableSorting: false,
      enableHiding: false,
    },
    {
      id: 'point',
      header: '点位',
      cell: ({ row: tableRow }) => (
        <div className="min-w-0 pr-2">
          <strong className="block truncate text-xs font-semibold text-foreground">{tableRow.original.point.displayName}</strong>
          <span className="mt-0.5 block truncate font-mono text-[11px] text-muted-foreground">{tableRow.original.point.pointCode}</span>
        </div>
      ),
    },
    {
      id: 'type',
      header: '类型',
      cell: ({ row: tableRow }) => (
        <StatusPillBadge tone="neutral">
          {POINT_TYPE_LABELS[tableRow.original.point.pointType]}
        </StatusPillBadge>
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
      id: 'quality',
      header: '质量',
      cell: ({ row: tableRow }) => (
        <span className="text-xs text-muted-foreground">{pointQualityLabel(tableRow.original.current)}</span>
      ),
    },
    {
      id: 'sample',
      header: '采样周期',
      cell: ({ row: tableRow }) => (
        <span className="whitespace-nowrap font-mono text-xs text-muted-foreground tabular-nums">{formatInterval(tableRow.original.point.sampleIntervalMs)}</span>
      ),
    },
    {
      id: 'capability',
      header: '源端能力',
      cell: ({ row: tableRow }) => (
        <StatusPillBadge tone={tableRow.original.point.writable ? 'warning' : 'neutral'}>
          {tableRow.original.point.writable ? '可写' : '只读'}
        </StatusPillBadge>
      ),
    },
  ], []);

  const table = useDataTable({
    key: 'device-engineering-points',
    data: rows,
    columns,
    pageSize: 10,
    getRowId: (engineeringPoint) => engineeringPoint.point.id,
  });

  return (
    <Card className="min-w-0 shadow-xs">
      <CardHeader className="border-b bg-muted/10 pb-3">
        <div className="flex flex-col gap-1 sm:flex-row sm:items-center sm:justify-between">
          <div>
            <CardTitle className="flex items-center gap-2 text-base font-semibold">
              <Cpu className="size-4.5 text-primary" aria-hidden="true" />
              工程点位
            </CardTitle>
            <CardDescription className="text-xs text-muted-foreground">
              设备已登记点位及当前观测。源端“可写”仅表示对象能力，不代表当前用户可执行控制。
            </CardDescription>
          </div>
          <DataTableViewOptions table={table} />
        </div>
      </CardHeader>
      <CardContent className="p-0">
        <div className="border-b bg-muted/5 p-3">
          <InputGroup className="max-w-sm h-8">
            <InputGroupInput value={query} onChange={(event) => { setQuery(event.currentTarget.value); table.setPageIndex(0); }} placeholder="搜索点位名称或编码" aria-label="搜索工程点位" className="h-8 text-xs" />
            <InputGroupAddon align="inline-start"><Search className="size-3.5" /></InputGroupAddon>
          </InputGroup>
        </div>
        <DataTable
          table={table}
          role="region"
          aria-label="工程点位表格，可横向滚动"
          tabIndex={0}
          className="gap-0"
          tableAriaLabel="工程点位"
          empty="没有符合搜索条件的点位。"
          getHeaderRowProps={() => ({
            className: 'border-b bg-muted/20 hover:bg-transparent',
          })}
          getHeaderCellProps={() => ({
            className: 'h-9 whitespace-nowrap text-[11px] font-semibold text-muted-foreground',
          })}
          getRowProps={() => ({ className: 'hover:bg-muted/30' })}
          getCellProps={() => ({ className: 'py-2.5' })}
          footer={(
            <DataTablePagination
              table={table}
              totalRows={rows.length}
              
              
              
              
              
            />
          )}
        />
      </CardContent>
    </Card>
  );
}

function DeviceMetadata({ row, timezone }: { readonly row: AssetsDeviceRow; readonly timezone: string }) {
  return (
    <Card size="sm" className="shadow-xs">
      <CardHeader className="border-b bg-muted/10 pb-3">
        <CardTitle className="flex items-center gap-2 text-base font-semibold">
          <ShieldCheck className="size-4 text-primary" aria-hidden="true" />
          设备资料
        </CardTitle>
        <CardDescription className="text-xs text-muted-foreground">设备清单中的稳定身份与生命周期事实。</CardDescription>
      </CardHeader>
      <CardContent className="p-0">
        <dl className="divide-y text-xs">
          <div className="flex items-center justify-between px-4 py-2.5">
            <dt className="text-muted-foreground">设备编码</dt>
            <dd className="font-mono font-medium text-foreground bg-muted/40 px-1.5 py-0.5 rounded">{row.device.code}</dd>
          </div>
          <div className="flex items-center justify-between px-4 py-2.5">
            <dt className="text-muted-foreground">生命周期</dt>
            <dd>
              <StatusPillBadge tone="success">
                {DEVICE_STATUS_LABELS[row.device.status]}
              </StatusPillBadge>
            </dd>
          </div>
          <div className="flex items-center justify-between px-4 py-2.5">
            <dt className="text-muted-foreground flex items-center gap-1"><Calendar className="size-3" />登记时间</dt>
            <dd className="font-medium tabular-nums text-foreground">{formatTimestamp(row.device.createdAt, timezone)}</dd>
          </div>
          <div className="flex items-center justify-between px-4 py-2.5">
            <dt className="text-muted-foreground flex items-center gap-1"><Clock className="size-3" />资料更新</dt>
            <dd className="font-medium tabular-nums text-foreground">{formatTimestamp(row.device.updatedAt, timezone)}</dd>
          </div>
        </dl>
      </CardContent>
    </Card>
  );
}

function ProfessionalExits({ row, site }: { readonly row: AssetsDeviceRow; readonly site: Readonly<Site> }) {
  const series = metricPoints(row, 4).map((point) => point.pointId).join(',');
  return (
    <Card size="sm" className="shadow-xs">
      <CardHeader className="border-b bg-muted/10 pb-3">
        <CardTitle className="flex items-center gap-2 text-base font-semibold">
          <ExternalLink className="size-4 text-primary" aria-hidden="true" />
          专业出口
        </CardTitle>
        <CardDescription className="text-xs text-muted-foreground">进入持久业务工作区继续调查或处理。</CardDescription>
      </CardHeader>
      <CardContent className="p-2 space-y-1">
        <Button variant="ghost" size="sm" className="w-full justify-between h-8 text-xs font-normal" asChild>
          <Link to="/sites/$siteId/trends" params={{ siteId: site.id }} search={{ series: series || undefined }}>
            <span className="flex items-center gap-2"><TrendingUp className="size-3.5 text-muted-foreground" />趋势分析</span>
            <ArrowRight className="size-3 text-muted-foreground" />
          </Link>
        </Button>
        <Button variant="ghost" size="sm" className="w-full justify-between h-8 text-xs font-normal" asChild>
          <Link to="/sites/$siteId/alarms" params={{ siteId: site.id }} search={{ view: 'active', source: 'device-detail', device: row.device.displayName, deviceId: row.device.id }}>
            <span className="flex items-center gap-2"><AlertTriangle className="size-3.5 text-muted-foreground" />告警中心</span>
            <ArrowRight className="size-3 text-muted-foreground" />
          </Link>
        </Button>
        <Button variant="ghost" size="sm" className="w-full justify-between h-8 text-xs font-normal" asChild>
          <Link to="/sites/$siteId/work-orders" params={{ siteId: site.id }} search={{ source: 'device-detail', device: row.device.id }}>
            <span className="flex items-center gap-2"><SlidersHorizontal className="size-3.5 text-muted-foreground" />运维工单</span>
            <ArrowRight className="size-3 text-muted-foreground" />
          </Link>
        </Button>
        <Button variant="ghost" size="sm" className="w-full justify-between h-8 text-xs font-normal" asChild>
          <Link to="/sites/$siteId/control" params={{ siteId: site.id }}>
            <span className="flex items-center gap-2"><Cpu className="size-3.5 text-muted-foreground" />控制中心</span>
            <ArrowRight className="size-3 text-muted-foreground" />
          </Link>
        </Button>
      </CardContent>
    </Card>
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
          <EmptyContent><Button variant="outline" onClick={onBack}><ArrowLeft />返回设备中心</Button></EmptyContent>
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
    { label: '运行状态', value: running.label, icon: Activity, tone: running.label === '运行中' || running.label === '已停止' ? 'neutral' : 'warning' },
    { label: '连接状态', value: connection.label, icon: Radio, tone: connection.label === '在线' || connection.label === '不适用' ? 'neutral' : connection.label === '离线' ? 'destructive' : 'warning' },
    { label: '数据新鲜度', value: freshnessLabel(row.operational.telemetry.freshness), detail: latestTimestamp(row, site.timezone), icon: Clock, tone: row.operational.telemetry.freshness === 'FRESH' ? 'neutral' : 'warning' },
    { label: '数据质量', value: qualityLabel(row.operational.telemetry.quality), icon: CheckCircle2, tone: row.operational.telemetry.quality === 'GOOD' ? 'neutral' : 'warning' },
  ] as const;

  return (
    <Main fluid className="space-y-5" data-testid="asset-device-detail" data-device-visible="true">
      {/* Device Identity Header */}
      <section className="space-y-3" aria-label="设备身份与当前状态">
        <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
          <div>
            <div className="flex items-center gap-2.5">
              <h1 className="text-xl font-bold tracking-tight text-foreground sm:text-2xl">{row.device.displayName}</h1>
              <Badge variant="secondary" className="text-xs font-normal">
                {assetsDeviceTypeLabel(row.device.deviceType)}
              </Badge>
            </div>
            <p className="mt-0.5 flex items-center gap-1.5 text-xs text-muted-foreground">
              <span>{assetsDeviceTypeLabel(row.device.deviceType)}</span>
              <span aria-hidden="true">·</span>
              <span>{deviceLocation(row)}</span>
              {asset ? (
                <>
                  <span aria-hidden="true">·</span>
                  <span>{asset}</span>
                </>
              ) : null}
              <span aria-hidden="true">·</span>
              <span>{site.displayName}</span>
              <span aria-hidden="true">·</span>
              <span>{currentStateNote}</span>
            </p>
          </div>
          <div className="flex items-center gap-2">
            <Button variant="outline" size="sm" onClick={onBack} className="h-8 gap-1.5 text-xs">
              <ArrowLeft className="size-3.5" aria-hidden="true" />
              返回设备中心
            </Button>
            <Button variant="outline" size="sm" onClick={data.refresh} className="h-8 gap-1.5 text-xs">
              <RefreshCw className={cn('size-3.5', (data.registry.isFetching || data.current.isFetching) && 'animate-spin')} aria-hidden="true" />
              刷新
            </Button>
          </div>
        </div>
        <IndependentStateStrip items={stateItems} ariaLabel="设备独立状态" />
      </section>

      {/* Main Investigation Workspace */}
      <section className="grid items-start gap-4 xl:grid-cols-[minmax(0,1.65fr)_minmax(300px,.65fr)]" aria-label="设备调查工作区">
        <div className="min-w-0 space-y-4">
          {/* Current Running Telemetry Cards */}
          <Card className="shadow-xs">
            <CardHeader className="border-b bg-muted/10 pb-3">
              <CardTitle className="flex items-center gap-2 text-base font-semibold">
                <Server className="size-4.5 text-primary" aria-hidden="true" />
                当前运行
              </CardTitle>
              <CardDescription className="text-xs text-muted-foreground">
                设备关键运行事实；每个值保留自己的采样时间与质量语义。
              </CardDescription>
            </CardHeader>
            <CardContent className="p-0">
              {data.currentPending ? (
                <div className="grid min-h-56 place-items-center text-sm text-muted-foreground">正在读取当前测点…</div>
              ) : data.currentUnavailable ? (
                <div className="grid min-h-56 place-items-center px-6 text-center text-sm text-muted-foreground">当前遥测暂不可用，设备身份信息仍可查看。</div>
              ) : points.length > 0 ? (
                <div className="grid divide-y sm:grid-cols-2 sm:divide-x sm:divide-y-0 lg:grid-cols-3">
                  {points.slice(0, 8).map((point) => {
                    const PointIcon = getPointMetricIcon(point.label);
                    return (
                      <div key={point.pointId} className="min-w-0 p-4 space-y-1.5 transition-colors hover:bg-muted/15">
                        <div className="flex items-center justify-between">
                          <span className="block truncate text-xs text-muted-foreground font-medium">{point.label}</span>
                          <PointIcon className="size-3.5 text-muted-foreground" aria-hidden="true" />
                        </div>
                        <strong className="block truncate text-2xl font-bold tracking-tight text-foreground tabular-nums">
                          {point.state === 'PRESENT' ? point.displayValue : '—'}
                          {point.state === 'PRESENT' && point.unit ? (
                            <span className="ml-1 text-xs font-normal text-muted-foreground">{point.unit}</span>
                          ) : null}
                        </strong>
                        <span className={cn('block truncate text-[11px]', point.quality === 'GOOD' && point.freshness === 'FRESH' ? 'text-muted-foreground' : 'text-warning font-medium')}>
                          {point.sampledAt ? formatTimestamp(point.sampledAt, site.timezone) : freshnessLabel(point.freshness)} · {pointQualityLabel(point)}
                        </span>
                      </div>
                    );
                  })}
                </div>
              ) : <div className="grid min-h-56 place-items-center text-sm text-muted-foreground">当前设备没有可展示的关键测点。</div>}
            </CardContent>
          </Card>

          {/* Attention Matters */}
          <Card size="sm" className="shadow-xs" aria-label="当前注意事项">
            <CardHeader className="border-b bg-muted/10 pb-3">
              <CardTitle className="flex items-center gap-2 text-base font-semibold">
                <AlertTriangle className="size-4 text-warning" aria-hidden="true" />
                当前注意事项
              </CardTitle>
              <CardDescription className="text-xs text-muted-foreground">仅展示当前设备投影能够确认的连接与数据注意项。</CardDescription>
            </CardHeader>
            <CardContent className="pt-3.5">
              {row.operational.needsAttention ? (
                <div className="flex flex-wrap gap-2">
                  {row.operational.attentionReasons.map((reason) => (
                    <Badge key={reason} variant="outline" className="border-warning/35 bg-warning/10 text-warning text-xs">
                      {ATTENTION_LABELS[reason]}
                    </Badge>
                  ))}
                </div>
              ) : (
                <div className="flex items-center gap-2 text-xs text-muted-foreground">
                  <CheckCircle2 className="size-3.5 text-success" />
                  <span>当前未发现需要优先核查的连接或数据问题。</span>
                </div>
              )}
            </CardContent>
          </Card>

          <RecentEvidence row={row} site={site} />
          <EngineeringPoints row={row} />
        </div>

        <aside className="min-w-0 space-y-4" aria-label="设备关系与资料">
          <Relationships row={row} site={site} />
          <DeviceMetadata row={row} timezone={site.timezone} />
          <ProfessionalExits row={row} site={site} />
        </aside>
      </section>
    </Main>
  );
}

