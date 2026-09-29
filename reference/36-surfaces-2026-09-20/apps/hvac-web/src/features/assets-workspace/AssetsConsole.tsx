import { useMemo, useState } from 'react';
import type { ColumnDef } from '@tanstack/react-table';
import {
  Activity,
  Box,
  Eye,
  Fan,
  GripVertical,
  Plus,
  RadioTower,
  RefreshCw,
  Search,
  SlidersHorizontal,
  Snowflake,
  Stethoscope,
  Wind,
  Wrench,
  X,
} from 'lucide-react';
import type { CurrentPrincipalResponse, Site } from '@/api/generated/platformGateway.gen';
import {
  DataTable,
  DataTableFacetedFilter,
  DataTablePagination,
  DataTableViewOptions,
  DataTableViewPills,
  StatusPillBadge,
  type DataTableFeatures,
  type DataTableViewPillOption,
} from '@/components/data-table';
import { Main } from '@/components/layout/Main';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Checkbox } from '@/components/ui/checkbox';
import { Input } from '@/components/ui/input';
import { useDataTable } from '@/hooks/use-data-table';

export interface AssetsConsoleProps {
  readonly site: Readonly<Site>;
  readonly principal: CurrentPrincipalResponse;
  readonly runtime?: unknown;
  readonly searchState?: unknown;
  readonly onSearchChange?: (patch: unknown) => void;
  readonly onSelectDevice?: (deviceId: string) => void;
  readonly onOpenDetail?: (deviceId: string) => void;
}

interface DeviceRecord {
  readonly id: string;
  readonly code: string;
  readonly name: string;
  readonly category: 'chiller' | 'pump' | 'tower' | 'ahu' | 'fcu';
  readonly categoryLabel: string;
  readonly location: string;
  readonly status: 'running' | 'standby' | 'fault' | 'offline';
  readonly statusLabel: string;
  readonly powerKW: number;
  readonly loadPercent: number;
  readonly keyMetric: string;
  readonly healthScore: number;
  readonly lastSeen: string;
}

const DEVICES: readonly DeviceRecord[] = [
  { id: 'CH-01', code: 'CH-01', name: '1# 变频离心冷水机组', category: 'chiller', categoryLabel: '冷水机组', location: '中央冷站 B1 机房', status: 'running', statusLabel: '运行中', powerKW: 240, loadPercent: 78, keyMetric: 'COP 6.2 · 供水 7.0°C', healthScore: 98, lastSeen: '刚刚' },
  { id: 'CH-02', code: 'CH-02', name: '2# 变频离心冷水机组', category: 'chiller', categoryLabel: '冷水机组', location: '中央冷站 B1 机房', status: 'running', statusLabel: '运行中', powerKW: 210, loadPercent: 68, keyMetric: 'COP 5.8 · 供水 7.1°C', healthScore: 94, lastSeen: '刚刚' },
  { id: 'CH-03', code: 'CH-03', name: '3# 螺杆式冷水主机 (备用)', category: 'chiller', categoryLabel: '冷水机组', location: '中央冷站 B1 机房', status: 'standby', statusLabel: '就绪待机', powerKW: 0, loadPercent: 0, keyMetric: '待机就绪 · 联锁正常', healthScore: 92, lastSeen: '1 分钟前' },
  { id: 'CHWP-01', code: 'CHWP-01', name: '1# 一次冷冻水循环泵', category: 'pump', categoryLabel: '输配水泵', location: '中央冷站 B1 泵房', status: 'running', statusLabel: '运行中', powerKW: 45, loadPercent: 82, keyMetric: '48.5 Hz · 流量 320 m³/h', healthScore: 96, lastSeen: '刚刚' },
  { id: 'CHWP-02', code: 'CHWP-02', name: '2# 一次冷冻水循环泵', category: 'pump', categoryLabel: '输配水泵', location: '中央冷站 B1 泵房', status: 'running', statusLabel: '运行中', powerKW: 42, loadPercent: 76, keyMetric: '47.0 Hz · 流量 310 m³/h', healthScore: 95, lastSeen: '刚刚' },
  { id: 'CHWP-03', code: 'CHWP-03', name: '3# 一次冷冻水循环泵', category: 'pump', categoryLabel: '输配水泵', location: '中央冷站 B1 泵房', status: 'standby', statusLabel: '就绪待机', powerKW: 0, loadPercent: 0, keyMetric: '待机就绪', healthScore: 99, lastSeen: '刚刚' },
  { id: 'CT-01', code: 'CT-01', name: '1# 开式冷却塔', category: 'tower', categoryLabel: '冷却散热', location: '主楼裙房屋顶', status: 'running', statusLabel: '运行中', powerKW: 22, loadPercent: 88, keyMetric: '45.0 Hz · 逼近度 3.8K', healthScore: 88, lastSeen: '刚刚' },
  { id: 'CT-02', code: 'CT-02', name: '2# 开式冷却塔', category: 'tower', categoryLabel: '冷却散热', location: '主楼裙房屋顶', status: 'running', statusLabel: '运行中', powerKW: 20, loadPercent: 80, keyMetric: '42.0 Hz · 逼近度 3.6K', healthScore: 91, lastSeen: '刚刚' },
  { id: 'AHU-01', code: 'AHU-01', name: '1F 综合办公区组合式空气机组', category: 'ahu', categoryLabel: '空调末端', location: '1F 东侧空调机房', status: 'running', statusLabel: '运行中', powerKW: 7.5, loadPercent: 65, keyMetric: '送风 16.5°C · 静压 240Pa', healthScore: 97, lastSeen: '刚刚' },
  { id: 'AHU-07', code: 'AHU-07', name: '3F 研发中心组合式空气机组', category: 'ahu', categoryLabel: '空调末端', location: '3F 北侧空调机房', status: 'running', statusLabel: '运行中', powerKW: 8.2, loadPercent: 72, keyMetric: '送风 16.0°C · 滤网阻力偏高', healthScore: 86, lastSeen: '刚刚' },
];

