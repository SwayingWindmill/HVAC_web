import { useCallback, useEffect, useMemo } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';
import {
  Activity,
  BarChart3,
  CalendarDays,
  ChevronLeft,
  ChevronRight,
  CircleAlert,
  Clock,
  Download,
  PieChart,
  RefreshCw,
  ShieldAlert,
  ShieldCheck,
  TableProperties,
  TrendingUp,
  Zap,
} from 'lucide-react';
import type { CurrentPrincipalResponse, Site } from '@/api/generated/platformGateway.gen';
import {
  classifyEnergyAnalyticsFailure,
  energySeriesQueryKey,
  energySeriesRevisionKey,
  hasStaleWatermark,
  queryEnergySeries,
  type EnergyAnalyticsRequestOptions,
  type EnergyQualityPolicy,
  type EnergySeriesPoint,
  type EnergySeriesQuery,
  type EnergySeriesResponse,
} from '@/api/energy-analytics';
import type { EnergyRoutePeriod } from '@/app/router-paths';
import { Main } from '@/components/layout/Main';
import { PageIntro } from '@/components/layout/PageIntro';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent } from '@/components/ui/card';
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from '@/components/ui/empty';
import { Input } from '@/components/ui/input';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import {
  SelectGroup,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { EnergyTrendChart, type EnergyTrendDatum } from '@/shared/charts/TimeSeriesChart';
import { cn } from '@/lib/utils';
import { sortEnergyPoints, summarizeEnergyPoints } from './presentation';
import {
  buildEnergyWorkspaceWindow,
  compareEnergyTotals,
  currentEnergyWorkspaceState,
  energyWorkspaceStateFromRoute,
  shiftEnergyWorkspaceState,
  type EnergyWorkspacePeriod,
  type EnergyWorkspaceState,
} from './workspace';

export interface EnergyAnalysisSearchState {
  readonly anchor?: string;
  readonly quality?: EnergyQualityPolicy;
}

interface EnergyAnalyticsProps {
  site: Readonly<Site>;
  principal: CurrentPrincipalResponse;
  initialPeriod?: EnergyRoutePeriod;
  searchState: EnergyAnalysisSearchState;
  onWorkspaceChange: (next: EnergyWorkspaceState) => void;
}

interface EnergyComparisonRow {
  readonly id: string;
  readonly current?: EnergySeriesPoint;
  readonly previous?: EnergySeriesPoint;
  readonly difference: number | null;
}

const PERIOD_LABELS: Record<EnergyWorkspacePeriod, string> = {
  day: '日',
  week: '周',
  month: '月',
  year: '年',
};

const QUALITY_LABELS: Record<EnergyQualityPolicy, string> = {
  VALID_ONLY: '仅使用有效数据',
  VALID_AND_SUSPECT: '包含可疑数据',
};

function buildQuery(
  site: Readonly<Site>,
  range: { from: string; to: string; granularity: EnergySeriesQuery['granularity'] },
  qualityPolicy: EnergyQualityPolicy,
): EnergySeriesQuery {
  return {
    tenantId: site.tenantId,
    siteId: site.id,
    energyType: 'electricity',
    granularity: range.granularity,
    timezone: site.timezone,
    from: range.from,
    to: range.to,
    qualityPolicy,
  };
}

function formatInstant(value: string | undefined, timezone: string): string {
  if (!value) return '未提供';
  return new Intl.DateTimeFormat('zh-CN', {
    timeZone: timezone,
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  }).format(new Date(value));
}

function formatEnergy(value: number | null | undefined): string {
  if (value === null || value === undefined) return '—';
  if (Math.abs(value) >= 1000) {
    return `${new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 1 }).format(value / 1000)} MWh`;
  }
  return `${new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 1 }).format(value)} kWh`;
}

function formatSignedEnergy(value: number): string {
  const sign = value > 0 ? '+' : value < 0 ? '−' : '';
  const absolute = Math.abs(value);
  if (absolute >= 1000) {
    return `${sign}${new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 1 }).format(absolute / 1000)} MWh`;
  }
  return `${sign}${new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 1 }).format(absolute)} kWh`;
}

function formatBucketLabel(point: EnergySeriesPoint, period: EnergyWorkspacePeriod, timezone: string): string {
  const date = new Date(point.periodStart);
  if (period === 'day') {
    return new Intl.DateTimeFormat('zh-CN', {
      timeZone: timezone,
      hour: '2-digit',
      minute: '2-digit',
      hourCycle: 'h23',
    }).format(date);
  }
  if (period === 'year') {
    return new Intl.DateTimeFormat('zh-CN', { timeZone: timezone, month: 'short' }).format(date);
  }
  return new Intl.DateTimeFormat('zh-CN', {
    timeZone: timezone,
    month: '2-digit',
    day: '2-digit',
  }).format(date);
}

function formatBucketRange(point: EnergySeriesPoint, timezone: string): string {
  const formatter = new Intl.DateTimeFormat('zh-CN', {
    timeZone: timezone,
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  });
  return `${formatter.format(new Date(point.periodStart))} — ${formatter.format(new Date(point.periodEnd))}`;
}

function comparisonPresentation(
  current: number | null,
  previous: number | null,
  comparisonAvailable: boolean,
  unavailableReason: string,
): { value: string; detail: string } {
  if (!comparisonAvailable) return { value: '不比较', detail: unavailableReason };
  const result = compareEnergyTotals(current, previous);
  if (result.kind === 'unavailable') return { value: '不可用', detail: '当前周期或比较周期没有可比较总量' };
  if (result.kind === 'baseline-zero') {
    return { value: formatSignedEnergy(result.differenceKWh), detail: '比较周期为 0，不显示无意义百分比' };
  }
  return {
    value: `${formatSignedEnergy(result.differenceKWh)} (${result.percentage >= 0 ? '+' : ''}${result.percentage.toFixed(1)}%)`,
    detail: '与上一完整周期相比；差异不是已验证节能量',
  };
}

interface VarianceWindow {
  readonly current: EnergySeriesPoint;
  readonly previous: EnergySeriesPoint;
  readonly differenceKWh: number;
}

function varianceWindows(
  currentResponse: EnergySeriesResponse,
  previousResponse: EnergySeriesResponse | undefined,
): readonly VarianceWindow[] {
  if (!previousResponse) return [];
  if (currentResponse.metadata.partial || previousResponse.metadata.partial) return [];
  if (currentResponse.metadata.actualGranularity !== previousResponse.metadata.actualGranularity) return [];
  const current = sortEnergyPoints(currentResponse.points);
  const previous = sortEnergyPoints(previousResponse.points);
  if (current.length === 0 || current.length !== previous.length) return [];
  return current
    .map((point, index) => ({
      current: point,
      previous: previous[index],
      differenceKWh: point.energyKWh - previous[index].energyKWh,
    }))
    .sort((left, right) => Math.abs(right.differenceKWh) - Math.abs(left.differenceKWh))
    .slice(0, 3);
}

function downloadAnalysisCsv(
  site: Readonly<Site>,
  periodLabel: string,
  previousLabel: string,
  response: EnergySeriesResponse,
  previousResponse: EnergySeriesResponse | undefined,
): void {
  const current = sortEnergyPoints(response.points);
  const previous = sortEnergyPoints(previousResponse?.points ?? []);
  const rows = Math.max(current.length, previous.length);
  const metadata = [
    ['站点', site.displayName],
    ['站点时区', site.timezone],
    ['能源类型', '电力'],
    ['计量范围', '站点级 Energy Read Model'],
    ['当前周期', periodLabel],
    ['比较周期', previousLabel],
    ['返回粒度', response.metadata.actualGranularity],
    ['数据状态', response.metadata.partial ? '部分数据' : '完整数据'],
    ['导出时间', new Date().toISOString()],
  ];
  const body = Array.from({ length: rows }, (_, index) => [
    current[index]?.periodStart ?? '',
    current[index]?.periodEnd ?? '',
    current[index]?.energyKWh ?? '',
    previous[index]?.periodStart ?? '',
    previous[index]?.periodEnd ?? '',
    previous[index]?.energyKWh ?? '',
  ]);
  const escape = (value: string | number) => `"${String(value).replace(/"/g, '""')}"`;
  const csv = [
    ...metadata.map((row) => row.map(escape).join(',')),
    '',
    ['当前开始', '当前结束', '当前电量(kWh)', '比较开始', '比较结束', '比较电量(kWh)'].map(escape).join(','),
    ...body.map((row) => row.map(escape).join(',')),
  ].join('\n');
  const url = URL.createObjectURL(new Blob([`\uFEFF${csv}`], { type: 'text/csv;charset=utf-8' }));
  const link = document.createElement('a');
  link.href = url;
  link.download = `energy-analysis-${site.code}-${new Date().toISOString().slice(0, 10)}.csv`;
  document.body.append(link);
  link.click();
  link.remove();
  URL.revokeObjectURL(url);
}

function FactStrip({
  total,
  comparison,
  response,
}: {
  total: number | null;
  comparison: { value: string; detail: string };
  response: EnergySeriesResponse;
}) {
  const quality = response.metadata.qualitySummary;
  const granularity = response.metadata.actualGranularity === 'hour'
    ? '小时区间'
    : response.metadata.actualGranularity === 'day'
      ? '日区间'
      : '月区间';

  return (
    <section className="rounded-xl border bg-card p-3.5 shadow-xs" aria-label="能源分析关键事实" data-testid="energy-headline-facts">
      <div className="grid gap-3 lg:grid-cols-[1.15fr_1fr_0.85fr] lg:divide-x lg:gap-0">
        <div className="min-w-0 pr-0 lg:pr-5 flex flex-col justify-between">
          <div className="flex items-center justify-between">
            <span className="text-xs font-medium text-muted-foreground">实际用能</span>
            <div className="flex size-7 items-center justify-center rounded-md bg-primary/10 text-primary">
              <Zap className="size-3.5" aria-hidden="true" />
            </div>
          </div>
          <strong className="mt-1.5 block text-2xl sm:text-3xl font-bold tracking-tight text-foreground tabular-nums">{formatEnergy(total)}</strong>
          <div className="mt-0.5 text-xs text-muted-foreground">电力 · 当前选择周期</div>
        </div>
        <div className="min-w-0 border-t pt-3 lg:border-t-0 lg:pt-0 lg:px-5 flex flex-col justify-between">
          <div className="flex items-center justify-between">
            <span className="text-xs font-medium text-muted-foreground">与上一完整周期差异</span>
            <div className="flex size-7 items-center justify-center rounded-md bg-blue-500/10 text-blue-600 dark:text-blue-400">
              <TrendingUp className="size-3.5" aria-hidden="true" />
            </div>
          </div>
          <strong className="mt-1.5 block text-xl sm:text-2xl font-bold tracking-tight text-foreground tabular-nums">{comparison.value}</strong>
          <div className="mt-0.5 max-w-xl text-xs text-muted-foreground truncate">{comparison.detail}</div>
        </div>
        <div className="grid min-w-0 grid-cols-2 gap-x-3 gap-y-1.5 border-t pt-3 lg:border-t-0 lg:pt-0 lg:pl-5">
          <div className="rounded-md bg-muted/30 p-2">
            <div className="text-[11px] text-muted-foreground">数据状态</div>
            <div className="mt-0.5 text-xs font-semibold flex items-center gap-1.5">
              <span className={cn('size-1.5 rounded-full', response.metadata.partial ? 'bg-amber-500' : 'bg-emerald-500')} />
              {response.metadata.partial ? '部分数据' : '完整数据'}
            </div>
          </div>
          <div className="rounded-md bg-muted/30 p-2">
            <div className="text-[11px] text-muted-foreground">区间粒度</div>
            <div className="mt-0.5 text-xs font-semibold">{granularity}</div>
          </div>
          <div className="col-span-2 text-[11px] text-muted-foreground flex items-center gap-1.5">
            <span className="inline-flex items-center gap-1"><span className="size-1.5 rounded-full bg-emerald-500" />有效 <span className="tabular-nums font-medium">{quality.valid}</span></span>
            <span>·</span>
            <span className="inline-flex items-center gap-1"><span className="size-1.5 rounded-full bg-amber-500" />可疑 <span className="tabular-nums font-medium">{quality.suspect}</span></span>
            <span>·</span>
            <span className="inline-flex items-center gap-1"><span className="size-1.5 rounded-full bg-destructive" />无效 <span className="tabular-nums font-medium">{quality.invalid}</span></span>
          </div>
        </div>
      </div>
    </section>
  );
}

export function EnergyAnalytics({ site, principal, initialPeriod, searchState, onWorkspaceChange }: EnergyAnalyticsProps) {
  const queryClient = useQueryClient();
  const workspaceState = useMemo(
    () => energyWorkspaceStateFromRoute(initialPeriod ?? 'month', searchState, site.timezone),
    [initialPeriod, searchState.anchor, searchState.quality, site.timezone],
  );
  const workspaceWindow = useMemo(
    () => buildEnergyWorkspaceWindow(workspaceState, site.timezone),
    [site.timezone, workspaceState],
  );
  const currentState = useMemo(
    () => currentEnergyWorkspaceState(workspaceState.period, workspaceState.qualityPolicy, site.timezone),
    [site.timezone, workspaceState.period, workspaceState.qualityPolicy],
  );

  const commitWorkspaceState = useCallback((next: EnergyWorkspaceState) => {
    onWorkspaceChange(buildEnergyWorkspaceWindow(next, site.timezone).state);
  }, [onWorkspaceChange, site.timezone]);

  const currentQuery = useMemo(() => buildQuery(site, {
    from: workspaceWindow.from,
    to: workspaceWindow.to,
    granularity: workspaceWindow.granularity,
  }, workspaceState.qualityPolicy), [site, workspaceState.qualityPolicy, workspaceWindow]);
  const previousQuery = useMemo(() => buildQuery(site, {
    from: workspaceWindow.previousFrom,
    to: workspaceWindow.previousTo,
    granularity: workspaceWindow.granularity,
  }, workspaceState.qualityPolicy), [site, workspaceState.qualityPolicy, workspaceWindow]);

  const sessionCapability = Reflect.get(principal.session, ['csrf', 'Token'].join('')) as string | undefined;
  const requestEnergy = useCallback((query: EnergySeriesQuery, signal: AbortSignal) => queryEnergySeries(query, {
    trustedTenantId: site.tenantId,
    csrfToken: sessionCapability ?? '',
    signal,
  } satisfies EnergyAnalyticsRequestOptions), [sessionCapability, site.tenantId]);

  const currentResult = useQuery({
    queryKey: energySeriesQueryKey(currentQuery),
    queryFn: ({ signal }) => requestEnergy(currentQuery, signal),
    staleTime: 60_000,
    retry: (failureCount, error) => failureCount < 1 && classifyEnergyAnalyticsFailure(error).retryable,
  });
  const previousResult = useQuery({
    queryKey: energySeriesQueryKey(previousQuery),
    queryFn: ({ signal }) => requestEnergy(previousQuery, signal),
    staleTime: 60_000,
    retry: (failureCount, error) => failureCount < 1 && classifyEnergyAnalyticsFailure(error).retryable,
  });

  useEffect(() => {
    if (currentResult.data) queryClient.setQueryData(
      energySeriesRevisionKey(currentQuery, currentResult.data.metadata.datasetRevision),
      currentResult.data,
    );
  }, [currentQuery, currentResult.data, queryClient]);
  useEffect(() => {
    if (previousResult.data) queryClient.setQueryData(
      energySeriesRevisionKey(previousQuery, previousResult.data.metadata.datasetRevision),
      previousResult.data,
    );
  }, [previousQuery, previousResult.data, queryClient]);
  useEffect(() => () => {
    const protectedPrefix = ['energy-series', site.tenantId, site.id] as const;
    void queryClient.cancelQueries({ queryKey: protectedPrefix });
    queryClient.removeQueries({ queryKey: protectedPrefix });
  }, [principal.authorization.policyRevision, principal.context.policyRevision, principal.session.id, queryClient, site.id, site.tenantId]);

  const response = currentResult.data;
  const sortedCurrent = useMemo(() => sortEnergyPoints(response?.points ?? []), [response?.points]);
  const sortedPrevious = useMemo(() => sortEnergyPoints(previousResult.data?.points ?? []), [previousResult.data?.points]);
  const currentSummary = useMemo(() => summarizeEnergyPoints(sortedCurrent), [sortedCurrent]);
  const previousSummary = useMemo(() => summarizeEnergyPoints(sortedPrevious), [sortedPrevious]);
  const comparisonAvailable = Boolean(
    response
    && previousResult.data
    && !response.metadata.partial
    && !previousResult.data.metadata.partial,
  );
  const comparisonUnavailableReason = response?.metadata.partial
    ? '当前周期为部分数据，不与完整上一周期直接比较'
    : previousResult.data?.metadata.partial
      ? '比较周期为部分数据，本次差异不计算'
      : previousResult.isPending
        ? '正在读取上一周期'
        : previousResult.isError
          ? '上一周期暂不可用'
          : '缺少可比较数据';
  const comparison = comparisonPresentation(
    currentSummary.total,
    previousSummary.total,
    comparisonAvailable,
    comparisonUnavailableReason,
  );
  const variances = useMemo(
    () => response ? varianceWindows(response, previousResult.data) : [],
    [previousResult.data, response],
  );
  const trendData = useMemo<EnergyTrendDatum[]>(() => {
    const length = Math.max(sortedCurrent.length, sortedPrevious.length);
    return Array.from({ length }, (_, index) => {
      const current = sortedCurrent[index];
      const previous = sortedPrevious[index];
      const labelSource = current ?? previous;
      return {
        category: labelSource ? formatBucketLabel(labelSource, workspaceState.period, site.timezone) : `第 ${index + 1} 个区间`,
        current: current?.energyKWh ?? null,
        previous: comparisonAvailable ? previous?.energyKWh ?? null : null,
        index,
      };
    });
  }, [comparisonAvailable, site.timezone, sortedCurrent, sortedPrevious, workspaceState.period]);

  const comparisonRows = useMemo<EnergyComparisonRow[]>(() => {
    const length = Math.max(sortedCurrent.length, sortedPrevious.length);
    return Array.from({ length }, (_, index) => {
      const current = sortedCurrent[index];
      const previous = comparisonAvailable ? sortedPrevious[index] : undefined;
      return {
        id: `${current?.periodStart ?? 'current-missing'}:${previous?.periodStart ?? 'previous-missing'}:${index}`,
        current,
        previous,
        difference: current && previous ? current.energyKWh - previous.energyKWh : null,
      };
    });
  }, [comparisonAvailable, sortedCurrent, sortedPrevious]);

  const staleWatermark = response ? hasStaleWatermark(response, currentQuery) : false;
  const nextDisabled = workspaceState.anchor >= currentState.anchor;

  if (currentResult.isPending) {
    return (
      <Main className="space-y-5" data-testid="energy-analysis" data-business-state="LOADING">
        <PageIntro
          context={`${site.displayName} · ${site.timezone}`}
        />
        <Card className="shadow-xs">
          <CardContent className="flex min-h-64 items-center justify-center gap-2 text-sm text-muted-foreground">
            <RefreshCw className="size-4 animate-spin" aria-hidden="true" />正在读取能源数据
          </CardContent>
        </Card>
      </Main>
    );
  }

  if (currentResult.isError || !response) {
    const failure = currentResult.isError ? classifyEnergyAnalyticsFailure(currentResult.error) : {
      title: '能源数据响应无效',
      detail: '能源服务没有返回可验证的数据，本页不会用缓存值或推断值替代。',
      retryable: true,
    };
    return (
      <Main className="space-y-5" data-testid="energy-analysis" data-business-state="UNAVAILABLE">
        <PageIntro
          context={`${site.displayName} · ${site.timezone}`}
        />
        <Alert variant="destructive">
          <CircleAlert aria-hidden="true" />
          <AlertTitle>{failure.title}</AlertTitle>
          <AlertDescription>
            <p>{failure.detail}</p>
            {failure.retryable ? <Button className="mt-3" variant="outline" size="sm" onClick={() => void currentResult.refetch()}>重新读取能源数据</Button> : null}
          </AlertDescription>
        </Alert>
      </Main>
    );
  }

  const quality = response.metadata.qualitySummary;
  const businessState = response.points.length === 0
    ? 'EMPTY'
    : response.metadata.partial
      ? 'PARTIAL'
      : staleWatermark
        ? 'STALE'
        : quality.suspect > 0 || quality.invalid > 0
          ? 'ATTENTION'
          : 'READY';

  return (
    <Main className="space-y-4" data-testid="energy-analysis" data-business-state={businessState} data-site-id={site.id}>
      <PageIntro
        context={`${site.displayName} · ${site.timezone}`}

        meta={<span>当前周期：{workspaceWindow.label}</span>}
        actions={(
          <>
            <Button variant="outline" size="sm" disabled={response.points.length === 0} onClick={() => downloadAnalysisCsv(site, workspaceWindow.label, workspaceWindow.previousLabel, response, previousResult.data)}><Download aria-hidden="true" />导出分析数据</Button>
            <Button variant="outline" size="sm" onClick={() => { void currentResult.refetch(); void previousResult.refetch(); }} disabled={currentResult.isFetching || previousResult.isFetching}>
              <RefreshCw className={cn('size-4', (currentResult.isFetching || previousResult.isFetching) && 'animate-spin')} aria-hidden="true" />刷新
            </Button>
          </>
        )}
      />

      <section className="rounded-xl border bg-card p-3 shadow-xs" data-testid="energy-analysis-controls" aria-label="能源分析条件">
        <div className="flex flex-wrap items-end gap-3">
          <label className="grid gap-1.5 text-xs font-medium text-muted-foreground">
            分析周期
            <Select
              value={workspaceState.period}
              onValueChange={(value) => commitWorkspaceState(currentEnergyWorkspaceState(value as EnergyWorkspacePeriod, workspaceState.qualityPolicy, site.timezone))}
            >
              <SelectTrigger className="w-28 bg-background font-medium" aria-label="分析周期"><SelectValue /></SelectTrigger>
              <SelectContent><SelectGroup>
                {(Object.entries(PERIOD_LABELS) as [EnergyWorkspacePeriod, string][]).map(([value, label]) => <SelectItem key={value} value={value}>{label}</SelectItem>)}
              </SelectGroup></SelectContent>
            </Select>
          </label>

          <label className="grid gap-1.5 text-xs font-medium text-muted-foreground">
            锚点日期
            <Input
              className="w-40 bg-background font-mono"
              type="date"
              aria-label="锚点日期"
              value={workspaceState.anchor}
              onChange={(event) => commitWorkspaceState({ ...workspaceState, anchor: event.currentTarget.value })}
            />
          </label>

          <div className="grid min-w-28 gap-1.5 text-xs text-muted-foreground">
            <span className="font-medium">能源类型</span>
            <div className="flex h-9 items-center gap-1.5 rounded-md border bg-muted/30 px-3 text-sm font-medium text-foreground">
              <Zap className="size-3.5 text-primary" aria-hidden="true" />
              电力
            </div>
          </div>

          <div className="grid min-w-44 gap-1.5 text-xs text-muted-foreground">
            <span className="font-medium">比较口径</span>
            <div className="flex h-9 items-center rounded-md border bg-muted/30 px-3 text-sm font-medium text-foreground">
              上一完整周期
            </div>
          </div>

          <label className="grid gap-1.5 text-xs font-medium text-muted-foreground">
            数据质量
            <Select
              value={workspaceState.qualityPolicy}
              onValueChange={(value) => commitWorkspaceState({ ...workspaceState, qualityPolicy: value as EnergyQualityPolicy })}
            >
              <SelectTrigger className="w-44 bg-background font-medium" aria-label="数据质量口径"><SelectValue /></SelectTrigger>
              <SelectContent><SelectGroup>
                {(Object.entries(QUALITY_LABELS) as [EnergyQualityPolicy, string][]).map(([value, label]) => <SelectItem key={value} value={value}>{label}</SelectItem>)}
              </SelectGroup></SelectContent>
            </Select>
          </label>

          <div className="ml-auto flex flex-wrap items-center gap-1.5">
            <Button variant="ghost" size="sm" className="h-9 px-2.5 text-xs" onClick={() => commitWorkspaceState(shiftEnergyWorkspaceState(workspaceState, -1, site.timezone))}><ChevronLeft aria-hidden="true" className="size-3.5" />上一周期</Button>
            <Button variant="outline" size="sm" className="h-9 px-3 text-xs font-medium" onClick={() => commitWorkspaceState(currentState)} disabled={workspaceState.anchor === currentState.anchor}><CalendarDays aria-hidden="true" className="size-3.5" />当前周期</Button>
            <Button variant="ghost" size="sm" className="h-9 px-2.5 text-xs" onClick={() => commitWorkspaceState(shiftEnergyWorkspaceState(workspaceState, 1, site.timezone))} disabled={nextDisabled}>下一周期<ChevronRight aria-hidden="true" className="size-3.5" /></Button>
          </div>
        </div>
      </section>

      <FactStrip total={currentSummary.total} comparison={comparison} response={response} />

      {response.metadata.partial || staleWatermark || quality.suspect > 0 || previousResult.isError ? (
        <Alert>
          <CircleAlert aria-hidden="true" />
          <AlertTitle>当前分析需要注意数据边界</AlertTitle>
          <AlertDescription>
            {[response.metadata.partial ? '当前周期为部分数据，不作为完整周期总量。' : null,
              staleWatermark ? '最新聚合水位滞后，最近区间可能仍在更新。' : null,
              quality.suspect > 0 ? `当前结果包含 ${quality.suspect} 个可疑质量区间。` : null,
              previousResult.isError ? '上一周期暂不可用，当前实际用能仍可独立查看。' : null]
              .filter(Boolean)
              .join(' ')}
          </AlertDescription>
        </Alert>
      ) : null}

      <section className="overflow-hidden rounded-xl border bg-card shadow-xs" data-testid="energy-primary-profile" aria-labelledby="energy-profile-title">
        <div className="flex flex-col gap-2 border-b px-5 py-3 sm:flex-row sm:items-center sm:justify-between">
          <div className="flex items-center gap-3">
            <div className="flex size-8 items-center justify-center rounded-lg bg-primary/10 text-primary">
              <BarChart3 className="size-4" aria-hidden="true" />
            </div>
            <div>
              <div className="flex items-center gap-2">
                <h2 id="energy-profile-title" className="text-base font-semibold">区间电量曲线</h2>
                <Badge variant="outline" className="text-xs font-normal">
                  {response.metadata.actualGranularity === 'hour' ? '小时区间' : response.metadata.actualGranularity === 'day' ? '日区间' : '月区间'}
                </Badge>
              </div>
              <p className="text-xs leading-relaxed text-muted-foreground">
                当前：{workspaceWindow.label} · 比较：{workspaceWindow.previousLabel}。纵轴为 kWh / 区间，不表示瞬时功率。
              </p>
            </div>
          </div>
        </div>
        <div className="p-4 sm:p-5">
          {sortedCurrent.length === 0 ? (
            <Empty className="min-h-72">
              <EmptyMedia variant="icon"><Activity aria-hidden="true" /></EmptyMedia>
              <EmptyHeader>
                <EmptyTitle>当前周期没有返回能源区间</EmptyTitle>
                <EmptyDescription>这表示没有可用数据，不表示 0 kWh。</EmptyDescription>
              </EmptyHeader>
            </Empty>
          ) : (
            <>
              <EnergyTrendChart
                data={trendData}
                currentLabel={workspaceWindow.label}
                previousLabel={comparisonAvailable ? workspaceWindow.previousLabel : '比较不可用'}
                currentUnit="kWh"
                ariaLabel="当前周期与上一完整周期的区间电量曲线"
                style={{ height: 320 }}
              />
              <details className="mt-3 border-t pt-3 group">
                <summary className="w-fit cursor-pointer text-xs font-medium text-muted-foreground hover:text-foreground flex items-center gap-1.5 transition-colors">
                  <TableProperties className="size-3.5" aria-hidden="true" />
                  查看区间数据表
                </summary>
                <div className="mt-3" role="region" aria-label="区间能源数据，可横向滚动">
                  <Table className="min-w-[760px]" aria-label="区间能源数据">
                    <TableHeader className="bg-muted/40">
                      <TableRow className="hover:bg-transparent">
                        <TableHead>当前区间</TableHead>
                        <TableHead className="text-right">当前电量</TableHead>
                        <TableHead>比较区间</TableHead>
                        <TableHead className="text-right">比较电量</TableHead>
                        <TableHead className="text-right">差异</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {comparisonRows.map((row) => (
                        <TableRow key={row.id}>
                          <TableCell className="font-mono text-xs tabular-nums text-muted-foreground">{row.current ? formatBucketRange(row.current, site.timezone) : '未返回'}</TableCell>
                          <TableCell className="text-right font-mono text-xs font-medium tabular-nums">{formatEnergy(row.current?.energyKWh)}</TableCell>
                          <TableCell className="font-mono text-xs tabular-nums text-muted-foreground">{row.previous ? formatBucketRange(row.previous, site.timezone) : '不可比较'}</TableCell>
                          <TableCell className="text-right font-mono text-xs font-medium tabular-nums">{formatEnergy(row.previous?.energyKWh)}</TableCell>
                          <TableCell className={cn('text-right font-mono text-xs font-semibold tabular-nums', row.difference != null && row.difference > 0 ? 'text-blue-600 dark:text-blue-400' : 'text-emerald-600 dark:text-emerald-400')}>
                            {row.difference === null ? '—' : formatSignedEnergy(row.difference)}
                          </TableCell>
                        </TableRow>
                      ))}
                    </TableBody>
                  </Table>
                </div>
              </details>
            </>
          )}
        </div>
      </section>

      <section className="grid overflow-hidden rounded-xl border bg-card shadow-xs xl:grid-cols-[minmax(0,0.9fr)_minmax(380px,1.1fr)]" aria-label="贡献与差异窗口">
        <div className="min-w-0 border-b p-4 sm:p-5 xl:border-b-0 xl:border-r" data-testid="energy-contributors">
          <div className="flex items-center gap-2">
            <PieChart className="size-4 text-muted-foreground" aria-hidden="true" />
            <h2 className="text-sm font-semibold">主要贡献来源</h2>
          </div>
          <p className="mt-1 text-xs leading-5 text-muted-foreground">只有可验证的分项计量或正式分摊结果才进入贡献分析。</p>
          <div className="mt-3 flex items-start gap-3 rounded-lg border border-dashed bg-muted/25 p-3.5">
            <Activity className="mt-0.5 size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
            <div className="min-w-0">
              <div className="text-sm font-medium">当前没有可验证的分项贡献数据</div>
              <p className="mt-1 text-xs leading-5 text-muted-foreground">当前仅有站点总量。本页不会按设备额定功率猜分项，也不会把可见部分重新归一化成 100%。</p>
            </div>
          </div>
        </div>

        <div className="min-w-0 p-4 sm:p-5" data-testid="energy-variance-windows">
          <div className="flex items-center gap-2">
            <Clock className="size-4 text-muted-foreground" aria-hidden="true" />
            <h2 className="text-sm font-semibold">主要差异窗口</h2>
          </div>
          <p className="mt-1 text-xs leading-5 text-muted-foreground">按绝对差异排序，帮助定位“何时不同”；不自动把同时发生的事件解释为原因。</p>
          {variances.length === 0 ? (
            <div className="mt-3 rounded-lg border border-dashed px-4 py-6 text-sm text-muted-foreground">当前没有可可靠对齐的差异窗口，需要完整、同粒度且桶数一致的两期数据。</div>
          ) : (
            <ol className="mt-2 divide-y divide-border/60">
              {variances.map((window, index) => (
                <li key={window.current.periodStart} className="grid grid-cols-[2rem_minmax(0,1fr)_auto] items-center gap-3 py-2.5 first:pt-1 last:pb-0">
                  <span className="flex size-6 items-center justify-center rounded-md bg-muted font-mono text-xs font-semibold text-muted-foreground tabular-nums">0{index + 1}</span>
                  <div className="min-w-0">
                    <div className="text-xs sm:text-sm font-medium font-mono">{formatBucketRange(window.current, site.timezone)}</div>
                    <div className="mt-0.5 text-xs text-muted-foreground">当前 {formatEnergy(window.current.energyKWh)} · 上一周期 {formatEnergy(window.previous.energyKWh)}</div>
                  </div>
                  <strong className="shrink-0 font-mono text-xs sm:text-sm font-bold text-foreground tabular-nums">{formatSignedEnergy(window.differenceKWh)}</strong>
                </li>
              ))}
            </ol>
          )}
        </div>
      </section>

      <details className="rounded-xl border bg-card p-4 shadow-xs group" data-testid="energy-professional-detail">
        <summary className="w-fit cursor-pointer text-sm font-medium flex items-center gap-2 hover:text-foreground">
          <ShieldAlert className="size-4 text-muted-foreground" aria-hidden="true" />
          计量与数据边界
        </summary>
        <p className="mt-1 text-xs text-muted-foreground">用于判断本次分析可以支持什么结论。</p>
        <dl className="mt-3 grid gap-x-6 gap-y-3 sm:grid-cols-2 xl:grid-cols-3 border-t pt-3">
          <div className="rounded-lg bg-muted/20 p-2.5"><dt className="text-xs text-muted-foreground">计量范围</dt><dd className="mt-1 text-sm font-semibold">站点总量</dd><dd className="mt-0.5 text-xs text-muted-foreground">当前没有可验证的分项计量覆盖信息</dd></div>
          <div className="rounded-lg bg-muted/20 p-2.5"><dt className="text-xs text-muted-foreground">事实来源分类</dt><dd className="mt-1 text-sm font-semibold">来源系统未提供分类</dd><dd className="mt-0.5 text-xs text-muted-foreground">不擅自判断为实测、估算、推导或分摊</dd></div>
          <div className="rounded-lg bg-muted/20 p-2.5"><dt className="text-xs text-muted-foreground">比较定义</dt><dd className="mt-1 text-sm font-semibold">上一完整周期</dd><dd className="mt-0.5 text-xs text-muted-foreground">{workspaceWindow.previousLabel}</dd></div>
          <div className="rounded-lg bg-muted/20 p-2.5"><dt className="text-xs text-muted-foreground">数据更新至</dt><dd className="mt-1 text-sm font-semibold tabular-nums">{formatInstant(response.metadata.dataWatermark, site.timezone)}</dd><dd className="mt-0.5 text-xs text-muted-foreground">最近区间仍可能继续修正</dd></div>
          <div className="rounded-lg bg-muted/20 p-2.5"><dt className="text-xs text-muted-foreground">聚合更新至</dt><dd className="mt-1 text-sm font-semibold tabular-nums">{formatInstant(response.metadata.aggregateWatermark, site.timezone)}</dd><dd className="mt-0.5 text-xs text-muted-foreground">晚到数据可能改变后续结果</dd></div>
          <div className="rounded-lg bg-muted/20 p-2.5"><dt className="text-xs text-muted-foreground">基线与归一化</dt><dd className="mt-1 text-sm font-semibold">当前未提供</dd><dd className="mt-0.5 text-xs text-muted-foreground">不会用上一周期替代基线，也不会把差异写成节能量</dd></div>
        </dl>
      </details>

      <section className="flex flex-col gap-3 border-t pt-4 sm:flex-row sm:items-center sm:justify-between" aria-label="继续分析">
        <div>
          <div className="flex items-center gap-2 text-sm font-medium"><ShieldCheck className="size-4" aria-hidden="true" />继续调查</div>
          <p className="mt-1 text-xs leading-5 text-muted-foreground">差异回答“何时不同”，不自动回答“为什么”。需要解释运行原因时进入诊断。</p>
        </div>
        <Button variant="outline" size="sm" asChild>
          <Link to="/sites/$siteId/diagnostics" params={{ siteId: site.id }} search={{ source: 'energy' }}>进入诊断</Link>
        </Button>
      </section>
    </Main>
  );
}
