import { useMemo, useState } from 'react';
import { getRouteApi } from '@tanstack/react-router';
import { Building2, Waypoints, X } from 'lucide-react';
import { PageHeader } from '@/blocks/page-header';
import { Main } from '@/components/layout/Main';
import { Button } from '@/components/ui/button';
import { Separator } from '@/components/ui/separator';
import { Skeleton } from '@/components/ui/skeleton';
import { Toggle } from '@/components/ui/toggle';
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group';
import { formatDecimal } from '@/lib/operator-format';
import { DeviceInspector } from '../../systems-devices/components/DeviceInspector';
import { formatClock, LiveIndicator, ReadingMetric } from '../../realtime/device-presentation';
import { PLANT_CATEGORY_LABELS, plantReading, type PlantReading } from '../../realtime/plant-model';
import { useRealtimePlant } from '../../realtime/use-realtime-plant';
import { BUILDING_PLANS, buildingTerminals } from '../twin-building';
import { buildTwinPlant, TWIN_STATE_LABELS, type TwinEquipment, type TwinKind } from '../twin-model';
import { useScenePalette } from '../use-scene-palette';
import { TwinScene, type CameraView } from './TwinScene';

const pageRoute = getRouteApi('/_app/_site/operations/digital-twin');

const KIND_LABELS: Readonly<Record<TwinKind, string>> = {
  chiller: '冷水机组',
  tower: '冷却塔',
  chilledPump: '冷冻水泵',
  coolingPump: '冷却水泵',
  ahu: '空调箱',
  fcu: '风机盘管',
  vav: '变风量末端',
};

function Kpi({ label, children }: { readonly label: string; readonly children: React.ReactNode }) {
  return (
    <div className="flex min-w-[124px] flex-col gap-1 rounded-[10px] border bg-card/90 px-3.5 py-2.5 shadow-sm backdrop-blur">
      <span className="text-xs text-muted-foreground">{label}</span>
      <span className="text-xl font-semibold tabular-nums">{children}</span>
    </div>
  );
}

function Legend({ chilled, cooling, running, inactive }: { readonly chilled: string; readonly cooling: string; readonly running: string; readonly inactive: string }) {
  const item = (color: string, label: string, round = false) => (
    <span className="flex items-center gap-1.5">
      <span className={round ? 'size-2 rounded-full' : 'h-1 w-4 rounded-full'} style={{ background: color }} />
      {label}
    </span>
  );
  return (
    <div className="flex items-center gap-3 rounded-[10px] border bg-card/90 px-3 py-2 text-xs text-muted-foreground shadow-sm backdrop-blur">
      {item(chilled, '冷冻水')}
      {item(cooling, '冷却水')}
      {item(running, '运行', true)}
      {item(inactive, '停止 / 未接入', true)}
    </div>
  );
}

/** An asset no device reports for: its name and that it is not integrated. */
function UnconnectedCard({ item, onClose }: { readonly item: TwinEquipment; readonly onClose: () => void }) {
  return (
    <div className="absolute top-4 right-4 w-72 rounded-[10px] border bg-card p-4 shadow-md">
      <div className="flex items-start justify-between gap-2">
        <div>
          <div className="text-sm font-semibold">{item.name}</div>
          <div className="text-xs text-muted-foreground">{KIND_LABELS[item.kind]}</div>
        </div>
        <Button variant="ghost" size="icon" className="-mt-1 -mr-2 size-7" aria-label="关闭" onClick={onClose}><X /></Button>
      </div>
      <p className="mt-3 text-sm text-muted-foreground">{TWIN_STATE_LABELS[item.state]}：这台设备还没有数据来源，接入电表或运行信号后会显示实时状态。</p>
    </div>
  );
}

