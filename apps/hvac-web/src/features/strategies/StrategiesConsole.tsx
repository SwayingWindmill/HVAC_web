import { useMemo, useState } from 'react';
import type { ColumnDef } from '@tanstack/react-table';
import { parseAsStringEnum, useQueryState } from 'nuqs';
import {
  Play,
  Pause,
  Clock,
  RotateCcw,
  Search,
  AlertTriangle,
  Shield,
  Layers,
  Sparkles,
} from 'lucide-react';
import { DataTableBlock } from '@/blocks/data-table';
import { Main } from '@/components/layout/Main';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Checkbox } from '@/components/ui/checkbox';
import {
  DataTable,
  DataTableAdvancedToolbar,
  DataTableFilterList,
  DataTablePagination,
  DataTableSortList,
  type DataTableFeatures,
} from '@/components/data-table';
import { StatusBadge } from '@/components/status-badge';
import { useDataTable } from '@/hooks/use-data-table';
import type { ExtendedColumnFilter, JoinOperator } from '@/lib/data-table-types';
import { getFiltersStateParser } from '@/lib/parsers';
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';

export type StrategyStatus = 'active' | 'shadow' | 'suspended' | 'pending_approval';
export type StrategyScope = 'chiller_plant' | 'chw_distribution' | 'cooling_towers' | 'terminal_ahu' | 'energy_storage';
export type StrategyType = 'setpoint_reset' | 'staging_optimization' | 'demand_response' | 'approach_control';

const STRATEGY_STATUS_OPTIONS = [
  { label: '运行中', value: 'active' },
  { label: '仿真校验', value: 'shadow' },
  { label: '待审批', value: 'pending_approval' },
  { label: '已停用', value: 'suspended' },
] as const;
const STRATEGY_FILTER_COLUMN_IDS = ['status'] as const;
const STRATEGY_FILTERS_QUERY_KEY = 'strategyFilters';
const STRATEGY_JOIN_OPERATOR_QUERY_KEY = 'strategyJoinOperator';

export interface StrategyItem {
  readonly id: string;
  readonly code: string;
  readonly name: string;
  readonly scope: StrategyScope;
  readonly scopeLabel: string;
  readonly type: StrategyType;
  readonly status: StrategyStatus;
  readonly version: string;
  readonly priority: number; // 1 (Highest) to 10
  readonly executionInterval: string;
  readonly triggerMode: string;
  readonly lastRunAt: string;
  readonly lastResult: 'success' | 'warning' | 'skipped';
  readonly description: string;
  readonly inputs: readonly string[];
  readonly outputs: readonly string[];
  readonly guardrails: readonly {
    readonly param: string;
    readonly limit: string;
    readonly description: string;
  }[];
  readonly failSafeAction: string;
}

function matchesStrategyAdvancedFilter(
  strategy: StrategyItem,
  filter: ExtendedColumnFilter<StrategyItem>,
) {
  if (filter.id !== 'status') return true;
  const values = Array.isArray(filter.value) ? filter.value : [filter.value];

  switch (filter.operator) {
    case 'eq':
    case 'inArray':
      return values.includes(strategy.status);
    case 'ne':
    case 'notInArray':
      return !values.includes(strategy.status);
    case 'isEmpty':
      return false;
    case 'isNotEmpty':
      return true;
    default:
      return false;
  }
}

