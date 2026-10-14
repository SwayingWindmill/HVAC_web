import { useQuery } from '@tanstack/react-query';
import { getRouteApi } from '@tanstack/react-router';
import { ChartLine, Gauge, Table2 } from 'lucide-react';
import { useMemo, useState, type ReactNode } from 'react';
import { Area, Bar, BarChart, CartesianGrid, Cell, ComposedChart, Line, ReferenceArea, ReferenceLine, XAxis, YAxis } from 'recharts';
import { PageHeader } from '@/blocks/page-header';
import { Panel, PanelHeader } from '@/blocks/panel';
import { InfoTip } from '@/components/info-tip';
import { Main } from '@/components/layout/Main';
import { Button } from '@/components/ui/button';
import { ChartContainer, ChartTooltip, type ChartConfig } from '@/components/ui/chart';
import { Skeleton } from '@/components/ui/skeleton';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group';
import { periodWindow } from '@/features/energy-analysis/consumption/model';
import { useRealtimePlant } from '@/features/operations/realtime/use-realtime-plant';
import { PERIOD_COLOR } from '@/features/tariff/period-presentation';
import { siteTariffQuery } from '@/features/tariff/query';
import { daySchedule, demandCharges, PERIOD_LABEL, periodRanges, type DemandCharge } from '@/features/tariff/tariff-model';
import { useNow } from '@/hooks/use-now';
import { formatDecimal, formatTime } from '@/lib/operator-format';
import { cn } from '@/lib/utils';
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

const chargedPeriods = (charges: readonly DemandCharge[]) => charges.map((charge) => `${PERIOD_LABEL[charge.code]}时段`).join('、');

