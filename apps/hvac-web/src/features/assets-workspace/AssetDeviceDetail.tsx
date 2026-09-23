// @surface-card-table-exception 07 — preserve reviewed Surface 07 parameter-table Card anatomy.
import { useMemo } from 'react';
import { Link } from '@tanstack/react-router';
import type { ColumnDef } from '@tanstack/react-table';
import {
  Activity,
  AlertTriangle,
  ArrowLeft,
  CheckCircle2,
  Gauge,
  RefreshCw,
  ShieldCheck,
  Snowflake,
  TrendingUp,
  Zap,
} from 'lucide-react';

import type { CurrentPrincipalResponse, Site, TelemetryPoint } from '@/api/generated/platformGateway.gen';
import type { HvacRouterContext } from '@/app/router-context';
import {
  DataTable,
  type DataTableFeatures,
} from '@/components/data-table';
import { Main } from '@/components/layout/Main';
import { StatusBadge } from '@/components/status-badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import {
  Empty,
  EmptyContent,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
} from '@/components/ui/empty';
import { Skeleton } from '@/components/ui/skeleton';
import {
  assetsDeviceTypeLabel,
  type AssetsDeviceRow,
  type AssetsPointView,
} from '@/features/assets/model';
import { useSiteAssetsData } from '@/features/assets/use-site-assets-data';
import { useDataTable } from '@/hooks/use-data-table';
import { cn } from '@/lib/utils';
import {
  connectedAsset,
  connectionPresentation,
  deviceLocation,
  freshnessLabel,
  latestTimestamp,
  metricPoints,
  qualityLabel,
  runningPresentation,
} from './presentation';

interface AssetDeviceDetailProps {
  readonly site: Readonly<Site>;
  readonly principal: CurrentPrincipalResponse;
  readonly runtime: HvacRouterContext['runtime'];
  readonly deviceId: string;
  readonly onBack: () => void;
}

interface OperatingParameterRow {
  readonly point: TelemetryPoint;
  readonly current: AssetsPointView | null;
}

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

function pointQualityLabel(point: AssetsPointView | null): string {
  if (!point || point.state !== 'PRESENT') return '无当前值';
  if (point.quality === 'GOOD') return '正常';
  if (point.quality === 'PARTIAL') return '部分有效';
  if (point.quality === 'ESTIMATED') return '估算';
  if (point.quality === 'MANUAL') return '人工';
  if (point.quality === 'STALE') return '延迟';
  if (point.quality === 'INVALID') return '无效';
  return '待核查';
}

function pointStatusTone(point: AssetsPointView | null): 'success' | 'warning' | 'neutral' {
  if (!point || point.state !== 'PRESENT') return 'neutral';
  return point.quality === 'GOOD' && point.freshness === 'FRESH' ? 'success' : 'warning';
}

function pointMetricIcon(label: string) {
  const normalized = label.toLowerCase();
  if (normalized.includes('功率') || normalized.includes('power')) return Zap;
  if (normalized.includes('cop') || normalized.includes('能效')) return Gauge;
  if (normalized.includes('冷') || normalized.includes('cooling')) return Snowflake;
  if (normalized.includes('温') || normalized.includes('temp')) return Activity;
  return Gauge;
}

function LoadingState() {
  return (
    <Main fluid className="space-y-6">
      <Skeleton className="h-16 w-full" />
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        {Array.from({ length: 4 }).map((_, index) => <Skeleton key={index} className="h-28" />)}
      </div>
      <Skeleton className="h-[360px] w-full" />
      <div className="grid gap-6 lg:grid-cols-2">
        <Skeleton className="h-[420px]" />
        <Skeleton className="h-[420px]" />
      </div>
    </Main>
  );
}

