import type { ReactNode } from 'react';
import { Bar, CartesianGrid, Cell, ComposedChart, Line, XAxis, YAxis } from 'recharts';
import { InfoTip } from '@/components/info-tip';
import { ChartContainer, ChartTooltip, ChartTooltipContent, type ChartConfig } from '@/components/ui/chart';
import { DeltaBadge } from '@/components/ui/delta-badge';
import { Skeleton } from '@/components/ui/skeleton';
import { SEVERITY_LABELS, SEVERITY_ORDER } from '@/features/alarms/alarm-presentation';
import { ruleFor } from '@/features/diagnostics/diagnostics-model';
import { costBasis, siteSavingsRate } from '@/features/savings/savings-model';
import { PERIOD_COLOR } from '@/features/tariff/period-presentation';
import { PERIOD_LABEL, type DemandCharge } from '@/features/tariff/tariff-model';
import { formatDecimal } from '@/lib/operator-format';
import { cn } from '@/lib/utils';
import type { AlarmSummary } from '../report-model';

// The pieces a monthly or yearly report is laid out from, whichever source its figures come from.

export const monthLabel = (month: string) => `${month.slice(0, 4)} 年 ${Number(month.slice(5, 7))} 月`;
export const energyText = (value: number | null) => (value === null ? '—' : value >= 10_000 ? `${formatDecimal(value / 1000, 2)} MWh` : `${formatDecimal(value, 0)} kWh`);
/** What an archived report says of a figure only its owner keeps, worked out now. */
export const NOT_ARCHIVED = '未存档，按当前记录现算';

const electricityConfig = { electricityKWh: { label: '空调用电（kWh）', color: 'var(--chart-1)' } } satisfies ChartConfig;
const copConfig = { cop: { label: '综合能效（COP）', color: 'var(--chart-2)' } } satisfies ChartConfig;

export function Section({ index, title, description, children }: { readonly index: number; readonly title: string; readonly description?: string; readonly children: ReactNode }) {
  return (
    <section className="flex flex-col gap-4 border-t border-divider px-10 py-7 break-inside-avoid">
      <h2 className="flex items-center gap-2.5 text-title font-semibold">
        <span className="flex size-5 items-center justify-center rounded-md bg-primary-soft font-mono text-[11px] font-semibold text-primary" aria-hidden="true">{index}</span>
        {title}
        {description ? <span className="text-xs font-normal text-muted-foreground">{description}</span> : null}
      </h2>
      {children}
    </section>
  );
}

export function Figure({ label, value, unit, delta, invert, note, info }: {
  readonly label: string;
  readonly value: string | null;
  readonly unit?: string;
  readonly delta?: number | null;
  readonly invert?: boolean;
  readonly note: ReactNode;
  readonly info?: string;
}) {
  return (
    <div className="flex min-w-0 flex-col gap-1">
      <span className="flex items-center gap-1 text-xs font-medium text-muted-foreground">{label}{info ? <InfoTip>{info}</InfoTip> : null}</span>
      <span className="flex items-baseline gap-2">
        <span className="text-[22px] leading-7 font-semibold tracking-[-0.02em] tabular-nums">
          {value ?? '—'}
          {value !== null && unit ? <span className="ml-1 text-xs font-medium tracking-normal text-subtle-foreground">{unit}</span> : null}
        </span>
        {delta != null ? <DeltaBadge delta={delta} invert={invert} variant="text" showIcon={false} /> : null}
      </span>
      <span className="text-xs text-muted-foreground">{note}</span>
    </div>
  );
}

/** A grid of figures, four to a row, as the report's sections lay them out. */
export function FigureGrid({ children }: { readonly children: ReactNode }) {
  return <div className="grid grid-cols-4 gap-x-8 gap-y-6">{children}</div>;
}

