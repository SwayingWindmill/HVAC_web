import { useMemo, useState } from 'react';
import type { ColumnDef } from '@tanstack/react-table';
import { parseAsStringEnum, useQueryState } from 'nuqs';
import {
  Award,
  BarChart,
  Building2,
  Globe,
  MapPin,
  RefreshCw,
  Search,
} from 'lucide-react';
import { DataTableBlock } from '@/blocks/data-table';
import { Main } from '@/components/layout/Main';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
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

export interface SiteBenchmarkingWorkspaceProps {
  readonly siteId: string;
}

type BenchmarkTier = 'BENCHMARK' | 'ADVANCED' | 'COMPLIANT' | 'LAGGING';

interface BenchmarkedSite {
  readonly rank: number;
  readonly id: string;
  readonly name: string;
  readonly city: string;
  readonly buildingType: string;
  readonly grossAreaM2: number;
  readonly systemCop: number;
  readonly rawEuiKWhPerM2: number;
  readonly normalizedEuiKWhPerM2: number;
  readonly tier: BenchmarkTier;
  readonly savingPotentialYuan: number;
  readonly priority: 'HIGH' | 'MEDIUM' | 'LOW';
  readonly isCurrentSite: boolean;
}

const BENCHMARKED_SITES: readonly BenchmarkedSite[] = [
  {
    rank: 1,
    id: 'site-tk-02',
    name: '丸之内金融中心 A座',
    city: '东京',
    buildingType: '甲级商办综合体',
    grossAreaM2: 65000,
    systemCop: 5.12,
    rawEuiKWhPerM2: 37.8,
    normalizedEuiKWhPerM2: 38.2,
    tier: 'BENCHMARK',
    savingPotentialYuan: 42000,
    priority: 'LOW',
    isCurrentSite: false,
  },
  {
    rank: 2,
    id: 'site-sh-01',
    name: '陆家嘴国际金融广场',
    city: '上海',
    buildingType: '甲级商办综合体',
    grossAreaM2: 82000,
    systemCop: 4.95,
    rawEuiKWhPerM2: 41.0,
    normalizedEuiKWhPerM2: 40.5,
    tier: 'ADVANCED',
    savingPotentialYuan: 85000,
    priority: 'LOW',
    isCurrentSite: false,
  },
  {
    rank: 3,
    id: 'site-01940000-0001-7000-8000-000000000001',
    name: '东京中央冷站 (本站)',
    city: '东京',
    buildingType: '区域综合集中能源站',
    grossAreaM2: 45000,
    systemCop: 4.88,
    rawEuiKWhPerM2: 43.5,
    normalizedEuiKWhPerM2: 42.8,
    tier: 'ADVANCED',
    savingPotentialYuan: 120000,
    priority: 'MEDIUM',
    isCurrentSite: true,
  },
  {
    rank: 4,
    id: 'site-sz-03',
    name: '南山科技园创新港',
    city: '深圳',
    buildingType: '研发办公园区',
    grossAreaM2: 58000,
    systemCop: 4.62,
    rawEuiKWhPerM2: 48.2,
    normalizedEuiKWhPerM2: 46.5,
    tier: 'COMPLIANT',
    savingPotentialYuan: 195000,
    priority: 'MEDIUM',
    isCurrentSite: false,
  },
  {
    rank: 5,
    id: 'site-bj-01',
    name: '中关村前沿科技中心',
    city: '北京',
    buildingType: '科技办公综合体',
    grossAreaM2: 52000,
    systemCop: 4.35,
    rawEuiKWhPerM2: 51.5,
    normalizedEuiKWhPerM2: 52.6,
    tier: 'COMPLIANT',
    savingPotentialYuan: 260000,
    priority: 'HIGH',
    isCurrentSite: false,
  },
  {
    rank: 6,
    id: 'site-sg-02',
    name: '滨海湾智能商务中心',
    city: '新加坡',
    buildingType: '热带商办大厦',
    grossAreaM2: 72000,
    systemCop: 3.82,
    rawEuiKWhPerM2: 78.4,
    normalizedEuiKWhPerM2: 68.2,
    tier: 'LAGGING',
    savingPotentialYuan: 680000,
    priority: 'HIGH',
    isCurrentSite: false,
  },
];

