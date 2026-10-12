import { useQuery } from '@tanstack/react-query';
import { getRouteApi, useNavigate, useSearch } from '@tanstack/react-router';
import { Printer } from 'lucide-react';
import { useMemo, type ReactNode } from 'react';
import { Bar, CartesianGrid, ComposedChart, Line, XAxis, YAxis } from 'recharts';
import { FactList, FactRow } from '@/blocks/fact-list';
import { PageHeader, PageMeta } from '@/blocks/page-header';
import { Main } from '@/components/layout/Main';
import { Button } from '@/components/ui/button';
import { ChartContainer, ChartLegend, ChartLegendContent, ChartTooltip, ChartTooltipContent, type ChartConfig } from '@/components/ui/chart';
import { DeltaBadge } from '@/components/ui/delta-badge';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Skeleton } from '@/components/ui/skeleton';
import { SEVERITY_LABELS, SEVERITY_ORDER } from '@/features/alarms/alarm-presentation';
import { ruleFor } from '@/features/diagnostics/diagnostics-model';
import { relativeChange } from '@/features/energy-analysis/consumption/model';
import { measuresQuery, summaryQuery } from '@/features/savings/query';
import { costBasis, monthToDate, savingsRate, STATUS_LABEL } from '@/features/savings/savings-model';
import { PERIOD_COLOR } from '@/features/tariff/period-presentation';
import { demandCharges, PERIOD_LABEL, touEnergyCost } from '@/features/tariff/tariff-model';
import { formatDecimal } from '@/lib/operator-format';
import { useMonthlyReport } from '../query';
import { alarmSummary, findingSummary, recentMonths, reportWindow, workOrderSummary, type ReportWindow } from '../report-model';

const siteRoute = getRouteApi('/_app/_site');

const monthLabel = (month: string) => `${month.slice(0, 4)} 年 ${Number(month.slice(5, 7))} 月`;
const energyText = (value: number | null) => (value === null ? '—' : value >= 10_000 ? `${formatDecimal(value / 1000, 2)} MWh` : `${formatDecimal(value, 0)} kWh`);

const trendConfig = {
  electricityKWh: { label: '空调用电（kWh）', color: 'var(--chart-1)' },
  cop: { label: '综合能效（COP）', color: 'var(--chart-3)' },
} satisfies ChartConfig;

function Section({ index, title, description, children }: { readonly index: number; readonly title: string; readonly description?: string; readonly children: ReactNode }) {
  return (
    <section className="flex flex-col gap-5 border-t px-10 py-8 break-inside-avoid">
      <header className="flex items-baseline gap-3">
        <span className="font-mono text-xs text-muted-foreground tabular-nums">{String(index).padStart(2, '0')}</span>
        <div>
          <h2 className="text-base font-semibold">{title}</h2>
          {description ? <p className="mt-0.5 text-sm text-muted-foreground">{description}</p> : null}
        </div>
      </header>
      {children}
    </section>
  );
}

function Figure({ label, value, unit, delta, invert, note }: {
  readonly label: string;
  readonly value: string | null;
  readonly unit?: string;
  readonly delta?: number | null;
  readonly invert?: boolean;
  readonly note: ReactNode;
}) {
  return (
    <div className="flex min-w-0 flex-col gap-2">
      <span className="text-sm text-muted-foreground">{label}</span>
      <div className="flex items-center gap-2">
        <span className="text-2xl leading-none font-medium tracking-tight tabular-nums">
          {value ?? '—'}
          {value !== null && unit ? <span className="ml-1 text-sm font-normal text-muted-foreground">{unit}</span> : null}
        </span>
        {delta != null ? <DeltaBadge delta={delta} invert={invert} variant="soft" showIcon={false} /> : null}
      </div>
      <span className="text-xs text-muted-foreground">{note}</span>
    </div>
  );
}

