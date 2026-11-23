import { useMemo } from 'react';
import type { SiteAssetModel } from '@/api/generated/platformGateway.gen';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { getRouteApi, Link, useNavigate } from '@tanstack/react-router';
import { ChevronRight, CircleCheck, Lock, RefreshCw } from 'lucide-react';
import { PageHeader } from '@/blocks/page-header';
import { Panel, PanelHeader } from '@/blocks/panel';
import { InfoTip } from '@/components/info-tip';
import { Main } from '@/components/layout/Main';
import { SeverityMark } from '@/components/severity-mark';
import { Button } from '@/components/ui/button';
import { ComposedChart } from '@/components/ui/composed-chart';
import { Skeleton } from '@/components/ui/skeleton';
import { Sparkline } from '@/components/ui/sparkline';
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group';
import { alarmHistoryQuery } from '@/features/alarms/alarm-api';
import { operationLabel, SEVERITY_LABELS, SEVERITY_LEVEL } from '@/features/alarms/alarm-presentation';
import { siteCommandsQuery } from '@/features/control/command-api';
import { commandSummary, STATUS_PRESENTATION } from '@/features/control/command-presentation';
import { CommandMark } from '@/features/control/components/CommandMark';
import { ModeGlyph } from '@/features/control/components/ControlBoundary';
import { commandLabels, controlTargets } from '@/features/control/control-model';
import { activeLocks, controlActorLabel, LOCK_PRESENTATION, MODE_PRESENTATION, siteControlStateQuery } from '@/features/control/control-state';
import { DataGapBanner } from '@/features/data-gaps/DataGapBanner';
import { dataGapsKeys } from '@/features/data-gaps/query';
import { formatCompactTime, formatDecimal } from '@/lib/operator-format';
import { cn } from '@/lib/utils';
import { coolingUncertaintyText, copUncertaintyText, equipmentStatus, loopDifferentialPressure, PLANT_CATEGORY_LABELS, plantReading, probeOffsetText, type ParallelBranch, type PlantDevice, type PlantReading, type PlantView, type WaterLoopKind } from '../plant-model';
import { useRealtimePlant } from '../use-realtime-plant';
import { usePlantHistory } from '../use-plant-history';
import { copSeries, COOLING_KEY, hourAgo, POWER_KEY } from '../plant-trend';
import { deviceStatus, formatClock, LiveIndicator, LowConfidenceBadge, ReadingMetric, ReadingValue } from '../device-presentation';
import { buildTimeSlots, fillGaugeSlots, fillScalarSlots } from '../../trends/timeline';
import { DeviceInspector } from '../../systems-devices/components/DeviceInspector';
import { PlantHydraulicSchematic } from './PlantHydraulicSchematic';

const pageRoute = getRouteApi('/_app/_site/operations/realtime');
const siteRoute = getRouteApi('/_app/_site');

type Metric = 'power' | 'cooling' | 'cop';
type Window = '6h' | '24h';

function MetricTab({ label, selected, onSelect, value, unit, values, hasTrend, caption, info }: {
  readonly label: string;
  readonly selected: boolean;
  readonly onSelect: () => void;
  readonly value: React.ReactNode;
  readonly unit?: string;
  readonly values: readonly (number | null)[];
  readonly hasTrend: boolean;
  readonly caption: string;
  readonly info?: string;
}) {
  return (
    <button
      type="button"
      role="tab"
      aria-selected={selected}
      onClick={onSelect}
      className={cn(
        'flex min-w-0 flex-1 basis-0 flex-col gap-1 border-r border-divider px-[18px] pt-3 pb-2.5 text-left shadow-[inset_0_-2px_0_transparent] outline-none last:border-r-0 hover:bg-accent/60 focus-visible:ring-3 focus-visible:ring-ring-soft focus-visible:ring-inset',
        selected && 'shadow-[inset_0_-2px_0_var(--primary)] hover:bg-transparent',
      )}
    >
      <span className={cn('flex items-center gap-1.5 text-xs font-medium', selected ? 'text-primary' : 'text-muted-foreground')}>
        {label}
        {info ? <InfoTip>{info}</InfoTip> : null}
      </span>
      <span className="flex items-end gap-3">
        <span className="text-[22px] leading-[26px] font-semibold tracking-[-0.03em] tabular-nums">
          {value}
          {unit ? <span className="ml-1 text-xs font-medium text-subtle-foreground">{unit}</span> : null}
        </span>
        {hasTrend ? (
          <Sparkline data={values} variant="line" animate={false} color={selected ? 'var(--chart-1)' : 'var(--muted-foreground)'} className="ml-auto h-[26px] w-[84px]" />
        ) : null}
      </span>
      <span className="truncate text-xs text-muted-foreground">{caption}</span>
    </button>
  );
}

