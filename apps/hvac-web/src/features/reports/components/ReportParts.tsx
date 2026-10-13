import type { ReactNode } from 'react';
import { Bar, CartesianGrid, ComposedChart, Line, XAxis, YAxis } from 'recharts';
import { FactList, FactRow } from '@/blocks/fact-list';
import { ChartContainer, ChartLegend, ChartLegendContent, ChartTooltip, ChartTooltipContent, type ChartConfig } from '@/components/ui/chart';
import { DeltaBadge } from '@/components/ui/delta-badge';
import { Skeleton } from '@/components/ui/skeleton';
import { SEVERITY_LABELS, SEVERITY_ORDER } from '@/features/alarms/alarm-presentation';
import { ruleFor } from '@/features/diagnostics/diagnostics-model';
import { costBasis, savingsRate } from '@/features/savings/savings-model';
import { PERIOD_COLOR } from '@/features/tariff/period-presentation';
import { PERIOD_LABEL, type DemandCharge } from '@/features/tariff/tariff-model';
import { formatDecimal } from '@/lib/operator-format';
import type { AlarmSummary } from '../report-model';

// The pieces a monthly or yearly report is laid out from, whichever source its figures come from.

export const monthLabel = (month: string) => `${month.slice(0, 4)} 年 ${Number(month.slice(5, 7))} 月`;
export const energyText = (value: number | null) => (value === null ? '—' : value >= 10_000 ? `${formatDecimal(value / 1000, 2)} MWh` : `${formatDecimal(value, 0)} kWh`);
/** What an archived report says of a figure only its owner keeps, worked out now. */
export const NOT_ARCHIVED = '未存档，按当前记录现算';

const trendConfig = {
  electricityKWh: { label: '空调用电（kWh）', color: 'var(--chart-1)' },
  cop: { label: '综合能效（COP）', color: 'var(--chart-3)' },
} satisfies ChartConfig;

