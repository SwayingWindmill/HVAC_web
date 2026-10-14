import { useQuery } from '@tanstack/react-query';
import { getRouteApi } from '@tanstack/react-router';
import { statusDotClass } from '@/components/common/StatusBadge';
import { Sparkline } from '@/components/ui/sparkline';
import { formatTelemetryUnit } from '@/domain/centralPlantTelemetry';
import { formatDecimal } from '@/lib/operator-format';
import { cn } from '@/lib/utils';
import { CATEGORY_ICONS, deviceStatus, formatClock, KEY_READINGS, POWER_KEY, ReadingValue } from '../../realtime/device-presentation';
import { PLANT_CATEGORY_LABELS, tileReadings, type PlantDevice } from '../../realtime/plant-model';
import { deviceDayHistoryQuery } from '../device-history';

const siteRoute = getRouteApi('/_app/_site');

/**
 * One device on the wall: what it is and its state, its headline reading with the last day's
 * shape, two more readings, and whether its points, data and work are in order.
 */
export function DeviceTile({ device, openWorkOrders, selected, onOpen }: {
  readonly device: PlantDevice;
  readonly openWorkOrders: number;
  readonly selected: boolean;
  readonly onOpen: () => void;
}) {
  const { site, principal } = siteRoute.useRouteContext();
  const status = deviceStatus(device);
  const { lead, facts } = tileReadings(
    device,
    [POWER_KEY[device.category], ...KEY_READINGS[device.category]].filter((key): key is string => Boolean(key)),
    KEY_READINGS[device.category],
  );
  const pointCode = lead ? device.row.telemetryPoints.find((point) => point.sourceKey === lead.sourceKey)?.pointCode : undefined;
  const history = useQuery({
    ...deviceDayHistoryQuery({ siteId: site.id, deviceId: device.deviceId, keys: pointCode ? [pointCode] : [], timezone: site.timezone, csrfToken: principal.session.csrfToken }),
    enabled: Boolean(pointCode) && principal.authorization.capabilities.includes('telemetry.history.read'),
  });
  const series = pointCode ? history.data?.get(pointCode) ?? [] : [];
  const values = series.map((point) => point.average);
  const unit = lead?.reading.unit ?? formatTelemetryUnit(undefined);
  const points = device.row.operational.points;
  const healthy = points.filter((point) => point.freshness === 'FRESH' && point.quality === 'GOOD').length;
  const Icon = CATEGORY_ICONS[device.category];
  const stale = device.hasStaleData || device.connection === 'OFFLINE';

  return (
    <button
      type="button"
      onClick={onOpen}
      aria-current={selected ? 'true' : undefined}
      aria-label={`${device.name}，${status.label}`}
      className={cn(
        'flex min-w-0 flex-col rounded-xl border bg-card text-left shadow-xs outline-none transition-colors duration-100 hover:border-input-hover focus-visible:ring-3 focus-visible:ring-ring-soft',
        selected && 'border-primary ring-1 ring-primary hover:border-primary',
      )}
    >
      <span className="flex items-center gap-2 px-4 pt-3.5">
        {Icon ? <Icon className="size-3.5 text-muted-foreground" aria-hidden="true" /> : null}
        <span className="truncate text-sm font-semibold">{device.name}</span>
        <span className="ml-auto flex shrink-0 items-center gap-1.5 text-xs">
          <span className={cn('size-2 rounded-full', statusDotClass(status.tone))} aria-hidden="true" />
          {status.label}
        </span>
      </span>
      <span className="truncate px-4 pt-1 text-xs text-muted-foreground">
        {PLANT_CATEGORY_LABELS[device.category]}{lead ? ` · ${lead.reading.label}` : ''}
      </span>
      <span className={cn('px-4 pt-1 text-[26px] leading-8 font-semibold tracking-[-0.03em] tabular-nums', stale && 'text-muted-foreground')}>
        {lead ? <ReadingValue reading={lead.reading} /> : <span className="text-muted-foreground">—</span>}
      </span>
      {facts.length > 0 ? (
        <span className="grid grid-cols-2 gap-x-3 px-4 pt-2">
          {facts.map((reading) => (
            <span key={reading.label} className="flex min-w-0 flex-col gap-0.5">
              <span className="truncate text-xs text-muted-foreground">{reading.label}</span>
              <span className="text-sm font-medium"><ReadingValue reading={reading} /></span>
            </span>
          ))}
        </span>
      ) : null}
      <span className="flex flex-col gap-0.5 px-4 pt-3 pb-3">
        {values.length > 1 ? (
          <Sparkline data={values} color={stale ? 'var(--muted-foreground)' : 'var(--chart-1)'} animate={false} className="h-9" />
        ) : (
          <span className="flex h-9 items-center text-xs text-subtle-foreground">{history.isPending && pointCode ? '' : '近 24 小时没有记录'}</span>
        )}
        {values.length > 1 ? (
          <span className="flex justify-between text-[11px] text-subtle-foreground">
            <span>24 小时</span>
            <span className="tabular-nums">
              {formatDecimal(Math.min(...series.map((point) => point.minimum)), 1)} – {formatDecimal(Math.max(...series.map((point) => point.maximum)), 1)} {unit ?? ''}
            </span>
          </span>
        ) : null}
      </span>
      <span className="mt-auto flex items-center gap-2 border-t border-divider px-4 py-2.5 text-xs text-muted-foreground">
        <span className={cn(healthy < points.length && 'text-warning-text')}>点位 {healthy}/{points.length}</span>
        <span className="text-subtle-foreground">·</span>
        <span className={cn('font-mono', device.hasStaleData && 'text-warning-text')}>{formatClock(device.latestSampleAt, site.timezone)}</span>
        <span className={cn('ml-auto', openWorkOrders > 0 ? 'font-medium text-primary' : 'text-subtle-foreground')}>
          {openWorkOrders > 0 ? `工单 ${openWorkOrders}` : '无工单'}
        </span>
      </span>
    </button>
  );
}