function PlantTrendPanel({ plant, metric, window, onMetric, onWindow, timeZone }: {
  readonly plant: PlantView;
  readonly metric: Metric;
  readonly window: Window;
  readonly onMetric: (metric: Metric) => void;
  readonly onWindow: (window: Window) => void;
  readonly timeZone: string;
}) {
  const history = usePlantHistory(plant, [POWER_KEY, COOLING_KEY], window === '24h' ? 24 : 6);
  const series = useMemo(() => {
    const power = history.series.get(POWER_KEY) ?? [];
    const cooling = history.series.get(COOLING_KEY) ?? [];
    return {
      power: power.map((point) => ({ periodStart: point.periodStart, value: point.average })),
      cooling: cooling.map((point) => ({ periodStart: point.periodStart, value: point.average })),
      cop: copSeries(power, cooling),
    };
  }, [history.series]);
  const slots = useMemo(
    () => buildTimeSlots({ fromMs: history.fromMs, toMs: history.toMs, granularity: 'QUARTER_HOUR', timeZone }),
    [history.fromMs, history.toMs, timeZone],
  );
  const filled = useMemo(
    () => ({
      power: fillScalarSlots(slots, series.power),
      cooling: fillScalarSlots(slots, series.cooling),
      cop: fillScalarSlots(slots, series.cop),
    }),
    [slots, series],
  );
  const clock = new Intl.DateTimeFormat('zh-CN', { timeZone, hour: '2-digit', minute: '2-digit', hour12: false });
  const selected = series[metric];
  const selectedSlots = filled[metric];
  const powerAgo = hourAgo(series.power);
  const coolingAgo = hourAgo(series.cooling);
  const copPeak = series.cop.length > 0 ? Math.max(...series.cop.map((point) => point.value)) : null;
  const titles: Readonly<Record<Metric, string>> = { power: '冷站总功率', cooling: '瞬时制冷量', cop: '冷站 COP' };
  const units: Readonly<Record<Metric, string>> = { power: 'kW', cooling: 'kW', cop: '' };

  return (
    <Panel role="region" aria-label="冷站运行">
      <div role="tablist" aria-label="冷站指标" className="flex border-b border-divider">
        <MetricTab
          label="冷站总功率"
          selected={metric === 'power'}
          onSelect={() => onMetric('power')}
          value={<ReadingMetric reading={plant.totalPower} digits={1} unit="kW" />}
          values={filled.power.map((point) => point.value)}
          hasTrend={series.power.length > 1}
          caption={powerAgo === null ? '空调总电表' : `1 小时前 ${formatDecimal(powerAgo, 1)} kW`}
        />
        <MetricTab
          label="瞬时制冷量"
          selected={metric === 'cooling'}
          onSelect={() => onMetric('cooling')}
          value={<ReadingMetric reading={plant.coolingCapacity} digits={0} unit="kW" />}
          values={filled.cooling.map((point) => point.value)}
          hasTrend={series.cooling.length > 1}
          caption={[coolingAgo === null ? '冷量表' : `1 小时前 ${formatDecimal(coolingAgo, 0)} kW`, coolingUncertaintyText(plant.coolingCapacity)].filter(Boolean).join(' · ')}
        />
        <MetricTab
          label="冷站 COP"
          selected={metric === 'cop'}
          onSelect={() => onMetric('cop')}
          value={plant.plantCop === null ? <span className="text-muted-foreground">—</span> : <>{formatDecimal(plant.plantCop, 2)}{plant.plantCopLowConfidence ? <LowConfidenceBadge /> : null}</>}
          values={filled.cop.map((point) => point.value)}
          hasTrend={series.cop.length > 1}
          caption={[copPeak === null ? '制冷量 ÷ 总功率' : `近 ${window === '24h' ? 24 : 6} 小时最高 ${formatDecimal(copPeak, 2)}`, copUncertaintyText(plant)].filter(Boolean).join(' · ')}
          info="冷站瞬时制冷量 ÷ 冷站总功率。温差相对探头误差过小时照常计算，标「低可信」并给出区间，这段不计入能效均值、故障诊断和节能验证。任一读数过期或不可信时不计算。"
        />
      </div>
      {probeOffsetText(plant) ? <p className="border-b border-divider px-5 py-2 text-xs text-muted-foreground">{probeOffsetText(plant)}</p> : null}
      <div className="flex flex-col gap-2 px-5 pt-3.5 pb-3">
        <div className="flex items-center gap-2.5">
          <span className="text-sm font-semibold">{titles[metric]}</span>
          <span className="text-xs text-muted-foreground">近 {window === '24h' ? 24 : 6} 小时 · 15 分钟平均{units[metric] ? ` · ${units[metric]}` : ''}</span>
          <ToggleGroup
            type="single"
            variant="outline"
            value={window}
            onValueChange={(value) => { if (value === '6h' || value === '24h') onWindow(value); }}
            aria-label="时间范围"
            spacing={0}
            className="ml-auto h-7"
          >
            <ToggleGroupItem value="6h" className="h-7 px-2.5 text-xs">6 小时</ToggleGroupItem>
            <ToggleGroupItem value="24h" className="h-7 px-2.5 text-xs">24 小时</ToggleGroupItem>
          </ToggleGroup>
        </div>
        {history.isPending ? <Skeleton className="h-[230px]" /> : history.isError ? (
          <p className="flex h-[230px] items-center justify-center text-sm text-muted-foreground">历史读数暂时读不到</p>
        ) : selected.length < 2 ? (
          <p className="flex h-[230px] items-center justify-center text-sm text-muted-foreground">这段时间没有记录</p>
        ) : (
          <ComposedChart
            data={selectedSlots.map((slot) => ({
              at: slot.startMs,
              value: slot.value === null ? null : Number(slot.value.toFixed(metric === 'cop' ? 2 : 1)),
            }))}
            xKey="at"
            xType="number"
            xDomain={[history.fromMs, history.toMs]}
            xFormatter={(value) => { const ms = typeof value === 'string' && value.trim() === '' ? NaN : Number(value); return Number.isFinite(ms) ? clock.format(new Date(ms)) : ''; }}
            series={[{ key: 'value', label: titles[metric], type: 'area', color: 'var(--chart-1)' }]}
            showGrid
            showYAxis
            yDomain={metric === 'cop' ? 'auto' : 'zero'}
            yFormatter={(value) => formatDecimal(value, metric === 'cop' ? 2 : 0)}
            animate={false}
            className="h-[230px]"
          />
        )}
      </div>
    </Panel>
  );
}

