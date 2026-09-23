import { useMemo, useState } from 'react';
import type { ColumnDef } from '@tanstack/react-table';
import { parseAsStringEnum, useQueryState } from 'nuqs';
import {
  AlertCircle,
  CheckCircle2,
  ChevronRight,
  ListTodo,
  Milestone,
  Plus,
  RefreshCw,
  Search,
  ShieldAlert,
  Target,
  Zap,
} from 'lucide-react';
import { DataTableBlock } from '@/blocks/data-table';
import { Main } from '@/components/layout/Main';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { Progress } from '@/components/ui/progress';
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet';
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

export interface ActionPlansWorkspaceProps {
  readonly siteId: string;
}

type PlanStatus = 'COMPLETED' | 'IN_PROGRESS' | 'BLOCKED' | 'PLANNED';

const ACTION_PLAN_STATUS_OPTIONS = [
  { label: '已结项', value: 'COMPLETED' },
  { label: '执行中', value: 'IN_PROGRESS' },
  { label: '受阻挂起', value: 'BLOCKED' },
  { label: '立项规划', value: 'PLANNED' },
] as const;
const ACTION_PLAN_FILTER_COLUMN_IDS = ['status', 'team'] as const;
const ACTION_PLAN_FILTERS_QUERY_KEY = 'actionPlanFilters';
const ACTION_PLAN_JOIN_OPERATOR_QUERY_KEY = 'actionPlanJoinOperator';

interface ActionPlan {
  readonly id: string;
  readonly code: string;
  readonly title: string;
  readonly seuReference: string;
  readonly owner: string;
  readonly team: string;
  readonly period: string;
  readonly budgetYuan: number;
  readonly targetSavingMWh: number;
  readonly verifiedSavingMWh: number;
  readonly progress: number;
  readonly status: PlanStatus;
  readonly blockerReason?: string;
  readonly milestones: readonly {
    readonly step: string;
    readonly date: string;
    readonly done: boolean;
  }[];
}

