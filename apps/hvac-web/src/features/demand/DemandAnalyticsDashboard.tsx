import { useMemo, useState } from 'react';
import type { ColumnDef } from '@tanstack/react-table';
import { parseAsStringEnum, useQueryState } from 'nuqs';
import {
  Area,
  AreaChart,
  CartesianGrid,
  Legend,
  Line,
  ReferenceLine,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts';
import {
  ArrowDownRight,
  BatteryCharging,
  Gauge,
  Power,
  TrendingDown,
  Zap,
  Search,
} from 'lucide-react';

import { DataTableBlock } from '@/blocks/data-table';
import { Main } from '@/components/layout/Main';
import type { CurrentPrincipalResponse, Site } from '@/api/generated/platformGateway.gen';
import { siteRoute } from '@/app/router-paths';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs';
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

interface DemandDashboardProps {
  site: Readonly<Site>;
  principal: CurrentPrincipalResponse;
}

interface SheddingCandidate {
  id: string;
  name: string;
  category: string;
  currentPower: number; // kW
  sheddingPotential: number; // kW
  responseTimeMinutes: number;
  constraintDescription: string;
  flexibilityStatus: 'READY' | 'STANDBY' | 'LOCKED';
}

const SHEDDING_CANDIDATES: readonly SheddingCandidate[] = [
  { id: 'ch-01', name: '1# 离心冷水机组', category: '冷水机组', currentPower: 342.5, sheddingPotential: 60.0, responseTimeMinutes: 5, constraintDescription: '允许冷冻水出水设定上调 1.0 °C，持续不超过 60 分钟', flexibilityStatus: 'READY' },
  { id: 'ch-02', name: '2# 离心冷水机组', category: '冷水机组', currentPower: 328.0, sheddingPotential: 55.0, responseTimeMinutes: 5, constraintDescription: '允许出水设定上调 1.0 °C，与 1# 主机交替削峰', flexibilityStatus: 'READY' },
  { id: 'ahu-b1', name: '地下一层商业区 AHU 群组', category: '空调末端', currentPower: 45.0, sheddingPotential: 20.0, responseTimeMinutes: 2, constraintDescription: '变频风机转速下调 15%，维持送风温度稳定', flexibilityStatus: 'READY' },
  { id: 'ahu-1f', name: '首层大堂 AHU-01/02', category: '空调末端', currentPower: 38.0, sheddingPotential: 15.0, responseTimeMinutes: 2, constraintDescription: '大堂高大空间利用热惰性阶段性释能', flexibilityStatus: 'STANDBY' },
  { id: 'cwp-sys', name: '冷却水泵变频群组', category: '水系统', currentPower: 67.8, sheddingPotential: 12.0, responseTimeMinutes: 3, constraintDescription: '受冷却塔逼近度与冷凝压力下限硬约束保护', flexibilityStatus: 'LOCKED' },
];

const DEMAND_CATEGORY_OPTIONS = [...new Set(SHEDDING_CANDIDATES.map((item) => item.category))]
  .map((value) => ({ label: value, value }));
const DEMAND_STATUS_OPTIONS = [
  { label: '就绪可用', value: 'READY' },
  { label: '待命备用', value: 'STANDBY' },
  { label: '约束锁定', value: 'LOCKED' },
] as const;
const DEMAND_FILTER_COLUMN_IDS = ['category', 'flexibilityStatus'] as const;
const DEMAND_FILTERS_QUERY_KEY = 'demandFilters';
const DEMAND_JOIN_OPERATOR_QUERY_KEY = 'demandJoinOperator';

function matchesDemandAdvancedFilter(
  item: SheddingCandidate,
  filter: ExtendedColumnFilter<SheddingCandidate>,
) {
  if (filter.id !== 'category' && filter.id !== 'flexibilityStatus') return true;
  const values = Array.isArray(filter.value) ? filter.value : [filter.value];
  const actual = filter.id === 'category' ? item.category : item.flexibilityStatus;
  switch (filter.operator) {
    case 'eq':
    case 'inArray':
      return values.includes(actual);
    case 'ne':
    case 'notInArray':
      return !values.includes(actual);
    case 'isEmpty':
      return false;
    case 'isNotEmpty':
      return true;
    default:
      return false;
  }
}

const HOURLY_DEMAND = [
  { time: '00:00', power: 280, yesterday: 290, tariff: 'valley' },
  { time: '02:00', power: 265, yesterday: 275, tariff: 'valley' },
  { time: '04:00', power: 270, yesterday: 270, tariff: 'valley' },
  { time: '06:00', power: 340, yesterday: 330, tariff: 'valley' },
  { time: '08:00', power: 620, yesterday: 610, tariff: 'flat' },
  { time: '10:00', power: 840, yesterday: 820, tariff: 'peak' },
  { time: '12:00', power: 945, yesterday: 910, tariff: 'sharp' },
  { time: '14:00', power: 920, yesterday: 890, tariff: 'peak' },
  { time: '16:00', power: 860, yesterday: 840, tariff: 'flat' },
  { time: '18:00', power: 890, yesterday: 880, tariff: 'sharp' },
  { time: '20:00', power: 780, yesterday: 760, tariff: 'peak' },
  { time: '22:00', power: 460, yesterday: 450, tariff: 'flat' },
];

// Load Duration Curve: 720 hours sorted descending
const LDC_DATA = [
  { hours: 0, power: 945, label: '尖峰负荷' },
  { hours: 50, power: 890, label: '尖峰负荷' },
  { hours: 100, power: 830, label: '尖峰负荷' },
  { hours: 200, power: 740, label: '中段负荷' },
  { hours: 300, power: 650, label: '中段负荷' },
  { hours: 400, power: 540, label: '中段负荷' },
  { hours: 500, power: 420, label: '中段负荷' },
  { hours: 600, power: 310, label: '基底负荷' },
  { hours: 720, power: 265, label: '基底负荷' },
];

export function DemandAnalyticsDashboard({ site }: DemandDashboardProps) {
  const [activeTab, setActiveTab] = useState<'today' | 'month'>('today');
  const [searchQuery, setSearchQuery] = useState('');
  const [advancedFilters] = useQueryState(
    DEMAND_FILTERS_QUERY_KEY,
    getFiltersStateParser<SheddingCandidate>([...DEMAND_FILTER_COLUMN_IDS]).withDefault([]),
  );
  const [joinOperator] = useQueryState(
    DEMAND_JOIN_OPERATOR_QUERY_KEY,
    parseAsStringEnum<JoinOperator>(['and', 'or']).withDefault('and'),
  );
  const filteredCandidates = useMemo(() => {
    return SHEDDING_CANDIDATES.filter((item) => {
      const q = searchQuery.trim().toLowerCase();
      const matchesSearch = !q || item.name.toLowerCase().includes(q) || item.id.toLowerCase().includes(q);
      const filterMatches = advancedFilters.map((filter) => matchesDemandAdvancedFilter(item, filter));
      const matchesAdvancedFilters = advancedFilters.length === 0 || (joinOperator === 'or'
        ? filterMatches.some(Boolean)
        : filterMatches.every(Boolean));
      return matchesSearch && matchesAdvancedFilters;
    });
  }, [advancedFilters, joinOperator, searchQuery]);

  const columns = useMemo<Array<ColumnDef<DataTableFeatures, SheddingCandidate>>>(() => [
    {
      id: 'select',
      header: ({ table }) => (
        <Checkbox
          checked={table.getIsAllPageRowsSelected() ? true : table.getIsSomePageRowsSelected() ? 'indeterminate' : false}
          onCheckedChange={(checked) => table.toggleAllPageRowsSelected(Boolean(checked))}
          aria-label="全选柔性资源"
        />
      ),
      cell: ({ row }) => (
        <Checkbox
          checked={row.getIsSelected()}
          onCheckedChange={(checked) => row.toggleSelected(Boolean(checked))}
          aria-label={`选择 ${row.original.name}`}
        />
      ),
      enableSorting: false,
      enableHiding: false,
    },
    {
      id: 'name',
      accessorFn: (row) => row.name,
      meta: { label: '设备 / 资源名称' },
      header: '设备 / 资源名称',
      cell: ({ row }) => <div className="font-medium text-foreground"><div>{row.original.name}</div><div className="font-mono text-[10px] text-muted-foreground">{row.original.id}</div></div>,
    },
    { id: 'category', accessorFn: (row) => row.category, enableColumnFilter: true, meta: { label: '子系统', variant: 'select', options: DEMAND_CATEGORY_OPTIONS }, header: '子系统', cell: ({ row }) => <Badge variant="outline" className="text-xs font-normal">{row.original.category}</Badge> },
    { id: 'currentPower', accessorFn: (row) => row.currentPower, meta: { label: '当前运行功率' }, header: '当前运行功率', cell: ({ row }) => <span className="font-mono font-medium text-foreground tabular-nums">{row.original.currentPower.toFixed(1)} kW</span> },
    { id: 'sheddingPotential', accessorFn: (row) => row.sheddingPotential, meta: { label: '可削峰容量' }, header: '可削峰容量 (kW)', cell: ({ row }) => <span className="font-mono font-semibold text-foreground tabular-nums">{row.original.sheddingPotential.toFixed(1)} kW</span> },
    { id: 'responseTime', accessorFn: (row) => row.responseTimeMinutes, meta: { label: '响应时延' }, header: '响应时延', cell: ({ row }) => <span className="font-mono tabular-nums">≤ {row.original.responseTimeMinutes} 分钟</span> },
    { id: 'constraint', header: '调控约束与安全边界', cell: ({ row }) => <span className="text-xs text-muted-foreground">{row.original.constraintDescription}</span> },
    { id: 'flexibilityStatus', accessorFn: (row) => row.flexibilityStatus, enableColumnFilter: true, meta: { label: '就绪状态', variant: 'select', options: [...DEMAND_STATUS_OPTIONS] }, header: '就绪状态', cell: ({ row }) => <StatusBadge label={row.original.flexibilityStatus === 'READY' ? '就绪可用' : row.original.flexibilityStatus === 'STANDBY' ? '待命备用' : '约束锁定'} tone={row.original.flexibilityStatus === 'READY' ? 'success' : row.original.flexibilityStatus === 'STANDBY' ? 'info' : 'warning'} /> },
    {
      id: 'action',
      header: '联动',
      cell: () => <Button variant="ghost" size="sm" asChild className="h-7 px-2 text-xs"><a href={siteRoute(site, 'control')}>控制联动</a></Button>,
      enableSorting: false,
    },
  ], [site]);

  const table = useDataTable({
    key: 'surface-15-demand-flexibility',
    data: [...filteredCandidates],
    columns,
    pageSize: 10,
    getRowId: (row) => row.id,
    meta: {
      queryKeys: {
        page: 'demandPage',
        perPage: 'demandPerPage',
        sort: 'demandSort',
        filters: DEMAND_FILTERS_QUERY_KEY,
        joinOperator: DEMAND_JOIN_OPERATOR_QUERY_KEY,
      },
    },
  });


  return (
    <Main className="space-y-6">
      {/* 1. Page Header */}
      <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <div className="flex items-center gap-2">
            
            <Badge variant="outline" className="font-normal gap-1.5 text-xs">
              <span className="size-1.5 rounded-full bg-emerald-500" />
              需量控制与柔性
            </Badge>
          </div>
        </div>
        <div className="flex items-center gap-2">
          <Tabs value={activeTab} onValueChange={(val) => setActiveTab(val as any)}>
            <TabsList className="bg-muted/60 h-9 p-1">
              <TabsTrigger value="today" className="text-xs">今日实时</TabsTrigger>
              <TabsTrigger value="month" className="text-xs">当月统计</TabsTrigger>
            </TabsList>
          </Tabs>
          <Button variant="outline" size="sm" asChild>
            <a href={siteRoute(site, 'cost')}>
              <Zap className="mr-1.5 size-3.5" />
              电价与成本
            </a>
          </Button>
        </div>
      </div>

      {/* 2. 4-Card Fact Strip */}
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-xs font-medium text-muted-foreground">最大需量</CardTitle>
            <Gauge className="size-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="flex items-baseline gap-2">
              <div className="text-2xl font-bold tracking-tight text-foreground tabular-nums">
                945.0 <span className="text-sm font-normal text-muted-foreground">kW</span>
              </div>
              <Badge variant="outline" className="font-normal gap-1 text-[10px]">
                <span className="size-1 rounded-full bg-emerald-500" />
                未超需量
              </Badge>
            </div>
            <p className="text-xs text-muted-foreground mt-1">申报容量 1,200 kVA · 发生于 09-15 12:15</p>
          </CardContent>
        </Card>

        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-xs font-medium text-muted-foreground">安全余量</CardTitle>
            <BatteryCharging className="size-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="flex items-baseline gap-2">
              <div className="text-2xl font-bold tracking-tight text-foreground tabular-nums">
                255.0 <span className="text-sm font-normal text-muted-foreground">kW</span>
              </div>
              <span className="text-xs text-muted-foreground tabular-nums">(21.2% 余量)</span>
            </div>
            <p className="text-xs text-muted-foreground mt-1">预警警戒线 1,080 kW · 安全运行中</p>
          </CardContent>
        </Card>

        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-xs font-medium text-muted-foreground">尖峰负荷</CardTitle>
            <Power className="size-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="flex items-baseline gap-2">
              <div className="text-2xl font-bold tracking-tight text-foreground tabular-nums">
                917.5 <span className="text-sm font-normal text-muted-foreground">kW</span>
              </div>
              <span className="flex items-center text-xs font-medium tabular-nums text-muted-foreground">
                <ArrowDownRight className="size-3.5 mr-0.5" />
                -15 kW
              </span>
            </div>
            <p className="text-xs text-muted-foreground mt-1">尖峰电价时段 (11:00-13:00 / 18:00-20:00)</p>
          </CardContent>
        </Card>

        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-xs font-medium text-muted-foreground">可调负荷</CardTitle>
            <TrendingDown className="size-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="flex items-baseline gap-2">
              <div className="text-2xl font-bold tracking-tight text-foreground tabular-nums">
                162.0 <span className="text-sm font-normal text-muted-foreground">kW</span>
              </div>
              <span className="text-xs text-muted-foreground">响应时延 ≤ 5 min</span>
            </div>
            <p className="text-xs text-muted-foreground mt-1">冷机温升 + 末端风机调速可快速响应</p>
          </CardContent>
        </Card>
      </div>

      {/* 3. Charts Row: Daily Load Shape & Load Duration Curve */}
      <div className="grid gap-6 lg:grid-cols-2">
        <Card className="shadow-xs">
          <CardHeader className="pb-2">
            <div className="flex items-center justify-between">
              <div>
                <CardTitle className="text-base">今日负荷</CardTitle>
                <CardDescription>与昨日对比</CardDescription>
              </div>
              <Badge variant="outline" className="font-normal text-xs">警戒线 1,080 kW</Badge>
            </div>
          </CardHeader>
          <CardContent>
            <div className="h-[280px]">
              <ResponsiveContainer width="100%" height="100%">
                <AreaChart data={HOURLY_DEMAND} margin={{ top: 20, right: 20, bottom: 10, left: 10 }}>
                  <defs>
                    <linearGradient id="powerGrad" x1="0" y1="0" x2="0" y2="1">
                      <stop offset="5%" stopColor="#2563eb" stopOpacity={0.35}/>
                      <stop offset="95%" stopColor="#2563eb" stopOpacity={0.0}/>
                    </linearGradient>
                  </defs>
                  <CartesianGrid strokeDasharray="3 3" stroke="#e4e4e7" strokeOpacity={0.6} />
                  <XAxis dataKey="time" tick={{ fontSize: 12, fill: '#71717a' }} />
                  <YAxis domain={[200, 1200]} tick={{ fontSize: 12, fill: '#71717a' }} unit=" kW" />
                  <Tooltip />
                  <Legend />
                  <ReferenceLine y={1080} stroke="#e11d48" strokeDasharray="4 4" label={{ value: '需量报警阈值 (1080 kW)', fill: '#e11d48', fontSize: 11 }} />
                  <ReferenceLine y={1200} stroke="#9ca3af" strokeDasharray="2 2" label={{ value: '申报容量 (1200 kVA)', fill: '#6b7280', fontSize: 11 }} />
                  <Area type="monotone" dataKey="power" name="今日实时负荷 (kW)" stroke="#2563eb" strokeWidth={2} fillOpacity={1} fill="url(#powerGrad)" />
                  <Line type="monotone" dataKey="yesterday" name="昨日负荷基准 (kW)" stroke="#94a3b8" strokeDasharray="3 3" dot={false} />
                </AreaChart>
              </ResponsiveContainer>
            </div>
          </CardContent>
        </Card>

        <Card className="shadow-xs">
          <CardHeader className="pb-2">
            <div className="flex items-center justify-between">
              <div>
                <CardTitle className="text-base">本月负荷持续曲线</CardTitle>
                
              </div>
              <Badge variant="outline" className="font-normal text-xs">720 小时统计</Badge>
            </div>
          </CardHeader>
          <CardContent>
            <div className="h-[280px]">
              <ResponsiveContainer width="100%" height="100%">
                <AreaChart data={LDC_DATA} margin={{ top: 20, right: 20, bottom: 10, left: 10 }}>
                  <defs>
                    <linearGradient id="ldcGrad" x1="0" y1="0" x2="0" y2="1">
                      <stop offset="5%" stopColor="#0ea5e9" stopOpacity={0.35}/>
                      <stop offset="95%" stopColor="#0ea5e9" stopOpacity={0.0}/>
                    </linearGradient>
                  </defs>
                  <CartesianGrid strokeDasharray="3 3" stroke="#e4e4e7" strokeOpacity={0.6} />
                  <XAxis dataKey="hours" tick={{ fontSize: 12, fill: '#71717a' }} unit="h" />
                  <YAxis domain={[200, 1000]} tick={{ fontSize: 12, fill: '#71717a' }} unit=" kW" />
                  <Tooltip />
                  <Legend />
                  <ReferenceLine x={100} stroke="#f59e0b" strokeDasharray="3 3" label={{ value: '尖峰带 (≤100h)', fill: '#f59e0b', fontSize: 11 }} />
                  <ReferenceLine x={500} stroke="#10b981" strokeDasharray="3 3" label={{ value: '基底带 (≥500h)', fill: '#10b981', fontSize: 11 }} />
                  <Area type="monotone" dataKey="power" name="负荷持续功率 (kW)" stroke="#0ea5e9" strokeWidth={2} fillOpacity={1} fill="url(#ldcGrad)" />
                </AreaChart>
              </ResponsiveContainer>
            </div>
          </CardContent>
        </Card>
      </div>

      {/* 4. Flexibility & Shedding Table */}
      <DataTableBlock
        title="可调负荷"
        actions={<div className="text-xs text-muted-foreground">总可用削峰潜力：<strong className="font-mono text-sm font-semibold tabular-nums text-foreground">162.0 kW</strong></div>}
      >
        <DataTable
          table={table}
          tableAriaLabel="可调负荷"
          empty="未找到匹配的柔性调控资源"
          getHeaderRowProps={() => ({ className: 'bg-muted/30' })}
          getHeaderCellProps={(header) => ({
            className:
              header.id === 'select' ? 'w-[40px]' :
              header.id === 'name' ? 'min-w-[200px]' :
              header.id === 'category' || header.id === 'currentPower' || header.id === 'flexibilityStatus' ? 'w-[120px]' :
              header.id === 'sheddingPotential' ? 'w-[140px]' :
              header.id === 'responseTime' ? 'w-[110px] text-center' :
              header.id === 'constraint' ? 'min-w-[260px]' :
              header.id === 'action' ? 'w-[100px] text-right' :
              undefined,
          })}
          getRowProps={() => ({ className: 'hover:bg-muted/40' })}
          getCellProps={(cell) => ({
            className:
              cell.column.id === 'currentPower' || cell.column.id === 'sheddingPotential' || cell.column.id === 'action'
                ? 'text-right'
                : cell.column.id === 'responseTime' || cell.column.id === 'status'
                  ? 'text-center'
                  : undefined,
          })}
          footer={(
            <DataTablePagination table={table} totalRows={filteredCandidates.length} />
          )}
        >
          <DataTableAdvancedToolbar table={table}>
            <div className="relative">
              <Search className="absolute left-2.5 top-2.5 h-3.5 w-3.5 text-muted-foreground" />
              <Input
                placeholder="搜索资源名称或编号..."
                value={searchQuery}
                onChange={(e) => {
                  setSearchQuery(e.target.value);
                  table.setPageIndex(0);
                }}
                className="h-8 w-[180px] pl-8 text-xs sm:w-[220px]"
              />
            </div>
            <DataTableSortList table={table} />
            <DataTableFilterList table={table} />
          </DataTableAdvancedToolbar>
        </DataTable>
      </DataTableBlock>
    </Main>
  );
}
