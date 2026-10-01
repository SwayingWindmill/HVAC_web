import { useMemo, useState } from 'react';
import type { ColumnDef } from '@tanstack/react-table';
import { parseAsStringEnum, useQueryState } from 'nuqs';
import {
  Building,
  Calendar,
  Zap,
  Plus,
  Search,
  CheckCircle2,
  Flame,
  Snowflake,
  Sun,
  Settings2,
} from 'lucide-react';
import { DataTableBlock } from '@/blocks/data-table';
import { Main } from '@/components/layout/Main';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet';
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

interface SiteProfile {
  readonly id: string;
  readonly code: string;
  readonly name: string;
  readonly city: string;
  readonly climateZone: string;
  readonly totalArea: number; // m²
  readonly coolingArea: number; // m²
  readonly designCoolingLoad: number; // RT
  readonly coolingLoadIndex: number; // W/m²
  readonly commissioningDate: string;
  readonly timezone: string;
  readonly weatherSource: string;
}

const SITE_PROFILES: readonly SiteProfile[] = [
  {
    id: 'S-SHA-01',
    code: 'SHA-HQ',
    name: '上海虹桥综合枢纽站',
    city: '上海市',
    climateZone: '夏热冬冷区',
    totalArea: 145000,
    coolingArea: 132000,
    designCoolingLoad: 3600,
    coolingLoadIndex: 96.2,
    commissioningDate: '2022-06',
    timezone: 'Asia/Shanghai (UTC+8)',
    weatherSource: '就地微气象站 + 国家站 (58362)',
  },
  {
    id: 'S-BJS-01',
    code: 'BJS-TOWER',
    name: '北京国贸超高层冷站',
    city: '北京市',
    climateZone: '寒冷地区',
    totalArea: 182000,
    coolingArea: 168000,
    designCoolingLoad: 4200,
    coolingLoadIndex: 88.0,
    commissioningDate: '2020-08',
    timezone: 'Asia/Shanghai (UTC+8)',
    weatherSource: '国家气象观测站 (54511)',
  },
  {
    id: 'S-SZX-01',
    code: 'SZX-PARK',
    name: '深圳湾科技生态园北区',
    city: '深圳市',
    climateZone: '夏热冬暖区',
    totalArea: 98000,
    coolingArea: 92000,
    designCoolingLoad: 2800,
    coolingLoadIndex: 107.0,
    commissioningDate: '2023-04',
    timezone: 'Asia/Shanghai (UTC+8)',
    weatherSource: '就地微气象站 (IOT-WEATHER-01)',
  },
  {
    id: 'S-CAN-01',
    code: 'CAN-AIRPORT',
    name: '广州白云机场物流冷热站',
    city: '广州市',
    climateZone: '夏热冬暖区',
    totalArea: 210000,
    coolingArea: 195000,
    designCoolingLoad: 5200,
    coolingLoadIndex: 93.8,
    commissioningDate: '2021-11',
    timezone: 'Asia/Shanghai (UTC+8)',
    weatherSource: '机场民航气象站双冗余',
  },
];

const SITE_SETTINGS_CITY_OPTIONS = [...new Set(SITE_PROFILES.map((site) => site.city))]
  .map((value) => ({ label: value, value }));
const SITE_SETTINGS_CLIMATE_OPTIONS = [...new Set(SITE_PROFILES.map((site) => site.climateZone))]
  .map((value) => ({ label: value, value }));
const SITE_SETTINGS_FILTER_COLUMN_IDS = ['city', 'climateZone'] as const;
const SITE_SETTINGS_FILTERS_QUERY_KEY = 'siteSettingsFilters';
const SITE_SETTINGS_JOIN_OPERATOR_QUERY_KEY = 'siteSettingsJoinOperator';

