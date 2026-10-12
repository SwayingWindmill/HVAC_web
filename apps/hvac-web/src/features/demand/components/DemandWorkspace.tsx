import { useQuery } from '@tanstack/react-query';
import { getRouteApi } from '@tanstack/react-router';
import { Gauge } from 'lucide-react';
import { useMemo } from 'react';
import { Area, Bar, BarChart, CartesianGrid, Cell, ComposedChart, Line, ReferenceArea, ReferenceLine, XAxis, YAxis } from 'recharts';
import { MetricCard, MetricGrid, MetricValue } from '@/blocks/metric-card';
import { PageHeader, PageMeta } from '@/blocks/page-header';
import { Main } from '@/components/layout/Main';
import { Badge } from '@/components/ui/badge';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { ChartContainer, ChartLegend, ChartLegendContent, ChartTooltip, type ChartConfig } from '@/components/ui/chart';
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from '@/components/ui/empty';
import { Skeleton } from '@/components/ui/skeleton';
import { periodWindow } from '@/features/energy-analysis/consumption/model';
import { useRealtimePlant } from '@/features/operations/realtime/use-realtime-plant';
import { PERIOD_COLOR } from '@/features/tariff/period-presentation';
import { siteTariffQuery } from '@/features/tariff/query';
import { daySchedule, demandCharges, PERIOD_LABEL, type DemandCharge } from '@/features/tariff/tariff-model';
import { useNow } from '@/hooks/use-now';
import { formatDecimal, formatTime } from '@/lib/operator-format';
import { demandChartRows, demandSummary, type DemandChartRow, type DemandPeak } from '../demand-model';
import { plantDemandQuery, plantPowerPoint } from '../query';

const siteRoute = getRouteApi('/_app/_site');

const curveConfig = {
  today: { label: '今日', color: 'var(--chart-1)' },
  yesterday: { label: '昨日', color: 'var(--chart-5)' },
} satisfies ChartConfig;

const dailyConfig = { peak: { label: '当日最大需量', color: 'var(--chart-1)' } } satisfies ChartConfig;

const clock = (minute: number) => `${String(Math.floor(minute / 60)).padStart(2, '0')}:${String(minute % 60).padStart(2, '0')}`;

function CurveTooltip({ active, payload }: { active?: boolean; payload?: { payload: DemandChartRow }[] }) {
  const row = payload?.[0]?.payload;
  if (!active || !row) return null;
  return (
    <div className="grid min-w-36 gap-1.5 rounded-lg border bg-background px-2.5 py-1.5 text-xs shadow-xl">
      <p className="font-medium">{clock(row.minute)}–{clock(row.minute + 15)}</p>
      {row.today !== null ? <p className="flex justify-between gap-4"><span className="text-muted-foreground">今日</span><span className="tabular-nums">{formatDecimal(row.today, 1)} kW</span></p> : null}
      {row.yesterday !== null ? <p className="flex justify-between gap-4"><span className="text-muted-foreground">昨日</span><span className="tabular-nums">{formatDecimal(row.yesterday, 1)} kW</span></p> : null}
    </div>
  );
}

function DailyTooltip({ active, payload }: { active?: boolean; payload?: { payload: { day: string; peak: number | null } }[] }) {
  const row = payload?.[0]?.payload;
  if (!active || !row || row.peak === null) return null;
  return (
    <div className="grid min-w-32 gap-1 rounded-lg border bg-background px-2.5 py-1.5 text-xs shadow-xl">
      <p className="font-medium">{row.day}</p>
      <p className="flex justify-between gap-4"><span className="text-muted-foreground">最大需量</span><span className="tabular-nums">{formatDecimal(row.peak, 1)} kW</span></p>
    </div>
  );
}

const peakValue = (peak: DemandPeak | null) => (peak ? formatDecimal(peak.demandKW, 1) : null);

const chargedPeriods = (charges: readonly DemandCharge[]) => charges.map((charge) => `${PERIOD_LABEL[charge.code]}时段`).join('、');

