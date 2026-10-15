import { useMemo } from 'react';
import { getRouteApi } from '@tanstack/react-router';
import { Building2, PlugZap, Waypoints } from 'lucide-react';
import { PageHeader } from '@/blocks/page-header';
import { FactTile } from '@/blocks/fact-list';
import { Panel, PanelHeader, PanelLink } from '@/blocks/panel';
import { StateCard } from '@/blocks/state/state-card';
import { StatusBadge, statusDotClass } from '@/components/common/StatusBadge';
import { InfoTip } from '@/components/info-tip';
import { Main } from '@/components/layout/Main';
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '@/components/ui/sheet';
import { Skeleton } from '@/components/ui/skeleton';
import { Toggle } from '@/components/ui/toggle';
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group';
import { formatDecimal } from '@/lib/operator-format';
import { DeviceInspector } from '../../systems-devices/components/DeviceInspector';
import { formatClock, LiveIndicator, ReadingValue } from '../../realtime/device-presentation';
import { PLANT_CATEGORY_LABELS, plantReading } from '../../realtime/plant-model';
import { useRealtimePlant } from '../../realtime/use-realtime-plant';
import { TWIN_STATUS_TONE } from '../twin-presentation';
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

const METRIC = 'text-base leading-[22px] font-semibold tracking-[-0.02em]';

function Legend({ chilled, cooling }: { readonly chilled: string; readonly cooling: string }) {
  const line = (color: string, label: string) => (
    <span className="flex items-center gap-1.5">
      <span aria-hidden="true" className="h-1 w-4 rounded-full" style={{ background: color }} />
      {label}
    </span>
  );
  const dot = (tone: Parameters<typeof statusDotClass>[0], label: string) => (
    <span className="flex items-center gap-1.5">
      <span aria-hidden="true" className={`size-1.5 rounded-full ${statusDotClass(tone)}`} />
      {label}
    </span>
  );
  return (
    <div className="ml-auto flex items-center gap-4 text-xs text-muted-foreground">
      {line(chilled, '冷冻水')}
      {line(cooling, '冷却水')}
      {dot('running', '运行')}
      {dot('neutral', '停机 / 未接入')}
    </div>
  );
}

/** An asset with no data source: what it is and that it is not integrated, in the page's detail sheet. */
function UnconnectedSheet({ item, onClose }: { readonly item: TwinEquipment | null; readonly onClose: () => void }) {
  return (
    <Sheet modal={false} open={Boolean(item)} onOpenChange={(open) => { if (!open) onClose(); }}>
      <SheetContent showOverlay={false} className="overflow-y-auto">
        {item ? (
          <>
            <SheetHeader className="gap-1.5 border-b border-divider px-5 pt-4 pr-12 pb-3.5">
              <div className="flex items-center gap-2 text-xs text-muted-foreground">
                {KIND_LABELS[item.kind]}
                <StatusBadge tone={TWIN_STATUS_TONE[item.state]} label={TWIN_STATE_LABELS[item.state]} />
              </div>
              <SheetTitle className="text-lg leading-[26px] font-semibold tracking-[-0.01em]">{item.name}</SheetTitle>
              <SheetDescription className="sr-only">{KIND_LABELS[item.kind]}，未接入</SheetDescription>
            </SheetHeader>
            <StateCard
              icon={PlugZap}
              title={<h3 className="text-title font-semibold">还没有数据来源</h3>}
              description="接入电表或运行信号后，这里会显示实时读数和运行状态。"
            />
          </>
        ) : null}
      </SheetContent>
    </Sheet>
  );
}

