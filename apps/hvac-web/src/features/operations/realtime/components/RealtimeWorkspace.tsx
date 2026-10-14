import { useMemo } from 'react';
import { useQuery } from '@tanstack/react-query';
import { getRouteApi, Link, useNavigate } from '@tanstack/react-router';
import { ChevronRight, CircleCheck, RefreshCw } from 'lucide-react';
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
import { commandLabels, controlTargets } from '@/features/control/control-model';
import { formatCompactTime, formatDecimal } from '@/lib/operator-format';
import { cn } from '@/lib/utils';
import type { HourlyPoint } from '../../systems-devices/device-history';
import { equipmentStatus, plantReading, type PlantReading, type PlantView } from '../plant-model';
import { useRealtimePlant } from '../use-realtime-plant';
import { usePlantHistory } from '../use-plant-history';
import { formatClock, LiveIndicator, ReadingMetric, ReadingValue } from '../device-presentation';

const pageRoute = getRouteApi('/_app/_site/operations/realtime');

type Metric = 'power' | 'cooling' | 'cop';
type Window = '6h' | '24h';

const POWER_KEY = 'hvac_meter.active_power';
const COOLING_KEY = 'btu_meter.instant_cooling_capacity';
const HOUR_MS = 3_600_000;

/** COP per quarter: cooling over power where both were recorded and power is positive. */
function copSeries(power: readonly HourlyPoint[], cooling: readonly HourlyPoint[]): { periodStart: string; value: number }[] {
  const coolingAt = new Map(cooling.map((point) => [point.periodStart, point.average]));
  return power
    .filter((point) => point.average > 0 && coolingAt.has(point.periodStart))
    .map((point) => ({ periodStart: point.periodStart, value: coolingAt.get(point.periodStart)! / point.average }));
}

/** The value an hour ago: the last quarter that started at least an hour before now. */
function hourAgo(points: readonly { periodStart: string; value: number }[]): number | null {
  const cutoff = Date.now() - HOUR_MS;
  const before = points.filter((point) => Date.parse(point.periodStart) <= cutoff);
  return before.length > 0 ? before[before.length - 1].value : null;
}

