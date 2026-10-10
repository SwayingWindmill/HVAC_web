import { Link } from '@tanstack/react-router';
import { Building2 } from 'lucide-react';
import type { CSSProperties } from 'react';
import { StatusBadge } from '@/components/common/StatusBadge';
import { cn } from '@/lib/utils';
import { plantReading, type PlantCategory, type PlantDevice, type PlantReading, type PlantView } from '../plant-model';
import { CATEGORY_ICONS, deviceStatus, KEY_READINGS, POWER_KEY, ReadingValue } from '../device-presentation';

// The plant as operators draw it: the cooling-water loop on the left, the chilled-water loop
// on the right, the chillers between them. Supply runs along the top, return along the bottom.

type Loop = 'cooling' | 'chilled';

const LOOP_COLOR: Readonly<Record<Loop, string>> = { cooling: 'var(--chart-2)', chilled: 'var(--chart-1)' };

// Inside a node the equipment is already named, so readings use short labels.
const NODE_LABELS: Readonly<Record<string, string>> = {
  power: '功率',
  flow_rate: '流量',
  frequency: '频率',
  compressor_load: '负载率',
  leaving_chilled_water_temperature: '出水温度',
  chilled_water_temperature_setpoint: '出水设定',
  fan_speed: '风机转速',
  leaving_water_temperature: '出塔水温',
  approach_temperature: '逼近温度',
  cop: '主机 COP',
};

function nodeReadings(device: PlantDevice) {
  const powerKey = POWER_KEY[device.category];
  return [powerKey, ...KEY_READINGS[device.category].slice(0, 2)]
    .filter((key): key is string => Boolean(key))
    .flatMap((key) => {
      const reading = device.reading(key);
      const label = NODE_LABELS[key.slice(key.indexOf('.') + 1)] ?? reading?.label;
      return reading && label ? [{ ...reading, label }] : [];
    });
}

function DeviceNode({ device, siteId }: { readonly device: PlantDevice; readonly siteId: string }) {
  const status = deviceStatus(device);
  const Icon = CATEGORY_ICONS[device.category];
  return (
    <Link
      to="/operations/systems-devices"
      search={{ site: siteId, inspect: device.deviceId }}
      className="block rounded-lg border bg-card p-3 shadow-xs transition-colors hover:border-foreground/20 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
    >
      <span className="flex min-w-0 items-center gap-1.5 text-sm font-medium">
        {Icon ? <Icon className="size-3.5 shrink-0 text-muted-foreground" aria-hidden="true" /> : null}
        <span className="truncate" title={device.name}>{device.name}</span>
      </span>
      <span className="mt-1.5 block"><StatusBadge tone={status.tone} label={status.label} /></span>
      <dl className="mt-2.5 space-y-1 text-xs">
        {nodeReadings(device).map((reading) => (
          <div key={reading.label} className="flex items-baseline justify-between gap-3">
            <dt className="whitespace-nowrap text-muted-foreground">{reading.label}</dt>
            <dd className="whitespace-nowrap text-sm font-medium"><ReadingValue reading={reading} /></dd>
          </div>
        ))}
      </dl>
    </Link>
  );
}

function Column({ title, devices, siteId }: { readonly title: string; readonly devices: readonly PlantDevice[]; readonly siteId: string }) {
  return (
    <div className="flex min-w-0 flex-col gap-2 self-center">
      <span className="text-xs font-medium text-muted-foreground">{title}</span>
      {devices.length > 0
        ? devices.map((device) => <DeviceNode key={device.deviceId} device={device} siteId={siteId} />)
        : <span className="rounded-lg border border-dashed px-3 py-6 text-center text-xs text-muted-foreground">未登记</span>}
    </div>
  );
}

/** One pipe run between two columns: supply on top flowing right, return below flowing left. */
function PipeLink({ loop, flowing, supply, ret, supplyLabel, returnLabel }: {
  readonly loop: Loop;
  readonly flowing: boolean;
  readonly supply: PlantReading | null;
  readonly supplyLabel: string;
  /** A return reading only where it is measured; elsewhere the run is drawn without a value. */
  readonly ret?: PlantReading | null;
  readonly returnLabel?: string;
}) {
  const style = { '--pipe': LOOP_COLOR[loop] } as CSSProperties;
  const line = (direction: 'right' | 'left', faded: boolean) => (
    <svg className="h-2 w-full overflow-visible" viewBox="0 0 100 8" preserveAspectRatio="none" aria-hidden="true">
      <line x1="0" y1="4" x2="100" y2="4" stroke="var(--pipe)" strokeOpacity={faded ? 0.45 : 1} strokeWidth="3" vectorEffect="non-scaling-stroke" />
      {flowing ? (
        <line
          x1="0" y1="4" x2="100" y2="4"
          stroke="var(--background)" strokeOpacity="0.7" strokeWidth="1.5" strokeDasharray="4 8" vectorEffect="non-scaling-stroke"
          className="motion-safe:animate-[pipe-flow_1.4s_linear_infinite]"
          style={direction === 'left' ? { animationDirection: 'reverse' } : undefined}
        />
      ) : null}
    </svg>
  );
  return (
    <div className="flex h-28 min-w-20 flex-col justify-between self-center text-xs" style={style}>
      <div className="space-y-1 text-center">
        <span className="block text-muted-foreground">{supplyLabel}</span>
        <ReadingValue reading={supply} className="font-medium text-foreground" />
        {line('right', false)}
      </div>
      <div className="space-y-1 text-center">
        {line('left', true)}
        {returnLabel ? (
          <>
            <ReadingValue reading={ret ?? null} className="font-medium text-foreground" />
            <span className="block text-muted-foreground">{returnLabel}</span>
          </>
        ) : <span className="block h-8" aria-hidden="true" />}
      </div>
    </div>
  );
}

