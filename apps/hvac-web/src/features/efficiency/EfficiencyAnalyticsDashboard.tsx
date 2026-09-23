import { useMemo, useState } from 'react';
import type { ColumnDef } from '@tanstack/react-table';
import { parseAsStringEnum, useQueryState } from 'nuqs';
import {
  Area,
  AreaChart,
  CartesianGrid,
  Legend,
  Line,
  ResponsiveContainer,
  Scatter,
  ScatterChart,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts';
import {
  ArrowDownRight,
  ArrowUpRight,
  Droplets,
  Flame,
  Gauge,
  Wind,
  Zap,
  Search,
} from 'lucide-react';

import { DataTableBlock } from '@/blocks/data-table';
import { Main } from '@/components/layout/Main';
import type { CurrentPrincipalResponse, Site } from '@/api/generated/platformGateway.gen';
import { siteRoute } from '@/app/router-paths';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
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

interface EfficiencyDashboardProps {
  site: Readonly<Site>;
  principal: CurrentPrincipalResponse;
}

interface EquipmentEfficiencyItem {
  id: string;
  name: string;
  category: 'CHILLER' | 'CHW_PUMP' | 'CW_PUMP' | 'TOWER';
  currentPower: number; // kW
  loadPercent: number; // %
  metricValue: number; // COP or kW/RT or Hz
  metricUnit: string;
  metricLabel: string;
  baselineDiff: number; // % compared to baseline
  status: 'OPTIMAL' | 'NORMAL' | 'WARNING';
}

const EQUIPMENT_DATA: readonly EquipmentEfficiencyItem[] = [
  { id: 'dev-ch-01', name: '1# 离心冷水机组', category: 'CHILLER', currentPower: 342.5, loadPercent: 78, metricValue: 5.42, metricUnit: 'COP', metricLabel: '实时 COP', baselineDiff: 3.2, status: 'OPTIMAL' },
  { id: 'dev-ch-02', name: '2# 离心冷水机组', category: 'CHILLER', currentPower: 328.0, loadPercent: 74, metricValue: 5.35, metricUnit: 'COP', metricLabel: '实时 COP', baselineDiff: 1.8, status: 'OPTIMAL' },
  { id: 'dev-ch-03', name: '3# 变频螺杆冷水机组', category: 'CHILLER', currentPower: 145.2, loadPercent: 42, metricValue: 4.15, metricUnit: 'COP', metricLabel: '实时 COP', baselineDiff: -6.4, status: 'WARNING' },
  { id: 'dev-chwp-01', name: '1# 一次冷冻水泵', category: 'CHW_PUMP', currentPower: 28.4, loadPercent: 82, metricValue: 43.5, metricUnit: 'Hz', metricLabel: '运行频率', baselineDiff: 0.5, status: 'NORMAL' },
  { id: 'dev-chwp-02', name: '2# 一次冷冻水泵', category: 'CHW_PUMP', currentPower: 27.8, loadPercent: 80, metricValue: 42.8, metricUnit: 'Hz', metricLabel: '运行频率', baselineDiff: 0.2, status: 'NORMAL' },
  { id: 'dev-cwp-01', name: '1# 冷却水泵', category: 'CW_PUMP', currentPower: 34.2, loadPercent: 85, metricValue: 45.0, metricUnit: 'Hz', metricLabel: '运行频率', baselineDiff: 2.1, status: 'NORMAL' },
  { id: 'dev-cwp-02', name: '2# 冷却水泵', category: 'CW_PUMP', currentPower: 33.6, loadPercent: 84, metricValue: 44.5, metricUnit: 'Hz', metricLabel: '运行频率', baselineDiff: 1.6, status: 'NORMAL' },
  { id: 'dev-ct-01', name: '1# 冷却塔风机', category: 'TOWER', currentPower: 11.2, loadPercent: 70, metricValue: 38.0, metricUnit: 'Hz', metricLabel: '风机频率', baselineDiff: -1.2, status: 'NORMAL' },
  { id: 'dev-ct-02', name: '2# 冷却塔风机', category: 'TOWER', currentPower: 11.0, loadPercent: 70, metricValue: 38.0, metricUnit: 'Hz', metricLabel: '风机频率', baselineDiff: -1.4, status: 'NORMAL' },
];

const SCATTER_DATA = [
  { load: 35, cop: 4.12 },
  { load: 38, cop: 4.25 },
  { load: 42, cop: 4.38 },
  { load: 48, cop: 4.65 },
  { load: 52, cop: 4.88 },
  { load: 58, cop: 5.15 },
  { load: 64, cop: 5.38 },
  { load: 68, cop: 5.48 },
  { load: 72, cop: 5.52 },
  { load: 75, cop: 5.56 },
  { load: 78, cop: 5.42 },
  { load: 82, cop: 5.35 },
  { load: 85, cop: 5.22 },
  { load: 88, cop: 5.08 },
  { load: 92, cop: 4.85 },
  { load: 95, cop: 4.62 },
  { load: 98, cop: 4.45 },
];

const HOURLY_DATA = [
  { time: '00:00', cop: 4.15, wetBulb: 21.5 },
  { time: '02:00', cop: 4.18, wetBulb: 21.2 },
  { time: '04:00', cop: 4.20, wetBulb: 21.0 },
  { time: '06:00', cop: 4.35, wetBulb: 21.8 },
  { time: '08:00', cop: 4.75, wetBulb: 23.5 },
  { time: '10:00', cop: 4.82, wetBulb: 25.2 },
  { time: '12:00', cop: 4.65, wetBulb: 26.8 },
  { time: '14:00', cop: 4.58, wetBulb: 27.4 },
  { time: '16:00', cop: 4.70, wetBulb: 26.5 },
  { time: '18:00', cop: 4.80, wetBulb: 24.8 },
  { time: '20:00', cop: 4.55, wetBulb: 23.2 },
  { time: '22:00', cop: 4.30, wetBulb: 22.0 },
];

const EFFICIENCY_STATUS_OPTIONS = [
  { label: '能效优良', value: 'OPTIMAL' },
  { label: '工况正常', value: 'NORMAL' },
  { label: '低效偏离', value: 'WARNING' },
] as const;
const EFFICIENCY_CATEGORY_OPTIONS = [
  { label: '冷水机组', value: 'CHILLER' },
  { label: '冷冻水泵', value: 'CHW_PUMP' },
  { label: '冷却水泵', value: 'CW_PUMP' },
  { label: '冷却塔', value: 'TOWER' },
] as const;
const EFFICIENCY_FILTER_COLUMN_IDS = ['category', 'status'] as const;
const EFFICIENCY_FILTERS_QUERY_KEY = 'efficiencyFilters';
const EFFICIENCY_JOIN_OPERATOR_QUERY_KEY = 'efficiencyJoinOperator';

function matchesEfficiencyAdvancedFilter(
  item: EquipmentEfficiencyItem,
  filter: ExtendedColumnFilter<EquipmentEfficiencyItem>,
) {
  if (filter.id !== 'status' && filter.id !== 'category') return true;
  const values = Array.isArray(filter.value) ? filter.value : [filter.value];
  const actual = filter.id === 'status' ? item.status : item.category;
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

export function EfficiencyAnalyticsDashboard({ site }: EfficiencyDashboardProps) {
  const [timeView, setTimeView] = useState<'today' | 'week' | 'month'>('today');
  const [searchQuery, setSearchQuery] = useState('');
  const [advancedFilters] = useQueryState(
    EFFICIENCY_FILTERS_QUERY_KEY,
    getFiltersStateParser<EquipmentEfficiencyItem>([...EFFICIENCY_FILTER_COLUMN_IDS]).withDefault([]),
  );
  const [joinOperator] = useQueryState(
    EFFICIENCY_JOIN_OPERATOR_QUERY_KEY,
    parseAsStringEnum<JoinOperator>(['and', 'or']).withDefault('and'),
  );
  const filteredEquipment = useMemo(() => {
    return EQUIPMENT_DATA.filter((item) => {
      const q = searchQuery.trim().toLowerCase();
      const matchesSearch = !q || item.name.toLowerCase().includes(q) || item.id.toLowerCase().includes(q);
      const filterMatches = advancedFilters.map((filter) => matchesEfficiencyAdvancedFilter(item, filter));
      const matchesAdvancedFilters = advancedFilters.length === 0 || (joinOperator === 'or'
        ? filterMatches.some(Boolean)
        : filterMatches.every(Boolean));
      return matchesSearch && matchesAdvancedFilters;
    });
  }, [advancedFilters, joinOperator, searchQuery]);

  const columns = useMemo<Array<ColumnDef<DataTableFeatures, EquipmentEfficiencyItem>>>(() => [
    {
      id: 'select',
      header: ({ table }) => (
        <Checkbox
          checked={table.getIsAllPageRowsSelected() ? true : table.getIsSomePageRowsSelected() ? 'indeterminate' : false}
          onCheckedChange={(checked) => table.toggleAllPageRowsSelected(Boolean(checked))}
          aria-label="全选能效设备"
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
      meta: { label: '设备名称' },
      header: '设备名称',
      cell: ({ row }) => (
        <div className="font-medium text-foreground">
          <div>{row.original.name}</div>
          <div className="font-mono text-[10px] text-muted-foreground">{row.original.id}</div>
        </div>
      ),
    },
    {
      id: 'category',
      accessorFn: (row) => row.category,
      enableColumnFilter: true,
      meta: { label: '设备类型', variant: 'select', options: [...EFFICIENCY_CATEGORY_OPTIONS] },
      header: '设备类型',
      cell: ({ row }) => (
        <Badge variant="outline" className="text-xs font-normal">
          {row.original.category === 'CHILLER'
            ? '冷水主机'
            : row.original.category === 'CHW_PUMP'
              ? '冷冻水泵'
              : row.original.category === 'CW_PUMP'
                ? '冷却水泵'
                : '冷却塔'}
        </Badge>
      ),
    },
    {
      id: 'currentPower',
      accessorFn: (row) => row.currentPower,
      meta: { label: '实时功率' },
      header: '实时功率',
      cell: ({ row }) => <span className="font-mono font-medium text-foreground tabular-nums">{row.original.currentPower.toFixed(1)} kW</span>,
    },
    {
      id: 'loadPercent',
      accessorFn: (row) => row.loadPercent,
      meta: { label: '负荷率' },
      header: '负荷率',
      cell: ({ row }) => <span className="font-mono tabular-nums">{row.original.loadPercent}%</span>,
    },
    {
      id: 'metric',
      accessorFn: (row) => row.metricValue,
      meta: { label: '当前能效/频率' },
      header: '当前能效/频率',
      cell: ({ row }) => (
        <span className="font-mono font-semibold text-foreground tabular-nums">
          {row.original.metricValue.toFixed(2)} <span className="text-xs font-normal text-muted-foreground">{row.original.metricUnit}</span>
        </span>
      ),
    },
    {
      id: 'baselineDiff',
      accessorFn: (row) => row.baselineDiff,
      meta: { label: '基准偏差' },
      header: '基准偏差',
      cell: ({ row }) => (
        <span className={`inline-flex items-center text-xs font-mono font-medium tabular-nums ${row.original.baselineDiff >= 0 ? 'text-emerald-600' : 'text-destructive'}`}>
          {row.original.baselineDiff >= 0 ? <ArrowUpRight className="mr-0.5 size-3.5" /> : <ArrowDownRight className="mr-0.5 size-3.5" />}
          {row.original.baselineDiff >= 0 ? `+${row.original.baselineDiff}%` : `${row.original.baselineDiff}%`}
        </span>
      ),
    },
    {
      id: 'status',
      accessorFn: (row) => row.status,
      enableColumnFilter: true,
      meta: { label: '运行工况', variant: 'select', options: [...EFFICIENCY_STATUS_OPTIONS] },
      header: '运行工况',
      cell: ({ row }) => (
        <StatusBadge
          label={row.original.status === 'OPTIMAL' ? '能效优良' : row.original.status === 'NORMAL' ? '工况正常' : '低效偏离'}
          tone={row.original.status === 'OPTIMAL' ? 'success' : row.original.status === 'NORMAL' ? 'neutral' : 'destructive'}
        />
      ),
    },
    {
      id: 'action',
      header: '操作',
      cell: ({ row }) => (
        <Button variant="ghost" size="sm" asChild className="h-7 px-2 text-xs">
          <a href={`${siteRoute(site, 'trends')}?device=${encodeURIComponent(row.original.id)}`}>查看时序</a>
        </Button>
      ),
      enableSorting: false,
    },
  ], [site]);

  const table = useDataTable({
    key: 'surface-16-efficiency-equipment',
    data: [...filteredEquipment],
    columns,
    pageSize: 10,
    getRowId: (row) => row.id,
    meta: {
      queryKeys: {
        page: 'efficiencyPage',
        perPage: 'efficiencyPerPage',
        sort: 'efficiencySort',
        filters: EFFICIENCY_FILTERS_QUERY_KEY,
        joinOperator: EFFICIENCY_JOIN_OPERATOR_QUERY_KEY,
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
              冷站综合能效
            </Badge>
          </div>
        </div>
        <div className="flex items-center gap-2">
          <Tabs value={timeView} onValueChange={(val) => setTimeView(val as any)}>
            <TabsList className="bg-muted/60 h-9 p-1">
              <TabsTrigger value="today" className="text-xs">今日</TabsTrigger>
              <TabsTrigger value="week" className="text-xs">近 7 天</TabsTrigger>
              <TabsTrigger value="month" className="text-xs">本月</TabsTrigger>
            </TabsList>
          </Tabs>
          <Button variant="outline" size="sm" asChild>
            <a href={siteRoute(site, 'opportunities')}>
              <Flame className="mr-1.5 size-3.5" />
              节能机会
            </a>
          </Button>
        </div>
      </div>

      {/* 2. 4-Card Fact Strip */}
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-xs font-medium text-muted-foreground">系统 COP</CardTitle>
            <Gauge className="size-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="flex items-baseline gap-2">
              <div className="text-2xl font-bold tracking-tight text-foreground tabular-nums">4.68</div>
              <span className="flex items-center text-xs font-medium tabular-nums text-emerald-600 dark:text-emerald-400">
                <ArrowUpRight className="size-3.5" />
                +4.0%
              </span>
            </div>
            <p className="text-xs text-muted-foreground mt-1">当日均值 4.42 · 标杆基准 4.50</p>
          </CardContent>
        </Card>

        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-xs font-medium text-muted-foreground">主机 COP</CardTitle>
            <Zap className="size-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="flex items-baseline gap-2">
              <div className="text-2xl font-bold tracking-tight text-foreground tabular-nums">
                0.62 <span className="text-sm font-normal text-muted-foreground">kW/RT</span>
              </div>
              <span className="flex items-center text-xs font-medium tabular-nums text-emerald-600 dark:text-emerald-400">
                <ArrowDownRight className="size-3.5" />
                -2.1%
              </span>
            </div>
            <p className="text-xs text-muted-foreground mt-1">额定指标 0.58 · 当前主机负荷率 76%</p>
          </CardContent>
        </Card>

        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-xs font-medium text-muted-foreground">冷冻水输配系数</CardTitle>
            <Droplets className="size-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="flex items-baseline gap-2">
              <div className="text-2xl font-bold tracking-tight text-foreground tabular-nums">36.5</div>
              <Badge variant="outline" className="font-normal gap-1 text-[10px]">
                <span className="size-1 rounded-full bg-emerald-500" />
                优于标准
              </Badge>
            </div>
            <p className="text-xs text-muted-foreground mt-1">供回水温差 ΔT: 4.8 °C (设计 5.0 °C)</p>
          </CardContent>
        </Card>

        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-xs font-medium text-muted-foreground">冷却塔逼近度</CardTitle>
            <Wind className="size-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="flex items-baseline gap-2">
              <div className="text-2xl font-bold tracking-tight text-foreground tabular-nums">
                3.2 <span className="text-sm font-normal text-muted-foreground">°C</span>
              </div>
              <span className="text-xs text-muted-foreground">出水 28.2 °C</span>
            </div>
            <p className="text-xs text-muted-foreground mt-1">室外湿球温度 25.0 °C · 冷却回水温差 4.2 °C</p>
          </CardContent>
        </Card>
      </div>

      {/* 3. Charts Row: Load vs COP Scatter & Hourly Trend */}
      <div className="grid gap-6 lg:grid-cols-2">
        <Card className="shadow-xs">
          <CardHeader className="pb-2">
            <div className="flex items-center justify-between">
              <div>
                <CardTitle className="text-base">冷机负荷与 COP</CardTitle>
                
              </div>
              <Badge variant="outline" className="font-normal text-xs">最佳区间 65%~85%</Badge>
            </div>
          </CardHeader>
          <CardContent>
            <div className="h-[280px]">
              <ResponsiveContainer width="100%" height="100%">
                <ScatterChart margin={{ top: 20, right: 20, bottom: 10, left: 10 }}>
                  <CartesianGrid strokeDasharray="3 3" stroke="#e4e4e7" strokeOpacity={0.6} />
                  <XAxis type="number" dataKey="load" name="冷负荷率" unit="%" domain={[30, 100]} tick={{ fontSize: 12, fill: '#71717a' }} />
                  <YAxis type="number" dataKey="cop" name="COP" domain={[3.5, 6.0]} tick={{ fontSize: 12, fill: '#71717a' }} />
                  <Tooltip cursor={{ strokeDasharray: '3 3' }} formatter={(val: any, name: any) => [val, name === 'cop' ? '实测 COP' : '负荷率']} />
                  <Scatter name="实测采样点" data={SCATTER_DATA} fill="#2563eb" />
                </ScatterChart>
              </ResponsiveContainer>
            </div>
          </CardContent>
        </Card>

        <Card className="shadow-xs">
          <CardHeader className="pb-2">
            <div className="flex items-center justify-between">
              <div>
                <CardTitle className="text-base">系统 COP 与湿球温度</CardTitle>
                
              </div>
              <Badge variant="outline" className="font-normal text-xs">连续时序采样</Badge>
            </div>
          </CardHeader>
          <CardContent>
            <div className="h-[280px]">
              <ResponsiveContainer width="100%" height="100%">
                <AreaChart data={HOURLY_DATA} margin={{ top: 20, right: 20, bottom: 10, left: 10 }}>
                  <defs>
                    <linearGradient id="copGrad" x1="0" y1="0" x2="0" y2="1">
                      <stop offset="5%" stopColor="#2563eb" stopOpacity={0.3}/>
                      <stop offset="95%" stopColor="#2563eb" stopOpacity={0.0}/>
                    </linearGradient>
                  </defs>
                  <CartesianGrid strokeDasharray="3 3" stroke="#e4e4e7" strokeOpacity={0.6} />
                  <XAxis dataKey="time" tick={{ fontSize: 12, fill: '#71717a' }} />
                  <YAxis yAxisId="cop" domain={[3.5, 5.5]} tick={{ fontSize: 12, fill: '#71717a' }} />
                  <YAxis yAxisId="wb" orientation="right" domain={[15, 32]} tick={{ fontSize: 12, fill: '#f59e0b' }} unit="°C" />
                  <Tooltip />
                  <Legend />
                  <Area yAxisId="cop" type="monotone" dataKey="cop" name="冷站综合 COP" stroke="#2563eb" fillOpacity={1} fill="url(#copGrad)" />
                  <Line yAxisId="wb" type="monotone" dataKey="wetBulb" name="室外湿球温度 (°C)" stroke="#f59e0b" strokeDasharray="4 4" dot={false} />
                </AreaChart>
              </ResponsiveContainer>
            </div>
          </CardContent>
        </Card>
      </div>

      {/* 4. Equipment Efficiency & Outliers Table */}
      <DataTableBlock
        title="设备能效"
      >
        <DataTable
          table={table}
          tableAriaLabel="设备能效"
          empty="未找到匹配的冷站能效设备"
          getHeaderRowProps={() => ({ className: 'bg-muted/30' })}
          getHeaderCellProps={(header) => ({
            className:
              header.id === 'select' ? 'w-[40px]' :
              header.id === 'name' ? 'min-w-[220px]' :
              header.id === 'category' || header.id === 'currentPower' || header.id === 'loadPercent' || header.id === 'status' ? 'w-[120px]' :
              header.id === 'metric' ? 'w-[140px]' :
              header.id === 'baselineDiff' ? 'w-[130px]' :
              header.id === 'action' ? 'w-[100px]' :
              undefined,
          })}
          getRowProps={() => ({ className: 'hover:bg-muted/40' })}
          getCellProps={(cell) => ({
            className:
              cell.column.id === 'currentPower' || cell.column.id === 'loadPercent' || cell.column.id === 'metric' || cell.column.id === 'baselineDiff' || cell.column.id === 'action'
                ? 'text-right'
                : cell.column.id === 'status'
                  ? 'text-center'
                  : undefined,
          })}
          footer={<DataTablePagination table={table} totalRows={filteredEquipment.length} />}
        >
          <DataTableAdvancedToolbar table={table}>
            <div className="relative">
              <Search className="absolute left-2.5 top-2.5 h-3.5 w-3.5 text-muted-foreground" />
              <Input
                placeholder="搜索设备名称或编号..."
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
