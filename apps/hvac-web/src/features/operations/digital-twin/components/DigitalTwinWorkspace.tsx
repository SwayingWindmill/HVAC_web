import { useMemo, useRef } from 'react';
import { useQuery } from '@tanstack/react-query';
import { getRouteApi } from '@tanstack/react-router';
import { Gauge, Snowflake, Zap } from 'lucide-react';
import { StateCard } from '@/blocks/state/state-card';
import { Skeleton } from '@/components/ui/skeleton';
import { activeAlarmsQuery } from '@/features/alarms/alarm-api';
import { useTodayAgainstYesterday } from '@/features/energy-analysis/consumption/query';
import { openWorkOrdersQuery } from '@/features/work-orders/work-order-queries';
import { formatDecimal } from '@/lib/operator-format';
import { cn } from '@/lib/utils';
import { copSeries, COOLING_KEY, hourAgo, POWER_KEY } from '../../realtime/plant-trend';
import { plantReading, type PlantReading } from '../../realtime/plant-model';
import { usePlantHistory } from '../../realtime/use-plant-history';
import { useRealtimePlant } from '../../realtime/use-realtime-plant';
import { BUILDING_PLANS, buildingTerminals } from '../twin-building';
import { buildTwinPlant, type TwinEquipment } from '../twin-model';
import { useScenePalette } from '../use-scene-palette';
import {
  BuildingCard, DeviceCard, FLOATING, KIND_ICONS, LOOP_ICONS, present, SiteCard, TwinKpis, TwinList, TwinLoop, TwinToolbar,
  type Attention, type KpiFigure, type ListTab, type LoopNode,
} from './TwinCorners';
import { TwinScene, type CameraView, type TwinCamera } from './TwinScene';

const pageRoute = getRouteApi('/_app/_site/operations/digital-twin');

const fixed = (reading: PlantReading | null, digits: number) => {
  const value = present(reading);
  return value && value.numeric !== null ? formatDecimal(value.numeric, digits) : null;
};

/**
 * The site's digital twin: the scene fills the page and the four corners carry every fact
 * (docs/specs/digital-twin.md 界面). Selecting a machine — in the scene or in the list — sets
 * ?inspect, frames it with brackets and turns the context card to it.
 */
