import { useQuery, useQueryClient } from '@tanstack/react-query';
import { getRouteApi, useNavigate } from '@tanstack/react-router';
import { Gauge, RefreshCw } from 'lucide-react';
import type { SiteTariff } from '@/api/generated/platformGateway.gen';
import { useMemo, type ReactNode } from 'react';
import { PageHeader, PageMeta } from '@/blocks/page-header';
import { Panel } from '@/blocks/panel';
import { InfoTip } from '@/components/info-tip';
import { Main } from '@/components/layout/Main';
import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { periodWindow, siteDayKey } from '@/features/energy-analysis/consumption/model';
import { useRealtimePlant } from '@/features/operations/realtime/use-realtime-plant';
import { siteTariffQuery } from '@/features/tariff/query';
import { demandCharges, periodAt, periodRanges } from '@/features/tariff/tariff-model';
import { useNow } from '@/hooks/use-now';
import { formatDecimal, formatTime } from '@/lib/operator-format';
import { cn } from '@/lib/utils';
import { demandSummary, type DemandPeak, type QuarterHour } from '../demand-model';
import { forecastBetween, forecastPeak, hourlyForecast, type LoadForecastPoint } from '../forecast-model';
import { expensiveSpans, forecastEnergy, loadCurveRows, loadDays, meteredHour, nearestQuarter, type LoadDays } from '../load-model';
import { plantDemandQuery, plantPowerPoint, siteLoadForecastQuery } from '../query';
import { DailyPeaks, type DailyPeakBar } from './DailyPeaks';
import { DemandCharge } from './DemandCharge';
import { ForecastHours, type ForecastHour } from './ForecastHours';
import { LoadCurve } from './LoadCurve';

// The plant's electrical load: where it stands now, today's and the month's highest, and what the
// forecast expects for the rest of today and tomorrow, on one curve. Load is the quarter-hour
// average power of the HVAC total meter, the figure a demand charge is billed on.

const siteRoute = getRouteApi('/_app/_site');
const pageRoute = getRouteApi('/_app/_site/energy-analysis/load');
const HOUR_MS = 3_600_000;
const QUARTERS_PER_DAY = 96;

type LoadView = 'hours' | 'days' | 'charge';

/** One figure of the overview strip: label, value with unit, and a line of context. */
function Figure({ label, value, unit, info, live, children }: {
  readonly label: string;
  readonly value: string | null;
  readonly unit: string;
  readonly info?: string;
  readonly live?: boolean;
  readonly children?: ReactNode;
}) {
  return (
    <div className="flex min-w-0 flex-col gap-1 px-5 py-4">
      <span className="flex items-center gap-1.5 text-xs font-medium text-muted-foreground">
        {live ? <span className="size-1.5 rounded-full bg-success" aria-hidden="true" /> : null}
        {label}
        {info ? <InfoTip>{info}</InfoTip> : null}
      </span>
      <span className="text-2xl leading-[29px] font-semibold tracking-[-0.03em] tabular-nums">
        {value ?? '—'}
        {value !== null ? <span className="ml-1 text-xs font-medium tracking-normal text-subtle-foreground">{unit}</span> : null}
      </span>
      <span className="text-xs text-muted-foreground">{children}</span>
    </div>
  );
}

/** How far the running quarter-hour has got, one segment per minute. */
function QuarterProgress({ start, now }: { readonly start: string; readonly now: number }) {
  const elapsed = Math.max(0, Math.min(15, Math.floor((now - Date.parse(start)) / 60_000)));
  return (
    <span className="flex gap-[2px]" aria-hidden="true">
      {Array.from({ length: 15 }, (_, index) => <span key={index} className={cn('h-1 flex-1 rounded-full', index < elapsed ? 'bg-primary' : 'bg-muted')} />)}
    </span>
  );
}

function Message({ icon, title, children }: { readonly icon?: ReactNode; readonly title: string; readonly children?: ReactNode }) {
  return (
    <div className="flex flex-col items-center gap-2 px-6 py-14 text-center">
      {icon ? <span className="flex size-9 items-center justify-center rounded-xl bg-muted text-muted-foreground">{icon}</span> : null}
      <p className="text-sm font-semibold">{title}</p>
      {children ? <p className="text-xs text-muted-foreground">{children}</p> : null}
    </div>
  );
}

