import { useMemo, useState } from 'react';
import type { ColumnDef } from '@tanstack/react-table';
import { parseAsStringEnum, useQueryState } from 'nuqs';
import {
  Award,
  ClipboardCheck,
  FileSpreadsheet,
  Flame,
  LineChart,
  TrendingDown,
} from 'lucide-react';
import { DataTableBlock } from '@/blocks/data-table';
import { Main } from '@/components/layout/Main';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Progress } from '@/components/ui/progress';
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs';
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

export interface EnergyReviewWorkspaceProps {
  readonly siteId: string;
}

interface SEUItem {
  readonly code: string;
  readonly name: string;
  readonly category: string;
  readonly annualKWh: number;
  readonly sharePercent: number;
  readonly cumulativePercent: number;
  readonly potentialSavingKWh: number;
  readonly opportunitySummary: string;
  readonly status: 'IDENTIFIED' | 'REVIEWED' | 'ACTION_IN_PROGRESS';
}

const SEU_ITEMS: readonly SEUItem[] = [
  {
    code: 'SEU-01',
    name: '1#离心冷水机组 (CH-01)',
    category: '冷水主机群',
    annualKWh: 542000,
    sharePercent: 41.2,
    cumulativePercent: 41.2,
    potentialSavingKWh: 48500,
    opportunitySummary: '冷水供水温自适应提升 + 低负荷减机运行优化',
    status: 'ACTION_IN_PROGRESS',
  },
  {
    code: 'SEU-02',
    name: '2#离心冷水机组 (CH-02)',
    category: '冷水主机群',
    annualKWh: 388000,
    sharePercent: 29.5,
    cumulativePercent: 70.7,
    potentialSavingKWh: 32000,
    opportunitySummary: '主机部分负荷运行能效衰退，建议清洗冷凝管并自适应排产',
    status: 'ACTION_IN_PROGRESS',
  },
  {
    code: 'SEU-03',
    name: '冷冻二次水输配泵组 (PMP-01~04)',
    category: '水泵输配系统',
    annualKWh: 184000,
    sharePercent: 14.0,
    cumulativePercent: 84.7,
    potentialSavingKWh: 26000,
    opportunitySummary: '消除固定压差控制的安全裕量冗余，改用最不利末端闭环变压差',
    status: 'REVIEWED',
  },
  {
    code: 'SEU-04',
    name: '超低噪冷却塔风机群 (CT-01~04)',
    category: '冷却塔散热系统',
    annualKWh: 112000,
    sharePercent: 8.5,
    cumulativePercent: 93.2,
    potentialSavingKWh: 14500,
    opportunitySummary: '湿球温度逼近度自适应多台风机同频运行调优',
    status: 'IDENTIFIED',
  },
  {
    code: 'SEU-05',
    name: '裙楼及大堂空调末端箱 (AHU-01~08)',
    category: '空气处理末端',
    annualKWh: 89000,
    sharePercent: 6.8,
    cumulativePercent: 100.0,
    potentialSavingKWh: 11000,
    opportunitySummary: '非营业时段待机能耗自动关断与新风二氧化碳需求联动',
    status: 'IDENTIFIED',
  },
];

const ENERGY_REVIEW_CATEGORY_OPTIONS = [...new Set(SEU_ITEMS.map((item) => item.category))]
  .map((value) => ({ label: value, value }));
const ENERGY_REVIEW_STATUS_OPTIONS = [
  { label: '已识别待立项', value: 'IDENTIFIED' },
  { label: '已完成评审', value: 'REVIEWED' },
  { label: '行动计划执行中', value: 'ACTION_IN_PROGRESS' },
] as const;
const ENERGY_REVIEW_FILTER_COLUMN_IDS = ['category', 'status'] as const;
const ENERGY_REVIEW_FILTERS_QUERY_KEY = 'energyReviewFilters';
const ENERGY_REVIEW_JOIN_OPERATOR_QUERY_KEY = 'energyReviewJoinOperator';

