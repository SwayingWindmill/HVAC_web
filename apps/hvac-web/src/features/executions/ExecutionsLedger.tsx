import { useMemo, useState } from 'react';
import type { ColumnDef } from '@tanstack/react-table';
import { parseAsStringEnum, useQueryState } from 'nuqs';
import {
  CheckCircle2,
  AlertOctagon,
  RotateCcw,
  Search,
  ArrowRight,
  Activity,
  Zap,
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

export type ExecutionStage = 'sent' | 'gateway_delivered' | 'controller_ack' | 'readback_confirmed';
export type ExecutionResult = 'confirmed' | 'precondition_blocked' | 'readback_deviation' | 'timeout_retry' | 'settling';

const EXECUTION_RESULT_OPTIONS = [
  { label: '回读确认达标', value: 'confirmed' },
  { label: '前置安全拦截', value: 'precondition_blocked' },
  { label: '回读偏差超标', value: 'readback_deviation' },
  { label: '通讯重发成功', value: 'timeout_retry' },
  { label: '物理稳态中', value: 'settling' },
] as const;
const EXECUTION_FILTER_COLUMN_IDS = ['result'] as const;
const EXECUTION_FILTERS_QUERY_KEY = 'executionFilters';
const EXECUTION_JOIN_OPERATOR_QUERY_KEY = 'executionJoinOperator';

export interface CommandExecutionFact {
  readonly id: string;
  readonly sequenceId: string;
  readonly requestedAt: string;
  readonly requester: string;
  readonly requesterType: 'strategy' | 'operator' | 'optimization_plan' | 'emergency';
  readonly targetDevice: string;
  readonly targetPoint: string;
  readonly previousValue: string;
  readonly intendedValue: string;
  readonly authorization: string;
  readonly gatewayStage: ExecutionStage;
  readonly gatewayLatencyMs: number;
  readonly controllerAckMs: number;
  readonly readbackValue: string;
  readonly readbackDeviation: string;
  readonly settleTimeSeconds: number;
  readonly result: ExecutionResult;
  readonly failureReason?: string;
  readonly rollbackOccurred: boolean;
  readonly protocolTrace: readonly {
    readonly timestamp: string;
    readonly event: string;
    readonly detail: string;
    readonly status: 'ok' | 'blocked' | 'warn';
  }[];
}

function matchesExecutionAdvancedFilter(
  execution: CommandExecutionFact,
  filter: ExtendedColumnFilter<CommandExecutionFact>,
) {
  if (filter.id !== 'result') return true;
  const values = Array.isArray(filter.value) ? filter.value : [filter.value];

  switch (filter.operator) {
    case 'eq':
    case 'inArray':
      return values.includes(execution.result);
    case 'ne':
    case 'notInArray':
      return !values.includes(execution.result);
    case 'isEmpty':
      return false;
    case 'isNotEmpty':
      return true;
    default:
      return false;
  }
}

const MOCK_EXECUTIONS: readonly CommandExecutionFact[] = [
  {
    id: 'exec-01',
    sequenceId: 'CMD-20260918-0068',
    requestedAt: '15:55:02',
    requester: 'STR-CW-APPROACH-01',
    requesterType: 'strategy',
    targetDevice: '冷却塔风机群 (CT-01~04)',
    targetPoint: 'CT_Total_Freq_Setpoint',
    previousValue: '40.0 Hz',
    intendedValue: '42.5 Hz',
    authorization: 'RBAC: Role=Automated_Strategy (Scope: site:cooling)',
    gatewayStage: 'readback_confirmed',
    gatewayLatencyMs: 38,
    controllerAckMs: 64,
    readbackValue: '42.4 Hz',
    readbackDeviation: '-0.1 Hz (允许范围 ±0.5 Hz)',
    settleTimeSeconds: 45,
    result: 'confirmed',
    rollbackOccurred: false,
    protocolTrace: [
      { timestamp: '15:55:02.110', event: 'Intent Emitted', detail: '策略引擎生成逼近度闭环调频指令 42.5 Hz', status: 'ok' },
      { timestamp: '15:55:02.148', event: 'Gateway Delivered', detail: '边缘网关经 BACnet IP 协议帧下发至 DDC 主控站', status: 'ok' },
      { timestamp: '15:55:02.212', event: 'Controller ACK', detail: 'DDC 返回 WriteProperty ACK (InvokeID: 8421)', status: 'ok' },
      { timestamp: '15:55:47.350', event: 'Physical Readback', detail: '变频器实测转速与频率稳定在 42.4 Hz，出水温稳定', status: 'ok' },
    ],
  },
  {
    id: 'exec-02',
    sequenceId: 'CMD-20260918-0067',
    requestedAt: '15:45:00',
    requester: 'OPT-2026-09-001 (冷冻水温升重置)',
    requesterType: 'optimization_plan',
    targetDevice: '1# 离心冷水机组 (CH-01)',
    targetPoint: 'CHW_Leaving_T_Setpoint',
    previousValue: '7.4 °C',
    intendedValue: '7.6 °C',
    authorization: 'RBAC: Role=Chief_Engineer (Approval: APPR-9921)',
    gatewayStage: 'readback_confirmed',
    gatewayLatencyMs: 42,
    controllerAckMs: 95,
    readbackValue: '7.6 °C',
    readbackDeviation: '0.0 °C (完全吻合)',
    settleTimeSeconds: 180,
    result: 'confirmed',
    rollbackOccurred: false,
    protocolTrace: [
      { timestamp: '15:45:00.020', event: 'Precondition Check', detail: '检查末端 48 台 AHU 水阀开度均 < 85%，安全门限通过', status: 'ok' },
      { timestamp: '15:45:00.062', event: 'Gateway Delivered', detail: 'Modbus TCP 保持寄存器 0x4012 写入目标值 760', status: 'ok' },
      { timestamp: '15:45:00.157', event: 'Controller ACK', detail: '主机微电脑控制板应答 0x06 Function Write ACK', status: 'ok' },
      { timestamp: '15:48:00.220', event: 'Physical Readback', detail: '蒸发器实际出水温度平缓爬升至 7.6°C，冷机 COP 提升 2.1%', status: 'ok' },
    ],
  },
  {
    id: 'exec-03',
    sequenceId: 'CMD-20260918-0066',
    requestedAt: '15:30:15',
    requester: '值班工程师 (张工)',
    requesterType: 'operator',
    targetDevice: '冷冻水供回水母管电动旁通阀',
    targetPoint: 'CHW_Bypass_Valve_Pos',
    previousValue: '15.0 %',
    intendedValue: '35.0 %',
    authorization: 'RBAC: Operator_Manual_Control (Session Token: VALID)',
    gatewayStage: 'readback_confirmed',
    gatewayLatencyMs: 25,
    controllerAckMs: 50,
    readbackValue: '34.8 %',
    readbackDeviation: '-0.2 % (合格)',
    settleTimeSeconds: 30,
    result: 'confirmed',
    rollbackOccurred: false,
    protocolTrace: [
      { timestamp: '15:30:15.010', event: 'Operator Intent', detail: '张工手动微调分集水器旁通电动阀开度至 35%', status: 'ok' },
      { timestamp: '15:30:15.035', event: 'Gateway Sent', detail: 'BACnet Analog-Output-12 写入开度 35.0%', status: 'ok' },
      { timestamp: '15:30:15.085', event: 'Controller ACK', detail: 'DDC 执行器控制器返回写入成功确认', status: 'ok' },
      { timestamp: '15:30:45.120', event: 'Physical Readback', detail: '执行器电位器模拟量反馈稳定在 34.8%', status: 'ok' },
    ],
  },
  {
    id: 'exec-04',
    sequenceId: 'CMD-20260918-0065',
    requestedAt: '15:15:00',
    requester: 'STR-CHW-RESET-02',
    requesterType: 'strategy',
    targetDevice: '1# 离心冷水机组 (CH-01)',
    targetPoint: 'CHW_Leaving_T_Setpoint',
    previousValue: '7.8 °C',
    intendedValue: '8.2 °C',
    authorization: 'RBAC: Automated_Strategy',
    gatewayStage: 'sent',
    gatewayLatencyMs: 0,
    controllerAckMs: 0,
    readbackValue: '保持 7.8 °C',
    readbackDeviation: '未下发',
    settleTimeSeconds: 0,
    result: 'precondition_blocked',
    failureReason: '前置硬安全约束阻断：4# 楼 8 层 AHU-08-01 表冷水阀开度达 91.2% (超过警戒阈值 90.0%)，拒绝温升重置。',
    rollbackOccurred: false,
    protocolTrace: [
      { timestamp: '15:15:00.010', event: 'Intent Generated', detail: '尝试将出水温度由 7.8°C 提升至 8.2°C', status: 'ok' },
      { timestamp: '15:15:00.018', event: 'Guardrail Intercepted', detail: '安全守卫检测到 AHU-08-01 负荷饱和，强行阻断下发', status: 'blocked' },
      { timestamp: '15:15:00.020', event: 'Audit Stored', detail: '记录安全阻断事件，系统维持当前 7.8°C 工况', status: 'warn' },
    ],
  },
  {
    id: 'exec-05',
    sequenceId: 'CMD-20260918-0064',
    requestedAt: '14:50:20',
    requester: 'STR-CW-APPROACH-01',
    requesterType: 'strategy',
    targetDevice: '3# 冷却水循环泵 (CWP-03)',
    targetPoint: 'CWP_Speed_Freq_Cmd',
    previousValue: '38.0 Hz',
    intendedValue: '42.0 Hz',
    authorization: 'RBAC: Automated_Strategy',
    gatewayStage: 'readback_confirmed',
    gatewayLatencyMs: 420,
    controllerAckMs: 310,
    readbackValue: '41.9 Hz',
    readbackDeviation: '-0.1 Hz',
    settleTimeSeconds: 65,
    result: 'timeout_retry',
    failureReason: '首包 BACnet 广播网络拥塞延时超 600ms 触发重试，重试包应答成功',
    rollbackOccurred: false,
    protocolTrace: [
      { timestamp: '14:50:20.100', event: 'First Attempt Sent', detail: '下发调速 42.0 Hz 指令', status: 'ok' },
      { timestamp: '14:50:20.700', event: 'Timeout Warn', detail: '超时未收到 ACK，网关按策略触发自动重发 (Attempt #2)', status: 'warn' },
      { timestamp: '14:50:21.120', event: 'Controller ACK', detail: '重试应答成功，确认接收', status: 'ok' },
      { timestamp: '14:51:25.000', event: 'Physical Readback', detail: '变频器电流与频率回读稳定', status: 'ok' },
    ],
  },
  {
    id: 'exec-06',
    sequenceId: 'CMD-20260918-0063',
    requestedAt: '14:30:00',
    requester: 'STR-CHLR-STAGE-03',
    requesterType: 'strategy',
    targetDevice: '3# 磁悬浮冷水机组 (CH-03)',
    targetPoint: 'Chiller_Start_Command',
    previousValue: 'Standby (待机)',
    intendedValue: 'Start (启动指令)',
    authorization: 'RBAC: Supervisory_Interlock_Pass',
    gatewayStage: 'readback_confirmed',
    gatewayLatencyMs: 35,
    controllerAckMs: 120,
    readbackValue: 'Running (运行工况已稳定)',
    readbackDeviation: '符合',
    settleTimeSeconds: 420,
    result: 'confirmed',
    rollbackOccurred: false,
    protocolTrace: [
      { timestamp: '14:30:00.010', event: 'Interlock Verif', detail: '冷冻水流开关、冷却水流开关及对应阀位信号校验全部置位', status: 'ok' },
      { timestamp: '14:30:00.045', event: 'Gateway Sent', detail: '向磁悬浮机组专用控制器下发远程启动脉冲', status: 'ok' },
      { timestamp: '14:30:00.165', event: 'Controller ACK', detail: 'PLC 回传机组进入自检与预润滑序列', status: 'ok' },
      { timestamp: '14:37:00.000', event: 'Readback Confirmed', detail: '机组压缩机转速上升至 18,000 RPM，供回水温差建立', status: 'ok' },
    ],
  },
];

export function ExecutionsLedger({ siteId: _siteId }: { readonly siteId?: string }) {
  const [searchQuery, setSearchQuery] = useState<string>('');
  const [selectedFact, setSelectedFact] = useState<CommandExecutionFact | null>(null);
  const [advancedFilters] = useQueryState(
    EXECUTION_FILTERS_QUERY_KEY,
    getFiltersStateParser<CommandExecutionFact>([...EXECUTION_FILTER_COLUMN_IDS]).withDefault([]),
  );
  const [joinOperator] = useQueryState(
    EXECUTION_JOIN_OPERATOR_QUERY_KEY,
    parseAsStringEnum<JoinOperator>(['and', 'or']).withDefault('and'),
  );
  const filteredFacts = useMemo(() => {
    return MOCK_EXECUTIONS.filter((item) => {
      const matchQuery =
        searchQuery.trim() === '' ||
        item.sequenceId.toLowerCase().includes(searchQuery.toLowerCase()) ||
        item.targetDevice.toLowerCase().includes(searchQuery.toLowerCase()) ||
        item.targetPoint.toLowerCase().includes(searchQuery.toLowerCase()) ||
        item.requester.toLowerCase().includes(searchQuery.toLowerCase());
      const filterMatches = advancedFilters.map((filter) => matchesExecutionAdvancedFilter(item, filter));
      const matchAdvancedFilters = advancedFilters.length === 0 || (joinOperator === 'or'
        ? filterMatches.some(Boolean)
        : filterMatches.every(Boolean));
      return matchQuery && matchAdvancedFilters;
    });
  }, [advancedFilters, joinOperator, searchQuery]);

  const stats = useMemo(() => {
    const total = 68;
    const confirmed = 66;
    const blocked = 1;
    const retry = 1;
    const confirmationRate = ((confirmed / total) * 100).toFixed(1);
    return { total, confirmed, blocked, retry, confirmationRate };
  }, []);

  const renderResultBadge = (result: ExecutionResult) => {
    switch (result) {
      case 'confirmed':
        return <StatusBadge tone="success" pulse={true} label="回读确认达标" />;
      case 'precondition_blocked':
        return <StatusBadge tone="warning" label="前置安全拦截" />;
      case 'timeout_retry':
        return <StatusBadge tone="info" label="通讯重发成功" />;
      case 'readback_deviation':
        return <StatusBadge tone="destructive" label="回读偏差超标" />;
      case 'settling':
        return <StatusBadge tone="neutral" label="物理稳态中" />;
    }
  };

  const columns = useMemo<Array<ColumnDef<DataTableFeatures, CommandExecutionFact>>>(() => [
    {
      id: 'select',
      header: ({ table }) => (
        <Checkbox
          checked={table.getIsAllPageRowsSelected() ? true : table.getIsSomePageRowsSelected() ? 'indeterminate' : false}
          onCheckedChange={(checked) => table.toggleAllPageRowsSelected(Boolean(checked))}
          aria-label="选择全部执行记录"
        />
      ),
      cell: ({ row }) => (
        <Checkbox
          checked={row.getIsSelected()}
          onCheckedChange={(checked) => row.toggleSelected(Boolean(checked))}
          aria-label={`选择 ${row.original.sequenceId}`}
          onClick={(event) => event.stopPropagation()}
        />
      ),
      enableSorting: false,
      enableHiding: false,
    },
    { id: 'time', accessorFn: (row) => row.requestedAt, meta: { label: '请求时间' }, header: '请求时间', cell: ({ row }) => <span className="font-mono text-xs text-muted-foreground">{row.original.requestedAt}</span> },
    { id: 'seq', accessorFn: (row) => row.sequenceId, meta: { label: '指令流水号' }, header: '指令流水号', cell: ({ row }) => <span className="font-mono text-xs font-semibold text-foreground">{row.original.sequenceId}</span> },
    {
      id: 'requester',
      accessorFn: (row) => row.requester,
      meta: { label: '发起方' },
      header: '发起方',
      cell: ({ row }) => (
        <div className="flex flex-col">
          <span className="text-xs font-medium text-foreground">{row.original.requester}</span>
          <span className="text-[11px] text-muted-foreground">
            {row.original.requesterType === 'strategy' ? '自动化策略' : row.original.requesterType === 'operator' ? '人工调度' : row.original.requesterType === 'optimization_plan' ? '能效方案' : '应急保护'}
          </span>
        </div>
      ),
    },
    {
      id: 'target',
      accessorFn: (row) => row.targetDevice,
      meta: { label: '目标设备' },
      header: '目标设备与点位',
      cell: ({ row }) => <div className="flex flex-col"><span className="text-xs font-medium text-foreground">{row.original.targetDevice}</span><span className="font-mono text-[11px] text-muted-foreground">{row.original.targetPoint}</span></div>,
    },
    {
      id: 'setpoint',
      header: '设定值调整',
      cell: ({ row }) => <div className="flex items-center gap-1 text-xs tabular-nums"><span className="text-muted-foreground">{row.original.previousValue}</span><ArrowRight className="h-3 w-3 shrink-0 text-muted-foreground" /><span className="font-semibold text-foreground">{row.original.intendedValue}</span></div>,
    },
    {
      id: 'stage',
      accessorFn: (row) => row.gatewayStage,
      meta: { label: '网关阶段' },
      header: '网关阶段',
      cell: ({ row }) => <div className="flex items-center gap-1.5 text-xs text-foreground"><span className="size-1.5 rounded-full bg-emerald-500" />{row.original.gatewayStage === 'readback_confirmed' ? 'ACK+回读' : row.original.gatewayStage === 'controller_ack' ? 'DDC ACK' : row.original.gatewayStage === 'gateway_delivered' ? '网关已送达' : '已拦截/发送中'}</div>,
    },
    {
      id: 'readback',
      header: '实测回读',
      cell: ({ row }) => <div className="flex flex-col"><span className="text-xs font-medium text-foreground tabular-nums">{row.original.readbackValue}</span><span className="truncate text-[11px] text-muted-foreground">{row.original.readbackDeviation}</span></div>,
    },
    {
      id: 'result',
      accessorFn: (row) => row.result,
      enableColumnFilter: true,
      meta: { label: '最终结果', variant: 'select', options: [...EXECUTION_RESULT_OPTIONS] },
      header: '最终结果',
      cell: ({ row }) => renderResultBadge(row.original.result),
    },
    {
      id: 'actions',
      header: '操作',
      cell: ({ row }) => <Button variant="ghost" size="sm" className="h-8 px-2 text-xs" onClick={(event) => { event.stopPropagation(); setSelectedFact(row.original); }}>查看详情</Button>,
      enableSorting: false,
      enableHiding: false,
    },
  ], []);

  const table = useDataTable({
    key: 'surface-13-executions',
    data: [...filteredFacts],
    columns,
    pageSize: 10,
    getRowId: (row) => row.id,
    meta: {
      queryKeys: {
        page: 'executionPage',
        perPage: 'executionPerPage',
        sort: 'executionSort',
        filters: EXECUTION_FILTERS_QUERY_KEY,
        joinOperator: EXECUTION_JOIN_OPERATOR_QUERY_KEY,
      },
    },
  });


  return (
    <Main className="space-y-6">
      {/* Header */}
      <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <div className="flex items-center gap-2">
            
            <Badge variant="outline" className="text-xs font-normal">执行审计</Badge>
          </div>
        </div>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" className="h-9 gap-1.5" onClick={() => window.location.reload()}>
            <RotateCcw className="h-4 w-4" />
            刷新记录
          </Button>
        </div>
      </div>

      {/* 4-Card Fact Strip */}
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">今日下发指令总数</CardTitle>
            <Activity className="h-4 w-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold tracking-tight text-foreground tabular-nums">
              {stats.total} <span className="text-xs font-normal text-muted-foreground">条指令</span>
            </div>
            <p className="mt-1 text-xs text-muted-foreground">
              自动化 61 条 · 方案 5 条 · 人工 2 条
            </p>
          </CardContent>
        </Card>

        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">设备回读确认率</CardTitle>
            <CheckCircle2 className="h-4 w-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold tracking-tight text-foreground tabular-nums">
              {stats.confirmationRate}%
            </div>
            <p className="mt-1 text-xs text-muted-foreground">
              {stats.confirmed} 条指令回读校验一致
            </p>
          </CardContent>
        </Card>

        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">安全前置条件拦截</CardTitle>
            <AlertOctagon className="h-4 w-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold tracking-tight text-foreground tabular-nums">
              {stats.blocked} <span className="text-xs font-normal text-muted-foreground">次拦截</span>
            </div>
            <p className="mt-1 text-xs text-muted-foreground">
              末端阀位超 90% 触发自适应拦截
            </p>
          </CardContent>
        </Card>

        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">平均网关响应延迟</CardTitle>
            <Zap className="size-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold tracking-tight text-foreground tabular-nums">
              68 <span className="text-xs font-normal text-muted-foreground">ms</span>
            </div>
            <p className="mt-1 text-xs text-muted-foreground">
              现场总线平均 ACK 响应时延
            </p>
          </CardContent>
        </Card>
      </div>

      {/* Main Executions Facts Table */}
      <DataTableBlock
        title="执行记录"
      >

        <DataTable
          table={table}
          tableAriaLabel="执行记录"
          empty="未检索到符合条件的指令执行记录"
          getHeaderCellProps={(header) => ({
            className:
              header.id === 'select' ? 'w-[40px] pl-4' :
              header.id === 'time' ? 'w-[110px]' :
              header.id === 'seq' ? 'w-[140px]' :
              header.id === 'target' ? 'min-w-[180px]' :
              header.id === 'setpoint' ? 'min-w-[140px]' :
              header.id === 'actions' ? 'pr-4 text-right' :
              undefined,
          })}
          getRowProps={(row) => ({
            className: 'cursor-pointer',
            onClick: () => setSelectedFact(row.original),
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
              totalRows={filteredFacts.length}
            />
          )}
        >
          <DataTableAdvancedToolbar table={table}>
            <div className="relative w-full sm:w-64">
              <Search className="absolute left-2.5 top-2.5 size-4 text-muted-foreground" />
              <Input
                placeholder="搜索指令序号、设备、点位或发起方..."
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

      {/* Fact Audit Protocol Sheet */}
      <Sheet open={Boolean(selectedFact)} onOpenChange={(open) => !open && setSelectedFact(null)}>
        <SheetContent className="sm:max-w-xl! w-full overflow-y-auto">
          {selectedFact && (
            <div className="space-y-6 py-2">
              <SheetHeader className="space-y-2 border-b pb-4 text-left">
                <div className="flex items-center gap-2">
                  <span className="font-mono text-xs text-muted-foreground">{selectedFact.sequenceId}</span>
                  {renderResultBadge(selectedFact.result)}
                </div>
                <SheetTitle className="text-lg font-bold leading-tight text-foreground">
                  执行记录
                </SheetTitle>
                <SheetDescription className="text-xs text-muted-foreground">
                  请求时间：{selectedFact.requestedAt} | 授权签名：{selectedFact.authorization}
                </SheetDescription>
              </SheetHeader>

              {/* Parameter Modification Details */}
              <div className="rounded-lg border bg-muted/20 p-4 space-y-3">
                <div className="text-xs font-semibold text-foreground">目标点位与设定参数</div>
                <div className="grid grid-cols-2 gap-2 text-xs">
                  <div>
                    <span className="text-muted-foreground">目标设备：</span>
                    <div className="font-medium text-foreground">{selectedFact.targetDevice}</div>
                  </div>
                  <div>
                    <span className="text-muted-foreground">受控点位：</span>
                    <div className="font-mono font-medium text-foreground">{selectedFact.targetPoint}</div>
                  </div>
                  <div>
                    <span className="text-muted-foreground">原设定值：</span>
                    <div className="tabular-nums text-muted-foreground">{selectedFact.previousValue}</div>
                  </div>
                  <div>
                    <span className="text-muted-foreground">新设定值：</span>
                    <div className="tabular-nums font-bold text-foreground">{selectedFact.intendedValue}</div>
                  </div>
                </div>

                {selectedFact.failureReason && (
                  <div className="border-t pt-2 text-xs text-amber-700 dark:text-amber-400">
                    <span className="font-semibold">拦截/异常原因：</span> {selectedFact.failureReason}
                  </div>
                )}
              </div>

              {/* Protocol Packet Timeline Trace */}
              <div className="space-y-3">
                <h4 className="text-sm font-semibold text-foreground">通信交互记录</h4>
                <div className="space-y-2">
                  {selectedFact.protocolTrace.map((event, idx) => (
                    <div key={idx} className="rounded-md border p-3 text-xs space-y-1">
                      <div className="flex items-center justify-between">
                        <span className="font-semibold text-foreground">{event.event}</span>
                        <span className="font-mono text-[11px] text-muted-foreground">{event.timestamp}</span>
                      </div>
                      <div className="text-muted-foreground">{event.detail}</div>
                    </div>
                  ))}
                </div>
              </div>

              {/* Physical Readback Verification */}
              <div className="rounded-lg border p-4 text-xs space-y-2">
                <div className="font-semibold text-foreground">现场回读核验</div>
                <div className="grid grid-cols-2 gap-2">
                  <div>
                    <span className="text-muted-foreground">实测回读值：</span>
                    <div className="tabular-nums font-semibold text-foreground">{selectedFact.readbackValue}</div>
                  </div>
                  <div>
                    <span className="text-muted-foreground">实测稳态偏差：</span>
                    <div className="tabular-nums text-foreground">{selectedFact.readbackDeviation}</div>
                  </div>
                  <div>
                    <span className="text-muted-foreground">网关往返延迟：</span>
                    <div className="tabular-nums text-foreground">{selectedFact.gatewayLatencyMs} ms</div>
                  </div>
                  <div>
                    <span className="text-muted-foreground">稳态达成耗时：</span>
                    <div className="tabular-nums text-foreground">{selectedFact.settleTimeSeconds} 秒</div>
                  </div>
                </div>
              </div>

              {/* Actions */}
              <div className="border-t pt-4 flex items-center justify-end gap-2">
                <Button variant="outline" size="sm" className="text-xs" onClick={() => setSelectedFact(null)}>
                  关闭
                </Button>
              </div>
            </div>
          )}
        </SheetContent>
      </Sheet>
    </Main>
  );
}