/** A day's forecast hours with the price period, the metered hour a day earlier and, where demand is billed, the headroom. */
function forecastHours(
  points: readonly LoadForecastPoint[],
  quarters: readonly QuarterHour[],
  days: LoadDays,
  tariff: SiteTariff | undefined,
  monthPeak: DemandPeak | null,
  timeZone: string,
): ForecastHour[] {
  const dayLength = days.today - days.yesterday;
  // The hour holding the day's highest quarter-hour, the figure a demand charge and the overview use.
  const highest = forecastPeak(points);
  const peakHour = highest ? Math.floor(Date.parse(highest.at) / HOUR_MS) : null;
  return hourlyForecast(points, timeZone).map((hour) => {
    const start = new Date(hour.start);
    const period = tariff ? periodAt(tariff, start, timeZone) : null;
    const billed = period !== null && period.demandRatePerKW > 0;
    return {
      ...hour,
      period: period?.periodCode ?? null,
      previousKW: meteredHour(quarters, Math.floor(start.getTime() / HOUR_MS) * HOUR_MS - dayLength),
      headroomKW: billed && monthPeak ? monthPeak.demandKW - (hour.upperKW ?? hour.loadKW) : null,
      peak: Math.floor(start.getTime() / HOUR_MS) === peakHour,
    };
  });
}

