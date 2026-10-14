import { useQuery, useQueryClient } from '@tanstack/react-query';
import { getRouteApi } from '@tanstack/react-router';
import { RefreshCw, TrendingUp } from 'lucide-react';
import { useMemo, type ReactNode } from 'react';
import { Area, CartesianGrid, ComposedChart, Line, ReferenceLine, XAxis, YAxis } from 'recharts';
import { PageHeader, PageMeta } from '@/blocks/page-header';
import { Panel, PanelHeader } from '@/blocks/panel';
import { InfoTip } from '@/components/info-tip';
import { Main } from '@/components/layout/Main';
import { Button } from '@/components/ui/button';
import { ChartContainer, ChartTooltip, type ChartConfig } from '@/components/ui/chart';
import { Skeleton } from '@/components/ui/skeleton';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { plantDemandQuery, plantPowerPoint } from '@/features/demand/query';
import { demandSummary } from '@/features/demand/demand-model';
import { siteDayKey } from '@/features/energy-analysis/consumption/model';
import { useRealtimePlant } from '@/features/operations/realtime/use-realtime-plant';
import { PERIOD_COLOR } from '@/features/tariff/period-presentation';
import { siteTariffQuery } from '@/features/tariff/query';
import { PERIOD_LABEL, periodAt } from '@/features/tariff/tariff-model';
import { useNow } from '@/hooks/use-now';
import { formatDecimal, formatTime } from '@/lib/operator-format';
import { cn } from '@/lib/utils';
import { forecastChartRows, forecastSummary, hourlyForecast, nextDay, type ForecastChartRow, type HourlyForecast } from '../forecast-model';
import { siteLoadForecastQuery, useRecentLoad } from '../query';

const siteRoute = getRouteApi('/_app/_site');

const chartConfig = {
  actualKW: { label: '实际负荷', color: 'var(--chart-5)' },
  forecastKW: { label: '预测负荷', color: 'var(--chart-1)' },
  band: { label: '预测区间', color: 'var(--chart-1)' },
} satisfies ChartConfig;

function ChartTooltipBody({ active, payload, timeZone }: { active?: boolean; payload?: { payload: ForecastChartRow }[]; timeZone: string }) {
  const row = payload?.[0]?.payload;
  if (!active || !row) return null;
  return (
    <div className="grid min-w-40 gap-1.5 rounded-lg border bg-background px-2.5 py-1.5 text-xs shadow-xl">
      <p className="font-medium">{formatTime(new Date(row.at).toISOString(), timeZone)}</p>
      {row.actualKW !== null ? <p className="flex justify-between gap-4"><span className="text-muted-foreground">实际</span><span className="tabular-nums">{formatDecimal(row.actualKW, 1)} kW</span></p> : null}
      {row.forecastKW !== null ? <p className="flex justify-between gap-4"><span className="text-muted-foreground">预测</span><span className="tabular-nums">{formatDecimal(row.forecastKW, 1)} kW</span></p> : null}
      {row.band ? <p className="flex justify-between gap-4"><span className="text-muted-foreground">区间</span><span className="tabular-nums">{formatDecimal(row.band[0], 0)}–{formatDecimal(row.band[1], 0)} kW</span></p> : null}
    </div>
  );
}

function Figure({ label, value, unit, children }: { readonly label: string; readonly value: string; readonly unit: string; readonly children: ReactNode }) {
  return (
    <div className="flex min-w-0 flex-col gap-1 px-5 py-4">
      <span className="text-xs font-medium text-muted-foreground">{label}</span>
      <span className="text-2xl leading-[29px] font-semibold tracking-[-0.03em] tabular-nums">
        {value}<span className="ml-1 text-xs font-medium tracking-normal text-subtle-foreground">{unit}</span>
      </span>
      <span className="text-xs text-muted-foreground">{children}</span>
    </div>
  );
}

interface HourRow extends HourlyForecast {
  readonly tomorrow: boolean;
  readonly period: ReturnType<typeof periodAt>;
  readonly yesterdayKW: number | null;
  /** How far the hour's upper bound sits below the month's peak demand, in an hour that is billed on demand. */
  readonly headroomKW: number | null;
}

