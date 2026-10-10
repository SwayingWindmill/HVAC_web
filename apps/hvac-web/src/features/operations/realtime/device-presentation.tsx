import { Fan, Gauge, Snowflake, ThermometerSun, Waves } from 'lucide-react';
import type { StatusTone } from '@/components/common/StatusBadge';
import { MetricValue } from '@/blocks/metric-card';
import { formatDecimal } from '@/lib/operator-format';
import { cn } from '@/lib/utils';
import type { PlantCategory, PlantDevice, PlantReading } from './plant-model';
import type { PlantLiveMode } from './use-realtime-plant';

// How plant equipment is presented wherever operators look at it: the realtime plant
// view and the equipment ledger.

// Key observations per equipment role, in display order. A device shows the ones it has.
export const KEY_READINGS: Readonly<Record<PlantCategory, readonly string[]>> = {
  CHILLER: [
    'chiller.compressor_load',
    'chiller.leaving_chilled_water_temperature',
    'chiller.chilled_water_temperature_setpoint',
    'chiller.cop',
  ],
  CHILLED_WATER_PUMP: ['chwp.frequency', 'chwp.flow_rate'],
  COOLING_WATER_PUMP: ['cwp.frequency', 'cwp.flow_rate'],
  COOLING_TOWER: ['cooling_tower.fan_speed', 'cooling_tower.leaving_water_temperature', 'cooling_tower.approach_temperature'],
  METER: [
    'hvac_meter.active_power',
    'hvac_meter.power_factor',
    'btu_meter.instant_cooling_capacity',
    'btu_meter.temperature_difference',
    'btu_meter.flow_rate',
  ],
  WEATHER: ['weather.ambient_dry_bulb_temperature', 'weather.ambient_wet_bulb_temperature', 'weather.relative_humidity'],
  OTHER: [],
};

export const POWER_KEY: Partial<Record<PlantCategory, string>> = {
  CHILLER: 'chiller.power',
  CHILLED_WATER_PUMP: 'chwp.power',
  COOLING_WATER_PUMP: 'cwp.power',
  COOLING_TOWER: 'cooling_tower.power',
};

export const CATEGORY_ICONS: Partial<Record<PlantCategory, typeof Snowflake>> = {
  CHILLER: Snowflake,
  CHILLED_WATER_PUMP: Waves,
  COOLING_WATER_PUMP: Waves,
  COOLING_TOWER: Fan,
  METER: Gauge,
  WEATHER: ThermometerSun,
};

export function deviceStatus(device: PlantDevice): { tone: StatusTone; label: string } {
  if (device.connection === 'OFFLINE') return { tone: 'offline', label: '离线' };
  if (device.runState === 'FAULT') return { tone: 'alarm', label: device.faultCode ? `故障 ${device.faultCode}` : '故障' };
  if (device.runState === 'RUNNING') return { tone: 'running', label: '运行' };
  if (device.runState === 'STOPPED') return { tone: 'neutral', label: '停机' };
  if (device.connection === 'ONLINE') return { tone: 'success', label: '在线' };
  return { tone: 'neutral', label: '状态未知' };
}

export function formatClock(value: string | null, timezone: string): string {
  if (!value) return '—';
  return new Intl.DateTimeFormat('zh-CN', {
    timeZone: timezone,
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false,
  }).format(new Date(value));
}

export function LiveIndicator({ mode }: { readonly mode: PlantLiveMode }) {
  const presentation = {
    live: { dot: 'bg-emerald-500', label: '实时推送' },
    connecting: { dot: 'bg-amber-500', label: '正在连接推送' },
    polling: { dot: 'bg-muted-foreground', label: '每 30 秒刷新' },
  }[mode];
  return (
    <span className="inline-flex items-center gap-1.5 text-xs text-muted-foreground">
      <span className={cn('size-1.5 rounded-full', presentation.dot)} aria-hidden="true" />
      {presentation.label}
    </span>
  );
}

export function ReadingValue({ reading, className }: { readonly reading: PlantReading | null; readonly className?: string }) {
  if (!reading || reading.state !== 'PRESENT') return <span className={cn('text-muted-foreground', className)}>—</span>;
  return (
    <span className={cn('tabular-nums', !reading.current && 'text-muted-foreground', className)} title={reading.current ? undefined : '数据过期或质量降级'}>
      {reading.display}
      {reading.unit ? <span className="ml-0.5 text-xs font-normal text-muted-foreground">{reading.unit}</span> : null}
    </span>
  );
}

export function KeyReadings({ device }: { readonly device: PlantDevice }) {
  const readings = KEY_READINGS[device.category]
    .map((key) => device.reading(key))
    .filter((reading): reading is PlantReading => Boolean(reading));
  if (readings.length === 0) return <span className="text-muted-foreground">—</span>;
  return (
    <span className="flex flex-wrap gap-x-4 gap-y-0.5">
      {readings.map((reading) => (
        <span key={reading.label} className="whitespace-nowrap">
          <span className="text-muted-foreground">{reading.label} </span>
          <ReadingValue reading={reading} />
        </span>
      ))}
    </span>
  );
}

/** A plant reading as a headline figure; a stale or degraded value is dimmed, never shown as current. */
export function ReadingMetric({ reading, digits, unit }: { readonly reading: PlantReading | null; readonly digits: number; readonly unit: string }) {
  const present = reading && reading.state === 'PRESENT' && reading.numeric !== null ? reading : null;
  return <MetricValue value={present ? formatDecimal(present.numeric!, digits) : null} unit={unit} stale={present ? !present.current : false} />;
}
