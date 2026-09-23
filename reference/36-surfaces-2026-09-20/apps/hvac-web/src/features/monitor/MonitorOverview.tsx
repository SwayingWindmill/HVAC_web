import type { ReactNode } from 'react';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardAction, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Empty, EmptyDescription } from '@/components/ui/empty';
import { AlertOutlined, ApartmentOutlined, BarChartOutlined } from '@/shared/icons';
import type { Site, SiteDashboardSummary } from '@/api/generated/platformGateway.gen';
import type { DashboardOverview } from '@/api/dashboard-overview';
import type { Alarm } from '@/api/alarms';
import { siteRoute } from '@/app/router-paths';
import { EventTimelineCard } from '@/shared/ui';
import {
  alarmSeverityBadgeClass,
  alarmSeverityLabel,
  deviceStateLabel,
  formatMonitorMetric,
  highestAlarm,
  pointMetric,
  sumPointValues,
  type MonitorDevice,
  type MonitorEnergyView,
} from './model';
import { MonitorX6Topology, type MonitorTopologyZone } from './MonitorX6Topology';
import { MonitorEnergyFlowChart } from './MonitorEnergyFlowChart';

function MonitorPanel({ title, action, children, className, id }: { readonly title: ReactNode; readonly action?: ReactNode; readonly children: ReactNode; readonly className?: string; readonly id?: string }) {
  return (
    <Card id={id} className={className}>
      <CardHeader className="border-b pb-3">
        <CardTitle>{title}</CardTitle>
        {action ? <CardAction>{action}</CardAction> : null}
      </CardHeader>
      <CardContent>{children}</CardContent>
    </Card>
  );
}

function MonitorEmpty({ description }: { readonly description: string }) {
  return <Empty className="min-h-32 border"><EmptyDescription>{description}</EmptyDescription></Empty>;
}

export function MonitorMetric({ label, value, note, action, onClick }: { label: string; value: string; note: string; action?: ReactNode; onClick?: () => void }) {
  const content = <><span>{label}</span><strong>{value}</strong><small>{note}</small>{action ? <div className="hvac-monitor__metric-action">{action}</div> : null}</>;
  return onClick
    ? <button type="button" className="hvac-monitor__metric is-interactive" onClick={onClick}>{content}</button>
    : <div className="hvac-monitor__metric">{content}</div>;
}

export function TopologyView({ devices, zones, overview, selectedDeviceId, anomalyMode = false, onOpenDevice, onOpenPlant, onOpenTerminal }: {
  devices: readonly MonitorDevice[];
  zones: readonly MonitorTopologyZone[];
  overview: DashboardOverview | undefined;
  selectedDeviceId: string | null;
  anomalyMode?: boolean;
  onOpenDevice: (deviceId: string) => void;
  onOpenPlant: () => void;
  onOpenTerminal: (zoneKey?: string) => void;
}) {
  return (
    <div className="hvac-monitor__topology-canvas">
      <div className="hvac-monitor__topology-toolbar">
        <div>
          <h2 className="text-base font-semibold">HVAC 系统实时拓扑</h2>
          <p className="mt-1 text-sm text-muted-foreground">冷却水环路与冷冻水环路 · 设备组状态实时汇总</p>
        </div>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" onClick={onOpenPlant}>冷源系统</Button>
          <Button variant="outline" size="sm" onClick={() => onOpenTerminal()}>空调末端</Button>
        </div>
      </div>
      <MonitorX6Topology
        devices={devices}
        zones={zones}
        overview={overview}
        selectedDeviceId={selectedDeviceId}
        anomalyMode={anomalyMode}
        onOpenDevice={onOpenDevice}
        onOpenPlant={onOpenPlant}
        onOpenTerminal={onOpenTerminal}
      />
    </div>
  );
}