interface PlantEvent {
  readonly key: string;
  readonly at: string;
  readonly mark: React.ReactNode;
  readonly title: string;
  readonly detail: string;
  readonly to?: { readonly path: '/operations/alarms' | '/operations/control'; readonly inspect?: string };
}

/** Today at the plant, newest first: alarms, commands and devices whose data stopped. */
function RecentEvents({ plant, siteId, myId, timeZone, assetModel }: {
  readonly plant: PlantView;
  readonly siteId: string;
  readonly myId: string;
  readonly timeZone: string;
  readonly assetModel: (Parameters<typeof controlTargets>[0] & Pick<SiteAssetModel, 'assets'>) | undefined;
}) {
  const alarms = useQuery(alarmHistoryQuery(siteId));
  const commands = useQuery(siteCommandsQuery(siteId));
  const controlState = useQuery(siteControlStateQuery(siteId));
  const modeState = controlState.data;
  const labels = useMemo(() => (assetModel ? commandLabels(controlTargets(assetModel, new Date())) : new Map()), [assetModel]);
  const assetName = (assetId: string) => assetModel?.assets.find((asset) => asset.id === assetId)?.displayName ?? '设备';
  const deviceName = (deviceId: string | undefined) => (deviceId ? plant.devices.find((device) => device.deviceId === deviceId)?.name ?? '设备' : '站点');
  const dayFmt = new Intl.DateTimeFormat('en-CA', { timeZone, year: 'numeric', month: '2-digit', day: '2-digit' });
  const today = dayFmt.format(new Date());
  const isToday = (value: string) => { const ms = Date.parse(value); return Number.isFinite(ms) && dayFmt.format(new Date(ms)) === today; };

  const events: PlantEvent[] = [
    ...(alarms.data?.alarms ?? []).flatMap((alarm) => alarm.timeline
      .filter((entry) => isToday(entry.occurredAt) && (entry.operation === 'PUBLISH' || entry.operation === 'ACKNOWLEDGE' || entry.operation === 'CLEAR'))
      .map((entry): PlantEvent => ({
        key: `alarm-${alarm.alarmId}-${entry.version}`,
        at: entry.occurredAt,
        mark: entry.operation === 'CLEAR'
          ? <CircleCheck className="size-3 text-success" aria-hidden="true" />
          : <SeverityMark level={SEVERITY_LEVEL[entry.currentSeverity]} label="" />,
        title: `${deviceName(alarm.deviceId)} · ${alarm.title}`,
        detail: `${SEVERITY_LABELS[entry.currentSeverity]}告警 · ${operationLabel(entry.operation)}`,
        to: { path: '/operations/alarms', inspect: alarm.alarmId },
      }))),
    ...(commands.data ?? []).filter((command) => isToday(command.updatedAt)).map((command): PlantEvent => {
      const summary = commandSummary(command, labels);
      return {
        key: `command-${command.commandId}`,
        at: command.updatedAt,
        mark: <CommandMark status={command.status} />,
        title: `${summary.target} · ${summary.title}`,
        detail: `指令 · ${STATUS_PRESENTATION[command.status].label}`,
        to: { path: '/operations/control', inspect: command.commandId },
      };
    }),
    ...(modeState?.updatedAt && isToday(modeState.updatedAt) ? [{
      key: `control-mode-${modeState.revision}`,
      at: modeState.updatedAt,
      mark: <ModeGlyph mode={modeState.controlMode} />,
      title: `控制方式改为${MODE_PRESENTATION[modeState.controlMode].label}`,
      detail: [controlActorLabel(modeState, myId), modeState.reason || '控制方式'].join(' · '),
      to: { path: '/operations/control' },
    } satisfies PlantEvent] : []),
    ...[...activeLocks(controlState.data).values()].filter((lock) => lock.updatedAt && isToday(lock.updatedAt)).map((lock): PlantEvent => ({
      key: `control-lock-${lock.assetId}-${lock.revision}`,
      at: lock.updatedAt ?? '',
      mark: <Lock className="size-3 text-muted-foreground" aria-hidden="true" />,
      title: `${assetName(lock.assetId)} · ${lock.lockMode === 'UNLOCKED' ? '解除锁定' : LOCK_PRESENTATION[lock.lockMode].label}`,
      detail: [controlActorLabel(lock, myId), lock.reason || '设备锁定'].join(' · '),
      to: { path: '/operations/control' },
    })),
    ...plant.devices.filter((device) => device.hasStaleData || device.connection === 'OFFLINE').map((device): PlantEvent => ({
      key: `data-${device.deviceId}`,
      at: device.latestSampleAt || new Date().toISOString(),
      mark: <svg viewBox="0 0 10 10" className="size-2.5 shrink-0" aria-hidden="true"><path d="M1.5 1.5h7v7h-7z" fill="none" strokeWidth="1.5" className="stroke-warning" /></svg>,
      title: `${device.name} · ${device.connection === 'OFFLINE' ? '通信离线' : '读数中断'}`,
      detail: device.connection === 'OFFLINE' ? '设备离线' : '通信在线，没有新数据',
    })),
  ].sort((left, right) => Date.parse(right.at) - Date.parse(left.at)).slice(0, 7);

  const pending = alarms.isPending || commands.isPending;
  return (
    <Panel role="region" aria-label="最近事件" className="min-h-0">
      <PanelHeader title="最近事件" caption="今天" />
      {pending ? <div className="flex flex-col gap-2 p-4"><Skeleton className="h-10" /><Skeleton className="h-10" /><Skeleton className="h-10" /></div> : events.length === 0 ? (
        <div className="flex flex-1 flex-col items-center justify-center gap-2 px-6 py-10 text-center">
          <CircleCheck className="size-5 text-success" aria-hidden="true" />
          <p className="text-sm font-semibold">今天没有告警、指令和控制变更</p>
          <p className="text-xs text-muted-foreground">设备数据均为最新</p>
        </div>
      ) : (
        <ul className="flex flex-col py-1">
          {events.map((event) => {
            const body = (
              <>
                <span className="font-mono text-[11px] leading-5 text-subtle-foreground">{formatCompactTime(event.at, timeZone)}</span>
                <span className="mt-[5px] flex justify-center">{event.mark}</span>
                <span className="flex min-w-0 flex-col">
                  <span className="truncate text-[13px] font-semibold">{event.title}</span>
                  <span className="truncate text-xs text-muted-foreground">{event.detail}</span>
                </span>
              </>
            );
            const className = 'grid grid-cols-[52px_12px_minmax(0,1fr)] gap-x-2.5 px-4 py-2 outline-none';
            return (
              <li key={event.key}>
                {event.to ? (
                  <Link to={event.to.path} search={{ site: siteId, inspect: event.to.inspect }} className={cn(className, 'hover:bg-accent focus-visible:bg-accent')}>{body}</Link>
                ) : <div className={className}>{body}</div>}
              </li>
            );
          })}
        </ul>
      )}
      {alarms.isError || commands.isError ? <p className="px-4 pb-3 text-xs text-muted-foreground">{alarms.isError ? '告警' : '指令'}记录暂时读不到</p> : null}
    </Panel>
  );
}

