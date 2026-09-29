import { useCallback, useEffect, useRef, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';
import {
  Activity,
  ArrowRight,
  Bell,
  Box,
  ChartNoAxesColumnIncreasing,
  CheckCircle2,
  CircleAlert,
  Cpu,
  Database,
  Droplets,
  Fan,
  Gauge,
  Layers,
  Power,
  RadioTower,
  RefreshCw,
  Settings2,
  SlidersHorizontal,
  Snowflake,
  Stethoscope,
  Wind,
  Zap,
} from 'lucide-react';
import { readDashboardOverview } from '@/api/dashboard-overview';
import { FactStrip } from '@/blocks/fact-strip';
import type { CurrentPrincipalResponse, Site } from '@/api/generated/platformGateway.gen';
import { presentSiteDashboardError, useSiteDashboardSummary } from '@/api/site-dashboard';
import { Main } from '@/components/layout/Main';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from '@/components/ui/empty';
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet';
import { Skeleton } from '@/components/ui/skeleton';
import { Item, ItemActions, ItemContent, ItemDescription, ItemGroup, ItemMedia, ItemTitle } from '@/components/ui/item';
import { cn } from '@/lib/utils';

export interface SystemOperationsSearchState {
  readonly group?: string;
}

interface SystemOperationsProps {
  readonly site: Readonly<Site>;
  readonly principal: CurrentPrincipalResponse;
  readonly searchState: SystemOperationsSearchState;
  readonly onSearchChange: (patch: Partial<SystemOperationsSearchState>) => void;
}

type TopologyRow = Awaited<ReturnType<typeof readDashboardOverview>>['topology'][number];

const QUALITY_LABEL: Readonly<Record<string, string>> = Object.freeze({
  READY: '正常',
  ATTENTION: '需关注',
  NO_DATA: '无数据',
  PARTIAL: '部分可用',
  STALE: '数据延迟',
  SUSPECT: '待核验',
  UNAVAILABLE: '不可用',
  NOT_AUTHORIZED: '无权限',
  NOT_INTEGRATED: '未接入',
});

const SEVERITY_LABEL: Readonly<Record<string, string>> = Object.freeze({
  CRITICAL: '紧急',
  MAJOR: '重要',
  MINOR: '一般',
  WARNING: '警告',
  INFO: '提示',
});

function formatNumber(value: number | null | undefined, digits = 1, minDigits = 0): string {
  if (value === null || value === undefined) return '—';
  return new Intl.NumberFormat('zh-CN', { minimumFractionDigits: minDigits, maximumFractionDigits: digits }).format(value);
}

function formatDeviation(value: number | null | undefined, digits = 1): string {
  if (value === null || value === undefined) return '—';
  const sign = value > 0 ? '+' : '';
  return `${sign}${formatNumber(value, digits)} K`;
}

function formatInstant(value: string | null | undefined, timezone: string): string {
  if (!value) return '—';
  return new Intl.DateTimeFormat('zh-CN', {
    timeZone: timezone,
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  }).format(new Date(value));
}

function groupState(row: TopologyRow): string {
  if (row.running === null || row.total === null) return '状态未知';
  if (row.total === 0) return '无设备';
  if (row.running === row.total) return '全部运行';
  if (row.running === 0) return '全部停止';
  return `${row.running} / ${row.total} 运行`;
}

function groupStateBadgeClass(row: TopologyRow): string {
  if (row.running !== null && row.total !== null && row.total > 0 && row.running === row.total) {
    return 'border-success/30 bg-success/10 text-success';
  }
  return 'border-border bg-muted/40 text-foreground';
}

const EQUIPMENT_GROUP_ICON = {
  'cooling-tower': RadioTower,
  chiller: Snowflake,
  'chw-pump': Fan,
  'cw-pump': Fan,
  ahu: Wind,
  'vav-fcu': SlidersHorizontal,
} as const;

function OperationsEmpty({ title, description }: { readonly title: string; readonly description?: string }) {
  return (
    <Empty className="min-h-56 border-0">
      <EmptyHeader>
        <EmptyTitle>{title}</EmptyTitle>
        {description ? <EmptyDescription>{description}</EmptyDescription> : null}
      </EmptyHeader>
    </Empty>
  );
}

function Loading(_props: { readonly site: Readonly<Site> }) {
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
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-5">
        {Array.from({ length: 5 }).map((_, i) => (
          <Skeleton key={i} className="h-24 rounded-lg" />
        ))}
      </div>
      <div className="grid gap-4 xl:grid-cols-[minmax(0,1fr)_22rem]">
        <Skeleton className="h-[560px] rounded-lg" />
        <Skeleton className="h-[440px] rounded-lg" />
      </div>
    </Main>
  );
}

