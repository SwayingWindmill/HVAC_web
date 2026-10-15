import { useState, type ReactNode } from 'react';
import { Link } from '@tanstack/react-router';
import {
  AirVent, ArrowDown, ArrowUp, Building2, ChevronRight, Droplet, Fan, Gauge, Globe, Home, Minus, Plus,
  RotateCcw, RotateCw, Snowflake, Thermometer, Waves, Waypoints, Wind, X, Zap, type LucideIcon,
} from 'lucide-react';
import { StatusBadge } from '@/components/common/StatusBadge';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { formatDecimal } from '@/lib/operator-format';
import { cn } from '@/lib/utils';
import { formatClock } from '../../realtime/device-presentation';
import type { PlantReading } from '../../realtime/plant-model';
import type { TwinBuildingPlan } from '../twin-building';
import { TWIN_STATE_LABELS, type TwinEquipment, type TwinKind } from '../twin-model';
import { TWIN_STATUS_TONE } from '../twin-presentation';
import type { CameraView } from './TwinScene';

// The four corners over the scene, laid out as the reference warehouse twin does: figures top left,
// the context card top right (the site, or whatever is selected) with the camera toolbar beside it,
// the chilled-water loop bottom left, the equipment list bottom right. Nothing is drawn inside the
// scene but the selection brackets; every fact lives in these panels (docs/specs/digital-twin.md).

export const FLOATING = 'rounded-xl border bg-popover text-popover-foreground shadow-md';

export const KIND_LABELS: Readonly<Record<TwinKind, string>> = {
  chiller: '冷水机组',
  tower: '冷却塔',
  chilledPump: '冷冻水泵',
  coolingPump: '冷却水泵',
  ahu: '空调箱',
  fcu: '风机盘管',
  vav: '变风量末端',
};

export const KIND_ICONS: Readonly<Record<TwinKind, LucideIcon>> = {
  chiller: Snowflake,
  tower: Fan,
  chilledPump: Waves,
  coolingPump: Waves,
  ahu: AirVent,
  fcu: Wind,
  vav: Gauge,
};

function IconChip({ icon: Icon, size = 'md', muted = false, solid = false }: { readonly icon: LucideIcon; readonly size?: 'md' | 'sm'; readonly muted?: boolean; readonly solid?: boolean }) {
  return (
    <span className={cn(
      'flex shrink-0 items-center justify-center',
      size === 'md' ? 'size-8 rounded-lg [&_svg]:size-4' : 'size-[26px] rounded-md [&_svg]:size-3.5',
      solid ? 'bg-primary text-primary-foreground' : muted ? 'bg-secondary text-muted-foreground' : 'bg-primary-soft text-primary',
    )}
    >
      <Icon aria-hidden="true" />
    </span>
  );
}

/** A reading with its unit stepped down; absent readings show a dash. */
export function Value({ value, unit, className }: { readonly value: string | null; readonly unit?: string | null; readonly className?: string }) {
  if (value === null) return <span className={cn('text-subtle-foreground', className)}>—</span>;
  return <span className={cn('tabular-nums', className)}>{value}{unit ? <span className="ml-1 text-xs font-medium text-subtle-foreground">{unit}</span> : null}</span>;
}

export const present = (reading: PlantReading | null | undefined) => (reading && reading.state === 'PRESENT' ? reading : null);

// ── Top left: the plant's three headline figures ──────────────────────────────────────────────────

export interface KpiFigure {
  readonly icon: LucideIcon;
  readonly label: string;
  readonly value: string | null;
  readonly unit?: string;
  readonly caption: ReactNode;
}

