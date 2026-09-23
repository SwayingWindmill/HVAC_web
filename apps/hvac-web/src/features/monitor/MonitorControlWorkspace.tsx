import type { ReactNode } from 'react';
import {
  Activity,
  AlertTriangle,
  ArrowRight,
  ArrowUpRight,
  ChartNoAxesCombined,
  CircleGauge,
  Gauge,
  Layers3,
  RadioTower,
  Thermometer,
  X,
  Zap,
} from 'lucide-react';
import type { Alarm } from '@/api/alarms';
import type { DashboardOverview } from '@/api/dashboard-overview';
import type { Site, SiteDashboardSummary } from '@/api/generated/platformGateway.gen';
import { siteRoute } from '@/app/router-paths';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card';
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { cn } from '@/lib/utils';
import {
  alarmSeverityLabel,
  deviceStateLabel,
  formatMonitorMetric,
  monitorDeviceKindLabel,
  monitorRunState,
  monitorRunStateLabel,
  runningDeviceSummary,
  type MonitorDevice,
  type MonitorEnergyView,
  type MonitorOverviewView,
} from './model';
import { MonitorX6Topology, type MonitorTopologyZone } from './MonitorX6Topology';

interface MonitorControlWorkspaceProps {
  readonly site: Readonly<Site>;
  readonly summary: SiteDashboardSummary | undefined;
  readonly overview: DashboardOverview | undefined;
  readonly devices: readonly MonitorDevice[];
  readonly zones: readonly MonitorTopologyZone[];
  readonly alarms: readonly Alarm[];
  readonly selectedDevice: MonitorDevice | null;
  readonly selectedZoneKey: string | null;
  readonly view: MonitorOverviewView;
  readonly energyView: MonitorEnergyView;
  readonly onOpenDevice: (deviceId: string) => void;
  readonly onCloseDevice: () => void;
  readonly onOpenPlant: () => void;
  readonly onOpenTerminal: (zoneKey?: string) => void;
  readonly onOpenAnalysis: () => void;
  readonly onOpenModes: () => void;
  readonly onOpenTopology: () => void;
  readonly onOpenAnomaly: () => void;
  readonly onOpenEnergy: () => void;
  readonly onEnergyViewChange: (view: MonitorEnergyView) => void;
}

function formatInstant(value: string | null | undefined, timezone: string): string {
  if (!value) return '—';
  return new Intl.DateTimeFormat('zh-CN', {
    timeZone: timezone,
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false,
  }).format(new Date(value));
}

function severityTone(severity: Alarm['currentSeverity']): 'critical' | 'attention' | 'neutral' {
  if (severity === 'CRITICAL') return 'critical';
  if (severity === 'MAJOR' || severity === 'WARNING') return 'attention';
  return 'neutral';
}

function freshnessLabel(value: string): string {
  if (value === 'FRESH') return '新鲜';
  if (value === 'STALE') return '延迟';
  if (value === 'MISSING' || value === 'NO_DATA') return '无数据';
  if (value === 'NOT_APPLICABLE') return '不适用';
  return '不可用';
}

function qualityLabel(value: string): string {
  if (value === 'GOOD') return '良好';
  if (value === 'DEGRADED') return '降级';
  if (value === 'NO_DATA' || value === 'MISSING') return '无数据';
  if (value === 'NOT_APPLICABLE') return '不适用';
  return '不可用';
}

function MetricFact({ label, value, tone }: { label: string; value: string; tone?: 'normal' | 'success' | 'attention' }) {
  return (
    <div className="min-w-0">
      <span className="block text-[11px] text-muted-foreground">{label}</span>
      <strong className={cn(
        'mt-1 block truncate text-sm font-semibold tabular-nums',
        tone === 'success' && 'text-success',
        tone === 'attention' && 'text-warning',
      )}>{value}</strong>
    </div>
  );
}

