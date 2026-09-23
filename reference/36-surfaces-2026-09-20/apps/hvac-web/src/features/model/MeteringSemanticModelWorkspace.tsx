import { useState, useMemo } from 'react';
import type { ColumnDef } from '@tanstack/react-table';
import {
  Layers,
  Database,
  Calculator,
  Search,
  CheckCircle2,
  GitFork,
  ArrowRight,
  ShieldCheck,
  RefreshCw,
} from 'lucide-react';
import { Main } from '@/components/layout/Main';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
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

interface MeteringSemanticModelWorkspaceProps {
  readonly siteId: string;
}

interface MeterNode {
  readonly code: string;
  readonly name: string;
  readonly level: 'MAIN' | 'BRANCH' | 'VIRTUAL';
  readonly type: string;
  readonly accuracy: string;
  readonly readingCurrentKw: number;
  readonly dailyTotalKwh: number;
  readonly balanceDiff: string;
  readonly isChild?: boolean;
  readonly shareOfMainPercent: number;
}

const METER_HIERARCHY: readonly MeterNode[] = [
  {
    code: 'M-MAIN-E01',
    name: '1# 变压器低压进线总柜电表',
    level: 'MAIN',
    type: '三相多功能电表',
    accuracy: '0.5S 级',
    readingCurrentKw: 4820.5,
    dailyTotalKwh: 38420,
    balanceDiff: '+0.4%',
    shareOfMainPercent: 100,
  },
  {
    code: 'M-SUB-CH01',
    name: 'CH-01 离心冷水机组专用动力分表',
    level: 'BRANCH',
    type: '智能电力仪表',
    accuracy: '0.5S 级',
    readingCurrentKw: 1240.2,
    dailyTotalKwh: 11280,
    balanceDiff: '正常',
    isChild: true,
    shareOfMainPercent: 25.7,
  },
  {
    code: 'M-SUB-CH02',
    name: 'CH-02 磁悬浮冷水机组专用动力分表',
    level: 'BRANCH',
    type: '智能电力仪表',
    accuracy: '0.5S 级',
    readingCurrentKw: 980.5,
    dailyTotalKwh: 8940,
    balanceDiff: '正常',
    isChild: true,
    shareOfMainPercent: 20.3,
  },
  {
    code: 'M-SUB-PUMP',
    name: '冷冻 / 冷却循环水泵总配电分表',
    level: 'BRANCH',
    type: '智能电力仪表',
    accuracy: '1.0 级',
    readingCurrentKw: 680.4,
    dailyTotalKwh: 6120,
    balanceDiff: '+0.2%',
    isChild: true,
    shareOfMainPercent: 14.1,
  },
  {
    code: 'M-SUB-CT',
    name: '冷却塔风机总配电动力分表',
    level: 'BRANCH',
    type: '智能电力仪表',
    accuracy: '1.0 级',
    readingCurrentKw: 185.2,
    dailyTotalKwh: 1680,
    balanceDiff: '正常',
    isChild: true,
    shareOfMainPercent: 3.8,
  },
  {
    code: 'M-VIRT-UNMETERED',
    name: '末端照明动力与控制线损 (母平差额平账表)',
    level: 'VIRTUAL',
    type: '算法虚拟表 (母平平衡)',
    accuracy: '动态平衡',
    readingCurrentKw: 34.2,
    dailyTotalKwh: 310,
    balanceDiff: '0.8% (达标 ≤ 2.0%)',
    isChild: true,
    shareOfMainPercent: 0.8,
  },
];