const BENCHMARK_BUILDING_TYPE_OPTIONS = [...new Set(BENCHMARKED_SITES.map((site) => site.buildingType))]
  .map((value) => ({ label: value, value }));
const BENCHMARK_TIER_OPTIONS = [
  { label: '行业标杆', value: 'BENCHMARK' },
  { label: '领先梯队', value: 'ADVANCED' },
  { label: '达标基线', value: 'COMPLIANT' },
  { label: '需整改', value: 'LAGGING' },
] as const;
const BENCHMARK_FILTER_COLUMN_IDS = ['buildingType', 'tier'] as const;
const BENCHMARK_FILTERS_QUERY_KEY = 'benchmarkFilters';
const BENCHMARK_JOIN_OPERATOR_QUERY_KEY = 'benchmarkJoinOperator';

function matchesBenchmarkAdvancedFilter(
  site: BenchmarkedSite,
  filter: ExtendedColumnFilter<BenchmarkedSite>,
) {
  const values = Array.isArray(filter.value) ? filter.value : [filter.value];
  const actual = filter.id === 'buildingType' ? site.buildingType : filter.id === 'tier' ? site.tier : undefined;
  if (actual === undefined) return true;
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

export function SiteBenchmarkingWorkspace({ siteId: _siteId }: SiteBenchmarkingWorkspaceProps) {
  const [searchQuery, setSearchQuery] = useState('');
  const [advancedFilters] = useQueryState(
    BENCHMARK_FILTERS_QUERY_KEY,
    getFiltersStateParser<BenchmarkedSite>([...BENCHMARK_FILTER_COLUMN_IDS]).withDefault([]),
  );
  const [joinOperator] = useQueryState(
    BENCHMARK_JOIN_OPERATOR_QUERY_KEY,
    parseAsStringEnum<JoinOperator>(['and', 'or']).withDefault('and'),
  );
  const filteredSites = useMemo(() => {
    return BENCHMARKED_SITES.filter((site) => {
      const q = searchQuery.trim().toLowerCase();
      const matchesSearch = !q || site.name.toLowerCase().includes(q) || site.city.toLowerCase().includes(q);
      const filterMatches = advancedFilters.map((filter) => matchesBenchmarkAdvancedFilter(site, filter));
      const matchesAdvancedFilters = advancedFilters.length === 0 || (joinOperator === 'or'
        ? filterMatches.some(Boolean)
        : filterMatches.every(Boolean));
      return matchesSearch && matchesAdvancedFilters;
    });
  }, [advancedFilters, joinOperator, searchQuery]);

  const columns = useMemo<Array<ColumnDef<DataTableFeatures, BenchmarkedSite>>>(() => [
    {
      id: 'select',
      header: ({ table }) => (
        <Checkbox
          checked={table.getIsAllPageRowsSelected() ? true : table.getIsSomePageRowsSelected() ? 'indeterminate' : false}
          onCheckedChange={(checked) => table.toggleAllPageRowsSelected(Boolean(checked))}
          aria-label="全选站点"
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
      id: 'rank',
      accessorFn: (row) => row.rank,
      meta: { label: '综合排名' },
      header: '综合排名',
      cell: ({ row }) => (
        <span className={`flex size-5 items-center justify-center rounded-full text-[11px] font-mono font-bold ${row.original.rank <= 3 ? 'bg-primary text-primary-foreground' : 'bg-muted text-muted-foreground'}`}>
          {row.original.rank}
        </span>
      ),
    },
    {
      id: 'name',
      accessorFn: (row) => row.name,
      meta: { label: '站点名称' },
      header: '站点名称与城市',
      cell: ({ row }) => <div><div className="flex items-center gap-1.5"><span className="font-medium text-foreground">{row.original.name}</span>{row.original.isCurrentSite ? <Badge variant="secondary" className="px-1.5 py-0 text-[10px] font-normal">当前站点</Badge> : null}</div><div className="mt-0.5 flex items-center gap-1 text-[11px] text-muted-foreground"><MapPin className="size-3" />{row.original.city}</div></div>,
    },
    { id: 'buildingType', accessorFn: (row) => row.buildingType, enableColumnFilter: true, meta: { label: '建筑业态', variant: 'select', options: BENCHMARK_BUILDING_TYPE_OPTIONS }, header: '建筑业态', cell: ({ row }) => <span className="text-muted-foreground">{row.original.buildingType}</span> },
    { id: 'grossArea', accessorFn: (row) => row.grossAreaM2, meta: { label: '建筑面积' }, header: '建筑面积', cell: ({ row }) => <span className="font-mono text-muted-foreground tabular-nums">{row.original.grossAreaM2.toLocaleString()} m²</span> },
    { id: 'cop', accessorFn: (row) => row.systemCop, meta: { label: '冷源系统 COP' }, header: '冷源系统 COP', cell: ({ row }) => <span className="font-mono font-semibold text-foreground tabular-nums">{row.original.systemCop.toFixed(2)}</span> },
    { id: 'rawEui', accessorFn: (row) => row.rawEuiKWhPerM2, meta: { label: '原始 EUI' }, header: '原始 EUI', cell: ({ row }) => <span className="font-mono text-muted-foreground tabular-nums">{row.original.rawEuiKWhPerM2.toFixed(1)} kWh/m²·a</span> },
    { id: 'normalizedEui', accessorFn: (row) => row.normalizedEuiKWhPerM2, meta: { label: '气象归一化 EUI' }, header: '气象归一化 EUI', cell: ({ row }) => <span className="font-mono font-bold text-foreground tabular-nums">{row.original.normalizedEuiKWhPerM2.toFixed(1)} kWh/m²·a</span> },
    {
      id: 'tier',
      accessorFn: (row) => row.tier,
      enableColumnFilter: true,
      meta: { label: '对标梯队评级', variant: 'select', options: [...BENCHMARK_TIER_OPTIONS] },
      header: '对标梯队评级',
      cell: ({ row }) => <StatusBadge tone={row.original.tier === 'BENCHMARK' ? 'success' : row.original.tier === 'ADVANCED' ? 'info' : row.original.tier === 'COMPLIANT' ? 'neutral' : 'destructive'} pulse={row.original.tier === 'LAGGING'} label={row.original.tier === 'BENCHMARK' ? '行业标杆 TOP 10%' : row.original.tier === 'ADVANCED' ? '能效领先梯队' : row.original.tier === 'COMPLIANT' ? '行业基线达标' : '高耗能亟需优化'} />,
    },
    { id: 'saving', accessorFn: (row) => row.savingPotentialYuan, meta: { label: '年潜在节约潜力' }, header: '年潜在节约潜力', cell: ({ row }) => <span className="font-mono text-foreground tabular-nums">¥{row.original.savingPotentialYuan.toLocaleString()} / 年</span> },
    {
      id: 'priority',
      accessorFn: (row) => row.priority,
      meta: { label: '改进优先级' },
      header: '改进优先级',
      cell: ({ row }) => <StatusBadge tone={row.original.priority === 'HIGH' ? 'destructive' : row.original.priority === 'MEDIUM' ? 'warning' : 'success'} pulse={row.original.priority === 'HIGH'} label={row.original.priority === 'HIGH' ? '优先技改' : row.original.priority === 'MEDIUM' ? '策略自调优' : '标杆保持'} />,
    },
  ], []);

  const table = useDataTable({
    key: 'surface-02-site-benchmarking',
    data: [...filteredSites],
    columns,
    pageSize: 10,
    getRowId: (site) => site.id,
    meta: {
      queryKeys: {
        page: 'benchmarkPage',
        perPage: 'benchmarkPerPage',
        sort: 'benchmarkSort',
        filters: BENCHMARK_FILTERS_QUERY_KEY,
        joinOperator: BENCHMARK_JOIN_OPERATOR_QUERY_KEY,
      },
    },
  });


  return (
    <Main className="space-y-6">
      {/* 1. Header */}
      <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
        <div className="space-y-1">
          <div className="flex items-center gap-2">
            
          </div>
        </div>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" className="gap-1.5" onClick={() => window.location.reload()}>
            <RefreshCw className="size-3.5" />
            刷新对标测算
          </Button>
        </div>
      </div>

      {/* 2. Fact Strip */}
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-xs font-medium text-muted-foreground">同业态排名</CardTitle>
            <Award className="size-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold tracking-tight text-foreground tabular-nums">第 3 名</div>
            <p className="mt-1 text-xs text-muted-foreground">
              领先梯队 (共 12 站点)
            </p>
          </CardContent>
        </Card>

        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-xs font-medium text-muted-foreground">标杆 EUI</CardTitle>
            <Building2 className="size-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold tracking-tight text-foreground tabular-nums">38.2 <span className="text-xs font-normal text-muted-foreground">kWh/m²·a</span></div>
            <p className="mt-1 text-xs text-muted-foreground">
              本站 42.8 kWh/m²·a (+12.0%)
            </p>
          </CardContent>
        </Card>

        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-xs font-medium text-muted-foreground">EUI 中位数</CardTitle>
            <BarChart className="size-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold tracking-tight text-foreground tabular-nums">52.6 <span className="text-xs font-normal text-muted-foreground">kWh/m²·a</span></div>
            <p className="mt-1 text-xs text-muted-foreground">
              落后站点高耗偏离 +68.2%
            </p>
          </CardContent>
        </Card>

        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-xs font-medium text-muted-foreground">气候气象归一化</CardTitle>
            <Globe className="size-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold tracking-tight text-foreground">已校正对齐</div>
            <p className="mt-1 text-xs text-muted-foreground">
              基于供冷度日数 CDD18
            </p>
          </CardContent>
        </Card>
      </div>

      {/* 3. Weather-Normalized Model Explainer */}
      <Card className="shadow-xs">
        <CardHeader className="pb-3">
          <div className="flex items-center justify-between">
            <div>
              <CardTitle className="text-base font-semibold">气象校正与基准对齐</CardTitle>
              <CardDescription className="text-xs">
                采用度日数 (CDD) 对实际建筑面积电耗进行校正，消除跨区域温差影响
              </CardDescription>
            </div>
            <Badge variant="outline" className="text-xs">
              ASHRAE 14
            </Badge>
          </div>
        </CardHeader>
        <CardContent>
          <div className="rounded-md border bg-muted/20 p-2.5 text-xs text-muted-foreground flex flex-col sm:flex-row sm:items-center sm:justify-between gap-2">
            <span>东京中央冷站原始实际 EUI: <strong className="text-foreground tabular-nums font-semibold">43.5 kWh/m²·a</strong>，归一化修正后为 <strong className="text-foreground tabular-nums font-semibold">42.8 kWh/m²·a</strong></span>
            <span className="text-emerald-600 font-medium tabular-nums">优于业态中位数 18.6%</span>
          </div>
        </CardContent>
      </Card>

      {/* 4. Benchmarked Sites Ledger */}
      <DataTableBlock>

        <DataTable
          table={table}
          tableAriaLabel="站点对标"
          getHeaderCellProps={(header) => ({
            className: header.id === 'select' ? 'w-10' : header.id === 'rank' ? 'w-[80px]' : header.id === 'priority' ? 'text-right' : undefined,
          })}
          getRowProps={(row) => ({
            className: row.original.isCurrentSite ? 'bg-primary/5 font-semibold' : undefined,
          })}
          getCellProps={(cell) => ({
            className: cell.column.id === 'priority' ? 'text-right' : undefined,
          })}
          footer={(
            <DataTablePagination
              table={table}
              totalRows={filteredSites.length}
            />
          )}
        >
          <DataTableAdvancedToolbar table={table}>
            <div className="relative w-60">
              <Search className="absolute left-2.5 top-2.5 size-4 text-muted-foreground" />
              <Input
                placeholder="搜索站点名称 / 城市..."
                value={searchQuery}
                onChange={(e) => {
                  setSearchQuery(e.target.value);
                  table.setPageIndex(0);
                }}
                className="h-9 pl-8 text-xs"
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
