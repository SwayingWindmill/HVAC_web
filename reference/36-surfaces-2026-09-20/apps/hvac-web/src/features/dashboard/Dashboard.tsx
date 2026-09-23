import { useQuery } from '@tanstack/react-query';
import {
  Activity,
  AlertTriangle,
  ArrowUpRight,
  CircleGauge,
  Gauge,
  Leaf,
  RadioTower,
  ServerCog,
  Wrench,
  Zap,
} from 'lucide-react';
import { listScopedAlarms, type Alarm } from '@/api/alarms';
import { readDashboardOverview, type DashboardOverview } from '@/api/dashboard-overview';
import type { CurrentPrincipalResponse, Site } from '@/api/generated/platformGateway.gen';
import { presentSiteDashboardError, useSiteDashboardSummary } from '@/api/site-dashboard';
import { siteRoute } from '@/app/router-paths';
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
import { Skeleton } from '@/components/ui/skeleton';
import { cn } from '@/lib/utils';
import { selectDashboardAlarmCandidate } from './dashboard-alarm-link';

interface DashboardProps {
  site: Readonly<Site>;
  principal: CurrentPrincipalResponse;
}

type AttentionTone = 'stable' | 'attention' | 'critical' | 'unknown';

const SEVERITY_LABEL: Readonly<Record<string, string>> = Object.freeze({
  CRITICAL: '紧急',
  MAJOR: '重要',
  MINOR: '一般',
  WARNING: '警告',
  INFO: '提示',
});

const PRESENTATION_LABEL: Readonly<Record<string, string>> = Object.freeze({
  READY: '数据完整',
  ATTENTION: '需要关注',
  NO_DATA: '暂无数据',
  PARTIAL: '部分数据',
  STALE: '数据延迟',
  SUSPECT: '数据待核验',
  UNAVAILABLE: '暂不可用',
  NOT_AUTHORIZED: '无权限',
  NOT_INTEGRATED: '未接入',
});

function formatNumber(value: number | null | undefined, digits = 1): string {
  if (value === null || value === undefined) return '—';
  return new Intl.NumberFormat('zh-CN', { maximumFractionDigits: digits }).format(value);
}