export function TwinKpis({ figures }: { readonly figures: readonly KpiFigure[] }) {
  return (
    <dl aria-label="冷站关键指标" className="pointer-events-auto m-0 flex gap-2.5">
      {figures.map((figure) => (
        <div key={figure.label} className={cn(FLOATING, 'flex w-[188px] items-start gap-2.5 px-3.5 py-3')}>
          <IconChip icon={figure.icon} />
          <div className="min-w-0">
            <dt className="text-xs text-muted-foreground">{figure.label}</dt>
            <dd className="mt-0.5 text-xl leading-[26px] font-semibold tracking-[-0.02em]"><Value value={figure.value} unit={figure.unit} /></dd>
            <div className="mt-0.5 truncate text-xs text-muted-foreground">{figure.caption}</div>
          </div>
        </div>
      ))}
    </dl>
  );
}

// ── Beside the context card: camera, views and layers ─────────────────────────────────────────────

function RailButton({ label, icon: Icon, pressed, onClick }: { readonly label: string; readonly icon: LucideIcon; readonly pressed?: boolean; readonly onClick: () => void }) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <button
          type="button"
          aria-label={label}
          aria-pressed={pressed}
          onClick={onClick}
          className={cn(
            'flex h-[34px] w-9 items-center justify-center text-secondary-foreground outline-none transition-colors duration-100 hover:bg-accent focus-visible:bg-accent [&_svg]:size-4',
            pressed && 'bg-primary-soft text-primary hover:bg-primary-soft',
          )}
        >
          <Icon aria-hidden="true" />
        </button>
      </TooltipTrigger>
      <TooltipContent side="left">{label}</TooltipContent>
    </Tooltip>
  );
}

export function TwinToolbar({ view, views, shell, emphasis, onCamera, onView, onShell, onEmphasis }: {
  readonly view: CameraView;
  readonly views: readonly CameraView[];
  readonly shell: boolean | null;
  readonly emphasis: boolean;
  readonly onCamera: (action: 'in' | 'out' | 'left' | 'right' | 'home') => void;
  readonly onView: (view: CameraView) => void;
  readonly onShell: (on: boolean) => void;
  readonly onEmphasis: (on: boolean) => void;
}) {
  const viewButtons: Readonly<Record<CameraView, { label: string; icon: LucideIcon }>> = {
    overview: { label: '全景', icon: Globe },
    plant: { label: '冷站', icon: Snowflake },
    terminal: { label: '末端楼宇', icon: Building2 },
  };
  const divider = <span className="mx-[7px] my-0.5 h-px bg-divider" />;
  return (
    <div role="toolbar" aria-label="视图工具" aria-orientation="vertical" className={cn(FLOATING, 'pointer-events-auto flex w-9 flex-col overflow-hidden')}>
      <RailButton label="放大" icon={Plus} onClick={() => onCamera('in')} />
      <RailButton label="缩小" icon={Minus} onClick={() => onCamera('out')} />
      <RailButton label="向左转" icon={RotateCcw} onClick={() => onCamera('left')} />
      <RailButton label="向右转" icon={RotateCw} onClick={() => onCamera('right')} />
      <RailButton label="回到默认视角" icon={Home} onClick={() => onCamera('home')} />
      {divider}
      {views.map((option) => <RailButton key={option} label={viewButtons[option].label} icon={viewButtons[option].icon} pressed={view === option} onClick={() => onView(option)} />)}
      {divider}
      {shell === null ? null : <RailButton label={shell ? '隐藏外立面' : '显示外立面'} icon={Building2} pressed={shell} onClick={() => onShell(!shell)} />}
      <RailButton label="突出管路" icon={Waypoints} pressed={emphasis} onClick={() => onEmphasis(!emphasis)} />
    </div>
  );
}

// ── Top right: the context card ───────────────────────────────────────────────────────────────────

