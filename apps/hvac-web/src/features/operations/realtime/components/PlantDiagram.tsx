import { Link } from '@tanstack/react-router';
import { Building2 } from 'lucide-react';
import type { CSSProperties } from 'react';
import { StatusBadge } from '@/components/common/StatusBadge';
import {
  isRunning,
  plantReading,
  soleReading,
  type PlantCategory,
  type PlantDevice,
  type PlantReading,
  type PlantView,
} from '../plant-model';
import { CATEGORY_ICONS, deviceStatus, ReadingValue } from '../device-presentation';

// The plant drawn by equipment role, as operators sketch it: the cooling-water loop on the left,
// the chilled-water loop on the right, chillers between them; supply on top, return below.
// It is a schematic by category, not the as-built piping.

type Loop = 'cooling' | 'chilled';

const LOOP_COLOR: Readonly<Record<Loop, string>> = { cooling: 'var(--chart-2)', chilled: 'var(--chart-1)' };

// What a node shows about its own equipment. Probe temperatures sit on the pipes instead,
// except when a category has several devices and each must show its own.
const NODE_READINGS: Readonly<Partial<Record<PlantCategory, readonly (readonly [string, string])[]>>> = {
  COOLING_TOWER: [['cooling_tower.power', '功率'], ['cooling_tower.fan_speed', '风机转速'], ['cooling_tower.approach_temperature', '逼近温度']],
  COOLING_WATER_PUMP: [['cwp.power', '功率'], ['cwp.frequency', '频率'], ['cwp.flow_rate', '流量']],
  CHILLER: [['chiller.power', '功率'], ['chiller.compressor_load', '负载率'], ['chiller.cop', '主机 COP']],
  CHILLED_WATER_PUMP: [['chwp.power', '功率'], ['chwp.frequency', '频率'], ['chwp.flow_rate', '流量']],
};
const PROBE_READINGS: Readonly<Partial<Record<PlantCategory, readonly (readonly [string, string])[]>>> = {
  COOLING_TOWER: [['cooling_tower.leaving_water_temperature', '出塔水温']],
  CHILLER: [['chiller.leaving_chilled_water_temperature', '出水温度'], ['chiller.entering_chilled_water_temperature', '回水温度']],
};

function DeviceNode({ device, shared, siteId }: { readonly device: PlantDevice; readonly shared: boolean; readonly siteId: string }) {
  const status = deviceStatus(device);
  const Icon = CATEGORY_ICONS[device.category];
  const rows = [...(NODE_READINGS[device.category] ?? []), ...(shared ? PROBE_READINGS[device.category] ?? [] : [])]
    .flatMap(([key, label]) => {
      const reading = device.reading(key);
      return reading ? [{ key, label, reading }] : [];
    });
  return (
    <div className="relative rounded-lg border bg-card p-3 shadow-xs transition-colors focus-within:ring-2 focus-within:ring-ring hover:border-foreground/20">
      <span className="flex min-w-0 items-center gap-1.5 text-sm font-medium">
        {Icon ? <Icon className="size-3.5 shrink-0 text-muted-foreground" aria-hidden="true" /> : null}
        <Link
          to="/operations/systems-devices"
          search={{ site: siteId, inspect: device.deviceId }}
          className="truncate outline-none after:absolute after:inset-0 after:rounded-lg"
          title={device.name}
        >
          {device.name}
        </Link>
      </span>
      <span className="mt-1.5 block"><StatusBadge tone={status.tone} label={status.label} /></span>
      <dl className="mt-2.5 space-y-1 text-xs">
        {rows.map(({ key, label, reading }) => (
          <div key={key} className="flex items-baseline justify-between gap-3">
            <dt className="whitespace-nowrap text-muted-foreground">{label}</dt>
            <dd className="whitespace-nowrap text-sm font-medium"><ReadingValue reading={reading} /></dd>
          </div>
        ))}
      </dl>
    </div>
  );
}

function Column({ title, devices, siteId }: { readonly title: string; readonly devices: readonly PlantDevice[]; readonly siteId: string }) {
  return (
    <div className="flex min-w-0 flex-col gap-2 self-center">
      <span className="text-xs font-medium text-muted-foreground">{title}</span>
      {devices.length > 0
        ? devices.map((device) => <DeviceNode key={device.deviceId} device={device} shared={devices.length > 1} siteId={siteId} />)
        : <span className="rounded-lg border border-dashed px-3 py-6 text-center text-xs text-muted-foreground">未登记</span>}
    </div>
  );
}