const CATEGORY_PILLS: readonly DataTableViewPillOption[] = [
  { key: 'all', label: '全部设备', count: 200 },
  { key: 'chiller', label: '冷水机组', count: 42, icon: Snowflake },
  { key: 'pump', label: '输配水泵', count: 86, icon: Fan },
  { key: 'tower', label: '冷却散热', count: 24, icon: RadioTower },
  { key: 'ahu', label: '空调末端', count: 48, icon: Wind },
];

const STATUS_OPTIONS = [
  { label: '运行中', value: 'running' },
  { label: '就绪待机', value: 'standby' },
  { label: '检修告警', value: 'fault' },
];

const CATEGORY_ICONS = {
  chiller: Snowflake,
  pump: Fan,
  tower: RadioTower,
  ahu: Wind,
  fcu: SlidersHorizontal,
};

export function AssetsConsole({ site, onSelectDevice, onOpenDetail }: AssetsConsoleProps) {
  const [activeCategoryPill, setActiveCategoryPill] = useState<string>('all');
  const [selectedStatuses, setSelectedStatuses] = useState<string[]>([]);
  const [searchQuery, setSearchQuery] = useState('');
  const isFiltered = activeCategoryPill !== 'all' || selectedStatuses.length > 0 || searchQuery.length > 0;

  const handleResetFilters = () => {
    setActiveCategoryPill('all');
    setSelectedStatuses([]);
    setSearchQuery('');
  };

  const filtered = DEVICES.filter((d) => {
    if (activeCategoryPill !== 'all' && d.category !== activeCategoryPill) return false;
    if (selectedStatuses.length > 0 && !selectedStatuses.includes(d.status)) return false;
    if (searchQuery && !d.name.toLowerCase().includes(searchQuery.toLowerCase()) && !d.code.toLowerCase().includes(searchQuery.toLowerCase()) && !d.location.toLowerCase().includes(searchQuery.toLowerCase())) return false;
    return true;
  });

  const columns = useMemo<Array<ColumnDef<DataTableFeatures, DeviceRecord>>>(() => [
    {
      id: 'select',
      header: ({ table }) => (
        <Checkbox
          checked={table.getIsAllPageRowsSelected() ? true : table.getIsSomePageRowsSelected() ? 'indeterminate' : false}
          onCheckedChange={(checked) => table.toggleAllPageRowsSelected(Boolean(checked))}
          aria-label="全选设备"
        />
      ),
      cell: ({ row }) => (
        <Checkbox
          checked={row.getIsSelected()}
          onCheckedChange={(checked) => row.toggleSelected(Boolean(checked))}
          aria-label={`选择设备 ${row.original.code}`}
        />
      ),
      enableSorting: false,
      enableHiding: false,
    },
    {
      id: 'drag',
      header: '',
      cell: () => <GripVertical className="size-3.5 text-muted-foreground/40" />,
      enableSorting: false,
      enableHiding: false,
    },
    { id: 'code', header: '设备编号', cell: ({ row }) => <span className="font-mono font-medium text-foreground">{row.original.code}</span> },
    {
      id: 'name',
      header: '设备名称 / 分项',
      cell: ({ row }) => {
        const Icon = CATEGORY_ICONS[row.original.category] ?? Box;
        return (
          <div className="flex items-center gap-2">
            <div className="flex size-6 shrink-0 items-center justify-center rounded-md border bg-muted/40 text-muted-foreground">
              <Icon className="size-3" />
            </div>
            <span className="font-medium text-foreground">{row.original.name}</span>
            <span className="rounded-full border border-border/70 bg-muted/30 px-2 py-0.5 text-[10px] text-muted-foreground">
              {row.original.categoryLabel}
            </span>
          </div>
        );
      },
    },
    { id: 'location', header: '物理空间 / 机房', cell: ({ row }) => <span className="text-muted-foreground">{row.original.location}</span> },
    {
      id: 'status',
      header: '运行工况',
      cell: ({ row }) => (
        <StatusPillBadge
          tone={row.original.status === 'running' ? 'success' : row.original.status === 'standby' ? 'neutral' : 'destructive'}
          pulse={row.original.status === 'running'}
        >
          {row.original.statusLabel}
        </StatusPillBadge>
      ),
    },
    {
      id: 'load',
      header: '实时负荷',
      cell: ({ row }) => row.original.loadPercent > 0 ? (
        <div className="w-28 space-y-1">
          <div className="flex justify-between text-[11px]">
            <span className="font-medium text-foreground tabular-nums">{row.original.powerKW} kW</span>
            <span className="text-muted-foreground tabular-nums">{row.original.loadPercent}%</span>
          </div>
          <div className="h-1.5 w-full overflow-hidden rounded-full bg-muted">
            <div className="h-full rounded-full bg-primary" style={{ width: `${row.original.loadPercent}%` }} />
          </div>
        </div>
      ) : <span className="text-muted-foreground tabular-nums">0 kW (待机)</span>,
    },
    { id: 'telemetry', header: '关键遥测指标', cell: ({ row }) => <span className="text-muted-foreground tabular-nums">{row.original.keyMetric}</span> },
    { id: 'health', header: '健康评分', cell: ({ row }) => <span className="text-xs font-medium text-foreground tabular-nums">{row.original.healthScore} <span className="text-[10px] font-normal text-muted-foreground">分</span></span> },
    {
      id: 'actions',
      header: '操作',
      cell: ({ row }) => (
        <Button
          variant="ghost"
          size="sm"
          onClick={() => (onOpenDetail ?? onSelectDevice)?.(row.original.id)}
          className="h-7 px-2 text-xs text-muted-foreground hover:text-foreground"
        >
          <Eye className="mr-1 size-3.5" />
          查看详情
        </Button>
      ),
      enableSorting: false,
      enableHiding: false,
    },
  ], [onOpenDetail, onSelectDevice]);

  const table = useDataTable({
    key: 'surface-06-assets-console',
    data: [...filtered],
    columns,
    pageSize: 10,
    getRowId: (row) => row.id,
  });

  return (
    <Main className="space-y-6 pb-16" data-testid="assets-console">
      {/* Header */}
      <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <div className="flex items-center gap-2.5">
            <h1 className="text-2xl font-bold tracking-tight text-foreground">设备中心</h1>
            <Badge variant="outline" className="text-xs font-normal">
              在册 200 台设备
            </Badge>
          </div>
          <p className="mt-1 text-sm text-muted-foreground">
            {site.displayName} · {site.timezone} · 全站机电设备运行状态与关键遥测台账
          </p>
        </div>

        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" className="h-8 gap-1.5 text-xs font-medium">
            <RefreshCw className="size-3.5" />
            刷新状态
          </Button>
        </div>
      </div>

      {/* Fleet KPI Ribbon (4 Standard Cards) */}
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">受控设备总数</CardTitle>
            <Box className="size-4 text-muted-foreground" aria-hidden="true" />
          </CardHeader>
          <CardContent className="space-y-1">
            <div className="text-2xl font-bold tracking-tight text-foreground tabular-nums">
              200 <span className="text-xs font-normal text-muted-foreground">台</span>
            </div>
            <p className="text-xs text-muted-foreground">
              覆盖冷热源与末端机电系统
            </p>
          </CardContent>
        </Card>

        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">实时在线率</CardTitle>
            <Activity className="size-4 text-muted-foreground" aria-hidden="true" />
          </CardHeader>
          <CardContent className="space-y-1">
            <div className="text-2xl font-bold tracking-tight text-foreground tabular-nums">
              92.5%
            </div>
            <p className="text-xs text-muted-foreground">
              185 在线 · 10 待机 · 5 检修
            </p>
          </CardContent>
        </Card>

        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">机组综合健康度</CardTitle>
            <Stethoscope className="size-4 text-muted-foreground" aria-hidden="true" />
          </CardHeader>
          <CardContent className="space-y-1">
            <div className="text-2xl font-bold tracking-tight text-foreground tabular-nums">
              96.4 <span className="text-xs font-normal text-muted-foreground">/ 100 分</span>
            </div>
            <p className="text-xs text-muted-foreground">
              健康运行 · 2 项亚健康跟踪
            </p>
          </CardContent>
        </Card>

        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">维护与保养待办</CardTitle>
            <Wrench className="size-4 text-muted-foreground" aria-hidden="true" />
          </CardHeader>
          <CardContent className="space-y-1">
            <div className="text-2xl font-bold tracking-tight text-foreground tabular-nums">
              2 <span className="text-xs font-normal text-muted-foreground">项处理中</span>
            </div>
            <p className="text-xs text-muted-foreground">
              1 项周期维保 · 1 项滤网清洗
            </p>
          </CardContent>
        </Card>
      </div>

      {/* Asset Data Table matching sadmann7/tablecn */}
      <Card className="shadow-xs">
        <CardHeader className="pb-3 border-b space-y-3">
          {/* Top Pill Tabs & Right Action Cluster */}
          <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
            <DataTableViewPills
              options={CATEGORY_PILLS}
              value={activeCategoryPill}
              onValueChange={(val) => {
                setActiveCategoryPill(val);
                table.setPageIndex(0);
              }}
              ariaLabel="设备系统分类"
            />
            <div className="flex items-center gap-2 self-end sm:self-auto">
              <DataTableViewOptions table={table} label="自定义列" />
              <Button size="sm" className="h-8 gap-1.5 text-xs font-medium">
                <Plus className="size-3.5" />
                新增设备
              </Button>
            </div>
          </div>

          {/* Sub-row: Search & Status Filters */}
          <div className="flex flex-wrap items-center gap-2 pt-1">
            <div className="relative w-full sm:w-64">
              <Search className="absolute left-2.5 top-2.5 size-3.5 text-muted-foreground" />
              <Input
                placeholder="搜索设备名称、编号或位置..."
                value={searchQuery}
                onChange={(e) => {
                  setSearchQuery(e.target.value);
                  table.setPageIndex(0);
                }}
                className="h-8 pl-8 text-xs bg-background"
              />
            </div>

            <DataTableFacetedFilter
              title="运行工况"
              options={STATUS_OPTIONS}
              selectedValues={new Set(selectedStatuses)}
              onSelect={(vals) => {
                setSelectedStatuses(vals);
                table.setPageIndex(0);
              }}
            />

            {isFiltered && (
              <Button
                variant="ghost"
                onClick={handleResetFilters}
                className="h-8 px-2 lg:px-3 text-xs gap-1"
              >
                <X className="size-3.5" />
                重置筛选
              </Button>
            )}
          </div>
        </CardHeader>

        {/* High-density Asset Table */}
        <CardContent className="p-0">
          <DataTable
            table={table}
            className="gap-0"
            tableAriaLabel="设备运行台账"
            empty="未找到匹配的设备"
            getHeaderRowProps={() => ({ className: 'bg-muted/20 hover:bg-transparent' })}
            getHeaderCellProps={(header) => ({
              className:
                header.id === 'select' ? 'w-10 px-3 text-center' :
                header.id === 'drag' ? 'w-6 px-0' :
                header.id === 'code' ? 'w-28 text-xs font-medium' :
                header.id === 'health' ? 'text-center text-xs font-medium' :
                header.id === 'actions' ? 'text-right text-xs font-medium' :
                'text-xs font-medium',
            })}
            getRowProps={() => ({ className: 'text-xs transition-colors hover:bg-muted/40' })}
            getCellProps={(cell) => ({
              className:
                cell.column.id === 'select' ? 'w-10 px-3 text-center' :
                cell.column.id === 'drag' ? 'w-6 cursor-grab px-0' :
                cell.column.id === 'health' ? 'text-center' :
                cell.column.id === 'actions' ? 'text-right' :
                undefined,
            })}
            footer={(
              <DataTablePagination
                table={table}
                totalRows={filtered.length}
                
                
                
                
                
              />
            )}
          />
        </CardContent>
      </Card>
    </Main>
  );
}