function CardHeader({ icon, eyebrow, title, caption, onClose }: { readonly icon: LucideIcon; readonly eyebrow: string; readonly title: string; readonly caption?: ReactNode; readonly onClose?: () => void }) {
  return (
    <header className="flex items-start gap-2.5 px-3.5 pt-3 pb-2.5">
      <IconChip icon={icon} />
      <div className="min-w-0 flex-1">
        <div className="text-micro text-primary">{eyebrow}</div>
        <h2 className="text-[15px] leading-[22px] font-semibold">{title}</h2>
        {caption ? <div className="text-xs text-muted-foreground">{caption}</div> : null}
      </div>
      {onClose ? (
        <button type="button" aria-label="取消选中" onClick={onClose} className="-mt-0.5 -mr-1.5 flex size-7 items-center justify-center rounded-md text-muted-foreground hover:bg-accent [&_svg]:size-4">
          <X aria-hidden="true" />
        </button>
      ) : null}
    </header>
  );
}

function Tile({ label, children }: { readonly label: string; readonly children: ReactNode }) {
  return (
    <div className="rounded-lg border border-divider bg-muted px-2.5 py-[7px]">
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="mt-px text-[15px] leading-[22px] font-semibold">{children}</dd>
    </div>
  );
}

export interface EquipmentGroup {
  readonly kind: TwinKind;
  readonly label: string;
  readonly items: readonly TwinEquipment[];
}

export interface Attention {
  readonly title: string;
  readonly count: number;
  readonly to: '/operations/alarms' | '/operations/work-center';
}

export function SiteCard({ siteId, siteName, sampledAt, timeZone, today, outdoor, groups, attention }: {
  readonly siteId: string;
  readonly siteName: string;
  readonly sampledAt: string | null;
  readonly timeZone: string;
  readonly today: { readonly electricity: string | null; readonly cooling: string | null; readonly cop: string | null };
  readonly outdoor: { readonly dry: string | null; readonly wet: string | null };
  readonly groups: readonly EquipmentGroup[];
  readonly attention: Attention | null;
}) {
  const machines = groups.flatMap((group) => group.items);
  const running = machines.filter((item) => item.state === 'running').length;
  const stopped = machines.filter((item) => item.state === 'stopped').length;
  const unconnected = machines.filter((item) => item.state === 'unconnected').length;
  const summary = [`${running} 台运行`, stopped ? `${stopped} 台停机` : null, unconnected ? `${unconnected} 台未接入` : null].filter(Boolean).join(' · ');
  return (
    <>
      <CardHeader icon={Home} eyebrow={`空调机房 · ${siteName}`} title="冷站" caption={sampledAt ? <>实时 <span className="font-mono">{formatClock(sampledAt, timeZone)}</span></> : '还没有读数'} />
      <div className="flex items-center gap-2 px-3.5 pb-2.5 text-xs text-muted-foreground">
        <StatusBadge tone={running > 0 ? 'running' : 'neutral'} label={running > 0 ? '运行中' : '停机'} />
        <span className="truncate">{summary}</span>
      </div>
      <dl className="m-0 grid grid-cols-2 gap-2 px-3.5 pb-2.5">
        <Tile label="今日用电"><Value value={today.electricity} unit="kWh" /></Tile>
        <Tile label="今日供冷"><Value value={today.cooling} unit="kWh" /></Tile>
        <Tile label="综合能效"><Value value={today.cop} /></Tile>
        <Tile label="室外 · 湿球"><Value value={outdoor.dry} unit="℃" /> <Value value={outdoor.wet} unit="℃" /></Tile>
      </dl>
      <div className="border-t border-divider px-3.5 pt-0.5 pb-1.5">
        <div className="flex h-[30px] items-center text-xs font-semibold">设备<span className="ml-auto font-normal text-muted-foreground">运行 / 台数</span></div>
        {groups.map((group) => {
          const groupRunning = group.items.filter((item) => item.state === 'running').length;
          const connected = group.items.some((item) => item.state !== 'unconnected');
          return (
            <div key={group.label} className="grid grid-cols-[26px_minmax(0,1fr)_auto] items-center gap-2.5 py-1.5 text-sm">
              <IconChip icon={KIND_ICONS[group.kind]} size="sm" muted={!connected} />
              <span className="truncate">{group.label}</span>
              <span className="flex items-center gap-2">
                <span className={cn('tabular-nums', connected ? 'font-semibold' : 'text-muted-foreground')}>{connected ? groupRunning : '—'} / {group.items.length}</span>
                <StatusBadge tone={connected ? (groupRunning > 0 ? 'running' : 'neutral') : 'neutral'} label={connected ? (groupRunning > 0 ? '运行' : '停机') : '未接入'} />
              </span>
            </div>
          );
        })}
      </div>
      {attention ? (
        <Link to={attention.to} search={{ site: siteId }} className="flex items-center gap-2 border-t border-divider px-3.5 py-2.5 outline-none hover:bg-accent focus-visible:bg-accent">
          <span className="size-2 shrink-0 rounded-full bg-warning" aria-hidden="true" />
          <span className="min-w-0 flex-1 truncate text-xs"><b className="font-semibold">需要处理 {attention.count}</b><span className="text-muted-foreground"> · {attention.title}</span></span>
          <ChevronRight className="size-3.5 text-subtle-foreground" aria-hidden="true" />
        </Link>
      ) : null}
    </>
  );
}

