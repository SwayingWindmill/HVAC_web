import { useState, useMemo } from 'react';
import type { ColumnDef } from '@tanstack/react-table';
import {
  Sun,
  BatteryCharging,
  Zap,
  RotateCcw,
  ShieldCheck,
  Search,
} from 'lucide-react';
import {
  ResponsiveContainer,
  Area,
  Line,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  Legend,
  ComposedChart,
} from 'recharts';
import { Main } from '@/components/layout/Main';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Progress } from '@/components/ui/progress';
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

interface HourlyDispatchRecord {
  readonly hour: string;
  readonly loadKW: number;
  readonly pvKW: number;
  readonly bessKW: number; // positive = discharging, negative = charging
  readonly soc: number; // 0 - 100%
}

const MOCK_DISPATCH_CURVE: readonly HourlyDispatchRecord[] = [
  { hour: '00:00', loadKW: 280, pvKW: 0, bessKW: -100, soc: 95 },
  { hour: '02:00', loadKW: 250, pvKW: 0, bessKW: -100, soc: 100 },
  { hour: '04:00', loadKW: 240, pvKW: 0, bessKW: 0, soc: 100 },
  { hour: '06:00', loadKW: 310, pvKW: 15, bessKW: 0, soc: 100 },
  { hour: '08:00', loadKW: 580, pvKW: 120, bessKW: 120, soc: 82 },
  { hour: '10:00', loadKW: 820, pvKW: 280, bessKW: 150, soc: 60 },
  { hour: '12:00', loadKW: 750, pvKW: 320, bessKW: -80, soc: 72 }, // Valley charging noon
  { hour: '14:00', loadKW: 920, pvKW: 290, bessKW: 180, soc: 48 }, // Peak shaving
  { hour: '16:00', loadKW: 880, pvKW: 180, bessKW: 180, soc: 25 },
  { hour: '18:00', loadKW: 680, pvKW: 40, bessKW: 60, soc: 18 },
  { hour: '20:00', loadKW: 490, pvKW: 0, bessKW: 0, soc: 18 },
  { hour: '22:00', loadKW: 360, pvKW: 0, bessKW: -100, soc: 45 },
];

const DER_EQUIPMENT = [
  { id: 'der-01', name: '屋顶光伏组串逆变器 (INV-01)', type: '分布式光伏', capacity: '175 kW', output: '168.2 kW', energyToday: '942 kWh', status: 'normal', mode: 'MPPT 最大功率跟踪' },
  { id: 'der-02', name: '车棚光伏组串逆变器 (INV-02)', type: '分布式光伏', capacity: '150 kW', output: '142.3 kW', energyToday: '815 kWh', status: 'normal', mode: 'MPPT 最大功率跟踪' },
  { id: 'der-03', name: '储能 PCS 变流升压一体机 (PCS-01)', type: '电化学储能', capacity: '250 kW / 500 kWh', output: '+180.0 kW (放电)', energyToday: '放电 620 kWh', status: 'normal', mode: '峰谷套利与需量削峰' },
  { id: 'der-04', name: '储能磷酸铁锂电池簇群 (BAT-01)', type: '储能电池', capacity: '500 kWh (16簇)', output: 'SoC 68.5% | SOH 98.2%', energyToday: '电芯温差 1.8°C', status: 'normal', mode: '液冷恒温循环正常' },
  { id: 'der-05', name: '园区柔性充电桩群 (EVSE-01~12)', type: '充电微网', capacity: '120 kW (12枪)', output: '45.8 kW', energyToday: '充电 280 kWh', status: 'normal', mode: '有序平抑微网调度' },
];