export function ReportWorkspace() {
  const { site } = siteRoute.useRouteContext();
  const search = useSearch({ from: '/_app/_site/reports' });
  const navigate = useNavigate({ from: '/reports' });
  const now = useMemo(() => new Date(), []);
  const months = useMemo(() => recentMonths(now, site.timezone), [now, site.timezone]);
  const month = search.month && months.includes(search.month) ? search.month : months[0];
  const span = useMemo(() => reportWindow(month, now, site.timezone), [month, now, site.timezone]);

  return (
    <Main>
      <PageHeader
        className="print:hidden"
        title="月度报告"
        description={`${site.displayName} · 按月汇总用能、能效、电费与运维`}
        meta={span.complete ? <PageMeta dot="live">已结月</PageMeta> : <PageMeta dot="warning">本月进行中，统计到当前</PageMeta>}
        actions={(
          <div className="flex items-center gap-2 print:hidden">
            <Select value={month} onValueChange={(value) => void navigate({ search: (previous) => ({ ...previous, month: value }), replace: true })}>
              <SelectTrigger className="h-8 w-36" aria-label="报告月份"><SelectValue /></SelectTrigger>
              <SelectContent>{months.map((option) => <SelectItem key={option} value={option}>{monthLabel(option)}</SelectItem>)}</SelectContent>
            </Select>
            <Button size="sm" onClick={() => globalThis.print()}>
              <Printer data-icon="inline-start" />
              导出 PDF
            </Button>
          </div>
        )}
      />
      <MonthlyReport key={month} month={month} span={span} />
    </Main>
  );
}

/** The month's verified savings, and which measures were in verification during it. */
function SavingsSection({ siteId, month, span, timeZone }: { readonly siteId: string; readonly month: string; readonly span: ReportWindow; readonly timeZone: string }) {
  // Verified days are Site-local dates: the whole month once it is over, through today while it runs.
  const to = span.complete ? `${month}-${String(span.days).padStart(2, '0')}` : monthToDate(span.to, timeZone).to;
  const summary = useQuery(summaryQuery(siteId, `${month}-01`, to));
  const measures = useQuery(measuresQuery(siteId));
  if (summary.isPending || measures.isPending) return <Skeleton className="h-20" />;
  if (summary.isError || measures.isError) return <p className="text-sm text-muted-foreground">节能核算暂不可用</p>;
  const totals = summary.data.totals;
  const rate = savingsRate(totals);
  const active = measures.data.filter((measure) => measure.implementedAt !== null
    && Date.parse(measure.implementedAt) < span.to.getTime() && (measure.endedAt === null || Date.parse(measure.endedAt) > span.from.getTime()));
  return (
    <>
      <div className="grid grid-cols-4 gap-8">
        <Figure label="节电量" value={totals.days > 0 ? formatDecimal(totals.avoidedKWh, 0) : null} unit="kWh" note={totals.days > 0 ? `调整后基线 ${energyText(totals.adjustedBaselineKWh)}` : '本月没有核算结果'} />
        <Figure label="节省电费" value={totals.days > 0 ? formatDecimal(totals.avoidedCost, 0) : null} unit="元" note={costBasis(totals)} />
        <Figure label="节电率" value={rate === null ? null : formatDecimal(rate * 100, 1)} unit="%" note="节电量 ÷ 调整后基线" />
        <Figure label="核算天数" value={String(totals.days)} unit="天" note={span.complete ? `本月 ${span.days} 天` : '每天 02:00 核算前一天'} />
      </div>
      <p className="text-sm text-muted-foreground">
        {active.length === 0
          ? '本月没有实施中的节能措施。'
          : active.map((measure) => `「${measure.name}」${measure.baseline?.trusted ? STATUS_LABEL[measure.status] : '基线不可信，未核算'}`).join('；')}
      </p>
    </>
  );
}