/** The fields a selected machine shows, by kind: what its device reports, by source key. */
const FIELDS: Readonly<Partial<Record<TwinKind, readonly (readonly [string, LucideIcon, readonly string[]])[]>>> = {
  chiller: [
    ['功率', Zap, ['chiller.power']],
    ['主机 COP', Gauge, ['chiller.cop']],
    ['冷冻水 出 / 进', Droplet, ['chiller.leaving_chilled_water_temperature', 'chiller.entering_chilled_water_temperature']],
    ['冷却水 进 / 出', Thermometer, ['chiller.entering_cooling_water_temperature', 'chiller.leaving_cooling_water_temperature']],
  ],
  chilledPump: [['功率', Zap, ['chwp.power']], ['频率', Gauge, ['chwp.frequency']], ['流量', Waves, ['chwp.flow_rate']]],
  coolingPump: [['功率', Zap, ['cwp.power']], ['频率', Gauge, ['cwp.frequency']], ['流量', Waves, ['cwp.flow_rate']]],
  tower: [['功率', Zap, ['cooling_tower.power']], ['风机转速', Fan, ['cooling_tower.fan_speed']], ['出水温度', Thermometer, ['cooling_tower.leaving_water_temperature']]],
};

const LOAD_KEY: Partial<Record<TwinKind, string>> = { chiller: 'chiller.compressor_load' };

