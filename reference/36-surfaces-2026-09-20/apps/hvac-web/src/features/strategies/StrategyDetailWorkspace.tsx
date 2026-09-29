import { useMemo, useState } from 'react';
import type { ColumnDef } from '@tanstack/react-table';
import {
  ArrowLeft,
  History,
  FileCheck2,
  CheckCircle2,
  TrendingUp,
  Sliders,
  GitCompare,
  Activity,
  Shield,
} from 'lucide-react';
import { Main } from '@/components/layout/Main';
import { Button } from '@/components/ui/button';
import { Badge } from '@/components/ui/badge';
import { DataTable, StatusPillBadge, type DataTableFeatures } from '@/components/data-table';
import { useDataTable } from '@/hooks/use-data-table';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import {
  ResponsiveContainer,
  LineChart,
  Line,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  Legend,
} from 'recharts';

interface StrategyDetailWorkspaceProps {
  readonly siteId: string;
  readonly strategyId?: string;
}

const SIMULATION_HOURLY_DATA = [
  { time: '08:00', baselineT: 32.0, optimizedT: 28.5, wetBulb: 24.2, copBaseline: 4.12, copOptimized: 4.58 },
  { time: '10:00', baselineT: 32.0, optimizedT: 27.8, wetBulb: 25.1, copBaseline: 4.05, copOptimized: 4.62 },
  { time: '12:00', baselineT: 32.0, optimizedT: 29.2, wetBulb: 26.8, copBaseline: 3.92, copOptimized: 4.41 },
  { time: '14:00', baselineT: 32.0, optimizedT: 30.1, wetBulb: 27.5, copBaseline: 3.85, copOptimized: 4.35 },
  { time: '16:00', baselineT: 32.0, optimizedT: 28.9, wetBulb: 26.0, copBaseline: 4.01, copOptimized: 4.52 },
  { time: '18:00', baselineT: 32.0, optimizedT: 27.2, wetBulb: 24.8, copBaseline: 4.18, copOptimized: 4.70 },
  { time: '20:00', baselineT: 32.0, optimizedT: 26.5, wetBulb: 23.5, copBaseline: 4.25, copOptimized: 4.82 },
];

const STRATEGY_IO_POINTS = [
  {
    name: '室外空气湿球温度',
    tag: 'WEATHER.OUTDOOR_WET_BULB_TEMP',
    direction: 'INPUT',
    unit: '℃',
    currentVal: '25.3',
    gateCondition: '遥测品质校验合格，刷新延迟 < 180s',
  },
  {
    name: '冷站总供冷负荷',
    tag: 'CH_PLANT.TOTAL_COOLING_LOAD',
    direction: 'INPUT',
    unit: 'RT',
    currentVal: '840.5',
    gateCondition: '负荷 ≥ 350 RT 触发自适应调节',
  },
  {
    name: '冷却水出水温度 (实际)',
    tag: 'CW_SYSTEM.SUPPLY_WATER_TEMP',
    direction: 'INPUT',
    unit: '℃',
    currentVal: '28.2',
    gateCondition: '双冗余热电阻温差 < 0.3℃',
  },
  {
    name: '冷水机组运行台数',
    tag: 'CHILLERS.RUNNING_COUNT',
    direction: 'INPUT',
    unit: '台',
    currentVal: '2',
    gateCondition: '冷机运行台数 ≥ 1',
  },
  {
    name: '冷却水供水温度目标设定值',
    tag: 'CW_SYSTEM.SETPOINT_SUPPLY_TEMP',
    direction: 'OUTPUT',
    unit: '℃',
    currentVal: '27.5',
    gateCondition: '硬门禁边界约束：18.0℃ ≤ 设定值 ≤ 34.0℃',
  },
  {
    name: '冷却塔风机频率目标设定',
    tag: 'CT_FANS.SPEED_FREQUENCY_SETPOINT',
    direction: 'OUTPUT',
    unit: 'Hz',
    currentVal: '44.0',
    gateCondition: '变频器共振频率跳跃避让 (28~32Hz 禁用)',
  },
];

const VERSION_DIFFS = [
  {
    param: '湿球逼近度设定目标 (Target Approach Temp)',
    v23: '3.0 ℃',
    v24: '2.5 ℃',
    rationale: '基于近30天冷却塔实测换热传热系数反演，气象过渡期可安全收敛至2.5℃逼近度',
  },
  {
    param: '单次下发最大阶跃步长 (Max Slew Rate)',
    v23: '1.0 ℃ / 周期',
    v24: '0.5 ℃ / 周期',
    rationale: '减缓主机热冲击与高低压排气温差震荡，提高机组长期运行寿命',
  },
  {
    param: '通讯超时回退保护时间 (Fail-safe Timeout)',
    v23: '300 秒',
    v24: '180 秒',
    rationale: '缩短南向网关失联判定时延，确保失效时更快恢复 32.0℃ 保守基准值',
  },
];