export function DigitalTwinWorkspace() {
  const { site, plant, mode, registry } = useRealtimePlant();
  const search = pageRoute.useSearch();
  const navigate = pageRoute.useNavigate();
  const palette = useScenePalette();
  const [selectedAssetId, setSelectedAssetId] = useState<string | null>(null);
  const assets = registry.data?.assetModel.assets;
  const twin = useMemo(() => (assets ? buildTwinPlant(assets, plant) : null), [assets, plant]);
  const plan = BUILDING_PLANS[site.code] ?? null;
  const terminals = useMemo(() => (plan ? buildingTerminals(plan).map((terminal) => terminal.equipment) : []), [plan]);
  const view: CameraView = search.view ?? 'overview';
  const cut = plan && search.floor && search.floor <= plan.floors.length ? search.floor : null;
  const all = twin ? [...twin.chillers, ...twin.towers, ...twin.chilledPumps, ...twin.coolingPumps, ...terminals] : [];
  const inspected = search.inspect ? plant.devices.find((device) => device.deviceId === search.inspect) : undefined;
  const selected = all.find((item) => item.assetId === selectedAssetId) ?? null;

  const setSearch = (patch: Partial<typeof search>) => void navigate({ search: (previous) => ({ ...previous, ...patch }), replace: true });
  const select = (item: TwinEquipment | null) => {
    setSelectedAssetId(item?.assetId ?? null);
    setSearch({ inspect: item?.device?.deviceId });
  };
  const temperature = (value: PlantReading | null) => <ReadingMetric reading={value} digits={1} unit="℃" />;
  const views: readonly (readonly [CameraView, string])[] = plan ? [['overview', '全景'], ['plant', '冷站'], ['terminal', '末端']] : [['overview', '全景'], ['plant', '冷站']];

  return (
    <Main fluid>
      <PageHeader
        title="数字孪生"
        description={plan ? `${site.displayName} · ${plan.name}` : site.displayName}
        meta={<LiveIndicator mode={mode} />}
        actions={plant.latestSampleAt ? <span className="text-xs text-muted-foreground">数据时间 <span className="font-mono">{formatClock(plant.latestSampleAt, site.timezone)}</span></span> : null}
      />
      <div className="relative h-[calc(100svh-var(--header-height)-120px)] min-h-[600px] overflow-hidden rounded-[10px] border bg-[radial-gradient(ellipse_at_50%_30%,var(--card),var(--background))]">
        {twin ? (
          <TwinScene
            plant={twin}
            plan={plan}
            palette={palette}
            view={view}
            cut={cut}
            shell={search.shell !== 'off'}
            emphasis={search.pipes === 'on'}
            selectedId={selectedAssetId}
            onSelect={select}
          />
        ) : registry.isError ? (
          <div className="grid h-full place-items-center text-sm text-muted-foreground">设备登记读取失败，请刷新重试</div>
        ) : (
          <Skeleton className="h-full w-full rounded-none" />
        )}

        <div className="pointer-events-none absolute top-4 left-4 flex flex-wrap gap-2 [&>*]:pointer-events-auto">
          <Kpi label="冷站总功率"><ReadingMetric reading={plant.totalPower} digits={1} unit="kW" /></Kpi>
          <Kpi label="制冷量"><ReadingMetric reading={plant.coolingCapacity} digits={0} unit="kW" /></Kpi>
          <Kpi label="COP">{plant.plantCop === null ? <span className="text-muted-foreground">—</span> : formatDecimal(plant.plantCop, 2)}</Kpi>
          <Kpi label="室外温度">{temperature(plantReading(plant, 'weather.ambient_dry_bulb_temperature'))}</Kpi>
          <Kpi label="室外湿球">{temperature(plantReading(plant, 'weather.ambient_wet_bulb_temperature'))}</Kpi>
        </div>

        <div className="absolute bottom-4 left-4">
          <Legend chilled={palette.chilled} cooling={palette.cooling} running={palette.running} inactive={palette.inactive} />
        </div>
        <div className="absolute right-4 bottom-4 flex items-center gap-1 rounded-[10px] border bg-card/90 p-1 shadow-sm backdrop-blur">
          <ToggleGroup type="single" size="sm" value={view} onValueChange={(value) => { if (value) setSearch({ view: value as CameraView }); }} aria-label="视角">
            {views.map(([value, label]) => <ToggleGroupItem key={value} value={value}>{label}</ToggleGroupItem>)}
          </ToggleGroup>
          {plan ? (
            <>
              <Separator orientation="vertical" className="mx-1 h-5" />
              <ToggleGroup
                type="single"
                size="sm"
                value={cut ? String(cut) : 'all'}
                onValueChange={(value) => { if (value) setSearch(value === 'all' ? { floor: undefined } : { floor: Number(value), view: 'terminal' }); }}
                aria-label="楼层"
              >
                <ToggleGroupItem value="all">全部楼层</ToggleGroupItem>
                {[...plan.floors].reverse().map((floor) => <ToggleGroupItem key={floor.level} value={String(floor.level)}>{floor.level}F</ToggleGroupItem>)}
              </ToggleGroup>
              <Separator orientation="vertical" className="mx-1 h-5" />
              <Toggle size="sm" pressed={search.shell !== 'off'} onPressedChange={(pressed) => setSearch({ shell: pressed ? undefined : 'off' })} aria-label="外立面">
                <Building2 />外立面
              </Toggle>
            </>
          ) : null}
          <Toggle size="sm" pressed={search.pipes === 'on'} onPressedChange={(pressed) => setSearch({ pipes: pressed ? 'on' : undefined })} aria-label="突出管路">
            <Waypoints />管路
          </Toggle>
        </div>

        {selected && !selected.device ? <UnconnectedCard item={selected} onClose={() => select(null)} /> : null}
      </div>

      <DeviceInspector
        device={inspected}
        categoryLabel={inspected ? PLANT_CATEGORY_LABELS[inspected.category] : ''}
        onClose={() => select(null)}
      />
    </Main>
  );
}