function EnergyFlowCanvas({ summary, overview, view, onViewChange }: {
  readonly summary: SiteDashboardSummary | undefined;
  readonly overview: DashboardOverview | undefined;
  readonly view: MonitorEnergyView;
  readonly onViewChange: (view: MonitorEnergyView) => void;
}) {
  const chilledSupply = overview?.waterTemperatures.supplyC;
  const chilledReturn = overview?.waterTemperatures.returnC;
  const coolingSupply = overview?.coolingWaterTemperatures?.supplyC;
  const coolingReturn = overview?.coolingWaterTemperatures?.returnC;
  const currentPower = summary?.fastMetrics.currentPower;
  const currentLoad = overview?.loadSummary.currentKW ?? overview?.kpis.totalLoadKW ?? null;

  const nodes: ReadonlyArray<{ icon: ReactNode; label: string; value: string; detail: string }> = [
    { icon: <Zap />, label: '系统输入', value: formatMonitorMetric(currentPower?.value, currentPower?.unit ?? 'kW', 0), detail: '站点功率摘要' },
    { icon: <CircleGauge />, label: '冷源系统', value: formatMonitorMetric(currentLoad, 'kW', 0), detail: '当前冷负荷' },
    { icon: <Thermometer />, label: '冷冻水环路', value: chilledSupply == null || chilledReturn == null ? '—' : `${chilledSupply.toFixed(1)} / ${chilledReturn.toFixed(1)} °C`, detail: '供水 / 回水' },
    { icon: <Layers3 />, label: '末端换热', value: overview?.kpis.comfortRatePercent == null ? '—' : `${overview.kpis.comfortRatePercent.toFixed(1)} %`, detail: '舒适度达标率' },
  ];

  return (
    <div className="flex min-h-[420px] flex-col justify-center gap-8 p-6" data-testid="hvac-monitor-energy-evidence" aria-label="HVAC 能流证据">
      <div className="control-monitor-energy__switch inline-flex w-fit rounded-md border bg-muted/30 p-1" aria-label="能流观察维度">
        {(['cooling', 'power', 'hydraulic'] as const).map((item) => (
          <button
            type="button"
            key={item}
            className={cn('rounded-sm px-3 py-1.5 text-xs font-medium text-muted-foreground transition-colors', view === item && 'bg-background text-foreground shadow-sm')}
            data-active={view === item}
            onClick={() => onViewChange(item)}
          >
            {item === 'cooling' ? '制冷' : item === 'power' ? '电力' : '水力'}
          </button>
        ))}
      </div>

      <div className="grid items-stretch gap-3 xl:grid-cols-[1fr_auto_1fr_auto_1fr_auto_1fr]">
        {nodes.map((node, index) => (
          <div key={node.label} className="contents">
            <div className="control-monitor-energy__node flex min-w-0 items-start gap-3 rounded-md border bg-card p-4">
              <span className="grid size-8 shrink-0 place-items-center rounded-md bg-muted text-muted-foreground [&_svg]:size-4">{node.icon}</span>
              <span className="min-w-0">
                <span className="block text-xs text-muted-foreground">{node.label}</span>
                <strong className="mt-1 block truncate text-base font-semibold tabular-nums">{node.value}</strong>
                <small className="mt-1 block text-[11px] text-muted-foreground">{node.detail}</small>
              </span>
            </div>
            {index < nodes.length - 1 ? <ArrowRight className="mx-auto hidden size-4 self-center text-muted-foreground xl:block" aria-hidden="true" /> : null}
          </div>
        ))}
      </div>

      <div className="grid gap-3 rounded-md border bg-muted/20 p-4 sm:grid-cols-2 xl:grid-cols-4">
        <MetricFact label="冷却水供水" value={formatMonitorMetric(coolingSupply, '°C', 1)} />
        <MetricFact label="冷却水回水" value={formatMonitorMetric(coolingReturn, '°C', 1)} />
        <MetricFact label="系统 COP" value={formatMonitorMetric(overview?.kpis.averageCop ?? summary?.slowMetrics.cop.value, null, 2)} tone="success" />
        <MetricFact label="今日 HVAC 能耗" value={formatMonitorMetric(overview?.savingsPerformance.actualEnergyKWh, 'kWh', 0)} />
      </div>
      <p className="text-[11px] text-muted-foreground">线宽不表达未经验证的流量或能量分配；当前仅展示已有测量与聚合证据。</p>
    </div>
  );
}