export function DeviceCard({ item, location, siteId, timeZone, onClose }: {
  readonly item: TwinEquipment;
  readonly location: string;
  readonly siteId: string;
  readonly timeZone: string;
  readonly onClose: () => void;
}) {
  const device = item.device;
  const load = device && LOAD_KEY[item.kind] ? present(device.reading(LOAD_KEY[item.kind]!)) : null;
  const fields = FIELDS[item.kind] ?? [];
  return (
    <>
      <CardHeader
        icon={KIND_ICONS[item.kind]}
        eyebrow={`${KIND_LABELS[item.kind]} · ${location}`}
        title={item.name}
        caption={device?.latestSampleAt ? <>最近数据 <span className="font-mono">{formatClock(device.latestSampleAt, timeZone)}</span></> : undefined}
        onClose={onClose}
      />
      <div className="px-3.5 pb-2.5">
        <div className="flex items-center gap-2 text-xs text-muted-foreground">
          <StatusBadge tone={TWIN_STATUS_TONE[item.state]} label={TWIN_STATE_LABELS[item.state]} />
          {load ? <>负载<span className="ml-auto font-semibold text-foreground tabular-nums">{load.display}{load.unit}</span></> : null}
        </div>
        {load && load.numeric !== null ? (
          <div className="mt-2 h-1.5 overflow-hidden rounded-full bg-divider"><div className="h-full rounded-full bg-primary" style={{ width: `${Math.max(0, Math.min(100, load.numeric))}%` }} /></div>
        ) : null}
      </div>
      {device ? (
        <dl className="m-0 px-3.5 pb-1">
          {fields.map(([label, Icon, keys]) => {
            const readings = keys.map((key) => present(device.reading(key)));
            const unit = readings.find(Boolean)?.unit ?? null;
            return (
              <div key={label} className="flex h-8 items-center border-t border-divider text-sm">
                <dt className="flex items-center gap-2 text-muted-foreground"><Icon className="size-3.5" aria-hidden="true" />{label}</dt>
                <dd className="ml-auto font-semibold tabular-nums">
                  {readings.every((reading) => reading === null) ? <span className="text-subtle-foreground">—</span> : (
                    <>{readings.map((reading) => reading?.display ?? '—').join(' / ')}{unit ? <span className="ml-1 text-xs font-medium text-subtle-foreground">{unit}</span> : null}</>
                  )}
                </dd>
              </div>
            );
          })}
        </dl>
      ) : (
        <p className="m-0 border-t border-divider px-3.5 py-3 text-xs leading-[18px] text-muted-foreground">这台设备还没有数据来源，接入电表或运行信号后会显示实时读数和运行状态。</p>
      )}
      {device ? (
        <footer className="flex items-center border-t border-divider px-3.5 py-2.5">
          <Link to="/operations/systems-devices" search={{ site: siteId, inspect: device.deviceId }} className="inline-flex items-center gap-0.5 text-xs font-medium text-primary hover:underline">
            设备详情<ChevronRight className="size-3.5" aria-hidden="true" />
          </Link>
        </footer>
      ) : null}
    </>
  );
}

export function BuildingCard({ plan, cut, terminals, onFloor }: {
  readonly plan: TwinBuildingPlan;
  readonly cut: number | null;
  readonly terminals: readonly { readonly level: number; readonly equipment: TwinEquipment }[];
  readonly onFloor: (level: number | null) => void;
}) {
  const floors = cut === null ? plan.floors : plan.floors.filter((floor) => floor.level === cut);
  const rooms = floors.flatMap((floor) => floor.rooms.filter((room) => room.kind === 'private' || room.kind === 'smallPrivate' || room.kind === 'hall'));
  const units = terminals.filter((terminal) => cut === null || terminal.level === cut);
  const connected = units.some((terminal) => terminal.equipment.state !== 'unconnected');
  const floor = cut === null ? null : plan.floors.find((candidate) => candidate.level === cut);
  return (
    <>
      <CardHeader icon={Building2} eyebrow={`末端楼宇 · ${plan.name}`} title={floor ? floor.label : '全部楼层'} caption={`${plan.floors.length} 层${floor ? ` · 剖切到 ${floor.level}F` : ''}`} />
      <div className="px-3.5 pb-2.5">
        <div role="group" aria-label="楼层" className="flex h-7 overflow-hidden rounded-lg border border-input">
          {[null, ...[...plan.floors].reverse().map((candidate) => candidate.level)].map((level) => (
            <button
              key={level ?? 'all'}
              type="button"
              aria-pressed={cut === level}
              onClick={() => onFloor(level)}
              className={cn('flex-1 border-r border-input text-xs last:border-r-0 hover:bg-accent', cut === level && 'bg-primary-soft font-semibold text-primary hover:bg-primary-soft')}
            >
              {level === null ? '全部' : `${level}F`}
            </button>
          ))}
        </div>
      </div>
      <dl className="m-0 grid grid-cols-2 gap-2 px-3.5 pb-2.5">
        <Tile label="房间"><Value value={String(rooms.length)} unit="间" /></Tile>
        <Tile label="末端设备"><Value value={String(units.length)} unit="台" /></Tile>
        <Tile label="平均室温"><Value value={null} /></Tile>
        <Tile label="新风"><Value value={null} /></Tile>
      </dl>
      {connected ? null : (
        <div className="flex items-center gap-2 border-t border-divider px-3.5 py-2.5 text-xs text-muted-foreground">
          <StatusBadge tone="neutral" label="未接入" />接入温控器后，房间按温度着色
        </div>
      )}
    </>
  );
}

