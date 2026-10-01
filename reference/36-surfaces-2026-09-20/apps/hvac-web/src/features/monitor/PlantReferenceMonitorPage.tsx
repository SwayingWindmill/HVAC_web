import { useMemo, type ReactNode } from 'react';
import type { ColumnDef } from '@tanstack/react-table';
import {
  AlertOutlined,
  BulbOutlined,
  ControlOutlined,
  DashboardOutlined,
  ExperimentOutlined,
  ThunderboltOutlined,
} from '@/shared/icons';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardAction, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { DataTable, type DataTableFeatures } from '@/components/data-table';
import { useDataTable } from '@/hooks/use-data-table';
import { StatusPillBadge } from '@/components/ui/status-pill-badge';
import type { Alarm } from '@/api/alarms';
import type { DashboardOverview } from '@/api/dashboard-overview';
import type { Site, SiteDashboardSummary } from '@/api/generated/platformGateway.gen';
import { siteRoute } from '@/app/router-paths';
import { DualAxisTimeSeriesChart } from '@/shared/charts/TimeSeriesChart';
import {
  alarmSeverityBadgeClass,
  alarmSeverityLabel,
  formatMonitorMetric,
  monitorRunState,
  monitorRunStateBadgeClass,
  monitorRunStateLabel,
  numericPoint,
  type MonitorDevice,
} from './model';
import { CHILLED_PUMP_ART, CHILLER_ART, COOLING_PUMP_ART, TOWER_ART } from './reference-equipment-art';
import './plant-reference.css';

const RT_TO_KW = 3.517;

interface PlantReferenceMonitorPageProps {
  readonly site: Readonly<Site>;
  readonly devices: readonly MonitorDevice[];
  readonly alarms: readonly Alarm[];
  readonly summary: SiteDashboardSummary | undefined;
  readonly overview: DashboardOverview | undefined;
  readonly onOpenOverview: () => void;
  readonly onOpenDevice: (deviceId: string) => void;
  readonly onOpenAnalysis: (deviceId?: string) => void;
  readonly onOpenModes: () => void;
  readonly onOpenTerminal: () => void;
  readonly onOpenEnergy: () => void;
}

interface PlantKpiProps {
  readonly icon: ReactNode;
  readonly tone: 'blue' | 'purple' | 'green';
  readonly label: string;
  readonly value: ReactNode;
  readonly onClick: () => void;
}

interface ComparisonRow {
  readonly key: string;
  readonly type: string;
  readonly label: string;
  readonly state: ReactNode;
  readonly operating: string;
  readonly device?: MonitorDevice;
}

function PlantPanel({ title, action, children, className }: { readonly title: ReactNode; readonly action?: ReactNode; readonly children: ReactNode; readonly className?: string }) {
  return (
    <Card className={className}>
      <CardHeader className="border-b pb-3">
        <CardTitle>{title}</CardTitle>
        {action ? <CardAction>{action}</CardAction> : null}
      </CardHeader>
      <CardContent>{children}</CardContent>
    </Card>
  );
}

function formatTime(value: string, timeZone: string): string {
  return new Intl.DateTimeFormat('zh-CN', {
    timeZone,
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  }).format(new Date(value));
}

function shortEquipmentName(name: string): string {
  const token = name.match(/\b(?:CH|CT|CWP|PMP|AHU)-?\d+\b/i)?.[0];
  return token ?? name;
}

function pointText(item: MonitorDevice, keywords: readonly string[], digits = 1): string {
  const point = numericPoint(item.state.points, keywords);
  if (!point) return '—';
  return formatMonitorMetric(point.value, point.point.unit, digits);
}

function PlantKpi({ icon, tone, label, value, onClick }: PlantKpiProps) {
  return (
    <button type="button" className={`hvac-plant-ref__kpi is-${tone}`} onClick={onClick}>
      <span className="hvac-plant-ref__kpi-icon">{icon}</span>
      <span><small>{label}</small><strong>{value}</strong></span>
    </button>
  );
}

function OperatingItem({ icon, label, value, note, onClick }: {
  readonly icon: ReactNode;
  readonly label: string;
  readonly value: string;
  readonly note?: string;
  readonly onClick: () => void;
}) {
  return (
    <button type="button" className="hvac-plant-ref__operating-item" onClick={onClick}>
      <span className="hvac-plant-ref__operating-icon">{icon}</span>
      <span><small>{label}</small><strong>{value}</strong>{note ? <em>{note}</em> : null}</span>
    </button>
  );
}

