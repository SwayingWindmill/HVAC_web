import { useState } from 'react';
import {
  AlertCircle,
  AlertTriangle,
  Bell,
  CheckCircle2,
  FileText,
  RefreshCw,
  ShieldAlert,
  Wrench,
  Zap,
} from 'lucide-react';
import type { CurrentPrincipalResponse, Site } from '@/api/generated/platformGateway.gen';
import { StatusBadge } from '@/components/status-badge';
import { Main } from '@/components/layout/Main';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import {
  SelectGroup,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { cn } from '@/lib/utils';

export interface AlarmCommandDeskProps {
  readonly site: Readonly<Site>;
  readonly principal: CurrentPrincipalResponse;
  readonly runtime?: unknown;
  readonly searchState?: unknown;
  readonly onSearchChange?: (patch: unknown) => void;
  readonly registerUnsavedDraft?: unknown;
  readonly registerProtectedResource?: unknown;
}

interface AlarmItem {
  readonly id: string;
  readonly severity: 'CRITICAL' | 'MAJOR' | 'WARNING' | 'INFO';
  readonly severityLabel: string;
  readonly title: string;
  readonly device: string;
  readonly location: string;
  readonly occurredAt: string;
  readonly duration: string;
  readonly triggerMetric: string;
  readonly triggerValue: string;
  readonly thresholdValue: string;
  readonly impact: string;
  readonly sop: readonly string[];
}

const ALARMS: readonly AlarmItem[] = [
  {
    id: 'alm-01',
    severity: 'CRITICAL',
    severityLabel: '紧急',
    title: 'CH-03 冷水机组蒸发压力过低联锁停机',
    device: '3# 螺杆式冷水主机 (CH-03)',
    location: '中央冷站 B1 机房',
    occurredAt: '14 分钟前',
    duration: '00:14:22',
    triggerMetric: '蒸发饱和压力',
    triggerValue: '238 kPa',
    thresholdValue: '< 280 kPa (持续 15s)',
    impact: '机组启动防冻保护联锁跳闸，冷负荷已自动切转至 CH-01 与 CH-02，供水温微升 0.2K',
    sop: [
      '核查蒸发器冷冻水水流开关及循环泵运行状态，确认是否有断水现象',
      '检查电子膨胀阀开度及制冷剂回路干燥过滤器压差',
      '巡视蒸发器筒体接头，排查冷媒是否存在微量泄漏迹象',
      '现场排故确认后，在本地电控柜进行故障复位方可重新列入群控调度',
    ],
  },
  {
    id: 'alm-02',
    severity: 'MAJOR',
    severityLabel: '重要',
    title: 'PMP-03 冷冻水循环泵电机轴承振动超限',
    device: '3# 冷冻水泵 (PMP-03)',
    location: '中央冷站 B1 泵房',
    occurredAt: '42 分钟前',
    duration: '00:42:10',
    triggerMetric: '电机驱动端振动烈度',
    triggerValue: '4.8 mm/s',
    thresholdValue: '> 4.5 mm/s (ISO 10816-3 预警线)',
    impact: '泵体运行出现异响，长期高振动可能导致轴承疲劳破损',
    sop: [
      '使用便携式测振仪复核 X/Y/Z 三向振动速度与加速度谱',
      '检查水泵与电机联轴器同轴度偏差及地脚螺栓紧固状态',
      '如振动持续超标，建议下发检修工单切换至备用泵',
    ],
  },
  {
    id: 'alm-03',
    severity: 'MAJOR',
    severityLabel: '重要',
    title: 'AHU-07 空气处理机组初中效滤网压差偏高',
    device: '3F 研发区组合式空调箱 (AHU-07)',
    location: '3F 北侧空调机房',
    occurredAt: '1 小时前',
    duration: '01:15:00',
    triggerMetric: '滤网前后静压差',
    triggerValue: '240 Pa',
    thresholdValue: '> 200 Pa (需清洗更换)',
    impact: '末端风量下降约 12%，风机变频电耗上升 8%',
    sop: [
      '安排物业巡检人员核实滤网积尘状况',
      '若积尘严重，派发工单更换备用初效/中效滤网滤料',
      '清洗后复测进出风压差是否恢复至 120 Pa 以下标称区间',
    ],
  },
  {
    id: 'alm-04',
    severity: 'WARNING',
    severityLabel: '一般',
    title: 'CT-01 冷却塔出水温度偏高逼近度偏离',
    device: '1# 开式冷却塔 (CT-01)',
    location: '主楼裙房屋顶',
    occurredAt: '2 小时前',
    duration: '02:08:45',
    triggerMetric: '冷却水出水温度逼近度',
    triggerValue: '3.8 K',
    thresholdValue: '> 3.2 K (热力性能衰退)',
    impact: '主机冷凝温度被动抬升，整体 COP 降低约 2.1%',
    sop: [
      '检查布水器喷头是否有堵塞或布水不均现象',
      '核查填料表面结垢附着与进风口百叶导流顺畅度',
      '适当提升塔风机运行频率以压低逼近度',
    ],
  },
  {
    id: 'alm-05',
    severity: 'WARNING',
    severityLabel: '一般',
    title: '2F 会议室温度传感器通信信号偶发抖动',
    device: '2F-TT-04 (环境传感器)',
    location: '2F 报告厅',
    occurredAt: '3 小时前',
    duration: '03:22:10',
    triggerMetric: 'Modbus 通信丢包率',
    triggerValue: '4.2 %',
    thresholdValue: '> 3.0 %',
    impact: '采集延迟微增，但历史缓存补齐正常，不影响温度调节回路',
    sop: [
      '排查 RS485 总线屏蔽层接地与接线端子松动情况',
    ],
  },
];

export function AlarmCommandDesk({ site }: AlarmCommandDeskProps) {
  const [selectedId, setSelectedId] = useState<string>('alm-01');
  const [filterSeverity, setFilterSeverity] = useState<string>('all');

  const selectedAlarm = ALARMS.find((a) => a.id === selectedId) ?? ALARMS[0];
  const filteredAlarms = ALARMS.filter((a) => {
    if (filterSeverity !== 'all' && a.severity !== filterSeverity) return false;
    return true;
  });

  return (
    <Main className="space-y-6" data-testid="alarm-command-desk">
      {/* Header */}
      <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <div className="flex items-center gap-2.5">
            <h1 className="text-2xl font-bold tracking-tight text-foreground">告警</h1>
            <Badge variant="outline" className="text-xs font-normal">
              活动告警 3 件
            </Badge>
          </div>
          <p className="mt-1 text-sm text-muted-foreground">{site.displayName}</p>
        </div>

        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" className="h-8 gap-1.5 text-xs font-medium">
            <RefreshCw className="size-3.5" />
            刷新
          </Button>
        </div>
      </div>

      {/* Alarm Status Stream Ribbon (4 Standard Cards) */}
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">紧急告警</CardTitle>
            <ShieldAlert className="size-4 text-muted-foreground" aria-hidden="true" />
          </CardHeader>
          <CardContent className="space-y-1">
            <div className="text-2xl font-bold tracking-tight text-foreground tabular-nums">
              1 <span className="text-xs font-normal text-muted-foreground">项未消警</span>
            </div>
            <p className="text-xs text-muted-foreground">
              核心冷水机组联锁保护跳闸
            </p>
          </CardContent>
        </Card>

        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">重要告警</CardTitle>
            <AlertTriangle className="size-4 text-muted-foreground" aria-hidden="true" />
          </CardHeader>
          <CardContent className="space-y-1">
            <div className="text-2xl font-bold tracking-tight text-foreground tabular-nums">
              2 <span className="text-xs font-normal text-muted-foreground">项需关注</span>
            </div>
            <p className="text-xs text-muted-foreground">
              水泵振动偏高 · 空调滤网阻力偏大
            </p>
          </CardContent>
        </Card>

        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">一般预警</CardTitle>
            <Bell className="size-4 text-muted-foreground" aria-hidden="true" />
          </CardHeader>
          <CardContent className="space-y-1">
            <div className="text-2xl font-bold tracking-tight text-foreground tabular-nums">
              5 <span className="text-xs font-normal text-muted-foreground">项跟踪中</span>
            </div>
            <p className="text-xs text-muted-foreground">
              传感器轻度漂移 · 通信偶发抖动
            </p>
          </CardContent>
        </Card>

        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">今日已恢复/消警</CardTitle>
            <CheckCircle2 className="size-4 text-muted-foreground" aria-hidden="true" />
          </CardHeader>
          <CardContent className="space-y-1">
            <div className="text-2xl font-bold tracking-tight text-foreground tabular-nums">
              14 <span className="text-xs font-normal text-muted-foreground">已处置</span>
            </div>
            <p className="text-xs text-muted-foreground">
              平均处置闭环 8.5 分钟
            </p>
          </CardContent>
        </Card>
      </div>

      {/* Split Alarm Command Desk */}
      <div className="grid grid-cols-1 gap-6 lg:grid-cols-[1.1fr_1.4fr]">
        {/* Left: Alarm List Feed */}
        <Card className="shadow-xs">
          <CardHeader className="pb-3 border-b">
            <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
              <CardTitle className="text-base font-semibold">当前告警</CardTitle>
              <Select value={filterSeverity} onValueChange={setFilterSeverity}>
                <SelectTrigger className="w-36" aria-label="筛选告警等级">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent><SelectGroup>
                  <SelectItem value="all">全部告警 ({ALARMS.length})</SelectItem>
                  <SelectItem value="CRITICAL">紧急 ({ALARMS.filter((alarm) => alarm.severity === 'CRITICAL').length})</SelectItem>
                  <SelectItem value="MAJOR">重要 ({ALARMS.filter((alarm) => alarm.severity === 'MAJOR').length})</SelectItem>
                  <SelectItem value="WARNING">一般 ({ALARMS.filter((alarm) => alarm.severity === 'WARNING').length})</SelectItem>
                </SelectGroup></SelectContent>
              </Select>
            </div>
          </CardHeader>
          <CardContent className="p-0 divide-y max-h-[560px] overflow-y-auto">
            {filteredAlarms.map((alarm) => {
              const isSelected = alarm.id === selectedId;
              return (
                <button
                  key={alarm.id}
                  type="button"
                  onClick={() => setSelectedId(alarm.id)}
                  className={cn(
                    'w-full text-left p-3.5 transition-colors flex flex-col gap-2 hover:bg-muted/40',
                    isSelected && 'bg-muted/60 border-l-2 border-l-primary',
                  )}
                >
                  <div className="flex items-start justify-between gap-2">
                    <div className="flex items-center gap-2">
                      <StatusBadge
                        tone={
                          alarm.severity === 'CRITICAL'
                            ? 'destructive'
                            : alarm.severity === 'MAJOR'
                              ? 'warning'
                              : 'neutral'
                        }
                        pulse={alarm.severity === 'CRITICAL'}
                      >
                        {alarm.severityLabel}
                      </StatusBadge>
                      <strong className="text-xs font-semibold text-foreground truncate max-w-[220px]">
                        {alarm.title}
                      </strong>
                    </div>
                    <span className="text-[11px] text-muted-foreground shrink-0 tabular-nums">{alarm.duration}</span>
                  </div>

                  <div className="flex items-center justify-between text-[11px] text-muted-foreground">
                    <span>{alarm.device}</span>
                    <span>{alarm.occurredAt}</span>
                  </div>
                </button>
              );
            })}
          </CardContent>
        </Card>

        {/* Right: Selected Alarm Deep Investigation Console */}
        <Card className="shadow-xs">
          <CardHeader className="pb-3 border-b">
            <div className="flex items-start justify-between gap-3">
              <div>
                <div className="flex items-center gap-2">
                  <StatusBadge
                    tone={
                      selectedAlarm.severity === 'CRITICAL'
                        ? 'destructive'
                        : selectedAlarm.severity === 'MAJOR'
                          ? 'warning'
                          : 'neutral'
                    }
                    pulse={selectedAlarm.severity === 'CRITICAL'}
                  >
                    {selectedAlarm.severityLabel}告警
                  </StatusBadge>
                  <CardTitle className="text-base font-semibold">{selectedAlarm.title}</CardTitle>
                </div>
                <CardDescription className="mt-1 text-xs">
                  触发对象：{selectedAlarm.device} · 位置：{selectedAlarm.location} · 持续时长：{selectedAlarm.duration}
                </CardDescription>
              </div>

              <div className="flex items-center gap-1.5 shrink-0">
                <Button variant="default" size="sm" className="h-8 gap-1.5 text-xs font-medium">
                  <Wrench className="size-3.5" />
                  派发工单
                </Button>
                <Button variant="outline" size="sm" className="h-8 gap-1.5 text-xs font-medium">
                  <CheckCircle2 className="size-3.5" />
                  确认告警
                </Button>
              </div>
            </div>
          </CardHeader>

          <CardContent className="space-y-4 pt-4">
            {/* Snapshot Telemetry Data */}
            <div className="rounded-lg border bg-muted/20 p-3.5 space-y-2">
              <span className="text-xs font-semibold text-foreground flex items-center gap-1.5">
                <Zap className="size-3.5 text-primary" />
                触发时测点数据快照
              </span>
              <div className="grid grid-cols-2 gap-3 text-xs pt-1">
                <div>
                  <span className="text-muted-foreground text-[11px] block">监控测点参数</span>
                  <p className="font-medium text-foreground mt-0.5">{selectedAlarm.triggerMetric}</p>
                </div>
                <div>
                  <span className="text-muted-foreground text-[11px] block">触发实测值 vs 设定阈值</span>
                  <p className="tabular-nums font-semibold text-foreground mt-0.5">
                    {selectedAlarm.triggerValue} <span className="text-xs text-muted-foreground font-normal">(阈值: {selectedAlarm.thresholdValue})</span>
                  </p>
                </div>
              </div>
            </div>

            {/* Impact Assessment */}
            <div className="rounded-lg border bg-muted/30 p-3.5 space-y-1.5 text-xs">
              <span className="font-medium text-foreground flex items-center gap-1.5">
                <AlertCircle className="size-3.5 text-muted-foreground" />
                系统影响分析
              </span>
              <p className="text-muted-foreground leading-relaxed">
                {selectedAlarm.impact}
              </p>
            </div>

            {/* Recommended SOP Checklist */}
            <div className="space-y-2">
              <span className="text-xs font-semibold text-foreground flex items-center gap-1.5">
                <FileText className="size-3.5 text-primary" />
                处置建议 (SOP)
              </span>
              <div className="rounded-lg border bg-card divide-y text-xs">
                {selectedAlarm.sop.map((step, idx) => (
                  <div key={idx} className="p-3 flex items-start gap-2.5">
                    <span className="flex size-5 shrink-0 items-center justify-center rounded-full bg-muted font-mono text-[10px] font-medium text-foreground">
                      {idx + 1}
                    </span>
                    <span className="text-muted-foreground leading-relaxed">{step}</span>
                  </div>
                ))}
              </div>
            </div>
          </CardContent>
        </Card>
      </div>
    </Main>
  );
}