function OperationsInspector({
  selected,
  canReadAlarms,
  site,
  totalPowerKW,
}: {
  readonly selected?: TopologyRow;
  readonly canReadAlarms: boolean;
  readonly site: Readonly<Site>;
  readonly totalPowerKW: number;
}) {
  if (!selected) return <p className="text-sm text-muted-foreground">选择设备群查看详情。</p>;

  const GroupIcon = EQUIPMENT_GROUP_ICON[selected.key as keyof typeof EQUIPMENT_GROUP_ICON] ?? Box;
  const runningRatio = selected.total && selected.total > 0 && selected.running != null
    ? Math.round((selected.running / selected.total) * 100)
    : 0;
  const powerShare = totalPowerKW > 0 && selected.powerKW != null
    ? Math.round((selected.powerKW / totalPowerKW) * 100)
    : null;
  const avgUnitPower = selected.running && selected.running > 0 && selected.powerKW != null
    ? Math.round(selected.powerKW / selected.running)
    : null;

  return (
    <div className="space-y-5">
      <div className="border-b pb-4">
        <div className="flex items-start justify-between gap-3">
          <div className="flex min-w-0 items-center gap-3">
            <div className="flex size-9 shrink-0 items-center justify-center rounded-md border bg-muted/30 text-muted-foreground">
              <GroupIcon className="size-4.5" aria-hidden="true" />
            </div>
            <div className="min-w-0">
              <span className="text-xs text-muted-foreground">设备群</span>
              <strong className="block truncate text-base font-semibold tracking-tight text-foreground">{selected.label}</strong>
            </div>
          </div>
          <Badge variant="outline" className={cn('shrink-0 font-medium', groupStateBadgeClass(selected))} data-testid="operations-selected-state">
            {groupState(selected)}
          </Badge>
        </div>
        <div className="mt-3 flex items-center justify-between text-xs">
          <span className="text-muted-foreground">运行比例</span>
          <span className="font-semibold tabular-nums text-foreground">{runningRatio}%</span>
        </div>
      </div>

      {/* Telemetry Metrics Grid */}
      <div className="divide-y rounded-lg border bg-card">
        <div className="flex items-center justify-between gap-4 px-4 py-3">
          <span className="flex items-center gap-2 text-sm text-muted-foreground">
            <Box className="size-4 text-primary" aria-hidden="true" />
            运行设备
          </span>
          <div className="text-right">
            <strong className="text-sm font-semibold tabular-nums text-foreground">{selected.running ?? '—'} / {selected.total ?? '—'}</strong>
            <span className="ml-1 text-xs text-muted-foreground">台</span>
          </div>
        </div>
        <div className="flex items-center justify-between gap-4 px-4 py-3">
          <span className="flex items-center gap-2 text-sm text-muted-foreground">
            <Power className="size-4 text-primary" aria-hidden="true" />
            当前功率
          </span>
          <div className="text-right">
            <strong className="text-sm font-semibold tabular-nums text-foreground">{selected.powerKW == null ? '—' : `${formatNumber(selected.powerKW, 0)} kW`}</strong>
            {powerShare != null && (
              <span className="ml-1.5 text-xs text-muted-foreground tabular-nums">({powerShare}% 负荷)</span>
            )}
          </div>
        </div>
        <div className="flex items-center justify-between gap-4 px-4 py-3">
          <span className="flex items-center gap-2 text-sm text-muted-foreground">
            <Zap className="size-4 text-muted-foreground" aria-hidden="true" />
            单机平均负荷
          </span>
          <div className="text-right">
            <strong className="text-sm font-semibold tabular-nums text-foreground">{avgUnitPower == null ? '—' : `${avgUnitPower} kW/台`}</strong>
          </div>
        </div>
        <div className="flex items-center justify-between gap-4 px-4 py-3">
          <span className="flex items-center gap-2 text-sm text-muted-foreground">
            <Cpu className="size-4 text-muted-foreground" aria-hidden="true" />
            当前控制来源
          </span>
          <div className="flex items-center gap-1.5 text-xs font-medium text-muted-foreground">
            <span className="size-1.5 rounded-full bg-muted-foreground/50" aria-hidden="true" />
            未提供
          </div>
        </div>
      </div>

      {/* Alarms Section */}
      {canReadAlarms ? (
        <div className="rounded-lg border bg-card p-3.5 space-y-2">
          <div className="flex items-center justify-between">
            <div className="flex items-center gap-2 text-sm font-medium text-foreground">
              <Bell className="size-4 text-destructive" aria-hidden="true" />
              站点活动告警
            </div>
            <Button variant="ghost" size="xs" asChild className="h-7 text-xs gap-1">
              <Link to="/sites/$siteId/issues" params={{ siteId: site.id }} search={{ view: 'alarms' }}>
                查看<ArrowRight className="size-3" aria-hidden="true" />
              </Link>
            </Button>
          </div>
          <p className="text-xs text-muted-foreground">当前仅提供站点范围告警</p>
        </div>
      ) : null}

      <div className="grid grid-cols-2 gap-2 border-t pt-4">
        <Button variant="outline" size="sm" asChild className="gap-1.5">
          <Link to="/sites/$siteId/operations/trends" params={{ siteId: site.id }}>
            <ChartNoAxesColumnIncreasing className="size-3.5 text-muted-foreground" aria-hidden="true" />
            趋势
          </Link>
        </Button>
        <Button variant="outline" size="sm" asChild className="gap-1.5">
          <Link to="/sites/$siteId/devices" params={{ siteId: site.id }}>
            <Box className="size-3.5 text-muted-foreground" aria-hidden="true" />
            设备
          </Link>
        </Button>
        <Button variant="outline" size="sm" asChild className="gap-1.5">
          <Link to="/sites/$siteId/issues" params={{ siteId: site.id }} search={{ view: 'diagnostics' }}>
            <Stethoscope className="size-3.5 text-muted-foreground" aria-hidden="true" />
            诊断
          </Link>
        </Button>
        <Button variant="outline" size="sm" asChild className="gap-1.5">
          <Link to="/sites/$siteId/operations/control" params={{ siteId: site.id }}>
            <SlidersHorizontal className="size-3.5 text-muted-foreground" aria-hidden="true" />
            控制
          </Link>
        </Button>
      </div>
    </div>
  );
}

