import { Panel, PanelHeader, PanelLink } from '@/blocks/panel';
import { InfoTip } from '@/components/info-tip';
import { Skeleton } from '@/components/ui/skeleton';
import { formatClock, ReadingValue } from '@/features/operations/realtime/device-presentation';
import {
  EQUIPMENT_CATEGORIES,
  equipmentStatus,
  isRunning,
  PLANT_CATEGORY_LABELS,
  PLANT_CATEGORY_ORDER,
  plantReading,
  type PlantDevice,
  type PlantReading,
  type PlantView,
} from '@/features/operations/realtime/plant-model';
import { formatDecimal } from '@/lib/operator-format';
import { cn } from '@/lib/utils';

type DeviceState = 'running' | 'stopped' | 'fault' | 'offline';

function deviceState(device: PlantDevice): DeviceState {
  if (device.connection === 'OFFLINE') return 'offline';
  if (device.runState === 'FAULT') return 'fault';
  return isRunning(device) ? 'running' : 'stopped';
}

const STATE_LABEL: Readonly<Record<DeviceState, string>> = { running: '运行', stopped: '停止', fault: '故障', offline: '离线' };

/** One device as a shape: ● running, ○ stopped, ▲ fault, a dashed ring offline. */
function DeviceDot({ device }: { readonly device: PlantDevice }) {
  const state = deviceState(device);
  const label = `${device.name} ${STATE_LABEL[state]}`;
  return (
    <svg viewBox="0 0 10 10" className="size-[9px] shrink-0" role="img" aria-label={label}>
      <title>{label}</title>
      {state === 'fault' ? <path d="M5 0.5 9.5 9H0.5z" className="fill-destructive" />
        : state === 'running' ? <circle cx="5" cy="5" r="5" className="fill-success" />
        : <circle cx="5" cy="5" r="3.8" fill="none" strokeWidth="1.5" strokeDasharray={state === 'offline' ? '1.6 1.4' : undefined} className="stroke-subtle-foreground" />}
    </svg>
  );
}

function current(reading: PlantReading | null): number | null {
  return reading && reading.state === 'PRESENT' && reading.numeric !== null ? reading.numeric : null;
}

/** Power and cooling on one scale, so the gap between them is the COP made visible. */
function ScaleBar({ label, reading, max, tone }: { readonly label: string; readonly reading: PlantReading | null; readonly max: number; readonly tone: 'power' | 'cooling' }) {
  const value = current(reading);
  return (
    <div className="grid grid-cols-[84px_minmax(0,1fr)_96px] items-center gap-3">
      <span className="text-xs text-muted-foreground">{label}</span>
      <span className="h-2.5 overflow-hidden rounded-full bg-muted">
        {value !== null && max > 0 ? (
          <span
            className={cn('block h-full rounded-full', tone === 'power' ? 'bg-chart-5' : 'bg-chart-1', reading && !reading.current && 'opacity-50')}
            style={{ width: `${Math.max(2, (value / max) * 100)}%` }}
          />
        ) : null}
      </span>
      <span className="text-right text-sm font-semibold tabular-nums">
        <ReadingValue reading={reading} />
      </span>
    </div>
  );
}

/** The plant this moment: efficiency first, then where power and cooling stand, then every machine as a dot. */
export function PlantNowPanel({ plant, registryState, siteId, timeZone }: {
  readonly plant: PlantView;
  readonly registryState: 'pending' | 'error' | 'success';
  readonly siteId: string;
  readonly timeZone: string;
}) {
  const status = equipmentStatus(plant);
  const power = plant.totalPower;
  const cooling = plant.coolingCapacity;
  const max = Math.max(current(power) ?? 0, current(cooling) ?? 0);
  const groups = PLANT_CATEGORY_ORDER.filter((category) => EQUIPMENT_CATEGORIES.has(category))
    .map((category) => ({ category, devices: plant.devices.filter((device) => device.category === category) }))
    .filter((group) => group.devices.length > 0);
  const issues = [status.faults > 0 ? `${status.faults} 台故障` : null, status.offline > 0 ? `${status.offline} 台离线` : null].filter(Boolean);

  return (
    <Panel role="region" aria-label="冷站实时">
      <PanelHeader
        title="冷站实时"
        caption={<>此刻 · <span className="font-mono">{plant.latestSampleAt ? formatClock(plant.latestSampleAt, timeZone) : '等待数据'}</span></>}
        action={<PanelLink to="/operations/realtime" search={{ site: siteId }}>实时运行</PanelLink>}
      />
      <div className="grid grid-cols-[220px_minmax(0,1fr)] items-center gap-6 px-5 py-4">
        <div className="flex flex-col gap-1 border-r border-divider pr-6">
          <span className="flex items-center gap-1.5 text-xs font-medium text-muted-foreground">
            实时 COP
            <InfoTip>冷站瞬时制冷量 ÷ 冷站总功率；任一读数过期时不计算</InfoTip>
          </span>
          <span className="text-[28px] leading-8 font-semibold tracking-[-0.03em] tabular-nums">
            {plant.plantCop === null ? <span className="text-muted-foreground">—</span> : formatDecimal(plant.plantCop, 2)}
          </span>
          <span className="text-xs text-muted-foreground">
            {plant.plantCop === null ? '读数不全，暂不计算' : `1 kW 电 → ${formatDecimal(plant.plantCop, 2)} kW 冷`}
          </span>
        </div>
        <div className="flex min-w-0 flex-col gap-3">
          <ScaleBar label="冷站功率" reading={power} max={max} tone="power" />
          <ScaleBar label="瞬时制冷量" reading={cooling} max={max} tone="cooling" />
          <div className="flex gap-4 pl-[96px] text-xs text-muted-foreground">
            <span>冷冻水供水 <ReadingValue reading={plantReading(plant, 'btu_meter.supply_water_temperature')} className="font-medium text-foreground" /></span>
            <span className="text-border">|</span>
            <span>室内温度 <ReadingValue reading={plantReading(plant, 'zone.temperature')} className="font-medium text-foreground" /></span>
          </div>
        </div>
      </div>
      <section aria-label="主要设备" className="flex min-h-11 flex-wrap items-center gap-x-5 gap-y-2 border-t border-divider px-5 py-2.5 text-xs">
        {registryState === 'pending' ? <Skeleton className="h-4 w-72" /> : registryState === 'error' ? (
          <span className="text-muted-foreground">设备清单暂不可用</span>
        ) : (
          <>
            {groups.map((group) => (
              <span key={group.category} className="flex items-center gap-2 text-muted-foreground">
                {PLANT_CATEGORY_LABELS[group.category]}
                <span className="flex items-center gap-[3px]">{group.devices.map((device) => <DeviceDot key={device.deviceId} device={device} />)}</span>
              </span>
            ))}
            <span className="ml-auto flex items-center gap-3">
              {issues.length > 0 ? (
                <span className="rounded-md bg-destructive-soft px-1.5 leading-5 font-medium text-destructive-text">{issues.join(' · ')}</span>
              ) : null}
              <span className="text-muted-foreground">
                主要设备 <b className="font-semibold text-foreground tabular-nums">{status.running} / {status.total}</b> 台运行
              </span>
              <span className="text-muted-foreground">
                <b className="font-semibold text-foreground tabular-nums">{plant.onlineCount} / {plant.devices.length}</b> 在线
              </span>
            </span>
          </>
        )}
      </section>
    </Panel>
  );
}