const LOOPS: readonly {
  readonly name: string;
  readonly waterLoop?: WaterLoopKind;
  readonly tone: string;
  readonly main: readonly [string, string];
  readonly others: readonly (readonly [string, string])[];
}[] = [
  { name: '冷冻水', waterLoop: 'CHILLED_WATER', tone: 'bg-chart-1', main: ['温差', 'btu_meter.temperature_difference'], others: [['供', 'btu_meter.supply_water_temperature'], ['回', 'btu_meter.return_water_temperature'], ['流量', 'btu_meter.flow_rate']] },
  { name: '冷却水', waterLoop: 'CONDENSER_WATER', tone: 'bg-chart-2', main: ['与湿球差', 'cooling_tower.approach_temperature'], others: [['出塔', 'cooling_tower.leaving_water_temperature'], ['进塔', 'cooling_tower.entering_water_temperature'], ['流量', 'cwp.flow_rate']] },
  { name: '室内', tone: 'bg-subtle-foreground', main: ['温度', 'zone.temperature'], others: [['宴会厅', 'zone.banquet_temperature']] },
  { name: '室外', tone: 'bg-subtle-foreground', main: ['干球', 'weather.ambient_dry_bulb_temperature'], others: [['湿球', 'weather.ambient_wet_bulb_temperature'], ['相对湿度', 'weather.relative_humidity']] },
];

