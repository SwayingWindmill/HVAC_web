import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { PERIOD_COLOR } from '@/features/tariff/period-presentation';
import { PERIOD_LABEL, type PeriodCode } from '@/features/tariff/tariff-model';
import { formatDecimal } from '@/lib/operator-format';
import { cn } from '@/lib/utils';
import type { HourlyForecast } from '../forecast-model';

export interface ForecastHour extends HourlyForecast {
  readonly period: PeriodCode | null;
  /** The same hour a day earlier, metered. */
  readonly previousKW: number | null;
  /** How far the hour's upper bound sits below the month's highest load, in an hour billed on demand. */
  readonly headroomKW: number | null;
  readonly peak: boolean;
}

/** One day's forecast hours: when, the price period, the forecast with its band, and the day before. */
export function ForecastHours({ title, hours, headroom }: {
  readonly title: string;
  readonly hours: readonly ForecastHour[];
  /** Whether the Tariff bills demand, so the distance to the month's highest load matters. */
  readonly headroom: boolean;
}) {
  return (
    <div className="flex min-w-0 flex-col">
      <p className="border-b border-divider px-5 py-2 text-xs font-medium text-muted-foreground">{title}</p>
      {hours.length === 0 ? (
        <p className="px-5 py-10 text-center text-sm text-muted-foreground">没有预测</p>
      ) : (
        <Table aria-label={`${title}逐时预测`} className="[&_td]:whitespace-nowrap [&_th]:whitespace-nowrap">
          <TableHeader>
            <TableRow className="hover:bg-transparent">
              <TableHead className="pl-5">时段</TableHead>
              <TableHead>电价</TableHead>
              <TableHead className="text-right">预测（kW）</TableHead>
              <TableHead className={cn('text-right', !headroom && 'pr-5')}>前一天</TableHead>
              {headroom ? <TableHead className="pr-5 text-right">距本月最高</TableHead> : null}
            </TableRow>
          </TableHeader>
          <TableBody>
            {hours.map((hour) => (
              <TableRow key={hour.start} className={cn(hour.peak && 'bg-primary-soft hover:bg-primary-soft')}>
                <TableCell className="pl-5">
                  <span className="flex items-center gap-2">
                    <span className="font-mono text-xs">{hour.label}</span>
                    {hour.peak ? <span className="rounded-md bg-primary px-1.5 text-[11px] leading-[18px] font-medium text-primary-foreground">最高</span> : null}
                  </span>
                </TableCell>
                <TableCell>
                  {hour.period ? (
                    <span className="flex items-center gap-1.5 text-xs">
                      <span className="size-2 rounded-[2px]" style={{ background: PERIOD_COLOR[hour.period] }} aria-hidden="true" />
                      {PERIOD_LABEL[hour.period]}
                    </span>
                  ) : <span className="text-muted-foreground">—</span>}
                </TableCell>
                <TableCell className="text-right tabular-nums">
                  <span className="font-medium">{formatDecimal(hour.loadKW, 0)}</span>
                  {hour.lowerKW !== null && hour.upperKW !== null ? (
                    <span className="ml-1.5 text-xs text-muted-foreground">{formatDecimal(hour.lowerKW, 0)}–{formatDecimal(hour.upperKW, 0)}</span>
                  ) : null}
                </TableCell>
                <TableCell className={cn('text-right text-muted-foreground tabular-nums', !headroom && 'pr-5')}>
                  {hour.previousKW === null ? '—' : formatDecimal(hour.previousKW, 0)}
                </TableCell>
                {headroom ? (
                  <TableCell className="pr-5 text-right tabular-nums">
                    {hour.headroomKW === null ? <span className="text-muted-foreground">—</span> : (
                      <span className={cn('font-medium', hour.headroomKW < 20 && 'text-warning-text', hour.headroomKW < 0 && 'text-destructive-text')}>
                        {hour.headroomKW < 0 ? `超出 ${formatDecimal(-hour.headroomKW, 0)}` : formatDecimal(hour.headroomKW, 0)}
                        <span className="ml-0.5 text-xs font-normal text-muted-foreground">kW</span>
                      </span>
                    )}
                  </TableCell>
                ) : null}
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </div>
  );
}