function MonthlyReport({ month, span }: { readonly month: string; readonly span: ReportWindow }) {
  const { site, meter, energy, demand, alarms, workOrders, findings, tariff } = useMonthlyReport(month, span);
  const timeZone = site.timezone;

  const cost = useMemo(
    () => (tariff.data && energy.data ? touEnergyCost(energy.data.hourly, tariff.data, timeZone) : null),
    [tariff.data, energy.data, timeZone],
  );
  const charges = useMemo(
    () => (tariff.data && demand.data ? demandCharges(demand.data, tariff.data, timeZone, span.from) : null),
    [tariff.data, demand.data, timeZone, span.from],
  );
  const peakDemand = useMemo(() => {
    let peak: { at: string; demandKW: number } | null = null;
    for (const hour of demand.data ?? []) if (hour.complete && (!peak || hour.demandKW > peak.demandKW)) peak = { at: hour.start, demandKW: hour.demandKW };
    return peak;
  }, [demand.data]);

  if (energy.isPending) return <Skeleton className="mx-auto h-[900px] w-full max-w-[1040px] rounded-xl" />;
  if (energy.isError || !energy.data) {
    return <div className="mx-auto w-full max-w-[1040px] rounded-xl border bg-card py-16 text-center text-sm text-muted-foreground">报告所需的能耗数据暂不可用</div>;
  }

  const { current, previous } = energy.data;
  const demandCost = charges?.reduce((sum, charge) => sum + (charge.charge ?? 0), 0) ?? 0;
  const totalCost = cost ? cost.total + demandCost : null;
  const days = current.rows.filter((row) => row.electricityKWh !== null);
  // Every day of the month on the axis, so days without metering show as gaps.
  const byLabel = new Map(current.rows.map((row) => [row.label.split(' ')[0], row]));
  const monthNumber = Number(month.slice(5, 7));
  const rows = Array.from({ length: span.days }, (_, index) => {
    const label = `${monthNumber}/${index + 1}`;
    const row = byLabel.get(label);
    return { label, electricityKWh: row?.electricityKWh == null ? null : Math.round(row.electricityKWh), cop: row?.cop == null ? null : Number(row.cop.toFixed(2)) };
  });
  const alarmFacts = alarms.data ? alarmSummary(alarms.data.alarms, span.from, span.to) : null;
  const orderFacts = workOrders.data ? workOrderSummary(workOrders.data, span.from, span.to) : null;
  const findingFacts = findings.data ? findingSummary(findings.data, span.from, span.to) : null;
  const range = new Intl.DateTimeFormat('zh-CN', { timeZone, month: 'long', day: 'numeric', hour: '2-digit', minute: '2-digit', hour12: false });

  return (
    <article aria-label={`${monthLabel(month)}能源报告`} className="mx-auto w-full max-w-[1040px] overflow-hidden rounded-xl bg-card shadow-xs ring-1 ring-foreground/10 print:w-[1040px] print:max-w-none print:rounded-none print:shadow-none print:ring-0 print:[zoom:0.62]">
      <header className="flex items-end justify-between gap-6 bg-muted/40 px-10 pt-10 pb-8">
        <div className="flex flex-col gap-2">
          <span className="text-sm text-muted-foreground">{site.displayName} · 冷站能源报告</span>
          <h2 className="text-3xl font-semibold tracking-tight">{monthLabel(month)}</h2>
          <p className="text-sm text-muted-foreground tabular-nums">
            统计期间 {range.format(span.from)} 至 {range.format(span.to)}{span.complete ? '' : '（本月进行中）'}
          </p>
        </div>
        <img src="/quanlaihe-mark.svg" alt="" className="size-12 opacity-90" />
      </header>

      <Section index={1} title="概要" description={span.complete ? '与上月全月对比' : '与上月同期对比'}>
        <div className="grid grid-cols-4 gap-8">
          <Figure label="空调用电" value={current.electricityKWh === null ? null : formatDecimal(current.electricityKWh, 0)} unit="kWh" delta={relativeChange(current.electricityKWh, previous.electricityKWh)} invert note={`上月 ${energyText(previous.electricityKWh)}`} />
          <Figure label="供冷量" value={current.coolingKWh === null ? null : formatDecimal(current.coolingKWh, 0)} unit="kWh" delta={relativeChange(current.coolingKWh, previous.coolingKWh)} note={`上月 ${energyText(previous.coolingKWh)}`} />
          <Figure label="综合能效" value={current.cop === null ? null : formatDecimal(current.cop, 2)} unit="COP" delta={relativeChange(current.cop, previous.cop)} note={previous.cop === null ? '上月没有数据' : `上月 ${formatDecimal(previous.cop, 2)}`} />
          <Figure label="估算电费" value={totalCost === null ? null : formatDecimal(totalCost, 0)} unit="元" note={tariff.data === null ? '站点还没有配置电价' : '按分时电价估算，以结算为准'} />
        </div>
      </Section>

      <Section index={2} title="用能与能效" description={`每日空调用电与综合能效；${days.length} 天有计量数据`}>
        <ChartContainer config={trendConfig} className="aspect-auto h-[260px] w-full">
          <ComposedChart data={rows} margin={{ left: 4, right: 4, top: 8 }}>
            <CartesianGrid vertical={false} />
            <XAxis dataKey="label" tickLine={false} axisLine={false} tickMargin={8} minTickGap={12} />
            <YAxis yAxisId="energy" tickLine={false} axisLine={false} width={48} />
            <YAxis yAxisId="cop" orientation="right" tickLine={false} axisLine={false} width={36} domain={[0, 'auto']} />
            <ChartTooltip content={<ChartTooltipContent indicator="dot" />} />
            <Bar yAxisId="energy" dataKey="electricityKWh" fill="var(--color-electricityKWh)" radius={[3, 3, 0, 0]} maxBarSize={22} isAnimationActive={false} />
            <Line yAxisId="cop" dataKey="cop" stroke="var(--color-cop)" strokeWidth={2} dot={{ r: 2.5, strokeWidth: 0, fill: 'var(--color-cop)' }} connectNulls isAnimationActive={false} />
            <ChartLegend content={<ChartLegendContent />} />
          </ComposedChart>
        </ChartContainer>
        <div className="grid grid-cols-3 gap-8">
          <FactList>
            <FactRow label="日均用电">{days.length ? energyText((current.electricityKWh ?? 0) / days.length) : '—'}</FactRow>
            <FactRow label="用电最多的一天" detail={current.peak ? energyText(current.peak.electricityKWh) : undefined}>{current.peak?.label ?? '—'}</FactRow>
          </FactList>
          <FactList>
            <FactRow label="最大需量" detail={peakDemand ? range.format(new Date(peakDemand.at)) : undefined}>{peakDemand ? `${formatDecimal(peakDemand.demandKW, 1)} kW` : meter ? '—' : '没有总电表'}</FactRow>
            <FactRow label="计量质量">{current.excludedIntervals > 0 ? `${current.excludedIntervals} 个区间未计入` : '全部有效'}</FactRow>
          </FactList>
          <FactList>
            <FactRow label="上月空调用电">{energyText(previous.electricityKWh)}</FactRow>
            <FactRow label="上月综合能效">{previous.cop === null ? '—' : formatDecimal(previous.cop, 2)}</FactRow>
          </FactList>
        </div>
      </Section>

      <Section index={3} title="电费构成" description={tariff.data ? `${tariff.data.displayName} · 按分时电价估算，正式电费以结算为准` : '站点还没有配置电价'}>
        {cost && tariff.data ? (
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b text-xs text-muted-foreground">
                <th className="py-2 text-left font-medium">项目</th>
                <th className="py-2 text-right font-medium">电量 / 需量</th>
                <th className="py-2 text-right font-medium">单价</th>
                <th className="py-2 text-right font-medium">电费</th>
                <th className="py-2 text-right font-medium">占比</th>
              </tr>
            </thead>
            <tbody className="tabular-nums">
              {cost.byPeriod.map((usage) => {
                const rate = tariff.data!.periods.find((period) => period.periodCode === usage.code)?.energyRatePerKWh ?? 0;
                return (
                  <tr key={usage.code} className="border-b">
                    <td className="py-2.5"><span className="flex items-center gap-2"><span className="size-2 rounded-[2px]" style={{ background: PERIOD_COLOR[usage.code] }} aria-hidden="true" />{PERIOD_LABEL[usage.code]}时段电度电费</span></td>
                    <td className="py-2.5 text-right">{formatDecimal(usage.energyKWh, 0)} kWh</td>
                    <td className="py-2.5 text-right">{formatDecimal(rate, 2)} 元/kWh</td>
                    <td className="py-2.5 text-right">{formatDecimal(usage.cost, 0)} 元</td>
                    <td className="py-2.5 text-right text-muted-foreground">{totalCost ? `${formatDecimal((usage.cost / totalCost) * 100, 1)}%` : '—'}</td>
                  </tr>
                );
              })}
              {(charges ?? []).map((charge) => (
                <tr key={charge.code} className="border-b">
                  <td className="py-2.5"><span className="flex items-center gap-2"><span className="size-2 rounded-[2px] bg-foreground/60" aria-hidden="true" />需量电费（{PERIOD_LABEL[charge.code]}段）</span></td>
                  <td className="py-2.5 text-right">{charge.peak ? `${formatDecimal(charge.peak.demandKW, 1)} kW` : '—'}</td>
                  <td className="py-2.5 text-right">{formatDecimal(charge.ratePerKW, 0)} 元/kW</td>
                  <td className="py-2.5 text-right">{charge.charge === null ? '—' : `${formatDecimal(charge.charge, 0)} 元`}</td>
                  <td className="py-2.5 text-right text-muted-foreground">{totalCost && charge.charge ? `${formatDecimal((charge.charge / totalCost) * 100, 1)}%` : '—'}</td>
                </tr>
              ))}
              <tr className="font-medium">
                <td className="pt-3">合计</td>
                <td className="pt-3 text-right">{energyText(cost.energyKWh)}</td>
                <td className="pt-3 text-right text-muted-foreground">均价 {cost.energyKWh > 0 ? formatDecimal((totalCost ?? 0) / cost.energyKWh, 3) : '—'} 元/kWh</td>
                <td className="pt-3 text-right">{totalCost === null ? '—' : `${formatDecimal(totalCost, 0)} 元`}</td>
                <td className="pt-3 text-right">100%</td>
              </tr>
            </tbody>
          </table>
        ) : (
          <p className="text-sm text-muted-foreground">为站点的电网进线发布电价后，报告按峰平谷单价与需量电价估算电费。</p>
        )}
      </Section>

      <Section index={4} title="节能成效" description="对照实施前基线、按实际供冷量调整后核算的节电量与节省电费（IPMVP 选项 C）">
        <SavingsSection siteId={site.id} month={month} span={span} timeZone={timeZone} />
      </Section>

      <Section index={5} title="运行与维护" description="本月发生的告警、工单与诊断发现">
        <div className="grid grid-cols-3 gap-8">
          <div className="flex flex-col gap-3">
            <h3 className="text-sm font-medium">告警</h3>
            {alarmFacts ? (
              <FactList>
                <FactRow label="新发生" detail={SEVERITY_ORDER.filter((severity) => alarmFacts.bySeverity[severity] > 0).map((severity) => `${SEVERITY_LABELS[severity]} ${alarmFacts.bySeverity[severity]}`).join(' · ') || undefined}>{alarmFacts.raised} 条</FactRow>
                <FactRow label="已恢复">{alarmFacts.cleared} 条</FactRow>
                <FactRow label="平均恢复用时">{alarmFacts.meanMinutesToClear === null ? '—' : `${formatDecimal(alarmFacts.meanMinutesToClear, 0)} 分钟`}</FactRow>
              </FactList>
            ) : <Skeleton className="h-24" />}
          </div>
          <div className="flex flex-col gap-3">
            <h3 className="text-sm font-medium">工单</h3>
            {orderFacts ? (
              <FactList>
                <FactRow label="新建">{orderFacts.opened} 张</FactRow>
                <FactRow label="完成">{orderFacts.completed} 张</FactRow>
                <FactRow label="新建中仍未结束">{orderFacts.stillOpen} 张</FactRow>
              </FactList>
            ) : <Skeleton className="h-24" />}
          </div>
          <div className="flex flex-col gap-3">
            <h3 className="text-sm font-medium">诊断发现</h3>
            {findingFacts ? (
              <FactList>
                <FactRow label="发现问题">{findingFacts.count} 条</FactRow>
                {findingFacts.byType.slice(0, 2).map((group) => (
                  <FactRow key={group.findingType} label={ruleFor(group.findingType)?.title ?? '其他发现'}>{group.count} 条</FactRow>
                ))}
              </FactList>
            ) : findings.isError ? <p className="text-sm text-muted-foreground">诊断发现暂不可用</p> : <Skeleton className="h-24" />}
          </div>
        </div>
      </Section>

      <footer className="border-t bg-muted/40 px-10 py-5 text-xs leading-relaxed text-muted-foreground">
        说明：空调用电、供冷量取自空调总电表与冷量表的有效计量区间；电费按站点当前生效的分时电价估算，需量按 15 分钟平均功率计，正式电费以结算为准。
        {alarms.data && !alarms.data.complete ? ' 告警超过读取上限，统计可能不全。' : ''}
      </footer>
    </article>
  );
}
