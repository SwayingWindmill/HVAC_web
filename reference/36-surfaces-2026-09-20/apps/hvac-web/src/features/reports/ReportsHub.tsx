import { useState, useMemo } from 'react';
import type { ColumnDef } from '@tanstack/react-table';
import {
  Clock,
  Download,
  Eye,
  FileCheck,
  FileText,
  Mail,
  Plus,
  Printer,
  RefreshCw,
  Search,
  Send,
  ShieldCheck,
} from 'lucide-react';
import { Main } from '@/components/layout/Main';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import {
  Sheet,
  SheetBody,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet';
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

export interface ReportsHubProps {
  readonly siteId: string;
}

type ReportType = 'ENERGY_EFFICIENCY' | 'MV_VERIFICATION' | 'CARBON_ACCOUNTING' | 'DAILY_OPERATIONS' | 'BILLING';

interface GeneratedReport {
  readonly id: string;
  readonly code: string;
  readonly title: string;
  readonly type: ReportType;
  readonly typeLabel: string;
  readonly period: string;
  readonly completeness: number;
  readonly generatedAt: string;
  readonly author: string;
  readonly recipients: string;
  readonly status: 'PUBLISHED' | 'PENDING_SIGN';
  readonly fileSize: string;
  readonly hash: string;
}

const GENERATED_REPORTS: readonly GeneratedReport[] = [
  {
    id: 'rep-01',
    code: 'REP-202608-EE',
    title: '东京中央冷站 2026年8月能效与运行分析报告',
    type: 'ENERGY_EFFICIENCY',
    typeLabel: '系统能效月报',
    period: '2026-08-01 ~ 2026-08-31',
    completeness: 100,
    generatedAt: '2026-09-01 08:30',
    author: '自动化定时调度引擎',
    recipients: '运维部、能管中心主管、物业总监 (共 6 人)',
    status: 'PUBLISHED',
    fileSize: '4.2 MB (PDF)',
    hash: 'sha256:4a8e31bf890...72c8',
  },
  {
    id: 'rep-02',
    code: 'REP-202608-MV',
    title: '暖通空调系统运行节能改造 M&V 节能量核验报告 (8月度)',
    type: 'MV_VERIFICATION',
    typeLabel: '节能量 M&V 核验',
    period: '2026-08-01 ~ 2026-08-31',
    completeness: 100,
    generatedAt: '2026-09-02 10:15',
    author: '李工 (节能核验工程师)',
    recipients: '节能服务商 EMC 联合项目组、业主财务审计组',
    status: 'PENDING_SIGN',
    fileSize: '2.8 MB (PDF + 原始数据包)',
    hash: 'sha256:b17f902c114...59a3',
  },
  {
    id: 'rep-03',
    code: 'REP-2026-Q2-CARB',
    title: '建筑群温室气体排放核算与绿电消纳结算报告 (Q2)',
    type: 'CARBON_ACCOUNTING',
    typeLabel: '碳盘查与绿电',
    period: '2026-04-01 ~ 2026-06-30',
    completeness: 98.5,
    generatedAt: '2026-07-05 14:00',
    author: 'ESG 综合管理组',
    recipients: '集团可持续发展委员会、第三方碳核查机构',
    status: 'PUBLISHED',
    fileSize: '6.1 MB (PDF)',
    hash: 'sha256:9c21ee8801d...f412',
  },
  {
    id: 'rep-04',
    code: 'REP-DAILY-0914',
    title: '中央冷站昨日运行调度与负荷峰值简报 (09-14)',
    type: 'DAILY_OPERATIONS',
    typeLabel: '运行调度日报',
    period: '2026-09-14 00:00 ~ 23:59',
    completeness: 100,
    generatedAt: '2026-09-15 06:00',
    author: '自动化定时调度引擎',
    recipients: '早班值班班长、暖通运行工程师群组',
    status: 'PUBLISHED',
    fileSize: '850 KB (PDF)',
    hash: 'sha256:5e128cb4410...099a',
  },
  {
    id: 'rep-05',
    code: 'REP-202608-BILL',
    title: '电费账单对账与峰谷分时电价成本核算表 (8月)',
    type: 'BILLING',
    typeLabel: '电费电价对账',
    period: '2026-08-01 ~ 2026-08-31',
    completeness: 100,
    generatedAt: '2026-09-03 16:20',
    author: '财务与结算网关',
    recipients: '财务结算中心、能源成本管理员',
    status: 'PUBLISHED',
    fileSize: '1.4 MB (Excel + PDF)',
    hash: 'sha256:c09931ad124...4311',
  },
];

export function ReportsHub({ siteId: _siteId }: ReportsHubProps) {
  const [selectedReport, setSelectedReport] = useState<GeneratedReport | null>(null);
  const [typeFilter, setTypeFilter] = useState<string>('ALL');
  const [statusFilter, setStatusFilter] = useState<string>('ALL');
  const [searchQuery, setSearchQuery] = useState('');
  const statusPills: readonly DataTableViewPillOption[] = [
    { key: 'ALL', label: '全部报告', count: GENERATED_REPORTS.length },
    { key: 'PUBLISHED', label: '已正式发布', count: GENERATED_REPORTS.filter((r) => r.status === 'PUBLISHED').length },
    { key: 'PENDING_SIGN', label: '待联合签署', count: GENERATED_REPORTS.filter((r) => r.status === 'PENDING_SIGN').length },
  ];

  const filteredReports = useMemo(() => {
    return GENERATED_REPORTS.filter((rep) => {
      if (typeFilter !== 'ALL' && rep.type !== typeFilter) return false;
      if (statusFilter !== 'ALL' && rep.status !== statusFilter) return false;
      if (!searchQuery.trim()) return true;
      const q = searchQuery.toLowerCase();
      return rep.title.toLowerCase().includes(q) || rep.code.toLowerCase().includes(q) || rep.author.toLowerCase().includes(q);
    });
  }, [typeFilter, statusFilter, searchQuery]);

  const columns = useMemo<Array<ColumnDef<DataTableFeatures, GeneratedReport>>>(() => [
    {
      id: 'select',
      header: ({ table }) => (
        <Checkbox
          checked={table.getIsAllPageRowsSelected() ? true : table.getIsSomePageRowsSelected() ? 'indeterminate' : false}
          onCheckedChange={(checked) => table.toggleAllPageRowsSelected(Boolean(checked))}
          aria-label="选择全部报告"
        />
      ),
      cell: ({ row }) => (
        <Checkbox
          checked={row.getIsSelected()}
          onCheckedChange={(checked) => row.toggleSelected(Boolean(checked))}
          aria-label={`选择 ${row.original.code}`}
          onClick={(event) => event.stopPropagation()}
        />
      ),
      enableSorting: false,
      enableHiding: false,
    },
    { id: 'code', header: '报告编号', cell: ({ row }) => <span className="font-mono font-semibold text-foreground">{row.original.code}</span> },
    {
      id: 'title',
      header: '报告标题与主题',
      cell: ({ row }) => <div><div className="font-medium text-foreground">{row.original.title}</div><div className="font-mono text-[11px] text-muted-foreground tabular-nums">{row.original.fileSize}</div></div>,
    },
    { id: 'type', header: '报告类别', cell: ({ row }) => <span className="rounded bg-muted px-2 py-0.5 text-[11px] text-muted-foreground">{row.original.typeLabel}</span> },
    { id: 'period', header: '统计时间跨度', cell: ({ row }) => <span className="font-mono text-[11px] text-muted-foreground tabular-nums">{row.original.period}</span> },
    { id: 'completeness', header: '完整度', cell: ({ row }) => <span className="font-mono font-medium text-foreground tabular-nums">{row.original.completeness}%</span> },
    { id: 'generatedAt', header: '生成时间', cell: ({ row }) => <span className="font-mono text-[11px] text-muted-foreground tabular-nums">{row.original.generatedAt}</span> },
    {
      id: 'status',
      header: '状态',
      cell: ({ row }) => <StatusPillBadge tone={row.original.status === 'PUBLISHED' ? 'success' : 'warning'} pulse={row.original.status === 'PENDING_SIGN'} label={row.original.status === 'PUBLISHED' ? '已正式发布' : '待联合签署'} />,
    },
    {
      id: 'actions',
      header: '操作',
      cell: ({ row }) => (
        <div className="flex items-center justify-end gap-1">
          <Button variant="ghost" size="sm" className="h-7 gap-1 px-2 text-xs" onClick={(event) => { event.stopPropagation(); setSelectedReport(row.original); }}>
            <Eye className="h-3 w-3" />预览
          </Button>
          <Button variant="outline" size="sm" className="h-7 gap-1 px-2 text-xs" onClick={(event) => { event.stopPropagation(); alert(`正在打包并安全导出 ${row.original.code} 离线报告文档包`); }}>
            <Download className="h-3 w-3" />下载
          </Button>
        </div>
      ),
      enableSorting: false,
    },
  ], []);

  const table = useDataTable({
    key: 'surface-28-reports',
    data: [...filteredReports],
    columns,
    pageSize: 10,
    getRowId: (row) => row.id,
  });

  return (
    <Main className="space-y-6">
      {/* 1. Surface Page Header */}
      <div className="flex flex-col gap-3 md:flex-row md:items-center md:justify-between border-b pb-4">
        <div>
          <div className="flex items-center gap-2">
            <h1 className="text-2xl font-bold tracking-tight text-foreground">报告中心</h1>
            <Badge variant="outline" className="border-border text-foreground/80 text-xs">
              运营与能效报表
            </Badge>
            <span className="rounded bg-muted px-2 py-0.5 font-mono text-xs text-muted-foreground">
              {GENERATED_REPORTS.length} 份归档报告
            </span>
          </div>
          <p className="text-sm text-muted-foreground mt-1">
            查阅、导出各站点能效月报、节能量核验与日常运营报表，支持定时自动生成
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" className="gap-1.5" onClick={() => window.location.reload()}>
            <RefreshCw className="h-3.5 w-3.5" />
            刷新报告库
          </Button>
          <Button size="sm" className="gap-1.5">
            <Plus className="h-3.5 w-3.5" />
            即时生成报告
          </Button>
        </div>
      </div>

      {/* 2. Document Compliance & Archive Posture Cards */}
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
        {/* 1. 已归档报告存量 */}
        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-xs font-medium text-muted-foreground">本月已归档发布报告</CardTitle>
            <FileText className="size-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="flex items-baseline gap-2">
              <span className="text-2xl font-bold tabular-nums tracking-tight text-foreground">14</span>
              <span className="text-xs text-muted-foreground">份 (PDF & Excel)</span>
            </div>
            <div className="mt-2 space-y-1 text-xs border-t pt-2">
              <div className="flex items-center justify-between text-muted-foreground">
                <span>覆盖报告范围</span>
                <span className="text-foreground font-medium">能效 / M&V / 碳核查</span>
              </div>
              <div className="flex items-center justify-between text-[11px] text-muted-foreground">
                <span>本月增量</span>
                <span className="text-emerald-600 font-semibold tabular-nums">+3 份新增</span>
              </div>
            </div>
          </CardContent>
        </Card>

        {/* 2. 自动化调度 */}
        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-xs font-medium text-muted-foreground">自动调度定时引擎</CardTitle>
            <Clock className="size-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="flex items-baseline gap-2">
              <span className="text-2xl font-bold tabular-nums tracking-tight text-foreground">5</span>
              <span className="text-xs text-muted-foreground">条活跃调度任务</span>
            </div>
            <div className="mt-2 space-y-1 text-xs border-t pt-2">
              <div className="flex items-center justify-between text-muted-foreground">
                <span>调度执行策略</span>
                <span className="text-foreground font-medium">日06:00 / 月1日08:00</span>
              </div>
              <div className="flex items-center justify-between text-[11px] text-muted-foreground">
                <span>下次触发</span>
                <span className="text-foreground font-medium">约 5 小时后</span>
              </div>
            </div>
          </CardContent>
        </Card>

        {/* 3. 投递可靠性 */}
        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-xs font-medium text-muted-foreground">分发投递成功率</CardTitle>
            <Send className="size-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="flex items-baseline gap-2">
              <span className="text-2xl font-bold tabular-nums tracking-tight text-foreground">100%</span>
              <span className="text-xs text-muted-foreground">全渠道零丢包</span>
            </div>
            <div className="mt-2 space-y-1 text-xs border-t pt-2">
              <div className="flex items-center justify-between text-muted-foreground">
                <span>通道保活覆盖</span>
                <span className="text-foreground font-medium">企微 / 钉钉 / 邮件</span>
              </div>
              <div className="flex items-center justify-between text-[11px] text-muted-foreground">
                <span>最后心跳</span>
                <span className="text-emerald-600 font-semibold">2 分钟前对齐</span>
              </div>
            </div>
          </CardContent>
        </Card>

        {/* 4. 待签署报告 */}
        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-xs font-medium text-muted-foreground">待联合签署审核报告</CardTitle>
            <FileCheck className="size-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="flex items-baseline gap-2">
              <span className="text-2xl font-bold tabular-nums tracking-tight text-foreground">1</span>
              <span className="text-xs text-muted-foreground">份待签核发</span>
            </div>
            <div className="mt-2 space-y-1 text-xs border-t pt-2">
              <div className="flex items-center justify-between text-muted-foreground">
                <span>待办关键文件</span>
                <span className="font-medium text-foreground text-[11px] truncate max-w-[140px]">8月 M&V 结算书</span>
              </div>
              <div className="flex items-center justify-between text-[11px] text-muted-foreground">
                <span>审核门禁要求</span>
                <span className="text-amber-600 font-medium">需暖通与财务双签</span>
              </div>
            </div>
          </CardContent>
        </Card>
      </div>

      {/* 3. Reports Ledger Table Container */}
      <Card className="shadow-xs">
        {/* Filter Toolbar */}
        <div className="p-4 border-b flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between bg-muted/10">
          <div>
            <h3 className="text-sm font-semibold text-foreground">已生成报告列表</h3>
            <p className="text-xs text-muted-foreground">
              支持在线预览与下载 PDF / Excel 格式报告
            </p>
          </div>

          <div className="flex flex-wrap items-center gap-2">
            <div className="relative w-48 sm:w-60">
              <Search className="absolute left-2.5 top-2.5 h-3.5 w-3.5 text-muted-foreground" />
              <Input
                placeholder="搜索标题 / 编号 / 编撰人..."
                value={searchQuery}
                onChange={(e) => {
                  setSearchQuery(e.target.value);
                  table.setPageIndex(0);
                }}
                className="h-8 pl-8 text-xs bg-background"
              />
            </div>

            <select
              value={typeFilter}
              onChange={(e) => {
                setTypeFilter(e.target.value);
                table.setPageIndex(0);
              }}
              className="h-8 rounded-md border border-input bg-background px-2.5 text-xs text-foreground focus:outline-none focus:ring-1 focus:ring-ring"
            >
              <option value="ALL">全部报告类别</option>
              <option value="ENERGY_EFFICIENCY">能效分析月报</option>
              <option value="MV_VERIFICATION">节能量 M&V 核验</option>
              <option value="CARBON_ACCOUNTING">碳盘查与绿电</option>
              <option value="DAILY_OPERATIONS">运行调度简报</option>
              <option value="BILLING">账单电价对账</option>
            </select>

            <DataTableViewOptions table={table} />
          </div>
        </div>

        <div className="px-4 py-2.5 border-b bg-background">
          <DataTableViewPills
            options={statusPills}
            value={statusFilter}
            onValueChange={(val) => {
              setStatusFilter(val);
              table.setPageIndex(0);
            }}
          />
        </div>

        <DataTable
          table={table}
          className="gap-0"
          tableAriaLabel="已生成报告列表"
          empty="没有符合筛选条件的归档报告记录"
          getHeaderRowProps={() => ({ className: 'bg-muted/30 text-xs hover:bg-muted/30' })}
          getHeaderCellProps={(header) => ({
            className:
              header.id === 'select' ? 'w-10' :
              header.id === 'code' ? 'w-[140px]' :
              header.id === 'type' || header.id === 'generatedAt' ? 'w-[130px]' :
              header.id === 'period' ? 'w-[180px]' :
              header.id === 'completeness' ? 'w-[100px] text-center' :
              header.id === 'status' ? 'w-[120px] text-center' :
              header.id === 'actions' ? 'w-[120px] text-right' :
              undefined,
          })}
          getRowProps={(row) => ({
            className: 'cursor-pointer text-xs transition-colors hover:bg-muted/40',
            onClick: () => setSelectedReport(row.original),
          })}
          getCellProps={(cell) => ({
            className:
              cell.column.id === 'completeness' || cell.column.id === 'status'
                ? 'text-center'
                : cell.column.id === 'actions'
                  ? 'text-right'
                  : undefined,
          })}
          footer={(
            <DataTablePagination
              table={table}
              totalRows={filteredReports.length}
              
              
              
              
              
            />
          )}
        />
      </Card>

      {/* 4. Report Preview Sheet */}
      <Sheet open={Boolean(selectedReport)} onOpenChange={(open) => !open && setSelectedReport(null)}>
        <SheetContent className="sm:max-w-xl">
          <SheetHeader>
            <div className="flex items-center gap-2">
              <span className="font-mono text-xs font-semibold text-muted-foreground">
                {selectedReport?.code}
              </span>
              <span className="rounded bg-muted px-2 py-0.5 text-[10px] font-medium text-muted-foreground">
                {selectedReport?.typeLabel}
              </span>
            </div>
            <SheetTitle className="text-base font-bold text-foreground mt-1">
              {selectedReport?.title}
            </SheetTitle>
            <SheetDescription className="text-xs">
              统计跨度: {selectedReport?.period} · 生成时间: {selectedReport?.generatedAt}
            </SheetDescription>
          </SheetHeader>

          {selectedReport && (
            <SheetBody className="space-y-5 pt-4 text-xs">
              {/* Report Facts Grid */}
              <div className="grid grid-cols-2 gap-3 rounded-lg border border-border/80 bg-muted/10 p-3.5">
                <div>
                  <div className="text-muted-foreground text-[11px]">报告编撰来源</div>
                  <div className="mt-1 font-medium text-foreground text-xs">
                    {selectedReport.author}
                  </div>
                </div>
                <div>
                  <div className="text-muted-foreground text-[11px]">数据完整度审计</div>
                  <div className="mt-1 tabular-nums text-sm font-bold text-emerald-600">
                    {selectedReport.completeness}% (无断点插值)
                  </div>
                </div>
                <div>
                  <div className="text-muted-foreground text-[11px]">文档包规格</div>
                  <div className="mt-1 text-[11px] tabular-nums text-foreground">
                    {selectedReport.fileSize}
                  </div>
                </div>
                <div>
                  <div className="text-muted-foreground text-[11px]">防篡改校验指纹</div>
                  <div className="mt-1 text-[11px] font-mono text-muted-foreground">
                    {selectedReport.hash}
                  </div>
                </div>
              </div>

              {/* Delivery Channels */}
              <div className="space-y-2">
                <div className="text-xs font-semibold text-foreground flex items-center gap-1.5">
                  <Mail className="h-3.5 w-3.5 text-primary" />
                  已投递受众与分发渠道留痕
                </div>
                <div className="rounded-md border border-border/80 p-3 text-[11px] text-muted-foreground bg-background leading-relaxed">
                  {selectedReport.recipients}
                </div>
              </div>

              {/* Report Abstract */}
              <div className="space-y-2">
                <div className="text-xs font-semibold text-foreground flex items-center gap-1.5">
                  <FileText className="h-3.5 w-3.5 text-primary" />
                  核心结论摘要
                </div>
                <div className="rounded-md border border-border/80 bg-muted/10 p-3 text-xs space-y-2 leading-relaxed text-foreground">
                  <p>
                    1. 报告期内系统综合供冷能效比达到 <strong>COP 4.88</strong>，同比去年基线期提升 <strong>+5.4%</strong>。
                  </p>
                  <p>
                    2. 实施自适应出水温动态提升与冷冻泵变频调节后，核验净节电量达到 <strong>68,500 kWh</strong>，折算节约电费支出 <strong>¥58,200 元</strong>。
                  </p>
                  <p>
                    3. 舒适度与室内环境监测显示，主要租户区域供冷温湿度保证率达到 <strong>99.6%</strong>，无超限告警。
                  </p>
                </div>
              </div>

              {/* Dual signoff / download actions */}
              <div className="pt-3 border-t flex items-center justify-between">
                <div className="flex items-center gap-1.5 text-[11px] text-muted-foreground">
                  <ShieldCheck className="h-3.5 w-3.5 text-emerald-600" />
                  <span>经系统校验与归档</span>
                </div>
                <div className="flex items-center gap-2">
                  <Button variant="outline" size="sm" onClick={() => setSelectedReport(null)}>
                    关闭
                  </Button>
                  <Button size="sm" className="gap-1.5">
                    <Printer className="h-3.5 w-3.5" />
                    下载 / 导出
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
