import { useMemo, useState } from 'react';
import type { ColumnDef } from '@tanstack/react-table';
import { parseAsStringEnum, useQueryState } from 'nuqs';
import {
  CheckCircle2,
  AlertTriangle,
  Clock,
  RotateCcw,
  Search,
  FileCheck2,
  ShieldCheck,
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
import { Progress } from '@/components/ui/progress';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';

export type VerificationStatus = 'pending' | 'testing' | 'passed' | 'failed' | 'inconclusive';
export type VerificationType = 'strategy_rollout' | 'maintenance_acceptance' | 'periodic_mbcx' | 'setpoint_reset';

const VERIFICATION_STATUS_OPTIONS = [
  { label: '待执行', value: 'pending' },
  { label: '验证中', value: 'testing' },
  { label: '验证通过', value: 'passed' },
  { label: '未达标', value: 'failed' },
  { label: '存疑待复测', value: 'inconclusive' },
] as const;
const VERIFICATION_TYPE_OPTIONS = [
  { label: '策略发布验证', value: 'strategy_rollout' },
  { label: '维保工单验收', value: 'maintenance_acceptance' },
  { label: '持续调试巡检', value: 'periodic_mbcx' },
  { label: '参数重置验证', value: 'setpoint_reset' },
] as const;
const VERIFICATION_FILTER_COLUMN_IDS = ['status', 'type'] as const;
const VERIFICATION_FILTERS_QUERY_KEY = 'verificationFilters';
const VERIFICATION_JOIN_OPERATOR_QUERY_KEY = 'verificationJoinOperator';

export interface VerificationItem {
  readonly id: string;
  readonly code: string;
  readonly title: string;
  readonly system: string;
  readonly type: VerificationType;
  readonly status: VerificationStatus;
  readonly sourceRef: string;
  readonly testConditions: string;
  readonly passCriteria: string;
  readonly observedResult: string;
  readonly confidenceScore: number;
  readonly engineer: string;
  readonly lastTestedAt: string;
  readonly sequenceSteps: readonly {
    readonly step: number;
    readonly name: string;
    readonly expected: string;
    readonly actual: string;
    readonly pass: boolean;
  }[];
  readonly telemetryEvidence: readonly {
    readonly point: string;
    readonly target: string;
    readonly measured: string;
    readonly unit: string;
    readonly withinRange: boolean;
  }[];
}

function matchesVerificationAdvancedFilter(
  item: VerificationItem,
  filter: ExtendedColumnFilter<VerificationItem>,
) {
  const actual = filter.id === 'status' ? item.status : filter.id === 'type' ? item.type : '';
  const values = Array.isArray(filter.value) ? filter.value : [filter.value];

  switch (filter.operator) {
    case 'eq':
    case 'inArray':
      return values.includes(actual);
    case 'ne':
    case 'notInArray':
      return !values.includes(actual);
    case 'isEmpty':
      return actual.length === 0;
    case 'isNotEmpty':
      return actual.length > 0;
    default:
      return false;
  }
}

const MOCK_VERIFICATIONS: readonly VerificationItem[] = [
  {
    id: 'v-01',
    code: 'VRF-2026-018',
    title: '冷却塔出水温度湿球逼近控制效果验证',
    system: '冷却水系统 (CW)',
    type: 'strategy_rollout',
    status: 'passed',
    sourceRef: '策略: STR-CW-APPROACH-V2',
    testConditions: '室外湿球温度 28.4°C，冷水系统总负荷率 > 65%',
    passCriteria: '逼近度维持在 3.0°C ~ 3.5°C 区间，风机无频繁启停 (>15min 抖动周期)',
    observedResult: '实测逼近度 3.2°C，风机转速平滑响应，系统 COP 提升 4.2%',
    confidenceScore: 98,
    engineer: '张工 (系统调试组)',
    lastTestedAt: '2026-09-18 14:30',
    sequenceSteps: [
      { step: 1, name: '湿球温度采样与滑动均值滤波', expected: '波动 < 0.2°C/5min', actual: '0.12°C/5min', pass: true },
      { step: 2, name: '冷却水出水设定值自适应下发', expected: '设定值 31.6°C', actual: '31.6°C (ACK Confirm)', pass: true },
      { step: 3, name: '变频风机阶梯调速与水温回读', expected: '稳态偏差 < 0.5°C', actual: '偏差 0.25°C', pass: true },
      { step: 4, name: '冷水机组高压保护联锁校验', expected: '冷凝压力 < 1.45 MPa', actual: '1.28 MPa 稳定', pass: true },
    ],
    telemetryEvidence: [
      { point: 'CW_T_Supply (冷却供水温)', target: '31.6 ± 0.5', measured: '31.4', unit: '°C', withinRange: true },
      { point: 'Wet_Bulb_Temp (室外湿球)', target: '实时气象站', measured: '28.2', unit: '°C', withinRange: true },
      { point: 'Approach_Delta (逼近度)', target: '≤ 3.5', measured: '3.2', unit: '°C', withinRange: true },
      { point: 'CT_Fan_Freq (风机总频率)', target: '30.0 ~ 50.0', measured: '42.5', unit: 'Hz', withinRange: true },
    ],
  },
  {
    id: 'v-02',
    code: 'VRF-2026-019',
    title: '冷冻水供水温升重置动态工况验证 (7.0°C → 8.0°C)',
    system: '冷冻水系统 (CHW)',
    type: 'setpoint_reset',
    status: 'testing',
    sourceRef: '优化方案: OPT-2026-09-001',
    testConditions: '日间尖峰时段，末端所有 AHU 水阀开度未达到 90% 饱和',
    passCriteria: '冷机蒸发温度上升 ≥ 0.8°C，冷水机组 COP 提升 ≥ 4.0%，且最不利端压差恒定',
    observedResult: '温升逐步爬坡至 7.8°C，观测末端回水温度与舒适度响应中...',
    confidenceScore: 78,
    engineer: '李工 (能效优化组)',
    lastTestedAt: '2026-09-18 15:45',
    sequenceSteps: [
      { step: 1, name: '末端阀门开度前置安全检查', expected: 'Max Valve < 85%', actual: '74.2%', pass: true },
      { step: 2, name: '供水设定值步进递增 (+0.2°C/15min)', expected: '7.0 → 7.6°C', actual: '已递增至 7.6°C', pass: true },
      { step: 3, name: '冷冻水供回水温差衰减观测', expected: 'ΔT > 4.2°C', actual: '4.6°C', pass: true },
      { step: 4, name: '最不利末端区域 IAQ 与温湿度监控', expected: '温升 < 0.5°C', actual: '监控中 (当前 24.2°C)', pass: true },
    ],
    telemetryEvidence: [
      { point: 'CHW_T_Supply (冷冻供水温)', target: '8.0 ± 0.3', measured: '7.6', unit: '°C', withinRange: true },
      { point: 'CHW_Delta_P (分集水器压差)', target: '180 ± 15', measured: '182', unit: 'kPa', withinRange: true },
      { point: 'Chiller_Lift_T (冷机温升压比)', target: '下降 ≥ 6%', measured: '-4.8%', unit: '%', withinRange: true },
      { point: 'Max_AHU_Valve (末端最大阀开度)', target: '< 90', measured: '76.4', unit: '%', withinRange: true },
    ],
  },
  {
    id: 'v-03',
    code: 'VRF-2026-020',
    title: '夜间过渡低负荷冷机变频加减机逻辑切换',
    system: '冷水主机组 (Chillers)',
    type: 'strategy_rollout',
    status: 'inconclusive',
    sourceRef: '工单: WO-2026-0042 维护后验收',
    testConditions: '夜间 22:00 ~ 06:00，系统制冷量负荷需求 < 400 kW',
    passCriteria: '由 2# 离心机平滑切换至 3# 磁悬浮小冷机，水温扰动 < 1.0°C，主机无喘振告警',
    observedResult: '切换过程水流开关瞬时触碰下限阈值，切换完成但减机延时超标 4 分钟',
    confidenceScore: 54,
    engineer: '刘工 (自控保障组)',
    lastTestedAt: '2026-09-17 23:20',
    sequenceSteps: [
      { step: 1, name: '负荷持续低于 380 kW 判定 (>20min)', expected: '计时器正常触发', actual: '22min 触发', pass: true },
      { step: 2, name: '备用磁悬浮小冷机预润滑与预启动', expected: '启动就绪信号置位', actual: 'Ready ACK 确认', pass: true },
      { step: 3, name: '水流旁通电动阀与联锁水泵切换', expected: '压差波动 < 30 kPa', actual: '波动 42 kPa (瞬间偏高)', pass: false },
      { step: 4, name: '主离心机平滑停机与卸载', expected: '减载至 20% 停机', actual: '停机正常，用时略长', pass: true },
    ],
    telemetryEvidence: [
      { point: 'CHW_Flow (冷冻水总流量)', target: '≥ 85.0', measured: '78.2', unit: 'm³/h', withinRange: false },
      { point: 'Chiller_Status (3#磁悬浮冷机)', target: 'Running', measured: 'Running', unit: 'State', withinRange: true },
      { point: 'CHW_T_Return (总回水温度)', target: '12.0 ± 0.8', measured: '13.1', unit: '°C', withinRange: false },
      { point: 'Pump_Speed (一次泵变频)', target: '35 ~ 45', measured: '38.0', unit: 'Hz', withinRange: true },
    ],
  },
  {
    id: 'v-04',
    code: 'VRF-2026-021',
    title: '冷冻水二次泵变频定末端压差重置控制',
    system: '水泵输配系统 (Pumps)',
    type: 'periodic_mbcx',
    status: 'pending',
    sourceRef: '持续调试规程 MBCx-2026-Q3',
    testConditions: '常规办公工作日 09:00 ~ 17:00，不同楼层负载扰动下',
    passCriteria: '二次泵频率按最不利末端差压动态调节，输配系数 WTF ≥ 32.0',
    observedResult: '待环境稳定后执行自动化阶梯阶跃测试',
    confidenceScore: 0,
    engineer: '张工 (系统调试组)',
    lastTestedAt: '尚未启动',
    sequenceSteps: [
      { step: 1, name: '各层末端压差变送器数据有效性校准', expected: '无漂移与卡阻', actual: '校验完成', pass: true },
      { step: 2, name: '目标压差从 180 kPa 阶跃至 160 kPa', expected: '调节时间 < 8min', actual: '待测试', pass: false },
      { step: 3, name: '观察二次泵总电功率降幅', expected: '节能 ≥ 12%', actual: '待测试', pass: false },
    ],
    telemetryEvidence: [
      { point: 'Critical_Zone_DP (末端最不利压差)', target: '160 ± 10', measured: '184', unit: 'kPa', withinRange: false },
      { point: 'Secondary_Pump_kW (二次泵总功率)', target: '< 45.0', measured: '52.4', unit: 'kW', withinRange: false },
      { point: 'Transport_WTF (水输配系数)', target: '≥ 32.0', measured: '28.6', unit: 'kW/kW', withinRange: false },
    ],
  },
  {
    id: 'v-05',
    code: 'VRF-2026-022',
    title: 'AHU-04 新风经济器 (Economizer) 变焓值混风模式验证',
    system: '空气处理机组 (AHU)',
    type: 'periodic_mbcx',
    status: 'failed',
    sourceRef: '诊断: D-AHU-2026-088 联动',
    testConditions: '过渡季室外焓值低于室内回风焓值时段',
    passCriteria: '新风风阀开度自适应放大至 100%，表冷水阀完全关闭，送风温度达标',
    observedResult: '新风阀执行器物理开度卡阻在 35%，导致表冷水阀无法关死，存在冷热抵消',
    confidenceScore: 92,
    engineer: '王工 (自控保障组)',
    lastTestedAt: '2026-09-17 11:10',
    sequenceSteps: [
      { step: 1, name: '室外与回风温湿度及焓值计算比对', expected: 'H_oa < H_ra', actual: '满足过渡季自然冷源条件', pass: true },
      { step: 2, name: '新风阀开度增大控制指令下发', expected: 'Command: 100%', actual: 'ACK 确认 100%', pass: true },
      { step: 3, name: '风阀物理开度反馈与冷水阀关断', expected: 'Feedback: 100%', actual: 'Feedback 锁定在 35%', pass: false },
      { step: 4, name: '表冷水阀完全关闭状态验证', expected: 'Valve Position: 0%', actual: '维持开度 18% (抵消冷量)', pass: false },
    ],
    telemetryEvidence: [
      { point: 'OA_Damper_Feedback (新风阀开度)', target: '100', measured: '35.2', unit: '%', withinRange: false },
      { point: 'CHW_Valve_Pos (表冷水阀开度)', target: '0.0', measured: '18.4', unit: '%', withinRange: false },
      { point: 'Supply_Air_Temp (送风温度)', target: '18.0 ± 1.0', measured: '17.2', unit: '°C', withinRange: true },
      { point: 'Energy_Waste_Rate (无效抵消功率)', target: '0.0', measured: '14.2', unit: 'kW', withinRange: false },
    ],
  },
];

export function VerificationsWorkspace({ siteId: _siteId }: { readonly siteId?: string }) {
  const [searchQuery, setSearchQuery] = useState<string>('');
  const [selectedItem, setSelectedItem] = useState<VerificationItem | null>(null);
  const [actionDialogOpen, setActionDialogOpen] = useState<boolean>(false);
  const [actionType, setActionType] = useState<'pass' | 'fail' | 'retest'>('pass');
  const [advancedFilters] = useQueryState(
    VERIFICATION_FILTERS_QUERY_KEY,
    getFiltersStateParser<VerificationItem>([...VERIFICATION_FILTER_COLUMN_IDS]).withDefault([]),
  );
  const [joinOperator] = useQueryState(
    VERIFICATION_JOIN_OPERATOR_QUERY_KEY,
    parseAsStringEnum<JoinOperator>(['and', 'or']).withDefault('and'),
  );

  const filteredItems = useMemo(() => {
    return MOCK_VERIFICATIONS.filter((item) => {
      const matchQuery =
        searchQuery.trim() === '' ||
        item.title.toLowerCase().includes(searchQuery.toLowerCase()) ||
        item.code.toLowerCase().includes(searchQuery.toLowerCase()) ||
        item.system.toLowerCase().includes(searchQuery.toLowerCase()) ||
        item.sourceRef.toLowerCase().includes(searchQuery.toLowerCase());
      const filterMatches = advancedFilters.map((filter) => matchesVerificationAdvancedFilter(item, filter));
      const matchAdvancedFilters = advancedFilters.length === 0 || (joinOperator === 'or'
        ? filterMatches.some(Boolean)
        : filterMatches.every(Boolean));
      return matchQuery && matchAdvancedFilters;
    });
  }, [advancedFilters, joinOperator, searchQuery]);

  const stats = useMemo(() => {
    const total = MOCK_VERIFICATIONS.length;
    const passed = MOCK_VERIFICATIONS.filter((i) => i.status === 'passed').length;
    const testing = MOCK_VERIFICATIONS.filter((i) => i.status === 'testing').length;
    const pending = MOCK_VERIFICATIONS.filter((i) => i.status === 'pending').length;
    const failedOrInconclusive = MOCK_VERIFICATIONS.filter((i) => i.status === 'failed' || i.status === 'inconclusive').length;
    const passRate = ((passed / (passed + failedOrInconclusive || 1)) * 100).toFixed(1);
    return { total, passed, testing, pending, failedOrInconclusive, passRate };
  }, []);

  const getStatusBadge = (status: VerificationStatus) => {
    switch (status) {
      case 'passed':
        return <StatusBadge tone="success" label="验证通过" />;
      case 'testing':
        return <StatusBadge tone="info" pulse={true} label="验证中" />;
      case 'inconclusive':
        return <StatusBadge tone="warning" label="存疑待复测" />;
      case 'failed':
        return <StatusBadge tone="destructive" label="未达标" />;
      case 'pending':
        return <StatusBadge tone="neutral" label="待执行" />;
    }
  };

  const getTypeLabel = (type: VerificationType) => {
    switch (type) {
      case 'strategy_rollout':
        return '策略发布验证';
      case 'maintenance_acceptance':
        return '维保工单验收';
      case 'periodic_mbcx':
        return '持续调试巡检';
      case 'setpoint_reset':
        return '参数重置验证';
    }
  };

  const columns = useMemo<Array<ColumnDef<DataTableFeatures, VerificationItem>>>(() => [
    {
      id: 'select',
      header: ({ table }) => (
        <Checkbox
          checked={table.getIsAllPageRowsSelected() ? true : table.getIsSomePageRowsSelected() ? 'indeterminate' : false}
          onCheckedChange={(checked) => table.toggleAllPageRowsSelected(Boolean(checked))}
          aria-label="选择全部验证项"
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
    { id: 'code', accessorFn: (row) => row.code, meta: { label: '验证编号' }, header: '验证编号', cell: ({ row }) => <span className="font-mono text-xs font-medium text-foreground">{row.original.code}</span> },
    {
      id: 'title',
      accessorFn: (row) => row.title,
      meta: { label: '验证项目' },
      header: '验证项目与所属系统',
      cell: ({ row }) => <div className="flex flex-col"><span className="font-medium text-foreground">{row.original.title}</span><span className="text-xs text-muted-foreground">{row.original.system}</span></div>,
    },
    {
      id: 'type',
      accessorFn: (row) => row.type,
      enableColumnFilter: true,
      meta: { label: '验证类型', variant: 'select', options: [...VERIFICATION_TYPE_OPTIONS] },
      header: '验证类型',
      cell: ({ row }) => <Badge variant="secondary" className="text-xs font-normal">{getTypeLabel(row.original.type)}</Badge>,
    },
    { id: 'source', accessorFn: (row) => row.sourceRef, meta: { label: '触发来源' }, header: '触发来源', cell: ({ row }) => <span className="font-mono text-xs text-muted-foreground">{row.original.sourceRef}</span> },
    {
      id: 'result',
      header: '实测判定与观测结果',
      cell: ({ row }) => <div className="flex flex-col"><span className="line-clamp-1 text-xs text-foreground">{row.original.observedResult}</span><span className="truncate text-[11px] text-muted-foreground">准则: {row.original.passCriteria}</span></div>,
    },
    {
      id: 'confidence',
      accessorFn: (row) => row.confidenceScore,
      meta: { label: '置信度' },
      header: '置信度',
      cell: ({ row }) => row.original.confidenceScore > 0 ? (
        <div className="flex items-center gap-1.5"><Progress value={row.original.confidenceScore} className="h-1.5 w-12" /><span className="text-xs text-muted-foreground tabular-nums">{row.original.confidenceScore}%</span></div>
      ) : <span className="text-xs text-muted-foreground">-</span>,
    },
    {
      id: 'status',
      accessorFn: (row) => row.status,
      enableColumnFilter: true,
      meta: { label: '状态', variant: 'select', options: [...VERIFICATION_STATUS_OPTIONS] },
      header: '状态',
      cell: ({ row }) => getStatusBadge(row.original.status),
    },
    { id: 'engineer', accessorFn: (row) => row.engineer, meta: { label: '责任人' }, header: '责任人', cell: ({ row }) => <span className="text-xs text-muted-foreground">{row.original.engineer}</span> },
    {
      id: 'actions',
      header: '操作',
      cell: ({ row }) => <Button variant="ghost" size="sm" className="h-8 px-2 text-xs" onClick={(event) => { event.stopPropagation(); setSelectedItem(row.original); }}>检查详情</Button>,
      enableSorting: false,
      enableHiding: false,
    },
  ], []);

  const table = useDataTable({
    key: 'surface-26-verifications',
    data: [...filteredItems],
    columns,
    pageSize: 10,
    getRowId: (row) => row.id,
    meta: {
      queryKeys: {
        page: 'verificationPage',
        perPage: 'verificationPerPage',
        sort: 'verificationSort',
        filters: VERIFICATION_FILTERS_QUERY_KEY,
        joinOperator: VERIFICATION_JOIN_OPERATOR_QUERY_KEY,
      },
    },
  });


  return (
    <Main className="space-y-6">
      {/* Header */}
      <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <div className="flex items-center gap-2">
            
            <Badge variant="outline" className="text-xs font-normal">
              调试验证
            </Badge>
          </div>
        </div>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" className="h-8 gap-1.5 text-xs" onClick={() => window.location.reload()}>
            <RotateCcw className="size-3.5" />
            刷新状态
          </Button>
          <Button size="sm" className="h-8 gap-1.5 text-xs">
            <FileCheck2 className="size-3.5" />
            新建验证
          </Button>
        </div>
      </div>

      {/* 4-Card Fact Strip */}
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-xs font-medium text-muted-foreground">待验证 / 执行中</CardTitle>
            <Clock className="size-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold tracking-tight text-foreground tabular-nums">
              {stats.pending + stats.testing} <span className="text-xs font-normal text-muted-foreground">项</span>
            </div>
            <p className="mt-1 text-xs text-muted-foreground">
              {stats.testing} 项比对中 · {stats.pending} 项待工况满足
            </p>
          </CardContent>
        </Card>

        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-xs font-medium text-muted-foreground">验证通过率</CardTitle>
            <CheckCircle2 className="size-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold tracking-tight text-foreground tabular-nums">
              {stats.passRate}%
            </div>
            <p className="mt-1 text-xs text-muted-foreground">
              已完成 {stats.passed} 项 · 目标 &gt; 90%
            </p>
          </CardContent>
        </Card>

        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-xs font-medium text-muted-foreground">平均置信度</CardTitle>
            <ShieldCheck className="size-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold tracking-tight text-foreground tabular-nums">
              96.4 <span className="text-xs font-normal text-muted-foreground">分</span>
            </div>
            <p className="mt-1 text-xs text-muted-foreground">
              遥测回读采样点数 &gt; 120 个/项
            </p>
          </CardContent>
        </Card>

        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-xs font-medium text-muted-foreground">覆盖子系统</CardTitle>
            <AlertTriangle className="size-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold tracking-tight text-foreground tabular-nums">
              4 <span className="text-xs font-normal text-muted-foreground">大子系统</span>
            </div>
            <p className="mt-1 text-xs text-muted-foreground">
              冷水机组 / 冷却水 / 输配 / 末端
            </p>
          </CardContent>
        </Card>
      </div>

      {/* Main Ledger Table */}
      <DataTableBlock>

        <DataTable
          table={table}
          tableAriaLabel="功能验证"
          empty="未找到匹配的功能验证项目。"
          getHeaderCellProps={(header) => ({
            className:
              header.id === 'select' ? 'w-[40px] pl-4' :
              header.id === 'code' ? 'w-[120px]' :
              header.id === 'title' ? 'min-w-[240px]' :
              header.id === 'result' ? 'min-w-[200px]' :
              header.id === 'actions' ? 'pr-4 text-right' :
              undefined,
          })}
          getRowProps={(row) => ({
            className: 'cursor-pointer',
            onClick: () => setSelectedItem(row.original),
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
              totalRows={filteredItems.length}
            />
          )}
        >
          <DataTableAdvancedToolbar table={table}>
            <div className="relative w-full sm:w-64">
              <Search className="absolute left-2.5 top-2.5 size-4 text-muted-foreground" />
              <Input
                placeholder="搜索验证项、策略、系统或单号..."
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

      {/* Slide-over Sheet Inspector */}
      <Sheet open={Boolean(selectedItem)} onOpenChange={(open) => !open && setSelectedItem(null)}>
        <SheetContent className="sm:max-w-xl! w-full overflow-y-auto">
          {selectedItem && (
            <div className="space-y-6 py-2">
              <SheetHeader className="space-y-2 border-b pb-4 text-left">
                <div className="flex items-center gap-2">
                  <span className="font-mono text-xs text-muted-foreground">{selectedItem.code}</span>
                  {getStatusBadge(selectedItem.status)}
                </div>
                <SheetTitle className="text-lg font-bold leading-tight text-foreground">
                  {selectedItem.title}
                </SheetTitle>
                <SheetDescription className="text-xs text-muted-foreground">
                  所属系统：{selectedItem.system} | 触发来源：{selectedItem.sourceRef}
                </SheetDescription>
              </SheetHeader>

              {/* Conditions & Criteria */}
              <div className="space-y-3 rounded-lg border bg-muted/20 p-4">
                <div className="space-y-1">
                  <div className="text-xs font-medium text-muted-foreground">测试前置工况</div>
                  <div className="text-xs text-foreground">{selectedItem.testConditions}</div>
                </div>
                <div className="space-y-1 border-t pt-2">
                  <div className="text-xs font-medium text-muted-foreground">合格判定准则 (Pass Criteria)</div>
                  <div className="text-xs text-foreground font-medium">{selectedItem.passCriteria}</div>
                </div>
                <div className="space-y-1 border-t pt-2">
                  <div className="text-xs font-medium text-muted-foreground">实测观测与推论</div>
                  <div className="text-xs text-foreground">{selectedItem.observedResult}</div>
                </div>
              </div>

              {/* Sequence Steps Execution Trace */}
              <div className="space-y-3">
                <h4 className="text-sm font-semibold text-foreground flex items-center justify-between">
                  <span>时序动作与联锁检验步骤</span>
                  <span className="text-xs font-normal text-muted-foreground font-mono">
                    {selectedItem.sequenceSteps.filter((s) => s.pass).length}/{selectedItem.sequenceSteps.length} 步骤达标
                  </span>
                </h4>
                <div className="space-y-2">
                  {selectedItem.sequenceSteps.map((step) => (
                    <div
                      key={step.step}
                      className="flex items-start justify-between rounded-md border p-3 text-xs"
                    >
                      <div className="flex items-start gap-2.5">
                        <span className={`mt-0.5 flex h-4 w-4 items-center justify-center rounded-full text-[10px] font-bold ${
                          step.pass ? 'bg-emerald-100 text-emerald-700 dark:bg-emerald-950 dark:text-emerald-400' : 'bg-rose-100 text-rose-700 dark:bg-rose-950 dark:text-rose-400'
                        }`}>
                          {step.step}
                        </span>
                        <div>
                          <div className="font-medium text-foreground">{step.name}</div>
                          <div className="mt-0.5 text-muted-foreground">预期: <span className="tabular-nums">{step.expected}</span></div>
                          <div className="mt-0.5 text-foreground">实测: <span className="tabular-nums font-medium">{step.actual}</span></div>
                        </div>
                      </div>
                      <Badge variant={step.pass ? 'outline' : 'destructive'} className="text-[10px] shrink-0">
                        {step.pass ? '符合' : '异常偏差'}
                      </Badge>
                    </div>
                  ))}
                </div>
              </div>

              {/* Telemetry Evidence Points */}
              <div className="space-y-3">
                <h4 className="text-sm font-semibold text-foreground">关键点位回读证据 (Readback Telemetry)</h4>
                <Table aria-label="关键点位回读证据">
                  <TableHeader className="bg-muted/40">
                    <TableRow className="hover:bg-transparent">
                      <TableHead>点位名称</TableHead>
                      <TableHead>目标范围</TableHead>
                      <TableHead>实测回读</TableHead>
                      <TableHead className="text-right">状态</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {(selectedItem?.telemetryEvidence ?? []).map((row) => (
                      <TableRow key={row.point}>
                        <TableCell className="font-mono text-muted-foreground">{row.point}</TableCell>
                        <TableCell>{row.target} {row.unit}</TableCell>
                        <TableCell className="font-mono font-medium">{row.measured} {row.unit}</TableCell>
                        <TableCell className="text-right">
                          {row.withinRange
                            ? <Badge variant="outline" className="border-emerald-500/30 text-[10px] text-emerald-600">在允许区间</Badge>
                            : <Badge variant="destructive" className="text-[10px]">越界偏差</Badge>}
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>

              {/* Inspector Actions */}
              <div className="border-t pt-4 flex items-center justify-between gap-2">
                <Button
                  variant="outline"
                  size="sm"
                  className="gap-1.5 text-xs text-rose-600 hover:text-rose-700"
                  onClick={() => {
                    setActionType('fail');
                    setActionDialogOpen(true);
                  }}
                >
                  <AlertTriangle className="h-3.5 w-3.5" />
                  判定不合格并派单
                </Button>
                <div className="flex items-center gap-2">
                  <Button
                    variant="outline"
                    size="sm"
                    className="gap-1.5 text-xs"
                    onClick={() => {
                      setActionType('retest');
                      setActionDialogOpen(true);
                    }}
                  >
                    <RotateCcw className="h-3.5 w-3.5" />
                    安排重新测试
                  </Button>
                  <Button
                    size="sm"
                    className="gap-1.5 text-xs bg-emerald-600 hover:bg-emerald-700 text-white"
                    onClick={() => {
                      setActionType('pass');
                      setActionDialogOpen(true);
                    }}
                  >
                    <CheckCircle2 className="h-3.5 w-3.5" />
                    签发验收合格
                  </Button>
                </div>
              </div>
            </div>
          )}
        </SheetContent>
      </Sheet>

      {/* Action Dialog */}
      <Dialog open={actionDialogOpen} onOpenChange={setActionDialogOpen}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>
              {actionType === 'pass' && '签发功能验证合格'}
              {actionType === 'fail' && '判定验证不达标并生成整改工单'}
              {actionType === 'retest' && '触发重新测试'}
            </DialogTitle>
            <DialogDescription>
              {actionType === 'pass' && '签署后该项目将被标记为通过并归档至持续调试审计日志，策略可正式进入长期投运。'}
              {actionType === 'fail' && '系统将记录偏差证据，并自动向自控工程组派发检修与再校准工单。'}
              {actionType === 'retest' && '系统将在下一次负荷工况满足时自动下发测试阶跃并采集点位回读。'}
            </DialogDescription>
          </DialogHeader>
          <div className="py-2 text-xs text-muted-foreground">
            验证编号：<span className="font-mono text-foreground font-semibold">{selectedItem?.code}</span>
          </div>
          <DialogFooter>
            <Button variant="outline" size="sm" onClick={() => setActionDialogOpen(false)}>取消</Button>
            <Button
              size="sm"
              variant={actionType === 'fail' ? 'destructive' : 'default'}
              onClick={() => {
                setActionDialogOpen(false);
                setSelectedItem(null);
              }}
            >
              确认提交
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </Main>
  );
}