function formatMetric(value: number | null | undefined, unit: string | null | undefined, digits = 1): string {
  const formatted = formatNumber(value, digits);
  return formatted === '—' ? formatted : `${formatted}${unit ? ` ${unit}` : ''}`;
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

function deviceCenterHref(site: Readonly<Site>, search?: Record<string, string>): string {
  const parameters = new URLSearchParams({ site: site.id, view: 'table', ...search });
  return `/devices?${parameters.toString()}`;
}

function alarmCenterHref(site: Readonly<Site>, alarmId?: string, severity?: string): string {
  const parameters = new URLSearchParams({ source: 'dashboard', tab: 'active' });
  if (alarmId) parameters.set('alarm', alarmId);
  if (severity && ['CRITICAL', 'MAJOR', 'MINOR', 'WARNING', 'INFO'].includes(severity)) parameters.set('severity', severity);
  return `${siteRoute(site, 'alarms')}?${parameters.toString()}`;
}

function postureFor(
  activeAlarms: number | null | undefined,
  highestSeverity: string | null | undefined,
  offline: number | undefined,
  stale: number | undefined,
  quality: string | undefined,
): { tone: AttentionTone; label: string; detail: string } {
  if (highestSeverity === 'CRITICAL' || highestSeverity === 'MAJOR') {
    return {
      tone: 'critical',
      label: '需要立即处置',
      detail: `当前存在${activeAlarms ?? '—'}条活动告警，最高级别为${SEVERITY_LABEL[highestSeverity] ?? highestSeverity}。`,
    };
  }
  if ((activeAlarms ?? 0) > 0 || (offline ?? 0) > 0 || (stale ?? 0) > 0) {
    return {
      tone: 'attention',
      label: '存在待处理事项',
      detail: `活动告警 ${activeAlarms ?? '—'} · 离线设备 ${offline ?? '—'} · 数据延迟 ${stale ?? '—'}`,
    };
  }
  if (quality && quality !== 'READY') {
    return { tone: 'unknown', label: '运行判断受数据限制', detail: PRESENTATION_LABEL[quality] ?? '当前摘要数据不完整。' };
  }
  return { tone: 'stable', label: '当前未发现高优先级异常', detail: '继续关注实时状态和待办变化。' };
}

function MetricCard({
  title,
  value,
  note,
  icon,
}: {
  title: string;
  value: string;
  note: string;
  icon: React.ReactNode;
}) {
  return (
    <Card size="sm" className="gap-2 shadow-none">
      <CardHeader className="gap-0.5">
        <CardTitle className="text-xs font-medium text-muted-foreground">{title}</CardTitle>
        <CardAction><span className="text-muted-foreground">{icon}</span></CardAction>
      </CardHeader>
      <CardContent>
        <div className="text-2xl font-semibold tracking-tight tabular-nums">{value}</div>
        <p className="mt-1 text-[11px] text-muted-foreground">{note}</p>
      </CardContent>
    </Card>
  );
}

function DashboardLoading() {
  return (
    <div className="mx-auto flex w-full max-w-[1600px] flex-col gap-6">
      <div className="flex items-center justify-between"><Skeleton className="h-14 w-72" /><Skeleton className="h-8 w-40" /></div>
      <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-4">{Array.from({ length: 4 }, (_, index) => <Skeleton key={index} className="h-28" />)}</div>
      <div className="grid gap-4 xl:grid-cols-[minmax(0,1.6fr)_minmax(320px,.8fr)]"><Skeleton className="h-[360px]" /><Skeleton className="h-[360px]" /></div>
    </div>
  );
}

function SeverityBadge({ severity }: { severity: string }) {
  const destructive = severity === 'CRITICAL' || severity === 'MAJOR';
  return <Badge variant={destructive ? 'destructive' : 'outline'}>{SEVERITY_LABEL[severity] ?? severity}</Badge>;
}

export function Dashboard({ site, principal }: DashboardProps) {
  const summaryQuery = useSiteDashboardSummary(
    principal.context.tenantId,
    site.id,
    `${principal.session.id}:${principal.authorization.policyRevision}`,
  );
  const overviewQuery = useQuery({
    queryKey: ['presentation', 'dashboard-overview', site.id],
    queryFn: ({ signal }) => readDashboardOverview(site.id, signal),
    staleTime: 30_000,
    retry: false,
  });
  const alarmListAllowed = principal.authorization.capabilities.includes('alarm.list');
  const recentAlarmQuery = useQuery({
    queryKey: ['control-home', 'active-alarms', site.tenantId, site.id],
    queryFn: ({ signal }) => listScopedAlarms({ condition: 'ACTIVE', limit: 5 }, {
      trustedTenantId: site.tenantId,
      trustedSiteId: site.id,
      signal,
    }),
    enabled: alarmListAllowed,
    staleTime: 30_000,
  });

  if (summaryQuery.isPending) return <DashboardLoading />;

  const summary = summaryQuery.data;
  const summaryError = summaryQuery.isError ? presentSiteDashboardError(summaryQuery.error) : null;
  const overview = overviewQuery.data;
  const population = summary?.devicePopulation;
  const activeAlarms = summary?.fastMetrics.openAlarms.activeCount;
  const highestSeverity = summary?.fastMetrics.openAlarms.highestSeverity;
  const posture = postureFor(activeAlarms, highestSeverity, population?.offline, population?.stale, summary?.quality);
  const businessState = summaryError ? 'UNAVAILABLE' : summary?.quality ?? 'UNAVAILABLE';
  const primaryAlarms = overview?.priorityAlarms ?? [];
  const liveAlarms = recentAlarmQuery.data?.items ?? [];
  const asOf = summary?.asOf ?? overview?.asOf;

  const priorityAlarmHref = (alarm: DashboardOverview['priorityAlarms'][number]) => {
    const matched = selectDashboardAlarmCandidate(alarm, liveAlarms);
    return alarmCenterHref(site, matched?.alarmId, alarm.severity);
  };

  const postureIcon = posture.tone === 'stable' ? <Activity /> : posture.tone === 'unknown' ? <ServerCog /> : <AlertTriangle />;
  const postureTone = posture.tone === 'critical'
    ? 'text-destructive'
    : posture.tone === 'attention'
      ? 'text-warning'
      : posture.tone === 'stable'
        ? 'text-success'
        : 'text-muted-foreground';

  return (
    <div className="mx-auto flex w-full max-w-[1600px] flex-col gap-6" data-testid="real-site-route-dashboard" data-business-state={businessState} data-site-id={site.id}>
      <section className="flex flex-col gap-4 sm:flex-row sm:items-end sm:justify-between" aria-labelledby="dashboard-title">
        <div>
          <p className="text-sm text-muted-foreground">{site.displayName}</p>
          <h2 id="dashboard-title" className="mt-1 text-2xl font-semibold tracking-tight">运营概览</h2>
          <p className="mt-1 text-xs text-muted-foreground">最后更新 {formatInstant(asOf, site.timezone)} · {site.timezone}</p>
        </div>
        <div className="flex gap-2">
          <Button variant="outline" size="sm" asChild><a href={siteRoute(site, 'operations')}>调查工作台</a></Button>
          <Button size="sm" asChild><a href={siteRoute(site, 'monitor')}>运行监控 <ArrowUpRight /></a></Button>
        </div>
      </section>

      {summaryError ? (
        <div className="rounded-md border border-destructive/25 bg-destructive/5 px-4 py-3 text-sm" role="alert">
          <strong className="font-medium text-destructive">{summaryError.title}</strong>
          <span className="ml-2 text-muted-foreground">{summaryError.description}</span>
        </div>
      ) : null}

      <section className="grid gap-4 md:grid-cols-2 xl:grid-cols-4" aria-label="关键运行指标">
        <MetricCard title="当前功率" value={formatMetric(summary?.fastMetrics.currentPower.value, summary?.fastMetrics.currentPower.unit, 0)} note={PRESENTATION_LABEL[summary?.fastMetrics.currentPower.state ?? 'UNAVAILABLE']} icon={<Zap className="size-4" />} />
        <MetricCard title="今日用能" value={formatMetric(summary?.slowMetrics.siteLocalDayEnergy.value, summary?.slowMetrics.siteLocalDayEnergy.unit, 0)} note={PRESENTATION_LABEL[summary?.slowMetrics.siteLocalDayEnergy.state ?? 'UNAVAILABLE']} icon={<CircleGauge className="size-4" />} />
        <MetricCard title="系统 COP" value={formatMetric(summary?.slowMetrics.cop.value ?? overview?.kpis.averageCop, summary?.slowMetrics.cop.unit, 2)} note={PRESENTATION_LABEL[summary?.slowMetrics.cop.state ?? 'UNAVAILABLE']} icon={<Gauge className="size-4" />} />
        <MetricCard title="设备可用率" value={population?.availabilityPercent == null ? '—' : `${formatNumber(population.availabilityPercent, 1)} %`} note={population ? PRESENTATION_LABEL[population.state] : '暂不可用'} icon={<RadioTower className="size-4" />} />
      </section>

      <section className="grid gap-4 xl:grid-cols-[minmax(0,1.6fr)_minmax(320px,.8fr)]" aria-label="运营态势">
        <Card className="shadow-none">
          <CardHeader>
            <CardTitle>运行态势</CardTitle>
            <CardDescription>把告警、设备连接和数据质量分开呈现，不合成为不透明的健康分。</CardDescription>
            <CardAction><Button variant="ghost" size="sm" asChild><a href={siteRoute(site, 'monitor')}>查看系统 <ArrowUpRight /></a></Button></CardAction>
          </CardHeader>
          <CardContent className="space-y-5">
            <div className="flex items-start gap-3 rounded-md border bg-muted/30 p-4">
              <span className={cn('mt-0.5 grid size-8 shrink-0 place-items-center rounded-md bg-background [&_svg]:size-4', postureTone)}>{postureIcon}</span>
              <div className="min-w-0">
                <p className="text-xs text-muted-foreground">当前判断</p>
                <strong className="mt-0.5 block text-sm font-semibold">{posture.label}</strong>
                <p className="mt-1 text-xs leading-5 text-muted-foreground">{posture.detail}</p>
              </div>
            </div>

            <div>
              <div className="mb-2 flex items-center justify-between">
                <div><p className="text-sm font-medium">设备运行状态</p><p className="text-xs text-muted-foreground">统计现场设备在线与遥测通信情况</p></div>
                <span className="text-xs text-muted-foreground tabular-nums">{population?.observable ?? '—'} / {population?.registered ?? '—'} 可观测</span>
              </div>
              <div className="grid overflow-hidden rounded-md border sm:grid-cols-4">
                {[
                  ['在线', population?.online ?? '—', deviceCenterHref(site)],
                  ['离线', population?.offline ?? '—', deviceCenterHref(site, { connection: 'OFFLINE' })],
                  ['数据延迟', population?.stale ?? '—', deviceCenterHref(site)],
                  ['未知 / 不可用', population ? population.unknown + population.unavailable : '—', deviceCenterHref(site)],
                ].map(([label, value, href], index) => (
                  <a key={label} href={String(href)} className={cn('grid gap-1 p-3 transition-colors hover:bg-muted/50', index > 0 && 'border-t sm:border-l sm:border-t-0')}>
                    <span className="text-[11px] text-muted-foreground">{label}</span>
                    <strong className="text-lg font-semibold tabular-nums">{value}</strong>
                  </a>
                ))}
              </div>
            </div>

            <div className="grid gap-3 sm:grid-cols-3">
              <div className="rounded-md border p-3"><span className="text-[11px] text-muted-foreground">基线节省</span><strong className="mt-1 block text-sm tabular-nums">{formatMetric(summary?.slowMetrics.baselineSavings.value, summary?.slowMetrics.baselineSavings.unit, 0)}</strong></div>
              <div className="rounded-md border p-3"><span className="text-[11px] text-muted-foreground">今日费用</span><strong className="mt-1 block text-sm tabular-nums">{formatMetric(summary?.slowMetrics.cost.value, summary?.slowMetrics.cost.unit, 0)}</strong></div>
              <div className="rounded-md border p-3"><span className="text-[11px] text-muted-foreground">今日减碳</span><strong className="mt-1 block text-sm tabular-nums">{overview?.kpis.carbonReductionTco2e == null ? '—' : `${formatNumber(overview.kpis.carbonReductionTco2e, 1)} tCO₂e`}</strong></div>
            </div>
          </CardContent>
        </Card>

        <Card className="shadow-none">
          <CardHeader>
            <CardTitle>优先处理</CardTitle>
            <CardDescription>待处置的重要告警与设备异常</CardDescription>
            <CardAction><Badge variant={activeAlarms ? 'destructive' : 'outline'}>{activeAlarms ?? '—'} 条活动告警</Badge></CardAction>
          </CardHeader>
          <CardContent className="space-y-1">
            {alarmListAllowed && primaryAlarms.length > 0 ? primaryAlarms.map((alarm) => (
              <a key={`${alarm.title}-${alarm.occurredAt}`} href={priorityAlarmHref(alarm)} className="flex items-start gap-3 rounded-md px-2 py-2.5 transition-colors hover:bg-muted/50">
                <SeverityBadge severity={alarm.severity} />
                <span className="min-w-0 flex-1"><strong className="block truncate text-xs font-medium">{alarm.title}</strong><small className="mt-0.5 block text-[11px] text-muted-foreground">{formatInstant(alarm.occurredAt, site.timezone)}</small></span>
                <ArrowUpRight className="mt-0.5 size-3.5 shrink-0 text-muted-foreground" />
              </a>
            )) : alarmListAllowed ? liveAlarms.slice(0, 4).map((alarm: Alarm) => (
              <a key={alarm.alarmId} href={alarmCenterHref(site, alarm.alarmId, alarm.currentSeverity)} className="flex items-start gap-3 rounded-md px-2 py-2.5 transition-colors hover:bg-muted/50">
                <SeverityBadge severity={alarm.currentSeverity} />
                <span className="min-w-0 flex-1"><strong className="block truncate text-xs font-medium">{alarm.title}</strong><small className="mt-0.5 block text-[11px] text-muted-foreground">{formatInstant(alarm.lastOccurredAt, site.timezone)}</small></span>
                <ArrowUpRight className="mt-0.5 size-3.5 shrink-0 text-muted-foreground" />
              </a>
            )) : <p className="py-6 text-center text-xs text-muted-foreground">当前账号无告警读取权限。</p>}

            {(population?.offline ?? 0) > 0 || (population?.stale ?? 0) > 0 || (population?.unavailable ?? 0) > 0 ? (
              <a href={deviceCenterHref(site)} className="mt-2 flex items-start gap-3 rounded-md border bg-muted/20 p-3 transition-colors hover:bg-muted/50">
                <ServerCog className="mt-0.5 size-4 text-warning" />
                <span className="min-w-0 flex-1"><strong className="block text-xs font-medium">存在异常或离线设备</strong><small className="mt-1 block text-[11px] text-muted-foreground">离线 {population?.offline ?? 0} · 数据延迟 {population?.stale ?? 0} · 不可用 {population?.unavailable ?? 0}</small></span>
                <ArrowUpRight className="size-3.5 text-muted-foreground" />
              </a>
            ) : null}
          </CardContent>
        </Card>
      </section>

      <section className="grid gap-4 lg:grid-cols-3" aria-label="数据与工作流">
        <Card className="shadow-none">
          <CardHeader><CardTitle>节能机会</CardTitle><CardDescription>基于运行工况分析识别的优化措施</CardDescription></CardHeader>
          <CardContent>
            {overview?.opportunities?.[0] ? (
              <a className="flex items-start gap-3 rounded-md border bg-muted/20 p-3 transition-colors hover:bg-muted/50" href={siteRoute(site, 'optimize')}>
                <Leaf className="mt-0.5 size-4 text-success" />
                <span className="min-w-0 flex-1"><strong className="block text-xs font-medium">{overview.opportunities[0].title}</strong><small className="mt-1 block text-[11px] text-muted-foreground">{overview.opportunities[0].savingKWhPerDay == null ? '收益待评估' : `${formatNumber(overview.opportunities[0].savingKWhPerDay, 0)} kWh/日`}</small></span>
                <ArrowUpRight className="size-3.5 text-muted-foreground" />
              </a>
            ) : <p className="py-6 text-center text-xs text-muted-foreground">当前没有可展示的优化机会。</p>}
          </CardContent>
        </Card>

        <Card className="shadow-none">
          <CardHeader><CardTitle>数据采集质量</CardTitle><CardDescription>测点数据完整率与更新时间</CardDescription></CardHeader>
          <CardContent className="space-y-3">
            <div className="flex items-center justify-between"><span className="text-xs text-muted-foreground">数据质量</span><Badge variant="outline">{PRESENTATION_LABEL[summary?.quality ?? 'UNAVAILABLE']}</Badge></div>
            <div className="flex items-center justify-between"><span className="text-xs text-muted-foreground">完整度</span><span className="text-xs font-medium">{PRESENTATION_LABEL[summary?.completeness ?? 'UNAVAILABLE']}</span></div>
            <div className="flex items-center justify-between"><span className="text-xs text-muted-foreground">最新采集时间</span><span className="text-xs font-medium tabular-nums">{formatInstant(summary?.dataWatermark, site.timezone)}</span></div>
            <div className="flex items-center justify-between"><span className="text-xs text-muted-foreground">统计计算周期</span><span className="text-xs font-medium tabular-nums">{formatInstant(summary?.aggregateWatermark, site.timezone)}</span></div>
            {overviewQuery.isError ? <p className="border-t pt-3 text-[11px] leading-5 text-muted-foreground">部分统计数据加载异常，当前仅展示实时数据。</p> : null}
          </CardContent>
        </Card>

        <Card className="shadow-none">
          <CardHeader><CardTitle>快捷导航</CardTitle><CardDescription>常用业务工作台直达</CardDescription></CardHeader>
          <CardContent className="grid gap-1">
            {[
              [siteRoute(site, 'monitor'), <CircleGauge key="monitor" />, '运行监控', '系统状态与异常上下文'],
              [deviceCenterHref(site), <Gauge key="devices" />, '设备中心', '设备状态与关键运行值'],
              [siteRoute(site, 'alarms'), <AlertTriangle key="alarms" />, '告警中心', '调查与处置活动告警'],
              [siteRoute(site, 'work-orders'), <Wrench key="orders" />, '工单', '责任、SLA 与现场动作'],
            ].map(([href, icon, title, detail]) => (
              <a key={String(title)} href={String(href)} className="flex items-center gap-3 rounded-md px-2 py-2 transition-colors hover:bg-muted/50">
                <span className="text-muted-foreground [&_svg]:size-4">{icon}</span>
                <span className="min-w-0 flex-1"><strong className="block text-xs font-medium">{title}</strong><small className="block truncate text-[11px] text-muted-foreground">{detail}</small></span>
                <ArrowUpRight className="size-3.5 text-muted-foreground" />
              </a>
            ))}
          </CardContent>
        </Card>
      </section>
    </div>
  );
}