function matchesEnergyReviewAdvancedFilter(
  item: SEUItem,
  filter: ExtendedColumnFilter<SEUItem>,
) {
  const actual = filter.id === 'category' ? item.category : filter.id === 'status' ? item.status : '';
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

export function EnergyReviewWorkspace({ siteId: _siteId }: EnergyReviewWorkspaceProps) {
  const [activeTab, setActiveTab] = useState('SEU');
  const [advancedFilters] = useQueryState(
    ENERGY_REVIEW_FILTERS_QUERY_KEY,
    getFiltersStateParser<SEUItem>([...ENERGY_REVIEW_FILTER_COLUMN_IDS]).withDefault([]),
  );
  const [joinOperator] = useQueryState(
    ENERGY_REVIEW_JOIN_OPERATOR_QUERY_KEY,
    parseAsStringEnum<JoinOperator>(['and', 'or']).withDefault('and'),
  );
  const filteredSeuItems = useMemo(() => SEU_ITEMS.filter((item) => {
    if (advancedFilters.length === 0) return true;
    const matches = advancedFilters.map((filter) => matchesEnergyReviewAdvancedFilter(item, filter));
    return joinOperator === 'or' ? matches.some(Boolean) : matches.every(Boolean);
  }), [advancedFilters, joinOperator]);
  const columns = useMemo<Array<ColumnDef<DataTableFeatures, SEUItem>>>(() => [
    {
      id: 'select',
      header: ({ table }) => (
        <Checkbox
          checked={table.getIsAllPageRowsSelected() ? true : table.getIsSomePageRowsSelected() ? 'indeterminate' : false}
          onCheckedChange={(checked) => table.toggleAllPageRowsSelected(Boolean(checked))}
          aria-label="全选重大用能设备"
        />
      ),
      cell: ({ row }) => (
        <Checkbox
          checked={row.getIsSelected()}
          onCheckedChange={(checked) => row.toggleSelected(Boolean(checked))}
          aria-label={`选择 ${row.original.code}`}
        />
      ),
      enableSorting: false,
      enableHiding: false,
    },
    { id: 'code', accessorFn: (row) => row.code, meta: { label: 'SEU 编号' }, header: 'SEU 编号', cell: ({ row }) => <span className="font-mono text-xs font-medium text-foreground">{row.original.code}</span> },
    { id: 'name', accessorFn: (row) => row.name, meta: { label: '重大用能系统 / 设备名称' }, header: '重大用能系统 / 设备名称', cell: ({ row }) => <span className="font-medium text-foreground">{row.original.name}</span> },
    { id: 'category', accessorFn: (row) => row.category, enableColumnFilter: true, meta: { label: '归属系统类别', variant: 'select', options: ENERGY_REVIEW_CATEGORY_OPTIONS }, header: '归属系统类别', cell: ({ row }) => <span className="text-xs text-muted-foreground">{row.original.category}</span> },
    { id: 'consumption', accessorFn: (row) => row.annualKWh, meta: { label: '年基准耗电量' }, header: '年基准耗电量', cell: ({ row }) => <span className="font-mono text-xs font-semibold text-foreground tabular-nums">{(row.original.annualKWh / 10000).toFixed(1)} 万 kWh</span> },
    { id: 'share', accessorFn: (row) => row.sharePercent, meta: { label: '全站耗能占比' }, header: '全站耗能占比', cell: ({ row }) => <span className="font-mono text-xs font-medium text-foreground tabular-nums">{row.original.sharePercent}%</span> },
    {
      id: 'cumulative',
      accessorFn: (row) => row.cumulativePercent,
      meta: { label: '帕累托累计占比' },
      header: '帕累托累计占比',
      cell: ({ row }) => (
        <div className="min-w-[130px] space-y-1">
          <div className="flex items-center justify-between text-xs font-mono tabular-nums">
            <span>{row.original.cumulativePercent}%</span>
            <span className="text-[10px] text-muted-foreground">{row.original.cumulativePercent <= 80 ? '前80%核心' : '长尾'}</span>
          </div>
          <Progress value={row.original.cumulativePercent} className="h-1.5" />
        </div>
      ),
    },
    { id: 'saving', accessorFn: (row) => row.potentialSavingKWh, meta: { label: '预估节能改进潜力' }, header: '预估节能改进潜力', cell: ({ row }) => <span className="font-mono text-xs font-semibold text-foreground tabular-nums">+{(row.original.potentialSavingKWh / 1000).toFixed(1)} MWh/年</span> },
    { id: 'opportunity', header: '核心改进空间与行动方案', cell: ({ row }) => <span className="block max-w-[280px] text-xs text-muted-foreground">{row.original.opportunitySummary}</span> },
    {
      id: 'status',
      accessorFn: (row) => row.status,
      enableColumnFilter: true,
      meta: { label: '管控状态', variant: 'select', options: [...ENERGY_REVIEW_STATUS_OPTIONS] },
      header: '管控状态',
      cell: ({ row }) => (
        <StatusBadge
          tone={row.original.status === 'ACTION_IN_PROGRESS' ? 'in-progress' : row.original.status === 'REVIEWED' ? 'success' : 'neutral'}
          pulse={row.original.status === 'ACTION_IN_PROGRESS'}
          label={row.original.status === 'ACTION_IN_PROGRESS' ? '行动计划执行中' : row.original.status === 'REVIEWED' ? '已完成评审' : '已识别待立项'}
        />
      ),
    },
  ], []);

  const table = useDataTable({
    key: 'surface-24-energy-review-seu',
    data: [...filteredSeuItems],
    columns,
    pageSize: 10,
    getRowId: (row) => row.code,
    meta: {
      queryKeys: {
        page: 'energyReviewPage',
        perPage: 'energyReviewPerPage',
        sort: 'energyReviewSort',
        filters: ENERGY_REVIEW_FILTERS_QUERY_KEY,
        joinOperator: ENERGY_REVIEW_JOIN_OPERATOR_QUERY_KEY,
      },
    },
  });


  return (
    <Main className="space-y-6">
      {/* 1. Header */}
      <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
        <div className="space-y-1">
          <div className="flex items-center gap-2">
            
            <Badge variant="outline" className="font-normal gap-1.5 text-xs">
              <span className="size-1.5 rounded-full bg-emerald-500" />
              ISO 50001 EnMS
            </Badge>
          </div>
        </div>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" className="gap-1.5">
            <FileSpreadsheet className="size-3.5" />
            导出评审档案 (PDF/Excel)
          </Button>
          <Button size="sm" className="gap-1.5">
            <ClipboardCheck className="size-3.5" />
            发起年度能源评审
          </Button>
        </div>
      </div>

      {/* 2. 4-Card Fact Strip */}
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-xs font-medium text-muted-foreground">重大用能单元 (SEUs)</CardTitle>
            <Flame className="size-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold tracking-tight text-foreground tabular-nums">3 个系统</div>
            <p className="mt-1 text-xs text-muted-foreground">
              前 3 项累计占全站总耗能 84.7%
            </p>
          </CardContent>
        </Card>

        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-xs font-medium text-muted-foreground">EnPI</CardTitle>
            <TrendingDown className="size-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold tracking-tight text-foreground tabular-nums">
              42.8 <span className="text-sm font-normal text-muted-foreground">kWh/m²</span>
            </div>
            <p className="mt-1 text-xs text-muted-foreground">
              同比基准下降 -5.4%，优于标杆 45.0
            </p>
          </CardContent>
        </Card>

        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-xs font-medium text-muted-foreground">能源基线拟合优度 (R²)</CardTitle>
            <LineChart className="size-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold tracking-tight text-foreground tabular-nums">0.938</div>
            <p className="mt-1 text-xs text-muted-foreground">
              CV-RMSE 7.6% (优于 IPMVP 15% 门限)
            </p>
          </CardContent>
        </Card>

        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-xs font-medium text-muted-foreground">ISO 50001 体系合规性</CardTitle>
            <Award className="size-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold tracking-tight text-foreground">已认证有效</div>
            <p className="mt-1 text-xs text-muted-foreground">
              当前周期: 2026-01-01 ~ 2026-12-31
            </p>
          </CardContent>
        </Card>
      </div>

      {/* 3. Baseline & Relevant Variables Card */}
      <Card className="shadow-xs">
        <CardHeader className="pb-3">
          <div className="flex flex-col gap-1 sm:flex-row sm:items-center sm:justify-between">
            <div>
              <CardTitle className="flex items-center gap-2 text-base">
                <LineChart className="size-4 text-muted-foreground" />
                能耗基线计算模型与关键影响因素
              </CardTitle>
              <CardDescription>
                基于历史运行数据与气象参数校正，计算供冷季基准用电量
              </CardDescription>
            </div>
            <Badge variant="outline" className="font-normal text-xs">
              多元线性回归模型 v2.4
            </Badge>
          </div>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="rounded-lg border bg-muted/20 p-4">
            <div className="text-xs text-muted-foreground font-medium">基准核算方程：</div>
            <div className="mt-2 font-medium text-sm text-foreground tracking-wide tabular-nums">
              E_baseline (kWh/day) = 148.6 × CDD + 34.2 × RH + 1.25 × Occupancy% + 780.0
            </div>
            <div className="mt-3 grid gap-3 sm:grid-cols-4 text-xs">
              <div className="rounded border bg-card p-2.5">
                <span className="text-muted-foreground">CDD (供冷度日数):</span>
                <span className="ml-1 font-semibold text-foreground">基准 18.0°C</span>
              </div>
              <div className="rounded border bg-card p-2.5">
                <span className="text-muted-foreground">RH (室外相对湿度):</span>
                <span className="ml-1 font-semibold text-foreground">60% ~ 85% 动态</span>
              </div>
              <div className="rounded border bg-card p-2.5">
                <span className="text-muted-foreground">Occupancy (人员负荷率):</span>
                <span className="ml-1 font-semibold text-foreground">门禁与照明联动</span>
              </div>
              <div className="rounded border bg-card p-2.5">
                <span className="text-muted-foreground">截距固定损耗:</span>
                <span className="ml-1 font-semibold text-foreground">780 kWh/day 基础能耗</span>
              </div>
            </div>
          </div>
        </CardContent>
      </Card>

      {/* 4. SEU Pareto & Identification Ledger */}
      <section className="space-y-3" aria-labelledby="seu-ledger-title">
        <div className="space-y-3">
          <div>
            <h2 id="seu-ledger-title" className="text-base font-semibold tracking-tight text-foreground">重点用能设备 (SEU) 清单与能耗占比</h2>
            <p className="text-sm text-muted-foreground">梳理站内耗电量大、具备节能改进空间的核心设备系统</p>
          </div>
          <Tabs value={activeTab} onValueChange={setActiveTab}>
            <TabsList className="h-9">
              <TabsTrigger value="SEU">重大用能清单 ({SEU_ITEMS.length})</TabsTrigger>
              <TabsTrigger value="FACTORS">影响变量矩阵 (4)</TabsTrigger>
              <TabsTrigger value="HISTORY">评审档案版本 (3)</TabsTrigger>
            </TabsList>
          </Tabs>
        </div>
        <div>
          {activeTab === 'SEU' && (
            <DataTableBlock aria-label="重点用能设备 SEU 清单">
              <DataTable
              table={table}
              tableAriaLabel="重点用能设备 SEU 清单"
              getHeaderRowProps={() => ({ className: 'bg-muted/30' })}
              getHeaderCellProps={(header) => ({
                className:
                  header.id === 'select' ? 'w-10' :
                  header.id === 'code' ? 'w-[100px]' :
                  header.id === 'cumulative' ? 'w-[160px]' :
                  header.id === 'status' ? 'text-right' :
                  undefined,
              })}
              getRowProps={() => ({ className: 'hover:bg-muted/40' })}
              getCellProps={(cell) => ({
                className: cell.column.id === 'status' ? 'text-right' : undefined,
              })}
              footer={<DataTablePagination table={table} totalRows={filteredSeuItems.length} />}
            >
              <DataTableAdvancedToolbar table={table}>
                <DataTableSortList table={table} />
                <DataTableFilterList table={table} />
              </DataTableAdvancedToolbar>
              </DataTable>
            </DataTableBlock>
          )}

          {activeTab === 'FACTORS' && (
            <div className="rounded-lg border bg-muted/10 p-6 space-y-4">
              <div>
                <h4 className="text-sm font-semibold text-foreground">主要影响变量敏感度分析</h4>
                <p className="text-xs text-muted-foreground">
                  分析室外气温、出勤率等关键因素对用电负荷的影响敏感度
                </p>
              </div>
              <div className="grid gap-3 sm:grid-cols-3 pt-2">
                <div className="rounded-lg border bg-card p-3.5 shadow-xs">
                  <div className="text-xs text-muted-foreground font-medium">室外干球温度 / CDD</div>
                  <div className="mt-1 text-lg font-bold tracking-tight text-foreground tabular-nums">+0.892 <span className="text-xs font-normal text-muted-foreground">(极强正相关)</span></div>
                  <p className="text-xs text-muted-foreground mt-1">每上升 1°C，冷机群即时功率提升约 18.5 kW。</p>
                </div>
                <div className="rounded-lg border bg-card p-3.5 shadow-xs">
                  <div className="text-xs text-muted-foreground font-medium">工作日人员出勤率</div>
                  <div className="mt-1 text-lg font-bold tracking-tight text-foreground tabular-nums">+0.745 <span className="text-xs font-normal text-muted-foreground">(强正相关)</span></div>
                  <p className="text-xs text-muted-foreground mt-1">末端换气与内部散热负荷主要贡献源。</p>
                </div>
                <div className="rounded-lg border bg-card p-3.5 shadow-xs">
                  <div className="text-xs text-muted-foreground font-medium">昼夜温差 ΔT_outdoor</div>
                  <div className="mt-1 text-lg font-bold tracking-tight text-foreground tabular-nums">-0.421 <span className="text-xs font-normal text-muted-foreground">(中度负相关)</span></div>
                  <p className="text-xs text-muted-foreground mt-1">夜间自由冷却与预冷工况可有效削减峰值负荷。</p>
                </div>
              </div>
            </div>
          )}

          {activeTab === 'HISTORY' && (
            <div className="rounded-lg border bg-muted/10 p-6 space-y-4">
              <div>
                <h4 className="text-sm font-semibold text-foreground">历史能源评审档案版本存证</h4>
                <p className="text-xs text-muted-foreground">符合 ISO 50001 体系审计要求的归档版本记录</p>
              </div>
              <div className="space-y-3">
                <div className="flex items-center justify-between rounded-lg border bg-card p-3.5 shadow-xs text-xs">
                  <div>
                    <div className="font-semibold text-foreground">2026 年度能源评审报告 (Rev 2.0)</div>
                    <div className="text-muted-foreground mt-0.5">评审主持: 能源管理负责人 李工 · 评审日期: 2026-01-15</div>
                  </div>
                  <Badge variant="outline" className="font-normal gap-1.5 text-xs">
                    <span className="size-1.5 rounded-full bg-emerald-500" />
                    现行有效版本
                  </Badge>
                </div>
                <div className="flex items-center justify-between rounded-lg border bg-card p-3.5 shadow-xs text-xs text-muted-foreground">
                  <div>
                    <div className="font-medium text-foreground">2025 年度能源评审报告 (Rev 1.2)</div>
                    <div className="text-muted-foreground mt-0.5">归档日期: 2025-01-20 · 历史审计留存</div>
                  </div>
                  <Badge variant="outline" className="font-normal text-xs">已归档</Badge>
                </div>
              </div>
            </div>
          )}
        </div>
      </section>
    </Main>
  );
}
