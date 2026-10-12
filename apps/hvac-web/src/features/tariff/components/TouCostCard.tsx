import { useQuery } from '@tanstack/react-query';
import { getRouteApi } from '@tanstack/react-router';
import { ReceiptText } from 'lucide-react';
import { useMemo } from 'react';
import type { SiteTariff } from '@/api/generated/platformGateway.gen';
import { Badge } from '@/components/ui/badge';
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from '@/components/ui/card';
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from '@/components/ui/empty';
import { Skeleton } from '@/components/ui/skeleton';
import { ENERGY_PERIODS, type EnergyPeriod } from '@/features/energy-analysis/consumption/model';
import { useHourlyElectricity } from '@/features/energy-analysis/consumption/query';
import { formatDecimal } from '@/lib/operator-format';
import { cn } from '@/lib/utils';
import { PERIOD_COLOR } from '../period-presentation';
import { siteTariffQuery } from '../query';
import { daySchedule, PERIOD_LABEL, touEnergyCost, type PeriodCode } from '../tariff-model';

const siteRoute = getRouteApi('/_app/_site');

const clock = (minute: number) => `${String(Math.floor(minute / 60)).padStart(2, '0')}:${String(minute % 60).padStart(2, '0')}`;
const percent = (part: number, whole: number) => (whole > 0 ? `${formatDecimal((part / whole) * 100, 0)}%` : '—');

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

/** One rate per period of the day, in clock order of first appearance. */
function dayRates(tariff: SiteTariff, timeZone: string, now: Date) {
  const rates = new Map<PeriodCode, number>();
  for (const period of daySchedule(tariff, now, timeZone)) if (!rates.has(period.periodCode)) rates.set(period.periodCode, period.energyRatePerKWh);
  return [...rates.entries()].sort(([, left], [, right]) => right - left);
}

/**
 * The period's metered HVAC electricity priced by the Site's time-of-use Tariff, split by
 * period. An estimate at published rates; the bill is Settlement's.
 */
export function TouCostCard({ period }: { readonly period: EnergyPeriod }) {
  const { site } = siteRoute.useRouteContext();
  const tariff = useQuery(siteTariffQuery(site.id));
  const hourly = useHourlyElectricity(period);
  const cost = useMemo(
    () => (tariff.data && hourly.data ? touEnergyCost(hourly.data, tariff.data, site.timezone) : null),
    [tariff.data, hourly.data, site.timezone],
  );
  const now = new Date();

  if (tariff.isPending || (tariff.data && hourly.isPending)) return <Skeleton className="h-[188px] rounded-xl" />;
  if (tariff.data === null) {
    return (
      <Card role="region" aria-label="分时电费">
        <Empty className="py-10">
          <EmptyHeader>
            <EmptyMedia variant="icon"><ReceiptText /></EmptyMedia>
            <EmptyTitle>站点还没有配置电价</EmptyTitle>
            <EmptyDescription>为站点的电网进线发布电价后，这里按峰平谷时段估算电费。</EmptyDescription>
          </EmptyHeader>
        </Empty>
      </Card>
    );
  }
  if (tariff.isError || hourly.isError || !tariff.data || !cost) {
    return <Card role="region" aria-label="分时电费"><CardContent className="py-10 text-center text-sm text-muted-foreground">电价或逐时用电暂不可用</CardContent></Card>;
  }

  const priced = cost.energyKWh - cost.unpricedKWh;
  return (
    <Card role="region" aria-label="分时电费">
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          分时电费
          <Badge variant="outline" className="font-normal">按电价估算</Badge>
        </CardTitle>
        <CardDescription>{tariff.data.displayName} · {ENERGY_PERIODS[period]}空调用电按峰平谷单价计价，正式电费以结算为准</CardDescription>
      </CardHeader>
      <CardContent className="grid gap-6 @4xl/main:grid-cols-[minmax(0,13rem)_minmax(0,1.4fr)_minmax(0,1fr)] @4xl/main:gap-8">
        <div className="flex flex-col gap-1">
          <span className="text-sm text-muted-foreground">估算电度电费</span>
          <span className="text-[1.75rem] leading-none font-medium tracking-tight tabular-nums">
            {formatDecimal(cost.total, 0)}
            <span className="ml-1 text-sm font-normal tracking-normal text-muted-foreground">元</span>
          </span>
          <span className="mt-1 text-xs text-muted-foreground tabular-nums">
            {priced > 0 ? `均价 ${formatDecimal(cost.total / priced, 3)} 元/kWh · 用电 ${formatDecimal(cost.energyKWh, 0)} kWh` : '本期间还没有计量数据'}
          </span>
        </div>

        <div className="flex min-w-0 flex-col gap-3">
          <div className="flex h-2.5 overflow-hidden rounded-full bg-muted" role="img" aria-label="峰平谷用电占比">
            {cost.byPeriod.map((usage) => (
              <div key={usage.code} style={{ width: percent(usage.energyKWh, cost.energyKWh), background: PERIOD_COLOR[usage.code] }} />
            ))}
          </div>
          <dl className="grid gap-1.5 text-sm">
            {cost.byPeriod.map((usage) => (
              <div key={usage.code} className="grid grid-cols-[minmax(0,1fr)_auto_auto] items-baseline gap-4">
                <dt className="flex items-center gap-2">
                  <span className="size-2 shrink-0 rounded-[2px]" style={{ background: PERIOD_COLOR[usage.code] }} aria-hidden="true" />
                  {PERIOD_LABEL[usage.code]}时段
                  <span className="text-xs text-muted-foreground tabular-nums">{percent(usage.energyKWh, cost.energyKWh)}</span>
                </dt>
                <dd className="text-right text-muted-foreground tabular-nums">{formatDecimal(usage.energyKWh, 0)} kWh</dd>
                <dd className="min-w-20 text-right font-medium tabular-nums">{formatDecimal(usage.cost, 0)} 元</dd>
              </div>
            ))}
          </dl>
        </div>

        <div className="flex min-w-0 flex-col gap-3">
          <span className="text-sm text-muted-foreground">今日电价时段</span>
          <TariffDayBand tariff={tariff.data} timeZone={site.timezone} now={now} />
          <div className="flex flex-wrap gap-x-4 gap-y-1 text-xs">
            {dayRates(tariff.data, site.timezone, now).map(([code, rate]) => (
              <span key={code} className="flex items-center gap-1.5">
                <span className="size-2 rounded-[2px]" style={{ background: PERIOD_COLOR[code] }} aria-hidden="true" />
                {PERIOD_LABEL[code]}
                <span className="text-muted-foreground tabular-nums">{formatDecimal(rate, 2)} 元/kWh</span>
              </span>
            ))}
          </div>
        </div>
      </CardContent>
      {cost.unpricedKWh > 0.5 ? (
        <CardFooter className="border-t text-xs text-muted-foreground">
          有 {formatDecimal(cost.unpricedKWh, 0)} kWh 落在电价未覆盖的时段，未计入电费
        </CardFooter>
      ) : null}
    </Card>
  );
}