export function SystemOperations({ site, principal, searchState, onSearchChange }: SystemOperationsProps) {
  const selectedButtonRef = useRef<HTMLButtonElement>(null);
  const [inspectorOpen, setInspectorOpen] = useState(false);
  const [compactInspector, setCompactInspector] = useState(false);
  const authorizationScope = `${principal.session.id}:${principal.authorization.policyRevision}`;
  const canReadAlarms = principal.authorization.capabilities.includes('alarm.list');

  useEffect(() => {
    const media = window.matchMedia('(max-width: 1279px)');
    const update = () => setCompactInspector(media.matches);
    update();
    media.addEventListener('change', update);
    return () => media.removeEventListener('change', update);
  }, []);

  const summaryQuery = useSiteDashboardSummary(principal.context.tenantId, site.id, authorizationScope);
  const overviewQuery = useQuery({
    queryKey: ['surface-04', 'overview-projection', site.id, authorizationScope],
    queryFn: ({ signal }) => readDashboardOverview(site.id, signal),
    staleTime: 30_000,
    retry: false,
  });

  const summary = summaryQuery.data;
  const overview = overviewQuery.data;
  const summaryError = summaryQuery.isError ? presentSiteDashboardError(summaryQuery.error) : null;
  const equipmentGroups = overview?.topology ?? [];
  const selected = equipmentGroups.find((row) => row.key === searchState.group) ?? equipmentGroups[0];
  const population = summary?.devicePopulation;
  const workspacePopulation = equipmentGroups.reduce(
    (accumulator, row) => {
      if (row.running == null || row.total == null) return { ...accumulator, complete: false };
      return {
        running: accumulator.running + row.running,
        total: accumulator.total + row.total,
        complete: accumulator.complete,
      };
    },
    { running: 0, total: 0, complete: equipmentGroups.length > 0 },
  );
  const totalRunningPowerKW = equipmentGroups.reduce((sum, row) => sum + (row.powerKW ?? 0), 0);
  const currentTotalPower = summary?.fastMetrics.currentPower.value ?? totalRunningPowerKW;

  const water = overview?.waterTemperatures;
  const condenserWater = overview?.coolingWaterTemperatures;
  const deltaT = water?.supplyC != null && water?.returnC != null ? water.returnC - water.supplyC : null;
  const condenserDeltaT = condenserWater?.supplyC != null && condenserWater?.returnC != null ? condenserWater.returnC - condenserWater.supplyC : null;
  const chilledSupplyDeviation = water?.supplyC != null && water?.setpointC != null ? water.supplyC - water.setpointC : null;
  const condenserSupplyDeviation = condenserWater?.supplyC != null && condenserWater?.setpointC != null ? condenserWater.supplyC - condenserWater.setpointC : null;
  const quality = summary?.quality ?? 'UNAVAILABLE';

  const selectGroup = useCallback((key: string) => {
    onSearchChange({ group: key });
    if (compactInspector) setInspectorOpen(true);
  }, [compactInspector, onSearchChange]);

  const refresh = () => {
    void summaryQuery.refetch();
    void overviewQuery.refetch();
  };

  if (summaryQuery.isPending) return <Loading site={site} />;

  return (
    <Main fluid className="space-y-5" data-testid="system-operations" data-site-id={site.id} data-business-state={summaryError ? 'UNAVAILABLE' : quality}>
      {/* Top Command Deck Header */}
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <p className="flex items-center gap-1.5 text-xs text-muted-foreground">
            <span>{site.displayName}</span>
            <span aria-hidden="true">·</span>
            <span>{site.timezone}</span>
            <span aria-hidden="true">·</span>
            <span>更新于 {formatInstant(summary?.asOf ?? overview?.asOf, site.timezone)}</span>
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Badge
            variant="outline"
            className={quality === 'READY'
              ? 'h-8 gap-1.5 border-success/30 bg-success/10 px-2.5 text-xs text-success'
              : quality === 'ATTENTION' || quality === 'STALE' || quality === 'SUSPECT'
                ? 'h-8 gap-1.5 border-warning/35 bg-warning/10 px-2.5 text-xs text-warning'
                : 'h-8 gap-1.5 px-2.5 text-xs'}
          >
            <CircleAlert className="size-3.5" aria-hidden="true" />
            数据 {QUALITY_LABEL[quality] ?? quality}
            {population?.stale ? ` · ${population.stale} 台延迟` : ''}
          </Badge>
          <Button variant="outline" size="sm" onClick={refresh} className="h-8 gap-1.5 text-xs">
            <RefreshCw className="size-3.5" aria-hidden="true" />
            刷新
          </Button>
        </div>
      </div>

      {summaryError ? (
        <Alert variant="destructive">
          <AlertTitle>{summaryError.title}</AlertTitle>
          <AlertDescription>{summaryError.description}</AlertDescription>
        </Alert>
      ) : null}

      <FactStrip
        ariaLabel="当前运行事实"
        items={[
          {
            key: 'power',
            label: '实时运行功率',
            value: formatNumber(summary?.fastMetrics.currentPower.value, 0),
            suffix: summary?.fastMetrics.currentPower.unit ?? 'kW',
            detail: '站点 HVAC 当前总负荷',
            icon: <Zap />,
            tone: 'accent',
          },
          {
            key: 'cop',
            label: '系统能效比',
            value: formatNumber(summary?.slowMetrics.cop.value ?? overview?.kpis.averageCop, 2, 2),
            suffix: 'COP',
            detail: '当前综合制冷能效',
            icon: <ChartNoAxesColumnIncreasing />,
          },
          {
            key: 'running',
            label: '设备群运行',
            value: workspacePopulation.complete ? [workspacePopulation.running, workspacePopulation.total].join(' / ') : '—',
            suffix: '台',
            detail: '当前工作区范围',
            icon: <Box />,
          },
          {
            key: 'alarms',
            label: '活动告警',
            value: summary?.fastMetrics.openAlarms.activeCount ?? 0,
            suffix: '项',
            detail: summary?.fastMetrics.openAlarms.highestSeverity
              ? '最高 ' + (SEVERITY_LABEL[summary.fastMetrics.openAlarms.highestSeverity] ?? summary.fastMetrics.openAlarms.highestSeverity)
              : '当前无活动告警',
            icon: <Bell />,
            tone: (summary?.fastMetrics.openAlarms.activeCount ?? 0) > 0 ? 'critical' : 'default',
          },
          {
            key: 'freshness',
            label: '数据延迟',
            value: population?.stale ?? 0,
            suffix: '台',
            detail: '站点设备数据范围',
            icon: <Database />,
            tone: population?.stale ? 'warning' : 'default',
          },
        ]}
      />

      {/* Main Engineering Operations Workspace */}
      <section className="grid items-stretch gap-4 xl:grid-cols-[minmax(0,1fr)_22rem]" aria-label="系统运行工作区">
        <section className="h-full min-w-0 overflow-hidden rounded-lg border bg-card">
          <div className="border-b px-4 py-3.5">
            <div className="flex flex-col gap-1 sm:flex-row sm:items-center sm:justify-between">
              <div>
                <h2 className="flex items-center gap-2 text-base font-semibold">
                  <Settings2 className="size-4.5 text-primary" aria-hidden="true" />
                  设备与过程
                </h2>
              </div>
              <div className="flex items-center gap-2">
                <Badge variant="outline" className="text-xs font-normal text-muted-foreground">
                  <Activity className="mr-1 size-3" aria-hidden="true" />
                  实时运行状态
                </Badge>
              </div>
            </div>
          </div>

          <div>
            {/* Digital Twin Dual-Circuit Process Dynamics */}
            <div className="grid gap-3 border-b bg-muted/20 p-4 lg:grid-cols-2" aria-label="关键过程量">
              {/* Circuit 1: 冷冻水供回回路 */}
              <section className="overflow-hidden rounded-lg border bg-background" aria-labelledby="chilled-water-title">
                <div className="flex items-center justify-between border-b px-4 py-2.5">
                  <div className="flex items-center gap-2">
                    <div className="flex size-6 items-center justify-center rounded-md bg-primary/10 text-primary">
                      <Snowflake className="size-3.5" aria-hidden="true" />
                    </div>
                    <h3 id="chilled-water-title" className="text-sm font-semibold tracking-tight">冷冻水</h3>
                  </div>
                  <div className="flex items-center gap-1.5">
                    <span className="size-1.5 rounded-full bg-primary" aria-hidden="true" />
                    <span className="text-xs text-muted-foreground font-medium">供回回路</span>
                  </div>
                </div>

                <div className="p-4 space-y-3">
                  {/* Temperature Metrics Bar */}
                  <div className="grid grid-cols-2 gap-2.5">
                    <div className="rounded-lg border bg-muted/20 p-2.5">
                      <div className="flex items-center justify-between text-xs text-muted-foreground">
                        <span>供水温度</span>
                        <span className="text-[11px] text-muted-foreground tabular-nums">
                          {water?.setpointC == null ? '设定 —' : `设定 ${formatNumber(water.setpointC, 1)} °C`}
                        </span>
                      </div>
                      <div className="mt-1 flex items-baseline justify-between">
                        <strong className="text-xl font-bold tracking-tight text-primary tabular-nums">
                          {water?.supplyC == null ? '—' : `${formatNumber(water.supplyC, 1)} °C`}
                        </strong>
                        {chilledSupplyDeviation != null && (
                          <span className="text-xs font-medium tabular-nums text-muted-foreground">
                            偏差 {formatDeviation(chilledSupplyDeviation)}
                          </span>
                        )}
                      </div>
                    </div>

                    <div className="rounded-lg border bg-muted/20 p-2.5">
                      <div className="flex items-center justify-between text-xs text-muted-foreground">
                        <span>回水温度</span>
                        <span className="text-[11px] text-muted-foreground">换热端</span>
                      </div>
                      <div className="mt-1 flex items-baseline justify-between">
                        <strong className="text-xl font-bold tracking-tight text-foreground tabular-nums">
                          {water?.returnC == null ? '—' : `${formatNumber(water.returnC, 1)} °C`}
                        </strong>
                        <span className="text-xs text-muted-foreground font-normal">当前实测</span>
                      </div>
                    </div>
                  </div>

                  {/* Delta T Strip */}
                  <div className="flex items-center justify-between rounded-lg border bg-muted/10 px-3 py-2 text-xs">
                    <div className="flex items-center gap-1.5 text-muted-foreground">
                      <Gauge className="size-3.5 text-primary" aria-hidden="true" />
                      <span>ΔT（供回水温差）</span>
                    </div>
                    <div className="flex items-center gap-2">
                      <strong className="text-sm font-semibold tabular-nums text-foreground">
                        {deltaT == null ? '—' : `${formatNumber(deltaT, 1)} K`}
                      </strong>
                      <span className="text-[11px] text-muted-foreground">参考区间未接入</span>
                    </div>
                  </div>
                </div>
              </section>

              {/* Circuit 2: 冷却水散热回路 */}
              <section className="overflow-hidden rounded-lg border bg-background" aria-labelledby="condenser-water-title">
                <div className="flex items-center justify-between border-b px-4 py-2.5">
                  <div className="flex items-center gap-2">
                    <div className="flex size-6 items-center justify-center rounded-md bg-chart-2/15 text-[var(--chart-2)]">
                      <Droplets className="size-3.5" aria-hidden="true" />
                    </div>
                    <h3 id="condenser-water-title" className="text-sm font-semibold tracking-tight">冷却水</h3>
                  </div>
                  <div className="flex items-center gap-1.5">
                    <span className="size-1.5 rounded-full bg-[var(--chart-2)]" aria-hidden="true" />
                    <span className="text-xs text-muted-foreground font-medium">散热回路</span>
                  </div>
                </div>

                <div className="p-4 space-y-3">
                  {/* Temperature Metrics Bar */}
                  <div className="grid grid-cols-2 gap-2.5">
                    <div className="rounded-lg border bg-muted/20 p-2.5">
                      <div className="flex items-center justify-between text-xs text-muted-foreground">
                        <span>供水温度</span>
                        <span className="text-[11px] text-muted-foreground tabular-nums">
                          {condenserWater?.setpointC == null ? '设定 —' : `设定 ${formatNumber(condenserWater.setpointC, 1)} °C`}
                        </span>
                      </div>
                      <div className="mt-1 flex items-baseline justify-between">
                        <strong className="text-xl font-bold tracking-tight text-foreground tabular-nums">
                          {condenserWater?.supplyC == null ? '—' : `${formatNumber(condenserWater.supplyC, 1)} °C`}
                        </strong>
                        {condenserSupplyDeviation != null && (
                          <span className="text-xs font-medium tabular-nums text-muted-foreground">
                            偏差 {formatDeviation(condenserSupplyDeviation)}
                          </span>
                        )}
                      </div>
                    </div>

                    <div className="rounded-lg border bg-muted/20 p-2.5">
                      <div className="flex items-center justify-between text-xs text-muted-foreground">
                        <span>回水温度</span>
                        <span className="text-[11px] text-muted-foreground">冷却塔端</span>
                      </div>
                      <div className="mt-1 flex items-baseline justify-between">
                        <strong className="text-xl font-bold tracking-tight text-foreground tabular-nums">
                          {condenserWater?.returnC == null ? '—' : `${formatNumber(condenserWater.returnC, 1)} °C`}
                        </strong>
                        {condenserDeltaT != null && (
                          <span className="text-xs font-medium tabular-nums text-muted-foreground">
                            温差 {formatNumber(condenserDeltaT, 1)} K
                          </span>
                        )}
                      </div>
                    </div>
                  </div>

                  {/* Water Flow Status Strip */}
                  <div className="flex items-center justify-between rounded-lg border bg-muted/10 px-3 py-2 text-xs">
                    <div className="flex items-center gap-1.5 text-muted-foreground">
                      <Activity className="size-3.5 text-[var(--chart-2)]" aria-hidden="true" />
                      <span>回水温差状态</span>
                    </div>
                    <div className="flex items-center gap-1.5 text-foreground font-medium">
                      <CheckCircle2 className="size-3.5 text-muted-foreground" aria-hidden="true" />
                      <span>{condenserDeltaT == null ? '当前温差 —' : `当前温差 ${formatNumber(condenserDeltaT, 1)} K`}</span>
                    </div>
                  </div>
                </div>
              </section>
            </div>

            {/* Equipment Groups Table */}
            {overviewQuery.isPending ? (
              <OperationsEmpty title="正在加载设备群" />
            ) : overviewQuery.isError ? (
              <OperationsEmpty title="设备群数据暂不可用" />
            ) : equipmentGroups.length > 0 ? (
              <ItemGroup className="divide-y border-t" aria-label="设备群运行状态">
                {equipmentGroups.map((row) => {
                  const isSelected = row.key === selected?.key;
                  const GroupIcon = EQUIPMENT_GROUP_ICON[row.key as keyof typeof EQUIPMENT_GROUP_ICON] ?? Box;
                  const runPercent = row.total && row.total > 0 && row.running != null
                    ? Math.round((row.running / row.total) * 100)
                    : 0;
                  const loadShare = currentTotalPower > 0 && row.powerKW != null
                    ? Math.round((row.powerKW / currentTotalPower) * 100)
                    : 0;
                  return (
                    <Item
                      key={row.key}
                      asChild
                      size="sm"
                      className={cn(
                        'rounded-none px-4 py-3 hover:bg-muted/30',
                        isSelected && 'bg-muted/55 hover:bg-muted/65',
                      )}
                    >
                      <button
                        ref={isSelected ? selectedButtonRef : undefined}
                        type="button"
                        aria-pressed={isSelected}
                        data-group-key={row.key}
                        data-group-power={row.powerKW ?? ''}
                        onClick={() => selectGroup(row.key)}
                      >
                        <ItemMedia className={cn(
                          'size-9 rounded-lg border',
                          isSelected ? 'border-primary/40 bg-primary/15 text-primary' : 'border-border/60 bg-muted/40',
                        )}>
                          <GroupIcon className="size-4.5" aria-hidden="true" />
                        </ItemMedia>
                        <ItemContent>
                          <ItemTitle>{row.label}</ItemTitle>
                          <ItemDescription className="space-y-1">
                            <span className="flex items-center gap-2">
                              <span className="inline-flex items-center gap-1.5">
                                <span className={cn(
                                  'size-2 rounded-full',
                                  row.running === row.total && 'bg-success',
                                  row.running === 0 && 'bg-muted-foreground',
                                  row.running !== null && row.total !== null && row.running > 0 && row.running < row.total && 'bg-primary',
                                )} />
                                {groupState(row)}
                              </span>
                              <span className="font-mono tabular-nums">{runPercent}%</span>
                            </span>
                            <span className="block h-1.5 max-w-52 overflow-hidden rounded-full bg-muted">
                              <span
                                className={cn('block h-full rounded-full', row.running === row.total ? 'bg-success' : 'bg-primary')}
                                style={{ width: `${runPercent}%` }}
                              />
                            </span>
                          </ItemDescription>
                        </ItemContent>
                        <ItemActions className="text-right">
                          <div>
                            <div className="font-mono text-sm font-semibold tabular-nums text-foreground">
                              {row.powerKW == null ? '—' : `${formatNumber(row.powerKW, 0)} kW`}
                            </div>
                            <div className="text-xs text-muted-foreground">负荷占比 <span className="font-mono tabular-nums text-foreground">{loadShare}%</span></div>
                          </div>
                          <ArrowRight className="size-4 text-muted-foreground" aria-hidden="true" />
                        </ItemActions>
                      </button>
                    </Item>
                  );
                })}
              </ItemGroup>
            ) : (
              <OperationsEmpty title="暂无设备群数据" />
            )}
          </div>
        </section>

        {/* Desktop Inspector */}
        <aside className="hidden h-full xl:block" aria-label="运行详情">
          <section className="h-full overflow-hidden rounded-lg border bg-card">
            <div className="border-b px-4 py-3.5">
              <div className="flex items-center justify-between">
                <h2 className="flex items-center gap-2 text-base font-semibold">
                  <Layers className="size-4 text-primary" aria-hidden="true" />
                  当前选择
                </h2>
                <Badge variant="secondary" className="text-xs font-normal">
                  实时遥测
                </Badge>
              </div>
            </div>
            <div className="p-4">
              <OperationsInspector
                selected={selected}
                canReadAlarms={canReadAlarms}
                site={site}
                totalPowerKW={currentTotalPower}
              />
            </div>
          </section>
        </aside>
      </section>

      {/* Mobile / Narrow Sheet Inspector */}
      <Sheet open={inspectorOpen && compactInspector} onOpenChange={setInspectorOpen}>
        <SheetContent
          className="w-[min(520px,94vw)] sm:max-w-[520px]!"
          aria-label="运行详情"
          onCloseAutoFocus={(event) => {
            event.preventDefault();
            selectedButtonRef.current?.focus({ preventScroll: true });
          }}
        >
          <SheetHeader>
            <SheetTitle>{selected?.label ?? '运行详情'}</SheetTitle>
            <SheetDescription>{selected ? groupState(selected) : '设备群'}</SheetDescription>
          </SheetHeader>
          <div className="min-h-0 flex-1 overflow-y-auto px-4 pt-4 pb-4">
            <OperationsInspector
              selected={selected}
              canReadAlarms={canReadAlarms}
              site={site}
              totalPowerKW={currentTotalPower}
            />
          </div>
        </SheetContent>
      </Sheet>
    </Main>
  );
}