function DemandChargeCard({ charges, unavailable }: { readonly charges: readonly DemandCharge[] | null; readonly unavailable: boolean }) {
  if (unavailable) {
    return <MetricCard label="本月需量电费" value={<MetricValue value={null} />} lead="电价暂不可用" detail="稍后刷新重试" />;
  }
  if (charges === null) {
    return <MetricCard label="本月需量电费" value={<MetricValue value={null} />} lead="站点还没有配置电价" detail="发布电价后按需量电价估算" />;
  }
  if (charges.length === 0) {
    return <MetricCard label="本月需量电费" value={<MetricValue value="0" unit="元" />} lead="现行电价不收需量电费" detail="按电价估算" />;
  }
  const billed = charges.filter((charge) => charge.charge !== null);
  const total = billed.reduce((sum, charge) => sum + charge.charge!, 0);
  return (
    <MetricCard
      label="本月需量电费"
      value={<MetricValue value={billed.length ? formatDecimal(total, 0) : null} unit="元" />}
      badge={<Badge variant="outline" className="font-normal">估算</Badge>}
      lead={billed.length
        ? billed.map((charge) => `${PERIOD_LABEL[charge.code]}段 ${formatDecimal(charge.peak!.demandKW, 1)} kW × ${formatDecimal(charge.ratePerKW, 0)} 元/kW`).join('，')
        : `${chargedPeriods(charges)}本月还没有完整的 15 分钟`}
      detail="按电价估算，正式电费以结算为准"
    />
  );
}

