import { useState, type ReactNode } from 'react';
import { Cloud, Flame, FlaskConical, Gauge, Moon, Settings, SlidersHorizontal, Zap } from 'lucide-react';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardAction, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Empty, EmptyDescription } from '@/components/ui/empty';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip';
import type { SiteDashboardSummary } from '@/api/generated/platformGateway.gen';
import type { DashboardOverview } from '@/api/dashboard-overview';
import { DualAxisTimeSeriesChart } from '@/shared/charts/TimeSeriesChart';
import {
  deviceStateBadgeClass,
  deviceStateLabel,
  formatMonitorMetric,
  type MonitorDevice,
} from './model';
import { MonitorMetric } from './MonitorOverview';
import { MonitorLinkageGraph } from './MonitorLinkageGraph';

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

export function LinkageAnalysisPage({ devices, summary, overview, selectedDeviceId, onOpenDevice, onOpenPlant, onOpenTerminal, onOpenAnomaly, onOpenModes }: {
  devices: readonly MonitorDevice[];
  summary: SiteDashboardSummary | undefined;
  overview: DashboardOverview | undefined;
  selectedDeviceId: string | null;
  onOpenDevice: (deviceId: string) => void;
  onOpenPlant: () => void;
  onOpenTerminal: () => void;
  onOpenAnomaly: (alarmId?: string) => void;
  onOpenModes: (opportunityRank?: number) => void;
}) {
  const plant = devices.filter((item) => item.kind !== 'terminal' && item.kind !== 'other');
  const selectedAnalysisDevice = selectedDeviceId ? plant.find((item) => item.device.id === selectedDeviceId) ?? null : null;
  const topOpportunity = overview?.opportunities[0] ?? null;
  const activeAlarmCount = devices.reduce((total, item) => total + item.activeAlarms.length, 0);
  const coolingByAt = new Map(overview?.coolingTrend?.map((point) => [point.at, point.actual] as const) ?? []);
  const powerPoints = overview?.loadTrend.map((point) => ({ x: point.at, value: point.actual })) ?? [];
  const coolingPoints = overview?.coolingTrend?.map((point) => ({ x: point.at, value: point.actual == null ? null : point.actual * 3.517 })) ?? [];
  const copPoints = overview?.loadTrend.map((point) => {
    const coolingRT = coolingByAt.get(point.at);
    const coolingKW = coolingRT == null ? null : coolingRT * 3.517;
    return {
      x: point.at,
      value: point.actual != null && point.actual > 0 && coolingKW != null ? Number((coolingKW / point.actual).toFixed(2)) : null,
    };
  }) ?? [];
  const hasTrend = powerPoints.length > 0 || coolingPoints.length > 0;
  const focusSection = (id: string) => document.getElementById(id)?.scrollIntoView({ behavior: 'smooth', block: 'nearest' });

  return (
    <div className="hvac-monitor__deep-page">
      <section className="hvac-monitor__kpi-grid is-five">
        <MonitorMetric label="系统 COP" value={formatMonitorMetric(summary?.slowMetrics.cop.value, summary?.slowMetrics.cop.unit)} note="当前系统效率 · 查看系统趋势" onClick={() => focusSection('linkage-trend')} />
        <MonitorMetric label="冷机 / 水泵匹配度" value="—" note="当前未提供联动模型结果 · 查看匹配分析" onClick={() => focusSection('linkage-scatter')} />
        <MonitorMetric label="冷却塔协同度" value="—" note="当前未提供联动模型结果 · 查看设备联动" onClick={() => focusSection('linkage-device-table')} />
        <MonitorMetric label="末端需求匹配度" value="—" note="当前未提供联动模型结果 · 查看匹配分析" onClick={() => focusSection('linkage-scatter')} />
        <MonitorMetric
          label="节能潜力"
          value={topOpportunity?.savingKWhPerDay != null ? `${topOpportunity.savingKWhPerDay.toFixed(0)} kWh/日` : '—'}
          note={topOpportunity ? `最高单项机会：${topOpportunity.title} · 查看建议` : '暂无已识别节能机会 · 查看建议'}
          onClick={() => focusSection('linkage-opportunities')}
        />
      </section>

      <div className="hvac-monitor__linkage-primary-grid">
        <MonitorPanel
          id="linkage-scatter"
          title="设备联动关系"
          action={(
            <div className="flex items-center gap-1">
              <Button variant="link" size="sm" onClick={onOpenPlant}>冷源系统</Button>
              <Button variant="link" size="sm" onClick={onOpenTerminal}>空调末端</Button>
            </div>
          )}
        >
          <MonitorLinkageGraph devices={devices} selectedDeviceId={selectedDeviceId} onOpenPlant={onOpenPlant} onOpenTerminal={onOpenTerminal} />
        </MonitorPanel>
        <div className="hvac-monitor__linkage-side">
          <MonitorPanel title="分析摘要">
            <div className="hvac-monitor__fact-list">
              <div><span>系统 COP</span><strong>{formatMonitorMetric(summary?.slowMetrics.cop.value, summary?.slowMetrics.cop.unit)}</strong></div>
              <div><span>活动告警</span><strong>{activeAlarmCount}</strong><small>{activeAlarmCount > 0 ? '存在需要核查的设备异常' : '当前未发现活动告警'}</small></div>
              <div><span>分析设备</span><strong>{selectedAnalysisDevice?.device.displayName ?? '系统级联动'}</strong></div>
              <div><span>节能机会</span><strong>{overview?.opportunities.length ?? 0}</strong><small>仅展示后端已识别机会</small></div>
            </div>
          </MonitorPanel>
          <MonitorPanel title="分析结论">
            <Alert>
              <AlertTitle>{topOpportunity ? '已发现节能机会，联动根因仍待验证' : '当前暂无已验证联动结论'}</AlertTitle>
              <AlertDescription>{topOpportunity ? '关系图展示系统工程链路与当前设备事实，不把告警先后或同时波动当作因果结论。' : '当前没有足够证据形成系统联动结论。'}</AlertDescription>
            </Alert>
          </MonitorPanel>
        </div>
      </div>

      <div className="hvac-monitor__linkage-secondary-grid">
        <MonitorPanel id="linkage-trend" title="系统趋势（近 24 小时）">
          {hasTrend ? (
            <DualAxisTimeSeriesChart
              primarySeries={[
                { key: 'input-power', label: '输入功率', points: powerPoints, tone: 'primary', showPoints: true },
                { key: 'cooling-output', label: '冷机输出', points: coolingPoints, tone: 'success', showPoints: true },
              ]}
              secondarySeries={[{ key: 'system-cop', label: '系统 COP', points: copPoints, tone: 'warning' }]}
              primaryUnit="kW"
              secondaryUnit="COP"
              secondaryDomain={[0, 8]}
              xType="time"
              xLabelFormatter={(value) => new Date(value).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit', hour12: false })}
              style={{ height: 260, width: '100%' }}
              ariaLabel="系统输入功率、冷机输出与系统 COP 的近 24 小时趋势"
              compact
            />
          ) : <MonitorEmpty description="暂无可验证系统趋势" />}
        </MonitorPanel>
        <MonitorPanel id="linkage-opportunities" title="优化建议">
          {topOpportunity ? (
            <div className="space-y-3">
              <div className="hvac-monitor__opportunity-list">
                {overview?.opportunities.slice(0, 3).map((opportunity) => (
                  <button type="button" key={opportunity.rank} onClick={() => onOpenModes(opportunity.rank)}>
                    <i>{opportunity.rank}</i>
                    <span><strong>{opportunity.title}</strong><small>{opportunity.savingKWhPerDay != null ? `预计 ${opportunity.savingKWhPerDay.toFixed(0)} kWh/日` : '节能量待评估'} · 进入运行模式评审</small></span>
                    <Badge variant={opportunity.priority === 'HIGH' ? 'destructive' : 'outline'}>{opportunity.priority === 'HIGH' ? '高优先' : opportunity.priority === 'MEDIUM' ? '中优先' : '低优先'}</Badge>
                  </button>
                ))}
              </div>
              <Button className="w-full" onClick={() => onOpenModes(topOpportunity?.rank)}>进入运行模式评审</Button>
              <small className="text-muted-foreground">评审参数计划并二次确认后才可执行</small>
            </div>
          ) : (
            <div className="space-y-3">
              <Alert>
                <AlertTitle>当前暂无已验证优化建议</AlertTitle>
                <AlertDescription>待联动分析形成可评审建议后进入运行模式。</AlertDescription>
              </Alert>
              <Button className="w-full" disabled>应用优化建议</Button>
            </div>
          )}
        </MonitorPanel>
      </div>

      <MonitorPanel
        id="linkage-device-table"
        title="设备联动表格"
        action={selectedAnalysisDevice ? <Badge variant="outline">分析设备：{selectedAnalysisDevice.device.displayName}</Badge> : undefined}
      >
        <Table aria-label="设备联动表格">
          <TableHeader>
            <TableRow className="hover:bg-transparent">
              <TableHead className="min-w-44">设备</TableHead>
              <TableHead className="w-32">运行状态</TableHead>
              <TableHead className="min-w-64">关键参数</TableHead>
              <TableHead className="w-36">分析线索</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {plant.map((item) => (
              <TableRow
                key={item.device.id}
                className={item.device.id === selectedDeviceId ? 'hvac-monitor__linkage-row is-selected cursor-pointer' : 'hvac-monitor__linkage-row cursor-pointer'}
                onClick={(event) => {
                  const target = event.target as HTMLElement;
                  if (target.closest('button,a')) return;
                  onOpenDevice(item.device.id);
                }}
              >
                <TableCell>
                  <Button variant="link" className="h-auto p-0" onClick={() => onOpenDevice(item.device.id)}>{item.device.displayName}</Button>
                </TableCell>
                <TableCell><Badge variant="outline" className={deviceStateBadgeClass(item.state)}>{deviceStateLabel(item.state)}</Badge></TableCell>
                <TableCell className="text-muted-foreground">
                  {item.state.points.filter((point) => point.state === 'PRESENT').slice(0, 3).map((point) => `${point.label} ${point.displayValue}${point.unit ?? ''}`).join(' · ') || '—'}
                </TableCell>
                <TableCell>
                  {item.activeAlarms.length
                    ? <Button variant="link" className="h-auto p-0" onClick={() => onOpenAnomaly(item.activeAlarms[0]!.alarmId)}>存在活动异常</Button>
                    : <span className="text-muted-foreground">暂无异常线索</span>}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </MonitorPanel>

      <div className="hvac-monitor__deep-actions">
        <Button variant="outline" onClick={() => onOpenAnomaly()}>查看异常线索</Button>
        <Button onClick={() => onOpenModes(topOpportunity?.rank)}>进入运行模式 / 场景切换</Button>
      </div>
    </div>
  );
}

const modeOptions = [
  { label: '自动', icon: <SlidersHorizontal />, description: '按策略自动调度' },
  { label: '夏季制冷', icon: <FlaskConical />, description: '高温制冷工况' },
  { label: '夜间模式', icon: <Moon />, description: '夜间低扰动运行' },
  { label: '节能模式', icon: <Zap />, description: '优先系统能效' },
  { label: '低负荷模式', icon: <Gauge />, description: '低负荷设备组合' },
  { label: '过渡季', icon: <Cloud />, description: '过渡季节策略' },
  { label: '冬季供暖', icon: <Flame />, description: '冬季供热工况' },
  { label: '自定义', icon: <Settings />, description: '按参数计划执行' },
] as const;

type RunningModeName = (typeof modeOptions)[number]['label'];

export function RunningModesPage({ overview, selectedOpportunityRank, onOpenAnalysis, onOpenOverview }: {
  overview: DashboardOverview | undefined;
  selectedOpportunityRank: number | null;
  onOpenAnalysis: () => void;
  onOpenOverview: () => void;
}) {
  const [mode, setMode] = useState<RunningModeName>('自动');
  const activeStrategy = overview?.strategies.find((strategy) => strategy.status === 'RUNNING') ?? null;
  const topOpportunity = selectedOpportunityRank != null
    ? overview?.opportunities.find((opportunity) => opportunity.rank === selectedOpportunityRank) ?? null
    : overview?.opportunities[0] ?? null;

  return (
    <div className="hvac-monitor__deep-page">
      <div className="hvac-monitor__mode-summary">
        <section><span>当前运行策略</span><strong>{activeStrategy?.title ?? '未提供'}</strong><small>{activeStrategy ? '站点当前生效策略' : '当前数据未提供生效策略'}</small></section>
        <i aria-hidden="true">→</i>
        <section className="is-candidate"><span>候选运行模式</span><strong>{mode}</strong><small>仅评估，不立即下发</small></section>
        <section className="is-authority"><span>执行入口</span><strong>二次确认后执行</strong><small>参数计划完整时才可提交</small></section>
      </div>

      <Alert>
        <AlertTitle>选择模式仅进入评估</AlertTitle>
        <AlertDescription>参数计划完整并二次确认后才执行。</AlertDescription>
      </Alert>

      <div className="hvac-monitor__mode-layout">
        <MonitorPanel title="运行模式">
          <div className="hvac-monitor__mode-grid">
            {modeOptions.map((item) => <button key={item.label} type="button" className={mode === item.label ? 'is-selected' : ''} onClick={() => setMode(item.label)}>{item.icon}<strong>{item.label}</strong><span>{mode === item.label ? '候选模式' : item.description}</span></button>)}
          </div>
        </MonitorPanel>
        <MonitorPanel title="预计影响">
          <dl className="grid gap-3 text-sm">
            {[
              ['预计 COP', '—'],
              ['预计输入功率', '—'],
              ['预计节能率', '—'],
              ['预计费用节省', '—'],
              ['舒适度影响', '尚未评估'],
              ['受影响区域', '尚未评估'],
            ].map(([label, value]) => (
              <div key={label} className="grid grid-cols-[minmax(0,1fr)_auto] gap-3 border-b pb-2 last:border-0 last:pb-0">
                <dt className="text-muted-foreground">{label}</dt>
                <dd className="font-medium">{value}</dd>
              </div>
            ))}
          </dl>
        </MonitorPanel>
      </div>

      <MonitorPanel title="参数变化">
        <div className="hvac-monitor__parameter-change"><span>当前运行模式</span><strong>{activeStrategy?.title ?? '当前运行模式未提供'}</strong><b>→</b><span>候选运行模式</span><strong>{mode}</strong></div>
        <div className="mt-4">
          {topOpportunity ? (
            <Alert>
              <AlertTitle>来源节能机会：{topOpportunity.title}</AlertTitle>
              <AlertDescription>{topOpportunity.savingKWhPerDay != null ? `站点读模型预计该机会单日节能 ${topOpportunity.savingKWhPerDay.toFixed(0)} kWh；当前尚未形成可执行参数计划。` : '当前尚未形成可执行参数计划。'}</AlertDescription>
            </Alert>
          ) : <MonitorEmpty description="当前站点尚未提供该候选模式的正式参数变化计划" />}
        </div>
      </MonitorPanel>

      <MonitorPanel title="执行状态">
        <div className="hvac-monitor__execution-rail">{['待提交', '正在提交', '网关已接收', '等待设备确认', '执行结果'].map((item, index) => <div key={item} className={index === 0 ? 'is-current' : ''}><i>{index + 1}</i><span>{item}</span></div>)}</div>
        <Alert className="mt-4 border-amber-300 bg-amber-50/60 dark:border-amber-900 dark:bg-amber-950/20">
          <AlertTitle>确认超时不等于执行失败</AlertTitle>
          <AlertDescription>超时时标记“执行结果未知”，先核查设备状态。</AlertDescription>
        </Alert>
      </MonitorPanel>

      <div className="hvac-monitor__deep-actions">
        <Button variant="outline" onClick={onOpenAnalysis}>返回联动分析</Button>
        <Button variant="outline" onClick={onOpenOverview}>返回运行监控</Button>
        <TooltipProvider>
          <Tooltip>
            <TooltipTrigger asChild><span tabIndex={0}><Button size="lg" disabled>应用新模式</Button></span></TooltipTrigger>
            <TooltipContent>当前站点尚未提供可执行的运行模式计划</TooltipContent>
          </Tooltip>
        </TooltipProvider>
      </div>
    </div>
  );
}
