import type { SiteTariff } from '@/api/generated/platformGateway.gen';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { formatDecimal } from '@/lib/operator-format';
import { cn } from '@/lib/utils';
import { PERIOD_COLOR } from '../period-presentation';
import { daySchedule, PERIOD_LABEL, periodRanges, type PeriodCode, type TouCost } from '../tariff-model';

const clock = (minute: number) => `${String(Math.floor(minute / 60)).padStart(2, '0')}:${String(minute % 60).padStart(2, '0')}`;

/** Today's periods as a 24-hour band, with the time now marked. */
function TariffDayBand({ tariff, timeZone, now, className }: { readonly tariff: SiteTariff; readonly timeZone: string; readonly now: Date; readonly className?: string }) {
  const schedule = daySchedule(tariff, now, timeZone);
  const local = new Intl.DateTimeFormat('en-GB', { timeZone, hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }).format(now).split(':').map(Number);
  const minuteNow = local[0] * 60 + local[1];
  return (
    <div className={cn('flex flex-col gap-1.5', className)}>
      <div className="relative flex h-3 overflow-hidden rounded-sm bg-muted">
        {schedule.map((period) => (
          <div
            key={`${period.startMinute}`}
            title={`${PERIOD_LABEL[period.periodCode]} ${clock(period.startMinute)}–${clock(period.endMinute)} · ${formatDecimal(period.energyRatePerKWh, 2)} 元/kWh`}
            className="absolute inset-y-0 border-r border-background/70"
            style={{ left: `${(period.startMinute / 1440) * 100}%`, width: `${((period.endMinute - period.startMinute) / 1440) * 100}%`, background: PERIOD_COLOR[period.periodCode] }}
          />
        ))}
        <div className="absolute inset-y-[-2px] w-0.5 rounded-full bg-foreground" style={{ left: `${(minuteNow / 1440) * 100}%` }} aria-hidden="true" />
      </div>
      <div className="flex justify-between text-[11px] text-muted-foreground tabular-nums">
        {[0, 6, 12, 18, 24].map((hour) => <span key={hour}>{String(hour).padStart(2, '0')}:00</span>)}
      </div>
    </div>
  );
}

/** Each period's energy rate on the day of an instant, falling back to any day that has it. */
function ratesOn(tariff: SiteTariff, now: Date, timeZone: string): Map<PeriodCode, number> {
  const rates = new Map<PeriodCode, number>();
  for (const period of [...daySchedule(tariff, now, timeZone), ...tariff.periods]) {
    if (!rates.has(period.periodCode)) rates.set(period.periodCode, period.energyRatePerKWh);
  }
  return rates;
}

/**
 * The period's metered HVAC electricity priced by the Site's time-of-use Tariff, split by
 * period, beside today's periods. An estimate at published rates; the bill is Settlement's.
 */
export function TouCostDetail({ tariff, cost, timeZone }: {
  readonly tariff: SiteTariff;
  readonly cost: TouCost;
  readonly timeZone: string;
}) {
  const now = new Date();
  const rates = ratesOn(tariff, now, timeZone);
  const ranges = periodRanges(tariff, now, timeZone);
  const share = (energy: number) => (cost.energyKWh > 0 ? energy / cost.energyKWh : 0);
  return (
    <div role="region" aria-label="分时电费">
      <div className="grid grid-cols-[260px_minmax(0,1fr)] gap-8 px-5 py-4">
        <div className="flex flex-col gap-3">
          <span className="text-xs text-muted-foreground">今日电价时段</span>
          <TariffDayBand tariff={tariff} timeZone={timeZone} now={now} />
          <div className="flex flex-wrap gap-x-4 gap-y-1 text-xs">
            {[...rates.keys()].map((code) => (
              <span key={code} className="flex items-center gap-1.5">
                <span className="size-2 rounded-[2px]" style={{ background: PERIOD_COLOR[code] }} aria-hidden="true" />
                {PERIOD_LABEL[code]}
              </span>
            ))}
          </div>
        </div>
        <Table aria-label="分时段电费" className="[&_td]:whitespace-nowrap [&_th]:whitespace-nowrap">
          <TableHeader>
            <TableRow className="hover:bg-transparent">
              <TableHead>时段</TableHead>
              <TableHead>时间</TableHead>
              <TableHead className="text-right">电价</TableHead>
              <TableHead className="text-right">用电</TableHead>
              <TableHead>占比</TableHead>
              <TableHead className="text-right">电费</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {cost.byPeriod.length === 0 ? (
              <TableRow className="hover:bg-transparent"><TableCell colSpan={6} className="py-6 text-center text-muted-foreground">本期间还没有计量数据</TableCell></TableRow>
            ) : cost.byPeriod.map((usage) => (
              <TableRow key={usage.code} className="hover:bg-transparent">
                <TableCell>
                  <span className="flex items-center gap-2 font-medium">
                    <span className="size-2 shrink-0 rounded-[2px]" style={{ background: PERIOD_COLOR[usage.code] }} aria-hidden="true" />
                    {PERIOD_LABEL[usage.code]}
                  </span>
                </TableCell>
                <TableCell className="max-w-64 text-xs leading-5 whitespace-normal! text-muted-foreground">{ranges.get(usage.code) ?? '今日不适用'}</TableCell>
                <TableCell className="text-right tabular-nums">
                  {rates.has(usage.code) ? formatDecimal(rates.get(usage.code)!, 2) : '—'}
                  <span className="ml-0.5 text-xs text-muted-foreground">元/kWh</span>
                </TableCell>
                <TableCell className="text-right tabular-nums">
                  {formatDecimal(usage.energyKWh, 0)}
                  <span className="ml-0.5 text-xs text-muted-foreground">kWh</span>
                </TableCell>
                <TableCell>
                  <span className="flex items-center gap-2">
                    <span className="h-1.5 w-20 overflow-hidden rounded-full bg-muted" aria-hidden="true">
                      <span className="block h-full rounded-full" style={{ width: `${share(usage.energyKWh) * 100}%`, background: PERIOD_COLOR[usage.code] }} />
                    </span>
                    <span className="text-xs tabular-nums">{formatDecimal(share(usage.energyKWh) * 100, 1)}%</span>
                  </span>
                </TableCell>
                <TableCell className="text-right font-medium tabular-nums">¥{formatDecimal(usage.cost, 0)}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
      {cost.unpricedKWh > 0.5 ? (
        <p className="border-t border-divider px-5 py-2.5 text-xs text-muted-foreground">
          有 {formatDecimal(cost.unpricedKWh, 0)} kWh 落在电价未覆盖的时段，未计入电费
        </p>
      ) : null}
    </div>
  );
}