export function AnomalyView({ devices, zones, overview, alarms, selectedAlarmId, selectedZoneKey, site, onOpenDevice, onOpenPlant, onOpenTerminal }: {
  devices: readonly MonitorDevice[];
  zones: readonly MonitorTopologyZone[];
  overview: DashboardOverview | undefined;
  alarms: readonly Alarm[];
  selectedAlarmId: string | null;
  selectedZoneKey?: string | null;
  site: Readonly<Site>;
  onOpenDevice: (deviceId: string) => void;
  onOpenPlant: () => void;
  onOpenTerminal: (zoneKey?: string) => void;
}) {
  const alarm = alarms.find((candidate) => candidate.alarmId === selectedAlarmId) ?? highestAlarm(alarms);
  const affectedDevice = alarm?.deviceId ? devices.find((item) => item.device.id === alarm.deviceId) ?? null : null;
  return (
    <div className="hvac-monitor__anomaly-grid" data-testid="hvac-anomaly-reference-view">
      <div className="hvac-monitor__anomaly-main">
        <section className="hvac-monitor__anomaly-canvas">
          <header>
            <div><strong>系统异常定位</strong><span>沿原运行拓扑高亮异常设备与待核查影响范围</span></div>
            <div className="flex items-center gap-2">
              <Button variant="outline" size="sm" onClick={onOpenPlant}>冷源系统</Button>
              <Button variant="outline" size="sm" onClick={() => onOpenTerminal()}>空调末端</Button>
            </div>
          </header>
          <MonitorX6Topology
            devices={devices}
            zones={zones}
            overview={overview}
            selectedDeviceId={affectedDevice?.device.id ?? null}
            selectedZoneKey={selectedZoneKey}
            anomalyMode
            onOpenDevice={onOpenDevice}
            onOpenPlant={onOpenPlant}
            onOpenTerminal={onOpenTerminal}
          />
        </section>
        <MonitorPanel
          className="hvac-monitor__anomaly-map"
          title="异常定位与影响范围"
          action={alarm ? <Badge variant="outline" className={alarmSeverityBadgeClass[alarm.currentSeverity]}>{alarmSeverityLabel[alarm.currentSeverity]}</Badge> : undefined}
        >
          {alarm ? (
            <div className="hvac-monitor__anomaly-impact-strip">
              <button type="button" onClick={() => affectedDevice && onOpenDevice(affectedDevice.device.id)}>
                <span className="hvac-monitor__anomaly-impact-icon"><AlertOutlined /></span>
                <span><small>异常设备</small><strong>{affectedDevice?.device.displayName ?? alarm.title}</strong><em>{alarm.title}</em></span>
              </button>
              <i aria-hidden="true">→</i>
              <section><small>系统影响</small><strong>供冷能力 / 效率需核查</strong><em>仅确认活动异常，不根据告警先后推断设备因果。</em></section>
              <i aria-hidden="true">→</i>
              <button type="button" className="is-zone" onClick={() => onOpenTerminal()}>
                <span className="hvac-monitor__anomaly-impact-icon"><ApartmentOutlined /></span>
                <span><small>受影响区域</small><strong>末端范围待确认</strong><em>进入空调末端核查空间影响</em></span>
              </button>
            </div>
          ) : <MonitorEmpty description="当前没有活动告警" />}
        </MonitorPanel>
      </div>
      <div className="hvac-monitor__anomaly-side">
        <MonitorPanel title="异常摘要">
          <div className="hvac-monitor__fact-list">
            <div><span>异常</span><strong>{alarm?.title ?? '当前无活动异常'}</strong></div>
            <div><span>设备</span><strong>{affectedDevice?.device.displayName ?? '—'}</strong></div>
            <div><span>风险冷量</span><strong>—</strong><small>当前数据未提供可验证估算</small></div>
            <div><span>影响区域</span><strong>待确认</strong><small>需结合末端实时数据</small></div>
          </div>
        </MonitorPanel>
        <MonitorPanel title="诊断建议">
          <p className="text-sm leading-6 text-muted-foreground">{alarm?.summary ?? '当前没有需要处理的活动异常。'}</p>
          {alarm ? (
            <div className="mt-3 flex flex-wrap gap-2">
              <Button variant="outline" asChild><a href={`${siteRoute(site, 'alarms')}?alarm=${encodeURIComponent(alarm.alarmId)}&source=hvac-monitor`}>查看告警</a></Button>
              <Button asChild><a href={`${siteRoute(site, 'work-orders')}?sourceAlarm=${encodeURIComponent(alarm.alarmId)}`}>生成工单</a></Button>
            </div>
          ) : null}
        </MonitorPanel>
        <EventTimelineCard
          title="异常时间线"
          items={alarm ? [
            {
              key: 'first-occurred',
              title: '首次发生',
              time: new Intl.DateTimeFormat('zh-CN', { timeZone: site.timezone, dateStyle: 'short', timeStyle: 'short' }).format(new Date(alarm.firstOccurredAt)),
              tone: 'error',
            },
            {
              key: 'last-occurred',
              title: '最近发生',
              time: new Intl.DateTimeFormat('zh-CN', { timeZone: site.timezone, dateStyle: 'short', timeStyle: 'short' }).format(new Date(alarm.lastOccurredAt)),
              tone: 'warning',
            },
          ] : []}
          emptyDescription="暂无异常时间线"
        />
      </div>
    </div>
  );
}