function HourTable({ rows }: { readonly rows: readonly HourRow[] }) {
  return (
    <Table aria-label="逐时预测负荷" className="[&_td]:whitespace-nowrap [&_th]:whitespace-nowrap">
      <TableHeader>
        <TableRow className="hover:bg-transparent">
          <TableHead className="pl-5">时段</TableHead>
          <TableHead>电价</TableHead>
          <TableHead className="text-right">预测</TableHead>
          <TableHead className="text-right">区间</TableHead>
          <TableHead className="text-right">昨日同时段</TableHead>
          <TableHead className="pr-5 text-right">距本月最大</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {rows.map((hour) => (
          <TableRow key={hour.start} className={cn(hour.peak && 'bg-primary-soft hover:bg-primary-soft')}>
            <TableCell className="pl-5">
              <span className="flex items-center gap-2">
                <span className="font-mono text-xs">{hour.label}</span>
                {hour.tomorrow && hour.label === '00:00' ? <span className="rounded-md bg-secondary px-1.5 text-[11px] leading-[18px] text-secondary-foreground">明天</span> : null}
                {hour.peak ? <span className="rounded-md bg-primary px-1.5 text-[11px] leading-[18px] font-medium text-primary-foreground">峰值</span> : null}
              </span>
            </TableCell>
            <TableCell>
              {hour.period ? (
                <span className="flex items-center gap-1.5 text-xs">
                  <span className="size-2 rounded-[2px]" style={{ background: PERIOD_COLOR[hour.period.periodCode] }} aria-hidden="true" />
                  {PERIOD_LABEL[hour.period.periodCode]}
                </span>
              ) : <span className="text-muted-foreground">—</span>}
            </TableCell>
            <TableCell className="text-right font-medium tabular-nums">{formatDecimal(hour.loadKW, 0)}<span className="ml-0.5 text-xs font-normal text-muted-foreground">kW</span></TableCell>
            <TableCell className="text-right text-xs text-muted-foreground tabular-nums">
              {hour.lowerKW !== null && hour.upperKW !== null ? `${formatDecimal(hour.lowerKW, 0)}–${formatDecimal(hour.upperKW, 0)}` : '—'}
            </TableCell>
            <TableCell className="text-right text-muted-foreground tabular-nums">{hour.yesterdayKW === null ? '—' : formatDecimal(hour.yesterdayKW, 0)}</TableCell>
            <TableCell className="pr-5 text-right tabular-nums">
              {hour.headroomKW === null ? <span className="text-muted-foreground">—</span> : (
                <span className={cn('font-medium', hour.headroomKW < 20 && 'text-warning-text', hour.headroomKW < 0 && 'text-destructive-text')}>
                  {hour.headroomKW < 0 ? `超出 ${formatDecimal(-hour.headroomKW, 0)}` : formatDecimal(hour.headroomKW, 0)}
                  <span className="ml-0.5 text-xs font-normal text-muted-foreground">kW</span>
                </span>
              )}
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}

export function ForecastWorkspace() {
  const { site, principal } = siteRoute.useRouteContext();
  const queryClient = useQueryClient();
  const now = useNow(60_000);
  const forecast = useQuery(siteLoadForecastQuery(site.id));
  const recent = useRecentLoad();
  const tariff = useQuery(siteTariffQuery(site.id));
  const { registry } = useRealtimePlant();
  const point = useMemo(() => (registry.data ? plantPowerPoint(registry.data.assetModel) : null), [registry.data]);
  const demand = useQuery(plantDemandQuery({ siteId: site.id, timezone: site.timezone, csrfToken: principal.session.csrfToken, point, now: new Date(now) }));
  const monthPeak = useMemo(() => (demand.data ? demandSummary(demand.data, new Date(now), site.timezone).month : null), [demand.data, now, site.timezone]);
  const data = useMemo(() => (forecast.data ? nextDay(forecast.data) : forecast.data), [forecast.data]);

  const summary = useMemo(() => (data ? forecastSummary(data) : null), [data]);
  const rows = useMemo(() => (data ? forecastChartRows(recent.data ?? [], data) : []), [data, recent.data]);
  const hours = useMemo((): HourRow[] => {
    if (!data) return [];
    const today = siteDayKey(new Date(now), site.timezone);
    // The same clock hour a day earlier falls in the last 24 metered hours.
    const yesterday = new Map((recent.data ?? []).map((hour) => [Date.parse(hour.periodStart), hour.electricityKWh]));
    return hourlyForecast(data, site.timezone).map((hour) => {
      const start = new Date(hour.start);
      const hourStart = Math.floor(start.getTime() / 3_600_000) * 3_600_000;
      const period = tariff.data ? periodAt(tariff.data, start, site.timezone) : null;
      const billed = period !== null && period.demandRatePerKW > 0;
      return {
        ...hour,
        tomorrow: siteDayKey(start, site.timezone) !== today,
        period,
        yesterdayKW: yesterday.get(hourStart - 86_400_000) ?? null,
        headroomKW: billed && monthPeak ? monthPeak.demandKW - (hour.upperKW ?? hour.loadKW) : null,
      };
    });
  }, [data, recent.data, tariff.data, monthPeak, now, site.timezone]);

  const tick = (at: number) => formatTime(new Date(at).toISOString(), site.timezone).slice(-5);
  // Whole hours every four hours, so the axis reads as a clock rather than odd minutes.
  const ticks = useMemo(() => {
    if (rows.length === 0) return [];
    const first = Math.ceil(rows[0].at / 3_600_000) * 3_600_000;
    const last = rows[rows.length - 1].at;
    return Array.from({ length: Math.floor((last - first) / 14_400_000) + 1 }, (_, index) => first + index * 14_400_000);
  }, [rows]);
  const when = (at: string) => `${siteDayKey(new Date(at), site.timezone) === siteDayKey(new Date(now), site.timezone) ? '今天' : '明天'} ${formatTime(at, site.timezone).slice(-5)}`;
  const half = Math.ceil(hours.length / 2);

  return (
    <Main>
      <PageHeader
        title="负荷预测"
        description={`${site.displayName} · 空调总电表`}
        meta={data ? (
          <PageMeta dot={data.quality === 'VALID' ? 'live' : 'warning'}>
            {data.quality === 'VALID' ? '按近 7 天同时段负荷预测' : '历史不足，暂按最近读数延续'}
          </PageMeta>
        ) : undefined}
        actions={(
          <>
            {data ? (
              <span className="flex items-center gap-1 text-xs text-muted-foreground">
                预测更新于 <span className="font-mono">{formatTime(data.origin, site.timezone).slice(-5)}</span>
                <InfoTip>每小时第 7 分钟自动更新；需要至少一小时的 15 分钟负荷数据</InfoTip>
              </span>
            ) : null}
            <Button variant="outline" size="sm" onClick={() => void queryClient.invalidateQueries({ queryKey: ['forecast', site.id] })}>
              <RefreshCw data-icon="inline-start" />
              刷新
            </Button>
          </>
        )}
      />

      {forecast.isPending ? (
        <Skeleton className="h-[520px] rounded-xl" />
      ) : forecast.isError ? (
        <Panel aria-label="预测概况">
          <div className="flex flex-col items-start gap-2 px-5 py-8">
            <p className="text-sm font-semibold">负荷预测暂时读不到</p>
            <Button variant="outline" size="sm" onClick={() => void forecast.refetch()}>重试</Button>
          </div>
        </Panel>
      ) : data === null || data === undefined || !summary ? (
        <Panel aria-label="预测概况">
          <div className="flex flex-col items-center gap-2 px-6 py-14 text-center">
            <span className="flex size-9 items-center justify-center rounded-xl bg-muted text-muted-foreground"><TrendingUp className="size-[18px]" aria-hidden="true" /></span>
            <p className="text-sm font-semibold">还没有负荷预测</p>
            <p className="text-xs text-muted-foreground">预测需要至少一小时的 15 分钟负荷数据，系统每小时第 7 分钟自动更新</p>
          </div>
        </Panel>
      ) : (
        <>
          <Panel aria-label="预测概况">
            <div className="grid grid-cols-3 divide-x divide-divider border-b border-divider">
              <Figure label="预测峰值负荷" value={formatDecimal(summary.peak.loadKW, 1)} unit="kW">
                {when(summary.peak.at)}
                {monthPeak ? ` · ${summary.peak.loadKW <= monthPeak.demandKW ? '低于' : '高于'}本月最大需量 ${formatDecimal(Math.abs(monthPeak.demandKW - summary.peak.loadKW), 1)} kW` : ''}
              </Figure>
              <Figure label="预测谷值负荷" value={formatDecimal(summary.trough.loadKW, 1)} unit="kW">{when(summary.trough.at)}</Figure>
              <Figure label="未来 24 小时用电" value={formatDecimal(summary.energyKWh, 0)} unit="kWh">按预测负荷累计</Figure>
            </div>
            <section role="region" aria-label="实际与预测" className="flex flex-col gap-2 px-5 pt-3.5 pb-3">
              <div className="flex min-h-7 items-center gap-2.5 text-sm">
                <span className="font-semibold">实际与预测</span>
                <span className="text-xs text-muted-foreground">kW</span>
                <span className="ml-auto flex items-center gap-3 text-xs text-muted-foreground">
                  <span className="flex items-center gap-1.5"><span className="h-0.5 w-3 rounded-full bg-chart-5" aria-hidden="true" />实际 · 每小时</span>
                  <span className="flex items-center gap-1.5"><span className="h-0.5 w-3 rounded-full bg-chart-1" aria-hidden="true" />预测 · 每 15 分钟</span>
                  <span className="flex items-center gap-1.5"><span className="size-2.5 rounded-[2px] bg-chart-1/15" aria-hidden="true" />预测区间</span>
                </span>
              </div>
              <ChartContainer config={chartConfig} className="aspect-auto h-[300px] w-full">
                <ComposedChart data={rows} margin={{ left: 4, right: 12 }}>
                  <CartesianGrid vertical={false} />
                  <XAxis dataKey="at" type="number" scale="time" domain={['dataMin', 'dataMax']} ticks={ticks} tickFormatter={tick} tickLine={false} axisLine={false} />
                  <YAxis tickLine={false} axisLine={false} width={44} domain={[0, 'auto']} />
                  <ChartTooltip cursor={{ strokeDasharray: '3 3' }} content={<ChartTooltipBody timeZone={site.timezone} />} />
                  <Area dataKey="band" stroke="none" fill="var(--color-band)" fillOpacity={0.12} connectNulls={false} isAnimationActive={false} />
                  <Line dataKey="actualKW" stroke="var(--color-actualKW)" strokeWidth={2} dot={false} connectNulls={false} isAnimationActive={false} />
                  <Line dataKey="forecastKW" stroke="var(--color-forecastKW)" strokeWidth={2} dot={false} connectNulls={false} isAnimationActive={false} />
                  <ReferenceLine x={now} stroke="var(--muted-foreground)" strokeDasharray="4 4" label={{ value: '现在', position: 'insideTopRight', fontSize: 11, fill: 'var(--muted-foreground)' }} />
                </ComposedChart>
              </ChartContainer>
            </section>
          </Panel>

          <Panel aria-label="逐时预测">
            <PanelHeader
              title={<span className="flex items-center gap-1.5">逐时预测<InfoTip>{monthPeak ? `距本月最大 = 本月最大需量 ${formatDecimal(monthPeak.demandKW, 1)} kW 减去预测上限；只在计收需量电费的时段计算` : '距本月最大：本月最大需量减去预测上限；只在计收需量电费的时段计算'}</InfoTip></span>}
              caption="每小时平均 · kW"
            />
            <div className="grid grid-cols-2 divide-x divide-divider">
              <HourTable rows={hours.slice(0, half)} />
              <HourTable rows={hours.slice(half)} />
            </div>
          </Panel>
        </>
      )}
    </Main>
  );
}