export function DemandWorkspace() {
  const { site, principal } = siteRoute.useRouteContext();
  const now = useNow(60_000);
  const { registry } = useRealtimePlant();
  const point = useMemo(() => (registry.data ? plantPowerPoint(registry.data.assetModel) : null), [registry.data]);
  const demand = useQuery(plantDemandQuery({ siteId: site.id, timezone: site.timezone, csrfToken: principal.session.csrfToken, point, now: new Date(now) }));
  const hours = demand.data;
  const summary = useMemo(() => (hours ? demandSummary(hours, new Date(now), site.timezone) : null), [hours, now, site.timezone]);
  const rows = useMemo(() => (hours ? demandChartRows(hours, new Date(now), site.timezone) : []), [hours, now, site.timezone]);
  const daily = useMemo(
    () => (summary?.dailyPeaks ?? []).map((day) => ({ day: day.day.slice(5).replace('-', '/'), peak: day.peak?.demandKW ?? null, isMax: Boolean(day.peak && summary?.month && day.peak.at === summary.month.at) })),
    [summary],
  );
  const tariff = useQuery(siteTariffQuery(site.id));
  const charges = useMemo(
    () => (tariff.data && hours ? demandCharges(hours, tariff.data, site.timezone, periodWindow('month', new Date(now), site.timezone).from) : null),
    [tariff.data, hours, now, site.timezone],
  );
  // Today's periods that carry a demand rate, shaded on the curve.
  const chargedToday = useMemo(
    () => (tariff.data ? daySchedule(tariff.data, new Date(now), site.timezone).filter((period) => period.demandRatePerKW > 0) : []),
    [tariff.data, now, site.timezone],
  );
  const at = (peak: DemandPeak | null, withDate = false) => (peak ? formatTime(peak.at, site.timezone).slice(withDate ? 0 : -5) : '—');

  return (
    <Main>
      <PageHeader
        title="需量分析"
        description={`${site.displayName} · 冷站用电按 15 分钟平均功率计需量${charges?.length ? `，需量电费按${chargedPeriods(charges)}当月最大需量计收` : ''}`}
        meta={<PageMeta icon={<Gauge />}>空调总电表口径</PageMeta>}
      />

      {registry.isPending || tariff.isPending || (point !== null && demand.isPending) ? (
        <Skeleton className="h-[520px] rounded-xl" />
      ) : point === null ? (
        <Card>
          <Empty className="py-16">
            <EmptyHeader>
              <EmptyMedia variant="icon"><Gauge /></EmptyMedia>
              <EmptyTitle>没有登记空调总电表</EmptyTitle>
              <EmptyDescription>需量按空调总电表的有功功率计算；登记该电表并接入数据后在此显示。</EmptyDescription>
            </EmptyHeader>
          </Empty>
        </Card>
      ) : demand.isError || !summary ? (
        <Card><CardContent className="py-16 text-center text-sm text-muted-foreground">需量数据暂不可用</CardContent></Card>
      ) : (
        <>
          <MetricGrid ariaLabel="需量概况" className="@xl/main:[&>:last-child]:col-span-2 @5xl/main:grid-cols-5 @5xl/main:[&>:last-child]:col-span-1">
            <MetricCard
              label="当前需量"
              value={<MetricValue value={summary.current ? formatDecimal(summary.current.demandKW, 1) : null} unit="kW" />}
              lead={summary.current
                ? `${formatTime(summary.current.start, site.timezone).slice(-5)} 起的 15 分钟${summary.current.complete ? '' : '，进行中'}`
                : '还没有功率数据'}
              detail="15 分钟平均有功功率"
            />
            <MetricCard
              label="今日最大需量"
              value={<MetricValue value={peakValue(summary.today)} unit="kW" />}
              lead={summary.today ? `出现在 ${at(summary.today)}` : '今日还没有完整的 15 分钟'}
              detail={summary.yesterday ? `昨日 ${formatDecimal(summary.yesterday.demandKW, 1)} kW，出现在 ${at(summary.yesterday)}` : '昨日没有需量数据'}
            />
            <MetricCard
              label="本月最大需量"
              value={<MetricValue value={peakValue(summary.month)} unit="kW" />}
              lead={summary.month ? `出现在 ${at(summary.month, true)}` : '本月还没有完整的 15 分钟'}
              detail="全天 15 分钟平均功率的最高值"
            />
            <DemandChargeCard charges={charges} unavailable={tariff.isError} />
            <MetricCard
              label="今日负荷率"
              value={<MetricValue value={summary.todayLoadFactor !== null ? formatDecimal(summary.todayLoadFactor * 100, 0) : null} unit="%" />}
              lead="今日平均需量 ÷ 今日最大需量"
              detail="越高说明用电越平稳"
            />
          </MetricGrid>

          <div className="grid items-start gap-4 md:gap-6 @5xl/main:grid-cols-[minmax(0,2fr)_minmax(0,1fr)]">
            <Card role="region" aria-label="需量曲线">
              <CardHeader>
                <CardTitle>需量曲线</CardTitle>
                <CardDescription>今日与昨日每 15 分钟平均功率（kW），虚线为本月最大需量{chargedToday.length ? `，底色为计收需量的${chargedPeriods(charges ?? [])}` : ''}</CardDescription>
              </CardHeader>
              <CardContent className="px-2 sm:px-6">
                <ChartContainer config={curveConfig} className="aspect-auto h-[340px] w-full">
                  <ComposedChart data={rows} margin={{ left: 4, right: 12, top: 16 }}>
                    <CartesianGrid vertical={false} />
                    <XAxis dataKey="minute" type="number" domain={[0, 1440]} ticks={[0, 240, 480, 720, 960, 1200, 1440]} tickFormatter={clock} tickLine={false} axisLine={false} />
                    <YAxis tickLine={false} axisLine={false} width={44} domain={[0, 'auto']} />
                    {chargedToday.map((period) => (
                      <ReferenceArea key={period.startMinute} x1={period.startMinute} x2={period.endMinute} fill={PERIOD_COLOR[period.periodCode]} fillOpacity={0.08} strokeOpacity={0} ifOverflow="hidden" />
                    ))}
                    <ChartTooltip cursor={{ strokeDasharray: '3 3' }} content={<CurveTooltip />} />
                    <Line dataKey="yesterday" stroke="var(--color-yesterday)" strokeWidth={1.5} strokeDasharray="4 3" dot={false} connectNulls={false} isAnimationActive={false} />
                    <Area dataKey="today" stroke="var(--color-today)" strokeWidth={2} fill="var(--color-today)" fillOpacity={0.12} dot={false} connectNulls={false} isAnimationActive={false} />
                    {summary.month ? (
                      <ReferenceLine y={summary.month.demandKW} stroke="var(--destructive)" strokeDasharray="6 4" label={{ value: `本月最大 ${formatDecimal(summary.month.demandKW, 0)} kW`, position: 'insideTopRight', fontSize: 11, fill: 'var(--destructive)' }} />
                    ) : null}
                    <ChartLegend content={<ChartLegendContent />} />
                  </ComposedChart>
                </ChartContainer>
              </CardContent>
            </Card>

            <Card role="region" aria-label="本月每日最大需量">
              <CardHeader>
                <CardTitle>本月每日最大需量</CardTitle>
                <CardDescription>每天最高的 15 分钟平均功率（kW），本月最大一天已标出</CardDescription>
              </CardHeader>
              <CardContent className="px-2 sm:px-6">
                {daily.length === 0 ? (
                  <p className="py-16 text-center text-sm text-muted-foreground">本月还没有需量数据</p>
                ) : (
                  <ChartContainer config={dailyConfig} className="aspect-auto h-[340px] w-full">
                    <BarChart data={daily} margin={{ left: 4, right: 8, top: 16 }}>
                      <CartesianGrid vertical={false} />
                      <XAxis dataKey="day" tickLine={false} axisLine={false} interval="preserveStartEnd" minTickGap={16} />
                      <YAxis tickLine={false} axisLine={false} width={44} domain={[0, 'auto']} />
                      <ChartTooltip cursor={false} content={<DailyTooltip />} />
                      <Bar dataKey="peak" radius={[3, 3, 0, 0]} maxBarSize={28} isAnimationActive={false}>
                        {daily.map((day) => <Cell key={day.day} fill={day.isMax ? 'var(--destructive)' : 'var(--color-peak)'} />)}
                      </Bar>
                    </BarChart>
                  </ChartContainer>
                )}
              </CardContent>
            </Card>
          </div>
        </>
      )}
    </Main>
  );
}