function AggregateSlots({
  art,
  total,
  running,
  onClick,
}: {
  readonly art: string;
  readonly total: number | null | undefined;
  readonly running: number | null | undefined;
  readonly onClick: () => void;
}) {
  const slotCount = Math.max(1, Math.min(total ?? 1, 3));
  return (
    <div className="hvac-plant-ref__aggregate-slots">
      {Array.from({ length: slotCount }, (_, index) => (
        <button type="button" key={index} className={running != null && index < running ? 'is-running' : 'is-idle'} onClick={onClick}>
          <img src={art} alt="" aria-hidden="true" draggable={false} />
          <span><i />{running == null ? '状态待确认' : index < running ? '运行中' : '待机'}</span>
        </button>
      ))}
    </div>
  );
}

function GroupRunningBadge({ running, total }: { readonly running: number | null | undefined; readonly total: number | null | undefined }) {
  const active = running != null && running > 0;
  return <Badge variant="outline" className={active ? 'border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-900 dark:bg-emerald-950/40 dark:text-emerald-300' : undefined}>{running ?? '—'}/{total ?? '—'} 运行</Badge>;
}

export function PlantReferenceMonitorPage({
  site,
  devices,
  alarms,
  summary,
  overview,
  onOpenOverview,
  onOpenDevice,
  onOpenAnalysis,
  onOpenModes,
  onOpenTerminal,
  onOpenEnergy,
}: PlantReferenceMonitorPageProps) {
  const chillers = devices.filter((item) => item.kind === 'chiller');
  const chilledPumps = devices.filter((item) => item.kind === 'chilled-pump');
  const coolingPumps = devices.filter((item) => item.kind === 'cooling-pump');
  const towers = devices.filter((item) => item.kind === 'tower');
  const chillerTopology = overview?.topology.find((item) => item.key === 'chiller');
  const chwTopology = overview?.topology.find((item) => item.key === 'chw-pump');
  const cwTopology = overview?.topology.find((item) => item.key === 'cw-pump');
  const towerTopology = overview?.topology.find((item) => item.key === 'cooling-tower');
  const runningStrategies = overview?.strategies.filter((strategy) => strategy.status === 'RUNNING') ?? [];
  const operatingMode = runningStrategies.find((strategy) => /夏季|夜间|低负荷|过渡季|冬季|自定义|自动/.test(strategy.title)) ?? null;
  const activeStrategy = runningStrategies.find((strategy) => /冷机|群控/.test(strategy.title) && strategy !== operatingMode)
    ?? runningStrategies.find((strategy) => strategy !== operatingMode)
    ?? null;
  const totalCoolingKW = overview?.coolingSummary?.currentRT != null
    ? overview.coolingSummary.currentRT * RT_TO_KW
    : null;
  const supply = overview?.waterTemperatures.supplyC;
  const returnC = overview?.waterTemperatures.returnC;
  const cwSupply = overview?.coolingWaterTemperatures?.supplyC;
  const cwReturn = overview?.coolingWaterTemperatures?.returnC;
  const runningCombination = chillerTopology?.running != null && chillerTopology.total != null
    ? `${chillerTopology.running} 开 ${Math.max(chillerTopology.total - chillerTopology.running, 0)} 备`
    : '—';

  const plantDeviceIds = new Set(devices.filter((item) => item.kind !== 'terminal' && item.kind !== 'other').map((item) => item.device.id));
  const plantAlarms = alarms.filter((alarm) => alarm.deviceId && plantDeviceIds.has(alarm.deviceId));
  const overviewPlantAlarms = overview?.priorityAlarms.filter((alarm) => /冷机|冷水机|冷冻水泵|冷却水泵|冷却塔/.test(`${alarm.locationLabel} ${alarm.title}`)) ?? [];
  const plantOpportunities = overview?.opportunities.filter((item) => /冷机|冷冻|冷却|水泵|冷站|群控/.test(item.title)).slice(0, 3) ?? [];

  const getRunStatePill = (points: Parameters<typeof monitorRunState>[0]) => {
    const rs = monitorRunState(points);
    const tone = rs === 'RUNNING' ? 'success' : rs === 'FAULT' ? 'destructive' : 'neutral';
    return <StatusPillBadge tone={tone} label={monitorRunStateLabel(rs)} />;
  };

  const comparisonRows: ComparisonRow[] = [
    ...chillers.slice(0, 3).map((item) => ({
      key: item.device.id,
      type: '冷水机组',
      label: shortEquipmentName(item.device.displayName),
      state: getRunStatePill(item.state.points),
      operating: pointText(item, ['load_rate', '负荷率'], 0),
      device: item,
    })),
    ...(chilledPumps.length > 0
      ? chilledPumps.slice(0, 3).map((item) => ({
          key: item.device.id,
          type: '冷冻水泵',
          label: shortEquipmentName(item.device.displayName),
          state: getRunStatePill(item.state.points),
          operating: pointText(item, ['frequency', '频率'], 0),
          device: item,
        }))
      : chwTopology ? [{
          key: 'chw-pump-group',
          type: '冷冻水泵',
          label: '冷冻水泵组',
          state: <GroupRunningBadge running={chwTopology.running} total={chwTopology.total} />,
          operating: chwTopology.powerKW != null ? `${chwTopology.powerKW.toFixed(0)} kW` : '—',
        }] : []),
    ...(coolingPumps.length > 0
      ? coolingPumps.slice(0, 2).map((item) => ({
          key: item.device.id,
          type: '冷却水泵',
          label: shortEquipmentName(item.device.displayName),
          state: getRunStatePill(item.state.points),
          operating: pointText(item, ['frequency', '频率'], 0),
          device: item,
        }))
      : cwTopology ? [{
          key: 'cw-pump-group',
          type: '冷却水泵',
          label: '冷却水泵组',
          state: <GroupRunningBadge running={cwTopology.running} total={cwTopology.total} />,
          operating: cwTopology.powerKW != null ? `${cwTopology.powerKW.toFixed(0)} kW` : '—',
        }] : []),
  ];

  const chillerColumns = useMemo<Array<ColumnDef<DataTableFeatures, MonitorDevice>>>(() => [
    { id: 'device', header: '冷机编号', cell: ({ row }) => <Button variant="link" className="h-auto p-0 font-medium" onClick={() => onOpenDevice(row.original.device.id)}>{shortEquipmentName(row.original.device.displayName)}</Button> },
    {
      id: 'state',
      header: '运行状态',
      cell: ({ row }) => {
        const state = monitorRunState(row.original.state.points);
        return <StatusPillBadge tone={state === 'RUNNING' ? 'success' : state === 'FAULT' ? 'destructive' : 'neutral'} label={monitorRunStateLabel(state)} />;
      },
    },
    { id: 'load', header: '负荷率', cell: ({ row }) => <span className="font-mono tabular-nums">{pointText(row.original, ['load_rate', '负荷率'], 0)}</span> },
    { id: 'cooling', header: '冷量 (kW)', cell: ({ row }) => <span className="font-mono tabular-nums">{pointText(row.original, ['chiller.cooling_capacity', '制冷量'], 0)}</span> },
    { id: 'power', header: '输入功率 (kW)', cell: ({ row }) => <span className="font-mono tabular-nums">{pointText(row.original, ['chiller.power', '功率'], 0)}</span> },
    { id: 'cop', header: 'COP', cell: ({ row }) => <span className="font-mono tabular-nums">{pointText(row.original, ['chiller.cop', 'cop'], 1)}</span> },
    { id: 'chw', header: '冷冻水进/回水 (°C)', cell: ({ row }) => <span className="font-mono tabular-nums">{`${pointText(row.original, ['chilled_water_supply_temperature', '冷冻水供水'], 1).replace(/ °C$/, '')} / ${pointText(row.original, ['chilled_water_return_temperature', '冷冻水回水'], 1).replace(/ °C$/, '')}`}</span> },
    { id: 'cw', header: '冷却水进/出水 (°C)', cell: ({ row }) => <span className="font-mono tabular-nums">{`${pointText(row.original, ['entering_cooling_water_temperature', '冷却水进水'], 1).replace(/ °C$/, '')} / ${pointText(row.original, ['leaving_cooling_water_temperature', '冷却水出水'], 1).replace(/ °C$/, '')}`}</span> },
  ], [onOpenDevice]);

  const chillerTable = useDataTable({
    key: `plant-reference-chillers-${site.id}`,
    data: [...chillers.slice(0, 3)],
    columns: chillerColumns,
    paginate: false,
    getRowId: (row) => row.device.id,
  });

  const comparisonColumns = useMemo<Array<ColumnDef<DataTableFeatures, ComparisonRow>>>(() => [
    { id: 'type', header: '设备类型', cell: ({ row }) => <span className="font-medium">{row.original.type}</span> },
    { id: 'label', header: '设备编号', cell: ({ row }) => row.original.label },
    { id: 'state', header: '运行状态', cell: ({ row }) => row.original.state },
    { id: 'operating', header: '频率 / 负荷率', cell: ({ row }) => <span className="font-mono tabular-nums">{row.original.operating}</span> },
    { id: 'trend', header: '关键指标趋势（30分钟）', cell: () => <span className="hvac-plant-ref__mini-trend is-empty">趋势未返回</span> },
    { id: 'today', header: '今日累计运行', cell: () => <span className="font-mono tabular-nums">—</span> },
  ], []);

  const comparisonTable = useDataTable({
    key: `plant-reference-comparison-${site.id}`,
    data: comparisonRows,
    columns: comparisonColumns,
    paginate: false,
    getRowId: (row) => row.key,
  });

  const coolingPoints = overview?.coolingTrend?.map((point) => ({
    x: Date.parse(point.at),
    value: point.actual == null ? null : point.actual * RT_TO_KW,
  })) ?? [];
  const powerPoints = overview?.loadTrend.map((point) => ({ x: Date.parse(point.at), value: point.actual })) ?? [];
  const powerByAt = new Map(overview?.loadTrend.map((point) => [point.at, point.actual] as const) ?? []);
  const copPoints = overview?.coolingTrend?.map((point) => {
    const power = powerByAt.get(point.at);
    return {
      x: Date.parse(point.at),
      value: point.actual != null && power != null && power > 0 ? Number(((point.actual * RT_TO_KW) / power).toFixed(2)) : null,
    };
  }) ?? [];

  return (
    <div className="hvac-plant-ref" data-testid="hvac-plant-reference-page">
      <div className="hvac-plant-ref__breadcrumb">
        <Button variant="link" className="h-auto p-0" onClick={onOpenOverview}>运行监控</Button><span>/</span><strong>冷源系统监控</strong>
      </div>

      <section className="hvac-plant-ref__kpis" aria-label="冷源关键指标">
        <PlantKpi icon={<ExperimentOutlined />} tone="blue" label="冷站总冷量" value={formatMonitorMetric(totalCoolingKW, 'kW', 0)} onClick={() => onOpenAnalysis()} />
        <PlantKpi icon={<ThunderboltOutlined />} tone="blue" label="冷站输入功率" value={formatMonitorMetric(summary?.fastMetrics.currentPower.value, summary?.fastMetrics.currentPower.unit, 0)} onClick={() => onOpenAnalysis()} />
        <PlantKpi icon={<DashboardOutlined />} tone="purple" label="平均 COP" value={formatMonitorMetric(overview?.kpis.averageCop ?? summary?.slowMetrics.cop.value, null, 1)} onClick={() => onOpenAnalysis()} />
        <PlantKpi icon={<ExperimentOutlined />} tone="blue" label="冷冻水供回水" value={`${supply?.toFixed(1) ?? '—'} / ${returnC?.toFixed(1) ?? '—'} °C`} onClick={onOpenEnergy} />
        <PlantKpi icon={<ExperimentOutlined />} tone="green" label="冷却水进出水" value={`${cwSupply?.toFixed(1) ?? '—'} / ${cwReturn?.toFixed(1) ?? '—'} °C`} onClick={() => towers[0] ? onOpenDevice(towers[0].device.id) : onOpenAnalysis()} />
        <PlantKpi icon={<ControlOutlined />} tone="green" label="运行主机" value={`${chillerTopology?.running ?? '—'} / ${chillerTopology?.total ?? '—'}`} onClick={() => chillers[0] ? onOpenDevice(chillers[0].device.id) : onOpenAnalysis()} />
      </section>

      <section className="hvac-plant-ref__operating-strip" aria-label="冷源运行策略">
        <OperatingItem icon={<ExperimentOutlined />} label="当前运行模式" value={operatingMode?.title ?? '—'} note={operatingMode ? '当前生效运行模式' : '运行模式未单独返回'} onClick={onOpenModes} />
        <OperatingItem icon={<BulbOutlined />} label="当前策略" value={activeStrategy?.title ?? '—'} note={activeStrategy ? '当前生效策略' : '当前未提供'} onClick={onOpenModes} />
        <OperatingItem icon={<ControlOutlined />} label="冷机台数建议" value={runningCombination} note={runningCombination === '—' ? '当前无可验证建议' : '当前群控组合'} onClick={() => onOpenAnalysis()} />
        <OperatingItem icon={<ExperimentOutlined />} label="冷冻水设定" value={overview?.waterTemperatures.setpointC != null ? `${overview.waterTemperatures.setpointC.toFixed(1)} °C` : '—'} onClick={onOpenModes} />
      </section>

      <section className="hvac-plant-ref__main-grid">
        <PlantPanel title="冷源系统拓扑图" className="hvac-plant-ref__topology-card">
          <div className="hvac-plant-ref__topology">
            <svg viewBox="0 0 1000 470" preserveAspectRatio="none" className="hvac-plant-ref__pipes" aria-hidden="true">
              <defs>
                <marker id="plant-green-arrow" markerWidth="8" markerHeight="8" refX="7" refY="4" orient="auto"><path d="M0,0 L8,4 L0,8 Z" fill="#18a957" /></marker>
                <marker id="plant-blue-arrow" markerWidth="8" markerHeight="8" refX="7" refY="4" orient="auto"><path d="M0,0 L8,4 L0,8 Z" fill="#1677ff" /></marker>
              </defs>
              <path d="M380 70 H610" className="is-green" markerEnd="url(#plant-green-arrow)" />
              <path d="M760 85 H900 V250" className="is-green" markerEnd="url(#plant-green-arrow)" />
              <path d="M540 85 V205" className="is-green" markerEnd="url(#plant-green-arrow)" />
              <path d="M140 205 H900" className="is-blue" markerEnd="url(#plant-blue-arrow)" />
              <path d="M170 290 V330 H850 V290" className="is-blue" markerEnd="url(#plant-blue-arrow)" />
              <path d="M110 395 H890" className="is-blue" markerEnd="url(#plant-blue-arrow)" />
            </svg>

            <section className="hvac-plant-ref__topology-group is-tower">
              <header><strong>冷却塔</strong><span>{towerTopology?.running ?? '—'}/{towerTopology?.total ?? '—'} 运行</span></header>
              <AggregateSlots art={TOWER_ART} total={towerTopology?.total} running={towerTopology?.running} onClick={() => towers[0] ? onOpenDevice(towers[0].device.id) : onOpenAnalysis()} />
            </section>
            <section className="hvac-plant-ref__topology-group is-cwp">
              <header><strong>冷却水泵</strong><span>{cwTopology?.running ?? '—'}/{cwTopology?.total ?? '—'} 运行</span></header>
              <AggregateSlots art={COOLING_PUMP_ART} total={cwTopology?.total} running={cwTopology?.running} onClick={() => coolingPumps[0] ? onOpenDevice(coolingPumps[0].device.id) : onOpenAnalysis()} />
            </section>
            <section className="hvac-plant-ref__topology-group is-chiller">
              <header><strong>冷水机组</strong><span>{chillerTopology?.running ?? '—'}/{chillerTopology?.total ?? '—'} 运行</span></header>
              <div className="hvac-plant-ref__chiller-nodes">
                {chillers.slice(0, 3).map((item) => {
                  const runState = monitorRunState(item.state.points);
                  return (
                    <button type="button" key={item.device.id} onClick={() => onOpenDevice(item.device.id)}>
                      <img src={CHILLER_ART} alt="" aria-hidden="true" draggable={false} />
                      <span><strong>{shortEquipmentName(item.device.displayName)}</strong><Badge variant="outline" className={monitorRunStateBadgeClass(runState)}>{monitorRunStateLabel(runState)}</Badge></span>
                      <small>冷量 {pointText(item, ['chiller.cooling_capacity', '制冷量'], 0)}</small>
                      <small>负荷率 {pointText(item, ['load_rate', '负荷率'], 0)}</small>
                    </button>
                  );
                })}
              </div>
            </section>
            <section className="hvac-plant-ref__topology-group is-chwp">
              <header><strong>冷冻水泵</strong><span>{chwTopology?.running ?? '—'}/{chwTopology?.total ?? '—'} 运行</span></header>
              {chilledPumps.length > 0 ? (
                <div className="hvac-plant-ref__pump-nodes">
                  {chilledPumps.slice(0, 3).map((item) => (
                    <button type="button" key={item.device.id} onClick={() => onOpenDevice(item.device.id)}>
                      <img src={CHILLED_PUMP_ART} alt="" aria-hidden="true" draggable={false} />
                      <strong>{shortEquipmentName(item.device.displayName)}</strong>
                      <small>{pointText(item, ['frequency', '频率'], 0)}</small>
                    </button>
                  ))}
                </div>
              ) : <AggregateSlots art={CHILLED_PUMP_ART} total={chwTopology?.total} running={chwTopology?.running} onClick={() => onOpenAnalysis()} />}
            </section>
            <button type="button" className="hvac-plant-ref__header-pipe is-supply" onClick={onOpenTerminal}>冷冻水供水总管（供）</button>
            <button type="button" className="hvac-plant-ref__header-pipe is-return" onClick={onOpenEnergy}>冷冻水回水总管（回）</button>
          </div>
        </PlantPanel>

        <div className="hvac-plant-ref__right-stack">
          <PlantPanel title="冷机群运行概况" className="hvac-plant-ref__chiller-overview">
            <div className="rounded-md border">
              <DataTable
                table={chillerTable}
                className="gap-0"
                tableAriaLabel="冷机群运行概况"
                getHeaderCellProps={(header) => ({
                  className:
                    header.id === 'state' ? 'w-28 text-center' :
                    ['load', 'cooling', 'power', 'cop', 'chw', 'cw'].includes(header.id) ? 'text-right' :
                    undefined,
                })}
                getRowProps={(row) => ({
                  className: 'cursor-pointer',
                  onClick: (event) => {
                    if (!(event.target as HTMLElement).closest('button,a')) onOpenDevice(row.original.device.id);
                  },
                })}
                getCellProps={(cell) => ({
                  className:
                    cell.column.id === 'state' ? 'text-center' :
                    ['load', 'cooling', 'power', 'cop', 'chw', 'cw'].includes(cell.column.id) ? 'text-right' :
                    undefined,
                })}
              />
            </div>
          </PlantPanel>
          <div className="hvac-plant-ref__right-pair">
            <PlantPanel title={<span className="flex items-center gap-1.5"><BulbOutlined /> 优化建议</span>}>
              {plantOpportunities.length > 0 ? (
                <div className="hvac-plant-ref__recommendations">
                  {plantOpportunities.map((item) => (
                    <button type="button" key={item.rank} onClick={() => onOpenAnalysis()}>
                      <i />
                      <span>{item.title}</span>
                      <small>{item.savingKWhPerDay != null ? `预计节能 ${item.savingKWhPerDay.toFixed(0)} kWh/日` : '节能量待评估'}</small>
                    </button>
                  ))}
                </div>
              ) : <div className="hvac-plant-ref__empty-copy">当前没有已验证的冷源优化建议</div>}
            </PlantPanel>
            <PlantPanel
              title={<span className="flex items-center gap-1.5"><AlertOutlined /> 当前告警 <Badge variant={plantAlarms.length || overviewPlantAlarms.length ? 'destructive' : 'outline'}>{plantAlarms.length || overviewPlantAlarms.length}</Badge></span>}
              action={<Button variant="link" size="sm" asChild><a href={`${siteRoute(site, 'alarms')}?source=plant-monitor`}>查看全部</a></Button>}
            >
              <div className="hvac-plant-ref__alarm-list">
                {plantAlarms.length > 0 ? plantAlarms.slice(0, 3).map((alarm) => (
                  <a key={alarm.alarmId} href={`${siteRoute(site, 'alarms')}?alarm=${encodeURIComponent(alarm.alarmId)}&source=plant-monitor`}>
                    <Badge variant="outline" className={alarmSeverityBadgeClass[alarm.currentSeverity]}>{alarmSeverityLabel[alarm.currentSeverity]}</Badge>
                    <span><strong>{alarm.title}</strong><small>{formatTime(alarm.lastOccurredAt, site.timezone)}</small></span>
                  </a>
                )) : overviewPlantAlarms.slice(0, 3).map((alarm) => (
                  <a key={`${alarm.title}-${alarm.occurredAt}`} href={`${siteRoute(site, 'alarms')}?source=plant-monitor`}>
                    <Badge variant="outline" className={alarmSeverityBadgeClass[alarm.severity]}>{alarmSeverityLabel[alarm.severity]}</Badge>
                    <span><strong>{alarm.title}</strong><small>{formatTime(alarm.occurredAt, site.timezone)}</small></span>
                  </a>
                ))}
                {plantAlarms.length === 0 && overviewPlantAlarms.length === 0 ? <div className="hvac-plant-ref__empty-copy">当前没有冷源相关活动告警</div> : null}
              </div>
            </PlantPanel>
          </div>
        </div>
      </section>

      <section className="hvac-plant-ref__bottom-grid">
        <PlantPanel title="冷站关键指标趋势（24小时）">
          <DualAxisTimeSeriesChart
            primarySeries={[
              { key: 'cooling', label: '冷量 (kW)', tone: 'primary', points: coolingPoints },
              { key: 'power', label: '输入功率 (kW)', tone: 'success', points: powerPoints },
            ]}
            secondarySeries={[{ key: 'cop', label: 'COP', tone: 'warning', points: copPoints }]}
            primaryUnit="kW"
            secondaryUnit="COP"
            secondaryDomain={[0, 8]}
            xLabelFormatter={(value) => new Intl.DateTimeFormat('zh-CN', { timeZone: site.timezone, hour: '2-digit', minute: '2-digit', hour12: false }).format(new Date(Number(value)))}
            style={{ height: 190, width: '100%' }}
            ariaLabel="冷站最近 24 小时冷量、输入功率和 COP 趋势"
            compact
          />
        </PlantPanel>

        <PlantPanel title="设备运行对比（实时）">
          <div className="rounded-md border">
            <DataTable
              table={comparisonTable}
              className="gap-0"
              tableAriaLabel="设备运行对比"
              getHeaderCellProps={(header) => ({
                className:
                  header.id === 'state' ? 'w-28 text-center' :
                  header.id === 'operating' || header.id === 'today' ? 'text-right' :
                  header.id === 'trend' ? 'text-center' :
                  undefined,
              })}
              getRowProps={(row) => ({
                className: row.original.device ? 'cursor-pointer' : undefined,
                onClick: (event) => {
                  if (row.original.device && !(event.target as HTMLElement).closest('button,a')) onOpenDevice(row.original.device.device.id);
                },
              })}
              getCellProps={(cell) => ({
                className:
                  cell.column.id === 'state' || cell.column.id === 'trend' ? 'text-center' :
                  cell.column.id === 'operating' || cell.column.id === 'today' ? 'text-right' :
                  undefined,
              })}
            />
          </div>
        </PlantPanel>

        <PlantPanel title="能效分析（今日）" className="hvac-plant-ref__efficiency-card">
          <div className="hvac-plant-ref__efficiency-grid">
            <button type="button" onClick={() => onOpenAnalysis()}><DashboardOutlined /><span>日均 COP</span><strong>{formatMonitorMetric(overview?.kpis.averageCop, null, 2)}</strong><small>{overview?.kpis.copComparePercent != null ? `较昨日 ${overview.kpis.copComparePercent >= 0 ? '▲' : '▼'} ${Math.abs(overview.kpis.copComparePercent).toFixed(1)}%` : '对比数据未返回'}</small></button>
            <button type="button" onClick={() => onOpenAnalysis()}><ExperimentOutlined /><span>总冷量</span><strong>{formatMonitorMetric(overview?.kpis.coolingTodayRT, 'RT', 0)}</strong><small>{overview?.kpis.coolingComparePercent != null ? `较昨日 ${overview.kpis.coolingComparePercent >= 0 ? '▲' : '▼'} ${Math.abs(overview.kpis.coolingComparePercent).toFixed(1)}%` : '对比数据未返回'}</small></button>
            <button type="button" onClick={() => onOpenAnalysis()}><ThunderboltOutlined /><span>总输入能耗</span><strong>{formatMonitorMetric(summary?.slowMetrics.siteLocalDayEnergy.value, summary?.slowMetrics.siteLocalDayEnergy.unit, 0)}</strong><small>站点本地日累计</small></button>
            <button type="button" onClick={() => onOpenAnalysis()}><DashboardOutlined /><span>系统能效比 EER</span><strong>—</strong><small>当前读模型未单独返回 EER</small></button>
          </div>
        </PlantPanel>
      </section>
    </div>
  );
}