export function AssetDeviceDetail({
  site,
  principal,
  runtime,
  deviceId,
  onBack,
}: AssetDeviceDetailProps) {
  const data = useSiteAssetsData({ site, principal, runtime });

  if (data.registry.isPending) return <LoadingState />;

  if (data.registry.isError) {
    return (
      <Main fluid>
        <Empty className="min-h-[520px] w-full rounded-lg border bg-card shadow-xs">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <AlertTriangle className="text-destructive" aria-hidden="true" />
            </EmptyMedia>
            <h1 className="text-xl font-semibold">设备详情暂时无法加载</h1>
            <EmptyDescription>设备身份信息暂不可用，请稍后重试。</EmptyDescription>
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

  const row = data.rows.find((candidate) => candidate.device.id === deviceId);

  if (!row) {
    return (
      <Main fluid>
        <Empty className="min-h-[520px] w-full rounded-lg border bg-card shadow-xs" data-testid="asset-device-not-visible">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <AlertTriangle className="text-warning" aria-hidden="true" />
            </EmptyMedia>
            <h1 className="text-xl font-semibold">设备不可见</h1>
            <EmptyDescription>该设备不在当前授权站点范围内，或已不在设备清单中。</EmptyDescription>
          </EmptyHeader>
          <EmptyContent>
            <Button variant="outline" onClick={onBack}>
              <ArrowLeft />
              返回设备中心
            </Button>
          </EmptyContent>
        </Empty>
      </Main>
    );
  }

  return <LoadedDeviceDetail row={row} site={site} data={data} onBack={onBack} />;
}

function LoadedDeviceDetail({
  row,
  site,
  data,
  onBack,
}: {
  readonly row: AssetsDeviceRow;
  readonly site: Readonly<Site>;
  readonly data: ReturnType<typeof useSiteAssetsData>;
  readonly onBack: () => void;
}) {
  const running = runningPresentation(row);
  const connection = connectionPresentation(row);
  const asset = connectedAsset(row);
  const keyPoints = metricPoints(row, 4);
  const trendSeries = keyPoints.map((point) => point.pointId).join(',');
  const parameterRows = useMemo<OperatingParameterRow[]>(
    () => row.telemetryPoints.map((point) => ({
      point,
      current: row.operational.points.find((current) => current.key === point.pointCode) ?? null,
    })),
    [row.operational.points, row.telemetryPoints],
  );

  const parameterColumns = useMemo<Array<ColumnDef<DataTableFeatures, OperatingParameterRow>>>(() => [
    {
      id: 'name',
      header: '物理参数',
      cell: ({ row: tableRow }) => (
        <div className="min-w-0">
          <span className="block truncate text-xs font-medium text-foreground">{tableRow.original.point.displayName}</span>
          <span className="mt-0.5 block truncate font-mono text-[11px] text-muted-foreground">{tableRow.original.point.pointCode}</span>
        </div>
      ),
    },
    {
      id: 'value',
      header: '当前实测值',
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
      id: 'limit',
      header: '设计安全区间',
      cell: () => <span className="font-mono text-xs text-muted-foreground">—</span>,
    },
    {
      id: 'status',
      header: '工况状态',
      cell: ({ row: tableRow }) => (
        <StatusBadge
          label={pointQualityLabel(tableRow.original.current)}
          tone={pointStatusTone(tableRow.original.current)}
          className="h-6 px-2 text-[11px]"
        />
      ),
    },
  ], []);

  const parameterTable = useDataTable({
    key: 'surface-07-operating-parameters',
    data: parameterRows,
    columns: parameterColumns,
    paginate: false,
    getRowId: (item) => item.point.id,
  });

  const statusText = running.label === '未知' ? connection.label : running.label;
  const statusTone = running.label === '运行中'
    ? 'in-progress'
    : connection.label === '离线'
      ? 'destructive'
      : running.label === '未知'
        ? 'warning'
        : 'neutral';

  return (
    <Main fluid className="space-y-6 pb-16" data-testid="asset-device-detail" data-device-visible="true">
      <header className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between" aria-label="设备身份与当前状态">
        <div className="flex min-w-0 items-start gap-3">
          <Button variant="outline" size="sm" onClick={onBack} className="mt-0.5 h-8 shrink-0 gap-1 text-xs">
            <ArrowLeft className="size-3.5" aria-hidden="true" />
            返回设备列表
          </Button>
          <div className="min-w-0">
            <div className="flex min-w-0 flex-wrap items-center gap-2.5">
              <h1 className="truncate text-2xl font-bold tracking-tight text-foreground">
                {row.device.displayName} ({row.device.code})
              </h1>
              <StatusBadge label={statusText} tone={statusTone} className="h-6 px-2 text-[11px]" />
            </div>
            <p className="mt-1 text-sm text-muted-foreground">
              资产编码：{row.device.code} · 安装位置：{deviceLocation(row)}
              {asset ? ` · 关联资产：${asset}` : ''}
            </p>
          </div>
        </div>
        <div className="flex shrink-0 items-center gap-2">
          <Button variant="outline" size="sm" className="h-8 gap-1.5 text-xs" onClick={data.refresh}>
            <RefreshCw
              className={cn('size-3.5', (data.registry.isFetching || data.current.isFetching) && 'animate-spin')}
              aria-hidden="true"
            />
            刷新
          </Button>
        </div>
      </header>

      <section className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4" aria-label="设备关键实时参数">
        {Array.from({ length: 4 }).map((_, index) => {
          const point = keyPoints[index] ?? null;
          const fallbackToDataState = !point && index === 3;
          const Icon = point ? pointMetricIcon(point.label) : fallbackToDataState ? CheckCircle2 : Gauge;
          const title = point?.label ?? (fallbackToDataState ? '数据状态' : `关键参数 ${index + 1}`);
          const value = point?.state === 'PRESENT'
            ? point.displayValue
            : fallbackToDataState
              ? freshnessLabel(row.operational.telemetry.freshness)
              : '—';
          const detail = point
            ? `${freshnessLabel(point.freshness)} · ${qualityLabel(point.quality ?? 'MISSING')} · ${formatTimestamp(point.sampledAt, site.timezone)}`
            : fallbackToDataState
              ? `质量 ${qualityLabel(row.operational.telemetry.quality)} · ${latestTimestamp(row, site.timezone)}`
              : '当前设备未提供该参数';
          return (
            <Card key={point?.pointId ?? `placeholder-${index}`} className="shadow-xs">
              <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
                <CardTitle className="truncate text-sm font-medium text-muted-foreground">{title}</CardTitle>
                <Icon className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
              </CardHeader>
              <CardContent className="space-y-1">
                <div className="flex items-baseline gap-2">
                  <span className="text-2xl font-bold tracking-tight tabular-nums text-foreground">{value}</span>
                  {point?.state === 'PRESENT' && point.unit ? <span className="text-xs text-muted-foreground">{point.unit}</span> : null}
                </div>
                <div className="text-xs text-muted-foreground">{detail}</div>
              </CardContent>
            </Card>
          );
        })}
      </section>

      <Card className="shadow-xs">
        <CardHeader className="flex flex-col gap-2 pb-2 sm:flex-row sm:items-center sm:justify-between">
          <div>
            <CardTitle className="text-base font-semibold">24小时工况遥测曲线</CardTitle>
            <CardDescription className="text-xs">
              查看关键参数最近 24 小时的变化与异常时段。
            </CardDescription>
          </div>
          <Button variant="outline" size="sm" asChild className="h-8 gap-1.5 text-xs">
            <Link
              to="/sites/$siteId/trends"
              params={{ siteId: site.id }}
              search={{ series: trendSeries || undefined }}
            >
              <TrendingUp className="size-3.5" aria-hidden="true" />
              查看24小时趋势
            </Link>
          </Button>
        </CardHeader>
        <CardContent className="pt-4">
          <div className="grid h-[260px] place-items-center rounded-md border border-dashed bg-muted/10 px-6 text-center">
            <div className="max-w-md">
              <TrendingUp className="mx-auto size-5 text-muted-foreground" aria-hidden="true" />
              <p className="mt-3 text-sm font-medium text-foreground">最近 24 小时趋势</p>
              <p className="mt-1 text-xs leading-relaxed text-muted-foreground">
                进入趋势分析查看当前设备关键参数的历史变化、异常时段和对比关系。
              </p>
            </div>
          </div>
        </CardContent>
      </Card>

      <section className="grid grid-cols-1 gap-6 lg:grid-cols-2" aria-label="设备运行参数与铭牌">
        <Card className="min-w-0 shadow-xs">
          <CardHeader className="pb-3">
            <CardTitle className="text-base font-semibold">核心运行参数</CardTitle>
          </CardHeader>
          <CardContent className="p-0">
            <DataTable
              table={parameterTable}
              className="gap-0"
              tableAriaLabel="设备核心运行参数"
              getHeaderRowProps={() => ({ className: 'bg-muted/20 hover:bg-transparent' })}
              getHeaderCellProps={(header) => ({
                className:
                  header.id === 'value' || header.id === 'limit'
                    ? 'text-right text-xs'
                    : header.id === 'status'
                      ? 'text-center text-xs'
                      : 'text-xs',
              })}
              getRowProps={() => ({ className: 'text-xs hover:bg-muted/40' })}
              getCellProps={(cell) => ({
                className:
                  cell.column.id === 'value' || cell.column.id === 'limit'
                    ? 'text-right'
                    : cell.column.id === 'status'
                      ? 'text-center'
                      : undefined,
              })}
            />
          </CardContent>
        </Card>

        <Card className="shadow-xs">
          <CardHeader className="pb-3">
            <CardTitle className="text-base font-semibold">设备铭牌与生命周期台账</CardTitle>
          </CardHeader>
          <CardContent className="space-y-3">
            <div className="grid grid-cols-1 gap-3 text-xs sm:grid-cols-2">
              <div className="space-y-1 rounded-lg border bg-muted/20 p-3">
                <span className="text-muted-foreground">设备类型</span>
                <p className="font-semibold text-foreground">{assetsDeviceTypeLabel(row.device.deviceType)}</p>
              </div>
              <div className="space-y-1 rounded-lg border bg-muted/20 p-3">
                <span className="text-muted-foreground">设备编码</span>
                <p className="font-mono font-semibold text-foreground">{row.device.code}</p>
              </div>
              <div className="space-y-1 rounded-lg border bg-muted/20 p-3">
                <span className="text-muted-foreground">安装位置</span>
                <p className="font-semibold text-foreground">{deviceLocation(row)}</p>
              </div>
              <div className="space-y-1 rounded-lg border bg-muted/20 p-3">
                <span className="text-muted-foreground">关联资产</span>
                <p className="font-semibold text-foreground">{asset ?? '未关联'}</p>
              </div>
              <div className="space-y-1 rounded-lg border bg-muted/20 p-3">
                <span className="text-muted-foreground">登记时间</span>
                <p className="font-semibold tabular-nums text-foreground">{formatTimestamp(row.device.createdAt, site.timezone)}</p>
              </div>
              <div className="space-y-1 rounded-lg border bg-muted/20 p-3">
                <span className="text-muted-foreground">资料更新</span>
                <p className="font-semibold tabular-nums text-foreground">{formatTimestamp(row.device.updatedAt, site.timezone)}</p>
              </div>
            </div>

            <div className="space-y-2 rounded-lg border bg-muted/30 p-3 text-xs">
              <div className="flex items-center gap-1.5 font-semibold text-foreground">
                <ShieldCheck className="size-4 text-muted-foreground" aria-hidden="true" />
                <span>当前设备状态</span>
              </div>
              <div className="grid gap-2 sm:grid-cols-2">
                <div className="flex items-center justify-between gap-3">
                  <span className="text-muted-foreground">运行 / 连接</span>
                  <span className="font-medium">{running.label} / {connection.label}</span>
                </div>
                <div className="flex items-center justify-between gap-3">
                  <span className="text-muted-foreground">数据</span>
                  <span className="font-medium">{freshnessLabel(row.operational.telemetry.freshness)} / {qualityLabel(row.operational.telemetry.quality)}</span>
                </div>
              </div>
              {row.operational.needsAttention ? (
                <div className="flex items-center gap-2 border-t pt-2 text-warning">
                  <AlertTriangle className="size-3.5 shrink-0" aria-hidden="true" />
                  <span>当前设备存在需要继续核查的事项。</span>
                </div>
              ) : (
                <div className="flex items-center gap-2 border-t pt-2 text-muted-foreground">
                  <CheckCircle2 className="size-3.5 text-success" aria-hidden="true" />
                  <span>当前没有需要优先核查的连接或数据问题。</span>
                </div>
              )}
            </div>
          </CardContent>
        </Card>
      </section>
    </Main>
  );
}