const MOCK_STRATEGIES: readonly StrategyItem[] = [
  {
    id: 's-01',
    code: 'STR-CW-APPROACH-01',
    name: '冷却水出水温度湿球逼近控制与风机协同',
    scope: 'cooling_towers',
    scopeLabel: '冷却塔群系统',
    type: 'approach_control',
    status: 'active',
    version: 'v2.4.1',
    priority: 3,
    executionInterval: '5 分钟',
    triggerMode: '定时周期 + 室外湿球温度突变 (>0.5°C)',
    lastRunAt: '2026-09-18 15:55:00',
    lastResult: 'success',
    description: '根据现场微气象站实时湿球温度，动态寻优冷却塔风机总频率与出水温设定值，使逼近度稳定在 3.0°C ~ 3.5°C，实现冷机与塔综合电耗最低。',
    inputs: ['OA_WetBulb_Temp (室外湿球)', 'CW_T_Supply (冷却供水温)', 'CW_T_Return (冷却回水温)', 'Chiller_Total_kW (冷机群总功率)'],
    outputs: ['CW_T_Set (冷却水出水设定值: 28.5~32.0°C)', 'CT_Fans_Freq_Cmd (冷却塔风机总频率: 25~50Hz)'],
    guardrails: [
      { param: '出水温度下限', limit: '≥ 19.0°C', description: '防止主机冷凝器水温过低导致润滑油分离不良与低压差保护' },
      { param: '风机频率变化率', limit: '≤ 2.0 Hz/min', description: '防止风机皮带与齿轮箱机械疲劳冲击' },
      { param: '主机冷凝压力保护', limit: 'P_cond < 1.45 MPa', description: '高压逼近阈值时强行锁定风机全速 50Hz' },
    ],
    failSafeAction: '若气象站通讯丢失超过 15 分钟，自动回退到定出水温设定值 32.0°C，塔风机维持工频安全运行。',
  },
  {
    id: 's-02',
    code: 'STR-CHW-RESET-02',
    name: '冷冻水供水温度自适应动态温升重置 (LDC/Valve)',
    scope: 'chw_distribution',
    scopeLabel: '冷冻水输配',
    type: 'setpoint_reset',
    status: 'active',
    version: 'v3.1.0',
    priority: 4,
    executionInterval: '15 分钟',
    triggerMode: '周期循环 + 末端阀门开度极值反馈',
    lastRunAt: '2026-09-18 15:45:00',
    lastResult: 'success',
    description: '轮询全楼 48 台 AHU 水阀物理开度，在最大阀位未达到 85% 饱和时，平缓将冷冻水出水温设定值自 7.0°C 重置至 8.0°C，提升主机蒸发温度与 COP。',
    inputs: ['Max_AHU_Valve_Pos (末端最大水阀开度)', 'Zone_Avg_Temp (室内均温)', 'Zone_Max_Temp (室内最高温)', 'CHW_T_Return (总回水温度)'],
    outputs: ['CHW_T_Set (冷冻供水设定值: 7.0~9.0°C)'],
    guardrails: [
      { param: '供水设定值上限', limit: '≤ 9.0°C', description: '防止末端高湿度工况下露点除湿能力不足' },
      { param: '温升调节步距', limit: '≤ 0.2°C / 15min', description: '平滑爬坡，避免制冷主机蒸发器水温剧烈振荡' },
      { param: '最不利端开度阈值', limit: 'Max Valve ≥ 90%', description: '若任意末端阀开度超 90%，立即触发急退机制 (-0.5°C/5min)' },
    ],
    failSafeAction: '若末端数据采集失联超过 3 个节点，立即退守回基线设定值 7.0°C。',
  },
  {
    id: 's-03',
    code: 'STR-CHLR-STAGE-03',
    name: '冷水主机基于效率曲线的最优加减机寻优',
    scope: 'chiller_plant',
    scopeLabel: '冷水机房',
    type: 'staging_optimization',
    status: 'active',
    version: 'v1.8.2',
    priority: 1,
    executionInterval: '10 分钟',
    triggerMode: '负荷死区穿越 (>20min 持续) 或单机过载告警',
    lastRunAt: '2026-09-18 15:50:00',
    lastResult: 'success',
    description: '结合 1#、2# 离心机与 3# 磁悬浮冷机各自当前的实测 COP-PartLoad 曲线，计算当前总需冷量下的最优组合开机矩阵，杜绝大机低负荷低效运行。',
    inputs: ['Total_Cooling_kW (总实时冷负荷)', 'Chiller_1/2/3_COP_Realtime', 'CHW_Total_Flow (冷冻水流量)'],
    outputs: ['Chiller_Stage_Matrix (加减机目标组合: [1#, 3#])', 'Load_Allocation_Pct (单机负荷分配比)'],
    guardrails: [
      { param: '最小启停死区时间', limit: '≥ 30 分钟', description: '防止频繁启停损伤大型高压离心电机' },
      { param: '单机负荷率红线', limit: '离心机 ≥ 30% 且 ≤ 95%', description: '避免离心主机低于 25% 负荷引发喘振，避免超载跳机' },
    ],
    failSafeAction: '保持当前机组运行台数不变，禁止自动发送启停脉冲指令，转为人工调度介入提示。',
  },
  {
    id: 's-04',
    code: 'STR-DR-PEAKSHAVE-04',
    name: '电价尖峰期负荷柔性转移与需量限额削峰',
    scope: 'energy_storage',
    scopeLabel: '电能与柔性储能',
    type: 'demand_response',
    status: 'shadow',
    version: 'v1.0.0-rc2',
    priority: 2,
    executionInterval: '实时 (1 分钟)',
    triggerMode: '变压器需量越限预警 (>950 kW) 或尖峰电价时段 (14:00~17:00)',
    lastRunAt: '2026-09-18 15:54:00',
    lastResult: 'success',
    description: '尖峰电价时段自动联动 BESS 储能电池放电 180 kW，并将公共区域空调设定值微调 +0.5°C，确保变压器最大需量不超过签约容量 1000 kW。',
    inputs: ['Transformer_Active_kW (主变有功功率)', 'TOU_Tariff_Tier (当前电价阶梯)', 'BESS_SoC (储能电量百分比)'],
    outputs: ['BESS_Discharge_Cmd (储能放电功率)', 'Precool_Trigger (提前预冷触发标志)'],
    guardrails: [
      { param: '储能放电深度下限', limit: 'SoC ≥ 15%', description: '保护磷酸铁锂电池组电芯循环寿命' },
      { param: '变压器越限硬红线', limit: 'P_total ≤ 980 kW', description: '绝对禁止触碰基本电费罚款门槛' },
    ],
    failSafeAction: '若通讯延迟超标，BESS 执行本地自主恒功率放电保底逻辑。',
  },
  {
    id: 's-05',
    code: 'STR-PUMP-DP-05',
    name: '冷冻水二次泵最不利末端压差自适应重置',
    scope: 'chw_distribution',
    scopeLabel: '冷冻水输配',
    type: 'setpoint_reset',
    status: 'pending_approval',
    version: 'v2.0.0-alpha',
    priority: 5,
    executionInterval: '10 分钟',
    triggerMode: '周期监测',
    lastRunAt: '未投运',
    lastResult: 'skipped',
    description: '根据最不利端压差变送器实测值，自动重置二次泵分集水器目标压差（160~220 kPa），削减水泵节流阻抗能耗。',
    inputs: ['Critical_Zone_DP_Sensors (末端压差集合)', 'Pump_Total_kW'],
    outputs: ['Header_Target_DP (母管目标压差)'],
    guardrails: [
      { param: '最小压差下限', limit: '≥ 140 kPa', description: '确保顶层末端 AHU 具备额定流通压头' },
    ],
    failSafeAction: '回退至固定压差设定值 190 kPa。',
  },
];