export function DigitalTwinWorkspace() {
  const { site, plant, registry } = useRealtimePlant();
  const search = pageRoute.useSearch();
  const navigate = pageRoute.useNavigate();
  const palette = useScenePalette();
  const camera = useRef<TwinCamera>(null);
  const assets = registry.data?.assetModel.assets;
  const twin = useMemo(() => (assets ? buildTwinPlant(assets, plant) : null), [assets, plant]);
  const plan = BUILDING_PLANS[site.code] ?? null;
  const terminals = useMemo(() => (plan ? buildingTerminals(plan) : []), [plan]);
  const view: CameraView = search.view ?? 'overview';
  const cut = plan && search.floor && search.floor <= plan.floors.length ? search.floor : null;
  const all = twin ? [...twin.chillers, ...twin.towers, ...twin.chilledPumps, ...twin.coolingPumps, ...terminals.map((terminal) => terminal.equipment)] : [];
  const selected = search.inspect ? all.find((item) => item.device?.deviceId === search.inspect || item.assetId === search.inspect) ?? null : null;

  const setSearch = (patch: Partial<typeof search>) => void navigate({ search: (previous) => ({ ...previous, ...patch }), replace: true });
  const select = (item: TwinEquipment | null) => setSearch({ inspect: item ? item.device?.deviceId ?? item.assetId : undefined });

  // Figures: now, against an hour ago.
  const history = usePlantHistory(plant, [POWER_KEY, COOLING_KEY], 2);
  const powerSeries = history.series.get(POWER_KEY) ?? [];
  const coolingSeries = history.series.get(COOLING_KEY) ?? [];
  const powerAgo = hourAgo(powerSeries.map((point) => ({ periodStart: point.periodStart, value: point.average })));
  const coolingAgo = hourAgo(coolingSeries.map((point) => ({ periodStart: point.periodStart, value: point.average })));
  const copAgo = hourAgo(copSeries(powerSeries, coolingSeries));
  const copChange = plant.plantCop !== null && copAgo !== null ? plant.plantCop - copAgo : null;
  const figures: KpiFigure[] = [
    { icon: Zap, label: '冷站功率', value: fixed(plant.totalPower, 1), unit: 'kW', caption: powerAgo === null ? '空调总电表' : `1 小时前 ${formatDecimal(powerAgo, 1)}` },
    { icon: Snowflake, label: '瞬时制冷量', value: fixed(plant.coolingCapacity, 0), unit: 'kW', caption: coolingAgo === null ? '冷量计' : `1 小时前 ${formatDecimal(coolingAgo, 0)}` },
    {
      icon: Gauge,
      label: '冷站 COP',
      value: plant.plantCop === null ? null : formatDecimal(plant.plantCop, 2),
      caption: copChange === null ? '制冷量 ÷ 功率' : (
        <><span className={cn(copChange >= 0 ? 'text-success-text' : 'text-destructive-text')}>{copChange >= 0 ? '↑' : '↓'} {formatDecimal(Math.abs(copChange), 2)}</span> 较 1 小时前</>
      ),
    },
  ];

  // The site card: today's energy, outdoor conditions, machine groups, what waits on someone.
  const comparison = useTodayAgainstYesterday();
  const today = comparison.data?.today;
  const alarms = useQuery(activeAlarmsQuery(site.id));
  const workOrders = useQuery(openWorkOrdersQuery(site.id));
  const activeAlarms = alarms.data?.items ?? [];
  const openOrders = workOrders.data ?? [];
  const attention: Attention | null = activeAlarms.length + openOrders.length === 0 ? null : {
    count: activeAlarms.length + openOrders.length,
    title: activeAlarms[0]?.title ?? openOrders[0]?.title ?? '',
    to: activeAlarms.length > 0 ? '/operations/alarms' : '/operations/work-center',
  };
  const groups = twin ? [
    { kind: 'chiller' as const, label: '冷水机组', items: twin.chillers },
    { kind: 'chilledPump' as const, label: '冷冻泵 · 冷却泵', items: [...twin.chilledPumps, ...twin.coolingPumps] },
    { kind: 'tower' as const, label: '冷却塔', items: twin.towers },
  ].filter((group) => group.items.length > 0) : [];

  const locationOf = (item: TwinEquipment) => {
    const terminal = terminals.find((candidate) => candidate.equipment.assetId === item.assetId);
    return terminal ? `${terminal.level}F · ${terminal.room.name}` : '空调机房';
  };

  // The loop: pump → chiller → supply → terminal side → return.
  const supply = present(twin?.supply);
  const back = present(twin?.return);
  const chillers = twin?.chillers ?? [];
  const pumps = twin?.chilledPumps ?? [];
  const runningOf = (items: readonly TwinEquipment[]) => items.filter((item) => item.state === 'running').length;
  const pumpConnected = pumps.some((item) => item.state !== 'unconnected');
  const selectedChiller = selected?.kind === 'chiller' ? selected : null;
  const selectedTerminal = selected && (selected.kind === 'ahu' || selected.kind === 'fcu' || selected.kind === 'vav') ? selected : null;
  const terminalConnected = terminals.some((terminal) => terminal.equipment.state !== 'unconnected');
  const nodes: LoopNode[] = [
    { icon: LOOP_ICONS.pump, label: '冷冻泵', caption: pumpConnected ? `${runningOf(pumps)} / ${pumps.length} 运行` : '未接入', state: pumpConnected && runningOf(pumps) > 0 ? 'done' : 'idle' },
    selectedChiller ? {
      icon: LOOP_ICONS.chiller,
      label: selectedChiller.name,
      caption: `进 ${selectedChiller.values['chiller.entering_chilled_water_temperature']?.display ?? '—'} → 出 ${selectedChiller.values['chiller.leaving_chilled_water_temperature']?.display ?? '—'}`,
      state: 'current',
    } : { icon: LOOP_ICONS.chiller, label: '冷水机组', caption: `${runningOf(chillers)} / ${chillers.length} 运行`, state: runningOf(chillers) > 0 ? 'done' : 'idle' },
    { icon: LOOP_ICONS.supply, label: '供水', caption: supply ? `${supply.display} ℃` : '—', state: supply ? 'done' : 'idle' },
    {
      icon: cut ? LOOP_ICONS.fcu : LOOP_ICONS.terminal,
      label: selectedTerminal ? selectedTerminal.name : cut ? `${cut}F 末端` : '楼宇末端',
      caption: terminalConnected ? '已接入' : '未接入',
      state: selectedTerminal ? 'current' : terminalConnected ? 'done' : 'idle',
    },
    { icon: LOOP_ICONS.return, label: '回水', caption: back ? `${back.display} ℃` : '—', state: back ? 'done' : 'idle' },
  ];
  const difference = supply?.numeric != null && back?.numeric != null ? formatDecimal(back.numeric - supply.numeric, 1) : null;

  // The list: the plant's machines, or the building's terminal units (one floor when cut).
  const loadOf = (item: TwinEquipment) => {
    const load = item.device ? present(item.device.reading('chiller.compressor_load')) : null;
    return load?.numeric ?? null;
  };
  const captionOf = (item: TwinEquipment) => {
    if (item.state === 'unconnected') return '没有数据来源';
    const power = Object.entries(item.values).find(([key]) => key.endsWith('.power'))?.[1];
    return power ? `${power.display} ${power.unit ?? ''}`.trim() : locationOf(item);
  };
  const rowsOf = (items: readonly TwinEquipment[], caption = captionOf) => items.map((item) => ({ key: item.assetId, item, title: item.name, caption: caption(item), icon: KIND_ICONS[item.kind], load: loadOf(item) }));
  const countOf = (items: readonly TwinEquipment[]) => (items.some((item) => item.state !== 'unconnected') ? `${runningOf(items)}/${items.length}` : `${items.length}`);
  const terminalsOf = (kind: 'fcu' | 'vav' | 'ahu') => terminals
    .filter((terminal) => terminal.equipment.kind === kind && (cut === null || terminal.level === cut))
    .map((terminal) => terminal.equipment);
  const plantPumps = twin ? [...twin.chilledPumps, ...twin.coolingPumps] : [];
  const tabs: ListTab[] = (view === 'terminal' && plan
    ? (['fcu', 'vav', 'ahu'] as const).map((kind) => ({
      key: kind,
      label: { fcu: '风机盘管', vav: '变风量末端', ahu: '空调箱' }[kind],
      count: countOf(terminalsOf(kind)),
      rows: rowsOf(terminalsOf(kind), locationOf),
    }))
    : twin ? [
      { key: 'chiller', label: '机组', count: countOf(twin.chillers), rows: rowsOf(twin.chillers) },
      { key: 'pump', label: '水泵', count: countOf(plantPumps), rows: rowsOf(plantPumps) },
      { key: 'tower', label: '冷却塔', count: countOf(twin.towers), rows: rowsOf(twin.towers) },
    ] : []).filter((tab) => tab.rows.length > 0);

  const card = selected ? (
    <DeviceCard item={selected} location={locationOf(selected)} siteId={site.id} timeZone={site.timezone} onClose={() => select(null)} />
  ) : view === 'terminal' && plan ? (
    <BuildingCard plan={plan} cut={cut} terminals={terminals.map((terminal) => ({ level: terminal.level, equipment: terminal.equipment }))} onFloor={(level) => setSearch({ floor: level ?? undefined })} />
  ) : (
    <SiteCard
      siteId={site.id}
      siteName={site.displayName}
      sampledAt={plant.latestSampleAt}
      timeZone={site.timezone}
      today={{
        electricity: today?.electricityKWh == null ? null : formatDecimal(today.electricityKWh, 0),
        cooling: today?.coolingKWh == null ? null : formatDecimal(today.coolingKWh, 0),
        cop: today?.cop == null ? null : formatDecimal(today.cop, 2),
      }}
      outdoor={{ dry: fixed(plantReading(plant, 'weather.ambient_dry_bulb_temperature'), 1), wet: fixed(plantReading(plant, 'weather.ambient_wet_bulb_temperature'), 1) }}
      groups={groups}
      attention={attention}
    />
  );

  const onCamera = (action: 'in' | 'out' | 'left' | 'right' | 'home') => {
    if (action === 'in') camera.current?.zoom(1);
    if (action === 'out') camera.current?.zoom(-1);
    if (action === 'left') camera.current?.rotate(-1);
    if (action === 'right') camera.current?.rotate(1);
    if (action === 'home') {
      if (selected) select(null);
      camera.current?.reset();
    }
  };

  return (
    <div className="relative h-[calc(100svh-var(--header-height))] min-h-[640px] w-full overflow-hidden bg-background">
      <h1 className="sr-only">数字孪生 · {site.displayName}</h1>
      {twin ? (
        <TwinScene
          plant={twin}
          plan={plan}
          palette={palette}
          view={view}
          cut={cut}
          shell={search.shell !== 'off'}
          emphasis={search.pipes === 'on'}
          selectedId={selected?.assetId ?? null}
          onSelect={select}
          camera={camera}
        />
      ) : registry.isError ? (
        <StateCard className="h-full justify-center" title={<h2 className="text-title font-semibold">设备登记读取失败</h2>} description="刷新页面重试；仍然失败时检查网络或稍后再来。" />
      ) : (
        <Skeleton className="h-full w-full rounded-none" />
      )}

      {twin ? (
        <div className="pointer-events-none absolute inset-0">
          <div className="absolute top-4 left-4"><TwinKpis figures={figures} /></div>
          <div className="absolute top-4 right-4 flex items-start gap-2.5">
            <TwinToolbar
              view={view}
              views={plan ? ['overview', 'plant', 'terminal'] : ['overview', 'plant']}
              shell={plan ? search.shell !== 'off' : null}
              emphasis={search.pipes === 'on'}
              onCamera={onCamera}
              onView={(next) => setSearch({ view: next, inspect: undefined, floor: next === 'terminal' ? search.floor : undefined })}
              onShell={(on) => setSearch({ shell: on ? undefined : 'off' })}
              onEmphasis={(on) => setSearch({ pipes: on ? 'on' : undefined })}
            />
            <aside aria-label={selected ? selected.name : '冷站'} className={cn(FLOATING, 'pointer-events-auto w-[300px] overflow-hidden')}>{card}</aside>
          </div>
          <div className="absolute bottom-4 left-4">
            <TwinLoop title={cut ? `冷冻水到 ${cut}F` : '冷冻水环路'} flow={fixed(twin.flow, 0)} nodes={nodes} difference={difference} siteId={site.id} />
          </div>
          {tabs.length ? (
            <div className="absolute right-4 bottom-4">
              <TwinList key={view} tabs={tabs} scope={view === 'terminal' && plan ? (cut ? `${cut}F` : plan.name) : '空调机房'} selectedId={selected?.assetId ?? null} onSelect={select} />
            </div>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}