/** The report sheet: its title block, sections and footnote, printable to A4. */
export function ReportSheet({ label, kicker, title, period, status, archived, footnote, children }: {
  readonly label: string;
  readonly kicker: string;
  readonly title: string;
  readonly period: ReactNode;
  /** When and how the figures were made. */
  readonly status: ReactNode;
  /** Read from a month-end snapshot rather than worked out now. */
  readonly archived: boolean;
  readonly footnote: ReactNode;
  readonly children: ReactNode;
}) {
  return (
    <article aria-label={label} className="mx-auto w-full max-w-[1040px] overflow-hidden rounded-xl border bg-card shadow-xs print:w-[1040px] print:max-w-none print:rounded-none print:border-0 print:shadow-none print:[zoom:0.62]">
      <header className="flex items-start justify-between gap-6 px-10 pt-9 pb-7">
        <div className="flex flex-col gap-1.5">
          <span className="text-xs font-medium text-muted-foreground">{kicker}</span>
          <h2 className="text-[26px] leading-8 font-bold tracking-[-0.03em]">{title}</h2>
          <p className="text-sm text-muted-foreground tabular-nums">{period}</p>
        </div>
        <span className="flex max-w-[360px] flex-col items-end gap-1.5 text-right">
          <span className={cn('inline-flex items-center gap-1.5 rounded-md px-1.5 text-xs leading-5 font-medium', archived ? 'bg-success-soft text-success-text' : 'bg-primary-soft text-primary')}>
            <span className={cn('size-1.5 rounded-full', archived ? 'bg-success' : 'bg-primary')} aria-hidden="true" />
            {archived ? '已存档' : '实时'}
          </span>
          <span className="text-xs text-muted-foreground">{status}</span>
        </span>
      </header>
      {children}
      <footer className="border-t border-divider bg-muted/40 px-10 py-5 text-xs leading-5 text-muted-foreground">{footnote}</footer>
    </article>
  );
}

/** Electricity by day (or month) with the highest one marked, and efficiency below it on its own scale. */
export function EnergyTrend({ rows, unitLabel = '空调用电 · kWh' }: {
  readonly rows: readonly { label: string; electricityKWh: number | null; cop: number | null }[];
  readonly unitLabel?: string;
}) {
  const peak = Math.max(...rows.map((row) => row.electricityKWh ?? 0));
  return (
    <div className="flex flex-col gap-2">
      <span className="text-xs text-muted-foreground">{unitLabel}</span>
      <ChartContainer config={electricityConfig} className="aspect-auto h-[200px] w-full">
        <ComposedChart data={[...rows]} margin={{ left: 4, right: 4, top: 8 }}>
          <CartesianGrid vertical={false} />
          <XAxis dataKey="label" tickLine={false} axisLine={false} tickMargin={8} minTickGap={12} />
          <YAxis tickLine={false} axisLine={false} width={48} />
          <ChartTooltip content={<ChartTooltipContent indicator="dot" />} />
          <Bar dataKey="electricityKWh" radius={[3, 3, 0, 0]} maxBarSize={22} isAnimationActive={false}>
            {rows.map((row) => <Cell key={row.label} fill="var(--color-electricityKWh)" fillOpacity={row.electricityKWh !== null && row.electricityKWh === peak && peak > 0 ? 1 : 0.45} />)}
          </Bar>
        </ComposedChart>
      </ChartContainer>
      <span className="text-xs text-muted-foreground">综合能效 · COP</span>
      <ChartContainer config={copConfig} className="aspect-auto h-[120px] w-full">
        <ComposedChart data={[...rows]} margin={{ left: 4, right: 4, top: 8 }}>
          <CartesianGrid vertical={false} />
          <XAxis dataKey="label" tickLine={false} axisLine={false} tickMargin={8} minTickGap={12} />
          <YAxis tickLine={false} axisLine={false} width={48} domain={['auto', 'auto']} />
          <ChartTooltip content={<ChartTooltipContent indicator="dot" />} />
          <Line dataKey="cop" stroke="var(--color-cop)" strokeWidth={2} dot={{ r: 2.5, strokeWidth: 0, fill: 'var(--color-cop)' }} connectNulls isAnimationActive={false} />
        </ComposedChart>
      </ChartContainer>
    </div>
  );
}