const BRANCH_DEVICE_KEYS: readonly string[] = [
  'chiller.leaving_chilled_water_temperature',
  'chiller.compressor_load',
  'chiller.power',
  'chwp.frequency',
  'cwp.frequency',
  'vfd.frequency_feedback',
  'chwp.flow_rate',
  'cwp.flow_rate',
  'chwp.power',
  'cwp.power',
  'cooling_tower.fan_speed',
  'cooling_tower.leaving_water_temperature',
  'valve.open_state',
  'valve.position_feedback',
  'dp_sensor.differential_pressure',
  'header.differential_pressure',
  'branch.flow_rate',
  'btu_meter.flow_rate',
  'hvac_meter.active_power',
];

function branchDeviceReadings(device: PlantDevice): readonly PlantReading[] {
  const picked: PlantReading[] = [];
  for (const key of BRANCH_DEVICE_KEYS) {
    const reading = device.reading(key);
    if (reading) picked.push(reading);
    if (picked.length >= 3) break;
  }
  return picked;
}

function branchDeviceBadge(device: PlantDevice): { readonly toneClass: string; readonly label: string } {
  const valveState = device.reading('valve.open_state');
  if (valveState && valveState.state === 'PRESENT' && device.connection !== 'OFFLINE') {
    const open = valveState.display === '全开';
    return {
      toneClass: open ? 'bg-success-soft text-success-text' : 'bg-muted text-muted-foreground',
      label: valveState.display,
    };
  }
  const status = deviceStatus(device);
  const toneClass = status.tone === 'running' || status.tone === 'success'
    ? 'bg-success-soft text-success-text'
    : status.tone === 'alarm'
      ? 'bg-destructive-soft text-destructive-text'
      : 'bg-muted text-muted-foreground';
  return { toneClass, label: status.label };
}

