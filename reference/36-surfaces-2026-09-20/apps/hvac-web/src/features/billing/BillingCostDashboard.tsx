import { useMemo, useState } from 'react';
import type { ColumnDef } from '@tanstack/react-table';
import {
  Bar,
  BarChart,
  CartesianGrid,
  Cell,
  Legend,
  Pie,
  PieChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts';
import {
  ArrowDownRight,
  BadgePercent,
  CircleDollarSign,
  Download,
  Receipt,
  Zap,
} from 'lucide-react';

import { Main } from '@/components/layout/Main';
import type { CurrentPrincipalResponse, Site } from '@/api/generated/platformGateway.gen';
import { siteRoute } from '@/app/router-paths';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs';
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

interface BillingCostProps {
  site: Readonly<Site>;
  principal: CurrentPrincipalResponse;
}

const TOU_ENERGY_COST_DATA = [
  { period: '尖峰时段 (11-13/18-20)', hours: '4h', rate: 1.48, energy: 38400, cost: 56832, fill: '#ef4444' },
  { period: '高峰时段 (08-11/13-18)', hours: '8h', rate: 1.12, energy: 72800, cost: 81536, fill: '#f97316' },
  { period: '平段 (07-08/20-23)', hours: '4h', rate: 0.76, energy: 41200, cost: 31312, fill: '#06b6d4' },
  { period: '低谷时段 (23-07)', hours: '8h', rate: 0.38, energy: 73600, cost: 27968, fill: '#3b82f6' },
];

const SUBSYSTEM_COST_DATA = [
  { name: '冷水主机组', cost: 112500, percent: 57.0, fill: '#2563eb' },
  { name: '冷冻/冷却泵组', cost: 42800, percent: 21.7, fill: '#0ea5e9' },
  { name: 'AHU/末端空调', cost: 28400, percent: 14.4, fill: '#10b981' },
  { name: '冷却塔风机', cost: 13948, percent: 6.9, fill: '#f59e0b' },
];

export function BillingCostDashboard({ site }: BillingCostProps) {
  const [selectedMonth, setSelectedMonth] = useState<'current' | 'last'>('current');
  const [periodFilter, setPeriodFilter] = useState<string>('ALL');
  const periodPills: readonly DataTableViewPillOption[] = [
    { key: 'ALL', label: '全部时段', count: TOU_ENERGY_COST_DATA.length },
    { key: 'PEAK', label: '尖峰/高峰', count: 2 },
    { key: 'VALLEY', label: '平段/低谷', count: 2 },
  ];

  const totalEnergy = useMemo(() => TOU_ENERGY_COST_DATA.reduce((acc, cur) => acc + cur.energy, 0), []);
  const totalTouCost = useMemo(() => TOU_ENERGY_COST_DATA.reduce((acc, cur) => acc + cur.cost, 0), []);
  const blendedRate = useMemo(() => (totalTouCost / totalEnergy).toFixed(3), [totalTouCost, totalEnergy]);

  const filteredData = useMemo(() => {
    if (periodFilter === 'PEAK') {
      return TOU_ENERGY_COST_DATA.filter((d) => d.rate >= 1.0);
    }
    if (periodFilter === 'VALLEY') {
      return TOU_ENERGY_COST_DATA.filter((d) => d.rate < 1.0);
    }
    return TOU_ENERGY_COST_DATA;
  }, [periodFilter]);

  type TouCostRow = (typeof TOU_ENERGY_COST_DATA)[number];

  const columns = useMemo<Array<ColumnDef<DataTableFeatures, TouCostRow>>>(() => [
    {
      id: 'select',
      header: ({ table }) => (
        <Checkbox
          checked={table.getIsAllPageRowsSelected() ? true : table.getIsSomePageRowsSelected() ? 'indeterminate' : false}
          onCheckedChange={(checked) => table.toggleAllPageRowsSelected(Boolean(checked))}
          aria-label="全选计费时段"
        />
      ),
      cell: ({ row }) => (
        <Checkbox
          checked={row.getIsSelected()}
          onCheckedChange={(checked) => row.toggleSelected(Boolean(checked))}
          aria-label={`选择 ${row.original.period}`}
        />
      ),
      enableSorting: false,
      enableHiding: false,
    },
    {
      id: 'period',
      header: '计费时段',
      cell: ({ row }) => (
        <div className="flex items-center gap-2 font-medium text-foreground">
          <span className="size-2 shrink-0 rounded-full" style={{ backgroundColor: row.original.fill }} />
          <span>{row.original.period}</span>
        </div>
      ),
    },
    { id: 'hours', header: '每日时长', cell: ({ row }) => <span className="font-mono tabular-nums">{row.original.hours}/天</span> },
    { id: 'rate', header: '执行费率 (元/kWh)', cell: ({ row }) => <span className="font-mono font-semibold tabular-nums">¥{row.original.rate.toFixed(2)}</span> },
    { id: 'energy', header: '时段电量 (kWh)', cell: ({ row }) => <span className="font-mono font-medium tabular-nums">{row.original.energy.toLocaleString()} kWh</span> },
    { id: 'cost', header: '电度电费小计 (元)', cell: ({ row }) => <span className="font-mono font-bold text-foreground tabular-nums">¥{row.original.cost.toLocaleString()}</span> },
    { id: 'share', header: '电费占比', cell: ({ row }) => <span className="font-mono tabular-nums">{((row.original.cost / totalTouCost) * 100).toFixed(1)}%</span> },
    {
      id: 'suggestion',
      header: '节费建议与响应',
      cell: ({ row }) => row.original.rate > 1.0 ? (
        <div className="flex items-center justify-end gap-2">
          <StatusPillBadge tone="warning" pulse label="建议参与削峰" />
          <Button variant="ghost" size="sm" className="h-7 px-2 text-xs" asChild>
            <a href={siteRoute(site, 'opportunities')}>削峰方案</a>
          </Button>
        </div>
      ) : (
        <div className="flex justify-end">
          <StatusPillBadge tone="success" label="基础填谷工况" />
        </div>
      ),
      enableSorting: false,
    },
  ], [site, totalTouCost]);

  const table = useDataTable({
    key: 'surface-18-billing-tou',
    data: [...filteredData],
    columns,
    pageSize: 10,
    getRowId: (row) => row.period,
  });

  return (
    <Main className="space-y-6">
      {/* 1. Page Header */}
      <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <div className="flex items-center gap-2">
            <h1 className="text-2xl font-bold tracking-tight text-foreground">账单、成本与电价</h1>
            <Badge variant="outline" className="text-xs font-normal">
              两部制分时账单
            </Badge>
          </div>
          <p className="mt-1 text-sm text-muted-foreground">
            综合核算电度电费、容量/需量基本电费与力调奖惩，透明化分时能源财务账单
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Tabs value={selectedMonth} onValueChange={(val) => setSelectedMonth(val as any)}>
            <TabsList className="h-9">
              <TabsTrigger value="current">本月账期 (09月)</TabsTrigger>
              <TabsTrigger value="last">上月账单 (08月)</TabsTrigger>
            </TabsList>
          </Tabs>
          <Button variant="outline" size="sm">
            <Download className="mr-1.5 size-3.5" />
            导出财务报表
          </Button>
        </div>
      </div>

      {/* 2. 4-Card Financial Fact Strip */}
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">当月累计总电费</CardTitle>
            <CircleDollarSign className="size-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="flex items-baseline gap-2">
              <div className="text-2xl font-bold tracking-tight text-foreground tabular-nums">¥197,648</div>
              <Badge variant="outline" className="gap-1 font-normal text-xs">
                <ArrowDownRight className="size-3" />
                -5.4%
              </Badge>
            </div>
            <p className="text-xs text-muted-foreground mt-1">电度电费 ¥197,648 + 基本电费 ¥38,600</p>
          </CardContent>
        </Card>

        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">综合平均度电成本</CardTitle>
            <Zap className="size-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="flex items-baseline gap-2">
              <div className="text-2xl font-bold tracking-tight text-foreground tabular-nums">{blendedRate} <span className="text-sm font-normal text-muted-foreground">元/kWh</span></div>
              <Badge variant="outline" className="gap-1 font-normal text-xs">
                <span className="size-1.5 rounded-full bg-emerald-500" />
                行业优选
              </Badge>
            </div>
            <p className="text-xs text-muted-foreground mt-1">基准参考价 0.890 元/kWh · 谷电占比提升 4.2%</p>
          </CardContent>
        </Card>

        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">基本电费测算 (需量计费)</CardTitle>
            <Receipt className="size-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="flex items-baseline gap-2">
              <div className="text-2xl font-bold tracking-tight text-foreground tabular-nums">¥38,600</div>
              <span className="text-xs text-muted-foreground tabular-nums">
                省 ¥8,400/月
              </span>
            </div>
            <p className="text-xs text-muted-foreground mt-1">最大需量 965 kW × 40元/kW（优于容量计费）</p>
          </CardContent>
        </Card>

        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">功率因数力调奖惩</CardTitle>
            <BadgePercent className="size-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="flex items-baseline gap-2">
              <div className="text-2xl font-bold tracking-tight text-foreground tabular-nums">+¥988 <span className="text-sm font-normal text-muted-foreground">奖励</span></div>
              <span className="text-xs text-muted-foreground tabular-nums">cos φ = 0.94</span>
            </div>
            <p className="text-xs text-muted-foreground mt-1">考核标准 0.90 · 享受 0.5% 力调电费减免</p>
          </CardContent>
        </Card>
      </div>

      {/* 3. Charts Row: TOU Breakdown & Subsystem Cost Composition */}
      <div className="grid gap-6 lg:grid-cols-2">
        <Card>
          <CardHeader className="pb-2">
            <div className="flex items-center justify-between">
              <div>
                <CardTitle className="text-base">分时电价（TOU）各时段用电量与费用构成</CardTitle>
                <CardDescription>尖峰、高峰、平段、低谷各时段用电量与支出构成</CardDescription>
              </div>
              <Badge variant="outline" className="text-xs">峰谷比 1:3.9</Badge>
            </div>
          </CardHeader>
          <CardContent>
            <div className="h-[280px]">
              <ResponsiveContainer width="100%" height="100%">
                <BarChart data={TOU_ENERGY_COST_DATA} margin={{ top: 20, right: 20, bottom: 10, left: 10 }}>
                  <CartesianGrid strokeDasharray="3 3" stroke="#e4e4e7" strokeOpacity={0.6} />
                  <XAxis dataKey="period" tick={{ fontSize: 11, fill: '#71717a' }} />
                  <YAxis yAxisId="cost" tick={{ fontSize: 12, fill: '#71717a' }} unit=" ¥" />
                  <Tooltip formatter={(val: any, name: any) => [name === 'cost' ? `¥${val.toLocaleString()}` : `${val.toLocaleString()} kWh`, name === 'cost' ? '电费支出' : '用电量']} />
                  <Legend />
                  <Bar yAxisId="cost" dataKey="cost" name="分时电费 (¥)">
                    {TOU_ENERGY_COST_DATA.map((entry, index) => (
                      <Cell key={`cell-${index}`} fill={entry.fill} />
                    ))}
                  </Bar>
                </BarChart>
              </ResponsiveContainer>
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="pb-2">
            <div className="flex items-center justify-between">
              <div>
                <CardTitle className="text-base">暖通空调主要子系统用电成本贡献</CardTitle>
                <CardDescription>冷水机组、输配水泵、末端风系统各分项当月电费支出拆解</CardDescription>
              </div>
              <Badge variant="outline" className="text-xs">分项计量模式</Badge>
            </div>
          </CardHeader>
          <CardContent>
            <div className="h-[280px] flex items-center">
              <div className="w-1/2 h-full">
                <ResponsiveContainer width="100%" height="100%">
                  <PieChart>
                    <Pie
                      data={SUBSYSTEM_COST_DATA}
                      cx="50%"
                      cy="50%"
                      innerRadius={60}
                      outerRadius={85}
                      paddingAngle={3}
                      dataKey="cost"
                    >
                      {SUBSYSTEM_COST_DATA.map((entry, index) => (
                        <Cell key={`slice-${index}`} fill={entry.fill} />
                      ))}
                    </Pie>
                    <Tooltip formatter={(val: any) => [`¥${Number(val).toLocaleString()}`, '电费']} />
                  </PieChart>
                </ResponsiveContainer>
              </div>
              <div className="w-1/2 space-y-3 pl-4">
                {SUBSYSTEM_COST_DATA.map((item) => (
                  <div key={item.name} className="flex items-center justify-between text-xs">
                    <div className="flex items-center gap-2">
                      <span className="size-2.5 rounded-full" style={{ backgroundColor: item.fill }} />
                      <span className="font-medium">{item.name}</span>
                    </div>
                    <div className="text-right">
                      <span className="tabular-nums font-semibold">¥{item.cost.toLocaleString()}</span>
                      <span className="text-muted-foreground ml-1">({item.percent}%)</span>
                    </div>
                  </div>
                ))}
              </div>
            </div>
          </CardContent>
        </Card>
      </div>

      {/* 4. TOU Billing Tariff Detail Table */}
      <Card>
        <CardHeader className="pb-4">
          <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
            <div>
              <CardTitle className="text-lg">分时电价执行明细与两部制电费构成</CardTitle>
              <CardDescription>当月 30 天计费周期核算事实，依据电网目录分时电价与需量申报协议</CardDescription>
            </div>
            <div className="flex flex-wrap items-center gap-2">
              <DataTableViewPills
                options={periodPills}
                value={periodFilter}
                onValueChange={(value) => {
                  setPeriodFilter(value);
                  table.setPageIndex(0);
                }}
              />
              <DataTableViewOptions table={table} />
            </div>
          </div>
        </CardHeader>
        <CardContent>
          <DataTable
            table={table}
            className="gap-0"
            tableAriaLabel="分时电价执行明细"
            getHeaderRowProps={() => ({ className: 'bg-muted/30 text-xs' })}
            getHeaderCellProps={(header) => ({
              className:
                header.id === 'select' ? 'w-10' :
                header.id === 'period' ? 'min-w-[200px]' :
                header.id === 'hours' ? 'w-[120px]' :
                header.id === 'rate' || header.id === 'energy' || header.id === 'cost' ? 'w-[140px]' :
                header.id === 'share' ? 'w-[130px]' :
                header.id === 'suggestion' ? 'w-[180px] text-right' :
                undefined,
            })}
            getRowProps={() => ({ className: 'text-xs transition-colors hover:bg-muted/40' })}
            getCellProps={(cell) => ({
              className: cell.column.id === 'suggestion' ? 'text-right' : undefined,
            })}
            footer={(
              <DataTablePagination
                table={table}
                totalRows={filteredData.length}
                
                
                
                
                
              />
            )}
          />
        </CardContent>
      </Card>
    </Main>
  );
}