function matchesSiteSettingsAdvancedFilter(
  site: SiteProfile,
  filter: ExtendedColumnFilter<SiteProfile>,
) {
  if (filter.id !== 'city' && filter.id !== 'climateZone') return true;
  const values = Array.isArray(filter.value) ? filter.value : [filter.value];
  const actual = filter.id === 'city' ? site.city : site.climateZone;
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

interface TariffTier {
  readonly tier: 'CRITICAL_PEAK' | 'PEAK' | 'MID' | 'VALLEY';
  readonly name: string;
  readonly color: string;
  readonly hours: string;
  readonly pricePerKWh: number;
  readonly multiplierText: string;
  readonly operationalAdvice: string;
}

const TARIFF_TIERS: readonly TariffTier[] = [
  {
    tier: 'CRITICAL_PEAK',
    name: '尖峰时段',
    color: 'bg-rose-500 text-white',
    hours: '14:00~16:00 / 19:00~21:00 (共4小时)',
    pricePerKWh: 1.342,
    multiplierText: '基准 × 1.70',
    operationalAdvice: '强制启用蓄冰融冰与负荷削峰，严禁非必需大功率辅机全开',
  },
  {
    tier: 'PEAK',
    name: '高峰时段',
    color: 'bg-amber-500 text-white',
    hours: '08:00~14:00 / 16:00~19:00 (共9小时)',
    pricePerKWh: 1.085,
    multiplierText: '基准 × 1.35',
    operationalAdvice: '冷机高效温差重置策略生效，变频水泵锁定经济转速区间',
  },
  {
    tier: 'MID',
    name: '平段时段',
    color: 'bg-sky-500 text-white',
    hours: '06:00~08:00 / 21:00~22:00 (共3小时)',
    pricePerKWh: 0.72,
    multiplierText: '基准平价 (1.00)',
    operationalAdvice: '常规冷量平衡供给，按需启停备用机组',
  },
  {
    tier: 'VALLEY',
    name: '低谷时段',
    color: 'bg-emerald-500 text-white',
    hours: '22:00~次日06:00 (共8小时)',
    pricePerKWh: 0.365,
    multiplierText: '基准 × 0.50',
    operationalAdvice: '启动双工况离心主机全功率制冰充能，蓄冰槽蓄冷率目标 100%',
  },
];

// 24 小时每小时的时段映射 (用于 24h 视觉色带)
const HOURLY_TARIFF_MAP: { hour: number; tier: 'VALLEY' | 'MID' | 'PEAK' | 'CRITICAL_PEAK' }[] = [
  { hour: 0, tier: 'VALLEY' },
  { hour: 1, tier: 'VALLEY' },
  { hour: 2, tier: 'VALLEY' },
  { hour: 3, tier: 'VALLEY' },
  { hour: 4, tier: 'VALLEY' },
  { hour: 5, tier: 'VALLEY' },
  { hour: 6, tier: 'MID' },
  { hour: 7, tier: 'MID' },
  { hour: 8, tier: 'PEAK' },
  { hour: 9, tier: 'PEAK' },
  { hour: 10, tier: 'PEAK' },
  { hour: 11, tier: 'PEAK' },
  { hour: 12, tier: 'PEAK' },
  { hour: 13, tier: 'PEAK' },
  { hour: 14, tier: 'CRITICAL_PEAK' },
  { hour: 15, tier: 'CRITICAL_PEAK' },
  { hour: 16, tier: 'PEAK' },
  { hour: 17, tier: 'PEAK' },
  { hour: 18, tier: 'PEAK' },
  { hour: 19, tier: 'CRITICAL_PEAK' },
  { hour: 20, tier: 'CRITICAL_PEAK' },
  { hour: 21, tier: 'MID' },
  { hour: 22, tier: 'VALLEY' },
  { hour: 23, tier: 'VALLEY' },
];

export function SiteSystemSettingsWorkspace() {
  const [searchQuery, setSearchQuery] = useState('');
  const [editingSite, setEditingSite] = useState<SiteProfile | null>(null);

  const [siteAdvancedFilters] = useQueryState(
    SITE_SETTINGS_FILTERS_QUERY_KEY,
    getFiltersStateParser<SiteProfile>([...SITE_SETTINGS_FILTER_COLUMN_IDS]).withDefault([]),
  );
  const [siteJoinOperator] = useQueryState(
    SITE_SETTINGS_JOIN_OPERATOR_QUERY_KEY,
    parseAsStringEnum<JoinOperator>(['and', 'or']).withDefault('and'),
  );

  const filteredSites = useMemo(() => {
    return SITE_PROFILES.filter((site) => {
      const q = searchQuery.trim().toLowerCase();
      const matchesSearch = !q || (
        site.name.toLowerCase().includes(q) ||
        site.code.toLowerCase().includes(q) ||
        site.city.toLowerCase().includes(q) ||
        site.climateZone.toLowerCase().includes(q)
      );
      const filterMatches = siteAdvancedFilters.map((filter) => matchesSiteSettingsAdvancedFilter(site, filter));
      const matchesAdvancedFilters = siteAdvancedFilters.length === 0 || (siteJoinOperator === 'or'
        ? filterMatches.some(Boolean)
        : filterMatches.every(Boolean));
      return matchesSearch && matchesAdvancedFilters;
    });
  }, [searchQuery, siteAdvancedFilters, siteJoinOperator]);

  const siteColumns = useMemo<Array<ColumnDef<DataTableFeatures, SiteProfile>>>(() => [
    {
      id: 'select',
      header: ({ table }) => (
        <Checkbox
          checked={table.getIsAllPageRowsSelected() ? true : table.getIsSomePageRowsSelected() ? 'indeterminate' : false}
          onCheckedChange={(checked) => table.toggleAllPageRowsSelected(Boolean(checked))}
          aria-label="选择全部受控站点"
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
    { id: 'code', accessorFn: (row) => row.code, meta: { label: '站点编码' }, header: '站点编码', cell: ({ row }) => <span className="font-mono text-[11px] font-medium text-foreground">{row.original.code}</span> },
    {
      id: 'name',
      accessorFn: (row) => row.name,
      meta: { label: '站点全称' },
      header: '站点全称',
      cell: ({ row }) => <div><div className="font-medium leading-snug text-foreground">{row.original.name}</div><div className="text-[10px] text-muted-foreground">投产: {row.original.commissioningDate}</div></div>,
    },
    {
      id: 'city',
      accessorFn: (row) => row.city,
      enableColumnFilter: true,
      meta: { label: '所属城市', variant: 'select', options: SITE_SETTINGS_CITY_OPTIONS },
      header: '所属城市',
      cell: ({ row }) => <span className="font-medium text-foreground">{row.original.city}</span>,
    },
    {
      id: 'climateZone',
      accessorFn: (row) => row.climateZone,
      enableColumnFilter: true,
      meta: { label: '气候区', variant: 'select', options: SITE_SETTINGS_CLIMATE_OPTIONS },
      header: '气候区',
      cell: ({ row }) => <StatusBadge label={row.original.climateZone} tone={row.original.climateZone === '寒冷地区' ? 'info' : row.original.climateZone === '夏热冬冷区' ? 'neutral' : 'warning'} />,
    },
    { id: 'totalArea', accessorFn: (row) => row.totalArea, meta: { label: '总建筑面积' }, header: '总建筑面积', cell: ({ row }) => <span className="font-mono text-[11px] text-foreground tabular-nums">{(row.original.totalArea / 10000).toFixed(1)} 万 m²</span> },
    { id: 'coolingArea', accessorFn: (row) => row.coolingArea, meta: { label: '供冷面积' }, header: '供冷面积', cell: ({ row }) => <span className="font-mono text-[11px] text-foreground tabular-nums">{(row.original.coolingArea / 10000).toFixed(1)} 万 m²</span> },
    { id: 'designLoad', accessorFn: (row) => row.designCoolingLoad, meta: { label: '设计冷负荷' }, header: '设计冷负荷', cell: ({ row }) => <span className="font-mono text-[11px] text-foreground tabular-nums">{row.original.designCoolingLoad} RT</span> },
    { id: 'loadIndex', accessorFn: (row) => row.coolingLoadIndex, meta: { label: '冷负荷指标' }, header: '冷负荷指标', cell: ({ row }) => <span className="font-mono text-[11px] font-medium text-emerald-600 tabular-nums">{row.original.coolingLoadIndex} W/m²</span> },
    { id: 'weatherSource', accessorFn: (row) => row.weatherSource, meta: { label: '室外气象站数据源' }, header: '室外气象站数据源', cell: ({ row }) => <span className="text-[11px] text-muted-foreground">{row.original.weatherSource}</span> },
    {
      id: 'actions',
      header: '操作',
      cell: ({ row }) => <Button variant="ghost" size="sm" onClick={() => setEditingSite(row.original)} className="h-7 px-2 text-xs text-primary hover:bg-primary/10 hover:text-primary">编辑参数</Button>,
      enableSorting: false,
    },
  ], []);

  const siteTable = useDataTable({
    key: 'surface-34-site-settings',
    data: [...filteredSites],
    columns: siteColumns,
    pageSize: 10,
    getRowId: (row) => row.id,
    meta: {
      queryKeys: {
        page: 'siteSettingsPage',
        perPage: 'siteSettingsPerPage',
        sort: 'siteSettingsSort',
        filters: SITE_SETTINGS_FILTERS_QUERY_KEY,
        joinOperator: SITE_SETTINGS_JOIN_OPERATOR_QUERY_KEY,
      },
    },
  });


  return (
    <Main className="space-y-6">
      {/* 顶部标题区 */}
      <div className="flex flex-col gap-3 md:flex-row md:items-center md:justify-between border-b pb-4">
        <div>
          
        </div>
        <div className="flex items-center gap-2">
          <Button size="sm" className="h-8 text-xs gap-1.5">
            <Plus className="h-3.5 w-3.5" />
            新增站点
          </Button>
        </div>
      </div>

      {/* 紧凑型系统运行态势带 (替代 4 个模板卡片) */}
      <div className="rounded-lg border border-border/80 bg-muted/20 p-4">
        <div className="grid grid-cols-2 gap-4 md:grid-cols-4 md:divide-x md:divide-border/60">
          <div className="space-y-1">
            <div className="text-[11px] font-medium text-muted-foreground">建筑面积</div>
            <div className="flex items-baseline gap-2">
              <span className="text-xl font-bold font-mono text-foreground">63.5</span>
              <span className="text-xs text-muted-foreground">万 m² (4 个枢纽站点)</span>
            </div>
            <div className="text-[11px] text-muted-foreground">
              空调服务面积 58.7 万 m² (占比 92.4%)
            </div>
          </div>

          <div className="space-y-1 md:pl-6">
            <div className="text-[11px] font-medium text-muted-foreground">当前运营季节模式</div>
            <div className="flex items-baseline gap-2">
              <span className="text-xl font-bold text-foreground flex items-center gap-1.5">
                <Snowflake className="h-4 w-4 text-sky-600" />
                夏季全制冷
              </span>
            </div>
            <div className="text-[11px] text-muted-foreground">
              周期: 05-01 ~ 10-15 · 工作日 07:30~20:00
            </div>
          </div>

          <div className="space-y-1 md:pl-6">
            <div className="text-[11px] font-medium text-muted-foreground">分时电价方案 (TOU)</div>
            <div className="flex items-baseline gap-2">
              <span className="text-xl font-bold tracking-tight text-foreground"><span className="tabular-nums">4</span> 段时率</span>
              <span className="text-xs text-muted-foreground">尖 / 峰 / 平 / 谷</span>
            </div>
            <div className="text-[11px] text-muted-foreground">
              尖峰 <span className="tabular-nums">¥1.342</span> · 蓄冰低谷 <span className="tabular-nums">¥0.365</span>
            </div>
          </div>

          <div className="space-y-1 md:pl-6">
            <div className="text-[11px] font-medium text-muted-foreground">室外气象源冗余</div>
            <div className="flex items-baseline gap-2">
              <span className="text-xl font-bold text-emerald-600 flex items-center gap-1">
                <CheckCircle2 className="h-4 w-4" /> 双冗余热备
              </span>
            </div>
            <div className="text-[11px] text-muted-foreground">
              就地微气象站 (首选) + 国家气象 API
            </div>
          </div>
        </div>
      </div>

      {/* 工作区 Tabs */}
      <Tabs defaultValue="sites" className="space-y-4">
        <TabsList className="bg-muted/50 p-1 border border-border/60">
          <TabsTrigger value="sites" className="text-xs gap-1.5">
            <Building className="h-3.5 w-3.5" />
            站点 ({SITE_PROFILES.length})
          </TabsTrigger>
          <TabsTrigger value="tariff" className="text-xs gap-1.5">
            <Zap className="h-3.5 w-3.5" />
            分时电价方案
          </TabsTrigger>
          <TabsTrigger value="calendar" className="text-xs gap-1.5">
            <Calendar className="h-3.5 w-3.5" />
            运行日历与作息
          </TabsTrigger>
        </TabsList>

        {/* Tab 1: 站点物理台账 */}
        <TabsContent value="sites" className="mt-0">
          <DataTableBlock aria-label="站点基础参数清单">
            <DataTable
            table={siteTable}
            tableAriaLabel="站点基础参数清单"
            empty="无匹配的受控站点记录"
            getHeaderRowProps={() => ({ className: 'bg-muted/40' })}
            getHeaderCellProps={(header) => ({
              className:
                header.id === 'select' ? 'w-[40px] px-3' :
                header.id === 'code' ? 'w-[120px] font-medium' :
                header.id === 'name' ? 'w-[180px] font-medium' :
                header.id === 'city' ? 'w-[120px] font-medium' :
                header.id === 'climateZone' ? 'w-[130px] font-medium' :
                header.id === 'totalArea' || header.id === 'coolingArea' ? 'w-[120px] text-right font-medium' :
                header.id === 'designLoad' || header.id === 'loadIndex' ? 'w-[110px] text-right font-medium' :
                header.id === 'weatherSource' ? 'font-medium' :
                header.id === 'actions' ? 'w-[90px] text-right font-medium' :
                undefined,
            })}
            getCellProps={(cell) => ({
              className:
                cell.column.id === 'select' ? 'px-3' :
                ['totalArea', 'coolingArea', 'designLoad', 'loadIndex', 'actions'].includes(cell.column.id) ? 'text-right' :
                undefined,
            })}
            footer={(
              <DataTablePagination
                table={siteTable}
                totalRows={filteredSites.length}
              />
            )}
          >
            <DataTableAdvancedToolbar table={siteTable}>
              <div className="relative w-64">
                <Search className="absolute left-2.5 top-2.5 h-3.5 w-3.5 text-muted-foreground" />
                <Input
                  placeholder="按站点名称 / 编码 / 气候区搜索..."
                  value={searchQuery}
                  onChange={(e) => {
                    setSearchQuery(e.target.value);
                    siteTable.setPageIndex(0);
                  }}
                  className="h-8 bg-background pl-8 text-xs"
                />
              </div>
              <DataTableSortList table={siteTable} />
              <DataTableFilterList table={siteTable} />
              <div className="text-xs text-muted-foreground">
                共 <span className="tabular-nums font-medium">{filteredSites.length}</span> 个站点
              </div>
            </DataTableAdvancedToolbar>
            </DataTable>
          </DataTableBlock>
        </TabsContent>

        {/* Tab 2: 分时电价方案与 24 小时时段色带 */}
        <TabsContent value="tariff" className="space-y-4 mt-0">
          <div className="rounded-lg border border-border/80 bg-card p-5 space-y-4">
            <div>
              <div className="text-sm font-semibold text-foreground">24 小时峰谷电价时段分布</div>
              <div className="text-xs text-muted-foreground mt-0.5">
                用于指导蓄冷充放策略与尖峰负荷平抑调度
              </div>
            </div>

            {/* 24 小时连续色带 */}
            <div className="space-y-2">
              <div className="grid grid-cols-24 gap-0.5 h-9 rounded-md overflow-hidden p-0.5 bg-muted/40 border">
                {HOURLY_TARIFF_MAP.map((item) => {
                  const tierInfo = TARIFF_TIERS.find((t) => t.tier === item.tier);
                  return (
                    <div
                      key={item.hour}
                      className={`flex flex-col items-center justify-center text-[10px] font-mono font-medium rounded-xs transition-transform hover:scale-105 cursor-pointer ${
                        item.tier === 'CRITICAL_PEAK'
                          ? 'bg-rose-500 text-white'
                          : item.tier === 'PEAK'
                          ? 'bg-amber-500 text-white'
                          : item.tier === 'MID'
                          ? 'bg-sky-500 text-white'
                          : 'bg-emerald-500 text-white'
                      }`}
                      title={`${item.hour}:00~${item.hour + 1}:00: ${tierInfo?.name} (¥${tierInfo?.pricePerKWh}/kWh)`}
                    >
                      <span>{item.hour}</span>
                    </div>
                  );
                })}
              </div>

              {/* 色带图例与比例 */}
              <div className="flex flex-wrap items-center justify-between text-xs pt-1">
                <div className="flex items-center gap-4">
                  <div className="flex items-center gap-1.5">
                    <span className="h-2.5 w-2.5 rounded-xs bg-rose-500" />
                    <span className="text-muted-foreground">尖峰时段 (14-16 / 19-21)</span>
                  </div>
                  <div className="flex items-center gap-1.5">
                    <span className="h-2.5 w-2.5 rounded-xs bg-amber-500" />
                    <span className="text-muted-foreground">高峰时段 (08-14 / 16-19)</span>
                  </div>
                  <div className="flex items-center gap-1.5">
                    <span className="h-2.5 w-2.5 rounded-xs bg-sky-500" />
                    <span className="text-muted-foreground">平段时段 (06-08 / 21-22)</span>
                  </div>
                  <div className="flex items-center gap-1.5">
                    <span className="h-2.5 w-2.5 rounded-xs bg-emerald-500" />
                    <span className="text-muted-foreground">低谷蓄能 (22-次日06)</span>
                  </div>
                </div>
                <div className="text-xs text-muted-foreground">
                  尖谷价差达 <span className="tabular-nums font-semibold">3.68</span> 倍 · 蓄冰经济性显著
                </div>
              </div>
            </div>

            {/* 电价资费明细表 */}
            <Table className="mt-3" aria-label="分时电价">
              <TableHeader className="bg-muted/40">
                <TableRow className="hover:bg-transparent">
                  <TableHead className="w-[120px]">费率时段分类</TableHead>
                  <TableHead className="w-[240px]">对应小时区间</TableHead>
                  <TableHead className="w-[120px] text-right">度电电价标准</TableHead>
                  <TableHead className="w-[120px] text-right">基准浮动倍率</TableHead>
                  <TableHead>调度策略建议</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {TARIFF_TIERS.map((tier) => (
                  <TableRow key={tier.tier}>
                    <TableCell>
                      <StatusBadge
                        label={tier.name}
                        tone={tier.tier === 'CRITICAL_PEAK' ? 'destructive' : tier.tier === 'PEAK' ? 'warning' : tier.tier === 'MID' ? 'info' : 'success'}
                      />
                    </TableCell>
                    <TableCell className="font-mono text-xs font-medium tabular-nums text-foreground">{tier.hours}</TableCell>
                    <TableCell className="text-right font-mono text-xs font-semibold tabular-nums text-foreground">{tier.pricePerKWh.toFixed(3)} 元/kWh</TableCell>
                    <TableCell className="text-right font-mono text-xs tabular-nums text-muted-foreground">{tier.multiplierText}</TableCell>
                    <TableCell className="text-xs text-muted-foreground">{tier.operationalAdvice}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        </TabsContent>

        {/* Tab 3: 运行季节与作息日历 */}
        <TabsContent value="calendar" className="space-y-4 mt-0">
          <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
            <div className="rounded-lg border border-border/80 bg-card p-4 space-y-2">
              <div className="flex items-center gap-2 text-sky-600 font-semibold text-sm">
                <Snowflake className="h-4 w-4" /> 夏季全制冷模式
              </div>
              <div className="text-xs text-muted-foreground">
                适用周期: 每年 05 月 01 日 至 10 月 15 日
              </div>
              <div className="rounded bg-muted/40 p-2.5 text-xs text-muted-foreground space-y-1">
                <div>• 冷水机组与冷却塔水系统全负荷就绪</div>
                <div>• 设定值自适应下调：供水 7.0℃ / 回水 12.0℃</div>
                <div>• 提前 30 分钟预冷 (07:00 启动主机)</div>
              </div>
            </div>

            <div className="rounded-lg border border-border/80 bg-card p-4 space-y-2">
              <div className="flex items-center gap-2 text-amber-600 font-semibold text-sm">
                <Sun className="h-4 w-4" /> 过渡季自然冷却 (Free Cooling)
              </div>
              <div className="text-xs text-muted-foreground">
                适用周期: 每年 03-15~04-30 / 10-16~11-15
              </div>
              <div className="rounded bg-muted/40 p-2.5 text-xs text-muted-foreground space-y-1">
                <div>• 当室外湿球温度 &lt; 10℃ 时优先开启板式换热器</div>
                <div>• 离心主机完全停机，仅开启冷却塔与水泵</div>
                <div>• 节能潜力：节约主机电耗 85% 以上</div>
              </div>
            </div>

            <div className="rounded-lg border border-border/80 bg-card p-4 space-y-2">
              <div className="flex items-center gap-2 text-rose-600 font-semibold text-sm">
                <Flame className="h-4 w-4" /> 冬季采暖与防冻保护
              </div>
              <div className="text-xs text-muted-foreground">
                适用周期: 每年 11 月 16 日 至 次年 03 月 14 日
              </div>
              <div className="rounded bg-muted/40 p-2.5 text-xs text-muted-foreground space-y-1">
                <div>• 热泵系统/市政热网阀门投入运行</div>
                <div>• 冷却塔室外管路完全排空防冻排水阀打开</div>
                <div>• 室内设计基准温度保持 20.0 ± 1.0℃</div>
              </div>
            </div>
          </div>
        </TabsContent>
      </Tabs>

      {/* 站点参数编辑抽屉 */}
      <Sheet open={Boolean(editingSite)} onOpenChange={(open) => !open && setEditingSite(null)}>
        <SheetContent className="sm:max-w-md! overflow-y-auto">
          <SheetHeader>
            <div className="flex items-center gap-2">
              <Settings2 className="h-5 w-5 text-primary" />
              <SheetTitle className="text-base font-bold">编辑站点参数</SheetTitle>
            </div>
            <SheetDescription className="text-xs">
              修改 {editingSite?.name} ({editingSite?.code}) 的建筑与供能参数
            </SheetDescription>
          </SheetHeader>

          {editingSite && (
            <div className="space-y-4 py-4 text-xs">
              <div className="space-y-1.5">
                <label className="text-muted-foreground font-medium">站点名称</label>
                <Input defaultValue={editingSite.name} className="h-8 text-xs" />
              </div>
              <div className="grid grid-cols-2 gap-3">
                <div className="space-y-1.5">
                  <label className="text-muted-foreground font-medium">总建筑面积 (m²)</label>
                  <Input defaultValue={editingSite.totalArea} className="h-8 text-xs font-mono" />
                </div>
                <div className="space-y-1.5">
                  <label className="text-muted-foreground font-medium">空调供冷面积 (m²)</label>
                  <Input defaultValue={editingSite.coolingArea} className="h-8 text-xs font-mono" />
                </div>
              </div>
              <div className="grid grid-cols-2 gap-3">
                <div className="space-y-1.5">
                  <label className="text-muted-foreground font-medium">设计冷负荷 (RT)</label>
                  <Input defaultValue={editingSite.designCoolingLoad} className="h-8 text-xs font-mono" />
                </div>
                <div className="space-y-1.5">
                  <label className="text-muted-foreground font-medium">冷指标 (W/m²)</label>
                  <Input defaultValue={editingSite.coolingLoadIndex} className="h-8 text-xs font-mono" />
                </div>
              </div>
              <div className="space-y-1.5">
                <label className="text-muted-foreground font-medium">绑定的室外气象源</label>
                <Input defaultValue={editingSite.weatherSource} className="h-8 text-xs" />
              </div>

              <div className="pt-4 flex items-center justify-end gap-2">
                <Button variant="outline" size="sm" onClick={() => setEditingSite(null)}>
                  取消
                </Button>
                <Button size="sm" onClick={() => setEditingSite(null)}>
                  保存配置
                </Button>
              </div>
            </div>
          )}
        </SheetContent>
      </Sheet>
    </Main>
  );
}