function ParallelBranchCard({ branch, onInspect }: { readonly branch: ParallelBranch; readonly onInspect: (deviceId: string) => void }) {
  const dotTone = branch.waterLoop === 'CONDENSER_WATER' ? 'bg-chart-2' : 'bg-chart-1';
  const summary = branch.equipmentCount > 0
    ? `${branch.runningCount} / ${branch.equipmentCount} 台运行`
    : `${branch.devices.length} 台设备`;
  return (
    <div className="flex flex-col gap-2.5 rounded-lg border border-divider bg-card p-3.5">
      <div className="flex items-center justify-between gap-2">
        <div className="flex min-w-0 items-center gap-2">
          <i className={cn('h-[3px] w-2.5 shrink-0 rounded-full', dotTone)} aria-hidden="true" />
          <span className="truncate text-sm font-semibold">{`支路 ${branch.branchCode}`}</span>
          <span className="shrink-0 rounded bg-muted px-1.5 py-0.5 text-[11px] font-medium text-secondary-foreground">{branch.loopLabel}</span>
        </div>
        <div className="flex shrink-0 items-center gap-2 text-xs text-muted-foreground tabular-nums">
          {branch.hasAlert ? <span className="rounded bg-warning-soft px-1.5 py-0.5 text-[11px] font-medium text-warning-text">需关注</span> : null}
          <span>{summary}</span>
        </div>
      </div>
      <div className="flex flex-col divide-y divide-divider">
        {branch.devices.map((device) => {
          const badge = branchDeviceBadge(device);
          const readings = branchDeviceReadings(device);
          return (
            <button
              key={device.deviceId}
              type="button"
              onClick={() => onInspect(device.deviceId)}
              className="flex w-full cursor-pointer items-center justify-between gap-3 rounded-md px-1 py-2 text-left transition-colors outline-none first:pt-1 last:pb-1 hover:bg-accent/50 focus-visible:ring-3 focus-visible:ring-ring-soft"
            >
              <div className="flex min-w-0 items-center gap-2">
                <span className="truncate text-xs font-medium">{device.name}</span>
                <span className={cn('shrink-0 rounded px-1.5 py-0.5 text-[11px] leading-4 font-medium', badge.toneClass)}>{badge.label}</span>
              </div>
              <div className="flex shrink-0 flex-wrap items-center justify-end gap-x-3 gap-y-0.5 text-xs">
                {readings.length === 0 ? <span className="text-muted-foreground">—</span> : readings.map((reading) => (
                  <span key={reading.label} className="whitespace-nowrap">
                    <span className="text-muted-foreground">{reading.label} </span>
                    <ReadingValue reading={reading} />
                  </span>
                ))}
              </div>
            </button>
          );
        })}
      </div>
    </div>
  );
}

function ParallelBranchesPanel({ plant, onInspect }: { readonly plant: PlantView; readonly onInspect: (deviceId: string) => void }) {
  if (plant.branches.length === 0) return null;
  return (
    <Panel role="region" aria-label="水力并联支路">
      <PanelHeader title="水力并联支路" action={<span className="text-xs text-muted-foreground tabular-nums">{plant.branches.length} 条支路</span>} />
      <div className="grid grid-cols-1 gap-3 p-4 @4xl/main:grid-cols-2">
        {plant.branches.map((branch) => (
          <ParallelBranchCard key={branch.key} branch={branch} onInspect={onInspect} />
        ))}
      </div>
    </Panel>
  );
}