/** One figure of the overview strip: label, value with unit, and a line of context. */
function Figure({ label, value, unit, children, info }: {
  readonly label: string;
  readonly value: string | null;
  readonly unit?: string;
  readonly children?: ReactNode;
  readonly info?: string;
}) {
  return (
    <div className="flex min-w-0 flex-col gap-1 px-5 py-4">
      <span className="flex items-center gap-1 text-xs font-medium text-muted-foreground">{label}{info ? <InfoTip>{info}</InfoTip> : null}</span>
      <span className="text-2xl leading-[29px] font-semibold tracking-[-0.03em] tabular-nums">
        {value ?? '—'}
        {value !== null && unit ? <span className="ml-1 text-xs font-medium tracking-normal text-subtle-foreground">{unit}</span> : null}
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

export function DemandWorkspace() {
  const { site, principal } = siteRoute.useRouteContext();
  const now = useNow(60_000);
  const [view, setView] = useState<'chart' | 'table'>('chart');
  const { registry } = useRealtimePlant();
  const point = useMemo(() => (registry.data ? plantPowerPoint(registry.data.assetModel) : null), [registry.data]);
  const demand = useQuery(plantDemandQuery({ siteId: site.id, timezone: site.timezone, csrfToken: principal.session.csrfToken, point, now: new Date(now) }));
  const hours = demand.data;
  const summary = useMemo(() => (hours ? demandSummary(hours, new Date(now), site.timezone) : null), [hours, now, site.timezone]);
  const rows = useMemo(() => (hours ? demandChartRows(hours, new Date(now), site.timezone) : []), [hours, now, site.timezone]);
  const todayKey = useMemo(() => periodWindow('today', new Date(now), site.timezone).from.getTime(), [now, site.timezone]);
  const daily = useMemo(
    () => (summary?.dailyPeaks ?? []).map((day, index, all) => ({
      day: day.day.slice(5).replace('-', '/'),
      peak: day.peak?.demandKW ?? null,
      isMax: Boolean(day.peak && summary?.month && day.peak.at === summary.month.at),
      isToday: index === all.length - 1 && Boolean(day.peak) && Date.parse(day.peak!.at) >= todayKey,
    })),
    [summary, todayKey],
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
  const ranges = useMemo(() => (tariff.data ? periodRanges(tariff.data, new Date(now), site.timezone) : new Map()), [tariff.data, now, site.timezone]);
  const at = (peak: DemandPeak | null, withDate = false) => (peak ? formatTime(peak.at, site.timezone).slice(withDate ? 0 : -5) : '—');
  const peakValue = (peak: DemandPeak | null) => (peak ? formatDecimal(peak.demandKW, 1) : null);

  return (
    <Main>
      <PageHeader
        title="需量分析"
        description={site.displayName}
      />

      {registry.isPending || tariff.isPending || (point !== null && demand.isPending) ? (
        <Skeleton className="h-[520px] rounded-xl" />
      ) : point === null ? (
        <Panel aria-label="需量概况">
          <div className="flex flex-col items-center gap-2 px-6 py-14 text-center">
            <span className="flex size-9 items-center justify-center rounded-xl bg-muted text-muted-foreground"><Gauge className="size-[18px]" aria-hidden="true" /></span>
            <p className="text-sm font-semibold">没有登记空调总电表</p>
            <p className="text-xs text-muted-foreground">需量按空调总电表的有功功率计算；登记该电表并接入数据后在此显示</p>
          </div>
        </Panel>
      ) : demand.isError || !summary ? (
        <Panel aria-label="需量概况">
          <div className="flex flex-col items-start gap-2 px-5 py-8">
            <p className="text-sm font-semibold">需量数据暂时读不到</p>
            <Button variant="outline" size="sm" onClick={() => void demand.refetch()}>重试</Button>
          </div>
        </Panel>
      ) : (
        <>
          <Panel aria-label="需量概况">
            <div className="grid grid-cols-4 divide-x divide-divider border-b border-divider">
              <div className="flex min-w-0 flex-col gap-1 px-5 py-4">
                <span className="flex items-center gap-1.5 text-xs font-medium text-muted-foreground">
                  {summary.current && !summary.current.complete ? <span className="size-1.5 rounded-full bg-success" aria-hidden="true" /> : null}
                  当前需量
                </span>
                <span className="text-2xl leading-[29px] font-semibold tracking-[-0.03em] tabular-nums">
                  {summary.current ? formatDecimal(summary.current.demandKW, 1) : '—'}
                  {summary.current ? <span className="ml-1 text-xs font-medium tracking-normal text-subtle-foreground">kW</span> : null}
                </span>
                {summary.current && !summary.current.complete ? <QuarterProgress start={summary.current.start} now={now} /> : null}
                <span className="text-xs text-muted-foreground">
                  {summary.current
                    ? `${formatTime(summary.current.start, site.timezone).slice(-5)} 起的 15 分钟${summary.current.complete ? '' : '，进行中'}`
                    : '还没有功率数据'}
                </span>
              </div>
              <Figure label="今日最大需量" value={peakValue(summary.today)} unit="kW">
                {summary.today
                  ? `出现在 ${at(summary.today)}${summary.month && summary.month.at !== summary.today.at ? ` · 距本月最大 ${formatDecimal(summary.month.demandKW - summary.today.demandKW, 1)} kW` : summary.month ? ' · 即本月最大' : ''}`
                  : '今日还没有完整的 15 分钟'}
              </Figure>
              <Figure label="昨日最大需量" value={peakValue(summary.yesterday)} unit="kW">
                {summary.yesterday ? `出现在 ${at(summary.yesterday)}` : '昨日没有需量数据'}
              </Figure>
              <Figure label="今日负荷率" info="今日平均需量 ÷ 今日最大需量" value={summary.todayLoadFactor !== null ? formatDecimal(summary.todayLoadFactor * 100, 0) : null} unit="%">
                越高说明用电越平稳
              </Figure>
            </div>

            <section role="region" aria-label="需量曲线" className="flex flex-col gap-2 px-5 pt-3.5 pb-3">
              <div className="flex min-h-7 items-center gap-2.5 text-sm">
                <span className="font-semibold">需量曲线</span>
                <span className="text-xs text-muted-foreground">15 分钟平均有功功率 · kW</span>
                <span className="ml-auto flex items-center gap-3 text-xs text-muted-foreground">
                  <span className="flex items-center gap-1.5"><span className="h-0.5 w-3 rounded-full bg-chart-1" aria-hidden="true" />今日</span>
                  <span className="flex items-center gap-1.5"><span className="h-0.5 w-3 rounded-full border-t border-dashed border-chart-5" aria-hidden="true" />昨日</span>
                  {chargedToday.length > 0 ? <span className="flex items-center gap-1.5"><span className="size-2.5 rounded-[2px] bg-warning/20" aria-hidden="true" />需量计费时段</span> : null}
                  {summary.month ? <span className="flex items-center gap-1.5"><span className="h-0.5 w-3 border-t border-dashed border-destructive" aria-hidden="true" />本月最大 {formatDecimal(summary.month.demandKW, 0)} kW</span> : null}
                  <ToggleGroup type="single" variant="outline" spacing={0} value={view} onValueChange={(value) => { if (value) setView(value as 'chart' | 'table'); }} aria-label="显示方式" className="h-7">
                    <ToggleGroupItem value="chart" aria-label="图表" className="h-7 px-2"><ChartLine className="size-3.5" /></ToggleGroupItem>
                    <ToggleGroupItem value="table" aria-label="表格" className="h-7 px-2"><Table2 className="size-3.5" /></ToggleGroupItem>
                  </ToggleGroup>
                </span>
              </div>
              {view === 'chart' ? (
                <ChartContainer config={curveConfig} className="aspect-auto h-[300px] w-full">
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
                    {summary.month ? <ReferenceLine y={summary.month.demandKW} stroke="var(--destructive)" strokeDasharray="6 4" /> : null}
                  </ComposedChart>
                </ChartContainer>
              ) : (
                <div className="max-h-[300px] overflow-y-auto">
                  <Table aria-label="每 15 分钟需量">
                    <TableHeader className="sticky top-0 bg-card">
                      <TableRow className="hover:bg-transparent">
                        <TableHead>时段</TableHead>
                        <TableHead className="text-right">今日（kW）</TableHead>
                        <TableHead className="text-right">昨日（kW）</TableHead>
                        <TableHead className="text-right">变化</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {rows.filter((row) => row.today !== null || row.yesterday !== null).reverse().map((row) => (
                        <TableRow key={row.minute}>
                          <TableCell className="font-mono text-xs">{clock(row.minute)}–{clock(row.minute + 15)}</TableCell>
                          <TableCell className="text-right font-medium tabular-nums">{row.today === null ? '—' : formatDecimal(row.today, 1)}</TableCell>
                          <TableCell className="text-right text-muted-foreground tabular-nums">{row.yesterday === null ? '—' : formatDecimal(row.yesterday, 1)}</TableCell>
                          <TableCell className="text-right text-xs tabular-nums">
                            {row.today === null || row.yesterday === null ? '—' : `${row.today >= row.yesterday ? '+' : '−'}${formatDecimal(Math.abs(row.today - row.yesterday), 1)}`}
                          </TableCell>
                        </TableRow>
                      ))}
                    </TableBody>
                  </Table>
                </div>
              )}
            </section>
          </Panel>

          <div className="grid items-start gap-[18px] grid-cols-[minmax(0,2fr)_minmax(0,1fr)]">
            <Panel aria-label="本月每日最大需量">
              <PanelHeader
                title="本月每日最大需量"
                caption={summary.month ? `本月最大 ${formatDecimal(summary.month.demandKW, 1)} kW · 出现在 ${at(summary.month, true)}` : 'kW'}
                action={(
                  <span className="flex items-center gap-3 text-xs text-muted-foreground">
                    <span className="flex items-center gap-1.5"><span className="size-2.5 rounded-[2px] bg-destructive" aria-hidden="true" />本月最大</span>
                    <span className="flex items-center gap-1.5"><span className="size-2.5 rounded-[2px] bg-chart-1" aria-hidden="true" />其他日</span>
                    <span className="flex items-center gap-1.5"><span className="size-2.5 rounded-[2px] bg-chart-1/40" aria-hidden="true" />今日进行中</span>
                  </span>
                )}
              />
              <div className="px-3 pt-3 pb-2">
                {daily.length === 0 ? (
                  <p className="py-16 text-center text-sm text-muted-foreground">本月还没有需量数据</p>
                ) : (
                  <ChartContainer config={dailyConfig} className="aspect-auto h-[240px] w-full">
                    <BarChart data={daily} margin={{ left: 4, right: 8, top: 12 }}>
                      <CartesianGrid vertical={false} />
                      <XAxis dataKey="day" tickLine={false} axisLine={false} interval="preserveStartEnd" minTickGap={16} />
                      <YAxis tickLine={false} axisLine={false} width={44} domain={[0, 'auto']} />
                      <ChartTooltip cursor={false} content={<DailyTooltip />} />
                      <Bar dataKey="peak" radius={[3, 3, 0, 0]} maxBarSize={28} isAnimationActive={false}>
                        {daily.map((day) => <Cell key={day.day} fill={day.isMax ? 'var(--destructive)' : 'var(--color-peak)'} fillOpacity={day.isToday && !day.isMax ? 0.4 : 1} />)}
                      </Bar>
                    </BarChart>
                  </ChartContainer>
                )}
              </div>
            </Panel>

            <ChargePanel charges={charges} unavailable={tariff.isError} ranges={ranges} timeZone={site.timezone} />
          </div>
        </>
      )}
    </Main>
  );
}

function Fact({ label, children }: { readonly label: string; readonly children: ReactNode }) {
  return (
    <>
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="font-medium">{children}</dd>
    </>
  );
}

/** This month's demand charge at the Site's Tariff: the billed period's highest quarter-hour times its rate. */
function ChargePanel({ charges, unavailable, ranges, timeZone }: {
  readonly charges: readonly DemandCharge[] | null;
  readonly unavailable: boolean;
  readonly ranges: ReadonlyMap<string, string>;
  readonly timeZone: string;
}) {
  const billed = charges?.filter((charge) => charge.charge !== null) ?? [];
  const total = billed.reduce((sum, charge) => sum + charge.charge!, 0);
  return (
    <Panel aria-label="本月需量电费">
      <PanelHeader title={<span className="flex items-center gap-1.5">本月需量电费<InfoTip>按电价估算，正式电费以结算为准</InfoTip></span>} caption="估算" />
      {unavailable ? (
        <p className="px-5 py-8 text-center text-sm text-muted-foreground">电价暂时读不到</p>
      ) : charges === null ? (
        <p className="px-5 py-8 text-center text-sm text-muted-foreground">站点还没有配置电价，发布电价后按需量电价估算</p>
      ) : charges.length === 0 ? (
        <div className="flex flex-col gap-1 px-5 py-4">
          <span className="text-2xl font-semibold tabular-nums">¥0</span>
          <span className="text-xs text-muted-foreground">现行电价不收需量电费</span>
        </div>
      ) : (
        <>
          <div className="flex flex-col gap-1 border-b border-divider px-5 py-4">
            <span className="text-2xl leading-[29px] font-semibold tracking-[-0.03em] tabular-nums">{billed.length ? `¥${formatDecimal(total, 0)}` : '暂无数据'}</span>
            <span className="text-xs text-muted-foreground">
              {billed.length
                ? billed.map((charge) => `${PERIOD_LABEL[charge.code]}时段最大需量 ${formatDecimal(charge.peak!.demandKW, 1)} kW × ${formatDecimal(charge.ratePerKW, 0)} 元/kW`).join('，')
                : `${chargedPeriods(charges)}本月还没有完整的 15 分钟`}
            </span>
          </div>
          <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-2.5 px-5 py-4 text-sm">
            <Fact label="计费方式">按{chargedPeriods(charges)}当月最大需量计收</Fact>
            <Fact label="计费时段">{charges.map((charge) => ranges.get(charge.code) ?? '今日不适用').join('；')}</Fact>
            <Fact label="出现在">
              {billed.length ? <span className="font-mono">{billed.map((charge) => formatTime(charge.peak!.at, timeZone)).join('；')}</span> : '—'}
            </Fact>
          </dl>
        </>
      )}
    </Panel>
  );
}