export function EnergyView({ flowView, summary, overview, devices, zones, onFlowChange, onOpenDevice, onOpenPlant, onOpenTerminal, onOpenAnalysis }: {
  flowView: MonitorEnergyView;
  summary: SiteDashboardSummary | undefined;
  overview: DashboardOverview | undefined;
  devices: readonly MonitorDevice[];
  zones: readonly MonitorTopologyZone[];
  onFlowChange: (view: MonitorEnergyView) => void;
  onOpenDevice: (deviceId: string) => void;
  onOpenPlant: () => void;
  onOpenTerminal: (zoneKey?: string) => void;
  onOpenAnalysis: () => void;
}) {
  const allChillers = devices.filter((item) => item.kind === 'chiller');
  const chillers = allChillers.slice(0, 3);
  const chilledPumps = devices.filter((item) => item.kind === 'chilled-pump');
  const coolingPumps = devices.filter((item) => item.kind === 'cooling-pump');
  const towers = devices.filter((item) => item.kind === 'tower');
  const primaryChiller = chillers[0] ?? null;
  const primaryChilledPump = chilledPumps[0] ?? null;
  const primaryCoolingPump = coolingPumps[0] ?? null;
  const primaryTower = towers[0] ?? null;
  const allPoints = devices.flatMap((item) => item.state.points);
  const coolingOutput = sumPointValues(allChillers, ['chiller.cooling_capacity', '制冷量']);
  const chillerPower = sumPointValues(allChillers, ['chiller.power', '主机功率']);
  const chilledPumpPower = sumPointValues(chilledPumps, ['chwp.power', '水泵功率']);
  const coolingPumpPower = sumPointValues(coolingPumps, ['cwp.power', '水泵功率']);
  const towerPower = sumPointValues(towers, ['cooling_tower.power', '冷却塔功率']);
  const topologyPower = (key: string, fallback: { value: number | null; unit: string | null }) => {
    const value = overview?.topology.find((item) => item.key === key)?.powerKW;
    return value != null ? formatMonitorMetric(value, 'kW', 0) : formatMonitorMetric(fallback.value, fallback.unit ?? 'kW', 0);
  };
  const coolingFlowValue = overview?.coolingSummary?.currentRT ?? coolingOutput.value;
  const coolingFlowUnit = overview?.coolingSummary?.currentRT != null ? 'RT' : coolingOutput.unit ?? 'kW';
  const coolingValue = formatMonitorMetric(coolingFlowValue, coolingFlowUnit, 0);
  const terminalCount = [overview?.topology.find((item) => item.key === 'ahu'), overview?.topology.find((item) => item.key === 'vav-fcu')]
    .reduce((total, item) => total + (item?.total ?? 0), 0);
  const chilledPumpSummary = overview?.topology.find((item) => item.key === 'chw-pump');
  const activeStrategy = overview?.strategies.find((strategy) => strategy.status === 'RUNNING') ?? null;
  const chilledWaterDeltaT = overview?.waterTemperatures.supplyC != null && overview.waterTemperatures.returnC != null
    ? `${(overview.waterTemperatures.returnC - overview.waterTemperatures.supplyC).toFixed(1)} °C`
    : pointMetric(allPoints, ['btu_meter.temperature_difference', '供回水温差', 'plant_delta_t']);
  return (
    <div className="hvac-monitor__energy-workbench" data-testid="hvac-energy-reference-view">
      <div className="hvac-monitor__energy-head">
        <div>
          <div role="group" aria-label="能流视图" className="inline-flex rounded-md border bg-muted/30 p-0.5">
            {([
              ['cooling', '冷量流向'],
              ['power', '功率流向'],
              ['hydraulic', '水力分析'],
            ] as const).map(([value, label]) => (
              <Button key={value} size="sm" variant={flowView === value ? 'secondary' : 'ghost'} aria-pressed={flowView === value} onClick={() => onFlowChange(value)}>{label}</Button>
            ))}
          </div>
          <p className="mt-2 text-sm text-muted-foreground">从冷源、输配到末端追踪能量与水力关系</p>
        </div>
        <Button variant="outline" onClick={onOpenAnalysis}><BarChartOutlined />多设备联动分析</Button>
      </div>
      <div className="hvac-monitor__energy-layout">
        <main className="hvac-monitor__energy-main">
          {flowView === 'cooling' ? (
            <div className="hvac-monitor__energy-flow-visual is-cooling">
              {coolingFlowValue != null && coolingFlowValue > 0 ? (
                <MonitorEnergyFlowChart
                  coolingValue={coolingFlowValue}
                  unit={coolingFlowUnit}
                  pumpLabel={chilledPumpSummary?.running != null && chilledPumpSummary.total != null ? `${chilledPumpSummary.running}/${chilledPumpSummary.total} 泵运行` : '输配系统'}
                  terminalLabel={terminalCount > 0 ? `${terminalCount} 台末端` : '末端系统'}
                  zones={zones}
                  onOpenChiller={primaryChiller ? () => onOpenDevice(primaryChiller.device.id) : undefined}
                  onOpenPump={primaryChilledPump ? () => onOpenDevice(primaryChilledPump.device.id) : undefined}
                  onOpenTerminal={onOpenTerminal}
                />
              ) : <MonitorEmpty description="当前没有可绘制的冷量流向数据" />}
              <div className="hvac-monitor__energy-device-strip is-inline">
                {chillers.length > 0 ? chillers.map((item) => (
                  <button type="button" key={item.device.id} onClick={() => onOpenDevice(item.device.id)}>
                    <strong>{item.device.displayName}</strong>
                    <span>{pointMetric(item.state.points, ['chiller.cooling_capacity', '制冷量'])} · {deviceStateLabel(item.state)}</span>
                  </button>
                )) : <span>当前未提供可打开的冷机设备明细</span>}
              </div>
              <small className="hvac-monitor__energy-scale-note is-inline">{coolingValue} 沿“冷机群 → 冷冻水输配 → 空调末端”传递；没有区域分项冷量时不拆分比例。</small>
            </div>
          ) : flowView === 'power' ? (
            <div className="hvac-monitor__power-flow-visual">
              <svg viewBox="0 0 1000 390" preserveAspectRatio="none" aria-hidden="true">
                <path d="M250 195 H420" />
                <path d="M420 70 V320" />
                <path d="M420 70 H615M420 132 H615M420 195 H615M420 258 H615M420 320 H615" />
              </svg>
              <button type="button" className="hvac-monitor__power-source" onClick={onOpenAnalysis}><span>系统输入功率</span><strong>{formatMonitorMetric(summary?.fastMetrics.currentPower.value, summary?.fastMetrics.currentPower.unit)}</strong><small>进入联动分析查看输入与设备组匹配</small></button>
              <div className="hvac-monitor__power-branches">
                <button type="button" onClick={() => primaryChiller ? onOpenDevice(primaryChiller.device.id) : onOpenPlant()}><span>冷水机组</span><strong>{topologyPower('chiller', chillerPower)}</strong><small>{primaryChiller ? '查看代表冷机' : '进入冷源系统'}</small></button>
                <button type="button" onClick={() => primaryChilledPump ? onOpenDevice(primaryChilledPump.device.id) : onOpenPlant()}><span>冷冻水泵</span><strong>{topologyPower('chw-pump', chilledPumpPower)}</strong><small>{primaryChilledPump ? '查看代表水泵' : '进入冷源系统'}</small></button>
                <button type="button" onClick={() => primaryCoolingPump ? onOpenDevice(primaryCoolingPump.device.id) : onOpenPlant()}><span>冷却水泵</span><strong>{topologyPower('cw-pump', coolingPumpPower)}</strong><small>{primaryCoolingPump ? '查看代表水泵' : '进入冷源系统'}</small></button>
                <button type="button" onClick={() => primaryTower ? onOpenDevice(primaryTower.device.id) : onOpenPlant()}><span>冷却塔</span><strong>{topologyPower('cooling-tower', towerPower)}</strong><small>{primaryTower ? '查看代表冷却塔' : '进入冷源系统'}</small></button>
                <button type="button" onClick={onOpenAnalysis}><span>辅助系统</span><strong>—</strong><small>当前未独立计量 · 进入联动分析</small></button>
              </div>
              <small className="hvac-monitor__energy-scale-note">分项为设备组功率快照；总表与设备组可能存在采样时间和计量边界差异，不强制相加。</small>
            </div>
          ) : (
            <div className="hvac-monitor__hydraulic-visual">
              <div className="hvac-monitor__hydraulic-loop" aria-hidden="true"><span className="is-supply" /><span className="is-return" /></div>
              <button type="button" className="hvac-monitor__hydraulic-terminal is-plant" onClick={onOpenPlant}><strong>冷源侧</strong><span>冷机 / 冷冻水泵</span><small>进入冷源系统</small></button>
              <button type="button" className="hvac-monitor__hydraulic-terminal is-terminal" onClick={() => onOpenTerminal()}><strong>末端侧</strong><span>AHU / FCU / VAV</span><small>进入末端监控</small></button>
              <div className="hvac-monitor__hydraulic-metrics">
                <MonitorMetric label="系统流量" value={pointMetric(allPoints, ['btu_meter.flow_rate', '冷冻水流量', '系统流量'])} note="实时流量测点" onClick={onOpenAnalysis} />
                <MonitorMetric label="供回水温差" value={overview?.waterTemperatures.supplyC != null && overview.waterTemperatures.returnC != null ? `${(overview.waterTemperatures.returnC - overview.waterTemperatures.supplyC).toFixed(1)} °C` : pointMetric(allPoints, ['btu_meter.temperature_difference', '供回水温差', 'plant_delta_t'])} note="冷冻水实时温差" onClick={onOpenAnalysis} />
                <MonitorMetric label="冷冻水压差" value={pointMetric(allPoints, ['压差', 'differential_pressure'])} note={primaryChilledPump ? '查看代表冷冻水泵' : '进入联动分析'} onClick={() => primaryChilledPump ? onOpenDevice(primaryChilledPump.device.id) : onOpenAnalysis()} />
                <MonitorMetric label="水泵频率" value={pointMetric(allPoints, ['chwp.frequency', 'cwp.frequency', '运行频率'])} note={primaryChilledPump ? '查看代表冷冻水泵' : '进入联动分析'} onClick={() => primaryChilledPump ? onOpenDevice(primaryChilledPump.device.id) : onOpenAnalysis()} />
                <MonitorMetric label="阀门开度" value={pointMetric(allPoints, ['valve', '阀门开度'])} note="进入空调末端查看区域侧" onClick={() => onOpenTerminal()} />
                <MonitorMetric label="流量匹配" value="—" note="联动模型结果未提供" onClick={onOpenAnalysis} />
              </div>
            </div>
          )}
        </main>
        <aside className="hvac-monitor__energy-side" aria-label="能流分析业务摘要">
          <MonitorPanel title="能流摘要">
            <div className="hvac-monitor__fact-list">
              <div><span>系统输入功率</span><strong>{formatMonitorMetric(summary?.fastMetrics.currentPower.value, summary?.fastMetrics.currentPower.unit)}</strong></div>
              <div><span>冷机输出</span><strong>{coolingValue}</strong></div>
              <div><span>系统 COP</span><strong>{formatMonitorMetric(overview?.kpis.averageCop ?? summary?.slowMetrics.cop.value, overview?.kpis.averageCop != null ? null : summary?.slowMetrics.cop.unit, 1)}</strong></div>
              <div><span>末端规模</span><strong>{terminalCount > 0 ? `${terminalCount} 台` : '—'}</strong></div>
            </div>
          </MonitorPanel>
          <MonitorPanel title="水力关键参数">
            <div className="hvac-monitor__fact-list">
              <div><span>冷冻水供回水温差</span><strong>{chilledWaterDeltaT}</strong></div>
              <div><span>冷冻水压差</span><strong>{pointMetric(allPoints, ['压差', 'differential_pressure'])}</strong></div>
              <div><span>系统流量</span><strong>{pointMetric(allPoints, ['btu_meter.flow_rate', '冷冻水流量', '系统流量'])}</strong></div>
              <div><span>水泵频率</span><strong>{pointMetric(allPoints, ['chwp.frequency', 'cwp.frequency', '运行频率'])}</strong></div>
            </div>
          </MonitorPanel>
          <MonitorPanel title="当前运行策略">
            <div className="hvac-monitor__energy-strategy">
              <strong>{activeStrategy?.title ?? '当前未提供生效策略'}</strong>
              <small>{activeStrategy ? '当前站点生效策略' : '进入联动分析查看已识别的优化机会'}</small>
              <Button variant="link" size="sm" onClick={onOpenAnalysis}>进入多设备联动分析</Button>
            </div>
          </MonitorPanel>
        </aside>
      </div>
      <Alert>
        <AlertTitle>节能建议不会直接下发设备</AlertTitle>
        <AlertDescription>需要先进入多设备联动分析确认依据，再由运行模式页面执行正式策略变更。</AlertDescription>
      </Alert>
    </div>
  );
}
