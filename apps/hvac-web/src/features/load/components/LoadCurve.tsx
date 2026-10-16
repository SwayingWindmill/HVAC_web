import type { ReactNode } from 'react';
import { Area, CartesianGrid, ComposedChart, Line, ReferenceArea, ReferenceLine, XAxis, YAxis } from 'recharts';
import { ChartContainer, ChartTooltip, type ChartConfig } from '@/components/ui/chart';
import { PERIOD_COLOR } from '@/features/tariff/period-presentation';
import type { PeriodCode } from '@/features/tariff/tariff-model';
import { formatDecimal, formatTime } from '@/lib/operator-format';
import type { LoadCurveRow, LoadDays } from '../load-model';

// Today and tomorrow on one axis: metered load up to now, yesterday's under it for comparison, the
// forecast and its band after it, the month's highest load as a line to stay under, and the
// peak-priced hours shaded.

const config = {
  actualKW: { label: '实际', color: 'var(--chart-1)' },
  previousKW: { label: '昨日同时段', color: 'var(--chart-5)' },
  forecastKW: { label: '预测', color: 'var(--chart-1)' },
  band: { label: '预测区间', color: 'var(--chart-1)' },
} satisfies ChartConfig;

const HOUR_MS = 3_600_000;

function Legend({ children, swatch }: { readonly children: ReactNode; readonly swatch: ReactNode }) {
  return <span className="flex items-center gap-1.5">{swatch}{children}</span>;
}

export function LoadCurve({ rows, days, now, monthPeakKW, spans, timeZone, forecastNote }: {
  readonly rows: readonly LoadCurveRow[];
  readonly days: LoadDays;
  readonly now: number;
  readonly monthPeakKW: number | null;
  readonly spans: readonly { from: number; to: number; code: PeriodCode }[];
  readonly timeZone: string;
  /** Said in place of the forecast legend while there is no forecast to draw. */
  readonly forecastNote: string | null;
}) {
  const clock = (at: number) => formatTime(new Date(at).toISOString(), timeZone).slice(-5);
  const label = (at: number) => `${at >= days.tomorrow ? '明天' : '今天'} ${clock(at)}`;
  const ticks = Array.from({ length: 13 }, (_, index) => days.today + index * 4 * HOUR_MS).filter((at) => at <= days.end);

  return (
    <section role="region" aria-label="负荷曲线" className="flex flex-col gap-2 px-5 pt-3.5 pb-3">
      <div className="flex min-h-7 items-center gap-2.5 text-sm">
        <span className="font-semibold">负荷曲线</span>
        <span className="text-xs text-muted-foreground">今天和明天 · 每 15 分钟平均功率 · kW</span>
        <span className="ml-auto flex items-center gap-3 text-xs text-muted-foreground">
          <Legend swatch={<span className="h-2 w-3 rounded-[2px] border-t-2 border-chart-1 bg-chart-1/15" aria-hidden="true" />}>实际</Legend>
          <Legend swatch={<span className="h-0.5 w-3 rounded-full bg-chart-5/70" aria-hidden="true" />}>昨日同时段</Legend>
          {forecastNote === null ? (
            <Legend swatch={<span className="h-2 w-3 rounded-[2px] border-t-2 border-dashed border-chart-1 bg-chart-1/10" aria-hidden="true" />}>预测及区间</Legend>
          ) : <span>{forecastNote}</span>}
          {spans.length ? <Legend swatch={<span className="size-2.5 rounded-[2px] bg-warning/25" aria-hidden="true" />}>电价高峰</Legend> : null}
          {monthPeakKW !== null ? (
            <Legend swatch={<span className="h-0.5 w-3 border-t border-dashed border-destructive" aria-hidden="true" />}>本月最高 {formatDecimal(monthPeakKW, 0)} kW</Legend>
          ) : null}
        </span>
      </div>
      <ChartContainer config={config} className="aspect-auto h-[320px] w-full">
        <ComposedChart data={rows as LoadCurveRow[]} margin={{ left: 4, right: 12, top: 18 }}>
          <CartesianGrid vertical={false} />
          <XAxis
            dataKey="at"
            type="number"
            scale="time"
            domain={[days.today, days.end]}
            ticks={ticks}
            tickFormatter={(at: number) => (at === days.tomorrow ? '明天' : clock(at))}
            tickLine={false}
            axisLine={false}
          />
          <YAxis tickLine={false} axisLine={false} width={44} domain={[0, 'auto']} />
          {spans.map((span) => (
            <ReferenceArea key={span.from} x1={span.from} x2={span.to} fill={PERIOD_COLOR[span.code]} fillOpacity={0.1} strokeOpacity={0} ifOverflow="hidden" />
          ))}
          <ReferenceLine x={days.tomorrow} stroke="var(--border)" />
          <ChartTooltip
            cursor={{ strokeDasharray: '3 3' }}
            content={({ active, payload }) => {
              const row = payload?.[0]?.payload as LoadCurveRow | undefined;
              if (!active || !row) return null;
              const line = (name: string, value: string) => (
                <p className="flex justify-between gap-4"><span className="text-muted-foreground">{name}</span><span className="tabular-nums">{value}</span></p>
              );
              return (
                <div className="grid min-w-40 gap-1.5 rounded-lg border bg-background px-2.5 py-1.5 text-xs shadow-xl">
                  <p className="font-medium">{label(row.at)}–{clock(row.at + 15 * 60_000)}</p>
                  {row.actualKW !== null ? line(row.at + 15 * 60_000 > now ? '实际（进行中）' : '实际', `${formatDecimal(row.actualKW, 1)} kW`) : null}
                  {row.forecastKW !== null ? line('预测', `${formatDecimal(row.forecastKW, 1)} kW`) : null}
                  {row.band ? line('区间', `${formatDecimal(row.band[0], 0)}–${formatDecimal(row.band[1], 0)} kW`) : null}
                  {row.previousKW !== null ? line('昨日同时段', `${formatDecimal(row.previousKW, 1)} kW`) : null}
                </div>
              );
            }}
          />
          <Area dataKey="band" stroke="none" fill="var(--color-band)" fillOpacity={0.1} connectNulls={false} isAnimationActive={false} />
          <Line dataKey="previousKW" stroke="var(--color-previousKW)" strokeOpacity={0.7} strokeWidth={1.25} dot={false} connectNulls={false} isAnimationActive={false} />
          <Area dataKey="actualKW" stroke="var(--color-actualKW)" strokeWidth={2} fill="var(--color-actualKW)" fillOpacity={0.14} dot={false} connectNulls={false} isAnimationActive={false} />
          <Line dataKey="forecastKW" stroke="var(--color-forecastKW)" strokeWidth={2} strokeDasharray="5 4" dot={false} connectNulls={false} isAnimationActive={false} />
          {monthPeakKW !== null ? <ReferenceLine y={monthPeakKW} stroke="var(--destructive)" strokeDasharray="6 4" /> : null}
          <ReferenceLine x={now} stroke="var(--muted-foreground)" strokeDasharray="4 4" label={{ value: '现在', position: 'top', fontSize: 11, fill: 'var(--muted-foreground)' }} />
        </ComposedChart>
      </ChartContainer>
    </section>
  );
}