export function DistributedEnergyWorkspace({ siteId: _siteId }: { readonly siteId?: string }) {
  const [filterType, setFilterType] = useState<string>('all');
  const [searchQuery, setSearchQuery] = useState('');
  const pillOptions = useMemo<readonly DataTableViewPillOption[]>(() => [
    { id: 'all', label: '全部设备', count: DER_EQUIPMENT.length },
    { id: 'pv', label: '分布式光伏', count: DER_EQUIPMENT.filter((e) => e.type.includes('光伏')).length },
    { id: 'bess', label: '电化学储能', count: DER_EQUIPMENT.filter((e) => e.type.includes('储能')).length },
    { id: 'ev', label: '充电微网', count: DER_EQUIPMENT.filter((e) => e.type.includes('充电')).length },
  ], []);

  const filtered = useMemo(() => {
    return DER_EQUIPMENT.filter((item) => {
      if (filterType === 'pv' && !item.type.includes('光伏')) return false;
      if (filterType === 'bess' && !item.type.includes('储能')) return false;
      if (filterType === 'ev' && !item.type.includes('充电')) return false;
      if (searchQuery && !item.name.toLowerCase().includes(searchQuery.toLowerCase()) && !item.id.toLowerCase().includes(searchQuery.toLowerCase())) {
        return false;
      }
      return true;
    });
  }, [filterType, searchQuery]);

  type DerEquipmentRow = (typeof DER_EQUIPMENT)[number];

  const columns = useMemo<Array<ColumnDef<DataTableFeatures, DerEquipmentRow>>>(() => [
    {
      id: 'select',
      header: ({ table }) => (
        <Checkbox
          checked={table.getIsAllPageRowsSelected() ? true : table.getIsSomePageRowsSelected() ? 'indeterminate' : false}
          onCheckedChange={(checked) => table.toggleAllPageRowsSelected(Boolean(checked))}
          aria-label="全选分布式设备"
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
      header: '设备名称',
      cell: ({ row }) => <div className="text-xs font-medium text-foreground"><div>{row.original.name}</div><div className="font-mono text-[10px] text-muted-foreground">{row.original.id}</div></div>,
    },
    { id: 'type', header: '能源类型', cell: ({ row }) => <Badge variant="secondary" className="text-xs font-normal">{row.original.type}</Badge> },
    { id: 'capacity', header: '额定容量', cell: ({ row }) => <span className="font-mono text-xs text-muted-foreground tabular-nums">{row.original.capacity}</span> },
    { id: 'output', header: '实时遥测输出', cell: ({ row }) => <span className="font-mono text-xs font-semibold text-primary tabular-nums">{row.original.output}</span> },
    { id: 'energyToday', header: '今日运行积分', cell: ({ row }) => <span className="font-mono text-xs text-muted-foreground tabular-nums">{row.original.energyToday}</span> },
    { id: 'mode', header: '控制模式与调度策略', cell: ({ row }) => <span className="text-xs text-muted-foreground">{row.original.mode}</span> },
    { id: 'status', header: '通讯状态', cell: () => <StatusPillBadge label="在线正常" tone="success" /> },
  ], []);

  const table = useDataTable({
    key: 'surface-20-der-equipment',
    data: [...filtered],
    columns,
    pageSize: 10,
    getRowId: (row) => row.id,
  });

  return (
    <Main className="space-y-6">
      {/* Header */}
      <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <div className="flex items-center gap-2">
            <h1 className="text-2xl font-bold tracking-tight text-foreground">分布式能源与微电网</h1>
            <Badge variant="outline" className="text-xs font-normal">DER & Flexibility</Badge>
          </div>
          <p className="text-sm text-muted-foreground">
            屋顶光伏、储能与柔性负荷协同调度与削峰运行
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" className="h-9 gap-1.5" onClick={() => window.location.reload()}>
            <RotateCcw className="h-4 w-4" />
            刷新工况
          </Button>
        </div>
      </div>

      {/* 4-Card Fact Strip */}
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">光伏即时发电功率</CardTitle>
            <Sun className="size-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold tracking-tight text-foreground tabular-nums">
              310.5 <span className="text-xs font-normal text-muted-foreground">kW</span>
            </div>
            <p className="mt-1 text-xs text-muted-foreground">
              今日累计发电 1,757 kWh，全额就地消纳
            </p>
          </CardContent>
        </Card>

        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">储能运行工况与 SoC</CardTitle>
            <BatteryCharging className="size-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold tracking-tight text-foreground tabular-nums">
              放电 180 <span className="text-xs font-normal text-muted-foreground">kW</span>
            </div>
            <div className="mt-2 flex items-center gap-2">
              <Progress value={68.5} className="h-1.5 flex-1" />
              <span className="text-xs font-medium text-foreground tabular-nums">SoC 68.5%</span>
            </div>
            <p className="mt-1 text-xs text-muted-foreground">
              尖峰时段削峰放电中，预计可支撑放电 1.8 小时
            </p>
          </CardContent>
        </Card>

        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">柔性调峰可用响应容量</CardTitle>
            <Zap className="size-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold tracking-tight text-foreground tabular-nums">
              145 <span className="text-xs font-normal text-muted-foreground">kW</span>
            </div>
            <p className="mt-1 text-xs text-muted-foreground">
              含储能余量 100 kW 与空调短时容许温升重置 45 kW
            </p>
          </CardContent>
        </Card>

        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">绿电自发自用率</CardTitle>
            <ShieldCheck className="size-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold tracking-tight text-foreground tabular-nums">
              98.4%
            </div>
            <p className="mt-1 text-xs text-muted-foreground">
              防逆流保护状态安全，无反向倒送电网
            </p>
          </CardContent>
        </Card>
      </div>

      {/* Multi-Energy Dispatch Curve */}
      <Card className="shadow-xs">
        <CardHeader className="pb-2">
          <CardTitle className="text-base font-semibold">24小时微网潮流平衡与储能充放曲线</CardTitle>
          <CardDescription>
            总负载、光伏出力、储能充放功率及电池电量（SoC）时序
          </CardDescription>
        </CardHeader>
        <CardContent>
          <div className="h-[320px] w-full pt-2">
            <ResponsiveContainer width="100%" height="100%">
              <ComposedChart data={MOCK_DISPATCH_CURVE as unknown as Record<string, unknown>[]} margin={{ top: 10, right: 20, left: 0, bottom: 0 }}>
                <CartesianGrid strokeDasharray="3 3" vertical={false} className="stroke-muted/40" />
                <XAxis dataKey="hour" className="text-xs" />
                <YAxis
                  yAxisId="kw"
                  className="text-xs"
                  label={{ value: '功率 (kW)', angle: -90, position: 'insideLeft', offset: 12, className: 'text-[11px] fill-muted-foreground' }}
                />
                <YAxis
                  yAxisId="soc"
                  orientation="right"
                  domain={[0, 100]}
                  className="text-xs"
                  unit="%"
                  label={{ value: '储能 SoC (%)', angle: 90, position: 'insideRight', offset: 12, className: 'text-[11px] fill-muted-foreground' }}
                />
                <Tooltip
                  content={({ active, payload, label }) => {
                    if (!active || !payload?.length) return null;
                    const d = payload[0].payload as HourlyDispatchRecord;
                    return (
                      <div className="rounded-lg border bg-background p-3 shadow-md text-xs space-y-1.5">
                        <div className="font-semibold text-foreground">{label} 调度切片</div>
                        <div className="border-t pt-1 space-y-1">
                          <div className="flex justify-between gap-4 text-muted-foreground">
                            <span>建筑总负载:</span>
                            <span className="tabular-nums text-foreground">{d.loadKW} kW</span>
                          </div>
                          <div className="flex justify-between gap-4 text-amber-600">
                            <span>光伏发电:</span>
                            <span className="tabular-nums">+{d.pvKW} kW</span>
                          </div>
                          <div className="flex justify-between gap-4 text-emerald-600">
                            <span>储能充放:</span>
                            <span className="tabular-nums">{d.bessKW > 0 ? `+${d.bessKW} kW (放电)` : d.bessKW < 0 ? `${d.bessKW} kW (充电)` : '0 kW (待机)'}</span>
                          </div>
                          <div className="flex justify-between gap-4 text-primary font-medium">
                            <span>电池电量 (SoC):</span>
                            <span className="tabular-nums">{d.soc}%</span>
                          </div>
                        </div>
                      </div>
                    );
                  }}
                />
                <Legend wrapperStyle={{ fontSize: '12px' }} />
                <Area yAxisId="kw" type="monotone" dataKey="loadKW" name="建筑负荷" fill="#94a3b8" stroke="#64748b" fillOpacity={0.2} />
                <Area yAxisId="kw" type="monotone" dataKey="pvKW" name="光伏功率" fill="#f59e0b" stroke="#d97706" fillOpacity={0.4} />
                <Line yAxisId="kw" type="stepAfter" dataKey="bessKW" name="储能充放电功率" stroke="#10b981" strokeWidth={2} dot={false} />
                <Line yAxisId="soc" type="monotone" dataKey="soc" name="储能 SoC" stroke="#3b82f6" strokeWidth={2} strokeDasharray="3 3" dot={{ r: 2 }} />
              </ComposedChart>
            </ResponsiveContainer>
          </div>
        </CardContent>
      </Card>

      {/* DER Equipment Ledger Table */}
      <Card className="shadow-xs">
        <CardHeader className="pb-3">
          <CardTitle className="text-base font-semibold">分布式设备台账与遥测工况</CardTitle>
          <CardDescription>
            逆变器、变流器、电池系统及充电桩实时遥测
          </CardDescription>
        </CardHeader>
        <div className="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-3 px-6 py-3 border-b">
          <div className="flex flex-wrap items-center gap-2">
            <DataTableViewPills
              options={pillOptions}
              value={filterType}
              onValueChange={(value) => {
                setFilterType(value);
                table.setPageIndex(0);
              }}
            />
            <div className="relative">
              <Search className="absolute left-2.5 top-2.5 h-3.5 w-3.5 text-muted-foreground" />
              <Input
                placeholder="搜索设备名称或编号..."
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
            tableAriaLabel="分布式设备台账与遥测工况"
            empty="未找到匹配的分布式发电设备或微网设施"
            getHeaderRowProps={() => ({ className: 'hover:bg-transparent' })}
            getHeaderCellProps={(header) => ({
              className:
                header.id === 'select' ? 'w-[40px]' :
                header.id === 'capacity' || header.id === 'output' || header.id === 'energyToday' ? 'text-right' :
                header.id === 'status' ? 'text-center' :
                undefined,
            })}
            getCellProps={(cell) => ({
              className:
                cell.column.id === 'capacity' || cell.column.id === 'output' || cell.column.id === 'energyToday'
                  ? 'text-right'
                  : cell.column.id === 'status'
                    ? 'text-center'
                    : undefined,
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