// ── Bottom left: the chilled-water loop ───────────────────────────────────────────────────────────

export type LoopNodeState = 'done' | 'current' | 'idle';

export interface LoopNode {
  readonly icon: LucideIcon;
  readonly label: string;
  readonly caption: ReactNode;
  readonly state: LoopNodeState;
}

export function TwinLoop({ title, flow, nodes, difference, siteId }: {
  readonly title: string;
  readonly flow: string | null;
  readonly nodes: readonly LoopNode[];
  readonly difference: string | null;
  readonly siteId: string;
}) {
  // The live stretch of the loop: from the first node that reports to the last.
  const first = nodes.findIndex((node) => node.state !== 'idle');
  const last = nodes.reduce((found, node, index) => (node.state !== 'idle' ? index : found), -1);
  const centre = (index: number) => `${((index + 0.5) / nodes.length) * 100}%`;
  return (
    <section aria-label={title} className={cn(FLOATING, 'pointer-events-auto flex w-[640px] overflow-hidden')}>
      <div className="min-w-0 flex-1 px-3.5 pt-2.5 pb-3">
        <div className="flex h-[26px] items-center gap-2">
          <Droplet className="size-4 text-primary" aria-hidden="true" />
          <h2 className="text-sm font-semibold">{title}</h2>
          <span className="ml-auto text-xs text-muted-foreground">流量 <Value value={flow} unit="m³/h" /></span>
        </div>
        <ol className="relative isolate m-0 mt-2 grid list-none p-0" style={{ gridTemplateColumns: `repeat(${nodes.length}, minmax(0, 1fr))` }}>
          <span aria-hidden="true" className="absolute top-3.5 -z-10 h-0.5 bg-border" style={{ left: centre(0), right: centre(0) }} />
          {first >= 0 && last > first ? (
            <span aria-hidden="true" className="absolute top-3.5 -z-10 h-0.5 bg-primary" style={{ left: centre(first), width: `${((last - first) / nodes.length) * 100}%` }} />
          ) : null}
          {nodes.map((node) => {
            const Icon = node.icon;
            return (
              <li key={node.label} className="flex flex-col items-center gap-1 text-center">
                <span className={cn(
                  'flex size-7 items-center justify-center rounded-full [&_svg]:size-3.5',
                  node.state === 'idle' && 'border border-dashed border-subtle-foreground bg-popover text-subtle-foreground',
                  node.state === 'done' && 'bg-primary text-primary-foreground',
                  node.state === 'current' && 'bg-primary text-primary-foreground ring-4 ring-ring-soft',
                )}
                >
                  <Icon aria-hidden="true" />
                </span>
                <span className={cn('text-xs font-medium', node.state === 'current' && 'text-primary')}>{node.label}</span>
                <span className="text-micro text-muted-foreground">{node.caption}</span>
              </li>
            );
          })}
        </ol>
      </div>
      <Link to="/operations/realtime" search={{ site: siteId }} className="m-2 flex w-[170px] shrink-0 flex-col gap-1 rounded-lg border border-divider bg-muted px-3 py-2.5 outline-none hover:border-input-hover focus-visible:border-ring">
        <span className="flex items-center gap-1.5 text-xs text-muted-foreground"><Thermometer className="size-3.5" aria-hidden="true" />供回水温差</span>
        <span className="text-xl leading-[26px] font-semibold"><Value value={difference} unit="℃" /></span>
        <span className="flex items-center text-xs font-medium text-primary">实时运行<ChevronRight className="ml-auto size-3.5" aria-hidden="true" /></span>
      </Link>
    </section>
  );
}

export const LOOP_ICONS = { pump: Waves, chiller: Snowflake, supply: ArrowUp, terminal: Building2, return: ArrowDown, riser: Waypoints, fcu: Wind } as const;