function DeviceInspector({ item, site, onClose }: { item: MonitorDevice; site: Readonly<Site>; onClose: () => void }) {
  const runState = monitorRunState(item.state.points);
  const visiblePoints = item.state.points.filter((point) => point.state === 'PRESENT').slice(0, 6);
  const detailSearch = new URLSearchParams({ site: site.id });
  return (
    <aside className="control-monitor-inspector w-full shrink-0 xl:w-[340px]" aria-label={`${item.device.displayName}上下文检查器`}>
      <Card className="h-full shadow-none">
        <CardHeader>
          <CardTitle>{item.device.displayName}</CardTitle>
          <CardDescription>{monitorDeviceKindLabel(item.kind)} · {item.device.code}</CardDescription>
          <CardAction><Button variant="ghost" size="icon-sm" aria-label="关闭设备检查器" onClick={onClose}><X /></Button></CardAction>
        </CardHeader>
        <CardContent className="space-y-5">
          <div className="grid grid-cols-2 overflow-hidden rounded-md border">
            {[
              ['运行', monitorRunStateLabel(runState)],
              ['连接', deviceStateLabel(item.state)],
              ['数据新鲜度', freshnessLabel(item.state.telemetry.freshness)],
              ['数据质量', qualityLabel(item.state.telemetry.quality)],
            ].map(([label, value], index) => (
              <div key={label} className={cn('p-3', index % 2 === 1 && 'border-l', index >= 2 && 'border-t')}>
                <span className="block text-[11px] text-muted-foreground">{label}</span>
                <strong className="mt-1 block text-xs font-medium">{value}</strong>
              </div>
            ))}
          </div>

          <section>
            <div className="mb-2 flex items-center justify-between"><span className="text-xs font-medium">当前关键值</span><small className="text-[11px] text-muted-foreground">{item.state.telemetry.presentPointCount} 个可用测点</small></div>
            <div className="divide-y rounded-md border">
              {visiblePoints.length > 0 ? visiblePoints.map((point) => (
                <div key={point.pointId} className="flex items-center gap-3 px-3 py-2.5">
                  <span className="min-w-0 flex-1 truncate text-xs text-muted-foreground">{point.label}</span>
                  <strong className="text-xs font-medium tabular-nums">{point.displayValue}{point.unit ? ` ${point.unit}` : ''}</strong>
                  <Badge variant="outline">{freshnessLabel(point.freshness)}</Badge>
                </div>
              )) : <p className="p-3 text-xs text-muted-foreground">当前没有可展示的实时测点。</p>}
            </div>
          </section>

          <section>
            <div className="mb-2 flex items-center justify-between"><span className="text-xs font-medium">活动告警</span><small className="text-[11px] text-muted-foreground">{item.activeAlarms.length} 条</small></div>
            <div className="space-y-1">
              {item.activeAlarms.length > 0 ? item.activeAlarms.slice(0, 3).map((alarm) => (
                <a key={alarm.alarmId} className="flex items-center gap-2 rounded-md px-2 py-2 hover:bg-muted/50" href={`${siteRoute(site, 'alarms')}?alarm=${encodeURIComponent(alarm.alarmId)}&source=hvac-monitor`}>
                  <Badge variant={severityTone(alarm.currentSeverity) === 'critical' ? 'destructive' : 'outline'}>{alarmSeverityLabel[alarm.currentSeverity]}</Badge>
                  <strong className="min-w-0 flex-1 truncate text-xs font-medium">{alarm.title}</strong>
                  <ArrowUpRight className="size-3.5 text-muted-foreground" />
                </a>
              )) : <p className="rounded-md border bg-muted/20 p-3 text-xs text-muted-foreground">当前没有关联活动告警。</p>}
            </div>
          </section>

          <div className="grid gap-2 border-t pt-4">
            <Button size="sm" asChild><a href={`/devices/${encodeURIComponent(item.device.id)}?${detailSearch.toString()}`}>打开设备详情 <ArrowUpRight /></a></Button>
            <Button variant="outline" size="sm" asChild><a href={`${siteRoute(site, 'alarms')}?device=${encodeURIComponent(item.device.id)}&source=hvac-monitor`}>查看相关告警</a></Button>
          </div>
        </CardContent>
      </Card>
    </aside>
  );
}

