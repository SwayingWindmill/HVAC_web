import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { ColumnDef } from '@tanstack/react-table';
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
import type { CurrentPrincipalResponse, Site } from '@/api/generated/platformGateway.gen';
import { presentSiteDashboardError, useSiteDashboardSummary } from '@/api/site-dashboard';
import { Main } from '@/components/layout/Main';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from '@/components/ui/empty';
import {
  Sheet,
  SheetBody,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet';
import { Skeleton } from '@/components/ui/skeleton';
import { DataTable, type DataTableFeatures } from '@/components/data-table';
import { useDataTable } from '@/hooks/use-data-table';
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

const EQUIPMENT_GROUP_META: Record<string, { subtitle: string; tag: string }> = {
  'cooling-tower': { subtitle: '冷却散热循环塔群', tag: '开式冷却' },
  chiller: { subtitle: '离心/螺杆式冷水主机', tag: '核心冷源' },
  'chw-pump': { subtitle: '一次/二次冷冻水循环泵', tag: '冷量输送' },
  'cw-pump': { subtitle: '冷凝器冷却水循环泵', tag: '散热循环' },
  ahu: { subtitle: '大温差组合式空气处理机组', tag: '集中空调' },
  'vav-fcu': { subtitle: '区域末端变风量/风机盘管', tag: '分区末端' },
};

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
  const meta = EQUIPMENT_GROUP_META[selected.key] ?? { subtitle: '暖通受控设备群', tag: '运行单元' };
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
      {/* Hero Header Card */}
      <div className="rounded-xl border bg-gradient-to-br from-card to-muted/30 p-4 shadow-xs">
        <div className="flex items-start justify-between gap-3">
          <div className="flex items-center gap-3 min-w-0">
            <div className="flex size-11 shrink-0 items-center justify-center rounded-lg border bg-primary/10 text-primary">
              <GroupIcon className="size-5.5" aria-hidden="true" />
            </div>
            <div className="min-w-0">
              <div className="flex items-center gap-1.5">
                <span className="text-xs text-muted-foreground font-medium">设备群</span>
                <span className="text-[10px] rounded bg-muted px-1.5 py-0.5 text-muted-foreground">{meta.tag}</span>
              </div>
              <strong className="block truncate text-base font-semibold tracking-tight text-foreground">{selected.label}</strong>
              <span className="block truncate text-xs text-muted-foreground">{meta.subtitle}</span>
            </div>
          </div>
          <Badge variant="outline" className={cn('shrink-0 font-medium', groupStateBadgeClass(selected))} data-testid="operations-selected-state">
            {groupState(selected)}
          </Badge>
        </div>

        {/* Running progress bar */}
        <div className="mt-3.5 space-y-1.5 border-t border-border/50 pt-3">
          <div className="flex items-center justify-between text-xs">
            <span className="text-muted-foreground">在网运行率</span>
            <span className="font-semibold text-foreground tabular-nums">{runningRatio}%</span>
          </div>
          <div className="h-1.5 w-full overflow-hidden rounded-full bg-muted">
            <div
              className={cn('h-full rounded-full transition-all duration-300', selected.running === selected.total ? 'bg-success' : 'bg-primary')}
              style={{ width: `${runningRatio}%` }}
            />
          </div>
        </div>
      </div>

      {/* Telemetry Metrics Grid */}
      <div className="divide-y rounded-xl border bg-card shadow-xs">
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
            控制策略模式
          </span>
          <div className="flex items-center gap-1.5 text-xs font-medium text-foreground">
            <span className="size-1.5 rounded-full bg-success" aria-hidden="true" />
            自动联动控制
          </div>
        </div>
      </div>

      {/* Alarms Section */}
      {canReadAlarms ? (
        <div className="rounded-xl border bg-card p-3.5 shadow-xs space-y-2">
          <div className="flex items-center justify-between">
            <div className="flex items-center gap-2 text-sm font-medium text-foreground">
              <Bell className="size-4 text-destructive" aria-hidden="true" />
              站点活动告警
            </div>
            <Button variant="ghost" size="xs" asChild className="h-7 text-xs gap-1">
              <Link to="/sites/$siteId/alarms" params={{ siteId: site.id }}>
                查看<ArrowRight className="size-3" aria-hidden="true" />
              </Link>
            </Button>
          </div>
          <p className="text-xs text-muted-foreground">当前仅提供站点范围告警</p>
        </div>
      ) : null}

      {/* Action Links */}
      <div className="space-y-2 border-t pt-4">
        <div className="grid grid-cols-2 gap-2">
          <Button variant="outline" size="sm" asChild className="gap-1.5">
            <Link to="/sites/$siteId/diagnostics" params={{ siteId: site.id }}>
              <Stethoscope className="size-3.5 text-muted-foreground" aria-hidden="true" />
              诊断
            </Link>
          </Button>
          <Button variant="outline" size="sm" asChild className="gap-1.5">
            <Link to="/sites/$siteId/control" params={{ siteId: site.id }}>
              <SlidersHorizontal className="size-3.5 text-muted-foreground" aria-hidden="true" />
              控制
            </Link>
          </Button>
        </div>
        <Button variant="ghost" size="sm" asChild className="w-full text-xs text-muted-foreground hover:text-foreground">
          <Link to="/sites/$siteId/devices" params={{ siteId: site.id }}>
            前往设备中心查看资产台账
            <ArrowRight className="ml-1 size-3" aria-hidden="true" />
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

  const equipmentColumns = useMemo<Array<ColumnDef<DataTableFeatures, TopologyRow>>>(() => [
    {
      id: 'group',
      header: '设备群',
      cell: ({ row }) => {
        const isSelected = row.original.key === selected?.key;
        const GroupIcon = EQUIPMENT_GROUP_ICON[row.original.key as keyof typeof EQUIPMENT_GROUP_ICON] ?? Box;
        const meta = EQUIPMENT_GROUP_META[row.original.key] ?? { subtitle: '暖通受控设备', tag: '受控群' };
        return (
          <div className="flex items-center gap-3">
            <div className={cn(
              'size-9 shrink-0 rounded-lg border flex items-center justify-center transition-colors',
              isSelected ? 'border-primary/40 bg-primary/15 text-primary' : 'border-border/60 bg-muted/40 text-muted-foreground group-hover/row:text-foreground',
            )}>
              <GroupIcon className="size-4.5" aria-hidden="true" />
            </div>
            <div className="min-w-0">
              <div className="flex items-center gap-2">
                <span className="text-sm font-semibold tracking-tight text-foreground">{row.original.label}</span>
                <Badge variant="secondary" className="h-4 px-1.5 py-0 text-[10px] font-normal">{meta.tag}</Badge>
              </div>
              <div className="mt-0.5 truncate text-xs text-muted-foreground">{meta.subtitle}</div>
            </div>
          </div>
        );
      },
    },
    {
      id: 'running',
      header: '运行状态',
      cell: ({ row }) => {
        const runPercent = row.original.total && row.original.total > 0 && row.original.running != null
          ? Math.round((row.original.running / row.original.total) * 100)
          : 0;
        return (
          <div className="max-w-[200px] space-y-1.5">
            <div className="flex items-center justify-between text-xs">
              <span className="inline-flex items-center gap-1.5 font-medium">
                <span className={cn(
                  'size-2 shrink-0 rounded-full',
                  row.original.running === row.original.total && 'bg-success',
                  row.original.running === 0 && 'bg-muted-foreground',
                  row.original.running !== null && row.original.total !== null && row.original.running > 0 && row.original.running < row.original.total && 'bg-primary',
                )} aria-hidden="true" />
                {groupState(row.original)}
              </span>
              <span className="font-mono text-[11px] text-muted-foreground tabular-nums">{runPercent}%</span>
            </div>
            <div className="h-1.5 w-full overflow-hidden rounded-full bg-muted">
              <div
                className={cn('h-full rounded-full transition-all duration-300', row.original.running === row.original.total ? 'bg-success' : 'bg-primary')}
                style={{ width: `${runPercent}%` }}
              />
            </div>
          </div>
        );
      },
    },
    {
      id: 'power',
      header: '功率 / 负荷',
      cell: ({ row }) => {
        const loadShare = currentTotalPower > 0 && row.original.powerKW != null
          ? Math.round((row.original.powerKW / currentTotalPower) * 100)
          : 0;
        return (
          <div className="space-y-1 text-right">
            <div className="font-mono text-sm font-semibold text-foreground tabular-nums">
              {row.original.powerKW == null ? '—' : `${formatNumber(row.original.powerKW, 0)} kW`}
            </div>
            <div className="flex items-center justify-end gap-1.5 text-xs text-muted-foreground">
              <span>负荷占比</span>
              <span className="font-mono font-medium text-foreground tabular-nums">{loadShare}%</span>
            </div>
          </div>
        );
      },
    },
    {
      id: 'action',
      header: '操作',
      cell: ({ row }) => {
        const isSelected = row.original.key === selected?.key;
        return (
          <Button
            ref={isSelected ? selectedButtonRef : undefined}
            variant={isSelected ? 'secondary' : 'ghost'}
            size="xs"
            aria-pressed={isSelected}
            onClick={(event) => {
              event.stopPropagation();
              selectGroup(row.original.key);
            }}
            onKeyDown={(event) => event.stopPropagation()}
            aria-label={isSelected ? `查看${row.original.label}（当前已选择）` : `查看${row.original.label}`}
            className="h-7 gap-1 text-xs"
          >
            查看<ArrowRight className="size-3.5" aria-hidden="true" />
          </Button>
        );
      },
      enableSorting: false,
    },
  ], [currentTotalPower, selectGroup, selected?.key]);

  const equipmentTable = useDataTable({
    key: `system-operations-equipment-${site.id}`,
    data: [...equipmentGroups],
    columns: equipmentColumns,
    paginate: false,
    getRowId: (row) => row.key,
  });

  if (summaryQuery.isPending) return <Loading site={site} />;

  return (
    <Main fluid className="space-y-5" data-testid="system-operations" data-site-id={site.id} data-business-state={summaryError ? 'UNAVAILABLE' : quality}>
      {/* Top Command Deck Header */}
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <div className="flex items-center gap-2.5">
            <h1 className="text-xl font-bold tracking-tight text-foreground sm:text-2xl">系统运行</h1>
            <Badge variant="outline" className="hidden sm:inline-flex items-center gap-1.5 border-primary/30 bg-primary/10 text-primary text-xs font-normal">
              <span className="size-1.5 rounded-full bg-primary animate-pulse" aria-hidden="true" />
              群控自动优化模式
            </Badge>
          </div>
          <p className="mt-0.5 flex items-center gap-1.5 text-xs text-muted-foreground">
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

      {/* 5-Card Operational KPI Ribbon */}
      <section aria-label="当前运行上下文" className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-5">
        <Card className="shadow-xs transition-shadow hover:shadow-sm">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">实时运行功率</CardTitle>
            <div className="flex size-7 items-center justify-center rounded-md bg-primary/10 text-primary">
              <Zap className="size-4" aria-hidden="true" />
            </div>
          </CardHeader>
          <CardContent className="space-y-1">
            <div className="text-2xl font-bold tracking-tight tabular-nums" data-testid="operations-current-power">
              {formatNumber(summary?.fastMetrics.currentPower.value, 0)}
              <span className="ml-1 text-sm font-normal text-muted-foreground">{summary?.fastMetrics.currentPower.unit ?? 'kW'}</span>
            </div>
            <p className="text-xs text-muted-foreground">
              站点 HVAC 实时总负荷
            </p>
          </CardContent>
        </Card>

        <Card className="shadow-xs transition-shadow hover:shadow-sm">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">系统能效比 (COP)</CardTitle>
            <div className="flex size-7 items-center justify-center rounded-md bg-chart-2/15 text-[var(--chart-2)]">
              <ChartNoAxesColumnIncreasing className="size-4" aria-hidden="true" />
            </div>
          </CardHeader>
          <CardContent className="space-y-1">
            <div className="text-2xl font-bold tracking-tight tabular-nums text-foreground">
              {formatNumber(summary?.slowMetrics.cop.value ?? overview?.kpis.averageCop, 2, 2)}
              <span className="ml-1 text-sm font-normal text-muted-foreground">COP</span>
            </div>
            <p className="text-xs text-muted-foreground">
              站点 HVAC 综合制冷能效
            </p>
          </CardContent>
        </Card>

        <dl aria-label="运行状态摘要" className="contents">
          <Card className="shadow-xs transition-shadow hover:shadow-sm">
            <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
              <dt className="text-sm font-medium text-muted-foreground">设备群运行</dt>
              <div className="flex size-7 items-center justify-center rounded-md bg-chart-1/15 text-[var(--chart-1)]">
                <Box className="size-4" aria-hidden="true" />
              </div>
            </CardHeader>
            <CardContent className="space-y-1">
              <dd className="text-2xl font-bold tracking-tight tabular-nums m-0">
                <span data-testid="operations-workspace-population">
                  {workspacePopulation.complete ? `${workspacePopulation.running} / ${workspacePopulation.total}` : '—'}
                </span>
                <span className="ml-1 text-sm font-normal text-muted-foreground">台</span>
              </dd>
              <p className="text-xs text-muted-foreground m-0">
                当前工作区范围
              </p>
            </CardContent>
          </Card>

          <Card className="shadow-xs transition-shadow hover:shadow-sm">
            <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
              <dt className="text-sm font-medium text-muted-foreground">活动告警</dt>
              <div className={cn('flex size-7 items-center justify-center rounded-md', (summary?.fastMetrics.openAlarms.activeCount ?? 0) > 0 ? 'bg-destructive/10 text-destructive' : 'bg-muted text-muted-foreground')}>
                <Bell className="size-4" aria-hidden="true" />
              </div>
            </CardHeader>
            <CardContent className="space-y-1">
              <dd className="text-2xl font-bold tracking-tight tabular-nums m-0">
                {summary?.fastMetrics.openAlarms.activeCount ?? 0}
                <span className="ml-1 text-sm font-normal text-muted-foreground">项</span>
              </dd>
              <p className="text-xs text-muted-foreground m-0 truncate">
                {summary?.fastMetrics.openAlarms.highestSeverity ? `最高 ${SEVERITY_LABEL[summary.fastMetrics.openAlarms.highestSeverity] ?? summary.fastMetrics.openAlarms.highestSeverity}` : '当前无告警'}
              </p>
            </CardContent>
          </Card>

          <Card className="shadow-xs transition-shadow hover:shadow-sm">
            <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
              <dt className="text-sm font-medium text-muted-foreground">数据通信质量</dt>
              <div className="flex size-7 items-center justify-center rounded-md bg-muted text-muted-foreground">
                <Database className="size-4" aria-hidden="true" />
              </div>
            </CardHeader>
            <CardContent className="space-y-1">
              <dd className={cn('text-2xl font-bold tracking-tight tabular-nums m-0', population?.stale ? 'text-warning' : 'text-foreground')}>
                {population ? `${population.stale}` : '0'}
                <span className="ml-1 text-sm font-normal text-muted-foreground">台延迟</span>
              </dd>
              <p className="text-xs text-muted-foreground m-0">
                站点设备数据范围
              </p>
            </CardContent>
          </Card>
        </dl>
      </section>

      {/* Main Engineering Operations Workspace */}
      <section className="grid items-stretch gap-4 xl:grid-cols-[minmax(0,1fr)_22rem]" aria-label="系统运行工作区">
        <Card className="h-full min-w-0 shadow-xs">
          <CardHeader className="border-b bg-muted/10 pb-3">
            <div className="flex flex-col gap-1 sm:flex-row sm:items-center sm:justify-between">
              <div>
                <CardTitle className="flex items-center gap-2 text-base font-semibold">
                  <Settings2 className="size-4.5 text-primary" aria-hidden="true" />
                  设备与过程
                </CardTitle>
                <CardDescription className="text-xs text-muted-foreground">
                  {equipmentGroups.length > 0 ? `${equipmentGroups.length} 个设备群 · 当前过程量` : '站点 HVAC'}
                </CardDescription>
              </div>
              <div className="flex items-center gap-2">
                <Badge variant="outline" className="text-xs font-normal text-muted-foreground">
                  <Activity className="mr-1 size-3 text-success" aria-hidden="true" />
                  自动控制运行中
                </Badge>
              </div>
            </div>
          </CardHeader>

          <CardContent className="p-0">
            {/* Digital Twin Dual-Circuit Process Dynamics */}
            <div className="grid gap-3 border-b bg-muted/20 p-4 lg:grid-cols-2" aria-label="关键过程量">
              {/* Circuit 1: 冷冻水供回回路 */}
              <section className="overflow-hidden rounded-xl border bg-card shadow-xs transition-shadow hover:shadow-sm" aria-labelledby="chilled-water-title">
                <div className="flex items-center justify-between border-b bg-gradient-to-r from-muted/30 to-background px-4 py-2.5">
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
                        <span className="text-xs text-muted-foreground font-normal">工况平稳</span>
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
                      <span className="text-[11px] text-success bg-success/10 px-1.5 py-0.5 rounded font-medium">设计区间正常</span>
                    </div>
                  </div>
                </div>
              </section>

              {/* Circuit 2: 冷却水散热回路 */}
              <section className="overflow-hidden rounded-xl border bg-card shadow-xs transition-shadow hover:shadow-sm" aria-labelledby="condenser-water-title">
                <div className="flex items-center justify-between border-b bg-gradient-to-r from-muted/30 to-background px-4 py-2.5">
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
                      <CheckCircle2 className="size-3.5 text-success" aria-hidden="true" />
                      <span>循环水流稳定</span>
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
              <div className="rounded-md border">
                <DataTable
                  table={equipmentTable}
                  className="gap-0"
                  tableAriaLabel="设备群运行状态"
                  getHeaderRowProps={() => ({ className: 'border-b bg-muted/20 hover:bg-transparent' })}
                  getHeaderCellProps={(header) => ({
                    className:
                      header.id === 'group' ? 'w-[38%] text-xs font-semibold' :
                      header.id === 'running' ? 'w-[28%] text-xs font-semibold' :
                      header.id === 'power' ? 'w-[22%] text-right text-xs font-semibold' :
                      'w-[12%] text-right text-xs font-semibold',
                  })}
                  getRowProps={(row) => {
                    const isSelected = row.original.key === selected?.key;
                    return {
                      tabIndex: 0,
                      'aria-selected': isSelected,
                      'data-state': isSelected ? 'selected' : undefined,
                      'data-group-key': row.original.key,
                      'data-group-power': row.original.powerKW ?? '',
                      className: cn(
                        'group/row cursor-pointer transition-colors focus:outline-none focus:ring-2 focus:ring-inset focus:ring-ring',
                        isSelected ? 'border-l-3 border-l-primary bg-muted/55 hover:bg-muted/65' : 'hover:bg-muted/30',
                      ),
                      onClick: () => selectGroup(row.original.key),
                      onKeyDown: (event) => {
                        if (event.key !== 'Enter' && event.key !== ' ') return;
                        event.preventDefault();
                        selectGroup(row.original.key);
                      },
                    };
                  }}
                  getCellProps={(cell) => ({
                    className:
                      cell.column.id === 'power' || cell.column.id === 'action'
                        ? 'py-3 text-right'
                        : 'py-3',
                  })}
                />
              </div>
            ) : (
              <OperationsEmpty title="暂无设备群数据" />
            )}
          </CardContent>
        </Card>

        {/* Desktop Inspector */}
        <aside className="hidden h-full xl:block" aria-label="运行详情">
          <Card className="h-full shadow-xs">
            <CardHeader className="border-b bg-muted/10 pb-3">
              <div className="flex items-center justify-between">
                <CardTitle className="flex items-center gap-2 text-base font-semibold">
                  <Layers className="size-4 text-primary" aria-hidden="true" />
                  当前选择
                </CardTitle>
                <Badge variant="secondary" className="text-xs font-normal">
                  实时遥测
                </Badge>
              </div>
            </CardHeader>
            <CardContent className="pt-4">
              <OperationsInspector
                selected={selected}
                canReadAlarms={canReadAlarms}
                site={site}
                totalPowerKW={currentTotalPower}
              />
            </CardContent>
          </Card>
        </aside>
      </section>

      {/* Mobile / Narrow Sheet Inspector */}
      <Sheet open={inspectorOpen && compactInspector} onOpenChange={setInspectorOpen}>
        <SheetContent
          className="w-[min(520px,94vw)]"
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
          <SheetBody>
            <OperationsInspector
              selected={selected}
              canReadAlarms={canReadAlarms}
              site={site}
              totalPowerKW={currentTotalPower}
            />
          </SheetBody>
        </SheetContent>
      </Sheet>
    </Main>
  );
}
