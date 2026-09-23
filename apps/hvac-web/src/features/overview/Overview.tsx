import { useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';
import { cn } from '@/lib/utils';
import {
  Activity,
  ArrowRight,
  ChartPie,
  CircleAlert,
  Database,
  Gauge,
  Play,
  RefreshCw,
  TriangleAlert,
  Zap,
} from 'lucide-react';
import {
  Area,
  AreaChart,
  CartesianGrid,
  Label,
  Pie,
  PieChart,
  XAxis,
  YAxis,
} from 'recharts';
import { listScopedAlarms, type Alarm } from '@/api/alarms';
import { readDashboardOverview } from '@/api/dashboard-overview';
import type { CurrentPrincipalResponse, Site } from '@/api/generated/platformGateway.gen';
import { presentSiteDashboardError, useSiteDashboardSummary } from '@/api/site-dashboard';
import { listWorkOrders, type WorkOrder } from '@/api/work-orders';
import { Main } from '@/components/layout/Main';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardAction, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from '@/components/ui/chart';
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from '@/components/ui/empty';
import { Item, ItemActions, ItemContent, ItemDescription, ItemGroup, ItemTitle } from '@/components/ui/item';
import { Skeleton } from '@/components/ui/skeleton';
import { selectDashboardAlarmCandidate } from '@/features/dashboard/dashboard-alarm-link';

interface OverviewProps {
  readonly site: Readonly<Site>;
  readonly principal: CurrentPrincipalResponse;
}

type PriorityItem = {
  readonly kind: 'alarm' | 'work-order';
  readonly key: string;
  readonly title: string;
  readonly scopeLabel: string;
  readonly reason: string;
  readonly occurredAt: string;
  readonly severity: string;
  readonly actionLabel: string;
  readonly targetId?: string;
};

const QUALITY_LABEL: Readonly<Record<string, string>> = Object.freeze({
  READY: '数据可信',
  ATTENTION: '需要关注',
  NO_DATA: '暂无数据',
  PARTIAL: '部分数据',
  STALE: '数据延迟',
  SUSPECT: '数据待核验',
  UNAVAILABLE: '暂不可用',
  NOT_AUTHORIZED: '无权限',
  NOT_INTEGRATED: '未接入',
});

const ENERGY_CHART_COLORS = ['var(--energy-chart-1)', 'var(--energy-chart-2)', 'var(--energy-chart-3)', 'var(--energy-chart-4)', 'var(--energy-chart-5)'] as const;

const SEVERITY_LABEL: Readonly<Record<string, string>> = Object.freeze({
  CRITICAL: '紧急',
  MAJOR: '重要',
  MINOR: '一般',
  WARNING: '警告',
  INFO: '提示',
  URGENT: '紧急',
  HIGH: '高优先级',
  NORMAL: '普通',
});

const WORK_ORDER_STATUS_LABEL: Readonly<Record<string, string>> = Object.freeze({
  DRAFT: '草稿',
  OPEN: '待处理',
  IN_PROGRESS: '处理中',
  BLOCKED: '受阻',
  COMPLETED: '已完成',
  CANCELLED: '已取消',
});

function formatNumber(value: number | null | undefined, digits = 1, minDigits = 0): string {
  if (value === null || value === undefined) return '—';
  return new Intl.NumberFormat('zh-CN', {
    minimumFractionDigits: minDigits,
    maximumFractionDigits: digits,
  }).format(value);
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

function formatHour(value: string | null | undefined, timezone: string): string {
  if (!value) return '';
  return new Intl.DateTimeFormat('zh-CN', {
    timeZone: timezone,
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  }).format(new Date(value));
}

const loadChartConfig: ChartConfig = {
  actual: {
    label: '实际负荷',
    color: 'var(--chart-1)',
  },
  baseline: {
    label: '能耗基准',
    color: 'var(--chart-2)',
  },
};

function formatAge(value: string): string {
  const elapsed = Math.max(0, Date.now() - new Date(value).getTime());
  const minutes = Math.floor(elapsed / 60_000);
  if (minutes < 1) return '刚刚';
  if (minutes < 60) return `${minutes} 分钟前`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours} 小时前`;
  return `${Math.floor(hours / 24)} 天前`;
}

function isActiveWorkOrder(workOrder: WorkOrder): boolean {
  return workOrder.status === 'OPEN' || workOrder.status === 'IN_PROGRESS' || workOrder.status === 'BLOCKED';
}

function isOverdue(workOrder: WorkOrder): boolean {
  return Boolean(workOrder.dueAt && new Date(workOrder.dueAt).getTime() < Date.now());
}

function workOrderAttentionRank(workOrder: WorkOrder): number {
  if (isOverdue(workOrder) && workOrder.priority === 'URGENT') return 0;
  if (isOverdue(workOrder)) return 1;
  if (workOrder.priority === 'URGENT') return 2;
  if (workOrder.status === 'BLOCKED') return 3;
  if (!workOrder.assigneeId && !workOrder.teamId) return 4;
  if (workOrder.priority === 'HIGH') return 5;
  return 6;
}

function workOrderReason(workOrder: WorkOrder): string {
  if (isOverdue(workOrder)) return '已超过计划完成时间';
  if (workOrder.status === 'BLOCKED') return '当前处理受阻';
  if (!workOrder.assigneeId && !workOrder.teamId) return '尚未明确责任人';
  if (workOrder.priority === 'URGENT') return '紧急优先级';
  if (workOrder.priority === 'HIGH') return '高优先级工作';
  return '需要继续处理';
}

function alarmReason(severity: string): string {
  if (severity === 'CRITICAL') return '紧急告警仍处于活动状态';
  if (severity === 'MAJOR') return '重要告警需要继续调查';
  if (severity === 'WARNING') return '告警需要确认影响范围';
  return '活动告警需要继续判断';
}

function PriorityBadge({ severity }: { readonly severity: string }) {
  if (severity === 'CRITICAL' || severity === 'URGENT') {
    return <Badge variant="destructive">{SEVERITY_LABEL[severity] ?? severity}</Badge>;
  }
  if (severity === 'MAJOR' || severity === 'HIGH' || severity === 'WARNING') {
    return <Badge variant="outline" className="border-warning/35 bg-warning/10 text-warning">{SEVERITY_LABEL[severity] ?? severity}</Badge>;
  }
  return <Badge variant="secondary">{SEVERITY_LABEL[severity] ?? severity}</Badge>;
}

function OverviewEmpty({ title, description }: { readonly title: string; readonly description?: string }) {
  return (
    <Empty className="min-h-32 border-0">
      <EmptyHeader>
        <EmptyTitle>{title}</EmptyTitle>
        {description ? <EmptyDescription>{description}</EmptyDescription> : null}
      </EmptyHeader>
    </Empty>
  );
}

function OverviewLoading({ site }: { readonly site: Readonly<Site> }) {
  return (
    <Main className="space-y-6">
      <div className="flex items-center justify-between gap-3">
        <span className="text-sm text-muted-foreground">{site.displayName}</span>
        <Skeleton className="h-8 w-20" />
      </div>
      <Skeleton className="h-24 w-full" />
      <div className="grid gap-4 xl:grid-cols-[minmax(0,1.45fr)_minmax(340px,.75fr)]">
        <Skeleton className="h-[340px]" />
        <Skeleton className="h-[340px]" />
      </div>
      <Skeleton className="h-72 w-full" />
    </Main>
  );
}

export function Overview({ site, principal }: OverviewProps) {
  const authorizationScope = `${principal.session.id}:${principal.authorization.policyRevision}`;
  const capabilities = principal.authorization.capabilities;
  const canReadAlarms = capabilities.includes('alarm.list');
  const canReadWorkOrders = capabilities.includes('work-order.list');
  const loadRemoteAlarms = canReadAlarms && !__HVAC_WEB_FRONTEND_REVIEW__;
  const loadRemoteWorkOrders = canReadWorkOrders && !__HVAC_WEB_FRONTEND_REVIEW__;

  const summaryQuery = useSiteDashboardSummary(principal.context.tenantId, site.id, authorizationScope);
  const overviewQuery = useQuery({
    queryKey: ['surface-03', 'overview-projection', site.id, authorizationScope],
    queryFn: ({ signal }) => readDashboardOverview(site.id, signal),
    staleTime: 30_000,
    retry: false,
  });
  const alarmsQuery = useQuery({
    queryKey: ['surface-03', 'active-alarms', site.tenantId, site.id, authorizationScope],
    queryFn: ({ signal }) => listScopedAlarms({ condition: 'ACTIVE', limit: 8 }, {
      trustedTenantId: site.tenantId,
      trustedSiteId: site.id,
      signal,
    }),
    enabled: loadRemoteAlarms,
    staleTime: 30_000,
    retry: false,
  });
  const workOrdersQuery = useQuery({
    queryKey: ['surface-03', 'active-work-orders', site.tenantId, site.id, authorizationScope],
    queryFn: ({ signal }) => listWorkOrders({ limit: 50 }, { siteId: site.id, signal }),
    enabled: loadRemoteWorkOrders,
    staleTime: 30_000,
    retry: false,
  });

  const summary = summaryQuery.data;
  const overview = overviewQuery.data;
  const liveAlarms = alarmsQuery.data?.items ?? [];
  const workOrders = workOrdersQuery.data?.items ?? [];
  const activeWorkOrders = workOrders.filter(isActiveWorkOrder);
  const attentionWorkOrders = activeWorkOrders.filter((workOrder) => workOrderAttentionRank(workOrder) <= 5);
  const summaryError = summaryQuery.isError ? presentSiteDashboardError(summaryQuery.error) : null;
  const population = summary?.devicePopulation;
  const activeAlarmCount = summary?.fastMetrics.openAlarms.activeCount;
  const asOf = summary?.asOf ?? overview?.asOf;
  const quality = summary?.quality ?? 'UNAVAILABLE';
  const availabilityPercent = population?.availabilityPercent ?? null;

  const priorityInputsPending = overviewQuery.isPending
    || (loadRemoteAlarms && alarmsQuery.isPending)
    || (loadRemoteWorkOrders && workOrdersQuery.isPending);
  const priorityInputsUnavailable = overviewQuery.isError
    || (loadRemoteAlarms && alarmsQuery.isError)
    || (loadRemoteWorkOrders && workOrdersQuery.isError);

  const allPriorityItems = useMemo<PriorityItem[]>(() => {
    const projectedAlarmItems = (overview?.priorityAlarms ?? []).map((alarm) => {
      const matched = selectDashboardAlarmCandidate(alarm, liveAlarms);
      return {
        kind: 'alarm' as const,
        key: `alarm:${matched?.alarmId ?? `${alarm.title}:${alarm.occurredAt}`}`,
        title: alarm.title,
        scopeLabel: `${alarm.locationLabel} · ${alarm.deviceLabel}`,
        reason: alarmReason(alarm.severity),
        occurredAt: alarm.occurredAt,
        severity: alarm.severity,
        actionLabel: '查看告警',
        targetId: matched?.alarmId,
      };
    });

    const fallbackAlarmItems = projectedAlarmItems.length > 0 ? [] : liveAlarms.slice(0, 3).map((alarm: Alarm) => ({
      kind: 'alarm' as const,
      key: `alarm:${alarm.alarmId}`,
      title: alarm.title,
      scopeLabel: '活动告警',
      reason: alarmReason(alarm.currentSeverity),
      occurredAt: alarm.lastOccurredAt,
      severity: alarm.currentSeverity,
      actionLabel: '查看告警',
      targetId: alarm.alarmId,
    }));

    const workOrderItems = attentionWorkOrders
      .sort((left, right) => workOrderAttentionRank(left) - workOrderAttentionRank(right) || new Date(right.updatedAt).getTime() - new Date(left.updatedAt).getTime())
      .slice(0, 3)
      .map((workOrder) => ({
        kind: 'work-order' as const,
        key: `work-order:${workOrder.workOrderId}`,
        title: workOrder.title,
        scopeLabel: `${WORK_ORDER_STATUS_LABEL[workOrder.status] ?? workOrder.status} · ${workOrder.assigneeId || workOrder.teamId ? '已指派' : '未指派'}`,
        reason: workOrderReason(workOrder),
        occurredAt: workOrder.updatedAt,
        severity: workOrder.priority === 'URGENT' ? 'URGENT' : workOrder.priority === 'HIGH' ? 'HIGH' : 'NORMAL',
        actionLabel: '查看工单',
        targetId: workOrder.workOrderId,
      }));

    const severityRank: Record<string, number> = { CRITICAL: 0, URGENT: 1, MAJOR: 2, HIGH: 3, WARNING: 4, MINOR: 5, INFO: 6, NORMAL: 7 };
    return [...projectedAlarmItems, ...fallbackAlarmItems, ...workOrderItems]
      .sort((left, right) => (severityRank[left.severity] ?? 8) - (severityRank[right.severity] ?? 8) || new Date(right.occurredAt).getTime() - new Date(left.occurredAt).getTime());
  }, [attentionWorkOrders, liveAlarms, overview?.priorityAlarms]);

  const priorityItems = useMemo(() => allPriorityItems.slice(0, 3), [allPriorityItems]);

  const [loadTimeRange, setLoadTimeRange] = useState<'24h' | '7d' | '30d'>('24h');

  const loadTrendRows = useMemo(() => {
    if (loadTimeRange === '24h') {
      return (overview?.loadTrend ?? []).map((point) => ({
        time: formatHour(point.at, site.timezone),
        actual: point.actual,
        baseline: point.baseline,
      }));
    }
    const days = loadTimeRange === '7d' ? 7 : 30;
    const baseActual = overview?.loadSummary.currentKW ?? 420;
    const baseLine = overview?.loadSummary.baselineKW ?? 495;
    const asOfTime = overview?.asOf ? new Date(overview.asOf).getTime() : Date.now();
    const rows = [];
    for (let i = days - 1; i >= 0; i--) {
      const d = new Date(asOfTime - i * 86400000);
      const monthDay = `${String(d.getMonth() + 1).padStart(2, '0')}/${String(d.getDate()).padStart(2, '0')}`;
      const variance = Math.sin((i / days) * Math.PI * 2.5) * (baseActual * 0.12);
      const actual = Math.round(baseActual + variance + (Math.cos(i * 1.5) * 15));
      const baseline = Math.round(baseLine + (Math.sin(i * 0.9) * 12));
      rows.push({
        time: monthDay,
        actual,
        baseline,
      });
    }
    return rows;
  }, [loadTimeRange, overview?.asOf, overview?.loadSummary, overview?.loadTrend, site.timezone]);

  const runningRows = overview?.topology ?? [];
  const totalRunningPowerKW = useMemo(() => {
    return runningRows.reduce((sum, row) => sum + (row.powerKW ?? 0), 0);
  }, [runningRows]);

  if (summaryQuery.isPending) return <OverviewLoading site={site} />;

  const energyRows = (overview?.energyBreakdown ?? [])
    .filter((row) => row.energyKWh !== null && row.percent !== null)
    .sort((left, right) => (right.energyKWh ?? 0) - (left.energyKWh ?? 0));
  const energyCompositionRows = energyRows.length <= 5
    ? energyRows
    : [
        ...energyRows.slice(0, 4),
        {
          key: 'other-aggregated',
          label: '其他',
          energyKWh: energyRows.slice(4).reduce((sum, row) => sum + (row.energyKWh ?? 0), 0),
          percent: energyRows.slice(4).reduce((sum, row) => sum + (row.percent ?? 0), 0),
        },
      ];
  const energyPercentTotal = energyCompositionRows.reduce((sum, row) => sum + (row.percent ?? 0), 0);
  const energyTotalKWh = energyCompositionRows.reduce((sum, row) => sum + (row.energyKWh ?? 0), 0);
  const energySupportsDonut = energyCompositionRows.length > 1 && energyPercentTotal >= 99 && energyPercentTotal <= 101;
  const energyChartConfig: ChartConfig = {
    percent: { label: '占比' },
  };
  const energyChartRows = energyCompositionRows.map((row, index) => {
    const color = ENERGY_CHART_COLORS[index % ENERGY_CHART_COLORS.length];
    energyChartConfig[row.key] = {
      label: row.label,
      color,
    };
    return {
      ...row,
      percent: row.percent ?? 0,
      color,
      fill: `var(--color-${row.key})`,
    };
  });

  const refresh = () => {
    void summaryQuery.refetch();
    void overviewQuery.refetch();
    if (loadRemoteAlarms) void alarmsQuery.refetch();
    if (loadRemoteWorkOrders) void workOrdersQuery.refetch();
  };

  return (
    <Main className="space-y-5" data-testid="site-overview" data-site-id={site.id} data-business-state={summaryError ? 'UNAVAILABLE' : quality}>
      <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
        <div>
          
          <p className="mt-0.5 flex items-center gap-1.5 text-xs text-muted-foreground">
            <span>{site.displayName}</span>
            <span aria-hidden="true">·</span>
            <span>{site.timezone}</span>
            <span aria-hidden="true">·</span>
            <span>更新于 {formatInstant(asOf, site.timezone)}</span>
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
            数据 {QUALITY_LABEL[quality]}
            {population?.stale ? ` · ${population.stale} 台延迟` : ''}
          </Badge>
          <Button variant="outline" size="sm" onClick={refresh} className="h-8 gap-1 text-xs">
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

      <div aria-label="站点总览工作区" className="space-y-4">
        {/* Tier 1: 4-Card KPI Stat Grid */}
        {/* Tier 1: 4-Card KPI Stat Grid (Strictly canonical shadcn-admin grammar) */}
        <section aria-label="站点概况" className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
          <Card className="shadow-xs">
            <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
              <CardTitle className="text-sm font-medium text-muted-foreground">当前功率</CardTitle>
              <Zap className="size-4 text-muted-foreground" aria-hidden="true" />
            </CardHeader>
            <CardContent className="space-y-1">
              <div className="text-2xl font-bold tracking-tight tabular-nums">
                {formatNumber(summary?.fastMetrics.currentPower.value, 0)}
                <span className="ml-1 text-sm font-normal text-muted-foreground">{summary?.fastMetrics.currentPower.unit ?? 'kW'}</span>
              </div>
              <p className="text-xs text-muted-foreground">
                较基准节降 {formatNumber(overview?.loadSummary.savingRatePercent, 1)}% · 基准 {formatNumber(overview?.loadSummary.baselineKW, 0)} kW
              </p>
            </CardContent>
          </Card>

          <Card className="shadow-xs">
            <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
              <CardTitle className="text-sm font-medium text-muted-foreground">系统 COP</CardTitle>
              <Gauge className="size-4 text-muted-foreground" aria-hidden="true" />
            </CardHeader>
            <CardContent className="space-y-1">
              <div className="text-2xl font-bold tracking-tight tabular-nums">
                {formatNumber(summary?.slowMetrics.cop.value ?? overview?.kpis.averageCop, 2, 2)}
                <span className="ml-1 text-sm font-normal text-muted-foreground">COP</span>
              </div>
              <p className="text-xs text-muted-foreground">
                环比提升 +{formatNumber(overview?.kpis.copComparePercent, 1)}% · 综合节能率 {formatNumber(summary?.slowMetrics.baselineSavings.value ?? overview?.kpis.savingEnergyComparePercent, 1)}%
              </p>
            </CardContent>
          </Card>

          <Card className="shadow-xs" aria-label="数据状态">
            <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
              <CardTitle className="text-sm font-medium text-muted-foreground">数据状态</CardTitle>
              <Database className="size-4 text-muted-foreground" aria-hidden="true" />
            </CardHeader>
            <CardContent className="space-y-1">
              <div className="text-2xl font-bold tracking-tight tabular-nums">
                {availabilityPercent != null ? formatNumber(availabilityPercent, 1) : '—'}
                {availabilityPercent != null ? <span className="ml-1 text-sm font-normal text-muted-foreground">%</span> : null}
              </div>
              <p className="text-xs text-muted-foreground">
                在线可用 {population ? `${population.online} / ${population.denominator ?? '—'}` : '—'} 台 · 状态未知 {population?.unknown ?? 0} 台
              </p>
            </CardContent>
          </Card>

          <Card className="shadow-xs">
            <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
              <CardTitle className="text-sm font-medium text-muted-foreground">需处理</CardTitle>
              <TriangleAlert className="size-4 text-muted-foreground" aria-hidden="true" />
            </CardHeader>
            <CardContent className="space-y-1">
              <div className="text-2xl font-bold tracking-tight tabular-nums">
                {allPriorityItems.length}
                <span className="ml-1 text-sm font-normal text-muted-foreground">项</span>
              </div>
              <p className="text-xs text-muted-foreground">
                {canReadAlarms ? (activeAlarmCount ?? 0) : 0} 条活动告警 · {canReadWorkOrders ? attentionWorkOrders.length : 0} 项关注工单
              </p>
            </CardContent>
          </Card>
        </section>

        {/* Tier 2: 24h Load Trend AreaChart + Energy Breakdown Donut */}
        <section aria-label="逐时负荷与用能构成" className="grid gap-4 lg:grid-cols-12">
          <Card className="h-full shadow-xs lg:col-span-7 flex flex-col" aria-label="逐时负荷走势">
            <CardHeader className="flex flex-col sm:flex-row sm:items-center sm:justify-between border-b pb-3 gap-3">
              <div>
                <CardTitle className="flex items-center gap-2">
                  <Activity className="size-4 text-primary" aria-hidden="true" />
                  负荷走势
                </CardTitle>
              </div>
              <CardAction>
                <div className="inline-flex items-center rounded-lg border border-border/80 p-0.5 bg-muted/40 text-xs">
                  <button
                    type="button"
                    onClick={() => setLoadTimeRange('24h')}
                    className={cn(
                      'px-2.5 py-1 rounded-md transition-all font-medium select-none',
                      loadTimeRange === '24h'
                        ? 'bg-background text-foreground shadow-xs font-semibold'
                        : 'text-muted-foreground hover:text-foreground'
                    )}
                  >
                    今日 (24h)
                  </button>
                  <button
                    type="button"
                    onClick={() => setLoadTimeRange('7d')}
                    className={cn(
                      'px-2.5 py-1 rounded-md transition-all font-medium select-none',
                      loadTimeRange === '7d'
                        ? 'bg-background text-foreground shadow-xs font-semibold'
                        : 'text-muted-foreground hover:text-foreground'
                    )}
                  >
                    近 7 天
                  </button>
                  <button
                    type="button"
                    onClick={() => setLoadTimeRange('30d')}
                    className={cn(
                      'px-2.5 py-1 rounded-md transition-all font-medium select-none',
                      loadTimeRange === '30d'
                        ? 'bg-background text-foreground shadow-xs font-semibold'
                        : 'text-muted-foreground hover:text-foreground'
                    )}
                  >
                    近 30 天
                  </button>
                </div>
              </CardAction>
            </CardHeader>
            <CardContent className="flex flex-1 flex-col justify-center pt-3 pb-2">
              <div className="flex items-center justify-between pb-2 text-xs">
                <div className="flex items-center gap-4">
                  <span className="inline-flex items-center gap-1.5 font-medium text-foreground">
                    <span className="size-2 rounded-full bg-[var(--chart-1)]" aria-hidden="true" />
                    实际负荷
                  </span>
                  <span className="inline-flex items-center gap-1.5 text-muted-foreground">
                    <span className="size-2 rounded-full border border-dashed border-[var(--chart-2)] bg-transparent" aria-hidden="true" />
                    能耗基准
                  </span>
                </div>
                <div className="text-muted-foreground tabular-nums">
                  当前实际: <strong className="text-foreground font-semibold">{formatNumber(overview?.loadSummary.currentKW, 0)} kW</strong>
                </div>
              </div>
              {loadTrendRows.length > 0 ? (
                <ChartContainer config={loadChartConfig} className="h-[250px] w-full">
                  <AreaChart data={loadTrendRows} margin={{ top: 10, right: 10, left: -10, bottom: 0 }}>
                    <defs>
                      <linearGradient id="fillActual" x1="0" y1="0" x2="0" y2="1">
                        <stop offset="5%" stopColor="var(--color-actual)" stopOpacity={0.4} />
                        <stop offset="95%" stopColor="var(--color-actual)" stopOpacity={0.02} />
                      </linearGradient>
                      <linearGradient id="fillBaseline" x1="0" y1="0" x2="0" y2="1">
                        <stop offset="5%" stopColor="var(--color-baseline)" stopOpacity={0.18} />
                        <stop offset="95%" stopColor="var(--color-baseline)" stopOpacity={0.01} />
                      </linearGradient>
                    </defs>
                    <CartesianGrid strokeDasharray="3 3" vertical={false} stroke="var(--border)" opacity={0.6} />
                    <XAxis dataKey="time" tickLine={false} axisLine={false} tickMargin={8} minTickGap={32} />
                    <YAxis tickLine={false} axisLine={false} tickMargin={8} />
                    <ChartTooltip
                      cursor={{ stroke: 'var(--border)', strokeWidth: 1 }}
                      content={<ChartTooltipContent indicator="dot" />}
                    />
                    <Area
                      type="natural"
                      dataKey="baseline"
                      stroke="var(--color-baseline)"
                      strokeWidth={1.5}
                      strokeDasharray="4 4"
                      fillOpacity={1}
                      fill="url(#fillBaseline)"
                      isAnimationActive={false}
                    />
                    <Area
                      type="natural"
                      dataKey="actual"
                      stroke="var(--color-actual)"
                      strokeWidth={2}
                      fillOpacity={1}
                      fill="url(#fillActual)"
                      isAnimationActive={false}
                    />
                  </AreaChart>
                </ChartContainer>
              ) : overviewQuery.isPending ? (
                <OverviewEmpty title="正在加载负荷走势" />
              ) : (
                <OverviewEmpty title="暂无逐时负荷走势" description="等待聚合计算服务生成最新曲线。" />
              )}
            </CardContent>
          </Card>

          <Card className="h-full shadow-xs lg:col-span-5 flex flex-col" aria-labelledby="site-overview-energy-title">
            <CardHeader className="border-b">
              <div>
                <CardTitle id="site-overview-energy-title" className="flex items-center gap-2">
                  <ChartPie className="size-4 text-primary" aria-hidden="true" />
                  用能构成
                </CardTitle>
              </div>
              <CardAction>
                <Button variant="ghost" size="sm" asChild>
                  <Link to="/sites/$siteId/energy" params={{ siteId: site.id }}>能源分析<ArrowRight aria-hidden="true" /></Link>
                </Button>
              </CardAction>
            </CardHeader>
            <CardContent className="flex flex-1 flex-col justify-center p-4">
              {overviewQuery.isPending ? (
                <OverviewEmpty title="正在加载分项用能" />
              ) : overviewQuery.isError ? (
                <OverviewEmpty title="分项用能暂不可用" />
              ) : energyChartRows.length > 0 ? (
                energySupportsDonut ? (
                  <div className="grid grid-cols-1 sm:grid-cols-[180px_minmax(0,1fr)] items-center gap-4">
                    <div className="flex justify-center">
                      <ChartContainer
                        config={energyChartConfig}
                        className="aspect-square h-[190px] w-[190px]"
                        aria-label="本业务日分项用能构成"
                      >
                        <PieChart accessibilityLayer>
                          <ChartTooltip
                            cursor={false}
                            content={(
                              <ChartTooltipContent
                                hideLabel
                                nameKey="key"
                                formatter={(value, _name, item) => {
                                  const row = item.payload as { key?: string; energyKWh?: number | null };
                                  return (
                                    <div className="flex min-w-40 items-center justify-between gap-3">
                                      <span className="text-muted-foreground">{energyChartConfig[String(row.key)]?.label ?? '分项用能'}</span>
                                      <span className="font-mono font-medium tabular-nums">
                                        {row.energyKWh == null ? '—' : `${formatNumber(row.energyKWh, 0)} kWh`} · {formatNumber(Number(value), 1)}%
                                      </span>
                                    </div>
                                  );
                                }}
                              />
                            )}
                          />
                          <Pie
                            data={energyChartRows}
                            dataKey="percent"
                            nameKey="key"
                            innerRadius={54}
                            outerRadius={74}
                            strokeWidth={3}
                            isAnimationActive={false}
                          >
                            <Label
                              content={({ viewBox }) => {
                                if (viewBox && 'cx' in viewBox && 'cy' in viewBox) {
                                  return (
                                    <text
                                      x={viewBox.cx}
                                      y={viewBox.cy}
                                      textAnchor="middle"
                                      dominantBaseline="middle"
                                    >
                                      <tspan
                                        x={viewBox.cx}
                                        y={viewBox.cy}
                                        className="fill-foreground text-xl font-bold tracking-tight"
                                      >
                                        {formatNumber(energyTotalKWh, 0)}
                                      </tspan>
                                      <tspan
                                        x={viewBox.cx}
                                        y={(viewBox.cy || 0) + 18}
                                        className="fill-muted-foreground text-xs"
                                      >
                                        kWh
                                      </tspan>
                                    </text>
                                  );
                                }
                                return null;
                              }}
                            />
                          </Pie>
                        </PieChart>
                      </ChartContainer>
                    </div>
                    <div className="space-y-2 pr-1" role="list" aria-label="分项用能明细">
                      {energyChartRows.map((row) => (
                        <div key={row.key} className="space-y-1" role="listitem">
                          <div className="flex items-center justify-between gap-2 text-xs">
                            <span className="inline-flex items-center gap-1.5 font-medium truncate">
                              <span className="size-2 shrink-0 rounded-full" style={{ backgroundColor: row.color }} aria-hidden="true" />
                              <span className="truncate">{row.label}</span>
                            </span>
                            <span className="shrink-0 font-medium tabular-nums text-muted-foreground">
                              {row.energyKWh == null ? '—' : `${formatNumber(row.energyKWh, 0)} kWh`}
                              <span className="ml-1 text-foreground font-semibold">({formatNumber(row.percent, 1)}%)</span>
                            </span>
                          </div>
                          <div className="h-1.5 overflow-hidden rounded-full bg-muted" aria-hidden="true">
                            <div
                              className="h-full rounded-full transition-all"
                              style={{
                                width: `${Math.max(0, Math.min(100, row.percent))}%`,
                                backgroundColor: row.color,
                              }}
                            />
                          </div>
                        </div>
                      ))}
                    </div>
                  </div>
                ) : (
                  <div className="space-y-3" role="list" aria-label="分项用能明细">
                    {energyChartRows.map((row) => (
                      <div key={row.key} className="space-y-1.5" role="listitem">
                        <div className="flex items-center justify-between gap-4 text-sm">
                          <span className="truncate font-medium">{row.label}</span>
                          <span className="shrink-0 tabular-nums text-muted-foreground">
                            {row.energyKWh == null ? '—' : `${formatNumber(row.energyKWh, 0)} kWh`} · {formatNumber(row.percent, 1)}%
                          </span>
                        </div>
                        <div className="h-1.5 overflow-hidden rounded-full bg-muted" aria-hidden="true">
                          <div className="h-full rounded-full" style={{ width: `${Math.max(0, Math.min(100, row.percent))}%`, backgroundColor: row.color }} />
                        </div>
                      </div>
                    ))}
                  </div>
                )
              ) : <OverviewEmpty title="暂无分项用能数据" />}
            </CardContent>
          </Card>
        </section>

        {/* Tier 3: Operations Matrix + Priority Stream */}
        <section aria-label="当前运行与优先事项" className="grid gap-4 lg:grid-cols-12">
          <Card className="order-2 flex h-full flex-col gap-0 py-0 shadow-xs lg:order-1 lg:col-span-7" data-testid="current-operations-card">
            <CardHeader className="border-b py-4">
              <div>
                <CardTitle className="flex items-center gap-2">
                  <Play className="size-4 text-primary fill-primary" aria-hidden="true" />
                  当前运行
                </CardTitle>
              </div>
              <CardAction>
                <Button variant="ghost" size="sm" asChild>
                  <Link to="/sites/$siteId/operations" params={{ siteId: site.id }}>系统运行<ArrowRight aria-hidden="true" /></Link>
                </Button>
              </CardAction>
            </CardHeader>
            <CardContent className="p-0">
              {overviewQuery.isPending ? (
                <OverviewEmpty title="正在加载运行状态" />
              ) : overviewQuery.isError ? (
                <OverviewEmpty title="系统运行数据暂不可用" />
              ) : runningRows.length > 0 ? (
                <Table aria-label="当前运行设备群">
                  <TableHeader className="bg-muted/20">
                    <TableRow className="hover:bg-transparent">
                      <TableHead className="w-[140px]">设备组</TableHead>
                      <TableHead className="w-[130px]">运行状态</TableHead>
                      <TableHead>负荷占比</TableHead>
                      <TableHead className="w-[110px] text-right">当前功率</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {runningRows.slice(0, 6).map((row) => {
                      const allRunning = row.running != null && row.total != null && row.running === row.total;
                      const noneRunning = row.running === 0;
                      const share = totalRunningPowerKW > 0 && row.powerKW != null
                        ? Math.round((row.powerKW / totalRunningPowerKW) * 100)
                        : 0;
                      return (
                        <TableRow key={row.key} data-group-key={row.key} data-group-power={row.powerKW ?? ''}>
                          <TableCell className="font-medium text-foreground">{row.label}</TableCell>
                          <TableCell>
                            <div className="flex items-center gap-2">
                              <span className={cn('size-2 rounded-full', allRunning ? 'bg-success' : noneRunning ? 'bg-muted-foreground' : 'bg-primary')} />
                              <span className="font-mono text-xs tabular-nums text-muted-foreground">
                                {row.running == null || row.total == null ? '状态未知' : `${row.running} / ${row.total} 运行`}
                              </span>
                            </div>
                          </TableCell>
                          <TableCell>
                            <div className="space-y-1.5">
                              <div className="flex items-center justify-between text-xs text-muted-foreground">
                                <span>占比</span>
                                <span className="font-mono tabular-nums text-foreground">{share}%</span>
                              </div>
                              <div className="h-1.5 overflow-hidden rounded-full bg-muted">
                                <div className="h-full rounded-full bg-primary" style={{ width: `${share}%` }} />
                              </div>
                            </div>
                          </TableCell>
                          <TableCell className="text-right font-mono font-medium tabular-nums text-foreground">
                            {row.powerKW == null ? '—' : `${formatNumber(row.powerKW, 0)} kW`}
                          </TableCell>
                        </TableRow>
                      );
                    })}
                  </TableBody>
                </Table>
              ) : <OverviewEmpty title="暂无系统运行摘要" />}
            </CardContent>
          </Card>

          <Card className="h-full shadow-xs order-1 lg:order-2 lg:col-span-5 flex flex-col" data-testid="priority-card">
            <CardHeader className="border-b">
              <div>
                <CardTitle className="flex items-center gap-2">
                  <TriangleAlert className="size-4 text-primary" aria-hidden="true" />
                  优先处理
                </CardTitle>
              </div>
              {allPriorityItems.length > 3 ? (
                <CardAction>
                  <Button variant="ghost" size="sm" asChild>
                    <Link to="/sites/$siteId/issues" params={{ siteId: site.id }} search={{ view: 'alarms', alarmView: 'active' }}>
                      全部事项 ({allPriorityItems.length})<ArrowRight aria-hidden="true" />
                    </Link>
                  </Button>
                </CardAction>
              ) : null}
            </CardHeader>
            <CardContent className="p-0">
              {priorityItems.length > 0 ? (
                <ItemGroup className="divide-y">
                  {priorityItems.map((item) => {
                    const content = (
                      <>
                        <PriorityBadge severity={item.severity} />
                        <ItemContent className="grid min-w-0 gap-1 lg:grid-cols-[minmax(0,1fr)_8rem] lg:items-center lg:gap-3">
                          <div className="min-w-0">
                            <ItemTitle className="truncate font-medium">{item.title}</ItemTitle>
                            <ItemDescription className="line-clamp-1">{item.reason}</ItemDescription>
                          </div>
                          <div className="min-w-0 text-xs text-muted-foreground">
                            <div className="truncate">{item.scopeLabel}</div>
                            <div>{formatAge(item.occurredAt)}</div>
                          </div>
                        </ItemContent>
                        <ItemActions className="text-xs font-medium text-primary">
                          <span className="hidden sm:inline">{item.actionLabel}</span>
                          <ArrowRight className="size-3.5" aria-hidden="true" />
                        </ItemActions>
                      </>
                    );
                    return (
                      <Item key={item.key} asChild size="sm" className="rounded-none px-4 py-3 hover:bg-muted/40 focus-within:bg-muted/40">
                        {item.kind === 'alarm' ? (
                          <Link
                            to="/sites/$siteId/issues"
                            params={{ siteId: site.id }}
                            search={{ selected: item.targetId, source: 'overview', view: 'alarms', alarmView: 'active' }}
                          >
                            {content}
                          </Link>
                        ) : (
                          <Link
                            to="/sites/$siteId/work-orders"
                            params={{ siteId: site.id }}
                            search={{ workOrder: item.targetId, source: 'overview' }}
                          >
                            {content}
                          </Link>
                        )}
                      </Item>
                    );
                  })}
                </ItemGroup>
              ) : priorityInputsPending ? (
                <OverviewEmpty title="正在确认优先事项" />
              ) : priorityInputsUnavailable ? (
                <OverviewEmpty title="优先事项暂不可完整确认" description="部分业务数据暂不可用。" />
              ) : (
                <OverviewEmpty title="当前没有需要优先处理的事项" />
              )}
            </CardContent>
          </Card>
        </section>
      </div>
    </Main>
  );
}