/** The two water loops and the air they serve, each led by the reading an operator checks first. */
function LoopsTable({ plant, timeZone }: { readonly plant: PlantView; readonly timeZone: string }) {
  const history = usePlantHistory(plant, LOOPS.map((loop) => loop.main[1]), 6);
  const slots = useMemo(
    () => buildTimeSlots({ fromMs: history.fromMs, toMs: history.toMs, granularity: 'QUARTER_HOUR', timeZone }),
    [history.fromMs, history.toMs, timeZone],
  );
  const rows = LOOPS.map((loop) => ({ loop, reading: plantReading(plant, loop.main[1]) })).filter((row) => row.reading !== null);
  if (rows.length === 0) return null;
  return (
    <Panel role="region" aria-label="水系统与室内外环境">
      <PanelHeader title="水系统与室内外环境" />
      <div role="table" aria-label="水系统与室内外环境" className="grid grid-cols-[110px_170px_minmax(0,1fr)_150px_148px]">
        <div role="row" className="contents text-xs font-medium text-muted-foreground">
          {['系统', '主读数', '其他读数', '近 6 小时', '数据时间'].map((title, index) => (
            <span key={title} role="columnheader" className={cn('flex h-9 items-center border-b bg-muted px-4', index === 4 && 'justify-end')}>{title}</span>
          ))}
        </div>
        {rows.map(({ loop, reading }) => {
          const stale = reading ? !reading.current : false;
          const recorded = history.series.get(loop.main[1]) ?? [];
          const values = fillGaugeSlots(slots, recorded).map((slot) => slot.average);
          const loopDp = loop.waterLoop ? loopDifferentialPressure(plant, loop.waterLoop) : null;
          const branchCount = loop.waterLoop ? (plant.loopBranches[loop.waterLoop] ?? 0) : 0;
          const extraOthers: readonly (readonly [string, PlantReading | null])[] = [
            ...loop.others.map(([label, key]) => [label, plantReading(plant, key)] as const),
            ...(loopDp ? [['压差', loopDp] as const] : []),
          ];
          return (
            <div role="row" key={loop.name} className="contents text-sm">
              <span role="cell" className="flex h-[42px] items-center gap-2 border-b border-divider px-4 font-semibold">
                <i className={cn('h-[3px] w-2.5 rounded-full', loop.tone)} aria-hidden="true" />
                {loop.name}
              </span>
              <span role="cell" className="flex h-[42px] items-center gap-1.5 border-b border-divider px-4">
                <span className="text-xs text-muted-foreground">{loop.main[0]}</span>
                <span className="text-[15px] font-semibold"><ReadingValue reading={reading} /></span>
              </span>
              <span role="cell" className={cn('flex h-[42px] min-w-0 items-center gap-1 truncate border-b border-divider px-4 text-xs', stale ? 'text-muted-foreground' : 'text-secondary-foreground')}>
                {branchCount > 0 ? <span className="mr-1.5 shrink-0 rounded bg-muted px-1.5 py-0.5 text-[11px] font-medium text-secondary-foreground tabular-nums">{branchCount} 条并联支路</span> : null}
                {extraOthers.length === 0 ? '—' : extraOthers.map(([label, itemReading], index) => (
                  <span key={label} className="whitespace-nowrap">{index > 0 ? ' · ' : ''}{label} <ReadingValue reading={itemReading} /></span>
                ))}
              </span>
              <span role="cell" className="flex h-[42px] items-center border-b border-divider px-4">
                {recorded.length > 1 ? <Sparkline data={values} variant="line" animate={false} color={stale ? 'var(--muted-foreground)' : 'var(--chart-1)'} className="h-6 w-full" /> : <span className="text-xs text-subtle-foreground">—</span>}
              </span>
              <span role="cell" className={cn('flex h-[42px] items-center justify-end gap-1.5 border-b border-divider px-4 font-mono text-xs whitespace-nowrap', stale ? 'text-warning-text' : 'text-secondary-foreground')}>
                {stale ? <span className="shrink-0 whitespace-nowrap rounded-md bg-warning-soft px-1.5 font-sans text-[11px] leading-[18px] font-medium">读数过期</span> : null}
                {formatClock(reading?.sampledAt ?? null, timeZone)}
              </span>
            </div>
          );
        })}
      </div>
    </Panel>
  );
}

