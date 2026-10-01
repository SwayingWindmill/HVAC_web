import { useMemo, useState } from 'react';
import type { ColumnDef } from '@tanstack/react-table';
import {
  ArrowDownRight,
  ChevronLeft,
  ChevronRight,
  Download,
  Flame,
  Gauge,
  Leaf,
  RefreshCw,
  TrendingUp,
  Zap,
} from 'lucide-react';
import {
  Bar,
  CartesianGrid,
  Cell,
  ComposedChart,
  Line,
  Pie,
  PieChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts';
import type { CurrentPrincipalResponse, Site } from '@/api/generated/platformGateway.gen';
import { Main } from '@/components/layout/Main';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Checkbox } from '@/components/ui/checkbox';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { DataTable, DataTablePagination, StatusPillBadge, type DataTableFeatures } from '@/components/data-table';
import { useDataTable } from '@/hooks/use-data-table';

export interface EnergyAnalyticsDashboardProps {
  readonly site: Readonly<Site>;
  readonly principal: CurrentPrincipalResponse;
  readonly initialPeriod?: string;
  readonly searchState?: unknown;
  readonly onWorkspaceChange?: (next: unknown) => void;
}

const TARIFF_COLORS = {
  valley: '#3b82f6', // 谷电
  flat: '#06b6d4',   // 平电
  peak: '#f59e0b',   // 峰电
  sharp: '#ef4444',  // 尖峰
};

const BREAKDOWN_DATA = [
  { name: '冷水机组群', value: 420, percent: 48.8, color: '#2563eb' },
  { name: '冷冻水泵', value: 150, percent: 17.4, color: '#06b6d4' },
  { name: '空调末端 AHU', value: 130, percent: 15.1, color: '#3b82f6' },
  { name: '冷却水泵及塔', value: 90, percent: 10.5, color: '#f59e0b' },
  { name: '其他动力辅助', value: 70, percent: 8.2, color: '#8b5cf6' },
];

const DAILY_TREND_DATA = Array.from({ length: 30 }, (_, index) => {
  const day = String(index + 1).padStart(2, '0');
  const valley = Math.round(900 + Math.sin(index / 2) * 80 + (index % 5) * 20);
  const flat = Math.round(1400 + Math.cos(index / 3) * 120 + (index % 3) * 30);
  const peak = Math.round(1200 + Math.sin(index / 4) * 100);
  const sharp = Math.round(350 + (index % 4) * 40);
  const total = valley + flat + peak + sharp;
  const baseline = Math.round(total * 1.08);
  const outdoorTemp = (28 + Math.sin(index / 5) * 4).toFixed(1);

  return {
    date: `08/${day}`,
    valley,
    flat,
    peak,
    sharp,
    total,
    baseline,
    outdoorTemp: Number(outdoorTemp),
  };
});

const TOP_CONSUMERS = [
  { rank: 1, name: '1# 离心冷水主机 (CH-01)', category: '冷水机组', powerKW: 240, dailyKWh: 4250, share: '24.2%', cop: 6.2, status: '优' },
  { rank: 2, name: '2# 螺杆冷水主机 (CH-02)', category: '冷水机组', powerKW: 210, dailyKWh: 3820, share: '21.8%', cop: 5.8, status: '良' },
  { rank: 3, name: '1# 一次冷冻水泵 (CHWP-01)', category: '输配系统', powerKW: 45, dailyKWh: 860, share: '8.1%', cop: 4.8, status: '优' },
  { rank: 4, name: '2# 一次冷冻水泵 (CHWP-02)', category: '输配系统', powerKW: 42, dailyKWh: 810, share: '7.8%', cop: 4.7, status: '优' },
  { rank: 5, name: '1# 开式冷却塔 (CT-01)', category: '散热系统', powerKW: 22, dailyKWh: 430, share: '5.2%', cop: 5.1, status: '良' },
];