const ACTION_PLANS: readonly ActionPlan[] = [
  {
    id: 'ap-01',
    code: 'AP-2026-01',
    title: '1#冷水主机冷冻供水温自适应提升 (7.0°C → 8.5°C)',
    seuReference: 'SEU-01 (1#冷水机组)',
    owner: '李工',
    team: '自控与策略组',
    period: '2026.03 ~ 2026.05',
    budgetYuan: 0,
    targetSavingMWh: 68.5,
    verifiedSavingMWh: 68.5,
    progress: 100,
    status: 'COMPLETED',
    milestones: [
      { step: '策略仿真与边界前置检查', date: '2026-03-05', done: true },
      { step: '边缘网关自适应逻辑投运', date: '2026-03-12', done: true },
      { step: '持续调试与末端舒适度跟踪', date: '2026-04-15', done: true },
      { step: 'IPMVP Option C 节能量核验结项', date: '2026-05-10', done: true },
    ],
  },
  {
    id: 'ap-02',
    code: 'AP-2026-02',
    title: '冷冻水二次泵变频压差闭环控制优化 (消除 45kPa 冗余)',
    seuReference: 'SEU-03 (水泵输配系统)',
    owner: '张工',
    team: '暖通运维组',
    period: '2026.04 ~ 2026.07',
    budgetYuan: 12000,
    targetSavingMWh: 42.0,
    verifiedSavingMWh: 38.5,
    progress: 92,
    status: 'IN_PROGRESS',
    milestones: [
      { step: '末端不利环路压差传感器校准', date: '2026-04-10', done: true },
      { step: '变频器 PID 参数整定优化', date: '2026-05-02', done: true },
      { step: '全工况能效跟踪测试', date: '2026-06-20', done: true },
      { step: '最终 M&V 核验与归档', date: '2026-07-15', done: false },
    ],
  },
  {
    id: 'ap-03',
    code: 'AP-2026-03',
    title: '过渡季超低噪冷却塔侧向直接供冷 (Free Cooling 改造)',
    seuReference: 'SEU-04 (冷却塔系统)',
    owner: '王工',
    team: '工程技改组',
    period: '2026.06 ~ 2026.10',
    budgetYuan: 85000,
    targetSavingMWh: 85.0,
    verifiedSavingMWh: 0,
    progress: 45,
    status: 'IN_PROGRESS',
    milestones: [
      { step: '板式换热器与旁通管路勘测', date: '2026-06-15', done: true },
      { step: '电动阀门与控制箱硬件安装', date: '2026-07-28', done: true },
      { step: '带水试压与电动蝶阀联动调试', date: '2026-09-01', done: false },
      { step: '秋冬过渡季自动投运', date: '2026-10-15', done: false },
    ],
  },
  {
    id: 'ap-04',
    code: 'AP-2026-04',
    title: '非营业时段商铺及办公末端空调待机自动切断',
    seuReference: 'SEU-05 (空调末端)',
    owner: '赵工',
    team: '物业值班组',
    period: '2026.05 ~ 2026.06',
    budgetYuan: 0,
    targetSavingMWh: 29.5,
    verifiedSavingMWh: 29.5,
    progress: 100,
    status: 'COMPLETED',
    milestones: [
      { step: '营业日历与各区域分区分时排程制定', date: '2026-05-05', done: true },
      { step: 'BACnet 控制器排程规则注入', date: '2026-05-18', done: true },
      { step: '夜间巡检无感关断验收', date: '2026-06-10', done: true },
    ],
  },
  {
    id: 'ap-05',
    code: 'AP-2026-05',
    title: '2#离心冷水机组冷凝器在线自动清洗装置加装',
    seuReference: 'SEU-02 (2#冷水机组)',
    owner: '陈工',
    team: '设备保全组',
    period: '2026.07 ~ 2026.09',
    budgetYuan: 45000,
    targetSavingMWh: 38.0,
    verifiedSavingMWh: 0,
    progress: 20,
    status: 'BLOCKED',
    blockerReason: '受供冷季连续满负荷运行约束，主机无法停机开盖加装胶球清洗泵，推迟至10月检修窗口。',
    milestones: [
      { step: '设备选型与商务采购入库', date: '2026-07-10', done: true },
      { step: '现场管路改造接驳 (受阻挂起)', date: '2026-08-01', done: false },
      { step: '在线清洗系统带电联调', date: '2026-09-20', done: false },
    ],
  },
];

const ACTION_PLAN_TEAM_OPTIONS = [...new Set(ACTION_PLANS.map((plan) => plan.team))]
  .map((value) => ({ label: value, value }));

