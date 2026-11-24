import { lazy, memo, Suspense, useCallback, useEffect, useMemo, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { getRouteApi, Link, useNavigate } from '@tanstack/react-router';
import { ChevronRight, Lock, RefreshCw } from 'lucide-react';
import { PageHeader } from '@/blocks/page-header';
import { Panel, PanelHeader } from '@/blocks/panel';
import { InfoTip } from '@/components/info-tip';
import { Main } from '@/components/layout/Main';
import { Button } from '@/components/ui/button';
import { ComposedChart } from '@/components/ui/composed-chart';
import { Skeleton } from '@/components/ui/skeleton';
import { Sparkline } from '@/components/ui/sparkline';
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group';
import { activeLocks, siteControlStateQuery } from '@/features/control/control-state';
import { DataGapBanner } from '@/features/data-gaps/DataGapBanner';
import { dataGapsKeys } from '@/features/data-gaps/query';
import { formatDecimal, getDateTimeFormatter } from '@/lib/operator-format';
import { cn } from '@/lib/utils';
import { coolingUncertaintyText, copUncertaintyText, equipmentStatus, findPlantUnit, loopDifferentialPressure, PLANT_CATEGORY_LABELS, plantReading, probeOffsetText, type ParallelBranch, type PlantDevice, type PlantReading, type PlantView, type WaterLoopKind } from '../plant-model';
import { useRealtimePlant } from '../use-realtime-plant';
import { usePlantHistory } from '../use-plant-history';
import { copSeries, COOLING_KEY, hourAgo, POWER_KEY } from '../plant-trend';
import { deviceStatus, formatClock, LiveIndicator, LowConfidenceBadge, ReadingMetric, ReadingValue } from '../device-presentation';
import { buildTimeSlots, fillGaugeSlots, fillScalarSlots } from '../../trends/timeline';
import { PlantHydraulicSchematic } from './PlantHydraulicSchematic';
import { RecentEvents } from './RecentEventsPanel';

const LazyDeviceInspector = lazy(() =>
  import('../../systems-devices/components/DeviceInspector').then((mod) => ({ default: mod.DeviceInspector })),
);

const pageRoute = getRouteApi('/_app/_site/operations/realtime');
const siteRoute = getRouteApi('/_app/_site');

type Metric = 'power' | 'cooling' | 'cop';
type Window = '6h' | '24h';

const PLANT_TREND_KEYS: readonly string[] = [POWER_KEY, COOLING_KEY];
const METRIC_TITLES: Readonly<Record<Metric, string>> = { power: '冷站总功率', cooling: '瞬时制冷量', cop: '冷站 COP' };
const METRIC_UNITS: Readonly<Record<Metric, string>> = { power: 'kW', cooling: 'kW', cop: '' };

const MetricTab = memo(function MetricTab({ label, selected, onSelect, value, unit, values, hasTrend, caption, info }: {
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
});

const PlantHistoryChart = memo(function PlantHistoryChart({
  slots,
  metric,
  title,
  fromMs,
  toMs,
  timeZone,
}: {
  readonly slots: ReturnType<typeof fillScalarSlots>;
  readonly metric: Metric;
  readonly title: string;
  readonly fromMs: number;
  readonly toMs: number;
  readonly timeZone: string;
}) {
  const digits = metric === 'cop' ? 2 : 1;
  const chartData = useMemo(
    () => slots.map((slot) => ({
      at: slot.startMs,
      value: slot.value === null ? null : Number(slot.value.toFixed(digits)),
    })),
    [digits, slots],
  );
  const xDomain = useMemo<readonly [number, number]>(() => [fromMs, toMs], [fromMs, toMs]);
  const chartSeries = useMemo(
    () => [{ key: 'value', label: title, type: 'area' as const, color: 'var(--chart-1)' }],
    [title],
  );
  const xFormatter = useCallback((value: unknown) => {
    const ms = typeof value === 'string' && value.trim() === '' ? NaN : Number(value);
    if (!Number.isFinite(ms)) return '';
    return getDateTimeFormatter('zh-CN', { timeZone, hour: '2-digit', minute: '2-digit', hour12: false }).format(new Date(ms));
  }, [timeZone]);
  const yFormatter = useCallback((value: number) => formatDecimal(value, metric === 'cop' ? 2 : 0), [metric]);

  return (
    <ComposedChart
      data={chartData}
      xKey="at"
      xType="number"
      xDomain={xDomain}
      xFormatter={xFormatter}
      series={chartSeries}
      showGrid
      showYAxis
      yDomain={metric === 'cop' ? 'auto' : 'zero'}
      yFormatter={yFormatter}
      animate={false}
      className="h-[230px]"
    />
  );
});

const PlantTrendPanel = memo(function PlantTrendPanel({ plant, metric, window, onMetric, onWindow, timeZone }: {
  readonly plant: PlantView;
  readonly metric: Metric;
  readonly window: Window;
  readonly onMetric: (metric: Metric) => void;
  readonly onWindow: (window: Window) => void;
  readonly timeZone: string;
}) {
  const history = usePlantHistory(plant, PLANT_TREND_KEYS, window === '24h' ? 24 : 6);
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
  const powerValues = useMemo(() => filled.power.map((point) => point.value), [filled.power]);
  const coolingValues = useMemo(() => filled.cooling.map((point) => point.value), [filled.cooling]);
  const copValues = useMemo(() => filled.cop.map((point) => point.value), [filled.cop]);
  const selectPower = useCallback(() => onMetric('power'), [onMetric]);
  const selectCooling = useCallback(() => onMetric('cooling'), [onMetric]);
  const selectCop = useCallback(() => onMetric('cop'), [onMetric]);
  const selected = series[metric];
  const selectedSlots = filled[metric];
  const powerAgo = hourAgo(series.power);
  const coolingAgo = hourAgo(series.cooling);
  const copPeak = series.cop.length > 0 ? Math.max(...series.cop.map((point) => point.value)) : null;

  return (
    <Panel role="region" aria-label="冷站运行">
      <div role="tablist" aria-label="冷站指标" className="flex border-b border-divider">
        <MetricTab
          label="冷站总功率"
          selected={metric === 'power'}
          onSelect={selectPower}
          value={<ReadingMetric reading={plant.totalPower} digits={1} unit="kW" />}
          values={powerValues}
          hasTrend={series.power.length > 1}
          caption={powerAgo === null ? '空调总电表' : `1 小时前 ${formatDecimal(powerAgo, 1)} kW`}
        />
        <MetricTab
          label="瞬时制冷量"
          selected={metric === 'cooling'}
          onSelect={selectCooling}
          value={<ReadingMetric reading={plant.coolingCapacity} digits={0} unit="kW" />}
          values={coolingValues}
          hasTrend={series.cooling.length > 1}
          caption={[coolingAgo === null ? '冷量表' : `1 小时前 ${formatDecimal(coolingAgo, 0)} kW`, coolingUncertaintyText(plant.coolingCapacity)].filter(Boolean).join(' · ')}
        />
        <MetricTab
          label="冷站 COP"
          selected={metric === 'cop'}
          onSelect={selectCop}
          value={plant.plantCop === null ? <span className="text-muted-foreground">—</span> : <>{formatDecimal(plant.plantCop, 2)}{plant.plantCopLowConfidence ? <LowConfidenceBadge /> : null}</>}
          values={copValues}
          hasTrend={series.cop.length > 1}
          caption={[copPeak === null ? '制冷量 ÷ 总功率' : `近 ${window === '24h' ? 24 : 6} 小时最高 ${formatDecimal(copPeak, 2)}`, copUncertaintyText(plant)].filter(Boolean).join(' · ')}
          info="冷站瞬时制冷量 ÷ 冷站总功率。温差相对探头误差过小时照常计算，标「低可信」并给出区间，这段不计入能效均值、故障诊断和节能验证。任一读数过期或不可信时不计算。"
        />
      </div>
      {probeOffsetText(plant) ? <p className="border-b border-divider px-5 py-2 text-xs text-muted-foreground">{probeOffsetText(plant)}</p> : null}
      <div className="flex flex-col gap-2 px-5 pt-3.5 pb-3">
        <div className="flex items-center gap-2.5">
          <span className="text-sm font-semibold">{METRIC_TITLES[metric]}</span>
          <span className="text-xs text-muted-foreground">近 {window === '24h' ? 24 : 6} 小时 · 15 分钟平均{METRIC_UNITS[metric] ? ` · ${METRIC_UNITS[metric]}` : ''}</span>
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
        {!history.allowed ? (
          <p className="flex h-[230px] items-center justify-center text-sm text-muted-foreground">当前账号没有历史读数查看权限</p>
        ) : history.isPending ? <Skeleton className="h-[230px]" /> : history.isError ? (
          <div className="flex h-[230px] flex-col items-center justify-center gap-2 text-sm text-muted-foreground">
            <span>历史读数暂时读不到</span>
            <Button variant="outline" size="sm" onClick={history.refetch}>重试</Button>
          </div>
        ) : selected.length < 2 ? (
          <p className="flex h-[230px] items-center justify-center text-sm text-muted-foreground">这段时间没有记录</p>
        ) : (
          <PlantHistoryChart
            slots={selectedSlots}
            metric={metric}
            title={METRIC_TITLES[metric]}
            fromMs={history.fromMs}
            toMs={history.toMs}
            timeZone={timeZone}
          />
        )}
      </div>
    </Panel>
  );
});

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
              key={device.unitId}
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

const LOOP_SOURCE_KEYS: readonly string[] = LOOPS.map((loop) => loop.main[1]);
const LOOP_HEADERS = ['系统', '主读数', '其他读数', '近 6 小时', '数据时间'] as const;

const LoopSparklineCell = memo(function LoopSparklineCell({
  slots,
  recorded,
  stale,
}: {
  readonly slots: ReturnType<typeof buildTimeSlots>;
  readonly recorded: Parameters<typeof fillGaugeSlots>[1];
  readonly stale: boolean;
}) {
  const values = useMemo(() => fillGaugeSlots(slots, recorded).map((slot) => slot.average), [slots, recorded]);
  if (recorded.length <= 1) return <span className="text-xs text-subtle-foreground">—</span>;
  return (
    <Sparkline
      data={values}
      variant="line"
      animate={false}
      color={stale ? 'var(--muted-foreground)' : 'var(--chart-1)'}
      className="h-6 w-full"
    />
  );
});

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

const ParallelBranchesSection = memo(ParallelBranchesPanel);

/** The two water loops and the air they serve, each led by the reading an operator checks first. */
const LoopsTable = memo(function LoopsTable({ plant, timeZone }: { readonly plant: PlantView; readonly timeZone: string }) {
  const history = usePlantHistory(plant, LOOP_SOURCE_KEYS, 6);
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
          {LOOP_HEADERS.map((title, index) => (
            <span key={title} role="columnheader" className={cn('flex h-9 items-center border-b bg-muted px-4', index === 4 && 'justify-end')}>{title}</span>
          ))}
        </div>
        {rows.map(({ loop, reading }) => {
          const stale = reading ? !reading.current : false;
          const recorded = history.series.get(loop.main[1]) ?? [];
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
                <LoopSparklineCell slots={slots} recorded={recorded} stale={stale} />
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
});

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
  const inspected = findPlantUnit(plant, search.inspect);
  const [inspectorMounted, setInspectorMounted] = useState(() => Boolean(search.inspect));
  useEffect(() => {
    if (search.inspect) setInspectorMounted(true);
  }, [search.inspect]);
  const setSearch = useCallback(
    (patch: { metric?: Metric; window?: Window; inspect?: string }) => void navigate({ search: (previous) => ({ ...previous, ...patch }), replace: true }),
    [navigate],
  );
  const handleInspect = useCallback((deviceId: string) => setSearch({ inspect: deviceId }), [setSearch]);
  const handleMetric = useCallback((metric: Metric) => setSearch({ metric }), [setSearch]);
  const handleWindow = useCallback((window: Window) => setSearch({ window }), [setSearch]);
  const handleCloseInspector = useCallback(() => setSearch({ inspect: undefined }), [setSearch]);
  const canListAlarms = principal.authorization.capabilities.includes('alarm.list');

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
              onMetric={handleMetric}
              onWindow={handleWindow}
              timeZone={site.timezone}
            />
            <RecentEvents
              plant={plant}
              siteId={site.id}
              myId={principal.principalId}
              timeZone={site.timezone}
              assetModel={registry.data?.assetModel}
              canListAlarms={canListAlarms}
              onInspect={handleInspect}
            />
          </div>
          <PlantHydraulicSchematic
            plant={plant}
            siteId={site.id}
            selectedDeviceId={search.inspect}
            onInspect={handleInspect}
          />
          <LoopsTable plant={plant} timeZone={site.timezone} />
          <ParallelBranchesSection plant={plant} onInspect={handleInspect} />
        </>
      ) : null}

      {inspectorMounted ? (
        <Suspense fallback={null}>
          <LazyDeviceInspector
            device={inspected}
            categoryLabel={inspected ? PLANT_CATEGORY_LABELS[inspected.category] : ''}
            onClose={handleCloseInspector}
          />
        </Suspense>
      ) : null}
    </Main>
  );
}
