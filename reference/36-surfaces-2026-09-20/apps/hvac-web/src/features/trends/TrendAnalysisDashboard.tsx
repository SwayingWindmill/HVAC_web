import { useState, useMemo } from 'react';
import type { ColumnDef } from '@tanstack/react-table';
import {
  Bell,
  Filter,
  RefreshCw,
  Search,
} from 'lucide-react';
import {
  Area,
  CartesianGrid,
  ComposedChart,
  Line,
  ReferenceLine,
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
import { cn } from '@/lib/utils';

export interface TrendAnalysisDashboardProps {
  readonly site: Readonly<Site>;
  readonly principal: CurrentPrincipalResponse;
  readonly searchState?: unknown;
  readonly onSearchChange?: (patch: any) => void;
}

interface VariableConfig {
  readonly id: string;
  readonly name: string;
  readonly unit: string;
  readonly color: string;
  readonly axis: 'left' | 'right';
  readonly min: number;
  readonly max: number;
  readonly current: number;
  readonly avg: number;
  readonly sensor: string;
  readonly status: 'normal' | 'warning';
}

const VARIABLES: readonly VariableConfig[] = [
  { id: 'chw_supply_temp', name: '冷冻水供水温度', unit: '°C', color: '#2563eb', axis: 'left', min: 6.8, max: 7.6, current: 7.0, avg: 7.1, sensor: 'CH-TT-01 (出水温变送器)', status: 'normal' },
  { id: 'chw_return_temp', name: '冷冻水回水温度', unit: '°C', color: '#06b6d4', axis: 'left', min: 11.2, max: 13.5, current: 12.4, avg: 12.2, sensor: 'CH-TT-02 (回水温变送器)', status: 'normal' },
  { id: 'cw_return_temp', name: '冷却水出水温度', unit: '°C', color: '#f59e0b', axis: 'left', min: 28.5, max: 33.8, current: 33.0, avg: 31.5, sensor: 'CT-TT-01 (冷却塔回水温)', status: 'normal' },
  { id: 'plant_power', name: '冷站实时总功率', unit: 'kW', color: '#8b5cf6', axis: 'right', min: 180, max: 540, current: 504, avg: 420, sensor: 'PM-MAIN-01 (总电力仪表)', status: 'normal' },
  { id: 'system_cop', name: '系统综合能效 COP', unit: '', color: '#10b981', axis: 'right', min: 4.5, max: 6.4, current: 5.8, avg: 5.6, sensor: 'CALC-PLANT-COP (群控算法)', status: 'normal' },
];

const TIME_SERIES_DATA = Array.from({ length: 25 }, (_, i) => {
  const hour = i;
  const time = `${String(hour).padStart(2, '0')}:00`;
  const loadFactor = hour >= 8 && hour <= 18 ? 0.85 + Math.sin((hour - 8) / 3) * 0.12 : 0.42 + Math.sin(hour / 4) * 0.08;
  const chw_supply_temp = Number((7.0 + Math.sin(hour / 2) * 0.25).toFixed(2));
  const chw_return_temp = Number((7.0 + 5.2 * loadFactor + (Math.sin(hour) * 0.3)).toFixed(2));
  const cw_return_temp = Number((29.0 + 4.0 * loadFactor + (Math.cos(hour / 2) * 0.4)).toFixed(2));
  const plant_power = Math.round(520 * loadFactor + (i % 3) * 8);
  const system_cop = Number((5.9 - loadFactor * 0.5 + Math.sin(hour / 3) * 0.3).toFixed(2));

  return {
    time,
    chw_supply_temp,
    chw_return_temp,
    cw_return_temp,
    plant_power,
    system_cop,
  };
});

export function TrendAnalysisDashboard({ site }: TrendAnalysisDashboardProps) {
  const [timeRange, setTimeRange] = useState('24h');
  const [activeVariables, setActiveVariables] = useState<string[]>([
    'chw_supply_temp',
    'chw_return_temp',
    'plant_power',
    'system_cop',
  ]);
  const [showAlarms, setShowAlarms] = useState(true);

  const [channelFilter, setChannelFilter] = useState<string>('all');
  const [channelSearch, setChannelSearch] = useState('');
  const channelPillOptions = useMemo<readonly DataTableViewPillOption[]>(() => [
    { id: 'all', label: '全部通道', count: VARIABLES.length },
    { id: 'temp', label: '温度通道', count: VARIABLES.filter((v) => v.unit === '°C').length },
    { id: 'power', label: '功率通道', count: VARIABLES.filter((v) => v.unit === 'kW').length },
    { id: 'calc', label: '计算指标', count: VARIABLES.filter((v) => !v.unit).length },
  ], []);

  const filteredVariables = useMemo(() => {
    return VARIABLES.filter((v) => {
      if (channelFilter === 'temp' && v.unit !== '°C') return false;
      if (channelFilter === 'power' && v.unit !== 'kW') return false;
      if (channelFilter === 'calc' && v.unit) return false;
      if (channelSearch && !v.name.toLowerCase().includes(channelSearch.toLowerCase()) && !v.sensor.toLowerCase().includes(channelSearch.toLowerCase())) {
        return false;
      }
      return true;
    });
  }, [channelFilter, channelSearch]);

  const columns = useMemo<Array<ColumnDef<DataTableFeatures, VariableConfig>>>(() => [
    {
      id: 'select',
      header: ({ table }) => (
        <Checkbox
          checked={table.getIsAllPageRowsSelected() ? true : table.getIsSomePageRowsSelected() ? 'indeterminate' : false}
          onCheckedChange={(checked) => table.toggleAllPageRowsSelected(Boolean(checked))}
          aria-label="全选遥测通道"
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
      id: 'channel',
      header: '遥测通道',
      cell: ({ row }) => <div className="flex items-center gap-2 font-medium text-foreground"><span className="size-2 rounded-full" style={{ backgroundColor: row.original.color }} /><span>{row.original.name}</span></div>,
    },
    { id: 'sensor', header: '关联传感器与物理点位', cell: ({ row }) => <span className="font-mono text-[11px] text-muted-foreground">{row.original.sensor}</span> },
    { id: 'current', header: '当前采样值', cell: ({ row }) => <span className="font-mono font-semibold text-foreground tabular-nums">{row.original.current} {row.original.unit}</span> },
    { id: 'min', header: '区间极小值', cell: ({ row }) => <span className="font-mono text-muted-foreground tabular-nums">{row.original.min} {row.original.unit}</span> },
    { id: 'max', header: '区间极大值', cell: ({ row }) => <span className="font-mono text-muted-foreground tabular-nums">{row.original.max} {row.original.unit}</span> },
    { id: 'avg', header: '区间均值', cell: ({ row }) => <span className="font-mono text-muted-foreground tabular-nums">{row.original.avg} {row.original.unit}</span> },
    { id: 'quality', header: '数据质量', cell: () => <StatusPillBadge label="100% 完整新鲜" tone="success" /> },
  ], []);

  const table = useDataTable({
    key: 'surface-07-trend-channels',
    data: [...filteredVariables],
    columns,
    pageSize: 10,
    getRowId: (row) => row.id,
  });

  const toggleVariable = (id: string) => {
    setActiveVariables((prev) =>
      prev.includes(id) ? (prev.length > 1 ? prev.filter((v) => v !== id) : prev) : [...prev, id],
    );
  };

  const selectedConfigs = VARIABLES.filter((v) => activeVariables.includes(v.id));

  return (
    <Main className="space-y-6" data-testid="trend-analysis-dashboard">
      {/* Header & Controls */}
      <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <div className="flex items-center gap-2.5">
            <h1 className="text-2xl font-bold tracking-tight text-foreground">趋势分析</h1>
            <Badge variant="outline" className="text-xs">
              连续时序采样 · 1 min
            </Badge>
          </div>
          <p className="mt-1 text-sm text-muted-foreground">
            {site.displayName} · {site.timezone} · 原始遥测时序关联比对与工况分析
          </p>
        </div>

        <div className="flex flex-wrap items-center gap-2">
          {/* Time range Tabs */}
          <Tabs value={timeRange} onValueChange={setTimeRange}>
            <TabsList className="h-8">
              <TabsTrigger value="1h" className="text-xs px-2.5">近1小时</TabsTrigger>
              <TabsTrigger value="6h" className="text-xs px-2.5">近6小时</TabsTrigger>
              <TabsTrigger value="24h" className="text-xs px-2.5">近24小时</TabsTrigger>
              <TabsTrigger value="7d" className="text-xs px-2.5">近7天</TabsTrigger>
            </TabsList>
          </Tabs>

          <Button
            variant={showAlarms ? 'secondary' : 'outline'}
            size="sm"
            onClick={() => setShowAlarms(!showAlarms)}
            className="h-8 gap-1.5 text-xs"
          >
            <Bell className={cn('size-3.5', showAlarms && 'text-amber-500')} />
            {showAlarms ? '已叠加告警事件' : '叠加告警事件'}
          </Button>

          <Button variant="outline" size="sm" className="h-8 gap-1.5 text-xs">
            <RefreshCw className="size-3.5" />
            刷新数据
          </Button>
        </div>
      </div>

      {/* Variable Quick Palette Strip */}
      <div className="flex flex-wrap items-center gap-1.5">
        <span className="text-xs font-medium text-muted-foreground mr-1 flex items-center gap-1">
          <Filter className="size-3.5" /> 观测变量:
        </span>
        {VARIABLES.map((v) => {
          const active = activeVariables.includes(v.id);
          return (
            <Button
              key={v.id}
              variant={active ? 'secondary' : 'outline'}
              size="sm"
              onClick={() => toggleVariable(v.id)}
              className={cn(
                'h-7 gap-1.5 px-2.5 text-xs font-normal',
                !active && 'text-muted-foreground opacity-70 hover:opacity-100',
              )}
            >
              <span className="size-2 rounded-full shrink-0" style={{ backgroundColor: v.color }} />
              <span>{v.name}</span>
              <span className="text-[10px] text-muted-foreground">{v.unit}</span>
            </Button>
          );
        })}
      </div>

      {/* Active Variable Realtime Stats Ribbon */}
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
        {selectedConfigs.slice(0, 4).map((v) => (
          <Card key={v.id} className="shadow-xs">
            <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
              <CardTitle className="text-sm font-medium text-muted-foreground truncate max-w-[160px]">{v.name}</CardTitle>
              <Badge variant="outline" className="gap-1 font-normal text-xs">
                <span className="size-1.5 rounded-full bg-emerald-500" />
                正常
              </Badge>
            </CardHeader>
            <CardContent className="space-y-2">
              <div className="flex items-baseline gap-1.5">
                <span className="text-2xl font-bold tracking-tight text-foreground tabular-nums">{v.current}</span>
                <span className="text-xs text-muted-foreground">{v.unit}</span>
              </div>
              <div className="flex items-center justify-between border-t pt-2 text-[11px] text-muted-foreground tabular-nums">
                <span>极小: {v.min}</span>
                <span>极大: {v.max}</span>
                <span>均值: {v.avg}</span>
              </div>
            </CardContent>
          </Card>
        ))}
      </div>

      {/* Main Multi-Variable Telemetry Chart Card */}
      <Card className="shadow-xs">
        <CardHeader className="flex flex-row items-center justify-between pb-2">
          <div>
            <CardTitle className="text-base font-semibold">多通道高频遥测同步趋势曲线</CardTitle>
            <CardDescription className="text-xs">
              温度（左轴）与功率/COP（右轴）跨域因果关联分析
            </CardDescription>
          </div>
          <div className="flex items-center gap-3 text-xs">
            {selectedConfigs.map((v) => (
              <div key={v.id} className="flex items-center gap-1.5">
                <span className="size-2.5 rounded-full" style={{ backgroundColor: v.color }} />
                <span className="text-muted-foreground font-medium">{v.name}</span>
              </div>
            ))}
          </div>
        </CardHeader>
        <CardContent className="pt-3">
          <div className="h-[380px] w-full">
            <ResponsiveContainer width="100%" height="100%">
              <ComposedChart data={TIME_SERIES_DATA} margin={{ top: 15, right: 15, left: -10, bottom: 0 }}>
                <CartesianGrid strokeDasharray="3 3" vertical={false} stroke="#e4e4e7" opacity={0.6} />
                <XAxis dataKey="time" tickLine={false} axisLine={false} tick={{ fontSize: 11, fill: '#71717a' }} />
                <YAxis yAxisId="left" tickLine={false} axisLine={false} tick={{ fontSize: 11, fill: '#71717a' }} domain={[5, 40]} unit="°C" />
                <YAxis yAxisId="right" orientation="right" tickLine={false} axisLine={false} tick={{ fontSize: 11, fill: '#71717a' }} domain={[0, 600]} />
                <Tooltip
                  content={({ active, payload, label }) => {
                    if (!active || !payload || !payload.length) return null;
                    return (
                      <div className="rounded-lg border bg-popover p-3 text-xs shadow-md space-y-1.5 min-w-[200px]">
                        <div className="font-semibold text-foreground border-b pb-1 flex items-center justify-between">
                          <span>时间点: {label}</span>
                          <span className="text-[10px] text-muted-foreground font-normal">采样正常</span>
                        </div>
                        {payload.map((entry) => {
                          const conf = VARIABLES.find((c) => c.id === entry.dataKey);
                          if (!conf) return null;
                          return (
                            <div key={conf.id} className="flex items-center justify-between gap-4">
                              <div className="flex items-center gap-1.5">
                                <span className="size-2 rounded-full" style={{ backgroundColor: conf.color }} />
                                <span className="text-muted-foreground">{conf.name}:</span>
                              </div>
                              <span className="tabular-nums font-medium text-foreground">
                                {entry.value} {conf.unit}
                              </span>
                            </div>
                          );
                        })}
                      </div>
                    );
                  }}
                />

                {/* Alarm events overlay */}
                {showAlarms && (
                  <ReferenceLine x="11:00" yAxisId="left" stroke="#f59e0b" strokeDasharray="3 3" label={{ value: 'CH-02 加载启机', fill: '#d97706', fontSize: 10, position: 'top' }} />
                )}
                {showAlarms && (
                  <ReferenceLine x="17:00" yAxisId="left" stroke="#3b82f6" strokeDasharray="3 3" label={{ value: '工况下调降载', fill: '#2563eb', fontSize: 10, position: 'top' }} />
                )}

                {/* Target setpoint baseline */}
                <ReferenceLine y={7.0} yAxisId="left" stroke="#2563eb" strokeDasharray="2 2" opacity={0.5} label={{ value: '供水设定 7.0°C', fill: '#2563eb', fontSize: 10, position: 'insideBottomLeft' }} />

                {/* Render active lines */}
                {activeVariables.includes('chw_supply_temp') && (
                  <Line yAxisId="left" type="monotone" dataKey="chw_supply_temp" stroke="#2563eb" strokeWidth={2} dot={false} name="冷冻水供水温度" />
                )}
                {activeVariables.includes('chw_return_temp') && (
                  <Line yAxisId="left" type="monotone" dataKey="chw_return_temp" stroke="#06b6d4" strokeWidth={2} dot={false} name="冷冻水回水温度" />
                )}
                {activeVariables.includes('cw_return_temp') && (
                  <Line yAxisId="left" type="monotone" dataKey="cw_return_temp" stroke="#f59e0b" strokeWidth={2} dot={false} name="冷却水出水温度" />
                )}
                {activeVariables.includes('plant_power') && (
                  <Area yAxisId="right" type="monotone" dataKey="plant_power" stroke="#8b5cf6" fill="#8b5cf6" fillOpacity={0.1} strokeWidth={1.5} name="冷站总功率" />
                )}
                {activeVariables.includes('system_cop') && (
                  <Line yAxisId="right" type="monotone" dataKey="system_cop" stroke="#10b981" strokeWidth={2} dot={false} name="系统能效 COP" />
                )}
              </ComposedChart>
            </ResponsiveContainer>
          </div>
        </CardContent>
      </Card>

      {/* Telemetry Sensor Metadata & Statistical Matrix */}
      <Card className="shadow-xs">
        <CardHeader className="pb-3">
          <div className="flex items-center justify-between">
            <div>
              <CardTitle className="text-base font-semibold">物理测点与质量统计台账</CardTitle>
              <CardDescription className="text-xs">
                遥测传感器数据新鲜度与有效区间统计
              </CardDescription>
            </div>
          </div>
        </CardHeader>
        <div className="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-3 px-6 py-3 border-b">
          <div className="flex flex-wrap items-center gap-2">
            <DataTableViewPills
              options={channelPillOptions}
              value={channelFilter}
              onValueChange={(value) => {
                setChannelFilter(value);
                table.setPageIndex(0);
              }}
            />
            <div className="relative">
              <Search className="absolute left-2.5 top-2.5 h-3.5 w-3.5 text-muted-foreground" />
              <Input
                placeholder="搜索通道名称或传感器编号..."
                value={channelSearch}
                onChange={(e) => {
                  setChannelSearch(e.target.value);
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
            tableAriaLabel="物理测点与质量统计台账"
            empty="未找到匹配的遥测通道"
            getHeaderRowProps={() => ({ className: 'bg-muted/30 hover:bg-transparent' })}
            getHeaderCellProps={(header) => ({
              className:
                header.id === 'select'
                  ? 'w-[40px]'
                  : header.id === 'current' || header.id === 'min' || header.id === 'max' || header.id === 'avg'
                    ? 'text-right text-xs'
                    : header.id === 'quality'
                      ? 'text-center text-xs'
                      : 'text-xs',
            })}
            getRowProps={() => ({ className: 'text-xs hover:bg-muted/40' })}
            getCellProps={(cell) => ({
              className:
                cell.column.id === 'current' || cell.column.id === 'min' || cell.column.id === 'max' || cell.column.id === 'avg'
                  ? 'text-right'
                  : cell.column.id === 'quality'
                    ? 'text-center'
                    : undefined,
            })}
            footer={(
              <DataTablePagination
                table={table}
                totalRows={filteredVariables.length}
                
                
                
                
                
              />
            )}
          />
        </CardContent>
      </Card>
    </Main>
  );
}