function matchesActionPlanAdvancedFilter(
  plan: ActionPlan,
  filter: ExtendedColumnFilter<ActionPlan>,
) {
  const actual = filter.id === 'status' ? plan.status : filter.id === 'team' ? plan.team : '';
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

export function ActionPlansWorkspace({ siteId: _siteId }: ActionPlansWorkspaceProps) {
  const [selectedPlan, setSelectedPlan] = useState<ActionPlan | null>(null);
  const [searchQuery, setSearchQuery] = useState('');
  const [advancedFilters] = useQueryState(
    ACTION_PLAN_FILTERS_QUERY_KEY,
    getFiltersStateParser<ActionPlan>([...ACTION_PLAN_FILTER_COLUMN_IDS]).withDefault([]),
  );
  const [joinOperator] = useQueryState(
    ACTION_PLAN_JOIN_OPERATOR_QUERY_KEY,
    parseAsStringEnum<JoinOperator>(['and', 'or']).withDefault('and'),
  );
  const filteredPlans = useMemo(() => {
    return ACTION_PLANS.filter((plan) => {
      const q = searchQuery.trim().toLowerCase();
      const matchesSearch = !q || (
        plan.title.toLowerCase().includes(q) ||
        plan.code.toLowerCase().includes(q) ||
        plan.owner.toLowerCase().includes(q) ||
        plan.seuReference.toLowerCase().includes(q)
      );
      const filterMatches = advancedFilters.map((filter) => matchesActionPlanAdvancedFilter(plan, filter));
      const matchesAdvancedFilters = advancedFilters.length === 0 || (joinOperator === 'or'
        ? filterMatches.some(Boolean)
        : filterMatches.every(Boolean));
      return matchesSearch && matchesAdvancedFilters;
    });
  }, [advancedFilters, joinOperator, searchQuery]);

  const columns = useMemo<Array<ColumnDef<DataTableFeatures, ActionPlan>>>(() => [
    {
      id: 'select',
      header: ({ table }) => (
        <Checkbox
          checked={table.getIsAllPageRowsSelected() ? true : table.getIsSomePageRowsSelected() ? 'indeterminate' : false}
          onCheckedChange={(checked) => table.toggleAllPageRowsSelected(Boolean(checked))}
          aria-label="全选行动计划"
        />
      ),
      cell: ({ row }) => (
        <Checkbox
          checked={row.getIsSelected()}
          onCheckedChange={(checked) => row.toggleSelected(Boolean(checked))}
          aria-label={`选择 ${row.original.title}`}
          onClick={(event) => event.stopPropagation()}
        />
      ),
      enableSorting: false,
      enableHiding: false,
    },
    { id: 'code', accessorFn: (row) => row.code, meta: { label: '计划编号' }, header: '计划编号', cell: ({ row }) => <span className="font-mono font-semibold text-foreground">{row.original.code}</span> },
    {
      id: 'title',
      accessorFn: (row) => row.title,
      meta: { label: '行动计划' },
      header: '行动计划名称与归属',
      cell: ({ row }) => <div><div className="font-medium text-foreground">{row.original.title}</div><div className="text-[11px] text-muted-foreground">{row.original.seuReference}</div></div>,
    },
    {
      id: 'team',
      accessorFn: (row) => row.team,
      enableColumnFilter: true,
      meta: { label: '执行团队', variant: 'select', options: ACTION_PLAN_TEAM_OPTIONS },
      header: '责任人与执行团队',
      cell: ({ row }) => <div><div className="font-medium text-foreground">{row.original.owner}</div><div className="text-[11px] text-muted-foreground">{row.original.team}</div></div>,
    },
    { id: 'period', accessorFn: (row) => row.period, meta: { label: '工期周期' }, header: '工期周期', cell: ({ row }) => <span className="font-mono text-[11px] text-muted-foreground tabular-nums">{row.original.period}</span> },
    {
      id: 'budget',
      accessorFn: (row) => row.budgetYuan,
      meta: { label: '投资预算' },
      header: '投资预算',
      cell: ({ row }) => <span className="font-mono font-medium text-foreground tabular-nums">{row.original.budgetYuan === 0 ? '零成本自控' : `¥${row.original.budgetYuan.toLocaleString()}`}</span>,
    },
    {
      id: 'saving',
      accessorFn: (row) => row.verifiedSavingMWh,
      meta: { label: '已核验节电' },
      header: '已核验 / 目标节电',
      cell: ({ row }) => <span className="font-mono tabular-nums"><strong>{row.original.verifiedSavingMWh.toFixed(1)}</strong> <span className="text-[11px] text-muted-foreground">/ {row.original.targetSavingMWh.toFixed(1)} MWh</span></span>,
    },
    {
      id: 'progress',
      accessorFn: (row) => row.progress,
      meta: { label: '实施进度' },
      header: '实施进度',
      cell: ({ row }) => <div className="min-w-24 space-y-1"><div className="text-[11px] font-mono tabular-nums">{row.original.progress}%</div><Progress value={row.original.progress} className="h-1.5" /></div>,
    },
    {
      id: 'status',
      accessorFn: (row) => row.status,
      enableColumnFilter: true,
      meta: { label: '状态', variant: 'select', options: [...ACTION_PLAN_STATUS_OPTIONS] },
      header: '状态',
      cell: ({ row }) => (
        <StatusBadge
          tone={row.original.status === 'COMPLETED' ? 'success' : row.original.status === 'IN_PROGRESS' ? 'info' : row.original.status === 'BLOCKED' ? 'destructive' : 'neutral'}
          pulse={row.original.status === 'IN_PROGRESS' || row.original.status === 'BLOCKED'}
          label={row.original.status === 'COMPLETED' ? '已结项' : row.original.status === 'IN_PROGRESS' ? '执行中' : row.original.status === 'BLOCKED' ? '受阻挂起' : '立项规划'}
        />
      ),
    },
    {
      id: 'actions',
      header: '操作',
      cell: ({ row }) => (
        <Button variant="ghost" size="sm" className="h-7 gap-0.5 px-2 text-xs" onClick={(event) => { event.stopPropagation(); setSelectedPlan(row.original); }}>
          里程碑<ChevronRight className="h-3 w-3" />
        </Button>
      ),
      enableSorting: false,
    },
  ], []);

  const table = useDataTable({
    key: 'surface-23-action-plans',
    data: [...filteredPlans],
    columns,
    pageSize: 10,
    getRowId: (plan) => plan.id,
    meta: {
      queryKeys: {
        page: 'actionPlanPage',
        perPage: 'actionPlanPerPage',
        sort: 'actionPlanSort',
        filters: ACTION_PLAN_FILTERS_QUERY_KEY,
        joinOperator: ACTION_PLAN_JOIN_OPERATOR_QUERY_KEY,
      },
    },
  });


  return (
    <Main className="space-y-6">
      {/* 1. Surface Page Header */}
      <div className="flex flex-col gap-3 md:flex-row md:items-center md:justify-between border-b pb-4">
        <div>
          <div className="flex items-center gap-2">
            
            <span className="rounded bg-muted px-2 py-0.5 text-xs text-muted-foreground tabular-nums">
              {ACTION_PLANS.length} 项在编行动方案
            </span>
          </div>
        </div>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" className="gap-1.5" onClick={() => window.location.reload()}>
            <RefreshCw className="h-3.5 w-3.5" />
            刷新推进进度
          </Button>
          <Button size="sm" className="gap-1.5">
            <Plus className="h-3.5 w-3.5" />
            新建行动计划
          </Button>
        </div>
      </div>

      {/* 2. Objectives Posture Strip (4 Fact Cards) */}
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-xs font-medium text-muted-foreground">节电目标</CardTitle>
            <Target className="size-4 text-muted-foreground" />
          </CardHeader>
          <CardContent className="space-y-1.5">
            <div className="flex items-baseline gap-2">
              <span className="text-2xl font-bold text-foreground tabular-nums">68.5%</span>
              <span className="text-xs text-muted-foreground tabular-nums">822.0 / 1,200 MWh</span>
            </div>
            <div className="pt-1">
              <Progress value={68.5} className="h-1.5" />
            </div>
            <div className="flex items-center justify-between text-[11px] pt-1">
              <span className="text-muted-foreground">核验依据</span>
              <span className="text-foreground font-medium">IPMVP Option C</span>
            </div>
          </CardContent>
        </Card>

        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-xs font-medium text-muted-foreground">已节省电费</CardTitle>
            <Zap className="size-4 text-muted-foreground" />
          </CardHeader>
          <CardContent className="space-y-1.5">
            <div className="flex items-baseline gap-2">
              <span className="text-2xl font-bold text-foreground tabular-nums">¥68.2</span>
              <span className="text-xs text-muted-foreground">万元</span>
            </div>
            <div className="flex items-center justify-between text-xs text-muted-foreground pt-1">
              <span>规划达成度</span>
              <span className="text-foreground font-medium tabular-nums">71.8% (目标 ¥95万)</span>
            </div>
            <div className="flex items-center justify-between text-[11px]">
              <span className="text-muted-foreground">加权电价</span>
              <span className="text-foreground font-medium tabular-nums">¥0.83 / kWh</span>
            </div>
          </CardContent>
        </Card>

        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-xs font-medium text-muted-foreground">进行中的措施</CardTitle>
            <ListTodo className="size-4 text-muted-foreground" />
          </CardHeader>
          <CardContent className="space-y-1.5">
            <div className="flex items-baseline gap-2">
              <span className="text-2xl font-bold text-foreground tabular-nums">5</span>
              <span className="text-xs text-muted-foreground">项立项推进</span>
            </div>
            <div className="flex items-center justify-between text-xs text-muted-foreground pt-1">
              <span>状态分布</span>
              <span className="text-[11px] text-foreground font-medium tabular-nums">2结项 / 2执行 / 1受阻</span>
            </div>
            <div className="flex items-center justify-between text-[11px]">
              <span className="text-muted-foreground">重点用能设备</span>
              <span className="text-foreground font-medium">覆盖 100% 重大设备</span>
            </div>
          </CardContent>
        </Card>

        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-xs font-medium text-muted-foreground">受阻措施</CardTitle>
            <ShieldAlert className="size-4 text-muted-foreground" />
          </CardHeader>
          <CardContent className="space-y-1.5">
            <div className="flex items-baseline gap-2">
              <span className="text-2xl font-bold text-foreground tabular-nums">1</span>
              <span className="text-xs text-muted-foreground font-medium">项工期阻塞</span>
            </div>
            <div className="flex items-center justify-between text-xs text-muted-foreground pt-1">
              <span>受阻计划</span>
              <span className="text-[11px] text-foreground font-medium truncate max-w-[150px]">AP-2026-05 自动清洗加装</span>
            </div>
            <div className="flex items-center justify-between text-[11px]">
              <span className="text-muted-foreground">解阻方案</span>
              <span className="text-foreground font-medium">调至 10 月检修停机窗口</span>
            </div>
          </CardContent>
        </Card>
      </div>

      {/* 3. Action Plans Ledger */}
      <DataTableBlock
        title="节能措施"
      >

        <DataTable
          table={table}
          tableAriaLabel="节能措施"
          empty="没有符合条件的行动计划记录"
          getHeaderCellProps={(header) => ({
            className:
              header.id === 'select' ? 'w-10' :
              header.id === 'code' ? 'w-[120px]' :
              header.id === 'team' || header.id === 'period' ? 'w-[140px]' :
              header.id === 'budget' ? 'w-[110px] text-right' :
              header.id === 'saving' ? 'w-[140px] text-right' :
              header.id === 'progress' ? 'w-[130px]' :
              header.id === 'status' ? 'w-[110px] text-center' :
              header.id === 'actions' ? 'w-[80px] text-right' :
              undefined,
          })}
          getRowProps={(row) => ({
            className: 'cursor-pointer',
            onClick: () => setSelectedPlan(row.original),
          })}
          getCellProps={(cell) => ({
            className:
              cell.column.id === 'budget' || cell.column.id === 'saving' || cell.column.id === 'actions'
                ? 'text-right'
                : cell.column.id === 'status'
                  ? 'text-center'
                  : undefined,
          })}
          footer={(
            <DataTablePagination
              table={table}
              totalRows={filteredPlans.length}
            />
          )}
        >
          <DataTableAdvancedToolbar table={table}>
            <div className="relative w-48 sm:w-60">
              <Search className="absolute left-2.5 top-2.5 size-3.5 text-muted-foreground" />
              <Input
                placeholder="搜索计划名称 / 编号 / 负责人..."
                value={searchQuery}
                onChange={(e) => {
                  setSearchQuery(e.target.value);
                  table.setPageIndex(0);
                }}
                className="h-8 bg-background pl-8 text-xs"
              />
            </div>
            <DataTableSortList table={table} />
            <DataTableFilterList table={table} />
          </DataTableAdvancedToolbar>
        </DataTable>
      </DataTableBlock>

      {/* 4. Plan Detail & Milestones Sheet */}
      <Sheet open={Boolean(selectedPlan)} onOpenChange={(open) => !open && setSelectedPlan(null)}>
        <SheetContent className="sm:max-w-xl!">
          <SheetHeader>
            <div className="flex items-center gap-2">
              <span className="font-mono text-xs font-semibold text-muted-foreground">
                {selectedPlan?.code}
              </span>
              <span className="rounded bg-muted px-2 py-0.5 text-[10px] font-medium text-muted-foreground">
                {selectedPlan?.seuReference}
              </span>
            </div>
            <SheetTitle className="text-base font-bold text-foreground mt-1">
              {selectedPlan?.title}
            </SheetTitle>
            <SheetDescription className="text-xs">
              责任人: {selectedPlan?.owner} ({selectedPlan?.team}) · 工期: {selectedPlan?.period}
            </SheetDescription>
          </SheetHeader>

          {selectedPlan && (
            <div className="min-h-0 flex-1 overflow-y-auto px-4 pb-4 space-y-5 pt-4 text-xs">
              {/* Core Facts Grid */}
              <div className="grid grid-cols-2 gap-3 rounded-lg border border-border/80 bg-muted/10 p-3.5">
                <div>
                  <div className="text-muted-foreground text-[11px]">目标年节电量</div>
                  <div className="mt-1 text-base font-bold text-foreground tabular-nums">
                    {selectedPlan.targetSavingMWh} MWh / 年
                  </div>
                </div>
                <div>
                  <div className="text-muted-foreground text-[11px]">已核验实际节电</div>
                  <div className="mt-1 text-base font-bold text-emerald-600 tabular-nums">
                    {selectedPlan.verifiedSavingMWh} MWh
                  </div>
                </div>
                <div>
                  <div className="text-muted-foreground text-[11px]">实施投资预算</div>
                  <div className="mt-1 text-xs font-medium text-foreground tabular-nums">
                    {selectedPlan.budgetYuan === 0 ? '零成本自控调优' : `¥${selectedPlan.budgetYuan.toLocaleString()}`}
                  </div>
                </div>
                <div>
                  <div className="text-muted-foreground text-[11px]">整体实施进度</div>
                  <div className="mt-1 text-xs font-semibold text-foreground tabular-nums">
                    {selectedPlan.progress}% 完成
                  </div>
                </div>
              </div>

              {/* Blocker Alert if applicable */}
              {selectedPlan.blockerReason && (
                <div className="rounded-lg border border-destructive/30 bg-destructive/5 p-3.5 space-y-1">
                  <div className="flex items-center gap-1.5 text-xs font-semibold text-destructive">
                    <AlertCircle className="h-3.5 w-3.5" />
                    行动计划当前受阻挂起原因
                  </div>
                  <p className="text-foreground text-[11px] leading-relaxed">
                    {selectedPlan.blockerReason}
                  </p>
                </div>
              )}

              {/* Milestones Timeline */}
              <div className="space-y-2">
                <div className="text-xs font-semibold text-foreground flex items-center gap-1.5">
                  <Milestone className="h-3.5 w-3.5 text-primary" />
                  实施推进阶段里程碑 (Milestones)
                </div>
                <div className="relative pl-5 space-y-3.5 border-l border-border/60 ml-2">
                  {selectedPlan.milestones.map((m, idx) => (
                    <div key={idx} className="relative">
                      <div
                        className={`absolute -left-[27px] top-0.5 h-3.5 w-3.5 rounded-full border-2 bg-background flex items-center justify-center ${
                          m.done ? 'border-emerald-500 text-emerald-500' : 'border-muted-foreground/60 text-muted-foreground'
                        }`}
                      >
                        {m.done && <CheckCircle2 className="h-2.5 w-2.5" />}
                      </div>
                      <div className="text-xs font-medium text-foreground">{m.step}</div>
                      <div className="text-[11px] text-muted-foreground mt-0.5 tabular-nums">
                        预定完成: {m.date} · {m.done ? '已验收达成' : '推进中'}
                      </div>
                    </div>
                  ))}
                </div>
              </div>

              {/* Actions */}
              <div className="pt-3 border-t flex items-center justify-end gap-2">
                <Button variant="outline" size="sm" onClick={() => setSelectedPlan(null)}>
                  关闭
                </Button>
                <Button size="sm">
                  更新里程碑进度
                </Button>
              </div>
            </div>
          )}
        </SheetContent>
      </Sheet>
    </Main>
  );
}