export function RealtimeWorkspace() {
  const { principal } = siteRoute.useRouteContext();
  const { site, plant, mode, registry, current, currentUnavailable, refresh } = useRealtimePlant();
  const queryClient = useQueryClient();
  const search = pageRoute.useSearch();
  const navigate = useNavigate({ from: '/operations/realtime' });
  const loading = registry.isPending || (current.isPending && current.fetchStatus !== 'idle');
  const equipment = equipmentStatus(plant);
  const controlState = useQuery(siteControlStateQuery(site.id));
  const lockCount = activeLocks(controlState.data).size;
  const inspected = search.inspect ? plant.devices.find((device) => device.deviceId === search.inspect) : undefined;
  const setSearch = (patch: { metric?: Metric; window?: Window; inspect?: string }) => void navigate({ search: (previous) => ({ ...previous, ...patch }), replace: true });

  return (
    <Main>
      <PageHeader
        title="实时运行"
        description={site.displayName}
        meta={<LiveIndicator mode={mode} />}
        actions={(
          <>
            {plant.latestSampleAt ? <span className="text-xs text-muted-foreground">数据时间 <span className="font-mono">{formatClock(plant.latestSampleAt, site.timezone)}</span></span> : null}
            {equipment.total > 0 ? (
              <Button variant="ghost" asChild className="text-secondary-foreground">
                <Link to="/operations/systems-devices" search={{ site: site.id }}>
                  <span className={cn('size-[7px] rounded-full', equipment.running === equipment.total ? 'bg-success' : 'bg-warning')} aria-hidden="true" />
                  主要设备 {equipment.running} / {equipment.total} 运行
                  <ChevronRight className="size-3.5 text-subtle-foreground" aria-hidden="true" />
                </Link>
              </Button>
            ) : null}
            {lockCount > 0 ? (
              <Button variant="ghost" asChild className="text-secondary-foreground">
                <Link to="/operations/control" search={{ site: site.id }}>
                  <Lock className="size-3.5 text-warning" aria-hidden="true" />
                  {lockCount} 台锁定
                  <ChevronRight className="size-3.5 text-subtle-foreground" aria-hidden="true" />
                </Link>
              </Button>
            ) : null}
            <Button variant="outline" onClick={() => { refresh(); void queryClient.invalidateQueries({ queryKey: dataGapsKeys.site(site.id) }); }}>
              <RefreshCw aria-hidden="true" data-icon="inline-start" />刷新
            </Button>
          </>
        )}
      />
      <DataGapBanner siteId={site.id} timeZone={site.timezone} />

      {registry.isError ? (
        <p className="rounded-lg border border-destructive-soft bg-destructive-soft/40 px-4 py-3 text-sm text-destructive-text">设备清单暂时读不到：{registry.error.message}</p>
      ) : null}
      {currentUnavailable ? (
        <p className="rounded-lg border border-warning-soft bg-warning-soft/50 px-4 py-3 text-sm text-warning-text">当前读数暂时读不到，恢复连接后会自动更新。</p>
      ) : null}

      {loading ? <Skeleton className="h-96 rounded-xl" /> : null}

      {!loading && registry.isSuccess && plant.devices.length === 0 ? (
        <Panel className="items-center gap-2 px-6 py-12 text-center">
          <p className="text-sm font-semibold">该站点还没有登记设备</p>
          <p className="text-xs text-muted-foreground">在 系统管理 → 设备与点位登记 登记冷站设备和点位后，这里会显示实时运行情况</p>
        </Panel>
      ) : null}

      {!loading && plant.devices.length > 0 ? (
        <>
          <div className="grid items-stretch gap-4 @5xl/main:grid-cols-[minmax(0,1fr)_380px]">
            <PlantTrendPanel
              plant={plant}
              metric={search.metric ?? 'cop'}
              window={search.window ?? '6h'}
              onMetric={(metric) => setSearch({ metric })}
              onWindow={(window) => setSearch({ window })}
              timeZone={site.timezone}
            />
            <RecentEvents plant={plant} siteId={site.id} myId={principal.principalId} timeZone={site.timezone} assetModel={registry.data?.assetModel} />
          </div>
          <PlantHydraulicSchematic
            plant={plant}
            siteId={site.id}
            selectedDeviceId={search.inspect}
            onInspect={(deviceId) => setSearch({ inspect: deviceId })}
          />
          <LoopsTable plant={plant} timeZone={site.timezone} />
          <ParallelBranchesPanel plant={plant} onInspect={(deviceId) => setSearch({ inspect: deviceId })} />
        </>
      ) : null}

      <DeviceInspector
        device={inspected}
        categoryLabel={inspected ? PLANT_CATEGORY_LABELS[inspected.category] : ''}
        onClose={() => setSearch({ inspect: undefined })}
      />
    </Main>
  );
}
