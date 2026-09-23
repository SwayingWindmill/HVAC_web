import { useState, useMemo } from 'react';
import type { ColumnDef } from '@tanstack/react-table';
import {
  ClipboardCheck,
  Award,
  DollarSign,
  TrendingDown,
  Layers,
  FileCheck2,
  CheckCircle2,
  Search,
  ShieldCheck,
  FileText,
  ChevronRight,
  AlertTriangle,
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

interface ManagementReviewWorkspaceProps {
  readonly siteId: string;
}

interface SeuAuditItem {
  readonly name: string;
  readonly code: string;
  readonly category: string;
  readonly sharePercent: number;
  readonly trendVsBaseline: string;
  readonly status: 'NORMAL' | 'ATTENTION';
  readonly finding: string;
  readonly color: string;
}

const SEU_AUDIT_DATA: readonly SeuAuditItem[] = [
  {
    name: '离心机组群 (CH-01 ~ CH-03)',
    code: 'SEU-01',
    category: '重大用能设备 (SEU-01)',
    sharePercent: 62.4,
    trendVsBaseline: '-9.2%',
    status: 'NORMAL',
    finding: '实施冷凝温度自适应重置策略后，平均 COP 由 4.15 提升至 4.72，能耗显著收窄',
    color: 'bg-blue-600',
  },
  {
    name: '冷冻水循环水泵群 (CHWP-01 ~ CHWP-04)',
    code: 'SEU-02',
    category: '重大用能设备 (SEU-02)',
    sharePercent: 18.5,
    trendVsBaseline: '+4.8%',
    status: 'ATTENTION',
    finding: '因 2 号板换旁通阀内漏导致系统大温差偏低，水泵处于高频运转，已派发检修工单',
    color: 'bg-amber-500',
  },
  {
    name: '冷却塔风机群 (CT-01 ~ CT-04)',
    code: 'SEU-03',
    category: '重大用能设备 (SEU-03)',
    sharePercent: 11.2,
    trendVsBaseline: '-3.1%',
    status: 'NORMAL',
    finding: '风机变频按湿球逼近度运行，过渡季节电效果达标',
    color: 'bg-cyan-500',
  },
  {
    name: '冷站公共照明及附属设施',
    code: 'SEU-04',
    category: '辅助用能设施 (SEU-04)',
    sharePercent: 7.9,
    trendVsBaseline: '+0.4%',
    status: 'NORMAL',
    finding: '定时感应开关运行稳定，无常明灯现象',
    color: 'bg-slate-400',
  },
];

interface CapaAction {
  readonly id: string;
  readonly source: string;
  readonly issue: string;
  readonly rootCause: string;
  readonly action: string;
  readonly owner: string;
  readonly dueDate: string;
  readonly status: 'VERIFIED' | 'IN_PROGRESS' | 'PENDING_VERIFY';
  readonly statusLabel: string;
  readonly verificationResult: string;
  readonly certNumber?: string;
}

const CAPA_ACTIONS: readonly CapaAction[] = [
  {
    id: 'CAPA-2026-Q3-01',
    source: '2026-Q2 往期评审决议',
    issue: '冷却水进水温度传感器未纳入强制周期检定，存在 0.8℃ 测量漂移风险',
    rootCause: '既往设备维护台账未明确区分计量级与一般监控级传感器校准规程',
    action: '完成第三方计量院溯源校准并建立半年周期检定台账',
    owner: '张志远 (计量工程师)',
    dueDate: '2026-08-31',
    status: 'VERIFIED',
    statusLabel: '已验收完成',
    verificationResult: '经第三方校准证书 (NO.JL202608-44) 核验，示值误差降至 ±0.05℃',
    certNumber: 'NO.JL202608-44',
  },
  {
    id: 'CAPA-2026-Q3-02',
    source: 'ISO 50001 内审不符合项',
    issue: '冷冻水二次泵夜间小负荷工况下未执行最低单泵停机保护，存在轻载低效大马拉小车',
    rootCause: '定频备用泵组切入联动逻辑与夜间极低流量阈值设定存在冲突死区',
    action: '优化夜间低负荷停机轮换策略并在离线环境中完成调试',
    owner: '李建军 (控制工程师)',
    dueDate: '2026-09-25',
    status: 'IN_PROGRESS',
    statusLabel: '推进中 (90%)',
    verificationResult: '已完成离线控制逻辑仿真测试，计划 9 月底现场带载核验',
    certNumber: 'SIM-202609-BETA',
  },
  {
    id: 'CAPA-2026-Q3-03',
    source: '重大能耗偏离事件',
    issue: '武汉中心站旁通电动阀内漏致大温差严重衰减 (ΔT = 2.1℃)',
    rootCause: '电动调节阀执行器机械限位松脱，关断力矩不足导致微量渗漏',
    action: '下发抢修工单 WO-202609-082，更换耐磨氟橡胶密封件并完成 MBCx 验证',
    owner: '王安全 (运维班长)',
    dueDate: '2026-09-20',
    status: 'PENDING_VERIFY',
    statusLabel: '现场施工完毕待验证',
    verificationResult: '已完成密封包更换，正在等待冷站带载 30 分钟进行物理温差复核',
    certNumber: 'WO-202609-082',
  },
];

interface InvestmentDecision {
  readonly id: string;
  readonly project: string;
  readonly budget: string;
  readonly expectedSavings: string;
  readonly payback: string;
  readonly priority: string;
  readonly decision: string;
  readonly status: 'APPROVED' | 'CONDITIONAL' | 'RECOMMENDED';
}

const INVESTMENT_DECISIONS: readonly InvestmentDecision[] = [
  {
    id: 'INV-2026-01',
    project: '1 号冷冻机房水泵低阻水力平衡阀改造与水力管网拓扑平衡调适',
    budget: '¥18.0 万元',
    expectedSavings: '¥8.5 万元/年 (106 MWh)',
    payback: '2.1 年',
    priority: '高优先级 (优先批复)',
    decision: '管理评审委员会一致批准，列入 Q4 资本性支出执行计划',
    status: 'APPROVED',
  },
  {
    id: 'INV-2026-02',
    project: '冷却水系统自清洗反冲洗过滤器与智能排污加药装置加装',
    budget: '¥12.0 万元',
    expectedSavings: '¥5.2 万元/年 (减少污垢热阻提效 2.5%)',
    payback: '2.3 年',
    priority: '中优先级',
    decision: '原则同意立项，要求补充水质实时在线电导联动监测方案后签署合同',
    status: 'CONDITIONAL',
  },
  {
    id: 'INV-2026-03',
    project: '楼宇屋顶 200kW 分布式光伏与 100kWh 梯次利用储能微电网示范',
    budget: '¥48.0 万元',
    expectedSavings: '¥14.0 万元/年 (削峰填谷套利)',
    payback: '3.4 年',
    priority: '战略示范',
    decision: '建议联合申报园区绿色低碳转型专项补贴资金后启动深化设计',
    status: 'RECOMMENDED',
  },
];

export function ManagementReviewWorkspace({ siteId }: ManagementReviewWorkspaceProps) {
  const [reviewSigned, setReviewSigned] = useState(false);
  const [selectedCapa, setSelectedCapa] = useState<CapaAction | null>(null);
  const [capaSearch, setCapaSearch] = useState('');
  const [capaStatusFilter, setCapaStatusFilter] = useState('ALL');
  const capaStatusPills: readonly DataTableViewPillOption[] = useMemo(() => [
    { key: 'ALL', label: '全部措施', count: CAPA_ACTIONS.length },
    { key: 'VERIFIED', label: '已验收完成', count: CAPA_ACTIONS.filter((a) => a.status === 'VERIFIED').length },
    { key: 'IN_PROGRESS', label: '推进中', count: CAPA_ACTIONS.filter((a) => a.status === 'IN_PROGRESS').length },
    { key: 'PENDING_VERIFY', label: '待现场验证', count: CAPA_ACTIONS.filter((a) => a.status === 'PENDING_VERIFY').length },
  ], []);

  const filteredCapas = useMemo(() => {
    return CAPA_ACTIONS.filter((item) => {
      if (capaStatusFilter !== 'ALL' && item.status !== capaStatusFilter) return false;
      if (!capaSearch.trim()) return true;
      const q = capaSearch.toLowerCase();
      return (
        item.id.toLowerCase().includes(q) ||
        item.issue.toLowerCase().includes(q) ||
        item.owner.toLowerCase().includes(q) ||
        item.source.toLowerCase().includes(q)
      );
    });
  }, [capaStatusFilter, capaSearch]);

  const capaColumns = useMemo<Array<ColumnDef<DataTableFeatures, CapaAction>>>(() => [
    {
      id: 'select',
      header: ({ table }) => (
        <Checkbox
          checked={table.getIsAllPageRowsSelected() ? true : table.getIsSomePageRowsSelected() ? 'indeterminate' : false}
          onCheckedChange={(checked) => table.toggleAllPageRowsSelected(Boolean(checked))}
          aria-label="选择全部措施"
        />
      ),
      cell: ({ row }) => (
        <Checkbox
          checked={row.getIsSelected()}
          onCheckedChange={(checked) => row.toggleSelected(Boolean(checked))}
          aria-label={`选择 ${row.original.id}`}
          onClick={(event) => event.stopPropagation()}
        />
      ),
      enableSorting: false,
      enableHiding: false,
    },
    { id: 'id', header: '措施编号', cell: ({ row }) => <span className="font-mono font-semibold text-foreground">{row.original.id}</span> },
    { id: 'source', header: '问题来源', cell: ({ row }) => <span className="rounded bg-muted px-2 py-0.5 text-[11px] font-medium text-muted-foreground">{row.original.source}</span> },
    { id: 'issue', header: '不符合事实与风险陈述', cell: ({ row }) => <span className="font-medium leading-relaxed text-foreground">{row.original.issue}</span> },
    { id: 'action', header: '纠正与预防行动方案', cell: ({ row }) => <span className="leading-relaxed text-muted-foreground">{row.original.action}</span> },
    {
      id: 'owner',
      header: '责任人 / 期限',
      cell: ({ row }) => <div className="text-[11px] text-muted-foreground"><div className="font-medium text-foreground">{row.original.owner}</div><div className="font-mono text-[10px]">{row.original.dueDate}</div></div>,
    },
    {
      id: 'status',
      header: '状态',
      cell: ({ row }) => (
        <StatusPillBadge
          label={row.original.statusLabel}
          tone={row.original.status === 'VERIFIED' ? 'success' : row.original.status === 'IN_PROGRESS' ? 'info' : 'warning'}
          pulse={row.original.status === 'PENDING_VERIFY'}
        />
      ),
    },
    {
      id: 'details',
      header: '详情',
      cell: ({ row }) => (
        <Button variant="ghost" size="sm" className="h-7 px-2 text-xs" onClick={(event) => { event.stopPropagation(); setSelectedCapa(row.original); }}>
          审查<ChevronRight className="ml-0.5 h-3 w-3" />
        </Button>
      ),
      enableSorting: false,
    },
  ], []);

  const capaTable = useDataTable({
    key: 'surface-25-management-review-capa',
    data: [...filteredCapas],
    columns: capaColumns,
    pageSize: 10,
    getRowId: (row) => row.id,
  });

  const seuColumns = useMemo<Array<ColumnDef<DataTableFeatures, SeuAuditItem>>>(() => [
    { id: 'name', header: '用能系统与设备群', cell: ({ row }) => <div><div className="font-semibold text-foreground">{row.original.name}</div><div className="font-mono text-[10px] text-muted-foreground">{row.original.code}</div></div> },
    { id: 'category', header: '体系类别', cell: ({ row }) => <span className="rounded bg-muted px-2 py-0.5 text-[11px] font-medium text-muted-foreground">{row.original.category}</span> },
    { id: 'share', header: '能耗占比 (%)', cell: ({ row }) => <span className="font-mono font-medium tabular-nums">{row.original.sharePercent}%</span> },
    { id: 'trend', header: '较基准偏差', cell: ({ row }) => <span className={row.original.trendVsBaseline.startsWith('-') ? 'font-mono font-semibold tabular-nums text-emerald-600' : 'font-mono font-semibold tabular-nums text-destructive'}>{row.original.trendVsBaseline}</span> },
    { id: 'status', header: '工况评价', cell: ({ row }) => <StatusPillBadge label={row.original.status === 'NORMAL' ? '受控达标' : '偏差需关注'} tone={row.original.status === 'NORMAL' ? 'success' : 'warning'} /> },
    { id: 'finding', header: '技术审查结论与高管指示', cell: ({ row }) => <span className="text-[11px] leading-relaxed text-muted-foreground">{row.original.finding}</span> },
  ], []);

  const seuTable = useDataTable({
    key: 'management-review-seu-audit',
    data: [...SEU_AUDIT_DATA],
    columns: seuColumns,
    paginate: false,
    getRowId: (row) => row.code,
  });

  const investmentColumns = useMemo<Array<ColumnDef<DataTableFeatures, InvestmentDecision>>>(() => [
    { id: 'project', header: '拟立项技改工程方案', cell: ({ row }) => <div><div className="font-semibold text-foreground">{row.original.project}</div><div className="font-mono text-[10px] text-muted-foreground">{row.original.id}</div></div> },
    { id: 'budget', header: '投资预算', cell: ({ row }) => <span className="font-mono font-medium text-foreground tabular-nums">{row.original.budget}</span> },
    { id: 'savings', header: '预期节能收益', cell: ({ row }) => <span className="font-mono font-medium text-emerald-600 tabular-nums">{row.original.expectedSavings}</span> },
    { id: 'payback', header: '静态回收期', cell: ({ row }) => <span className="font-mono font-medium tabular-nums">{row.original.payback}</span> },
    { id: 'priority', header: '实施优先级', cell: ({ row }) => <span className="rounded bg-muted px-2 py-0.5 text-[11px] font-medium text-muted-foreground">{row.original.priority}</span> },
    { id: 'decision', header: '管理层终审决策决议', cell: ({ row }) => <span className="text-[11px] font-medium leading-relaxed text-foreground">{row.original.decision}</span> },
    {
      id: 'status',
      header: '立项状态',
      cell: ({ row }) => row.original.status === 'APPROVED'
        ? <StatusPillBadge label="已批准" tone="success" />
        : row.original.status === 'CONDITIONAL'
          ? <StatusPillBadge label="附条件同意" tone="info" />
          : <StatusPillBadge label="建议申报" tone="neutral" />,
    },
  ], []);

  const investmentTable = useDataTable({
    key: 'management-review-investments',
    data: [...INVESTMENT_DECISIONS],
    columns: investmentColumns,
    paginate: false,
    getRowId: (row) => row.id,
  });

  return (
    <Main className="space-y-6">
      {/* 顶部标题与管理评审周期 */}
      <div className="flex flex-col gap-3 md:flex-row md:items-center md:justify-between border-b pb-4">
        <div>
          <div className="flex items-center gap-2">
            <h1 className="text-2xl font-bold tracking-tight text-foreground">管理评审</h1>
            <Badge variant="outline" className="border-border text-foreground/80 text-xs">
              能源管理评审
            </Badge>
            <span className="rounded bg-muted px-2 py-0.5 font-mono text-xs text-muted-foreground">
              2026 年度 Q3 评审周期
            </span>
          </div>
          <p className="text-sm text-muted-foreground mt-1">
            复盘本期能效基线偏差，跟进整改措施落实，审议下阶段技改立项
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" asChild>
            <a href={`/sites/${siteId}/energy-review`} className="flex items-center gap-1.5">
              <Layers className="h-4 w-4" />
              查看能效基准
            </a>
          </Button>
          {!reviewSigned ? (
            <Button size="sm" className="flex items-center gap-1.5" onClick={() => setReviewSigned(true)}>
              <FileCheck2 className="h-4 w-4" />
              签署本期评审决议
            </Button>
          ) : (
            <div className="flex items-center gap-1.5 text-xs font-medium text-emerald-600 bg-emerald-500/10 border border-emerald-500/30 rounded-md px-3 py-1.5">
              <CheckCircle2 className="h-4 w-4 text-emerald-600" />
              本期管理评审决议已签署归档
            </div>
          )}
        </div>
      </div>

      {/* ISO 50001 Executive EnMS Review Ribbon (Standard 4 Cards) */}
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
        {/* 1. 能效基线偏离 */}
        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">能效指标基线偏离</CardTitle>
            <TrendingDown className="h-4 w-4 text-muted-foreground" />
          </CardHeader>
          <CardContent className="space-y-1">
            <div className="text-2xl font-bold tracking-tight text-foreground tabular-nums">
              -8.4%
            </div>
            <p className="text-xs text-muted-foreground">
              优于调整基线 · 季度核验节电 320 MWh
            </p>
          </CardContent>
        </Card>

        {/* 2. CAPA 完成率 */}
        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">CAPA 整改完成率</CardTitle>
            <CheckCircle2 className="h-4 w-4 text-muted-foreground" />
          </CardHeader>
          <CardContent className="space-y-2">
            <div className="text-2xl font-bold tracking-tight text-foreground tabular-nums">
              91.7%
            </div>
            <Progress value={91.7} className="h-1.5" />
            <p className="text-xs text-muted-foreground">
              11/12 项已验收 · 1 项在途跟踪
            </p>
          </CardContent>
        </Card>

        {/* 3. ISO 50001 体系有效性 */}
        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">体系运行评价</CardTitle>
            <Award className="h-4 w-4 text-muted-foreground" />
          </CardHeader>
          <CardContent className="space-y-1">
            <div className="text-2xl font-bold tracking-tight text-foreground">
              良好
            </div>
            <p className="text-xs text-muted-foreground">
              无重大不符合 · 合规义务 100% 达标
            </p>
          </CardContent>
        </Card>

        {/* 4. 拟批资本性支出 */}
        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">本期批复技改投资 (Q4)</CardTitle>
            <DollarSign className="h-4 w-4 text-muted-foreground" />
          </CardHeader>
          <CardContent className="space-y-1">
            <div className="text-2xl font-bold tracking-tight text-foreground tabular-nums">
              ¥30.0 <span className="text-xs font-normal text-muted-foreground">万元</span>
            </div>
            <p className="text-xs text-muted-foreground">
              2 项获批 · 预计年化节约 13.7 万元
            </p>
          </CardContent>
        </Card>
      </div>

      {/* 核心工作区 Tabs */}
      <Tabs defaultValue="capa" className="space-y-4">
        <TabsList className="bg-muted/60 h-9 p-1">
          <TabsTrigger value="capa" className="text-xs">
            <ClipboardCheck className="h-3.5 w-3.5 mr-1.5" />
            整改与纠正措施台账 ({CAPA_ACTIONS.length})
          </TabsTrigger>
          <TabsTrigger value="seu" className="text-xs">
            <Layers className="h-3.5 w-3.5 mr-1.5" />
            核心用能设备 (SEU) 能耗分析
          </TabsTrigger>
          <TabsTrigger value="investment" className="text-xs">
            <DollarSign className="h-3.5 w-3.5 mr-1.5" />
            技改投资决策 ({INVESTMENT_DECISIONS.length})
          </TabsTrigger>
        </TabsList>

        {/* Tab 1: CAPA 纠正预防措施台账 */}
        <TabsContent value="capa" className="space-y-4 mt-0">
          <div className="rounded-lg border border-border/80 bg-card overflow-hidden shadow-sm">
            {/* Filter Toolbar */}
            <div className="p-4 border-b flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between bg-muted/10">
              <div className="space-y-0.5">
                <h3 className="text-sm font-semibold text-foreground">
                  整改与纠正预防措施 (CAPA) 台账
                </h3>
                <p className="text-xs text-muted-foreground">
                  记录审核不符合项、能耗偏离与往期评审遗留事项的整改进度
                </p>
              </div>

              <div className="flex flex-wrap items-center gap-2">
                <div className="relative w-48 sm:w-56">
                  <Search className="absolute left-2.5 top-2.5 h-3.5 w-3.5 text-muted-foreground" />
                  <Input
                    placeholder="搜索编号、问题或责任人..."
                    value={capaSearch}
                    onChange={(e) => {
                      setCapaSearch(e.target.value);
                      capaTable.setPageIndex(0);
                    }}
                    className="h-8 pl-8 text-xs bg-background"
                  />
                </div>

                <DataTableViewPills
                  options={capaStatusPills}
                  value={capaStatusFilter}
                  onValueChange={(val) => {
                    setCapaStatusFilter(val);
                    capaTable.setPageIndex(0);
                  }}
                />

                <DataTableViewOptions table={capaTable} />
              </div>
            </div>

            <DataTable
              table={capaTable}
              className="gap-0"
              tableAriaLabel="CAPA 整改措施台账"
              empty="无匹配的 CAPA 整改措施记录"
              getHeaderRowProps={() => ({ className: 'bg-muted/30 text-xs' })}
              getHeaderCellProps={(header) => ({
                className:
                  header.id === 'select' ? 'w-[40px] px-3' :
                  header.id === 'id' || header.id === 'owner' ? 'w-[140px]' :
                  header.id === 'source' ? 'w-[150px]' :
                  header.id === 'issue' ? 'w-[280px]' :
                  header.id === 'status' ? 'w-[130px] text-center' :
                  header.id === 'details' ? 'w-[80px] text-right' :
                  undefined,
              })}
              getRowProps={(row) => ({
                className: 'cursor-pointer text-xs hover:bg-muted/40',
                onClick: () => setSelectedCapa(row.original),
              })}
              getCellProps={(cell) => ({
                className:
                  cell.column.id === 'select' ? 'px-3' :
                  cell.column.id === 'status' ? 'text-center' :
                  cell.column.id === 'details' ? 'text-right' :
                  undefined,
              })}
              footer={(
                <DataTablePagination
                  table={capaTable}
                  totalRows={filteredCapas.length}
                  
                  
                  
                  
                  
                />
              )}
            />
          </div>
        </TabsContent>

        {/* Tab 2: SEU 审计 */}
        <TabsContent value="seu" className="space-y-4 mt-0">
          {/* SEU Pareto Visual Distribution Bar */}
          <div className="rounded-lg border border-border/80 bg-card p-4 shadow-sm space-y-3">
            <div className="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-1">
              <div>
                <h3 className="text-sm font-semibold text-foreground">
                  核心用能设备 (SEU) 能耗结构
                </h3>
                <p className="text-xs text-muted-foreground">
                  展示主要用能系统的电耗分布与重点监控对象（占全站总用电 92.1%）
                </p>
              </div>
              <div className="flex items-center gap-3 text-xs">
                <span className="flex items-center gap-1 font-mono text-muted-foreground">
                  <span className="h-2 w-2 rounded-full bg-blue-600" /> 离心机 62.4%
                </span>
                <span className="flex items-center gap-1 font-mono text-muted-foreground">
                  <span className="h-2 w-2 rounded-full bg-amber-500" /> 水泵 18.5%
                </span>
                <span className="flex items-center gap-1 font-mono text-muted-foreground">
                  <span className="h-2 w-2 rounded-full bg-emerald-600" /> 冷却塔 11.2%
                </span>
                <span className="flex items-center gap-1 font-mono text-muted-foreground">
                  <span className="h-2 w-2 rounded-full bg-slate-400" /> 辅能 7.9%
                </span>
              </div>
            </div>

            {/* Segmented Color Bar */}
            <div className="h-3 w-full rounded-full overflow-hidden flex bg-muted">
              {SEU_AUDIT_DATA.map((seu) => (
                <div
                  key={seu.code}
                  className={`${seu.color} transition-all`}
                  style={{ width: `${seu.sharePercent}%` }}
                  title={`${seu.name}: ${seu.sharePercent}%`}
                />
              ))}
            </div>
          </div>

          <div className="rounded-lg border border-border/80 bg-card overflow-hidden shadow-sm">
            <div className="p-4 border-b bg-muted/10">
              <h4 className="text-sm font-semibold text-foreground">重点设备能耗偏差分析</h4>
              <p className="text-xs text-muted-foreground">
                对比季度基准用电，核查超耗原因与设备运行状况
              </p>
            </div>
            <DataTable
              table={seuTable}
              className="gap-0"
              tableAriaLabel="重点设备能耗偏差分析"
              getHeaderRowProps={() => ({ className: 'bg-muted/30 text-xs' })}
              getHeaderCellProps={(header) => ({
                className:
                  header.id === 'name' ? 'w-[220px]' :
                  header.id === 'category' ? 'w-[160px]' :
                  header.id === 'share' ? 'w-[100px] text-right' :
                  header.id === 'trend' ? 'w-[120px] text-right' :
                  header.id === 'status' ? 'w-[110px] text-center' :
                  undefined,
              })}
              getRowProps={() => ({ className: 'text-xs hover:bg-muted/40' })}
              getCellProps={(cell) => ({
                className:
                  cell.column.id === 'share' || cell.column.id === 'trend' ? 'text-right' :
                  cell.column.id === 'status' ? 'text-center' :
                  undefined,
              })}
            />
          </div>
        </TabsContent>

        {/* Tab 3: 资源配置与技改投资决策 */}
        <TabsContent value="investment" className="space-y-4 mt-0">
          <div className="rounded-lg border border-border/80 bg-card overflow-hidden shadow-sm">
            <div className="p-4 border-b bg-muted/10 flex flex-col sm:flex-row sm:items-center sm:justify-between gap-2">
              <div>
                <h3 className="text-sm font-semibold text-foreground">
                  节能技改 CAPEX 与资源分配决策
                </h3>
                <p className="text-xs text-muted-foreground">
                  高价值节能机会立项与投资回收期终审决议
                </p>
              </div>
              <div className="text-xs text-muted-foreground">
                合计批复预算：<span className="font-mono tabular-nums font-semibold text-foreground">¥30.0 万元</span> · 年化收益：<span className="font-mono tabular-nums font-semibold text-emerald-600">¥13.7 万元/年</span>
              </div>
            </div>

            <DataTable
              table={investmentTable}
              className="gap-0"
              tableAriaLabel="节能技改投资决策"
              getHeaderRowProps={() => ({ className: 'bg-muted/30 text-xs' })}
              getHeaderCellProps={(header) => ({
                className:
                  header.id === 'project' ? 'w-[300px]' :
                  header.id === 'budget' ? 'w-[100px] text-right' :
                  header.id === 'savings' ? 'w-[160px] text-right' :
                  header.id === 'payback' ? 'w-[90px] text-center' :
                  header.id === 'priority' ? 'w-[120px]' :
                  header.id === 'status' ? 'w-[110px] text-center' :
                  undefined,
              })}
              getRowProps={() => ({ className: 'text-xs hover:bg-muted/40' })}
              getCellProps={(cell) => ({
                className:
                  cell.column.id === 'budget' || cell.column.id === 'savings' ? 'text-right' :
                  cell.column.id === 'payback' || cell.column.id === 'status' ? 'text-center' :
                  undefined,
              })}
            />
          </div>
        </TabsContent>
      </Tabs>

      {/* Interactive Inspection Sheet for CAPA Details */}
      <Sheet open={Boolean(selectedCapa)} onOpenChange={(open) => !open && setSelectedCapa(null)}>
        <SheetContent className="sm:max-w-xl">
          <SheetHeader>
            <div className="flex items-center gap-2">
              <span className="font-mono text-xs font-semibold text-muted-foreground">
                {selectedCapa?.id}
              </span>
              <span className="rounded bg-muted px-2 py-0.5 text-[10px] font-medium text-muted-foreground">
                {selectedCapa?.source}
              </span>
            </div>
            <SheetTitle className="text-base font-bold text-foreground mt-1">
              {selectedCapa?.issue}
            </SheetTitle>
            <SheetDescription className="text-xs">
              责任人: {selectedCapa?.owner} · 计划完成期限: {selectedCapa?.dueDate}
            </SheetDescription>
          </SheetHeader>

          {selectedCapa && (
            <SheetBody className="space-y-5 pt-4 text-xs">
              {/* Root Cause Card */}
              <div className="rounded-lg border border-border/80 bg-muted/10 p-3.5 space-y-1.5">
                <div className="flex items-center gap-1.5 text-xs font-semibold text-foreground">
                  <AlertTriangle className="h-3.5 w-3.5 text-amber-600" />
                  根因分析 (5-Why Root Cause)
                </div>
                <p className="text-muted-foreground leading-relaxed">
                  {selectedCapa.rootCause}
                </p>
              </div>

              {/* Action Plan */}
              <div className="rounded-lg border border-border/80 bg-muted/10 p-3.5 space-y-1.5">
                <div className="flex items-center gap-1.5 text-xs font-semibold text-foreground">
                  <ShieldCheck className="h-3.5 w-3.5 text-primary" />
                  纠正与预防行动方案
                </div>
                <p className="text-foreground font-medium leading-relaxed">
                  {selectedCapa.action}
                </p>
              </div>

              {/* Verification & Certificate */}
              <div className="rounded-lg border border-border/80 bg-muted/10 p-3.5 space-y-2">
                <div className="flex items-center gap-1.5 text-xs font-semibold text-foreground">
                  <FileText className="h-3.5 w-3.5 text-emerald-600" />
                  效果核验证据与第三方检定
                </div>
                <p className="text-muted-foreground leading-relaxed">
                  {selectedCapa.verificationResult}
                </p>
                {selectedCapa.certNumber && (
                  <div className="flex items-center justify-between pt-2 border-t text-[11px]">
                    <span className="text-muted-foreground">校准证书/依据工单：</span>
                    <span className="font-mono font-semibold text-foreground">
                      {selectedCapa.certNumber}
                    </span>
                  </div>
                )}
              </div>

              {/* Signoff Audit Footer */}
              <div className="rounded-lg border border-border/80 p-3.5 bg-background space-y-2">
                <div className="text-xs font-semibold text-foreground">整改核验确认</div>
                <p className="text-[11px] text-muted-foreground">
                  整改完成后需由质安负责人核验证据并签署确认。
                </p>
                <div className="pt-2 flex items-center justify-end gap-2">
                  <Button variant="outline" size="sm" onClick={() => setSelectedCapa(null)}>
                    关闭
                  </Button>
                  <Button size="sm">
                    签署确认
                  </Button>
                </div>
              </div>
            </SheetBody>
          )}
        </SheetContent>
      </Sheet>
    </Main>
  );
}