function MetricTab({ label, selected, onSelect, value, unit, values, caption, info }: {
  readonly label: string;
  readonly selected: boolean;
  readonly onSelect: () => void;
  readonly value: React.ReactNode;
  readonly unit?: string;
  readonly values: readonly number[];
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
        {values.length > 1 ? (
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
  const clock = new Intl.DateTimeFormat('zh-CN', { timeZone, hour: '2-digit', minute: '2-digit', hour12: false });
  const selected = series[metric];
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
          values={series.power.map((point) => point.value)}
          caption={powerAgo === null ? '空调总电表' : `1 小时前 ${formatDecimal(powerAgo, 1)} kW`}
        />
        <MetricTab
          label="瞬时制冷量"
          selected={metric === 'cooling'}
          onSelect={() => onMetric('cooling')}
          value={<ReadingMetric reading={plant.coolingCapacity} digits={0} unit="kW" />}
          values={series.cooling.map((point) => point.value)}
          caption={coolingAgo === null ? '冷量表' : `1 小时前 ${formatDecimal(coolingAgo, 0)} kW`}
        />
        <MetricTab
          label="冷站 COP"
          selected={metric === 'cop'}
          onSelect={() => onMetric('cop')}
          value={plant.plantCop === null ? <span className="text-muted-foreground">—</span> : formatDecimal(plant.plantCop, 2)}
          values={series.cop.map((point) => point.value)}
          caption={copPeak === null ? '制冷量 ÷ 总功率' : `近 ${window === '24h' ? 24 : 6} 小时最高 ${formatDecimal(copPeak, 2)}`}
          info="冷站瞬时制冷量 ÷ 冷站总功率；任一读数过期时不计算"
        />
      </div>
      <div className="flex flex-col gap-2 px-5 pt-3.5 pb-3">
        <div className="flex items-center gap-2.5">
          <span className="text-sm font-semibold">{titles[metric]}</span>
          <span className="text-xs text-muted-foreground">近 {window === '24h' ? 24 : 6} 小时 · 15 分钟平均{units[metric] ? ` · ${units[metric]}` : ''}</span>
          <ToggleGroup
            type="single"
            variant="outline"
            value={window}
            onValueChange={(value) => { if (value) onWindow(value as Window); }}
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
            data={selected.map((point) => ({ time: clock.format(new Date(point.periodStart)), value: Number(point.value.toFixed(metric === 'cop' ? 2 : 1)) }))}
            xKey="time"
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
function RecentEvents({ plant, siteId, timeZone, assetModel }: {
  readonly plant: PlantView;
  readonly siteId: string;
  readonly timeZone: string;
  readonly assetModel: Parameters<typeof controlTargets>[0] | undefined;
}) {
  const alarms = useQuery(alarmHistoryQuery(siteId));
  const commands = useQuery(siteCommandsQuery(siteId));
  const labels = useMemo(() => (assetModel ? commandLabels(controlTargets(assetModel, new Date())) : new Map()), [assetModel]);
  const deviceName = (deviceId: string | undefined) => (deviceId ? plant.devices.find((device) => device.deviceId === deviceId)?.name ?? '设备' : '站点');
  const today = new Intl.DateTimeFormat('en-CA', { timeZone, year: 'numeric', month: '2-digit', day: '2-digit' }).format(new Date());
  const isToday = (value: string) => new Intl.DateTimeFormat('en-CA', { timeZone, year: 'numeric', month: '2-digit', day: '2-digit' }).format(new Date(value)) === today;

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
    ...plant.devices.filter((device) => device.hasStaleData || device.connection === 'OFFLINE').map((device): PlantEvent => ({
      key: `data-${device.deviceId}`,
      at: device.latestSampleAt ?? new Date().toISOString(),
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
          <p className="text-sm font-semibold">今天没有告警和指令</p>
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
  readonly tone: string;
  readonly main: readonly [string, string];
  readonly others: readonly (readonly [string, string])[];
}[] = [
  { name: '冷冻水', tone: 'bg-chart-1', main: ['温差', 'btu_meter.temperature_difference'], others: [['供', 'btu_meter.supply_water_temperature'], ['回', 'btu_meter.return_water_temperature'], ['流量', 'btu_meter.flow_rate']] },
  { name: '冷却水', tone: 'bg-chart-2', main: ['与湿球差', 'cooling_tower.approach_temperature'], others: [['出塔', 'cooling_tower.leaving_water_temperature'], ['进塔', 'cooling_tower.entering_water_temperature'], ['流量', 'cwp.flow_rate']] },
  { name: '室内', tone: 'bg-subtle-foreground', main: ['温度', 'zone.temperature'], others: [] },
  { name: '室外', tone: 'bg-subtle-foreground', main: ['干球', 'weather.ambient_dry_bulb_temperature'], others: [['湿球', 'weather.ambient_wet_bulb_temperature'], ['相对湿度', 'weather.relative_humidity']] },
];

/** The two water loops and the air they serve, each led by the reading an operator checks first. */
function LoopsTable({ plant, timeZone }: { readonly plant: PlantView; readonly timeZone: string }) {
  const history = usePlantHistory(plant, LOOPS.map((loop) => loop.main[1]), 6);
  const rows = LOOPS.map((loop) => ({ loop, reading: plantReading(plant, loop.main[1]) })).filter((row) => row.reading !== null);
  if (rows.length === 0) return null;
  return (
    <Panel role="region" aria-label="水系统与室内外环境">
      <PanelHeader title="水系统与室内外环境" />
      <div role="table" aria-label="水系统与室内外环境" className="grid grid-cols-[110px_170px_minmax(0,1fr)_150px_96px]">
        <div role="row" className="contents text-xs font-medium text-muted-foreground">
          {['系统', '主读数', '其他读数', '近 6 小时', '数据时间'].map((title, index) => (
            <span key={title} role="columnheader" className={cn('flex h-9 items-center border-b bg-muted px-4', index === 4 && 'justify-end')}>{title}</span>
          ))}
        </div>
        {rows.map(({ loop, reading }) => {
          const stale = reading ? !reading.current : false;
          const values = (history.series.get(loop.main[1]) ?? []).map((point) => point.average);
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
                {loop.others.length === 0 ? '—' : loop.others.map(([label, key], index) => (
                  <span key={key} className="whitespace-nowrap">{index > 0 ? ' · ' : ''}{label} <ReadingValue reading={plantReading(plant, key) as PlantReading | null} /></span>
                ))}
              </span>
              <span role="cell" className="flex h-[42px] items-center border-b border-divider px-4">
                {values.length > 1 ? <Sparkline data={values} variant="line" animate={false} color={stale ? 'var(--muted-foreground)' : 'var(--chart-1)'} className="h-6 w-full" /> : <span className="text-xs text-subtle-foreground">—</span>}
              </span>
              <span role="cell" className={cn('flex h-[42px] items-center justify-end gap-1.5 border-b border-divider px-4 font-mono text-xs', stale ? 'text-warning-text' : 'text-secondary-foreground')}>
                {stale ? <span className="rounded-md bg-warning-soft px-1.5 font-sans text-[11px] leading-[18px] font-medium">读数过期</span> : null}
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
  const { site, plant, mode, registry, current, currentUnavailable, refresh } = useRealtimePlant();
  const search = pageRoute.useSearch();
  const navigate = useNavigate({ from: '/operations/realtime' });
  const loading = registry.isPending || (current.isPending && current.fetchStatus !== 'idle');
  const equipment = equipmentStatus(plant);
  const setSearch = (patch: { metric?: Metric; window?: Window }) => void navigate({ search: (previous) => ({ ...previous, ...patch }), replace: true });

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
            <Button variant="outline" onClick={refresh}>
              <RefreshCw aria-hidden="true" data-icon="inline-start" />刷新
            </Button>
          </>
        )}
      />

      {registry.isError ? (
        <p className="rounded-lg border border-destructive-soft bg-destructive-soft/40 px-4 py-3 text-sm text-destructive-text">设备清单暂时读不到：{registry.error.message}</p>
      ) : null}
      {currentUnavailable ? (
        <p className="rounded-lg border border-warning-soft bg-warning-soft/50 px-4 py-3 text-sm text-warning-text">当前读数暂时读不到；页面不显示推测的数值。</p>
      ) : null}

      {loading ? <Skeleton className="h-96 rounded-xl" /> : null}

      {!loading && registry.isSuccess && plant.devices.length === 0 ? (
        <Panel className="items-center gap-2 px-6 py-12 text-center">
          <p className="text-sm font-semibold">该站点还没有登记设备</p>
          <p className="text-xs text-muted-foreground">在 系统管理 → 资产与点位 登记冷站设备和点位后，这里会显示实时运行情况</p>
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
            <RecentEvents plant={plant} siteId={site.id} timeZone={site.timezone} assetModel={registry.data?.assetModel} />
          </div>
          <LoopsTable plant={plant} timeZone={site.timezone} />
        </>
      ) : null}
    </Main>
  );
}