export function StrategiesConsole({ siteId: _siteId }: { readonly siteId?: string }) {
  const [searchQuery, setSearchQuery] = useState<string>('');
  const [selectedStrategy, setSelectedStrategy] = useState<StrategyItem | null>(null);
  const [modeDialogOpen, setModeDialogOpen] = useState<boolean>(false);
  const [pendingMode, setPendingMode] = useState<StrategyStatus>('active');
  const [advancedFilters] = useQueryState(
    STRATEGY_FILTERS_QUERY_KEY,
    getFiltersStateParser<StrategyItem>([...STRATEGY_FILTER_COLUMN_IDS]).withDefault([]),
  );
  const [joinOperator] = useQueryState(
    STRATEGY_JOIN_OPERATOR_QUERY_KEY,
    parseAsStringEnum<JoinOperator>(['and', 'or']).withDefault('and'),
  );

  const filteredStrategies = useMemo(() => {
    return MOCK_STRATEGIES.filter((item) => {
      const matchQuery =
        searchQuery.trim() === '' ||
        item.name.toLowerCase().includes(searchQuery.toLowerCase()) ||
        item.code.toLowerCase().includes(searchQuery.toLowerCase()) ||
        item.scopeLabel.toLowerCase().includes(searchQuery.toLowerCase());
      const filterMatches = advancedFilters.map((filter) => matchesStrategyAdvancedFilter(item, filter));
      const matchAdvancedFilters = advancedFilters.length === 0 || (joinOperator === 'or'
        ? filterMatches.some(Boolean)
        : filterMatches.every(Boolean));
      return matchQuery && matchAdvancedFilters;
    });
  }, [advancedFilters, joinOperator, searchQuery]);

  const stats = useMemo(() => {
    const total = MOCK_STRATEGIES.length;
    const active = MOCK_STRATEGIES.filter((s) => s.status === 'active').length;
    const shadow = MOCK_STRATEGIES.filter((s) => s.status === 'shadow').length;
    const pending = MOCK_STRATEGIES.filter((s) => s.status === 'pending_approval' || s.status === 'suspended').length;
    return { total, active, shadow, pending };
  }, []);

  const getStatusBadge = (status: StrategyStatus) => {
    switch (status) {
      case 'active':
        return <StatusBadge tone="success" pulse={true} label="运行中" />;
      case 'shadow':
        return <StatusBadge tone="info" label="仿真校验" />;
      case 'pending_approval':
        return <StatusBadge tone="warning" label="待审批" />;
      case 'suspended':
        return <StatusBadge tone="neutral" label="已停用" />;
    }
  };

  const columns = useMemo<Array<ColumnDef<DataTableFeatures, StrategyItem>>>(() => [
    {
      id: 'select',
      header: ({ table }) => (
        <Checkbox
          checked={table.getIsAllPageRowsSelected() ? true : table.getIsSomePageRowsSelected() ? 'indeterminate' : false}
          onCheckedChange={(checked) => table.toggleAllPageRowsSelected(Boolean(checked))}
          aria-label="选择全部策略"
        />
      ),
      cell: ({ row }) => (
        <Checkbox
          checked={row.getIsSelected()}
          onCheckedChange={(checked) => row.toggleSelected(Boolean(checked))}
          aria-label={`选择 ${row.original.code}`}
          onClick={(event) => event.stopPropagation()}
        />
      ),
      enableSorting: false,
      enableHiding: false,
    },
    { id: 'code', accessorFn: (row) => row.code, meta: { label: '策略编号' }, header: '策略编号', cell: ({ row }) => <span className="font-mono text-xs font-medium text-foreground">{row.original.code}</span> },
    {
      id: 'name',
      accessorFn: (row) => row.name,
      meta: { label: '策略名称' },
      header: '策略名称与描述',
      cell: ({ row }) => <div className="flex flex-col"><span className="font-medium text-foreground">{row.original.name}</span><span className="line-clamp-1 text-xs text-muted-foreground">{row.original.description}</span></div>,
    },
    { id: 'scope', accessorFn: (row) => row.scopeLabel, meta: { label: '作用域' }, header: '作用域', cell: ({ row }) => <Badge variant="secondary" className="text-xs font-normal">{row.original.scopeLabel}</Badge> },
    { id: 'priority', accessorFn: (row) => row.priority, meta: { label: '优先级' }, header: '优先级', cell: ({ row }) => <span className="text-xs font-semibold text-foreground tabular-nums">P{row.original.priority}</span> },
    {
      id: 'trigger',
      header: '执行周期与触发',
      cell: ({ row }) => <div className="flex flex-col"><span className="text-xs text-foreground">{row.original.executionInterval}</span><span className="truncate text-[11px] text-muted-foreground">{row.original.triggerMode}</span></div>,
    },
    { id: 'version', header: '版本', cell: ({ row }) => <span className="font-mono text-xs text-muted-foreground">{row.original.version}</span> },
    {
      id: 'status',
      accessorFn: (row) => row.status,
      enableColumnFilter: true,
      meta: { label: '状态', variant: 'select', options: [...STRATEGY_STATUS_OPTIONS] },
      header: '状态',
      cell: ({ row }) => getStatusBadge(row.original.status),
    },
    { id: 'lastRun', accessorFn: (row) => row.lastRunAt, meta: { label: '上次运行' }, header: '上次运行', cell: ({ row }) => <span className="text-xs text-muted-foreground tabular-nums">{row.original.lastRunAt.split(' ')[1] || row.original.lastRunAt}</span> },
    {
      id: 'actions',
      header: '操作',
      cell: ({ row }) => <Button variant="ghost" size="sm" className="h-8 px-2 text-xs" onClick={(event) => { event.stopPropagation(); setSelectedStrategy(row.original); }}>查看详情</Button>,
      enableSorting: false,
      enableHiding: false,
    },
  ], []);

  const table = useDataTable({
    key: 'surface-12-strategies',
    data: [...filteredStrategies],
    columns,
    pageSize: 10,
    getRowId: (row) => row.id,
    meta: {
      queryKeys: {
        page: 'strategyPage',
        perPage: 'strategyPerPage',
        sort: 'strategySort',
        filters: STRATEGY_FILTERS_QUERY_KEY,
        joinOperator: STRATEGY_JOIN_OPERATOR_QUERY_KEY,
      },
    },
  });


  return (
    <Main className="space-y-6">
      {/* Header */}
      <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <div className="flex items-center gap-2">
            
          </div>
        </div>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" className="h-9 gap-1.5" onClick={() => window.location.reload()}>
            <RotateCcw className="h-4 w-4" />
            刷新状态
          </Button>
          <Button size="sm" className="h-9 gap-1.5">
            <Sparkles className="h-4 w-4" />
            新建策略
          </Button>
        </div>
      </div>

      {/* 4-Card Fact Strip */}
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">运行中策略</CardTitle>
            <Play className="h-4 w-4 text-emerald-600" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold tracking-tight text-emerald-600 tabular-nums">
              {stats.active} <span className="text-xs font-normal text-muted-foreground">项</span>
            </div>
            <p className="mt-1 text-xs text-muted-foreground">
              冷却水温重置与加减机策略正常执行
            </p>
          </CardContent>
        </Card>

        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">仿真评估中</CardTitle>
            <Layers className="h-4 w-4 text-blue-600" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold tracking-tight text-blue-600 tabular-nums">
              {stats.shadow} <span className="text-xs font-normal text-muted-foreground">项</span>
            </div>
            <p className="mt-1 text-xs text-muted-foreground">
              尖峰负荷调节与蓄冷策略正在模拟计算
            </p>
          </CardContent>
        </Card>

        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">待审批 / 挂起</CardTitle>
            <Clock className="h-4 w-4 text-amber-600" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold tracking-tight text-amber-600 tabular-nums">
              {stats.pending} <span className="text-xs font-normal text-muted-foreground">项</span>
            </div>
            <p className="mt-1 text-xs text-muted-foreground">
              二次泵定末端压差重置策略待签发
            </p>
          </CardContent>
        </Card>

        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">防护联锁覆盖</CardTitle>
            <Shield className="h-4 w-4 text-emerald-600" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold tracking-tight text-foreground tabular-nums">
              100%
            </div>
            <p className="mt-1 text-xs text-muted-foreground">
              防冻/防喘振/流量低限硬保护生效
            </p>
          </CardContent>
        </Card>
      </div>

      {/* Main Strategy Ledger Table */}
      <DataTableBlock>

        <DataTable
          table={table}
          tableAriaLabel="控制策略"
          empty="未检索到符合条件的策略记录"
          getHeaderCellProps={(header) => ({
            className:
              header.id === 'select' ? 'w-[40px] pl-4' :
              header.id === 'code' ? 'w-[130px]' :
              header.id === 'name' ? 'min-w-[260px]' :
              header.id === 'actions' ? 'pr-4 text-right' :
              undefined,
          })}
          getRowProps={(row) => ({
            className: 'cursor-pointer',
            onClick: () => setSelectedStrategy(row.original),
          })}
          getCellProps={(cell) => ({
            className:
              cell.column.id === 'select' ? 'pl-4' :
              cell.column.id === 'actions' ? 'pr-4 text-right' :
              undefined,
          })}
          footer={(
            <DataTablePagination
              table={table}
              totalRows={filteredStrategies.length}
            />
          )}
        >
          <DataTableAdvancedToolbar table={table}>
            <div className="relative w-full sm:w-64">
              <Search className="absolute left-2.5 top-2.5 size-4 text-muted-foreground" />
              <Input
                placeholder="搜索策略名称、编号或作用域..."
                className="h-9 pl-8 text-sm"
                value={searchQuery}
                onChange={(e) => {
                  setSearchQuery(e.target.value);
                  table.setPageIndex(0);
                }}
              />
            </div>
            <DataTableSortList table={table} />
            <DataTableFilterList table={table} />
          </DataTableAdvancedToolbar>
        </DataTable>
      </DataTableBlock>

      {/* Strategy Detail Sheet */}
      <Sheet open={Boolean(selectedStrategy)} onOpenChange={(open) => !open && setSelectedStrategy(null)}>
        <SheetContent className="sm:max-w-xl! w-full overflow-y-auto">
          {selectedStrategy && (
            <div className="space-y-6 py-2">
              <SheetHeader className="space-y-2 border-b pb-4 text-left">
                <div className="flex items-center gap-2">
                  <span className="font-mono text-xs text-muted-foreground">{selectedStrategy.code}</span>
                  {getStatusBadge(selectedStrategy.status)}
                  <span className="text-xs text-muted-foreground">优先级 P{selectedStrategy.priority}</span>
                </div>
                <SheetTitle className="text-lg font-bold leading-tight text-foreground">
                  {selectedStrategy.name}
                </SheetTitle>
                <SheetDescription className="text-xs text-muted-foreground">
                  作用域：{selectedStrategy.scopeLabel} | 固件/算法版本：{selectedStrategy.version}
                </SheetDescription>
              </SheetHeader>

              {/* Description */}
              <div className="rounded-lg border bg-muted/20 p-4 text-xs space-y-1">
                <div className="font-semibold text-foreground">策略控制逻辑概述</div>
                <div className="text-muted-foreground leading-relaxed">{selectedStrategy.description}</div>
              </div>

              {/* Guardrails */}
              <div className="space-y-3">
                <div className="flex items-center gap-2">
                  <Shield className="h-4 w-4 text-primary" />
                  <h4 className="text-sm font-semibold text-foreground">安全限制与保护阈值</h4>
                </div>
                <div className="space-y-2">
                  {selectedStrategy.guardrails.map((g, idx) => (
                    <div key={idx} className="rounded-md border p-3 text-xs space-y-1">
                      <div className="flex items-center justify-between font-medium">
                        <span className="text-foreground">{g.param}</span>
                        <Badge variant="outline" className="tabular-nums text-[11px] border-primary/30 text-primary">
                          {g.limit}
                        </Badge>
                      </div>
                      <div className="text-muted-foreground">{g.description}</div>
                    </div>
                  ))}
                </div>
              </div>

              {/* I/O Point Mapping */}
              <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                <div className="rounded-md border p-3 text-xs space-y-2">
                  <div className="font-semibold text-foreground">输入感知点位 (Inputs)</div>
                  <ul className="space-y-1">
                    {selectedStrategy.inputs.map((inp, idx) => (
                      <li key={idx} className="text-xs text-muted-foreground flex items-center gap-1.5">
                        <span className="h-1 w-1 rounded-full bg-blue-500 shrink-0" />
                        <span>{inp}</span>
                      </li>
                    ))}
                  </ul>
                </div>

                <div className="rounded-md border p-3 text-xs space-y-2">
                  <div className="font-semibold text-foreground">控制指令下发点位 (Outputs)</div>
                  <ul className="space-y-1">
                    {selectedStrategy.outputs.map((out, idx) => (
                      <li key={idx} className="text-xs text-muted-foreground flex items-center gap-1.5">
                        <span className="h-1 w-1 rounded-full bg-emerald-500 shrink-0" />
                        <span>{out}</span>
                      </li>
                    ))}
                  </ul>
                </div>
              </div>

              {/* Fail-safe Fallback */}
              <div className="rounded-md border border-amber-500/30 bg-amber-500/5 p-3 text-xs space-y-1">
                <div className="flex items-center gap-1.5 font-semibold text-amber-700 dark:text-amber-400">
                  <AlertTriangle className="h-4 w-4" />
                  通信中断保护动作
                </div>
                <div className="text-muted-foreground leading-relaxed">{selectedStrategy.failSafeAction}</div>
              </div>

              {/* Actions */}
              <div className="border-t pt-4 flex items-center justify-between gap-2">
                {selectedStrategy.status === 'active' ? (
                  <Button
                    variant="outline"
                    size="sm"
                    className="gap-1.5 text-xs text-amber-600 hover:text-amber-700"
                    onClick={() => {
                      setPendingMode('suspended');
                      setModeDialogOpen(true);
                    }}
                  >
                    <Pause className="h-3.5 w-3.5" />
                    暂停策略
                  </Button>
                ) : (
                  <Button
                    variant="outline"
                    size="sm"
                    className="gap-1.5 text-xs text-emerald-600 hover:text-emerald-700"
                    onClick={() => {
                      setPendingMode('active');
                      setModeDialogOpen(true);
                    }}
                  >
                    <Play className="h-3.5 w-3.5" />
                    启用策略
                  </Button>
                )}

                <div className="flex items-center gap-2">
                  <Button
                    variant="outline"
                    size="sm"
                    className="gap-1.5 text-xs text-blue-600 hover:text-blue-700"
                    onClick={() => {
                      setPendingMode('shadow');
                      setModeDialogOpen(true);
                    }}
                  >
                    <Layers className="h-3.5 w-3.5" />
                    切换为仿真模式
                  </Button>
                </div>
              </div>
            </div>
          )}
        </SheetContent>
      </Sheet>

      {/* Mode Change Dialog */}
      <Dialog open={modeDialogOpen} onOpenChange={setModeDialogOpen}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>调整策略运行模式</DialogTitle>
            <DialogDescription>
              确定将策略 <span className="font-mono font-semibold text-foreground">{selectedStrategy?.code}</span> 调整为模式：
              <span className="font-semibold text-primary ml-1">
                {pendingMode === 'active' && '正式运行'}
                {pendingMode === 'shadow' && '仿真模式'}
                {pendingMode === 'suspended' && '暂停运行'}
              </span>
              ？
            </DialogDescription>
          </DialogHeader>
          <div className="text-xs text-muted-foreground py-2">
            变更将被记录至自控安全审计日志，同时通知值班自控工程师。
          </div>
          <DialogFooter>
            <Button variant="outline" size="sm" onClick={() => setModeDialogOpen(false)}>取消</Button>
            <Button
              size="sm"
              onClick={() => {
                setModeDialogOpen(false);
                setSelectedStrategy(null);
              }}
            >
              确认生效
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </Main>
  );
}