export function DigitalTwinWorkspace() {
  const { site, plant, mode, registry } = useRealtimePlant();
  const search = pageRoute.useSearch();
  const navigate = pageRoute.useNavigate();
  const palette = useScenePalette();
  const assets = registry.data?.assetModel.assets;
  const twin = useMemo(() => (assets ? buildTwinPlant(assets, plant) : null), [assets, plant]);
  const plan = BUILDING_PLANS[site.code] ?? null;
  const terminals = useMemo(() => (plan ? buildingTerminals(plan).map((terminal) => terminal.equipment) : []), [plan]);
  const view: CameraView = search.view ?? 'overview';
  const cut = plan && search.floor && search.floor <= plan.floors.length ? search.floor : null;
  const all = twin ? [...twin.chillers, ...twin.towers, ...twin.chilledPumps, ...twin.coolingPumps, ...terminals] : [];
  // The selection is the URL's ?inspect: a bound device's id opens its sheet, an asset's id the 未接入 sheet.
  const selected = search.inspect ? all.find((item) => item.device?.deviceId === search.inspect || item.assetId === search.inspect) ?? null : null;
  const inspected = selected?.device ?? undefined;

  const setSearch = (patch: Partial<typeof search>) => void navigate({ search: (previous) => ({ ...previous, ...patch }), replace: true });
  const select = (item: TwinEquipment | null) => setSearch({ inspect: item ? item.device?.deviceId ?? item.assetId : undefined });
  const views: readonly (readonly [CameraView, string])[] = plan ? [['overview', '全景'], ['plant', '冷站'], ['terminal', '末端']] : [['overview', '全景'], ['plant', '冷站']];
  const toggleItem = 'h-7 px-2.5 text-xs';

  return (
    <Main fluid>
      <PageHeader
        title="数字孪生"
        description={site.displayName}
        meta={<LiveIndicator mode={mode} />}
        actions={plant.latestSampleAt ? <span className="text-xs text-muted-foreground">数据时间 <span className="font-mono">{formatClock(plant.latestSampleAt, site.timezone)}</span></span> : null}
      />
      <Panel role="region" aria-label="站点三维视图">
        <PanelHeader
          title="站点三维视图"
          badge={<InfoTip>拖动旋转、右键平移、滚轮缩放；点选设备查看详情。冷冻水在冷冻泵运行时流动，冷却水在机组运行时流动。</InfoTip>}
          caption={plan ? `冷站与${plan.name}楼宇` : '冷站'}
          action={<PanelLink to="/operations/realtime" search={{ site: site.id }}>实时运行</PanelLink>}
        />
        <div className="flex h-12 shrink-0 items-center gap-2 border-b border-divider px-4">
          <ToggleGroup type="single" variant="outline" spacing={0} className="h-7" value={view} onValueChange={(value) => { if (value) setSearch({ view: value as CameraView }); }} aria-label="视角">
            {views.map(([value, label]) => <ToggleGroupItem key={value} value={value} className={toggleItem}>{label}</ToggleGroupItem>)}
          </ToggleGroup>
          {plan ? (
            <>
              <ToggleGroup
                type="single"
                variant="outline"
                spacing={0}
                className="h-7"
                value={cut ? String(cut) : 'all'}
                onValueChange={(value) => { if (value) setSearch(value === 'all' ? { floor: undefined } : { floor: Number(value), view: 'terminal' }); }}
                aria-label="楼层"
              >
                <ToggleGroupItem value="all" className={toggleItem}>全部楼层</ToggleGroupItem>
                {[...plan.floors].reverse().map((floor) => <ToggleGroupItem key={floor.level} value={String(floor.level)} className={toggleItem}>{floor.level}F</ToggleGroupItem>)}
              </ToggleGroup>
              <Toggle variant="outline" size="sm" className={toggleItem} pressed={search.shell !== 'off'} onPressedChange={(pressed) => setSearch({ shell: pressed ? undefined : 'off' })}>
                <Building2 />外立面
              </Toggle>
            </>
          ) : null}
          <Toggle variant="outline" size="sm" className={toggleItem} pressed={search.pipes === 'on'} onPressedChange={(pressed) => setSearch({ pipes: pressed ? 'on' : undefined })}>
            <Waypoints />突出管路
          </Toggle>
          <Legend chilled={palette.chilled} cooling={palette.cooling} />
        </div>

        <div className="relative h-[calc(100svh-var(--header-height)-300px)] min-h-[520px]">
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
            />
          ) : registry.isError ? (
            <StateCard className="h-full justify-center" title={<h3 className="text-title font-semibold">设备登记读取失败</h3>} description="刷新页面重试；仍然失败时检查网络或稍后再来。" />
          ) : (
            <Skeleton className="h-full w-full rounded-none" />
          )}
        </div>

        <dl className="grid grid-cols-5 divide-x divide-divider border-t border-divider py-3">
          {([
            ['冷站总功率', <ReadingValue key="power" reading={plant.totalPower} className={METRIC} />],
            ['瞬时制冷量', <ReadingValue key="cooling" reading={plant.coolingCapacity} className={METRIC} />],
            ['冷站 COP', plant.plantCop === null ? <span key="cop" className={`${METRIC} text-muted-foreground`}>—</span> : <span key="cop" className={`${METRIC} tabular-nums`}>{formatDecimal(plant.plantCop, 2)}</span>],
            ['室外温度', <ReadingValue key="dry" reading={plantReading(plant, 'weather.ambient_dry_bulb_temperature')} className={METRIC} />],
            ['室外湿球', <ReadingValue key="wet" reading={plantReading(plant, 'weather.ambient_wet_bulb_temperature')} className={METRIC} />],
          ] as const).map(([label, value]) => (
            <div key={label} className="px-4">
              <FactTile label={label}>{value}</FactTile>
            </div>
          ))}
        </dl>
      </Panel>

      <DeviceInspector
        device={inspected}
        categoryLabel={inspected ? PLANT_CATEGORY_LABELS[inspected.category] : ''}
        onClose={() => select(null)}
      />
      <UnconnectedSheet item={selected && !selected.device ? selected : null} onClose={() => select(null)} />
    </Main>
  );
}