const SEMANTIC_POINTS = [
  {
    pointTag: 'CH_01.Evap_Leaving_Water_Temp',
    name: '1#冷机蒸发器出水温度',
    brickClass: 'brick:Leaving_Water_Temperature_Sensor',
    haystackTags: 'chilled, water, leaving, temp, sensor, point',
    device: 'CH-01 离心机',
    unit: '℃',
    range: '0.0 ~ 30.0 ℃',
    sampleInterval: '10 秒',
    qualityRule: '梯度限幅 ≤ 1.5℃/min',
  },
  {
    pointTag: 'CH_01.Evap_Entering_Water_Temp',
    name: '1#冷机蒸发器回水温度',
    brickClass: 'brick:Entering_Water_Temperature_Sensor',
    haystackTags: 'chilled, water, return, temp, sensor, point',
    device: 'CH-01 离心机',
    unit: '℃',
    range: '0.0 ~ 35.0 ℃',
    sampleInterval: '10 秒',
    qualityRule: '双通道热敏电阻温差 ≤ 0.3℃',
  },
  {
    pointTag: 'CH_PLANT.Chilled_Water_Flow',
    name: '冷冻水供水总管瞬时流量',
    brickClass: 'brick:Chilled_Water_Flow_Sensor',
    haystackTags: 'chilled, water, flow, sensor, point',
    device: 'FLOW-METER-01',
    unit: 'm³/h',
    range: '0.0 ~ 1200.0 m³/h',
    sampleInterval: '5 秒',
    qualityRule: '零位防漂移死区 15 m³/h',
  },
  {
    pointTag: 'CH_PLANT.Total_Electric_Power',
    name: '冷冻机房总用电瞬时有功功率',
    brickClass: 'brick:Electric_Power_Sensor',
    haystackTags: 'electric, power, sensor, meter, point',
    device: 'M-MAIN-E01',
    unit: 'kW',
    range: '0.0 ~ 6000.0 kW',
    sampleInterval: '5 秒',
    qualityRule: '母线平衡防抖死区 10 kW',
  },
];

const VIRTUAL_PIPELINES = [
  {
    name: '冷站实时制冷量 (Thermal Cooling Power)',
    tag: 'VIRT.CH_PLANT.COOLING_CAPACITY_KW',
    formula: 'Q_c = M × c_p × (T_return - T_supply) / 3600',
    inputs: ['CH_PLANT.Chilled_Water_Flow', 'CH_01.Evap_Entering_Water_Temp', 'CH_01.Evap_Leaving_Water_Temp'],
    unit: 'kW (冷量)',
    frequency: '10 秒滑动窗口',
    status: 'ACTIVE',
  },
  {
    name: '冷站综合能效比 (Instantaneous System COP)',
    tag: 'VIRT.CH_PLANT.INSTANT_COP',
    formula: 'COP = Q_cooling_kW / P_total_electric_kW',
    inputs: ['VIRT.CH_PLANT.COOLING_CAPACITY_KW', 'CH_PLANT.Total_Electric_Power'],
    unit: 'COP (无量纲)',
    frequency: '10 秒滑动窗口',
    status: 'ACTIVE',
  },
  {
    name: '冷却塔室外湿球逼近度 (Approach Temperature)',
    tag: 'VIRT.CT_SYSTEM.APPROACH_TEMP',
    formula: 'T_approach = T_cw_leaving - T_outdoor_wet_bulb',
    inputs: ['CW_SYSTEM.SUPPLY_WATER_TEMP', 'WEATHER.OUTDOOR_WET_BULB_TEMP'],
    unit: '℃',
    frequency: '30 秒滑动窗口',
    status: 'ACTIVE',
  },
];

