import { useState, useMemo } from 'react';
import type { ColumnDef } from '@tanstack/react-table';
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
  DataTableViewPills,
  StatusPillBadge,
  DataTableViewOptions,
  DataTablePagination,
  type DataTableFeatures,
  type DataTableViewPillOption,
} from '@/components/data-table';
import { useDataTable } from '@/hooks/use-data-table';

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
  const [sheddingStatusFilter, setSheddingStatusFilter] = useState<string>('ALL');
  const [searchQuery, setSearchQuery] = useState('');
  const pillOptions = useMemo<readonly DataTableViewPillOption[]>(() => [
    { id: 'ALL', label: '全部资源', count: SHEDDING_CANDIDATES.length },
    { id: 'READY', label: '就绪可用', count: SHEDDING_CANDIDATES.filter((i) => i.flexibilityStatus === 'READY').length },
    { id: 'STANDBY', label: '待命备用', count: SHEDDING_CANDIDATES.filter((i) => i.flexibilityStatus === 'STANDBY').length },
    { id: 'LOCKED', label: '约束锁定', count: SHEDDING_CANDIDATES.filter((i) => i.flexibilityStatus === 'LOCKED').length },
  ], []);

  const filteredCandidates = useMemo(() => {
    return SHEDDING_CANDIDATES.filter((item) => {
      if (sheddingStatusFilter !== 'ALL' && item.flexibilityStatus !== sheddingStatusFilter) return false;
      if (searchQuery && !item.name.toLowerCase().includes(searchQuery.toLowerCase()) && !item.id.toLowerCase().includes(searchQuery.toLowerCase())) {
        return false;
      }
      return true;
    });
  }, [sheddingStatusFilter, searchQuery]);

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
      header: '设备 / 资源名称',
      cell: ({ row }) => <div className="font-medium text-foreground"><div>{row.original.name}</div><div className="font-mono text-[10px] text-muted-foreground">{row.original.id}</div></div>,
    },
    { id: 'category', header: '子系统', cell: ({ row }) => <Badge variant="outline" className="text-xs font-normal">{row.original.category}</Badge> },
    { id: 'currentPower', header: '当前运行功率', cell: ({ row }) => <span className="font-mono font-medium text-foreground tabular-nums">{row.original.currentPower.toFixed(1)} kW</span> },
    { id: 'sheddingPotential', header: '可削峰容量 (kW)', cell: ({ row }) => <span className="font-mono font-semibold text-foreground tabular-nums">{row.original.sheddingPotential.toFixed(1)} kW</span> },
    { id: 'responseTime', header: '响应时延', cell: ({ row }) => <span className="font-mono tabular-nums">≤ {row.original.responseTimeMinutes} 分钟</span> },
    { id: 'constraint', header: '调控约束与安全边界', cell: ({ row }) => <span className="text-xs text-muted-foreground">{row.original.constraintDescription}</span> },
    { id: 'status', header: '就绪状态', cell: ({ row }) => <StatusPillBadge label={row.original.flexibilityStatus === 'READY' ? '就绪可用' : row.original.flexibilityStatus === 'STANDBY' ? '待命备用' : '约束锁定'} tone={row.original.flexibilityStatus === 'READY' ? 'success' : row.original.flexibilityStatus === 'STANDBY' ? 'info' : 'warning'} /> },
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
  });

  return (
    <Main className="space-y-6">
      {/* 1. Page Header */}
      <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <div className="flex items-center gap-2">
            <h1 className="text-2xl font-bold tracking-tight text-foreground">需求与负荷分析</h1>
            <Badge variant="outline" className="font-normal gap-1.5 text-xs">
              <span className="size-1.5 rounded-full bg-emerald-500" />
              需量控制与柔性
            </Badge>
          </div>
          <p className="mt-1 text-sm text-muted-foreground">
            变压器需量预警、负荷持续特征与柔性调控资源
          </p>
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
            <CardTitle className="text-xs font-medium text-muted-foreground">当月最大需量 (Peak Demand)</CardTitle>
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
            <CardTitle className="text-xs font-medium text-muted-foreground">需量安全余量</CardTitle>
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
            <CardTitle className="text-xs font-medium text-muted-foreground">尖峰时段平均负荷</CardTitle>
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
            <CardTitle className="text-xs font-medium text-muted-foreground">可调柔性削峰容量</CardTitle>
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
                <CardTitle className="text-base">日 24 小时负荷时序与需量预警线</CardTitle>
                <CardDescription>今日实时与昨日基准功率对比</CardDescription>
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
                <CardTitle className="text-base">月度负荷持续曲线 (Load Duration Curve)</CardTitle>
                <CardDescription>月度各功率区间的累计运行小时数统计</CardDescription>
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
      <Card className="shadow-xs">
        <CardHeader className="pb-4">
          <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
            <div>
              <CardTitle className="text-lg">可调柔性负荷资源与削峰响应台账</CardTitle>
              <CardDescription>需量接近报警阈值时的优先调控与负荷压降清单</CardDescription>
            </div>
            <div className="flex items-center gap-2 text-xs text-muted-foreground">
              <span>总可用削峰潜力：<strong className="text-foreground text-sm font-semibold font-mono tabular-nums">162.0 kW</strong></span>
            </div>
          </div>
        </CardHeader>
        <div className="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-3 px-6 py-3 border-b">
          <div className="flex flex-wrap items-center gap-2">
            <DataTableViewPills
              options={pillOptions}
              value={sheddingStatusFilter}
              onValueChange={(value) => {
                setSheddingStatusFilter(value);
                table.setPageIndex(0);
              }}
            />
            <div className="relative">
              <Search className="absolute left-2.5 top-2.5 h-3.5 w-3.5 text-muted-foreground" />
              <Input
                placeholder="搜索资源名称或编号..."
                value={searchQuery}
                onChange={(e) => {
                  setSearchQuery(e.target.value);
                  table.setPageIndex(0);
                }}
                className="h-8 pl-8 text-xs w-[180px] sm:w-[220px]"
              />
            </div>
          </div>
          <DataTableViewOptions table={table} />
        </div>
        <CardContent className="p-0">
          <DataTable
            table={table}
            className="gap-0"
            tableAriaLabel="可调柔性负荷资源与削峰响应台账"
            empty="未找到匹配的柔性调控资源"
            getHeaderRowProps={() => ({ className: 'bg-muted/30 text-xs' })}
            getHeaderCellProps={(header) => ({
              className:
                header.id === 'select' ? 'w-[40px]' :
                header.id === 'name' ? 'min-w-[200px]' :
                header.id === 'category' || header.id === 'currentPower' || header.id === 'status' ? 'w-[120px]' :
                header.id === 'sheddingPotential' ? 'w-[140px]' :
                header.id === 'responseTime' ? 'w-[110px] text-center' :
                header.id === 'constraint' ? 'min-w-[260px]' :
                header.id === 'action' ? 'w-[100px] text-right' :
                undefined,
            })}
            getRowProps={() => ({ className: 'text-xs hover:bg-muted/40' })}
            getCellProps={(cell) => ({
              className:
                cell.column.id === 'currentPower' || cell.column.id === 'sheddingPotential' || cell.column.id === 'action'
                  ? 'text-right'
                  : cell.column.id === 'responseTime' || cell.column.id === 'status'
                    ? 'text-center'
                    : undefined,
            })}
            footer={(
              <DataTablePagination
                table={table}
                totalRows={filteredCandidates.length}
                
                
                
                
                
              />
            )}
          />
        </CardContent>
      </Card>
    </Main>
  );
}