function LoadNode({ plant }: { readonly plant: PlantView }) {
  const rows: [string, string][] = [
    ['制冷量', 'btu_meter.instant_cooling_capacity'],
    ['供回水温差', 'btu_meter.temperature_difference'],
    ['冷冻水流量', 'btu_meter.flow_rate'],
  ];
  return (
    <div className="flex min-w-0 flex-col gap-2 self-center">
      <span className="text-xs font-medium text-muted-foreground">末端</span>
      <div className="rounded-lg border border-dashed bg-muted/30 p-3">
        <span className="flex items-center gap-1.5 text-sm font-medium">
          <Building2 className="size-3.5 text-muted-foreground" aria-hidden="true" />
          建筑负荷
        </span>
        <dl className="mt-2.5 space-y-1 text-xs">
          {rows.map(([label, key]) => (
            <div key={key} className="flex items-baseline justify-between gap-3">
              <dt className="whitespace-nowrap text-muted-foreground">{label}</dt>
              <dd className="whitespace-nowrap text-sm font-medium"><ReadingValue reading={plantReading(plant, key)} /></dd>
            </div>
          ))}
        </dl>
      </div>
    </div>
  );
}

export function PlantDiagram({ plant, siteId }: { readonly plant: PlantView; readonly siteId: string }) {
  const devices = (category: PlantCategory) => plant.groups.find((group) => group.category === category)?.devices ?? [];
  const running = (category: PlantCategory) => devices(category).some((device) => device.connection !== 'OFFLINE' && device.runState === 'RUNNING');
  const reading = (key: string) => plantReading(plant, key);
  return (
    <figure aria-label="冷站系统图" className="overflow-x-auto">
      <div className={cn('grid min-w-[880px] items-center gap-x-1 py-2', 'grid-cols-[minmax(0,1fr)_minmax(5rem,0.55fr)_minmax(0,1fr)_minmax(5rem,0.55fr)_minmax(0,1.1fr)_minmax(5rem,0.55fr)_minmax(0,1fr)_minmax(5rem,0.55fr)_minmax(0,1fr)]')}>
        <Column title="冷却塔" devices={devices('COOLING_TOWER')} siteId={siteId} />
        <PipeLink
          loop="cooling" flowing={running('COOLING_WATER_PUMP')}
          supply={reading('cooling_tower.leaving_water_temperature')} supplyLabel="出塔"
          ret={reading('cooling_tower.entering_water_temperature')} returnLabel="回塔"
        />
        <Column title="冷却水泵" devices={devices('COOLING_WATER_PUMP')} siteId={siteId} />
        <PipeLink
          loop="cooling" flowing={running('COOLING_WATER_PUMP')}
          supply={reading('chiller.entering_cooling_water_temperature')} supplyLabel="进冷凝器"
        />
        <Column title="冷水机组" devices={devices('CHILLER')} siteId={siteId} />
        <PipeLink
          loop="chilled" flowing={running('CHILLED_WATER_PUMP')}
          supply={reading('btu_meter.supply_water_temperature')} supplyLabel="供水"
          ret={reading('btu_meter.return_water_temperature')} returnLabel="回水"
        />
        <Column title="冷冻水泵" devices={devices('CHILLED_WATER_PUMP')} siteId={siteId} />
        <PipeLink
          loop="chilled" flowing={running('CHILLED_WATER_PUMP')}
          supply={reading('chwp.flow_rate')} supplyLabel="流量"
        />
        <LoadNode plant={plant} />
      </div>
      <figcaption className="mt-3 flex flex-wrap gap-x-5 gap-y-1 text-xs text-muted-foreground">
        <span className="flex items-center gap-1.5"><span className="h-0.5 w-5 rounded" style={{ background: LOOP_COLOR.cooling }} />冷却水</span>
        <span className="flex items-center gap-1.5"><span className="h-0.5 w-5 rounded" style={{ background: LOOP_COLOR.chilled }} />冷冻水</span>
        <span>上方为供水，下方为回水；水泵运行时管道显示流动</span>
      </figcaption>
    </figure>
  );
}