export function StrategyDetailWorkspace({
  siteId,
  strategyId = 'STRAT-CW-OPT-01',
}: StrategyDetailWorkspaceProps) {
  const [approvalStatus, setApprovalStatus] = useState<'pending' | 'approved'>('pending');

  type VersionDiffRow = (typeof VERSION_DIFFS)[number];
  type StrategyIoRow = (typeof STRATEGY_IO_POINTS)[number];

  const versionColumns = useMemo<Array<ColumnDef<DataTableFeatures, VersionDiffRow>>>(() => [
    { id: 'param', header: '控制算法调节参数项', cell: ({ row }) => <span className="font-medium text-foreground">{row.original.param}</span> },
    { id: 'v23', header: '原生产版本 (v2.3)', cell: ({ row }) => <span className="font-mono text-muted-foreground tabular-nums">{row.original.v23}</span> },
    { id: 'v24', header: '候选版本 (v2.4)', cell: ({ row }) => <span className="font-mono font-semibold text-foreground tabular-nums">{row.original.v24}</span> },
    { id: 'rationale', header: '变更说明与安全依据', cell: ({ row }) => <span className="text-[11px] leading-relaxed text-muted-foreground">{row.original.rationale}</span> },
  ], []);

  const versionTable = useDataTable({
    key: `strategy-version-diff-${strategyId}`,
    data: [...VERSION_DIFFS],
    columns: versionColumns,
    paginate: false,
    getRowId: (row) => row.param,
  });

  const ioColumns = useMemo<Array<ColumnDef<DataTableFeatures, StrategyIoRow>>>(() => [
    { id: 'name', header: '参数名称', cell: ({ row }) => <span className="font-medium text-foreground">{row.original.name}</span> },
    { id: 'tag', header: 'Brick / Haystack 测点标签', cell: ({ row }) => <span className="font-mono text-[11px] text-muted-foreground">{row.original.tag}</span> },
    { id: 'direction', header: '流向', cell: ({ row }) => <StatusPillBadge tone={row.original.direction === 'INPUT' ? 'info' : 'success'}>{row.original.direction === 'INPUT' ? 'INPUT 消费' : 'OUTPUT 下发'}</StatusPillBadge> },
    { id: 'value', header: '当前采样值', cell: ({ row }) => <span className="font-mono font-semibold text-foreground tabular-nums">{row.original.currentVal} {row.original.unit}</span> },
    { id: 'gate', header: '策略前置门禁准入与保护条件', cell: ({ row }) => <span className="text-[11px] text-muted-foreground">{row.original.gateCondition}</span> },
  ], []);

  const ioTable = useDataTable({
    key: `strategy-io-${strategyId}`,
    data: [...STRATEGY_IO_POINTS],
    columns: ioColumns,
    paginate: false,
    getRowId: (row) => row.tag,
  });

  return (
    <Main className="space-y-6">
      {/* 顶部导航与操作栏 */}
      <div className="flex flex-col gap-3 md:flex-row md:items-center md:justify-between border-b pb-4">
        <div>
          <div className="flex items-center gap-2 text-xs text-muted-foreground mb-1">
            <Button variant="link" size="sm" asChild className="p-0 h-auto text-xs text-muted-foreground">
              <a href={`/sites/${siteId}/strategies`} className="flex items-center gap-1">
                <ArrowLeft className="h-3.5 w-3.5" />
                返回策略中心
              </a>
            </Button>
            <span>/</span>
            <span>策略详情与仿真</span>
            <span>/</span>
            <span className="font-mono text-foreground font-medium">{strategyId}</span>
          </div>
          <div className="flex items-center gap-3">
            <h1 className="text-2xl font-bold tracking-tight text-foreground">
              冷却水供水温度自适应逼近与塔群联控策略
            </h1>
            <Badge variant="outline" className="text-xs font-normal">
              候选版本 v2.4
            </Badge>
            <Badge variant="outline" className="font-normal gap-1.5 text-xs">
              <span className="size-1.5 rounded-full bg-emerald-500" />
              离线仿真通过 (置信度 96.8%)
            </Badge>
          </div>
        </div>

        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" asChild className="h-8 text-xs gap-1.5">
            <a href={`/sites/${siteId}/executions`}>
              <History className="h-3.5 w-3.5" />
              历史执行总账
            </a>
          </Button>
          {approvalStatus === 'pending' ? (
            <Button size="sm" className="h-8 text-xs gap-1.5" onClick={() => setApprovalStatus('approved')}>
              <FileCheck2 className="h-3.5 w-3.5" />
              签署发布上线审批 (v2.4)
            </Button>
          ) : (
            <Badge variant="outline" className="font-normal gap-1.5 text-xs py-1 px-2.5">
              <CheckCircle2 className="size-3.5 text-emerald-500" /> 已完成双人复核签发 · 下一周期投产
            </Badge>
          )}
        </div>
      </div>

      {/* 4 Fact Cards */}
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-xs font-medium text-muted-foreground">预测系统 COP 综合提升</CardTitle>
            <TrendingUp className="size-4 text-muted-foreground" />
          </CardHeader>
          <CardContent className="space-y-1">
            <div className="flex items-baseline gap-2">
              <span className="text-2xl font-bold tabular-nums text-foreground">+8.6%</span>
              <span className="text-xs text-muted-foreground tabular-nums">(4.01 → 4.54)</span>
            </div>
            <p className="text-xs text-muted-foreground">
              基于近 30 天负荷样本与气象回归
            </p>
          </CardContent>
        </Card>

        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-xs font-medium text-muted-foreground">预测单日节电收益</CardTitle>
            <Activity className="size-4 text-muted-foreground" />
          </CardHeader>
          <CardContent className="space-y-1">
            <div className="flex items-baseline gap-2">
              <span className="text-2xl font-bold tabular-nums text-foreground">1,480</span>
              <span className="text-xs text-muted-foreground">kWh / 日</span>
            </div>
            <p className="text-xs text-muted-foreground">
              约省 ¥1,120 / 天
            </p>
          </CardContent>
        </Card>

        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-xs font-medium text-muted-foreground">下发控制周期与防抖步长</CardTitle>
            <Sliders className="size-4 text-muted-foreground" />
          </CardHeader>
          <CardContent className="space-y-1">
            <div className="flex items-baseline gap-2">
              <span className="text-2xl font-bold tabular-nums text-foreground">15 min</span>
              <span className="text-xs text-muted-foreground">/ 周期</span>
            </div>
            <p className="text-xs text-muted-foreground">
              防抖死区 ±0.3℃ · 最大阶跃 ≤ 0.5℃
            </p>
          </CardContent>
        </Card>

        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-xs font-medium text-muted-foreground">硬安全边界防护约束</CardTitle>
            <Shield className="size-4 text-muted-foreground" />
          </CardHeader>
          <CardContent className="space-y-1">
            <div className="flex items-baseline gap-2">
              <span className="text-2xl font-bold tabular-nums text-foreground">18.0℃ ~ 34.0℃</span>
            </div>
            <p className="text-xs text-muted-foreground">
              逼近度 ≥ 2.5℃ · 通讯超时 180s 回退
            </p>
          </CardContent>
        </Card>
      </div>

      {/* 主体工作台：左右双栏布局 (70% 仿真分析与参数差异 / 30% 安全约束与签批状态) */}
      <div className="grid grid-cols-1 lg:grid-cols-12 gap-6 items-start">
        {/* 左侧主要区域 (8 栏 ~ 67%) */}
        <div className="lg:col-span-8 space-y-6">
          {/* 仿真模拟时序曲线图 */}
          <Card className="shadow-xs">
            <CardHeader className="pb-2">
              <CardTitle className="text-base font-semibold flex items-center gap-2">
                <Activity className="size-4 text-muted-foreground" />
                24 小时逐时工况仿真曲线 (优化设定值 vs 基准运行 vs 室外湿球)
              </CardTitle>
              <CardDescription className="text-xs">
                对比动态逼近控制与固定 32℃ 出水温度下的冷机综合 COP
              </CardDescription>
            </CardHeader>
            <CardContent>
              <div className="h-72 w-full pt-2">
                <ResponsiveContainer width="100%" height="100%">
                  <LineChart data={SIMULATION_HOURLY_DATA} margin={{ top: 10, right: 10, left: -10, bottom: 0 }}>
                    <CartesianGrid strokeDasharray="3 3" vertical={false} stroke="hsl(var(--border))" opacity={0.6} />
                    <XAxis dataKey="time" tickLine={false} tick={{ fontSize: 11 }} />
                    <YAxis
                      yAxisId="temp"
                      domain={[20, 36]}
                      tickLine={false}
                      tick={{ fontSize: 11 }}
                      unit="℃"
                    />
                    <YAxis
                      yAxisId="cop"
                      orientation="right"
                      domain={[3.5, 5.2]}
                      tickLine={false}
                      tick={{ fontSize: 11 }}
                      unit=" COP"
                    />
                    <Tooltip
                      contentStyle={{
                        backgroundColor: 'hsl(var(--background))',
                        borderColor: 'hsl(var(--border))',
                        borderRadius: '6px',
                        fontSize: '11px',
                      }}
                    />
                    <Legend wrapperStyle={{ fontSize: '11px', paddingTop: '8px' }} />
                    <Line
                      yAxisId="temp"
                      type="monotone"
                      dataKey="wetBulb"
                      name="室外湿球温度 (℃)"
                      stroke="#94a3b8"
                      strokeDasharray="4 4"
                      dot={false}
                    />
                    <Line
                      yAxisId="temp"
                      type="stepAfter"
                      dataKey="baselineT"
                      name="基准固定出水温 (32℃)"
                      stroke="#cbd5e1"
                      strokeWidth={1.5}
                      dot={false}
                    />
                    <Line
                      yAxisId="temp"
                      type="monotone"
                      dataKey="optimizedT"
                      name="自适应优化出水温 (℃)"
                      stroke="#0284c7"
                      strokeWidth={2}
                      dot={{ r: 3 }}
                    />
                    <Line
                      yAxisId="cop"
                      type="monotone"
                      dataKey="copOptimized"
                      name="优化后冷机 COP"
                      stroke="#10b981"
                      strokeWidth={2}
                      dot={{ r: 3 }}
                    />
                  </LineChart>
                </ResponsiveContainer>
              </div>
            </CardContent>
          </Card>

          {/* 策略版本参数差异与变更理由 (Diff Table) */}
          <Card className="shadow-xs">
            <CardHeader className="p-4 border-b">
              <CardTitle className="text-sm font-semibold flex items-center gap-2">
                <GitCompare className="size-4 text-muted-foreground" />
                版本参数对比 (当前运行 v2.3 vs 候选版本 v2.4)
              </CardTitle>
              <CardDescription className="text-xs">
                查看参数调整项与调整说明
              </CardDescription>
            </CardHeader>
            <CardContent className="p-0">
              <DataTable
                table={versionTable}
                className="gap-0"
                tableAriaLabel="策略版本参数对比"
                getHeaderRowProps={() => ({ className: 'bg-muted/20 text-xs hover:bg-muted/20' })}
                getHeaderCellProps={(header) => ({
                  className:
                    header.id === 'param' ? 'w-[240px] font-medium' :
                    header.id === 'v23' || header.id === 'v24' ? 'w-[120px] font-medium' :
                    'font-medium',
                })}
                getRowProps={() => ({ className: 'text-xs hover:bg-muted/30' })}
              />
            </CardContent>
          </Card>

          {/* 输入 / 输出测点映射与门禁条件 */}
          <Card className="shadow-xs">
            <CardHeader className="p-4 border-b">
              <CardTitle className="text-sm font-semibold flex items-center gap-2">
                <Sliders className="size-4 text-muted-foreground" />
                输入 / 输出测点绑定与门禁前置约束
              </CardTitle>
              <CardDescription className="text-xs">
                明确策略实时消费的遥测点位与下发的控制设定值
              </CardDescription>
            </CardHeader>
            <CardContent className="p-0">
              <DataTable
                table={ioTable}
                className="gap-0"
                tableAriaLabel="策略输入输出测点与门禁"
                getHeaderRowProps={() => ({ className: 'bg-muted/20 text-xs hover:bg-muted/20' })}
                getHeaderCellProps={(header) => ({
                  className:
                    header.id === 'name' ? 'w-[180px] font-medium' :
                    header.id === 'tag' ? 'w-[240px] font-medium' :
                    header.id === 'direction' || header.id === 'value' ? 'w-[110px] font-medium' :
                    'font-medium',
                })}
                getRowProps={() => ({ className: 'text-xs hover:bg-muted/30' })}
              />
            </CardContent>
          </Card>
        </div>

        {/* 右侧上下文与签批区 (4 栏 ~ 33%) */}
        <div className="lg:col-span-4 space-y-6">
          {/* 仿真评估结论卡 */}
          <Card className="shadow-xs">
            <CardHeader className="pb-3">
              <CardTitle className="text-xs font-semibold text-muted-foreground flex items-center gap-1.5">
                <TrendingUp className="size-3.5 text-muted-foreground" />
                运行仿真评估结论
              </CardTitle>
            </CardHeader>
            <CardContent className="space-y-2 text-xs">
              <div className="flex justify-between items-center">
                <span className="text-muted-foreground">仿真验证状态:</span>
                <span className="text-foreground font-semibold flex items-center gap-1">
                  <CheckCircle2 className="size-3.5 text-emerald-500" /> 已通过 168 小时离线回放
                </span>
              </div>
              <div className="flex justify-between items-center">
                <span className="text-muted-foreground">模型拟合优度 (R²):</span>
                <span className="tabular-nums font-bold text-foreground">0.968</span>
              </div>
              <div className="flex justify-between items-center">
                <span className="text-muted-foreground">变异系数 (CV-RMSE):</span>
                <span className="tabular-nums font-medium text-foreground">6.4% <span className="text-muted-foreground font-normal">(符合 ASHRAE 14)</span></span>
              </div>
              <div className="flex justify-between items-center">
                <span className="text-muted-foreground">热负荷欠满足概率:</span>
                <span className="tabular-nums text-foreground font-medium">0.0% <span className="text-muted-foreground font-normal">(舒适度无衰减)</span></span>
              </div>
            </CardContent>
          </Card>

          {/* 安全防线与回退机制 */}
          <Card className="shadow-xs">
            <CardHeader className="pb-3">
              <CardTitle className="text-xs font-semibold text-muted-foreground flex items-center gap-1.5">
                <Shield className="size-3.5 text-muted-foreground" />
                就地安全保护限制
              </CardTitle>
            </CardHeader>
            <CardContent className="space-y-2 text-xs text-muted-foreground">
              <div className="p-2.5 rounded-lg border bg-muted/20 space-y-1">
                <div className="font-semibold text-foreground">• 冷机出水低温防冻保护</div>
                <div className="text-[11px] text-muted-foreground">出水温度 ≤ 16.0℃ 时立即终止负向调节，保持最低极限温差</div>
              </div>
              <div className="p-2.5 rounded-lg border bg-muted/20 space-y-1">
                <div className="font-semibold text-foreground">• 冷却塔防飞水转速限制</div>
                <div className="text-[11px] text-muted-foreground">塔风机最大频率钳位在 48.0Hz，禁止过载运行</div>
              </div>
              <div className="p-2.5 rounded-lg border bg-muted/20 space-y-1">
                <div className="font-semibold text-foreground">• 通信失联自动退避</div>
                <div className="text-[11px] text-muted-foreground">若南向控制器 180s 未收到心跳，恢复本地 32.0℃ 保守设定</div>
              </div>
            </CardContent>
          </Card>

          {/* 双人复核发布上线签批块 */}
          <Card className="shadow-xs">
            <CardHeader className="pb-3">
              <CardTitle className="text-xs font-semibold text-foreground flex items-center gap-1.5">
                <FileCheck2 className="size-4 text-muted-foreground" />
                策略发布上线审批
              </CardTitle>
              <CardDescription className="text-xs">
                策略发布需工程师与总工共同签署确认
              </CardDescription>
            </CardHeader>
            <CardContent className="space-y-3">
              <div className="space-y-1.5 text-xs rounded-lg bg-muted/20 p-2.5 border">
                <div className="flex justify-between items-center">
                  <span className="text-muted-foreground">提请工程师:</span>
                  <span className="font-medium text-foreground">王建平 (主任工程师) · 已签署</span>
                </div>
                <div className="flex justify-between items-center">
                  <span className="text-muted-foreground">终审总工:</span>
                  {approvalStatus === 'approved' ? (
                    <span className="font-medium text-foreground">李明 (总工) · 已批准</span>
                  ) : (
                    <Badge variant="outline" className="font-normal gap-1 text-[11px]">
                      <span className="size-1 rounded-full bg-amber-500" />
                      待总工签字复核
                    </Badge>
                  )}
                </div>
              </div>

              {approvalStatus === 'pending' ? (
                <Button
                  className="w-full h-8 text-xs gap-1.5"
                  onClick={() => setApprovalStatus('approved')}
                >
                  <FileCheck2 className="size-3.5" />
                  签署并发布生效 (投产 v2.4)
                </Button>
              ) : (
                <Button
                  variant="outline"
                  className="w-full h-8 text-xs"
                  onClick={() => setApprovalStatus('pending')}
                >
                  重置审批状态 (测试模拟)
                </Button>
              )}
            </CardContent>
          </Card>
        </div>
      </div>
    </Main>
  );
}