export function EnergyAnalyticsDashboard({ site }: EnergyAnalyticsDashboardProps) {
  const [period, setPeriod] = useState('month');

  type TopConsumer = (typeof TOP_CONSUMERS)[number];

  const consumerColumns = useMemo<Array<ColumnDef<DataTableFeatures, TopConsumer>>>(() => [
    {
      id: 'select',
      header: ({ table }) => (
        <Checkbox
          checked={table.getIsAllPageRowsSelected() ? true : table.getIsSomePageRowsSelected() ? 'indeterminate' : false}
          onCheckedChange={(checked) => table.toggleAllPageRowsSelected(Boolean(checked))}
          aria-label="全选重点用能设备"
        />
      ),
      cell: ({ row }) => (
        <Checkbox
          checked={row.getIsSelected()}
          onCheckedChange={(checked) => row.toggleSelected(Boolean(checked))}
          aria-label={`选择设备 ${row.original.rank}`}
        />
      ),
      enableSorting: false,
      enableHiding: false,
    },
    { id: 'rank', header: '序号', cell: ({ row }) => <span className="font-mono text-muted-foreground tabular-nums">{row.original.rank}</span> },
    { id: 'name', header: '设备名称', cell: ({ row }) => <span className="font-medium text-foreground">{row.original.name}</span> },
    { id: 'category', header: '系统分类', cell: ({ row }) => <span className="text-muted-foreground">{row.original.category}</span> },
    { id: 'powerKW', header: '实时功率', cell: ({ row }) => <span className="font-mono tabular-nums">{row.original.powerKW} kW</span> },
    { id: 'dailyKWh', header: '周期电量', cell: ({ row }) => <span className="font-mono font-semibold text-foreground tabular-nums">{row.original.dailyKWh} kWh</span> },
    { id: 'share', header: '能耗占比', cell: ({ row }) => <span className="font-mono text-muted-foreground tabular-nums">{row.original.share}</span> },
    { id: 'status', header: '能效等级', cell: ({ row }) => <StatusPillBadge tone={row.original.status === '优' ? 'success' : 'info'} label={row.original.status} /> },
  ], []);

  const consumerTable = useDataTable({
    key: 'surface-14-energy-top-consumers',
    data: [...TOP_CONSUMERS],
    columns: consumerColumns,
    pageSize: 10,
    getRowId: (row) => String(row.rank),
  });

  return (
    <Main className="space-y-6" data-testid="energy-analytics-dashboard">
      {/* Header & Controls */}
      <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <div className="flex items-center gap-2.5">
            <h1 className="text-2xl font-bold tracking-tight text-foreground">能源分析</h1>
            <Badge variant="outline" className="text-xs">
              分项计量模式
            </Badge>
          </div>
          <p className="mt-1 text-sm text-muted-foreground">
            {site.displayName} · {site.timezone} · 锚点周期：2026年8月 · 峰平谷负荷结构与能效对标
          </p>
        </div>

        <div className="flex flex-wrap items-center gap-2">
          <Select value={period} onValueChange={setPeriod}>
            <SelectTrigger className="h-8 w-24 text-xs">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="day">按日分析</SelectItem>
              <SelectItem value="week">按周分析</SelectItem>
              <SelectItem value="month">按月分析</SelectItem>
              <SelectItem value="year">按年分析</SelectItem>
            </SelectContent>
          </Select>

          <div className="flex items-center rounded-md border bg-background p-0.5 text-xs">
            <Button variant="ghost" size="sm" className="h-7 px-2 text-xs">
              <ChevronLeft className="size-3.5" />
              上一周期
            </Button>
            <span className="px-2 font-medium">2026年08月</span>
            <Button variant="ghost" size="sm" className="h-7 px-2 text-xs">
              下一周期
              <ChevronRight className="size-3.5" />
            </Button>
          </div>

          <Button variant="outline" size="sm" className="h-8 gap-1.5 text-xs">
            <Download className="size-3.5" />
            导出报表
          </Button>
          <Button variant="outline" size="sm" className="h-8 gap-1.5 text-xs">
            <RefreshCw className="size-3.5" />
            刷新
          </Button>
        </div>
      </div>

      {/* 4 Core Energy KPI Ribbon */}
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">周期综合用电</CardTitle>
            <Zap className="size-4 text-muted-foreground" aria-hidden="true" />
          </CardHeader>
          <CardContent className="space-y-1.5">
            <div className="flex items-baseline gap-2">
              <span className="text-2xl font-bold tracking-tight text-foreground tabular-nums">124.8</span>
              <span className="text-xs text-muted-foreground">MWh</span>
            </div>
            <div className="flex items-center gap-1.5 text-xs text-muted-foreground">
              <Badge variant="outline" className="gap-1 font-normal text-[11px] px-1.5 py-0">
                <ArrowDownRight className="size-3 inline" /> -4.2%
              </Badge>
              <span>较同期基准节约 5.5 MWh</span>
            </div>
          </CardContent>
        </Card>

        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">综合能源费用</CardTitle>
            <TrendingUp className="size-4 text-muted-foreground" aria-hidden="true" />
          </CardHeader>
          <CardContent className="space-y-1.5">
            <div className="flex items-baseline gap-2">
              <span className="text-2xl font-bold tracking-tight text-foreground tabular-nums">¥98,420</span>
              <span className="text-xs text-muted-foreground">CNY</span>
            </div>
            <div className="flex items-center gap-1.5 text-xs text-muted-foreground">
              <span className="font-medium text-foreground tabular-nums">¥0.789 / kWh</span>
              <span>· 尖峰电量占 18.2%</span>
            </div>
          </CardContent>
        </Card>

        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">单位面积能耗 (EUI)</CardTitle>
            <Gauge className="size-4 text-muted-foreground" aria-hidden="true" />
          </CardHeader>
          <CardContent className="space-y-1.5">
            <div className="flex items-baseline gap-2">
              <span className="text-2xl font-bold tracking-tight text-foreground tabular-nums">14.2</span>
              <span className="text-xs text-muted-foreground">kWh/m²·月</span>
            </div>
            <div className="flex items-center gap-1.5 text-xs text-muted-foreground">
              <Badge variant="outline" className="gap-1 font-normal text-[11px] px-1.5 py-0">
                <span className="size-1.5 rounded-full bg-emerald-500" />
                优级能效
              </Badge>
              <span>领先同类公共建筑 11.5%</span>
            </div>
          </CardContent>
        </Card>

        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">折合碳排放总量</CardTitle>
            <Leaf className="size-4 text-muted-foreground" aria-hidden="true" />
          </CardHeader>
          <CardContent className="space-y-1.5">
            <div className="flex items-baseline gap-2">
              <span className="text-2xl font-bold tracking-tight text-foreground tabular-nums">72.4</span>
              <span className="text-xs text-muted-foreground">tCO₂e</span>
            </div>
            <div className="text-xs text-muted-foreground">
              电网排放因子 0.5810 t/MWh
            </div>
          </CardContent>
        </Card>
      </div>

      {/* Main Dual Charts Row */}
      <div className="grid grid-cols-1 gap-6 xl:grid-cols-[minmax(0,1.6fr)_minmax(340px,1fr)]">
        {/* Left: Stacked Peak/Valley Electricity Trend */}
        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between pb-2">
            <div>
              <CardTitle className="text-base font-semibold">逐日用能趋势与电价峰谷分布</CardTitle>
              <CardDescription className="text-xs">
                每日电量堆叠分布 (谷电 / 平电 / 峰电 / 尖峰) 与同期基线对比 (kWh)
              </CardDescription>
            </div>
            <div className="flex items-center gap-2 text-xs">
              <div className="flex items-center gap-1">
                <span className="size-2.5 rounded-xs" style={{ background: TARIFF_COLORS.sharp }} />
                <span className="text-muted-foreground">尖峰</span>
              </div>
              <div className="flex items-center gap-1">
                <span className="size-2.5 rounded-xs" style={{ background: TARIFF_COLORS.peak }} />
                <span className="text-muted-foreground">峰电</span>
              </div>
              <div className="flex items-center gap-1">
                <span className="size-2.5 rounded-xs" style={{ background: TARIFF_COLORS.flat }} />
                <span className="text-muted-foreground">平电</span>
              </div>
              <div className="flex items-center gap-1">
                <span className="size-2.5 rounded-xs" style={{ background: TARIFF_COLORS.valley }} />
                <span className="text-muted-foreground">谷电</span>
              </div>
            </div>
          </CardHeader>
          <CardContent className="pt-4">
            <div className="h-[320px] w-full">
              <ResponsiveContainer width="100%" height="100%">
                <ComposedChart data={DAILY_TREND_DATA} margin={{ top: 10, right: 10, left: -15, bottom: 0 }}>
                  <CartesianGrid strokeDasharray="3 3" vertical={false} stroke="#e4e4e7" opacity={0.6} />
                  <XAxis dataKey="date" tickLine={false} axisLine={false} tick={{ fontSize: 11, fill: '#71717a' }} />
                  <YAxis tickLine={false} axisLine={false} tick={{ fontSize: 11, fill: '#71717a' }} />
                  <Tooltip
                    content={({ active, payload, label }) => {
                      if (!active || !payload || !payload.length) return null;
                      const data = payload[0].payload;
                      return (
                        <div className="rounded-lg border bg-popover p-3 text-xs shadow-md">
                          <div className="font-semibold text-foreground mb-1.5">{label} 能耗详情</div>
                          <div className="space-y-1">
                            <div className="flex justify-between gap-4">
                              <span className="text-muted-foreground">尖峰电量:</span>
                              <span className="tabular-nums font-medium">{data.sharp} kWh</span>
                            </div>
                            <div className="flex justify-between gap-4">
                              <span className="text-muted-foreground">峰段电量:</span>
                              <span className="tabular-nums font-medium">{data.peak} kWh</span>
                            </div>
                            <div className="flex justify-between gap-4">
                              <span className="text-muted-foreground">平段电量:</span>
                              <span className="tabular-nums font-medium">{data.flat} kWh</span>
                            </div>
                            <div className="flex justify-between gap-4">
                              <span className="text-muted-foreground">谷段电量:</span>
                              <span className="tabular-nums font-medium">{data.valley} kWh</span>
                            </div>
                            <div className="border-t pt-1 flex justify-between gap-4 font-semibold">
                              <span>当日总量:</span>
                              <span className="text-primary tabular-nums">{data.total} kWh</span>
                            </div>
                            <div className="flex justify-between gap-4 text-emerald-600">
                              <span>基线对比:</span>
                              <span className="tabular-nums font-medium">-{data.baseline - data.total} kWh</span>
                            </div>
                          </div>
                        </div>
                      );
                    }}
                  />
                  <Bar dataKey="valley" stackId="tariff" fill={TARIFF_COLORS.valley} radius={[0, 0, 0, 0]} />
                  <Bar dataKey="flat" stackId="tariff" fill={TARIFF_COLORS.flat} radius={[0, 0, 0, 0]} />
                  <Bar dataKey="peak" stackId="tariff" fill={TARIFF_COLORS.peak} radius={[0, 0, 0, 0]} />
                  <Bar dataKey="sharp" stackId="tariff" fill={TARIFF_COLORS.sharp} radius={[4, 4, 0, 0]} />
                  <Line type="monotone" dataKey="baseline" stroke="#94a3b8" strokeDasharray="4 4" dot={false} strokeWidth={1.5} name="能耗基线" />
                </ComposedChart>
              </ResponsiveContainer>
            </div>
          </CardContent>
        </Card>

        {/* Right: Subsystem Breakdown Donut */}
        <Card className="shadow-xs">
          <CardHeader className="pb-2">
            <CardTitle className="text-base font-semibold">用能系统分项结构</CardTitle>
            <CardDescription className="text-xs">
              各子系统电耗构成与占比
            </CardDescription>
          </CardHeader>
          <CardContent className="pt-2">
            <div className="h-[180px] w-full relative flex items-center justify-center">
              <ResponsiveContainer width="100%" height="100%">
                <PieChart>
                  <Pie
                    data={BREAKDOWN_DATA}
                    innerRadius={50}
                    outerRadius={75}
                    paddingAngle={3}
                    dataKey="value"
                  >
                    {BREAKDOWN_DATA.map((entry, index) => (
                      <Cell key={`cell-${index}`} fill={entry.color} />
                    ))}
                  </Pie>
                  <Tooltip
                    content={({ active, payload }) => {
                      if (!active || !payload || !payload.length) return null;
                      const d = payload[0].payload;
                      return (
                        <div className="rounded-md border bg-popover px-2.5 py-1.5 text-xs shadow-sm">
                          <span className="font-semibold">{d.name}</span>: <span className="tabular-nums font-medium">{d.value} MWh ({d.percent}%)</span>
                        </div>
                      );
                    }}
                  />
                </PieChart>
              </ResponsiveContainer>
              <div className="absolute flex flex-col items-center justify-center pointer-events-none">
                <span className="text-xl font-bold tabular-nums tracking-tight">860</span>
                <span className="text-[10px] text-muted-foreground uppercase">MWh 累计</span>
              </div>
            </div>

            {/* Breakdown List */}
            <div className="mt-3 space-y-2 border-t pt-3">
              {BREAKDOWN_DATA.map((item) => (
                <div key={item.name} className="flex items-center justify-between text-xs">
                  <div className="flex items-center gap-2">
                    <span className="size-2 rounded-full" style={{ backgroundColor: item.color }} />
                    <span className="text-muted-foreground">{item.name}</span>
                  </div>
                  <div className="flex items-center gap-3">
                    <div className="w-16 h-1.5 rounded-full bg-muted overflow-hidden">
                      <div className="h-full rounded-full" style={{ width: `${item.percent}%`, backgroundColor: item.color }} />
                    </div>
                    <span className="tabular-nums font-medium text-foreground w-12 text-right">{item.percent}%</span>
                  </div>
                </div>
              ))}
            </div>
          </CardContent>
        </Card>
      </div>

      {/* Bottom Section: Top Consumers & AI Insights */}
      <div className="grid grid-cols-1 gap-6 lg:grid-cols-[1.4fr_1fr]">
        {/* Left: Top Consumers Table */}
        <Card className="shadow-xs">
          <CardHeader className="pb-3">
            <div className="flex items-center justify-between">
              <div>
                <CardTitle className="text-base font-semibold">主要用能设备贡献排行</CardTitle>
                <CardDescription className="text-xs">
                  按当前周期用电量降序排列
                </CardDescription>
              </div>
              <Badge variant="outline" className="text-xs">Top 5 重点设备</Badge>
            </div>
          </CardHeader>
          <CardContent className="p-0">
            <DataTable
              table={consumerTable}
              className="gap-0"
              tableAriaLabel="主要用能设备贡献排行"
              getHeaderRowProps={() => ({ className: 'bg-muted/30 hover:bg-transparent' })}
              getHeaderCellProps={(header) => ({
                className:
                  header.id === 'select' || header.id === 'rank' ? 'w-12 text-center text-xs' :
                  ['powerKW', 'dailyKWh', 'share'].includes(header.id) ? 'text-right text-xs' :
                  header.id === 'status' ? 'w-28 text-center text-xs' :
                  'text-xs',
              })}
              getRowProps={() => ({ className: 'text-xs hover:bg-muted/40' })}
              getCellProps={(cell) => ({
                className:
                  cell.column.id === 'select' || cell.column.id === 'rank' || cell.column.id === 'status'
                    ? 'text-center'
                    : ['powerKW', 'dailyKWh', 'share'].includes(cell.column.id)
                      ? 'text-right'
                      : undefined,
              })}
              footer={(
                <DataTablePagination
                  table={consumerTable}
                  totalRows={TOP_CONSUMERS.length}
                />
              )}
            />
          </CardContent>
        </Card>

        {/* Right: AI Energy Saving Insights */}
        <Card className="shadow-xs">
          <CardHeader className="pb-3">
            <div className="flex items-center justify-between">
              <div>
                <CardTitle className="text-base font-semibold">能效优化与节电建议</CardTitle>
                <CardDescription className="text-xs">
                  基于历史基线与实时负荷分析生成的建议
                </CardDescription>
              </div>
              <div className="flex size-7 items-center justify-center rounded-md bg-muted text-muted-foreground">
                <Flame className="size-4" />
              </div>
            </div>
          </CardHeader>
          <CardContent className="space-y-3 pt-0">
            <div className="rounded-lg border bg-muted/30 p-3 text-xs space-y-1.5">
              <div className="flex items-center justify-between font-semibold text-foreground">
                <span>夜间低负荷工况基础能耗优化</span>
                <Badge variant="outline" className="gap-1 text-[10px] font-normal">
                  <span className="size-1.5 rounded-full bg-amber-500" />
                  高收益
                </Badge>
              </div>
              <p className="text-muted-foreground leading-relaxed">
                凌晨 02:00-05:00 期间冷冻水泵仍以 46Hz 恒频运行，建议调整末端压差设定，预计月度可节电 <strong className="text-foreground">1,820 kWh</strong>。
              </p>
            </div>

            <div className="rounded-lg border bg-muted/30 p-3 text-xs space-y-1.5">
              <div className="flex items-center justify-between font-semibold text-foreground">
                <span>峰谷电价移峰填谷策略</span>
                <Badge variant="outline" className="gap-1 text-[10px] font-normal">
                  <span className="size-1.5 rounded-full bg-sky-500" />
                  电价优化
                </Badge>
              </div>
              <p className="text-muted-foreground leading-relaxed">
                在 06:00-07:30 谷电时段提前实施建筑蓄冷预冷，可将 09:00-11:00 尖峰时段主机负荷降低约 <strong className="text-foreground">15%</strong>。
              </p>
            </div>

            <div className="rounded-lg border bg-muted/30 p-3 text-xs space-y-1.5">
              <div className="flex items-center justify-between font-semibold text-foreground">
                <span>冷却塔进出水逼近度改善</span>
                <Badge variant="outline" className="gap-1 text-[10px] font-normal">
                  <span className="size-1.5 rounded-full bg-emerald-500" />
                  设备保养
                </Badge>
              </div>
              <p className="text-muted-foreground leading-relaxed">
                CT-01 散热逼近度由 3.1K 偏离至 4.6K，清洗填料后预计可降低主机冷凝温度 1.2°C，提高主机 COP <strong className="text-foreground">3.5%</strong>。
              </p>
            </div>
          </CardContent>
        </Card>
      </div>
    </Main>
  );
}