function PipeLine({ flowing, direction, faded }: { readonly flowing: boolean; readonly direction: 'right' | 'left'; readonly faded: boolean }) {
  return (
    <svg className="h-2 w-full overflow-visible" viewBox="0 0 100 8" preserveAspectRatio="none" aria-hidden="true">
      <line x1="0" y1="4" x2="100" y2="4" stroke="var(--pipe)" strokeOpacity={faded ? 0.45 : 1} strokeWidth="3" vectorEffect="non-scaling-stroke" />
      {flowing ? (
        <line
          x1="0" y1="4" x2="100" y2="4"
          stroke="var(--background)" strokeOpacity="0.7" strokeWidth="1.5" strokeDasharray="4 8" vectorEffect="non-scaling-stroke"
          className="motion-safe:animate-pipe-flow"
          style={direction === 'left' ? { animationDirection: 'reverse' } : undefined}
        />
      ) : null}
    </svg>
  );
}

/** One pipe run between two columns: supply on top flowing right, return below flowing left. */
function PipeRun({ loop, flowing, supplyLabel, supply, returnLabel, ret }: {
  readonly loop: Loop;
  readonly flowing: boolean;
  readonly supplyLabel?: string;
  readonly supply?: PlantReading | null;
  /** A reading only where it is measured; elsewhere the run is drawn without a value. */
  readonly returnLabel?: string;
  readonly ret?: PlantReading | null;
}) {
  return (
    <div
      className="flex h-28 min-w-20 flex-col justify-between self-center text-xs"
      style={{ '--pipe': LOOP_COLOR[loop] } as CSSProperties}
      data-flowing={flowing}
    >
      <div className="space-y-1 text-center">
        {supplyLabel ? (
          <>
            <span className="block text-muted-foreground">{supplyLabel}</span>
            <ReadingValue reading={supply ?? null} className="font-medium" />
          </>
        ) : <span className="block h-8" aria-hidden="true" />}
        <PipeLine flowing={flowing} direction="right" faded={false} />
      </div>
      <div className="space-y-1 text-center">
        <PipeLine flowing={flowing} direction="left" faded />
        {returnLabel ? (
          <>
            <ReadingValue reading={ret ?? null} className="font-medium" />
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

const GRID = 'grid min-w-[880px] grid-cols-[minmax(0,1fr)_minmax(5rem,0.55fr)_minmax(0,1fr)_minmax(5rem,0.55fr)_minmax(0,1.1fr)_minmax(5rem,0.55fr)_minmax(0,1fr)_minmax(5rem,0.55fr)_minmax(0,1fr)] items-center gap-x-1 py-2';

export function PlantDiagram({ plant, siteId }: { readonly plant: PlantView; readonly siteId: string }) {
  const devices = (category: PlantCategory) => plant.groups.find((group) => group.category === category)?.devices ?? [];
  const pumping = (category: PlantCategory) => devices(category).some(isRunning);
  return (
    <figure aria-label="冷站系统图" className="overflow-x-auto">
      <div className={GRID}>
        <Column title="冷却塔" devices={devices('COOLING_TOWER')} siteId={siteId} />
        <PipeRun
          loop="cooling" flowing={pumping('COOLING_WATER_PUMP')}
          supplyLabel="出塔" supply={soleReading(plant, 'COOLING_TOWER', 'cooling_tower.leaving_water_temperature')}
          returnLabel="回塔" ret={soleReading(plant, 'COOLING_TOWER', 'cooling_tower.entering_water_temperature')}
        />
        <Column title="冷却水泵" devices={devices('COOLING_WATER_PUMP')} siteId={siteId} />
        <PipeRun
          loop="cooling" flowing={pumping('COOLING_WATER_PUMP')}
          supplyLabel="进冷凝器" supply={soleReading(plant, 'CHILLER', 'chiller.entering_cooling_water_temperature')}
        />
        <Column title="冷水机组" devices={devices('CHILLER')} siteId={siteId} />
        <PipeRun
          loop="chilled" flowing={pumping('CHILLED_WATER_PUMP')}
          supplyLabel="出水" supply={soleReading(plant, 'CHILLER', 'chiller.leaving_chilled_water_temperature')}
          returnLabel="回水" ret={soleReading(plant, 'CHILLER', 'chiller.entering_chilled_water_temperature')}
        />
        <Column title="冷冻水泵" devices={devices('CHILLED_WATER_PUMP')} siteId={siteId} />
        <PipeRun
          loop="chilled" flowing={pumping('CHILLED_WATER_PUMP')}
          supplyLabel="供水" supply={plantReading(plant, 'btu_meter.supply_water_temperature')}
          returnLabel="回水" ret={plantReading(plant, 'btu_meter.return_water_temperature')}
        />
        <LoadNode plant={plant} />
      </div>
      <figcaption className="mt-3 flex flex-wrap gap-x-5 gap-y-1 text-xs text-muted-foreground">
        <span className="flex items-center gap-1.5"><span className="h-0.5 w-5 rounded" style={{ background: LOOP_COLOR.cooling }} />冷却水</span>
        <span className="flex items-center gap-1.5"><span className="h-0.5 w-5 rounded" style={{ background: LOOP_COLOR.chilled }} />冷冻水</span>
        <span>上方供水、下方回水；水泵运行时管道显示流动。按设备类别示意，不代表实际管路。</span>
      </figcaption>
    </figure>
  );
}