export interface PeriodLine {
  readonly code: string;
  readonly energyKWh: number;
  readonly cost: number;
  readonly rate: number;
}

const headCell = 'h-9 bg-muted px-3 text-xs font-medium whitespace-nowrap text-muted-foreground';
const bodyCell = 'border-b border-divider px-3 py-2.5';

function ShareBar({ value, total, color }: { readonly value: number; readonly total: number; readonly color: string }) {
  const share = total > 0 ? value / total : 0;
  return (
    <span className="flex items-center gap-2">
      <span className="h-1.5 w-20 overflow-hidden rounded-full bg-muted" aria-hidden="true">
        <span className="block h-full rounded-full" style={{ width: `${share * 100}%`, background: color }} />
      </span>
      <span className="text-xs text-muted-foreground tabular-nums">{total > 0 ? `${formatDecimal(share * 100, 1)}%` : '—'}</span>
    </span>
  );
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
  return (
    <table className="w-full text-sm" aria-label="电费构成">
      <thead>
        <tr>
          <th className={cn(headCell, 'rounded-l-md text-left')}>项目</th>
          <th className={cn(headCell, 'text-right')}>电量 / 需量</th>
          <th className={cn(headCell, 'text-right')}>单价</th>
          <th className={cn(headCell, 'text-left')}>占比</th>
          <th className={cn(headCell, 'rounded-r-md text-right')}>电费</th>
        </tr>
      </thead>
      <tbody className="tabular-nums">
        {periods.map((period) => {
          const color = PERIOD_COLOR[period.code as keyof typeof PERIOD_COLOR];
          return (
            <tr key={period.code}>
              <td className={bodyCell}><span className="flex items-center gap-2"><span className="size-2 rounded-[2px]" style={{ background: color }} aria-hidden="true" />{PERIOD_LABEL[period.code as keyof typeof PERIOD_LABEL] ?? period.code}时段电度电费</span></td>
              <td className={cn(bodyCell, 'text-right')}>{formatDecimal(period.energyKWh, 0)} kWh</td>
              <td className={cn(bodyCell, 'text-right')}>{formatDecimal(period.rate, 2)} 元/kWh</td>
              <td className={bodyCell}><ShareBar value={period.cost} total={total} color={color} /></td>
              <td className={cn(bodyCell, 'text-right font-medium')}>¥{formatDecimal(period.cost, 0)}</td>
            </tr>
          );
        })}
        {(charges ?? []).map((charge) => (
          <tr key={charge.code}>
            <td className={bodyCell}>
              <span className="flex items-center gap-2"><span className="size-2 rounded-[2px] bg-foreground/60" aria-hidden="true" />需量电费（{PERIOD_LABEL[charge.code]}段）</span>
              {chargesNote ? <span className="mt-0.5 block pl-4 text-xs text-muted-foreground">{chargesNote}</span> : null}
            </td>
            <td className={cn(bodyCell, 'text-right')}>{charge.peak ? `${formatDecimal(charge.peak.demandKW, 1)} kW` : '—'}</td>
            <td className={cn(bodyCell, 'text-right')}>{formatDecimal(charge.ratePerKW, 0)} 元/kW</td>
            <td className={bodyCell}>{charge.charge ? <ShareBar value={charge.charge} total={total} color="var(--foreground)" /> : <span className="text-muted-foreground">—</span>}</td>
            <td className={cn(bodyCell, 'text-right font-medium')}>{charge.charge === null ? '—' : `¥${formatDecimal(charge.charge, 0)}`}</td>
          </tr>
        ))}
        <tr className="font-semibold">
          <td className="px-3 pt-3">合计</td>
          <td className="px-3 pt-3 text-right">{energyText(energyKWh)}</td>
          <td className="px-3 pt-3 text-right text-xs font-normal text-muted-foreground">均价 {energyKWh > 0 ? formatDecimal(total / energyKWh, 3) : '—'} 元/kWh</td>
          <td className="px-3 pt-3" />
          <td className="px-3 pt-3 text-right">¥{formatDecimal(total, 0)}</td>
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
  readonly plantKWh: number;
}

export function SavingsGrid({ totals, daysNote }: { readonly totals: SavingsFigures; readonly daysNote: string }) {
  const rate = siteSavingsRate(totals);
  return (
    <FigureGrid>
      <Figure label="节电量" value={totals.days > 0 ? formatDecimal(totals.avoidedKWh, 0) : null} unit="kWh" note={totals.days > 0 ? `冷站同期用电 ${energyText(totals.plantKWh)}` : '本月没有统计结果'} />
      <Figure label="节电率" info="节电量 ÷（冷站同期用电 + 节电量）" value={rate === null ? null : formatDecimal(rate * 100, 1)} unit="%" note="对比没做措施时的预计用电" />
      <Figure label="节省电费" value={totals.days > 0 ? `¥${formatDecimal(totals.avoidedCost, 0)}` : null} note={costBasis(totals)} />
      <Figure label="统计天数" value={String(totals.days)} unit="天" note={daysNote} />
    </FigureGrid>
  );
}

function Count({ label, value, unit, detail }: { readonly label: string; readonly value: string; readonly unit: string; readonly detail?: string }) {
  return (
    <div className="flex min-w-0 flex-col gap-0.5">
      <span className="text-xs text-muted-foreground">{label}</span>
      <span className="text-base leading-[22px] font-semibold tracking-[-0.02em] tabular-nums">{value}<span className="ml-1 text-xs font-medium tracking-normal text-subtle-foreground">{unit}</span></span>
      {detail ? <span className="truncate text-xs text-muted-foreground">{detail}</span> : null}
    </div>
  );
}

function OperationsGroup({ title, note, children }: { readonly title: string; readonly note?: string; readonly children: ReactNode }) {
  return (
    <div className="flex flex-col gap-3 rounded-lg border border-divider px-4 py-3">
      <h3 className="flex items-baseline gap-2 text-sm font-semibold">{title}{note ? <span className="text-xs font-normal text-muted-foreground">{note}</span> : null}</h3>
      <div className="grid grid-cols-3 gap-3">{children}</div>
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
    <div className="grid grid-cols-3 gap-4">
      <OperationsGroup title="告警" note={liveNote}>
        {alarms ? (
          <>
            <Count
              label="新发生"
              value={String(alarms.raised)}
              unit="条"
              detail={SEVERITY_ORDER.filter((severity) => alarms.bySeverity[severity] > 0).map((severity) => `${SEVERITY_LABELS[severity]} ${alarms.bySeverity[severity]}`).join(' · ') || undefined}
            />
            <Count label="已恢复" value={String(alarms.cleared)} unit="条" />
            <Count label="平均恢复" value={alarms.meanMinutesToClear === null ? '—' : formatDecimal(alarms.meanMinutesToClear, 0)} unit="分钟" />
          </>
        ) : <Skeleton className="col-span-3 h-12" />}
      </OperationsGroup>
      <OperationsGroup title="工单" note={liveNote}>
        {orders ? (
          <>
            <Count label="新建" value={String(orders.opened)} unit="张" />
            <Count label="完成" value={String(orders.completed)} unit="张" />
            <Count label="仍未结束" value={String(orders.stillOpen)} unit="张" />
          </>
        ) : <Skeleton className="col-span-3 h-12" />}
      </OperationsGroup>
      <OperationsGroup title="诊断发现">
        {findings ? (
          <>
            <Count label="发现问题" value={String(findings.count)} unit="条" />
            {findings.byType.slice(0, 2).map((group) => (
              <Count key={group.findingType} label={ruleFor(group.findingType)?.title ?? '其他发现'} value={String(group.count)} unit="条" />
            ))}
          </>
        ) : findingsUnavailable ? <p className="col-span-3 text-sm text-muted-foreground">诊断发现暂时读不到</p> : <Skeleton className="col-span-3 h-12" />}
      </OperationsGroup>
    </div>
  );
}