export function MeteringSemanticModelWorkspace({ siteId: _siteId }: MeteringSemanticModelWorkspaceProps) {
  const [meterSearch, setMeterSearch] = useState('');
  const [meterLevelFilter, setMeterLevelFilter] = useState<string>('ALL');

  const meterLevelPills: readonly DataTableViewPillOption[] = useMemo(() => [
    { key: 'ALL', label: '全部回路', count: METER_HIERARCHY.length },
    { key: 'MAIN', label: '总受电柜', count: METER_HIERARCHY.filter((m) => m.level === 'MAIN').length },
    { key: 'BRANCH', label: '动力分表', count: METER_HIERARCHY.filter((m) => m.level === 'BRANCH').length },
    { key: 'VIRTUAL', label: '虚拟平账表', count: METER_HIERARCHY.filter((m) => m.level === 'VIRTUAL').length },
  ], []);

  const filteredMeters = useMemo(() => {
    return METER_HIERARCHY.filter((m) => {
      if (meterLevelFilter !== 'ALL' && m.level !== meterLevelFilter) return false;
      if (!meterSearch.trim()) return true;
      const q = meterSearch.toLowerCase();
      return m.name.toLowerCase().includes(q) || m.code.toLowerCase().includes(q) || m.type.toLowerCase().includes(q);
    });
  }, [meterLevelFilter, meterSearch]);

  const [pointSearch, setPointSearch] = useState('');
  const [pointDeviceFilter, setPointDeviceFilter] = useState<string>('ALL');

  const pointDevicePills: readonly DataTableViewPillOption[] = useMemo(() => [
    { key: 'ALL', label: '全部设备点位', count: SEMANTIC_POINTS.length },
    { key: 'CH-01 离心机', label: '1# 离心机', count: SEMANTIC_POINTS.filter((p) => p.device === 'CH-01 离心机').length },
    { key: 'FLOW-METER-01', label: '流量计', count: SEMANTIC_POINTS.filter((p) => p.device === 'FLOW-METER-01').length },
    { key: 'M-MAIN-E01', label: '进线总表', count: SEMANTIC_POINTS.filter((p) => p.device === 'M-MAIN-E01').length },
  ], []);

  const filteredPoints = useMemo(() => {
    return SEMANTIC_POINTS.filter((p) => {
      if (pointDeviceFilter !== 'ALL' && p.device !== pointDeviceFilter) return false;
      if (!pointSearch.trim()) return true;
      const q = pointSearch.toLowerCase();
      return (
        p.pointTag.toLowerCase().includes(q) ||
        p.name.toLowerCase().includes(q) ||
        p.brickClass.toLowerCase().includes(q) ||
        p.device.toLowerCase().includes(q)
      );
    });
  }, [pointSearch, pointDeviceFilter]);

  const meterColumns = useMemo<Array<ColumnDef<DataTableFeatures, MeterNode>>>(() => [
    {
      id: 'select',
      header: ({ table }) => (
        <Checkbox
          checked={table.getIsAllPageRowsSelected() ? true : table.getIsSomePageRowsSelected() ? 'indeterminate' : false}
          onCheckedChange={(checked) => table.toggleAllPageRowsSelected(Boolean(checked))}
          aria-label="选择全部回路"
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
    { id: 'code', header: '表具编码', cell: ({ row }) => <span className="font-mono text-[11px] font-medium text-foreground">{row.original.code}</span> },
    {
      id: 'name',
      header: '计量回路名称 (拓扑树)',
      cell: ({ row }) => <div className="flex items-center gap-1.5">{row.original.isChild ? <span className="mr-1 select-none font-mono text-muted-foreground">└─</span> : null}<span className={row.original.level === 'MAIN' ? 'font-bold text-foreground' : 'text-foreground'}>{row.original.name}</span></div>,
    },
    {
      id: 'level',
      header: '拓扑层级',
      cell: ({ row }) => <StatusPillBadge label={row.original.level === 'MAIN' ? '总进线' : row.original.level === 'VIRTUAL' ? '虚拟差额' : '一级支路'} tone={row.original.level === 'MAIN' ? 'info' : row.original.level === 'VIRTUAL' ? 'warning' : 'neutral'} />,
    },
    { id: 'typeAccuracy', header: '仪表类型 / 精度', cell: ({ row }) => <span className="text-[11px] text-muted-foreground">{row.original.type} · <span className="font-mono tabular-nums">{row.original.accuracy}</span></span> },
    { id: 'readingCurrentKw', header: '当前实时负荷', cell: ({ row }) => <span className="font-mono text-[11px] font-medium text-foreground tabular-nums">{row.original.readingCurrentKw.toLocaleString()} kW</span> },
    { id: 'shareOfMainPercent', header: '进线占比', cell: ({ row }) => <span className="font-mono text-[11px] text-muted-foreground tabular-nums">{row.original.shareOfMainPercent}%</span> },
    { id: 'dailyTotalKwh', header: '当日累计电量', cell: ({ row }) => <span className="font-mono text-[11px] text-muted-foreground tabular-nums">{row.original.dailyTotalKwh.toLocaleString()} kWh</span> },
    {
      id: 'balanceDiff',
      header: '平账状态 / 差额比',
      cell: ({ row }) => <StatusPillBadge label={row.original.balanceDiff} tone={row.original.balanceDiff.includes('达标') || row.original.balanceDiff.includes('正常') ? 'success' : 'warning'} />,
    },
  ], []);

  const meterTable = useDataTable({
    key: 'surface-33-meter-hierarchy',
    data: [...filteredMeters],
    columns: meterColumns,
    pageSize: 10,
    getRowId: (row) => row.code,
  });

  type SemanticPoint = (typeof SEMANTIC_POINTS)[number];

  const pointColumns = useMemo<Array<ColumnDef<DataTableFeatures, SemanticPoint>>>(() => [
    {
      id: 'select',
      header: ({ table }) => (
        <Checkbox
          checked={table.getIsAllPageRowsSelected() ? true : table.getIsSomePageRowsSelected() ? 'indeterminate' : false}
          onCheckedChange={(checked) => table.toggleAllPageRowsSelected(Boolean(checked))}
          aria-label="选择全部点位"
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
    { id: 'pointTag', header: '点位标签 (Tag)', cell: ({ row }) => <span className="font-mono text-[11px] font-medium text-foreground">{row.original.pointTag}</span> },
    { id: 'name', header: '物理测点语义描述', cell: ({ row }) => <span className="font-medium leading-snug text-foreground">{row.original.name}</span> },
    { id: 'brickClass', header: 'Brick Schema 类', cell: ({ row }) => <span className="font-mono text-[11px] text-primary">{row.original.brickClass}</span> },
    { id: 'haystackTags', header: 'Project Haystack 标签集', cell: ({ row }) => <span className="font-mono text-[11px] text-muted-foreground">{row.original.haystackTags}</span> },
    { id: 'device', header: '所属物理设备', cell: ({ row }) => <StatusPillBadge label={row.original.device} tone="neutral" /> },
    { id: 'unit', header: '工程单位', cell: ({ row }) => <span className="font-mono font-medium text-foreground">{row.original.unit}</span> },
    { id: 'qualityRule', header: '数据品质校验规则', cell: ({ row }) => <span className="text-[11px] text-muted-foreground">{row.original.qualityRule}</span> },
  ], []);

  const pointTable = useDataTable({
    key: 'surface-33-semantic-points',
    data: [...filteredPoints],
    columns: pointColumns,
    pageSize: 10,
    getRowId: (row) => row.pointTag,
  });

  return (
    <Main className="space-y-6">
      {/* 顶部标题区 */}
      <div className="flex flex-col gap-3 md:flex-row md:items-center md:justify-between border-b pb-4">
        <div>
          <h1 className="text-2xl font-bold tracking-tight text-foreground">计量与点位模型</h1>
          <p className="text-sm text-muted-foreground mt-1">
            配置电能分项计量层级、母线平衡核验规则与能效派生计算点位
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" className="h-8 text-xs gap-1.5">
            <RefreshCw className="h-3.5 w-3.5" />
            校验母线电量平衡
          </Button>
          <Button size="sm" className="h-8 text-xs gap-1.5">
            <GitFork className="h-3.5 w-3.5" />
            更新点位模型
          </Button>
        </div>
      </div>

      {/* 紧凑型拓扑平衡态势带 (替代 4 个模板卡片) */}
      <div className="rounded-lg border border-border/80 bg-muted/20 p-4">
        <div className="grid grid-cols-2 gap-4 md:grid-cols-4 md:divide-x md:divide-border/60">
          <div className="space-y-1">
            <div className="text-[11px] font-medium text-muted-foreground">物理分项与虚拟表具</div>
            <div className="flex items-baseline gap-2">
              <span className="text-xl font-bold tracking-tight tabular-nums text-foreground">86</span>
              <span className="text-xs text-muted-foreground">块 (电 64 / 水 14 / 冷 8)</span>
            </div>
            <div className="text-[11px] text-muted-foreground">
              一级受电进线 2 路 · 末端分项覆盖 100%
            </div>
          </div>

          <div className="space-y-1 md:pl-6">
            <div className="text-[11px] font-medium text-muted-foreground">母评分项拓扑平衡率</div>
            <div className="flex items-baseline gap-2">
              <span className="text-xl font-bold tracking-tight tabular-nums text-emerald-600">99.2%</span>
              <span className="text-xs text-muted-foreground tabular-nums">(差额 0.8%)</span>
            </div>
            <div className="text-[11px] text-emerald-600 font-medium flex items-center gap-1">
              <ShieldCheck className="h-3 w-3" />
              优于国标允许公差 ≤ ±2.0% (合规)
            </div>
          </div>

          <div className="space-y-1 md:pl-6">
            <div className="text-[11px] font-medium text-muted-foreground">语义标签打标覆盖率</div>
            <div className="flex items-baseline gap-2">
              <span className="text-xl font-bold tracking-tight tabular-nums text-foreground">1,420</span>
              <span className="text-xs text-muted-foreground">/ <span className="tabular-nums">1,440</span> 点 (<span className="tabular-nums">98.6%</span>)</span>
            </div>
            <div className="text-[11px] text-muted-foreground">
              Brick v1.3 + Project Haystack 4 双标准
            </div>
          </div>

          <div className="space-y-1 md:pl-6">
            <div className="text-[11px] font-medium text-muted-foreground">实时派生计算管道</div>
            <div className="flex items-baseline gap-2">
              <span className="text-xl font-bold tracking-tight tabular-nums text-foreground">12</span>
              <span className="text-xs text-muted-foreground">条活跃算法管道</span>
            </div>
            <div className="text-[11px] text-muted-foreground">
              10s 级热力学平衡防抖与死区保护
            </div>
          </div>
        </div>
      </div>

      {/* 工作区 Tabs */}
      <Tabs defaultValue="meters" className="space-y-4">
        <TabsList className="bg-muted/50 p-1 border border-border/60">
          <TabsTrigger value="meters" className="text-xs gap-1.5">
            <Layers className="h-3.5 w-3.5" />
            母线-支路分项计量树与平账校验
          </TabsTrigger>
          <TabsTrigger value="ontology" className="text-xs gap-1.5">
            <Database className="h-3.5 w-3.5" />
            Brick / Haystack 语义标签点位字典 ({SEMANTIC_POINTS.length})
          </TabsTrigger>
          <TabsTrigger value="pipelines" className="text-xs gap-1.5">
            <Calculator className="h-3.5 w-3.5" />
            派生虚拟计算管道与公式流 ({VIRTUAL_PIPELINES.length})
          </TabsTrigger>
        </TabsList>

        {/* Tab 1: 分项计量层级树与平账校验 */}
        <TabsContent value="meters" className="space-y-4 mt-0">
          {/* 母平差额可视化平衡指示条 */}
          <div className="rounded-lg border border-border/80 bg-card p-4 space-y-3">
            <div className="flex items-center justify-between">
              <div>
                <div className="text-sm font-semibold text-foreground">母平差额动态检验与线损吸收</div>
                <div className="text-xs text-muted-foreground mt-0.5">
                  进线总受电功率与各分项动力支路功率矢量和比对，差额自动由算法虚拟表吸收平账
                </div>
              </div>
              <div className="text-xs text-emerald-600 font-semibold flex items-center gap-1.5">
                <CheckCircle2 className="h-4 w-4" />
                母平差额比 <span className="tabular-nums">0.8%</span> (达标合规)
              </div>
            </div>

            <div className="flex items-center gap-3 text-xs">
              <div className="rounded border bg-muted/30 px-3 py-1.5">
                <span className="text-muted-foreground">进线总功率: </span>
                <span className="tabular-nums font-semibold text-foreground">4,820.5 kW</span>
              </div>
              <ArrowRight className="h-4 w-4 text-muted-foreground" />
              <div className="rounded border bg-muted/30 px-3 py-1.5">
                <span className="text-muted-foreground">实测支路和: </span>
                <span className="tabular-nums font-semibold text-foreground">4,786.3 kW (99.2%)</span>
              </div>
              <span className="text-muted-foreground">+</span>
              <div className="rounded border bg-muted/30 px-3 py-1.5">
                <span className="text-muted-foreground">未分项线损: </span>
                <span className="tabular-nums font-semibold text-emerald-600">34.2 kW (0.8%)</span>
              </div>
            </div>
          </div>

          {/* 工具栏 */}
          <div className="flex flex-col gap-2.5 sm:flex-row sm:items-center sm:justify-between">
            <div className="flex flex-wrap items-center gap-2">
              <div className="relative w-64">
                <Search className="absolute left-2.5 top-2.5 h-3.5 w-3.5 text-muted-foreground" />
                <Input
                  placeholder="按回路名称 / 编码 / 仪表搜索..."
                  value={meterSearch}
                  onChange={(e) => {
                    setMeterSearch(e.target.value);
                    meterTable.setPageIndex(0);
                  }}
                  className="h-8 pl-8 text-xs bg-background"
                />
              </div>
              <DataTableViewPills
                options={meterLevelPills}
                value={meterLevelFilter}
                onValueChange={(v) => {
                  setMeterLevelFilter(v);
                  meterTable.setPageIndex(0);
                }}
              />
            </div>
            <div className="flex items-center gap-2">
              <DataTableViewOptions table={meterTable} />
              <div className="text-xs text-muted-foreground">
                共 <span className="tabular-nums font-medium">{filteredMeters.length}</span> 个计量回路
              </div>
            </div>
          </div>

          <DataTable
            table={meterTable}
            tableAriaLabel="计量回路拓扑树"
            empty="无匹配的计量回路"
            getHeaderRowProps={() => ({ className: 'bg-muted/40 text-xs hover:bg-muted/40' })}
            getHeaderCellProps={(header) => ({
              className:
                header.id === 'select' ? 'w-[40px] px-3' :
                header.id === 'code' ? 'w-[140px] font-medium' :
                header.id === 'name' ? 'w-[280px] font-medium' :
                header.id === 'level' ? 'w-[110px] font-medium' :
                header.id === 'typeAccuracy' ? 'w-[150px] font-medium' :
                header.id === 'readingCurrentKw' ? 'w-[130px] text-right font-medium' :
                header.id === 'shareOfMainPercent' ? 'w-[100px] text-right font-medium' :
                header.id === 'dailyTotalKwh' ? 'w-[130px] text-right font-medium' :
                header.id === 'balanceDiff' ? 'w-[140px] text-right font-medium' :
                undefined,
            })}
            getRowProps={(row) => ({
              className: `text-xs transition-colors hover:bg-muted/30 ${row.original.level === 'MAIN' ? 'bg-muted/10 font-semibold' : ''}`,
            })}
            getCellProps={(cell) => ({
              className:
                cell.column.id === 'select' ? 'px-3' :
                ['readingCurrentKw', 'shareOfMainPercent', 'dailyTotalKwh', 'balanceDiff'].includes(cell.column.id) ? 'text-right' :
                undefined,
            })}
            footer={(
              <DataTablePagination
                table={meterTable}
                totalRows={filteredMeters.length}
                
                
                
                
                
              />
            )}
          />
        </TabsContent>

        {/* Tab 2: 语义模型字典 */}
        <TabsContent value="ontology" className="space-y-3 mt-0">
          <div className="flex flex-col gap-2.5 sm:flex-row sm:items-center sm:justify-between">
            <div className="flex flex-wrap items-center gap-2">
              <div className="relative w-64">
                <Search className="absolute left-2.5 top-2.5 h-3.5 w-3.5 text-muted-foreground" />
                <Input
                  placeholder="按点位标签 / Brick 类 / 设备名称搜索..."
                  value={pointSearch}
                  onChange={(e) => {
                    setPointSearch(e.target.value);
                    pointTable.setPageIndex(0);
                  }}
                  className="h-8 pl-8 text-xs bg-background"
                />
              </div>
              <DataTableViewPills
                options={pointDevicePills}
                value={pointDeviceFilter}
                onValueChange={(v) => {
                  setPointDeviceFilter(v);
                  pointTable.setPageIndex(0);
                }}
              />
            </div>
            <div className="flex items-center gap-2">
              <DataTableViewOptions table={pointTable} />
              <div className="text-xs text-muted-foreground">
                共 <span className="tabular-nums font-medium">{filteredPoints.length}</span> 个标准化语义点位
              </div>
            </div>
          </div>

          <DataTable
            table={pointTable}
            tableAriaLabel="标准化语义点位字典"
            empty="无匹配的语义点位"
            getHeaderRowProps={() => ({ className: 'bg-muted/40 text-xs hover:bg-muted/40' })}
            getHeaderCellProps={(header) => ({
              className:
                header.id === 'select' ? 'w-[40px] px-3' :
                header.id === 'pointTag' ? 'w-[200px] font-medium' :
                header.id === 'name' ? 'w-[180px] font-medium' :
                header.id === 'brickClass' ? 'w-[220px] font-medium' :
                header.id === 'haystackTags' ? 'font-medium' :
                header.id === 'device' ? 'w-[120px] font-medium' :
                header.id === 'unit' ? 'w-[80px] text-center font-medium' :
                header.id === 'qualityRule' ? 'w-[160px] font-medium' :
                undefined,
            })}
            getRowProps={() => ({ className: 'text-xs hover:bg-muted/30' })}
            getCellProps={(cell) => ({
              className:
                cell.column.id === 'select' ? 'px-3' :
                cell.column.id === 'unit' ? 'text-center' :
                undefined,
            })}
            footer={(
              <DataTablePagination
                table={pointTable}
                totalRows={filteredPoints.length}
                
                
                
                
                
              />
            )}
          />
        </TabsContent>

        {/* Tab 3: 派生虚拟计算管道 */}
        <TabsContent value="pipelines" className="space-y-3 mt-0">
          <div className="grid grid-cols-1 gap-3">
            {VIRTUAL_PIPELINES.map((pipe) => (
              <div key={pipe.tag} className="rounded-lg border border-border/80 bg-card p-4 space-y-2">
                <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-2">
                  <div className="space-y-0.5">
                    <div className="text-sm font-semibold text-foreground flex items-center gap-2">
                      {pipe.name}
                      <span className="font-mono text-xs font-normal text-muted-foreground">({pipe.tag})</span>
                    </div>
                    <div className="font-mono text-xs text-primary font-bold bg-muted/40 px-2 py-1 rounded inline-block">
                      {pipe.formula}
                    </div>
                  </div>
                  <div className="flex items-center gap-2 text-xs">
                    <span className="text-muted-foreground">{pipe.frequency}</span>
                    <span className="inline-flex items-center gap-1 text-emerald-600 font-medium">
                      <span className="h-1.5 w-1.5 rounded-full bg-emerald-500" />
                      运行中
                    </span>
                  </div>
                </div>

                <div className="flex items-center gap-2 text-xs pt-1 border-t text-muted-foreground">
                  <span className="font-medium text-foreground">输入测点流:</span>
                  {pipe.inputs.map((inp) => (
                    <span key={inp} className="font-mono bg-muted px-1.5 py-0.5 rounded text-[11px]">
                      {inp}
                    </span>
                  ))}
                  <span className="ml-auto text-[11px] text-foreground font-medium">
                    输出量纲: {pipe.unit}
                  </span>
                </div>
              </div>
            ))}
          </div>
        </TabsContent>
      </Tabs>
    </Main>
  );
}