// ── Bottom right: the equipment list ──────────────────────────────────────────────────────────────

export interface ListTab {
  readonly key: string;
  readonly label: string;
  readonly count: string;
  readonly rows: readonly ListRow[];
}

export interface ListRow {
  readonly key: string;
  readonly item: TwinEquipment;
  readonly title: string;
  readonly caption: string;
  readonly icon: LucideIcon;
  readonly load: number | null;
}

const LIST_LIMIT = 5;

export function TwinList({ tabs, scope, selectedId, onSelect }: {
  readonly tabs: readonly ListTab[];
  readonly scope: string;
  readonly selectedId: string | null;
  readonly onSelect: (item: TwinEquipment) => void;
}) {
  const [active, setActive] = useState(tabs[0]?.key);
  const tab = tabs.find((candidate) => candidate.key === active) ?? tabs[0];
  if (!tab) return null;
  const rows = tab.rows.slice(0, LIST_LIMIT);
  return (
    <section aria-label="设备列表" className={cn(FLOATING, 'pointer-events-auto w-[360px] overflow-hidden')}>
      <div role="tablist" aria-label="设备类型" className="flex gap-4 border-b border-divider px-3.5">
        {tabs.map((candidate) => (
          <button
            key={candidate.key}
            type="button"
            role="tab"
            aria-selected={candidate.key === tab.key}
            onClick={() => setActive(candidate.key)}
            className={cn('flex h-9 items-center gap-1.5 border-b-2 border-transparent text-xs text-muted-foreground outline-none hover:text-foreground focus-visible:text-foreground', candidate.key === tab.key && 'border-primary font-semibold text-foreground')}
          >
            {candidate.label}<span className="font-medium text-subtle-foreground">{candidate.count}</span>
          </button>
        ))}
        <span className="ml-auto self-center text-xs text-muted-foreground">{scope}</span>
      </div>
      <div role="tabpanel">
        {rows.map((row) => {
          const selected = row.item.assetId === selectedId;
          const live = row.item.state === 'running';
          return (
            <button
              key={row.key}
              type="button"
              onClick={() => onSelect(row.item)}
              aria-pressed={selected}
              className={cn('grid h-11 w-full grid-cols-[26px_minmax(0,1fr)_auto_52px_14px] items-center gap-2 border-t border-divider pr-3 pl-3.5 text-left text-sm outline-none first:border-t-0 hover:bg-accent focus-visible:bg-accent', selected && 'bg-primary-soft hover:bg-primary-soft')}
            >
              <IconChip icon={row.icon} size="sm" muted={!live && !selected} solid={selected} />
              <span className="min-w-0">
                <span className={cn('block truncate font-semibold', selected && 'text-primary')}>{row.title}</span>
                <span className="block truncate text-xs text-muted-foreground">{row.caption}</span>
              </span>
              <StatusBadge tone={TWIN_STATUS_TONE[row.item.state]} label={TWIN_STATE_LABELS[row.item.state]} />
              <span>
                {row.load === null ? <span className="text-xs text-subtle-foreground">—</span> : (
                  <>
                    <span className="block h-1 overflow-hidden rounded-full bg-divider"><span className="block h-full rounded-full bg-primary" style={{ width: `${Math.max(0, Math.min(100, row.load))}%` }} /></span>
                    <span className="text-micro text-muted-foreground tabular-nums">{formatDecimal(row.load, 0)}%</span>
                  </>
                )}
              </span>
              <ChevronRight className={cn('size-3.5', selected ? 'text-primary' : 'text-subtle-foreground')} aria-hidden="true" />
            </button>
          );
        })}
      </div>
      {tab.rows.length > LIST_LIMIT ? (
        <div className="border-t border-divider px-3.5 py-2 text-xs text-muted-foreground">还有 {tab.rows.length - LIST_LIMIT} 台</div>
      ) : null}
    </section>
  );
}

