import { Bar, BarChart, CartesianGrid, Cell, ReferenceLine, XAxis, YAxis } from 'recharts';
import { ChartContainer, ChartTooltip, type ChartConfig } from '@/components/ui/chart';
import { formatDecimal } from '@/lib/operator-format';

// Each day's highest quarter-hour this month, the month's highest marked, and tomorrow's forecast
// highest beside them so it reads against the days already metered.

const config = { peak: { label: '当日最高负荷', color: 'var(--chart-1)' } } satisfies ChartConfig;

export interface DailyPeakBar {
  readonly day: string;
  readonly peak: number | null;
  readonly kind: 'day' | 'monthMax' | 'today' | 'forecast';
}

export function DailyPeaks({ bars, monthPeakKW }: { readonly bars: readonly DailyPeakBar[]; readonly monthPeakKW: number | null }) {
  if (bars.length === 0) return <p className="py-16 text-center text-sm text-muted-foreground">本月还没有负荷数据</p>;
  return (
    <div className="flex flex-col gap-2 px-5 pt-3 pb-3">
      <span className="flex items-center justify-end gap-3 text-xs text-muted-foreground">
        <span className="flex items-center gap-1.5"><span className="size-2.5 rounded-[2px] bg-destructive" aria-hidden="true" />本月最高</span>
        <span className="flex items-center gap-1.5"><span className="size-2.5 rounded-[2px] bg-chart-1" aria-hidden="true" />其他日</span>
        <span className="flex items-center gap-1.5"><span className="size-2.5 rounded-[2px] bg-chart-1/40" aria-hidden="true" />今天（进行中）</span>
        <span className="flex items-center gap-1.5"><span className="size-2.5 rounded-[2px] border border-dashed border-chart-1 bg-chart-1/10" aria-hidden="true" />明天（预测）</span>
      </span>
      <ChartContainer config={config} className="aspect-auto h-[260px] w-full">
        <BarChart data={bars as DailyPeakBar[]} margin={{ left: 4, right: 8, top: 12 }}>
          <CartesianGrid vertical={false} />
          <XAxis dataKey="day" tickLine={false} axisLine={false} interval="preserveStartEnd" minTickGap={16} />
          <YAxis tickLine={false} axisLine={false} width={44} domain={[0, 'auto']} />
          <ChartTooltip
            cursor={false}
            content={({ active, payload }) => {
              const bar = payload?.[0]?.payload as DailyPeakBar | undefined;
              if (!active || !bar || bar.peak === null) return null;
              return (
                <div className="grid min-w-32 gap-1 rounded-lg border bg-background px-2.5 py-1.5 text-xs shadow-xl">
                  <p className="font-medium">{bar.day}</p>
                  <p className="flex justify-between gap-4">
                    <span className="text-muted-foreground">{bar.kind === 'forecast' ? '预测最高' : '最高负荷'}</span>
                    <span className="tabular-nums">{formatDecimal(bar.peak, 1)} kW</span>
                  </p>
                </div>
              );
            }}
          />
          {monthPeakKW !== null ? <ReferenceLine y={monthPeakKW} stroke="var(--destructive)" strokeDasharray="6 4" /> : null}
          <Bar dataKey="peak" radius={[3, 3, 0, 0]} maxBarSize={28} isAnimationActive={false}>
            {bars.map((bar) => (
              <Cell
                key={bar.day}
                fill={bar.kind === 'monthMax' ? 'var(--destructive)' : 'var(--color-peak)'}
                fillOpacity={bar.kind === 'today' ? 0.4 : bar.kind === 'forecast' ? 0.12 : 1}
                stroke={bar.kind === 'forecast' ? 'var(--color-peak)' : undefined}
                strokeDasharray={bar.kind === 'forecast' ? '3 2' : undefined}
              />
            ))}
          </Bar>
        </BarChart>
      </ChartContainer>
    </div>
  );
}
