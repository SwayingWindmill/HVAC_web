import { useMemo, useState } from 'react';
import type { ColumnDef } from '@tanstack/react-table';
import {
  ArrowDownRight,
  ArrowUpRight,
  ChevronRight,
  Droplets,
  Gauge,
  RadioTower,
  RefreshCw,
  Snowflake,
  Sparkles,
  Zap,
} from 'lucide-react';
import {
  Area,
  AreaChart,
  CartesianGrid,
  Line,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts';
import type { CurrentPrincipalResponse, Site } from '@/api/generated/platformGateway.gen';
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
import { Checkbox } from '@/components/ui/checkbox';
import { Main } from '@/components/layout/Main';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { cn } from '@/lib/utils';

export interface SubsystemItem {
  readonly code: string;
  readonly name: string;
  readonly status: string;
  readonly load: number;
  readonly power: number;
  readonly cop?: number | null;
  readonly freq?: string;
  readonly flow?: string;
  readonly chwOut?: string;
  readonly cwIn?: string;
  readonly approach?: string;
}

export interface EquipmentGroupItem {
  readonly id: string;
  readonly name: string;
  readonly category: string;
  readonly icon: typeof Snowflake;
  readonly running: number;
  readonly total: number;
  readonly powerKW: number;
  readonly powerShare: number;
  readonly ratedPowerKW: number;
  readonly cop: number | null;
  readonly subsystems: readonly SubsystemItem[];
}

export interface OperationsConsoleProps {
  readonly site?: Readonly<Site>;
  readonly principal?: CurrentPrincipalResponse;
  readonly searchState?: { readonly group?: string };
  readonly onSearchChange?: (patch: { group?: string }) => void;
}

// 24小时系统工况趋势数据
const SYSTEM_HOURLY_PROFILE = [
  { time: '00:00', loadKW: 180, powerKW: 38, cop: 4.74, chwSupplyTemp: 7.8, chwReturnTemp: 11.2 },
  { time: '02:00', loadKW: 150, powerKW: 32, cop: 4.69, chwSupplyTemp: 7.9, chwReturnTemp: 11.0 },
  { time: '04:00', loadKW: 140, powerKW: 30, cop: 4.67, chwSupplyTemp: 8.0, chwReturnTemp: 10.9 },
  { time: '06:00', loadKW: 210, powerKW: 42, cop: 5.00, chwSupplyTemp: 7.5, chwReturnTemp: 11.8 },
  { time: '08:00', loadKW: 380, powerKW: 68, cop: 5.59, chwSupplyTemp: 7.2, chwReturnTemp: 12.4 },
  { time: '10:00', loadKW: 520, powerKW: 88, cop: 5.91, chwSupplyTemp: 7.0, chwReturnTemp: 12.8 },
  { time: '12:00', loadKW: 580, powerKW: 96, cop: 6.04, chwSupplyTemp: 7.0, chwReturnTemp: 13.0 },
  { time: '14:00', loadKW: 620, powerKW: 102, cop: 6.08, chwSupplyTemp: 6.9, chwReturnTemp: 13.2 },
  { time: '16:00', loadKW: 540, powerKW: 91, cop: 5.93, chwSupplyTemp: 7.1, chwReturnTemp: 12.7 },
  { time: '18:00', loadKW: 430, powerKW: 75, cop: 5.73, chwSupplyTemp: 7.3, chwReturnTemp: 12.2 },
  { time: '20:00', loadKW: 310, powerKW: 57, cop: 5.44, chwSupplyTemp: 7.5, chwReturnTemp: 11.8 },
  { time: '22:00', loadKW: 240, powerKW: 46, cop: 5.22, chwSupplyTemp: 7.7, chwReturnTemp: 11.4 },
];

// 设备群组状态清单
const EQUIPMENT_GROUPS: readonly EquipmentGroupItem[] = [
  {
    id: 'chillers',
    name: '冷水机组群',
    category: 'chiller',
    icon: Snowflake,
    running: 2,
    total: 3,
    powerKW: 185.4,
    powerShare: 68,
    ratedPowerKW: 280,
    cop: 5.82,
    subsystems: [
      { code: 'CH-01', name: '1# 离心冷水机组', status: 'running', load: 84, cop: 5.91, power: 94.2, chwOut: '7.0°C', cwIn: '29.2°C' },
      { code: 'CH-02', name: '2# 离心冷水机组', status: 'running', load: 78, cop: 5.73, power: 91.2, chwOut: '7.1°C', cwIn: '29.4°C' },
      { code: 'CH-03', name: '3# 螺杆备用机组', status: 'standby', load: 0, cop: 0, power: 0, chwOut: '—', cwIn: '—' },
    ],
  },
  {
    id: 'pumps',
    name: '冷水泵群',
    category: 'pump',
    icon: Droplets,
    running: 3,
    total: 4,
    powerKW: 38.6,
    powerShare: 14,
    ratedPowerKW: 60,
    cop: null,
    subsystems: [
      { code: 'CHWP-01', name: '1# 一次冷水泵', status: 'running', load: 85, freq: '48.5 Hz', flow: '320 m³/h', power: 13.2 },
      { code: 'CHWP-02', name: '2# 一次冷水泵', status: 'running', load: 82, freq: '47.0 Hz', flow: '310 m³/h', power: 12.8 },
      { code: 'CHWP-03', name: '3# 一次冷水泵', status: 'running', load: 80, freq: '46.0 Hz', flow: '305 m³/h', power: 12.6 },
      { code: 'CHWP-04', name: '4# 备用冷水泵', status: 'standby', load: 0, freq: '0.0 Hz', flow: '0 m³/h', power: 0 },
    ],
  },
  {
    id: 'towers',
    name: '冷却塔群',
    category: 'tower',
    icon: RadioTower,
    running: 3,
    total: 4,
    powerKW: 18.2,
    powerShare: 7,
    ratedPowerKW: 30,
    cop: null,
    subsystems: [
      { code: 'CT-01', name: '1# 闭式冷却塔', status: 'running', load: 80, freq: '42.0 Hz', approach: '2.8 K', power: 6.1 },
      { code: 'CT-02', name: '2# 闭式冷却塔', status: 'running', load: 75, freq: '38.5 Hz', approach: '2.8 K', power: 6.1 },
      { code: 'CT-03', name: '3# 闭式冷却塔', status: 'running', load: 72, freq: '36.8 Hz', approach: '2.9 K', power: 6.0 },
      { code: 'CT-04', name: '4# 闭式冷却塔', status: 'standby', load: 0, freq: '0.0 Hz', approach: '—', power: 0 },
    ],
  },
];

export function OperationsConsole({
  site,
  principal: _principal,
  searchState,
  onSearchChange,
}: OperationsConsoleProps) {
  const [selectedGroup, setSelectedGroup] = useState<string>(searchState?.group ?? 'chillers');
  const [autoRefresh, setAutoRefresh] = useState(true);
  const activeGroupData = useMemo(() => {
    return EQUIPMENT_GROUPS.find((g) => g.id === selectedGroup) ?? EQUIPMENT_GROUPS[0];
  }, [selectedGroup]);

  const groupPills: readonly DataTableViewPillOption[] = useMemo(() => {
    return EQUIPMENT_GROUPS.map((g) => ({
      id: g.id,
      label: g.name,
      count: `${g.running}/${g.total}`,
    }));
  }, []);

  const columns = useMemo<Array<ColumnDef<DataTableFeatures, SubsystemItem>>>(() => [
    {
      id: 'select',
      header: ({ table }) => (
        <Checkbox
          checked={table.getIsAllPageRowsSelected() ? true : table.getIsSomePageRowsSelected() ? 'indeterminate' : false}
          onCheckedChange={(checked) => table.toggleAllPageRowsSelected(Boolean(checked))}
          aria-label="选择全部设备"
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
    {
      id: 'name',
      header: '设备编号及名称',
      cell: ({ row }) => <div className="flex flex-col font-medium"><span className="font-semibold text-foreground">{row.original.name}</span><span className="font-mono text-[11px] text-muted-foreground">{row.original.code}</span></div>,
    },
    {
      id: 'status',
      header: '运行状态',
      cell: ({ row }) => {
        const isRunning = row.original.status === 'running';
        return <StatusPillBadge tone={isRunning ? 'success' : 'neutral'} pulse={isRunning} label={isRunning ? '运行中' : '热备用'} />;
      },
    },
    {
      id: 'load',
      header: '负荷率 / 频率',
      cell: ({ row }) => {
        const isRunning = row.original.status === 'running';
        return <div className="w-28 space-y-1 font-medium"><div className="flex justify-between text-[11px] text-muted-foreground tabular-nums"><span>{row.original.load > 0 ? `${row.original.load}%` : '0%'}</span><span>{row.original.freq ?? ''}</span></div><div className="h-1.5 w-full overflow-hidden rounded-full bg-muted"><div className={cn('h-full rounded-full', isRunning ? 'bg-primary' : 'bg-muted-foreground/30')} style={{ width: `${row.original.load}%` }} /></div></div>;
      },
    },
    { id: 'power', header: '当前功率', cell: ({ row }) => <span className="font-medium tabular-nums">{row.original.power > 0 ? `${row.original.power} kW` : '0.0 kW'}</span> },
    {
      id: 'points',
      header: '关键工艺点位',
      cell: ({ row }) => (
        <div className="text-muted-foreground">
          {row.original.chwOut ? <div className="flex gap-2"><span>出水: <strong className="text-foreground tabular-nums">{row.original.chwOut}</strong></span><span>进水: <strong className="text-foreground tabular-nums">{row.original.cwIn}</strong></span></div> : null}
          {row.original.flow ? <span>流量: <strong className="text-foreground tabular-nums">{row.original.flow}</strong></span> : null}
        </div>
      ),
    },
    {
      id: 'efficiency',
      header: '能效 / 逼近度',
      cell: ({ row }) => <span className="font-semibold tabular-nums">{row.original.cop != null && row.original.cop > 0 ? `COP ${row.original.cop}` : row.original.approach ?? '—'}</span>,
    },
    {
      id: 'actions',
      header: '操作',
      cell: () => <Button variant="ghost" size="sm" className="h-7 gap-1 text-xs text-muted-foreground hover:text-foreground">控制调试<ChevronRight className="size-3" /></Button>,
      enableSorting: false,
      enableHiding: false,
    },
  ], []);

  const table = useDataTable({
    key: `surface-05-operations-${selectedGroup}`,
    data: [...activeGroupData.subsystems],
    columns,
    pageSize: 10,
    getRowId: (row) => row.code,
  });

  return (
    <Main className="space-y-6" data-testid="operations-console">
      {/* 顶部标题栏与控制状态条 */}
      <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <div className="flex items-center gap-2.5">
            <h1 className="text-2xl font-bold tracking-tight text-foreground">系统运行</h1>
            <Badge variant="outline" className="gap-1.5 text-xs font-normal">
              <span className="size-1.5 rounded-full bg-emerald-500 animate-pulse" />
              智能群控在线
            </Badge>
            <Badge variant="secondary" className="text-xs font-normal">
              COP 5.82 优良
            </Badge>
          </div>
          <p className="mt-1 text-sm text-muted-foreground">
            {site?.displayName ?? '东京中央冷站'} · 实时暖通回路、工艺测点与设备群控状态监视
          </p>
        </div>

        <div className="flex items-center gap-2">
          <Button
            variant="outline"
            size="sm"
            onClick={() => setAutoRefresh(!autoRefresh)}
            className="gap-1.5 text-xs font-medium"
          >
            <RefreshCw className={cn('size-3.5', autoRefresh && 'text-foreground')} />
            {autoRefresh ? '自动刷新 5s' : '已暂停'}
          </Button>
          <Button variant="default" size="sm" className="gap-1.5 text-xs font-medium">
            <Sparkles className="size-3.5" />
            一键优化调度
          </Button>
        </div>
      </div>

      {/* 5 联关键工况 KPI 指标带 (Standard shadcn grammar) */}
      <div className="grid grid-cols-2 gap-4 sm:grid-cols-3 lg:grid-cols-5">
        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">当前冷负荷</CardTitle>
            <Snowflake className="size-4 text-muted-foreground" aria-hidden="true" />
          </CardHeader>
          <CardContent className="space-y-1">
            <div className="text-2xl font-bold tracking-tight text-foreground tabular-nums">
              504 <span className="text-xs font-normal text-muted-foreground">kW</span>
            </div>
            <p className="text-xs text-muted-foreground flex items-center gap-1">
              <ArrowDownRight className="size-3 text-muted-foreground" />
              较设计基准 -8.5%
            </p>
          </CardContent>
        </Card>

        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">综合制冷能效</CardTitle>
            <Gauge className="size-4 text-muted-foreground" aria-hidden="true" />
          </CardHeader>
          <CardContent className="space-y-1">
            <div className="text-2xl font-bold tracking-tight text-foreground tabular-nums">
              5.82 <span className="text-xs font-normal text-muted-foreground">COP</span>
            </div>
            <p className="text-xs text-muted-foreground flex items-center gap-1">
              <ArrowUpRight className="size-3 text-muted-foreground" />
              超一级能效标准
            </p>
          </CardContent>
        </Card>

        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">冷水总用电功率</CardTitle>
            <Zap className="size-4 text-muted-foreground" aria-hidden="true" />
          </CardHeader>
          <CardContent className="space-y-1">
            <div className="text-2xl font-bold tracking-tight text-foreground tabular-nums">
              86.6 <span className="text-xs font-normal text-muted-foreground">kW</span>
            </div>
            <p className="text-xs text-muted-foreground truncate">
              冷水机组 71% · 水泵 21% · 塔 8%
            </p>
          </CardContent>
        </Card>

        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">冷冻水供回工况</CardTitle>
            <Droplets className="size-4 text-muted-foreground" aria-hidden="true" />
          </CardHeader>
          <CardContent className="space-y-1">
            <div className="text-2xl font-bold tracking-tight text-foreground tabular-nums">
              7.0 / 12.8 <span className="text-xs font-normal text-muted-foreground">°C</span>
            </div>
            <p className="text-xs text-muted-foreground truncate">
              温差 ΔT 5.8 K · 流量 185 m³/h
            </p>
          </CardContent>
        </Card>

        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">冷却水供回工况</CardTitle>
            <RadioTower className="size-4 text-muted-foreground" aria-hidden="true" />
          </CardHeader>
          <CardContent className="space-y-1">
            <div className="text-2xl font-bold tracking-tight text-foreground tabular-nums">
              29.2 / 34.1 <span className="text-xs font-normal text-muted-foreground">°C</span>
            </div>
            <p className="text-xs text-muted-foreground truncate">
              逼近度 2.8 K · 流量 240 m³/h
            </p>
          </CardContent>
        </Card>
      </div>

      {/* 中部双栏：24小时负荷与COP动态趋势 + 回路过程量孪生监视 */}
      <div className="grid grid-cols-1 gap-6 lg:grid-cols-3">
        {/* 左侧 2/3: 实时负荷与能效动态曲线 */}
        <Card className="lg:col-span-2 shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between pb-2">
            <div>
              <CardTitle className="text-base font-semibold">24小时冷负荷与能效 COP 动态轨迹</CardTitle>
              <CardDescription className="text-xs text-muted-foreground">
                瞬时冷负荷（kW）与系统综合 COP 联动响应关系
              </CardDescription>
            </div>
            <div className="flex items-center gap-3 text-xs text-muted-foreground">
              <span className="flex items-center gap-1.5">
                <span className="size-2 rounded-full bg-primary" />
                冷负荷 (kW)
              </span>
              <span className="flex items-center gap-1.5">
                <span className="size-2 rounded-full bg-emerald-500" />
                系统 COP
              </span>
            </div>
          </CardHeader>
          <CardContent className="pt-2">
            <div className="h-72 w-full">
              <ResponsiveContainer width="100%" height="100%">
                <AreaChart data={SYSTEM_HOURLY_PROFILE} margin={{ top: 10, right: 20, left: -10, bottom: 0 }}>
                  <defs>
                    <linearGradient id="loadKWGrad" x1="0" y1="0" x2="0" y2="1">
                      <stop offset="5%" stopColor="var(--primary)" stopOpacity={0.25} />
                      <stop offset="95%" stopColor="var(--primary)" stopOpacity={0.0} />
                    </linearGradient>
                  </defs>
                  <CartesianGrid strokeDasharray="3 3" vertical={false} stroke="var(--border)" strokeOpacity={0.6} />
                  <XAxis dataKey="time" stroke="var(--muted-foreground)" fontSize={12} tickLine={false} />
                  <YAxis yAxisId="left" stroke="var(--muted-foreground)" fontSize={12} tickLine={false} domain={[0, 700]} />
                  <YAxis yAxisId="right" orientation="right" stroke="var(--muted-foreground)" fontSize={12} tickLine={false} domain={[3, 7]} />
                  <Tooltip
                    contentStyle={{
                      backgroundColor: 'var(--popover)',
                      borderColor: 'var(--border)',
                      borderRadius: '8px',
                      fontSize: '12px',
                    }}
                  />
                  <Area
                    yAxisId="left"
                    type="monotone"
                    dataKey="loadKW"
                    name="冷负荷 (kW)"
                    stroke="var(--primary)"
                    strokeWidth={2}
                    fillOpacity={1}
                    fill="url(#loadKWGrad)"
                  />
                  <Line
                    yAxisId="right"
                    type="monotone"
                    dataKey="cop"
                    name="系统 COP"
                    stroke="#10b981"
                    strokeWidth={2}
                    dot={{ r: 3, fill: '#10b981' }}
                  />
                </AreaChart>
              </ResponsiveContainer>
            </div>
          </CardContent>
        </Card>

        {/* 右侧 1/3: 冷站双回路水力热工过程参数 */}
        <Card className="shadow-xs flex flex-col justify-between">
          <CardHeader className="pb-3">
            <CardTitle className="text-base font-semibold">回路水力工况孪生</CardTitle>
            <CardDescription className="text-xs text-muted-foreground">
              冷水输配与冷却循环水力平衡
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            {/* 冷冻水回路 */}
            <div className="rounded-lg border bg-muted/20 p-3.5 space-y-3">
              <div className="flex items-center justify-between">
                <div className="flex items-center gap-2">
                  <span className="size-2 rounded-full bg-primary" />
                  <span className="text-xs font-semibold text-foreground">
                    冷冻水循环回路 (一次定频+二次变频)
                  </span>
                </div>
                <Badge variant="outline" className="text-[10px] font-normal">
                  平衡正常
                </Badge>
              </div>
              <div className="grid grid-cols-2 gap-3 text-xs">
                <div>
                  <span className="text-muted-foreground block text-[11px]">供水设定/实际</span>
                  <span className="font-semibold text-foreground tabular-nums">7.0°C / 7.0°C</span>
                </div>
                <div>
                  <span className="text-muted-foreground block text-[11px]">回水温度/温差</span>
                  <span className="font-semibold text-foreground tabular-nums">12.8°C / 5.8 K</span>
                </div>
                <div>
                  <span className="text-muted-foreground block text-[11px]">循环总流量</span>
                  <span className="font-semibold text-foreground tabular-nums">185.0 m³/h</span>
                </div>
                <div>
                  <span className="text-muted-foreground block text-[11px]">最不利末端压差</span>
                  <span className="font-semibold text-foreground tabular-nums">0.18 MPa (达标)</span>
                </div>
              </div>
            </div>

            {/* 冷却水回路 */}
            <div className="rounded-lg border bg-muted/20 p-3.5 space-y-3">
              <div className="flex items-center justify-between">
                <div className="flex items-center gap-2">
                  <span className="size-2 rounded-full bg-foreground/60" />
                  <span className="text-xs font-semibold text-foreground">
                    冷却水散热回路 (变频风机+水泵)
                  </span>
                </div>
                <Badge variant="outline" className="text-[10px] font-normal">
                  高效散热
                </Badge>
              </div>
              <div className="grid grid-cols-2 gap-3 text-xs">
                <div>
                  <span className="text-muted-foreground block text-[11px]">冷却水出/进温</span>
                  <span className="font-semibold text-foreground tabular-nums">29.2°C / 34.1°C</span>
                </div>
                <div>
                  <span className="text-muted-foreground block text-[11px]">室外湿球温度</span>
                  <span className="font-semibold text-foreground tabular-nums">26.4°C (逼近 2.8K)</span>
                </div>
                <div>
                  <span className="text-muted-foreground block text-[11px]">循环总流量</span>
                  <span className="font-semibold text-foreground tabular-nums">240.0 m³/h</span>
                </div>
                <div>
                  <span className="text-muted-foreground block text-[11px]">塔风机总功率</span>
                  <span className="font-semibold text-foreground tabular-nums">18.2 kW (75%)</span>
                </div>
              </div>
            </div>
          </CardContent>
        </Card>
      </div>

      {/* 下部：设备群控矩阵与单机实时测点 */}
      <Card className="shadow-xs">
        <CardHeader className="pb-3">
          <div className="flex flex-col gap-3 md:flex-row md:items-center md:justify-between">
            <div>
              <CardTitle className="text-base font-semibold">冷站设备群控矩阵</CardTitle>
              <CardDescription className="text-xs text-muted-foreground">
                按工艺段群控策略划分的单机运行遥测与负荷调度
              </CardDescription>
            </div>

            <div className="flex flex-wrap items-center gap-2">
              <DataTableViewOptions table={table} label="自定义列" />
            </div>
          </div>

          <div className="mt-3">
            <DataTableViewPills
              options={groupPills}
              value={selectedGroup}
              onValueChange={(val) => {
                setSelectedGroup(val);
                table.setPageIndex(0);
                onSearchChange?.({ group: val });
              }}
            />
          </div>
        </CardHeader>

        <CardContent className="p-0">
          <DataTable
            table={table}
            className="gap-0"
            tableAriaLabel="冷站设备群控矩阵"
            getHeaderRowProps={() => ({ className: 'bg-muted/20 hover:bg-transparent' })}
            getHeaderCellProps={(header) => ({
              className:
                header.id === 'select' ? 'w-[40px] pl-4' :
                header.id === 'name' ? 'w-[180px] text-xs font-semibold' :
                header.id === 'actions' ? 'pr-4 text-right text-xs font-semibold' :
                'text-xs font-semibold',
            })}
            getRowProps={() => ({ className: 'text-xs transition-colors hover:bg-muted/30' })}
            getCellProps={(cell) => ({
              className:
                cell.column.id === 'select' ? 'pl-4' :
                cell.column.id === 'actions' ? 'pr-4 text-right' :
                undefined,
            })}
            footer={(
              <DataTablePagination
                table={table}
                totalRows={activeGroupData.subsystems.length}
                
                
                
                
                
              />
            )}
          />
        </CardContent>
      </Card>
    </Main>
  );
}
