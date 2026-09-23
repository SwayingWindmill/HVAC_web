import { useState, useMemo } from 'react';
import type { ColumnDef } from '@tanstack/react-table';
import {
  Building2,
  AlertTriangle,
  TrendingDown,
  Activity,
  ArrowUpRight,
  ExternalLink,
  ShieldAlert,
  Sparkles,
  Search,
  Layers,
  DollarSign,
  ChevronRight,
  PhoneCall,
} from 'lucide-react';
import { Main } from '@/components/layout/Main';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { Progress } from '@/components/ui/progress';
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
import {
  Sheet,
  SheetBody,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet';
import {
  ResponsiveContainer,
  BarChart,
  Bar,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  Legend,
} from 'recharts';

interface PortfolioSite {
  readonly id: string;
  readonly code: string;
  readonly name: string;
  readonly region: string;
  readonly buildingArea: number; // m²
  readonly hvacType: string;
  readonly currentCop: number;
  readonly designCop: number;
  readonly monthlyConsumption: number; // MWh
  readonly eui: number; // kWh/m²·a
  readonly normalizedEui: number; // CDD normalized
  readonly peerRank: string;
  readonly activeAlerts: number;
  readonly highRiskAlerts: number;
  readonly dataCompleteness: number; // %
  readonly status: 'normal' | 'attention' | 'critical';
  readonly keyIssue?: string;
  readonly leadEngineer?: string;
}

const PORTFOLIO_SITES: readonly PortfolioSite[] = [
  {
    id: '018f3a8b-1234-7abc-8def-0123456789ab',
    code: 'SHA-HQ',
    name: '上海虹桥综合枢纽站',
    region: '华东大区',
    buildingArea: 145000,
    hvacType: '离心机+变频泵+蓄冰',
    currentCop: 4.85,
    designCop: 5.2,
    monthlyConsumption: 382.4,
    eui: 74.2,
    normalizedEui: 71.8,
    peerRank: '前 15%',
    activeAlerts: 1,
    highRiskAlerts: 0,
    dataCompleteness: 99.8,
    status: 'normal',
    leadEngineer: '张工 (138-0021-0001)',
  },
  {
    id: '018f3a8b-5678-7abc-8def-0123456789cd',
    code: 'BJS-TOWER',
    name: '北京国贸超高层冷站',
    region: '华北大区',
    buildingArea: 182000,
    hvacType: '双工况磁悬浮+大温差水泵',
    currentCop: 4.12,
    designCop: 4.9,
    monthlyConsumption: 465.1,
    eui: 92.5,
    normalizedEui: 88.3,
    peerRank: '前 45%',
    activeAlerts: 4,
    highRiskAlerts: 2,
    dataCompleteness: 94.2,
    status: 'critical',
    keyIssue: 'CH-02 持续喘振临界点，冷却水供水温差 < 3.2℃ 小温差大流量',
    leadEngineer: '李工 (139-0010-0002)',
  },
  {
    id: '018f3a8b-9012-7abc-8def-0123456789ef',
    code: 'SZX-PARK',
    name: '深圳湾科技生态园北区',
    region: '大湾区',
    buildingArea: 98000,
    hvacType: '变频离心机+板换免费供冷',
    currentCop: 4.62,
    designCop: 4.8,
    monthlyConsumption: 245.8,
    eui: 68.4,
    normalizedEui: 66.5,
    peerRank: '前 20%',
    activeAlerts: 2,
    highRiskAlerts: 0,
    dataCompleteness: 99.1,
    status: 'normal',
    leadEngineer: '王工 (137-0755-0003)',
  },
  {
    id: '018f3a8b-3456-7abc-8def-012345678901',
    code: 'WUH-CENTER',
    name: '武汉光谷中心城能源中心',
    region: '华中大区',
    buildingArea: 120000,
    hvacType: '水冷螺杆+吸收式机组',
    currentCop: 3.78,
    designCop: 4.5,
    monthlyConsumption: 338.9,
    eui: 88.6,
    normalizedEui: 85.1,
    peerRank: '后 30%',
    activeAlerts: 5,
    highRiskAlerts: 1,
    dataCompleteness: 91.5,
    status: 'attention',
    keyIssue: '冷水二次泵旁通阀内漏导致回水混合损失，能耗偏离基线 +14.2%',
    leadEngineer: '陈工 (136-0027-0004)',
  },
  {
    id: '018f3a8b-7890-7abc-8def-012345678923',
    code: 'CAN-AIRPORT',
    name: '广州白云机场物流冷热站',
    region: '大湾区',
    buildingArea: 210000,
    hvacType: '高效冷水机房全变频',
    currentCop: 5.34,
    designCop: 5.4,
    monthlyConsumption: 512.6,
    eui: 61.2,
    normalizedEui: 59.8,
    peerRank: '前 5% (标杆)',
    activeAlerts: 0,
    highRiskAlerts: 0,
    dataCompleteness: 100,
    status: 'normal',
    leadEngineer: '周工 (135-0020-0005)',
  },
  {
    id: '018f3a8b-1122-7abc-8def-012345678945',
    code: 'CTU-HIGH-TECH',
    name: '成都高新智慧科技城',
    region: '西南大区',
    buildingArea: 86000,
    hvacType: '直燃机+电制冷离心机',
    currentCop: 3.95,
    designCop: 4.4,
    monthlyConsumption: 218.3,
    eui: 81.4,
    normalizedEui: 80.2,
    peerRank: '后 25%',
    activeAlerts: 3,
    highRiskAlerts: 0,
    dataCompleteness: 96.8,
    status: 'attention',
    keyIssue: '室外湿球温度逼近度过大 (Approach > 4.8℃)，冷却塔填料结垢',
    leadEngineer: '赵工 (133-0028-0006)',
  },
];

const MONTHLY_TREND_DATA = [
  { month: '2026-03', actualMWh: 1680, baselineMWh: 1920, savingsMWh: 240, costK: 1344 },
  { month: '2026-04', actualMWh: 1840, baselineMWh: 2110, savingsMWh: 270, costK: 1472 },
  { month: '2026-05', actualMWh: 2190, baselineMWh: 2540, savingsMWh: 350, costK: 1752 },
  { month: '2026-06', actualMWh: 2680, baselineMWh: 3120, savingsMWh: 440, costK: 2144 },
  { month: '2026-07', actualMWh: 3250, baselineMWh: 3810, savingsMWh: 560, costK: 2600 },
  { month: '2026-08', actualMWh: 3410, baselineMWh: 4020, savingsMWh: 610, costK: 2728 },
];

const PORTFOLIO_OPPORTUNITIES = [
  {
    id: 'OPP-PORT-01',
    site: '北京国贸超高层冷站',
    title: '冷却水大温差低阻改造与出水温度设定值动态重置',
    category: '水力平衡与控制',
    potentialSavingMWh: 310,
    estimatedSavingCost: '¥24.8万/年',
    paybackMonths: 4.2,
    risk: '低风险',
    status: '工程方案评审中',
  },
  {
    id: 'OPP-PORT-02',
    site: '武汉光谷中心城能源中心',
    title: '冷水二次泵压差设定值动态优化与内漏旁通阀切断检修',
    category: '管网平衡与变频',
    potentialSavingMWh: 195,
    estimatedSavingCost: '¥15.6万/年',
    paybackMonths: 1.5,
    risk: '低风险',
    status: '已立项待施工',
  },
  {
    id: 'OPP-PORT-03',
    site: '成都高新智慧科技城',
    title: '冷却塔填料高压化学清洗与风机变频联控算法升级',
    category: '冷却塔优化',
    potentialSavingMWh: 140,
    estimatedSavingCost: '¥11.2万/年',
    paybackMonths: 3.0,
    risk: '低风险',
    status: '待提报',
  },
];

const CRITICAL_PORTFOLIO_RISKS = [
  {
    id: 'RISK-01',
    site: '北京国贸超高层冷站',
    siteId: '018f3a8b-5678-7abc-8def-0123456789cd',
    level: 'CRITICAL',
    title: 'CH-02 变频离心机在 45Hz 低负荷工况下持续接近喘振边界',
    source: '物理防喘振线遥测对比',
    impact: '机组机械疲劳破坏风险，当前排气压力与吸气压力差持续高于安全线 8%',
    action: '需立即派发紧急就地工单检查导叶开度传感器并执行最小排气升压保护',
    time: '持续 1小时42分钟',
  },
  {
    id: 'RISK-02',
    site: '武汉光谷中心城能源中心',
    siteId: '018f3a8b-3456-7abc-8def-012345678901',
    level: 'MAJOR',
    title: '冷冻水供回水大温差严重衰减 (ΔT = 2.1℃ vs 设计 5.0℃)',
    source: '水力热工遥测',
    impact: '系统输送系数大幅恶化，循环水泵耗电较标杆增加 48.5%',
    action: '已自动转派工单 WO-202609-082 排查末端旁通与电动二通阀关断性',
    time: '持续 3天11小时',
  },
];

export function PortfolioOverviewWorkspace() {
  const [selectedRegion, setSelectedRegion] = useState<string>('all');
  const [selectedStatus, setSelectedStatus] = useState<string>('all');
  const [searchQuery, setSearchQuery] = useState('');
  const [inspectedSite, setInspectedSite] = useState<PortfolioSite | null>(null);

  const siteStatusPills = useMemo<readonly DataTableViewPillOption[]>(() => [
    { id: 'all', label: '全部站点', count: PORTFOLIO_SITES.length },
    { id: 'normal', label: '正常运行', count: PORTFOLIO_SITES.filter((s) => s.status === 'normal').length },
    { id: 'attention', label: '能效偏离', count: PORTFOLIO_SITES.filter((s) => s.status === 'attention').length },
    { id: 'critical', label: '关键风险', count: PORTFOLIO_SITES.filter((s) => s.status === 'critical').length },
  ], []);

  const filteredSites = useMemo(() => {
    return PORTFOLIO_SITES.filter((site) => {
      const matchRegion = selectedRegion === 'all' || site.region === selectedRegion;
      const matchStatus = selectedStatus === 'all' || site.status === selectedStatus;
      const matchQuery =
        !searchQuery.trim() ||
        site.name.toLowerCase().includes(searchQuery.toLowerCase()) ||
        site.code.toLowerCase().includes(searchQuery.toLowerCase()) ||
        site.region.toLowerCase().includes(searchQuery.toLowerCase());
      return matchRegion && matchStatus && matchQuery;
    });
  }, [searchQuery, selectedRegion, selectedStatus]);

  const siteColumns = useMemo<Array<ColumnDef<DataTableFeatures, PortfolioSite>>>(() => [
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
          onClick={(event) => event.stopPropagation()}
        />
      ),
      enableSorting: false,
      enableHiding: false,
    },
    {
      id: 'site',
      header: '站点编码与名称',
      cell: ({ row }) => (
        <div className="font-medium">
          <div className="font-mono text-xs text-muted-foreground">{row.original.code}</div>
          <div className="text-sm font-semibold text-foreground">{row.original.name}</div>
          {row.original.keyIssue ? <div className="mt-1 max-w-xs rounded bg-destructive/10 px-1.5 py-0.5 text-[11px] leading-tight text-destructive">{row.original.keyIssue}</div> : null}
        </div>
      ),
    },
    { id: 'regionArea', header: '大区 / 面积', cell: ({ row }) => <div><div className="font-medium text-foreground">{row.original.region}</div><div className="font-mono text-[11px] text-muted-foreground tabular-nums">{(row.original.buildingArea / 10000).toFixed(1)} 万 m²</div></div> },
    { id: 'hvacType', header: '冷站系统架构', cell: ({ row }) => <span className="text-muted-foreground">{row.original.hvacType}</span> },
    {
      id: 'cop',
      header: '当前 COP / 设计',
      cell: ({ row }) => <span className="font-mono tabular-nums"><span className={`font-semibold ${row.original.currentCop < 4.0 ? 'font-bold text-destructive' : 'text-foreground'}`}>{row.original.currentCop.toFixed(2)}</span><span className="text-[11px] text-muted-foreground"> / {row.original.designCop.toFixed(1)}</span></span>,
    },
    { id: 'consumption', header: '月度用能 (MWh)', cell: ({ row }) => <span className="font-mono font-medium text-foreground tabular-nums">{row.original.monthlyConsumption.toFixed(1)}</span> },
    { id: 'eui', header: '实测 EUI / 气象归一化', cell: ({ row }) => <span className="font-mono tabular-nums"><span className="font-medium text-foreground">{row.original.eui.toFixed(1)}</span><span className="text-[11px] text-muted-foreground"> / {row.original.normalizedEui.toFixed(1)}</span></span> },
    { id: 'peerRank', header: '同行排位', cell: ({ row }) => <span className="font-medium text-muted-foreground">{row.original.peerRank}</span> },
    {
      id: 'completeness',
      header: '采集完整率',
      cell: ({ row }) => <span className={`font-mono font-medium tabular-nums ${row.original.dataCompleteness >= 99 ? 'text-emerald-600' : row.original.dataCompleteness < 95 ? 'text-amber-600' : 'text-foreground'}`}>{row.original.dataCompleteness}%</span>,
    },
    {
      id: 'status',
      header: '运行状态',
      cell: ({ row }) => row.original.status === 'critical'
        ? <StatusPillBadge label={`关键风险 (${row.original.highRiskAlerts})`} tone="destructive" pulse />
        : row.original.status === 'attention'
          ? <StatusPillBadge label={`能效偏离 (${row.original.activeAlerts})`} tone="warning" />
          : <StatusPillBadge label="工况达标" tone="success" />,
    },
    {
      id: 'action',
      header: '操作',
      cell: ({ row }) => (
        <div className="flex items-center justify-end gap-1">
          <Button variant="ghost" size="sm" className="h-7 px-2 text-xs" onClick={(event) => { event.stopPropagation(); setInspectedSite(row.original); }}>查看</Button>
          <Button variant="outline" size="sm" asChild className="h-7 px-2 text-xs">
            <a href={`/sites/${row.original.id}/overview`} onClick={(event) => event.stopPropagation()} className="flex items-center gap-1">进入<ArrowUpRight className="h-3 w-3" /></a>
          </Button>
        </div>
      ),
      enableSorting: false,
    },
  ], []);

  const siteTable = useDataTable({
    key: 'surface-01-portfolio-sites',
    data: [...filteredSites],
    columns: siteColumns,
    pageSize: 10,
    getRowId: (row) => row.id,
  });

  type PortfolioOpportunity = (typeof PORTFOLIO_OPPORTUNITIES)[number];

  const opportunityColumns = useMemo<Array<ColumnDef<DataTableFeatures, PortfolioOpportunity>>>(() => [
    {
      id: 'select',
      header: ({ table }) => (
        <Checkbox
          checked={table.getIsAllPageRowsSelected() ? true : table.getIsSomePageRowsSelected() ? 'indeterminate' : false}
          onCheckedChange={(checked) => table.toggleAllPageRowsSelected(Boolean(checked))}
          aria-label="全选节能机会"
        />
      ),
      cell: ({ row }) => (
        <Checkbox
          checked={row.getIsSelected()}
          onCheckedChange={(checked) => row.toggleSelected(Boolean(checked))}
          aria-label={`选择 ${row.original.title}`}
        />
      ),
      enableSorting: false,
      enableHiding: false,
    },
    { id: 'id', header: '机会编号', cell: ({ row }) => <span className="font-mono font-semibold text-foreground">{row.original.id}</span> },
    { id: 'site', header: '所属站点', cell: ({ row }) => <span className="font-semibold text-foreground">{row.original.site}</span> },
    { id: 'title', header: '优化方案陈述', cell: ({ row }) => <span className="font-medium text-foreground">{row.original.title}</span> },
    { id: 'category', header: '专业类别', cell: ({ row }) => <span className="rounded bg-muted px-2 py-0.5 text-[11px] font-medium text-muted-foreground">{row.original.category}</span> },
    { id: 'saving', header: '预期年节电量', cell: ({ row }) => <span className="font-mono font-semibold text-foreground tabular-nums">+{row.original.potentialSavingMWh} MWh</span> },
    { id: 'cost', header: '预期经济效益', cell: ({ row }) => <span className="font-mono font-semibold text-foreground tabular-nums">{row.original.estimatedSavingCost}</span> },
    { id: 'payback', header: '静态回收期', cell: ({ row }) => <span className="font-mono font-medium tabular-nums">{row.original.paybackMonths} 个月</span> },
    { id: 'status', header: '当前状态', cell: ({ row }) => <StatusPillBadge label={row.original.status} tone={row.original.status === '执行中' ? 'success' : row.original.status === '待立项' ? 'warning' : 'info'} /> },
    {
      id: 'actions',
      header: '操作',
      cell: () => <Button variant="ghost" size="sm" asChild className="h-7 px-2 text-xs"><a href="/sites/018f3a8b-1234-7abc-8def-0123456789ab/opportunities">下钻<ChevronRight className="ml-0.5 h-3 w-3" /></a></Button>,
      enableSorting: false,
      enableHiding: false,
    },
  ], []);

  const opportunityTable = useDataTable({
    key: 'surface-01-portfolio-opportunities',
    data: [...PORTFOLIO_OPPORTUNITIES],
    columns: opportunityColumns,
    pageSize: 10,
    getRowId: (row) => row.id,
  });

  const totalMonthlyMWh = PORTFOLIO_SITES.reduce((sum, s) => sum + s.monthlyConsumption, 0);
  const totalArea = PORTFOLIO_SITES.reduce((sum, s) => sum + s.buildingArea, 0);
  const avgCop = (PORTFOLIO_SITES.reduce((sum, s) => sum + s.currentCop, 0) / PORTFOLIO_SITES.length).toFixed(2);
  const designAvgCop = (PORTFOLIO_SITES.reduce((s, x) => s + x.designCop, 0) / PORTFOLIO_SITES.length).toFixed(2);
  const totalHighRisk = PORTFOLIO_SITES.reduce((sum, s) => sum + s.highRiskAlerts, 0);
  const attentionCount = PORTFOLIO_SITES.filter((s) => s.status !== 'normal').length;

  return (
    <Main className="space-y-6">
      {/* 顶部标题与范围标识 */}
      <div className="flex flex-col gap-3 md:flex-row md:items-center md:justify-between border-b pb-4">
        <div>
          <div className="flex items-center gap-2">
            <h1 className="text-2xl font-bold tracking-tight text-foreground">企业总览</h1>
            <Badge variant="outline" className="border-border text-foreground/80 font-mono text-xs">
              集团概览
            </Badge>
            <span className="rounded bg-muted px-2 py-0.5 font-mono text-xs text-muted-foreground">
              {PORTFOLIO_SITES.length} 个已接入站点
            </span>
          </div>
          <p className="text-sm text-muted-foreground mt-1">
            查看集团各站点用能指标、运行安全风险与节能推进情况
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" asChild>
            <a href="/sites/018f3a8b-1234-7abc-8def-0123456789ab/benchmarking" className="flex items-center gap-1.5">
              <Layers className="h-4 w-4" />
              进入全景对标分析
            </a>
          </Button>
          <Button size="sm" asChild>
            <a href="/sites/018f3a8b-1234-7abc-8def-0123456789ab/reports" className="flex items-center gap-1.5">
              <ExternalLink className="h-4 w-4" />
              导出集团能效月报
            </a>
          </Button>
        </div>
      </div>

      {/* Portfolio Operational Posture Cards */}
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
        {/* 1. 集团用电量 */}
        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-xs font-medium text-muted-foreground">集团总用电量 (本月)</CardTitle>
            <Activity className="size-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="flex items-baseline gap-2">
              <span className="text-2xl font-bold tracking-tight text-foreground tabular-nums">
                {totalMonthlyMWh.toLocaleString(undefined, { maximumFractionDigits: 1 })}
              </span>
              <span className="text-xs text-muted-foreground">MWh</span>
            </div>
            <div className="mt-2 space-y-1 text-xs border-t pt-2">
              <div className="flex items-center justify-between text-muted-foreground">
                <span>覆盖建筑面积</span>
                <span className="text-foreground font-medium tabular-nums">{(totalArea / 10000).toFixed(1)} 万 m²</span>
              </div>
              <div className="flex items-center justify-between text-[11px] text-muted-foreground">
                <span>同期对比</span>
                <span className="text-emerald-600 font-medium tabular-nums">-14.8% (节约 384 MWh)</span>
              </div>
            </div>
          </CardContent>
        </Card>

        {/* 2. 集团加权 COP */}
        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-xs font-medium text-muted-foreground">冷站加权平均 COP</CardTitle>
            <TrendingDown className="size-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="flex items-baseline gap-2">
              <span className="text-2xl font-bold tracking-tight text-foreground tabular-nums">{avgCop}</span>
              <span className="text-xs text-muted-foreground tabular-nums">/ 设计 {designAvgCop}</span>
            </div>
            <div className="mt-2 space-y-1 text-xs border-t pt-2">
              <div className="flex items-center justify-between text-muted-foreground">
                <span>最优站点</span>
                <span className="text-foreground font-medium">5.34 (广州白云)</span>
              </div>
              <div className="flex items-center justify-between text-[11px] text-muted-foreground">
                <span>偏离站点</span>
                <span className="text-foreground font-medium">2 站 COP &lt; 4.0</span>
              </div>
            </div>
          </CardContent>
        </Card>

        {/* 3. 重大安全与运行风险 */}
        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-xs font-medium text-muted-foreground">运行安全与异常风险</CardTitle>
            <AlertTriangle className="size-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="flex items-baseline gap-2">
              <span className="text-2xl font-bold tracking-tight text-foreground tabular-nums">{totalHighRisk}</span>
              <span className="text-xs text-muted-foreground font-medium">项重大 / {attentionCount} 站偏离</span>
            </div>
            <div className="mt-2 space-y-1 text-xs border-t pt-2">
              <div className="flex items-center justify-between text-muted-foreground">
                <span>主要隐患</span>
                <span className="text-destructive text-[11px] font-medium">北京国贸 CH-02 喘振</span>
              </div>
              <div className="flex items-center justify-between text-[11px] text-muted-foreground">
                <span>水力工况</span>
                <span className="text-foreground font-medium">武汉光谷 ΔT 2.1℃</span>
              </div>
            </div>
          </CardContent>
        </Card>

        {/* 4. 经核验累计节能 M&V */}
        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-xs font-medium text-muted-foreground">核验累计节能量 (M&V)</CardTitle>
            <DollarSign className="size-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="flex items-baseline gap-2">
              <span className="text-2xl font-bold tracking-tight text-foreground tabular-nums">2,470</span>
              <span className="text-xs text-muted-foreground">MWh / ¥197.6万</span>
            </div>
            <div className="mt-2 space-y-1 text-xs border-t pt-2">
              <Progress value={82.3} className="h-1.5 bg-muted" />
              <div className="flex items-center justify-between text-[11px] text-muted-foreground pt-0.5">
                <span>年度目标达成率</span>
                <span className="font-medium text-emerald-600 tabular-nums">82.3% (目标 3,000 MWh)</span>
              </div>
            </div>
          </CardContent>
        </Card>
      </div>

      {/* 集中重大风险运营督办卡 (Industrial Incident Alert) */}
      {CRITICAL_PORTFOLIO_RISKS.length > 0 && (
        <Card className="shadow-xs border-destructive/30 bg-destructive/5">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-3">
            <div className="flex items-center gap-2">
              <ShieldAlert className="size-4 text-destructive" />
              <CardTitle className="text-sm font-semibold text-destructive">
                集团重点运行与安全风险 ({CRITICAL_PORTFOLIO_RISKS.length} 项待跟进)
              </CardTitle>
            </div>
            <span className="text-xs text-muted-foreground">
              实时保护阈值判定
            </span>
          </CardHeader>
          <CardContent className="pt-0">
            <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
              {CRITICAL_PORTFOLIO_RISKS.map((risk) => (
                <div
                  key={risk.id}
                  className="rounded-md border border-destructive/20 bg-background/95 p-3.5 text-xs space-y-2 shadow-xs"
                >
                  <div className="flex items-center justify-between">
                    <div className="flex items-center gap-2">
                      <Badge variant="outline" className="font-normal text-[10px] text-destructive border-destructive/30">
                        {risk.level}
                      </Badge>
                      <span className="font-semibold text-foreground">{risk.site}</span>
                    </div>
                    <span className="text-muted-foreground font-mono text-[11px]">{risk.time}</span>
                  </div>
                  <div className="font-medium text-foreground">{risk.title}</div>
                  <div className="text-muted-foreground leading-relaxed text-[11px]">{risk.impact}</div>
                  <div className="pt-2 border-t flex items-center justify-between text-[11px]">
                    <span className="text-destructive font-medium">行动建议: {risk.action}</span>
                    <Button variant="outline" size="sm" asChild className="h-6 text-[11px] px-2 border-destructive/30 text-destructive hover:bg-destructive/10">
                      <a href={`/sites/${risk.siteId}/work-orders`}>
                        督办工单
                        <ChevronRight className="h-3 w-3 ml-0.5" />
                      </a>
                    </Button>
                  </div>
                </div>
              ))}
            </div>
          </CardContent>
        </Card>
      )}

      {/* 核心工作区 Tabs */}
      <Tabs defaultValue="sites" className="space-y-4">
        <div className="flex flex-col gap-3 md:flex-row md:items-center md:justify-between">
          <TabsList className="bg-muted/60 h-9 p-1">
            <TabsTrigger value="sites" className="text-xs">
              <Building2 className="h-3.5 w-3.5 mr-1.5" />
              站点矩阵与健康度 ({PORTFOLIO_SITES.length})
            </TabsTrigger>
            <TabsTrigger value="trend" className="text-xs">
              <Activity className="h-3.5 w-3.5 mr-1.5" />
              集团能耗与基线趋势
            </TabsTrigger>
            <TabsTrigger value="opportunities" className="text-xs">
              <Sparkles className="h-3.5 w-3.5 mr-1.5" />
              跨站点节能机会池 ({PORTFOLIO_OPPORTUNITIES.length})
            </TabsTrigger>
          </TabsList>

          {/* Faceted Filters Toolbar */}
          <div className="flex items-center gap-2">
            <div className="relative w-48 sm:w-60">
              <Search className="absolute left-2.5 top-2.5 h-3.5 w-3.5 text-muted-foreground" />
              <Input
                placeholder="搜索站点名称 / 编号 / 区域..."
                value={searchQuery}
                onChange={(e) => {
                  setSearchQuery(e.target.value);
                  siteTable.setPageIndex(0);
                }}
                className="h-8 pl-8 text-xs bg-background"
              />
            </div>

            <select
              value={selectedRegion}
              onChange={(e) => {
                setSelectedRegion(e.target.value);
                siteTable.setPageIndex(0);
              }}
              className="h-8 rounded-md border border-input bg-background px-2.5 text-xs text-foreground focus:outline-none focus:ring-1 focus:ring-ring"
            >
              <option value="all">所有大区</option>
              <option value="华东大区">华东大区</option>
              <option value="华北大区">华北大区</option>
              <option value="大湾区">大湾区</option>
              <option value="华中大区">华中大区</option>
              <option value="西南大区">西南大区</option>
            </select>

            <select
              value={selectedStatus}
              onChange={(e) => {
                setSelectedStatus(e.target.value);
                siteTable.setPageIndex(0);
              }}
              className="h-8 rounded-md border border-input bg-background px-2.5 text-xs text-foreground focus:outline-none focus:ring-1 focus:ring-ring"
            >
              <option value="all">全部运行状态</option>
              <option value="normal">正常运行</option>
              <option value="attention">能效偏离</option>
              <option value="critical">关键风险</option>
            </select>
          </div>
        </div>

        {/* Tab 1: 站点矩阵与状态 */}
        <TabsContent value="sites" className="space-y-4 mt-0">
          <Card className="shadow-xs">
            <div className="p-4 border-b flex flex-col sm:flex-row sm:items-center sm:justify-between gap-3 bg-muted/10">
              <div>
                <h3 className="text-sm font-semibold text-foreground">各站点实时运行状态</h3>
                <p className="text-xs text-muted-foreground">
                  监控各站点实时能效、当月用电量与运行健康度
                </p>
              </div>
              <div className="flex flex-wrap items-center gap-2">
                <DataTableViewPills
                  options={siteStatusPills}
                  value={selectedStatus}
                  onValueChange={(value) => {
                    setSelectedStatus(value);
                    siteTable.setPageIndex(0);
                  }}
                />
                <DataTableViewOptions table={siteTable} />
              </div>
            </div>

            <DataTable
              table={siteTable}
              className="gap-0"
              tableAriaLabel="企业站点运行矩阵"
              empty="未找到匹配的站点记录"
              getHeaderRowProps={() => ({ className: 'bg-muted/30 text-xs' })}
              getHeaderCellProps={(header) => ({
                className:
                  header.id === 'select' ? 'w-[40px]' :
                  header.id === 'site' ? 'w-[200px]' :
                  header.id === 'regionArea' ? 'w-[120px]' :
                  header.id === 'hvacType' ? 'w-[150px]' :
                  header.id === 'cop' ? 'w-[110px] text-right' :
                  header.id === 'consumption' ? 'w-[100px] text-right' :
                  header.id === 'eui' ? 'w-[140px] text-right' :
                  header.id === 'peerRank' ? 'w-[100px] text-center' :
                  header.id === 'completeness' ? 'w-[90px] text-center' :
                  header.id === 'status' ? 'w-[120px] text-center' :
                  header.id === 'action' ? 'w-[140px] text-right' :
                  undefined,
              })}
              getRowProps={(row) => ({
                className: 'cursor-pointer text-xs hover:bg-muted/40',
                onClick: () => setInspectedSite(row.original),
              })}
              getCellProps={(cell) => ({
                className:
                  ['cop', 'consumption', 'eui', 'action'].includes(cell.column.id) ? 'text-right' :
                  ['peerRank', 'completeness', 'status'].includes(cell.column.id) ? 'text-center' :
                  undefined,
              })}
              footer={(
                <DataTablePagination
                  table={siteTable}
                  totalRows={filteredSites.length}
                  
                  
                  
                  
                  
                />
              )}
            />
          </Card>
        </TabsContent>

        {/* Tab 2: 集团用能趋势与基线 */}
        <TabsContent value="trend" className="space-y-4 mt-0">
          <Card className="shadow-xs">
            <div className="p-4 border-b flex flex-col sm:flex-row sm:items-center sm:justify-between gap-2 bg-muted/10">
              <div>
                <h3 className="text-sm font-semibold text-foreground">
                  近 6 个月用电量与基线对比
                </h3>
                <p className="text-xs text-muted-foreground">
                  对比历史基线用电，统计各月核验节电量
                </p>
              </div>
              <div className="flex items-center gap-4 text-xs">
                <div className="flex items-center gap-1.5">
                  <span className="h-2.5 w-2.5 rounded bg-blue-600" />
                  <span>实际用电量</span>
                </div>
                <div className="flex items-center gap-1.5">
                  <span className="h-2.5 w-2.5 rounded bg-amber-500" />
                  <span>调整基线能耗</span>
                </div>
                <div className="flex items-center gap-1.5">
                  <span className="h-2.5 w-2.5 rounded bg-emerald-600" />
                  <span>核验净节能量</span>
                </div>
              </div>
            </div>

            <div className="p-4">
              <div className="h-[320px] w-full">
                <ResponsiveContainer width="100%" height="100%">
                  <BarChart data={MONTHLY_TREND_DATA} margin={{ top: 10, right: 10, left: 10, bottom: 0 }}>
                    <CartesianGrid strokeDasharray="3 3" vertical={false} stroke="#e5e7eb" />
                    <XAxis dataKey="month" tick={{ fontSize: 11 }} />
                    <YAxis tick={{ fontSize: 11 }} unit=" MWh" />
                    <Tooltip
                      formatter={(val) => [`${Number(val ?? 0).toLocaleString()} MWh`, '']}
                      labelFormatter={(label) => `统计月份: ${label}`}
                      contentStyle={{ fontSize: '12px', borderRadius: '6px' }}
                    />
                    <Legend wrapperStyle={{ fontSize: '12px' }} />
                    <Bar dataKey="baselineMWh" name="调整基线能耗" fill="#f59e0b" radius={[4, 4, 0, 0]} />
                    <Bar dataKey="actualMWh" name="实际用电量" fill="#2563eb" radius={[4, 4, 0, 0]} />
                    <Bar dataKey="savingsMWh" name="核验节电量" fill="#10b981" radius={[4, 4, 0, 0]} />
                  </BarChart>
                </ResponsiveContainer>
              </div>
            </div>
          </Card>
        </TabsContent>

        {/* Tab 3: 跨站点高价值机会池 */}
        <TabsContent value="opportunities" className="space-y-4 mt-0">
          <Card className="shadow-xs">
            <div className="p-4 border-b flex flex-col sm:flex-row sm:items-center sm:justify-between gap-2 bg-muted/10">
              <div>
                <h3 className="text-sm font-semibold text-foreground">重点节能与优化建议</h3>
                <p className="text-xs text-muted-foreground">
                  汇总各站点已确认的运行优化措施及预期收益
                </p>
              </div>
              <Badge variant="outline" className="font-normal text-xs gap-1.5">
                <span className="size-1.5 rounded-full bg-emerald-500" />
                总预期节能量：645 MWh / ¥51.6万每年
              </Badge>
            </div>

            <DataTable
              table={opportunityTable}
              className="gap-0"
              tableAriaLabel="跨站节能机会清单"
              getHeaderRowProps={() => ({ className: 'bg-muted/30 text-xs' })}
              getHeaderCellProps={(header) => ({
                className:
                  header.id === 'select' ? 'w-[40px]' :
                  header.id === 'id' ? 'w-[120px]' :
                  header.id === 'site' ? 'w-[160px]' :
                  header.id === 'category' ? 'w-[140px]' :
                  header.id === 'saving' ? 'w-[110px] text-right' :
                  header.id === 'cost' ? 'w-[120px] text-right' :
                  header.id === 'payback' ? 'w-[100px] text-center' :
                  header.id === 'status' ? 'w-[120px] text-center' :
                  header.id === 'actions' ? 'w-[80px] text-right' :
                  undefined,
              })}
              getRowProps={() => ({ className: 'text-xs hover:bg-muted/40' })}
              getCellProps={(cell) => ({
                className:
                  cell.column.id === 'saving' || cell.column.id === 'cost' || cell.column.id === 'actions'
                    ? 'text-right'
                    : cell.column.id === 'payback' || cell.column.id === 'status'
                      ? 'text-center'
                      : undefined,
              })}
              footer={(
                <DataTablePagination
                  table={opportunityTable}
                  totalRows={PORTFOLIO_OPPORTUNITIES.length}
                />
              )}
            />
          </Card>
        </TabsContent>
      </Tabs>

      {/* 站点运营快速画板 (Inspection Sheet) */}
      <Sheet open={Boolean(inspectedSite)} onOpenChange={(open) => !open && setInspectedSite(null)}>
        <SheetContent className="sm:max-w-xl">
          <SheetHeader>
            <div className="flex items-center gap-2">
              <span className="font-mono text-xs font-semibold text-muted-foreground">
                {inspectedSite?.code}
              </span>
              <span className="rounded bg-muted px-2 py-0.5 text-[10px] font-medium text-muted-foreground">
                {inspectedSite?.region}
              </span>
            </div>
            <SheetTitle className="text-base font-bold text-foreground mt-1">
              {inspectedSite?.name}
            </SheetTitle>
            <SheetDescription className="text-xs">
              建筑面积: {((inspectedSite?.buildingArea ?? 0) / 10000).toFixed(1)} 万 m² · 冷站架构: {inspectedSite?.hvacType}
            </SheetDescription>
          </SheetHeader>

          {inspectedSite && (
            <SheetBody className="space-y-5 pt-4 text-xs">
              {/* Telemetry Metrics Grid */}
              <div className="grid grid-cols-2 gap-3 rounded-lg border border-border/80 bg-muted/10 p-3.5">
                <div>
                  <div className="text-muted-foreground text-[11px]">即时综合 COP / 设计</div>
                  <div className="mt-1 font-mono text-lg font-bold text-foreground">
                    <span className={inspectedSite.currentCop < 4.0 ? 'text-destructive' : 'text-emerald-600'}>
                      {inspectedSite.currentCop.toFixed(2)}
                    </span>
                    <span className="text-xs text-muted-foreground font-normal"> / {inspectedSite.designCop.toFixed(1)}</span>
                  </div>
                </div>
                <div>
                  <div className="text-muted-foreground text-[11px]">当月用电量</div>
                  <div className="mt-1 font-mono text-lg font-bold text-foreground">
                    {inspectedSite.monthlyConsumption.toFixed(1)}{' '}
                    <span className="text-xs text-muted-foreground font-normal">MWh</span>
                  </div>
                </div>
                <div>
                  <div className="text-muted-foreground text-[11px]">实测 EUI / 气象归一化</div>
                  <div className="mt-1 font-mono text-sm font-semibold text-foreground">
                    {inspectedSite.eui.toFixed(1)} / {inspectedSite.normalizedEui.toFixed(1)}{' '}
                    <span className="text-[10px] text-muted-foreground font-normal">kWh/m²·a</span>
                  </div>
                </div>
                <div>
                  <div className="text-muted-foreground text-[11px]">遥测完整率 / 对标</div>
                  <div className="mt-1 font-mono text-sm font-semibold text-foreground">
                    {inspectedSite.dataCompleteness}% ({inspectedSite.peerRank})
                  </div>
                </div>
              </div>

              {/* Status & Issue */}
              {inspectedSite.keyIssue && (
                <div className="rounded-lg border border-destructive/30 bg-destructive/5 p-3.5 space-y-1.5">
                  <div className="flex items-center gap-1.5 text-xs font-semibold text-destructive">
                    <AlertTriangle className="h-3.5 w-3.5" />
                    当前重点问题
                  </div>
                  <p className="text-foreground text-[11px] leading-relaxed">
                    {inspectedSite.keyIssue}
                  </p>
                </div>
              )}

              {/* Lead Engineer Contact */}
              <div className="rounded-lg border border-border/80 bg-muted/10 p-3.5 space-y-2">
                <div className="flex items-center justify-between">
                  <div className="flex items-center gap-1.5 text-xs font-semibold text-foreground">
                    <PhoneCall className="h-3.5 w-3.5 text-primary" />
                    现场值班工程师
                  </div>
                  <span className="font-mono text-foreground font-medium text-[11px]">
                    {inspectedSite.leadEngineer}
                  </span>
                </div>
                <p className="text-[11px] text-muted-foreground">
                  如需进一步排查，可直接联络站内值班工程师或进入站点查看详情。
                </p>
              </div>

              {/* Action Buttons */}
              <div className="pt-3 border-t flex items-center justify-end gap-2">
                <Button variant="outline" size="sm" onClick={() => setInspectedSite(null)}>
                  关闭
                </Button>
                <Button size="sm" asChild>
                  <a href={`/sites/${inspectedSite.id}/overview`} className="flex items-center gap-1">
                    进入该站点
                    <ArrowUpRight className="h-3.5 w-3.5" />
                  </a>
                </Button>
              </div>
            </SheetBody>
          )}
        </SheetContent>
      </Sheet>
    </Main>
  );
}