export function Section({ index, title, description, children }: { readonly index: number; readonly title: string; readonly description?: string; readonly children: ReactNode }) {
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

export function Figure({ label, value, unit, delta, invert, note }: {
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

/** The report sheet: its title block, sections and footnote, printable to A4. */
export function ReportSheet({ label, kicker, title, period, status, footnote, children }: {
  readonly label: string;
  readonly kicker: string;
  readonly title: string;
  readonly period: ReactNode;
  readonly status: ReactNode;
  readonly footnote: ReactNode;
  readonly children: ReactNode;
}) {
  return (
    <article aria-label={label} className="mx-auto w-full max-w-[1040px] overflow-hidden rounded-xl bg-card shadow-xs ring-1 ring-foreground/10 print:w-[1040px] print:max-w-none print:rounded-none print:shadow-none print:ring-0 print:[zoom:0.62]">
      <header className="flex items-end justify-between gap-6 bg-muted/40 px-10 pt-10 pb-8">
        <div className="flex flex-col gap-2">
          <span className="text-sm text-muted-foreground">{kicker}</span>
          <h2 className="text-3xl font-semibold tracking-tight">{title}</h2>
          <p className="text-sm text-muted-foreground tabular-nums">{period}</p>
          <p className="text-xs text-muted-foreground">{status}</p>
        </div>
        <img src="/quanlaihe-mark.svg" alt="" className="size-12 opacity-90" />
      </header>
      {children}
      <footer className="border-t bg-muted/40 px-10 py-5 text-xs leading-relaxed text-muted-foreground">{footnote}</footer>
    </article>
  );
}

export function EnergyTrend({ rows }: { readonly rows: readonly { label: string; electricityKWh: number | null; cop: number | null }[] }) {
  return (
    <ChartContainer config={trendConfig} className="aspect-auto h-[260px] w-full">
      <ComposedChart data={[...rows]} margin={{ left: 4, right: 4, top: 8 }}>
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
  );
}

export interface PeriodLine {
  readonly code: string;
  readonly energyKWh: number;
  readonly cost: number;
  readonly rate: number;
}

/** Energy cost by time-of-use period, then the demand charges, then the total. */
export function CostTable({ periods, charges, chargesNote, energyKWh }: {
  readonly periods: readonly PeriodLine[];
  readonly charges: readonly DemandCharge[] | null;
  readonly chargesNote?: string;
  readonly energyKWh: number;
}) {
  const demandCost = charges?.reduce((sum, charge) => sum + (charge.charge ?? 0), 0) ?? 0;
  const total = periods.reduce((sum, period) => sum + period.cost, 0) + demandCost;
  const share = (value: number) => (total ? `${formatDecimal((value / total) * 100, 1)}%` : '—');
  return (
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
        {periods.map((period) => (
          <tr key={period.code} className="border-b">
            <td className="py-2.5"><span className="flex items-center gap-2"><span className="size-2 rounded-[2px]" style={{ background: PERIOD_COLOR[period.code as keyof typeof PERIOD_COLOR] }} aria-hidden="true" />{PERIOD_LABEL[period.code as keyof typeof PERIOD_LABEL] ?? period.code}时段电度电费</span></td>
            <td className="py-2.5 text-right">{formatDecimal(period.energyKWh, 0)} kWh</td>
            <td className="py-2.5 text-right">{formatDecimal(period.rate, 2)} 元/kWh</td>
            <td className="py-2.5 text-right">{formatDecimal(period.cost, 0)} 元</td>
            <td className="py-2.5 text-right text-muted-foreground">{share(period.cost)}</td>
          </tr>
        ))}
        {(charges ?? []).map((charge) => (
          <tr key={charge.code} className="border-b">
            <td className="py-2.5">
              <span className="flex items-center gap-2"><span className="size-2 rounded-[2px] bg-foreground/60" aria-hidden="true" />需量电费（{PERIOD_LABEL[charge.code]}段）</span>
              {chargesNote ? <span className="mt-0.5 block pl-4 text-xs text-muted-foreground">{chargesNote}</span> : null}
            </td>
            <td className="py-2.5 text-right">{charge.peak ? `${formatDecimal(charge.peak.demandKW, 1)} kW` : '—'}</td>
            <td className="py-2.5 text-right">{formatDecimal(charge.ratePerKW, 0)} 元/kW</td>
            <td className="py-2.5 text-right">{charge.charge === null ? '—' : `${formatDecimal(charge.charge, 0)} 元`}</td>
            <td className="py-2.5 text-right text-muted-foreground">{charge.charge ? share(charge.charge) : '—'}</td>
          </tr>
        ))}
        <tr className="font-medium">
          <td className="pt-3">合计</td>
          <td className="pt-3 text-right">{energyText(energyKWh)}</td>
          <td className="pt-3 text-right text-muted-foreground">均价 {energyKWh > 0 ? formatDecimal(total / energyKWh, 3) : '—'} 元/kWh</td>
          <td className="pt-3 text-right">{`${formatDecimal(total, 0)} 元`}</td>
          <td className="pt-3 text-right">100%</td>
        </tr>
      </tbody>
    </table>
  );
}

export interface SavingsFigures {
  readonly days: number;
  readonly unpricedHours: number;
  readonly adjustedBaselineKWh: number;
  readonly avoidedKWh: number;
  readonly avoidedCost: number;
}

export function SavingsGrid({ totals, daysNote }: { readonly totals: SavingsFigures; readonly daysNote: string }) {
  const rate = savingsRate({ ...totals, actualKWh: 0 });
  return (
    <div className="grid grid-cols-4 gap-8">
      <Figure label="节电量" value={totals.days > 0 ? formatDecimal(totals.avoidedKWh, 0) : null} unit="kWh" note={totals.days > 0 ? `调整后基线 ${energyText(totals.adjustedBaselineKWh)}` : '本月没有核算结果'} />
      <Figure label="节省电费" value={totals.days > 0 ? formatDecimal(totals.avoidedCost, 0) : null} unit="元" note={costBasis(totals)} />
      <Figure label="节电率" value={rate === null ? null : formatDecimal(rate * 100, 1)} unit="%" note="节电量 ÷ 调整后基线" />
      <Figure label="核算天数" value={String(totals.days)} unit="天" note={daysNote} />
    </div>
  );
}

/** Alarms and work orders as their owners hold them now; findings from the report's own source. */
export function OperationsGrid({ alarms, orders, findings, findingsUnavailable, liveNote }: {
  readonly alarms: AlarmSummary | null;
  readonly orders: { opened: number; completed: number; stillOpen: number } | null;
  readonly findings: { count: number; byType: readonly { findingType: string; count: number }[] } | null;
  readonly findingsUnavailable?: boolean;
  /** Set on an archived report: alarms and work orders were not archived with it. */
  readonly liveNote?: string;
}) {
  return (
    <div className="grid grid-cols-3 gap-8">
      <div className="flex flex-col gap-3">
        <h3 className="text-sm font-medium">告警{liveNote ? <span className="ml-2 text-xs font-normal text-muted-foreground">{liveNote}</span> : null}</h3>
        {alarms ? (
          <FactList>
            <FactRow label="新发生" detail={SEVERITY_ORDER.filter((severity) => alarms.bySeverity[severity] > 0).map((severity) => `${SEVERITY_LABELS[severity]} ${alarms.bySeverity[severity]}`).join(' · ') || undefined}>{alarms.raised} 条</FactRow>
            <FactRow label="已恢复">{alarms.cleared} 条</FactRow>
            <FactRow label="平均恢复用时">{alarms.meanMinutesToClear === null ? '—' : `${formatDecimal(alarms.meanMinutesToClear, 0)} 分钟`}</FactRow>
          </FactList>
        ) : <Skeleton className="h-24" />}
      </div>
      <div className="flex flex-col gap-3">
        <h3 className="text-sm font-medium">工单{liveNote ? <span className="ml-2 text-xs font-normal text-muted-foreground">{liveNote}</span> : null}</h3>
        {orders ? (
          <FactList>
            <FactRow label="新建">{orders.opened} 张</FactRow>
            <FactRow label="完成">{orders.completed} 张</FactRow>
            <FactRow label="新建中仍未结束">{orders.stillOpen} 张</FactRow>
          </FactList>
        ) : <Skeleton className="h-24" />}
      </div>
      <div className="flex flex-col gap-3">
        <h3 className="text-sm font-medium">诊断发现</h3>
        {findings ? (
          <FactList>
            <FactRow label="发现问题">{findings.count} 条</FactRow>
            {findings.byType.slice(0, 2).map((group) => (
              <FactRow key={group.findingType} label={ruleFor(group.findingType)?.title ?? '其他发现'}>{group.count} 条</FactRow>
            ))}
          </FactList>
        ) : findingsUnavailable ? <p className="text-sm text-muted-foreground">诊断发现暂不可用</p> : <Skeleton className="h-24" />}
      </div>
    </div>
  );
}