export function LoadWorkspace() {
  const { site, principal } = siteRoute.useRouteContext();
  const search = pageRoute.useSearch();
  const navigate = useNavigate({ from: '/energy-analysis/load' });
  const queryClient = useQueryClient();
  const now = useNow(60_000);
  const timeZone = site.timezone;

  const { registry } = useRealtimePlant();
  const point = useMemo(() => (registry.data ? plantPowerPoint(registry.data.assetModel) : null), [registry.data]);
  const demand = useQuery(plantDemandQuery({ siteId: site.id, timezone: timeZone, csrfToken: principal.session.csrfToken, point, now: new Date(now) }));
  const forecast = useQuery(siteLoadForecastQuery(site.id));
  const tariff = useQuery(siteTariffQuery(site.id));
  // Null when the Site has no Tariff: no price periods, no demand charge.
  const prices = tariff.data ?? undefined;

  const days = useMemo(() => loadDays(new Date(now), timeZone), [now, timeZone]);
  const quarters = useMemo(() => demand.data ?? [], [demand.data]);
  const summary = useMemo(() => (demand.data ? demandSummary(demand.data, new Date(now), timeZone) : null), [demand.data, now, timeZone]);
  // Forecast points start at the forecast origin, off the quarter-hour; the page reads each at the nearest one.
  const ahead = useMemo(
    () => (forecast.data ? forecastBetween(forecast.data, days.today, days.end).map((point) => ({ ...point, at: new Date(nearestQuarter(point.at)).toISOString() })) : []),
    [forecast.data, days],
  );
  const tomorrow = useMemo(() => ahead.filter((point) => Date.parse(point.at) >= days.tomorrow), [ahead, days]);
  const tomorrowPeak = useMemo(() => forecastPeak(tomorrow), [tomorrow]);
  const rows = useMemo(() => loadCurveRows(quarters, ahead, days), [quarters, ahead, days]);
  const spans = useMemo(() => (tariff.data ? expensiveSpans(tariff.data, days, timeZone) : []), [tariff.data, days, timeZone]);
  const charges = useMemo(
    () => (tariff.data && demand.data ? demandCharges(demand.data, tariff.data, timeZone, periodWindow('month', new Date(now), timeZone).from) : []),
    [tariff.data, demand.data, now, timeZone],
  );
  const ranges = useMemo(() => (tariff.data ? periodRanges(tariff.data, new Date(now), timeZone) : new Map<string, string>()), [tariff.data, now, timeZone]);
  const monthPeak = summary?.month ?? null;
  const headroom = charges.length > 0;
  const restOfToday = useMemo(
    () => forecastHours(ahead.filter((point) => Date.parse(point.at) < days.tomorrow), quarters, days, prices, monthPeak, timeZone),
    [ahead, quarters, days, prices, monthPeak, timeZone],
  );
  const tomorrowHours = useMemo(() => forecastHours(tomorrow, quarters, days, prices, monthPeak, timeZone), [tomorrow, quarters, days, prices, monthPeak, timeZone]);
  const tomorrowEnergy = useMemo(() => forecastEnergy(tomorrow, prices, timeZone), [tomorrow, prices, timeZone]);
  const bars = useMemo((): DailyPeakBar[] => {
    const metered = (summary?.dailyPeaks ?? []).map((day): DailyPeakBar => ({
      day: day.day.slice(5).replace('-', '/'),
      peak: day.peak?.demandKW ?? null,
      kind: day.peak && monthPeak && day.peak.at === monthPeak.at ? 'monthMax' : day.day === siteDayKey(new Date(now), timeZone) ? 'today' : 'day',
    }));
    const sameMonth = siteDayKey(new Date(days.tomorrow), timeZone).slice(0, 7) === siteDayKey(new Date(now), timeZone).slice(0, 7);
    return metered.length && tomorrowPeak && sameMonth ? [...metered, { day: '明天', peak: tomorrowPeak.loadKW, kind: 'forecast' }] : metered;
  }, [summary, monthPeak, tomorrowPeak, days, now, timeZone]);

  const view: LoadView = search.view === 'charge' && !headroom ? 'hours' : search.view ?? 'hours';
  const clock = (at: string) => formatTime(at, timeZone).slice(-5);
  const forecastState = forecast.isPending ? '正在读取预测'
    : forecast.isError ? '预测暂时读不到'
      : forecast.data === null || ahead.length === 0 ? '还没有负荷预测' : null;

  return (
    <Main>
      <PageHeader
        title="用电负荷"
        description={`${site.displayName} · 空调总电表现在的负荷、最高负荷和接下来的预测`}
        meta={forecast.data ? (
          <PageMeta dot={forecast.data.quality === 'VALID' ? 'live' : 'warning'}>
            {forecast.data.quality === 'VALID' ? '预测按近 7 天同时段' : '预测历史不足，暂按最近读数延续'}
          </PageMeta>
        ) : undefined}
        actions={(
          <>
            {forecast.data ? (
              <span className="flex items-center gap-1 text-xs text-muted-foreground">
                预测更新于 <span className="font-mono">{clock(forecast.data.origin)}</span>
                <InfoTip>每小时第 7 分钟自动更新；负荷读数每分钟刷新</InfoTip>
              </span>
            ) : null}
            <Button
              variant="outline"
              size="sm"
              onClick={() => {
                void queryClient.invalidateQueries({ queryKey: ['forecast', site.id] });
                void queryClient.invalidateQueries({ queryKey: ['demand', site.id] });
              }}
            >
              <RefreshCw data-icon="inline-start" />
              刷新
            </Button>
          </>
        )}
      />

      {registry.isPending || tariff.isPending || (point !== null && demand.isPending) ? (
        <Skeleton className="h-[560px] rounded-xl" />
      ) : point === null ? (
        <Panel aria-label="负荷概况">
          <Message icon={<Gauge className="size-[18px]" aria-hidden="true" />} title="没有登记空调总电表">
            负荷按空调总电表的有功功率计算；登记该电表并接入数据后在此显示
          </Message>
        </Panel>
      ) : demand.isError || !summary ? (
        <Panel aria-label="负荷概况">
          <div className="flex flex-col items-start gap-2 px-5 py-8">
            <p className="text-sm font-semibold">负荷数据暂时读不到</p>
            <Button variant="outline" size="sm" onClick={() => void demand.refetch()}>重试</Button>
          </div>
        </Panel>
      ) : (
        <>
          <Panel aria-label="负荷概况">
            <div className="grid grid-cols-4 divide-x divide-divider border-b border-divider">
              <Figure label="当前负荷" live={Boolean(summary.current && !summary.current.complete)} value={summary.current ? formatDecimal(summary.current.demandKW, 1) : null} unit="kW">
                {summary.current && !summary.current.complete ? <QuarterProgress start={summary.current.start} now={now} /> : null}
                <span className="mt-1 block">
                  {summary.current ? `${clock(summary.current.start)} 起的 15 分钟${summary.current.complete ? '' : '，进行中'}` : '还没有功率数据'}
                </span>
              </Figure>
              <Figure label="今日最高" info="负荷率 = 今日平均负荷 ÷ 今日最高，越高说明用电越平稳" value={summary.today ? formatDecimal(summary.today.demandKW, 1) : null} unit="kW">
                {summary.today
                  ? `出现在 ${clock(summary.today.at)}${summary.todayLoadFactor !== null ? ` · 负荷率 ${formatDecimal(summary.todayLoadFactor * 100, 0)}%` : ''}`
                  : '今天还没有完整的 15 分钟'}
              </Figure>
              <Figure
                label="本月最高"
                info={headroom ? '有的电价按当月最高的 15 分钟平均功率（需量）收费，本站的计费见「需量电费」' : '电价里说的需量，就是 15 分钟平均负荷；本站现行电价不按需量收费'}
                value={monthPeak ? formatDecimal(monthPeak.demandKW, 1) : null}
                unit="kW"
              >
                {monthPeak ? `出现在 ${formatTime(monthPeak.at, timeZone)}` : '本月还没有完整的 15 分钟'}
              </Figure>
              <Figure label="明天最高（预测）" value={tomorrowPeak ? formatDecimal(tomorrowPeak.loadKW, 1) : null} unit="kW">
                {tomorrowPeak ? (
                  <>
                    预计 {clock(tomorrowPeak.at)}
                    {monthPeak ? (
                      <span className={cn(tomorrowPeak.loadKW > monthPeak.demandKW && 'font-medium text-warning-text')}>
                        {' · '}{tomorrowPeak.loadKW > monthPeak.demandKW ? '高于' : '低于'}本月最高 {formatDecimal(Math.abs(monthPeak.demandKW - tomorrowPeak.loadKW), 1)} kW
                      </span>
                    ) : null}
                  </>
                ) : forecastState ?? '预测还没覆盖明天'}
              </Figure>
            </div>
            <LoadCurve rows={rows} days={days} now={now} monthPeakKW={monthPeak?.demandKW ?? null} spans={spans} timeZone={timeZone} forecastNote={forecastState} />
          </Panel>

          <Panel aria-label="负荷明细">
            <Tabs value={view} onValueChange={(value) => void navigate({ search: (previous) => ({ ...previous, view: value as LoadView }) })} className="gap-0">
              <header className="flex h-12 shrink-0 items-center gap-4 border-b border-divider px-5">
                <TabsList variant="line" className="h-12 gap-5 p-0">
                  <TabsTrigger value="hours" className="h-12 flex-none px-0 after:bottom-0!">逐时预测</TabsTrigger>
                  <TabsTrigger value="days" className="h-12 flex-none px-0 after:bottom-0!">本月每日最高</TabsTrigger>
                  {headroom ? <TabsTrigger value="charge" className="h-12 flex-none px-0 after:bottom-0!">需量电费</TabsTrigger> : null}
                </TabsList>
                <span className="ml-auto min-w-0 truncate text-xs text-muted-foreground">
                  {view === 'hours' && tomorrow.length >= QUARTERS_PER_DAY
                    ? `明天预计用电 ${formatDecimal(tomorrowEnergy.energyKWh, 0)} kWh${tomorrowEnergy.expensiveShare !== null ? `，其中电价高峰时段占 ${formatDecimal(tomorrowEnergy.expensiveShare * 100, 0)}%` : ''}`
                    : view === 'hours' && tomorrow.length ? `预测覆盖到明天 ${clock(tomorrow.at(-1)!.at)}`
                    : view === 'days' && monthPeak ? `本月最高 ${formatDecimal(monthPeak.demandKW, 1)} kW · 出现在 ${formatTime(monthPeak.at, timeZone)}`
                      : view === 'charge' ? '按电价估算' : null}
                </span>
              </header>
              <TabsContent value="hours">
                {forecastState ? (
                  <Message title={forecastState}>预测需要至少一小时的 15 分钟负荷数据，系统每小时第 7 分钟自动更新</Message>
                ) : (
                  <div className="grid grid-cols-2 divide-x divide-divider">
                    <ForecastHours title={`今天剩余 ${restOfToday.length} 小时`} hours={restOfToday} headroom={headroom} />
                    <ForecastHours title="明天" hours={tomorrowHours} headroom={headroom} />
                  </div>
                )}
              </TabsContent>
              <TabsContent value="days">
                <DailyPeaks bars={bars} monthPeakKW={monthPeak?.demandKW ?? null} />
              </TabsContent>
              {headroom ? (
                <TabsContent value="charge">
                  <DemandCharge charges={charges} ranges={ranges} timeZone={timeZone} />
                </TabsContent>
              ) : null}
            </Tabs>
          </Panel>
        </>
      )}
    </Main>
  );
}