export function MonitorControlWorkspace({
  site,
  summary,
  overview,
  devices,
  zones,
  alarms,
  selectedDevice,
  selectedZoneKey,
  view,
  energyView,
  onOpenDevice,
  onCloseDevice,
  onOpenPlant,
  onOpenTerminal,
  onOpenAnalysis,
  onOpenModes,
  onOpenTopology,
  onOpenAnomaly,
  onOpenEnergy,
  onEnergyViewChange,
}: MonitorControlWorkspaceProps) {
  const running = runningDeviceSummary(devices);
  const criticalAlarms = alarms.filter((alarm) => alarm.currentSeverity === 'CRITICAL' || alarm.currentSeverity === 'MAJOR');
  const asOf = overview?.asOf ?? summary?.asOf;
  const selectedDeviceId = selectedDevice?.device.id ?? null;
  const sortedAlarms = [...alarms].sort((left, right) => {
    const rank = { CRITICAL: 5, MAJOR: 4, MINOR: 3, WARNING: 2, INFO: 1 } as const;
    return rank[right.currentSeverity] - rank[left.currentSeverity];
  });

  const changeView = (next: string) => {
    if (next === 'anomaly') onOpenAnomaly();
    else if (next === 'energy') onOpenEnergy();
    else onOpenTopology();
  };

  return (
    <div className="mx-auto flex w-full max-w-[1800px] flex-col gap-5" data-testid="hvac-control-workspace" data-view={view}>
      <section className="flex flex-col gap-4 lg:flex-row lg:items-end lg:justify-between" aria-label="运行监控上下文">
        <div>
          <p className="text-sm text-muted-foreground">{site.displayName}</p>
          <h2 className="mt-1 text-2xl font-semibold tracking-tight">HVAC 运行监控</h2>
          <p className="mt-1 text-xs text-muted-foreground">更新时间 {formatInstant(asOf, site.timezone)}</p>
        </div>
        <div className="flex flex-wrap gap-2">
          <Button variant="outline" size="sm" onClick={onOpenPlant}>冷源系统</Button>
          <Button variant="outline" size="sm" onClick={() => onOpenTerminal()}>末端系统</Button>
          <Button variant="outline" size="sm" onClick={onOpenAnalysis}>联动分析</Button>
          <Button variant="outline" size="sm" onClick={onOpenModes}>运行策略</Button>
        </div>
      </section>

      <section className="grid gap-3 md:grid-cols-2 xl:grid-cols-5" data-testid="hvac-monitor-summary" aria-label="系统运行摘要">
        {[
          ['运行设备', `${running.running} / ${running.observed || '—'}`, running.running > 0 ? 'success' : 'normal'],
          ['活动告警', `${alarms.length}`, criticalAlarms.length > 0 ? 'attention' : 'normal'],
          ['当前功率', formatMonitorMetric(summary?.fastMetrics.currentPower.value, summary?.fastMetrics.currentPower.unit ?? 'kW', 0), 'normal'],
          ['系统 COP', formatMonitorMetric(overview?.kpis.averageCop ?? summary?.slowMetrics.cop.value, null, 2), 'normal'],
          ['冷冻水供 / 回', overview?.waterTemperatures.supplyC == null || overview.waterTemperatures.returnC == null ? '—' : `${overview.waterTemperatures.supplyC.toFixed(1)} / ${overview.waterTemperatures.returnC.toFixed(1)} °C`, 'normal'],
        ].map(([label, value, tone]) => (
          <Card key={String(label)} size="sm" className="shadow-none" data-testid="hvac-monitor-summary-fact"><CardContent><MetricFact label={String(label)} value={String(value)} tone={tone as 'normal' | 'success' | 'attention'} /></CardContent></Card>
        ))}
      </section>

      <Tabs value={view} onValueChange={changeView} className="gap-4">
        <TabsList>
          <TabsTrigger className="control-monitor-tab gap-2" value="topology"><CircleGauge className="size-3.5" />系统拓扑</TabsTrigger>
          <TabsTrigger className="control-monitor-tab gap-2" value="anomaly"><AlertTriangle className="size-3.5" />异常定位</TabsTrigger>
          <TabsTrigger className="control-monitor-tab gap-2" value="energy"><ChartNoAxesCombined className="size-3.5" />能流证据</TabsTrigger>
        </TabsList>

        <section className="flex min-w-0 flex-col gap-4 xl:flex-row" data-testid="hvac-monitor-main-workspace">
          <Card className="min-w-0 flex-1 gap-0 py-0 shadow-none" data-testid="hvac-monitor-main-canvas">
            <CardHeader className="border-b py-4">
              <CardTitle>{view === 'topology' ? '当前系统运行关系' : view === 'anomaly' ? '异常对象与系统位置' : '测量与聚合能流证据'}</CardTitle>
              <CardAction className="flex items-center gap-2 text-[11px] text-muted-foreground">
                <span className="inline-flex items-center gap-1"><i className="size-1.5 rounded-full bg-success" />运行</span>
                <span className="inline-flex items-center gap-1"><i className="size-1.5 rounded-full bg-warning" />异常</span>
                <span className="inline-flex items-center gap-1"><i className="size-1.5 rounded-full bg-muted-foreground" />未知</span>
              </CardAction>
            </CardHeader>
            <CardContent className="p-0">
              {view === 'energy' ? (
                <EnergyFlowCanvas summary={summary} overview={overview} view={energyView} onViewChange={onEnergyViewChange} />
              ) : (
                <div className="min-h-[520px]">
                  <MonitorX6Topology
                    overview={overview}
                    devices={devices}
                    zones={zones}
                    selectedDeviceId={selectedDeviceId}
                    selectedZoneKey={selectedZoneKey}
                    anomalyMode={view === 'anomaly'}
                    onOpenDevice={onOpenDevice}
                    onOpenPlant={onOpenPlant}
                    onOpenTerminal={onOpenTerminal}
                  />
                </div>
              )}
            </CardContent>
          </Card>
          {selectedDevice ? <DeviceInspector item={selectedDevice} site={site} onClose={onCloseDevice} /> : null}
        </section>
      </Tabs>

      <section className="control-monitor-evidence-dock grid gap-4 xl:grid-cols-3" aria-label="运行监控证据栏">
        <Card className="control-monitor-evidence-panel shadow-none">
          <CardHeader><CardTitle>当前运行</CardTitle></CardHeader>
          <CardContent className="grid grid-cols-2 gap-x-5 gap-y-4">
            <MetricFact label="当前冷负荷" value={formatMonitorMetric(overview?.loadSummary.currentKW ?? overview?.kpis.totalLoadKW, 'kW', 0)} />
            <MetricFact label="供回水温差" value={overview?.waterTemperatures.supplyC == null || overview.waterTemperatures.returnC == null ? '—' : `${(overview.waterTemperatures.returnC - overview.waterTemperatures.supplyC).toFixed(1)} °C`} />
            <MetricFact label="舒适度达标率" value={formatMonitorMetric(overview?.kpis.comfortRatePercent, '%', 1)} />
            <MetricFact label="今日 HVAC 能耗" value={formatMonitorMetric(overview?.savingsPerformance.actualEnergyKWh, 'kWh', 0)} />
          </CardContent>
        </Card>

        <Card className="control-monitor-evidence-panel shadow-none">
          <CardHeader>
            <CardTitle>当前告警</CardTitle>
            <CardAction><Button variant="ghost" size="sm" asChild><a href={`${siteRoute(site, 'alarms')}?source=hvac-monitor`}>全部 <ArrowUpRight /></a></Button></CardAction>
          </CardHeader>
          <CardContent className="space-y-1">
            {sortedAlarms.length > 0 ? sortedAlarms.slice(0, 4).map((alarm) => (
              <a key={alarm.alarmId} className="flex items-center gap-2 rounded-md px-2 py-2 hover:bg-muted/50" href={`${siteRoute(site, 'alarms')}?alarm=${encodeURIComponent(alarm.alarmId)}&source=hvac-monitor`}>
                <Badge variant={severityTone(alarm.currentSeverity) === 'critical' ? 'destructive' : 'outline'}>{alarmSeverityLabel[alarm.currentSeverity]}</Badge>
                <strong className="min-w-0 flex-1 truncate text-xs font-medium">{alarm.title}</strong>
                <small className="text-[10px] text-muted-foreground tabular-nums">{formatInstant(alarm.lastOccurredAt, site.timezone)}</small>
              </a>
            )) : <p className="flex items-center gap-2 py-6 text-xs text-muted-foreground"><RadioTower className="size-4" />当前没有已知活动告警。</p>}
          </CardContent>
        </Card>

        <Card className="control-monitor-evidence-panel shadow-none">
          <CardHeader><CardTitle>继续调查</CardTitle></CardHeader>
          <CardContent className="grid gap-1">
            <button type="button" className="flex items-center gap-3 rounded-md px-2 py-2 text-left hover:bg-muted/50" onClick={onOpenAnalysis}><Activity className="size-4 text-muted-foreground" /><span className="min-w-0 flex-1"><strong className="block text-xs font-medium">联动分析</strong><small className="text-[11px] text-muted-foreground">多设备关系与证据</small></span><ArrowRight className="size-3.5 text-muted-foreground" /></button>
            <button type="button" className="flex items-center gap-3 rounded-md px-2 py-2 text-left hover:bg-muted/50" onClick={onOpenModes}><Gauge className="size-4 text-muted-foreground" /><span className="min-w-0 flex-1"><strong className="block text-xs font-medium">运行策略</strong><small className="text-[11px] text-muted-foreground">模式候选与执行入口</small></span><ArrowRight className="size-3.5 text-muted-foreground" /></button>
            <a className="flex items-center gap-3 rounded-md px-2 py-2 hover:bg-muted/50" href={siteRoute(site, 'energy')}><ChartNoAxesCombined className="size-4 text-muted-foreground" /><span className="min-w-0 flex-1"><strong className="block text-xs font-medium">能耗分析</strong><small className="text-[11px] text-muted-foreground">周期趋势与钻取</small></span><ArrowRight className="size-3.5 text-muted-foreground" /></a>
          </CardContent>
        </Card>
      </section>
    </div>
  );
}
