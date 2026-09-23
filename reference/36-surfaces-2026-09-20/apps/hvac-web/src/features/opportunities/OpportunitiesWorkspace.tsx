import { useMemo, useState } from 'react';
import type { ColumnDef } from '@tanstack/react-table';
import {
  Calculator,
  Clock,
  Coins,
  Layers,
  Lightbulb,
  Search,
  ShieldCheck,
  Wrench,
  Zap,
} from 'lucide-react';

import { Main } from '@/components/layout/Main';
import type { CurrentPrincipalResponse, Site } from '@/api/generated/platformGateway.gen';
import { siteRoute } from '@/app/router-paths';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import {
  Sheet,
  SheetBody,
  SheetContent,
  SheetDescription,
  SheetFooter,
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
import { cn } from '@/lib/utils';

interface OpportunitiesProps {
  site: Readonly<Site>;
  principal: CurrentPrincipalResponse;
}

export interface EcoOpportunity {
  id: string;
  code: string;
  title: string;
  subsystem: 'CHILLER' | 'PUMP' | 'TOWER' | 'TERMINAL' | 'SYSTEM';
  complexity: 'ZERO_LOW_COST' | 'MEDIUM' | 'CAPITAL';
  annualSavingsKWh: number;
  annualSavingsCostCNY: number;
  investmentCostCNY: number;
  paybackMonths: number;
  status: 'IDENTIFIED' | 'REVIEWING' | 'APPROVED' | 'IN_PROGRESS' | 'VERIFYING';
  summary: string;
  baselineCondition: string;
  proposedCondition: string;
  calculationMethod: string;
  riskNotice: string;
}

const OPPORTUNITIES_DATA: readonly EcoOpportunity[] = [
  {
    id: 'eco-01',
    code: 'ECO-CHW-01',
    title: '冷冻水供水温度自适应动态重置 (7.0°C → 8.5°C)',
    subsystem: 'CHILLER',
    complexity: 'ZERO_LOW_COST',
    annualSavingsKWh: 68500,
    annualSavingsCostCNY: 58200,
    investmentCostCNY: 0,
    paybackMonths: 0,
    status: 'APPROVED',
    summary: '根据建筑末端最大阀位开度与回水温度，将主机出水温度由固定 7.0°C 提高到 7.5°C~8.5°C，每提升 1°C 主机 COP 可提高 2%~3%。',
    baselineCondition: '主机常年固定 7.0°C 出水，末端阀门开度普遍低于 50%，存在典型大流量小温差。',
    proposedCondition: '基于最不利末端阀门开度闭环调节出水温度设定值，目标保持最不利环路阀门开度在 85%~90%。',
    calculationMethod: '基于当前主机功耗曲线，平均温升 1.2°C 对应主机年节电率 3.8%。',
    riskNotice: '必须监视最不利环路温湿度，若湿度超标立即触发回退保护。',
  },
  {
    id: 'eco-02',
    code: 'ECO-PUMP-02',
    title: '冷冻水二次泵变频压差闭环控制优化',
    subsystem: 'PUMP',
    complexity: 'ZERO_LOW_COST',
    annualSavingsKWh: 42000,
    annualSavingsCostCNY: 34500,
    investmentCostCNY: 5000,
    paybackMonths: 1.7,
    status: 'REVIEWING',
    summary: '消除传统固定压差控制的安全裕量冗余，改用最不利末端动态压差复核与自适应设定。',
    baselineCondition: '主供回水管集分水器固定 0.25 MPa 压差控制，部分水泵频繁运行在 48Hz 以上。',
    proposedCondition: '将压差传感器信号引自末端最不利立管，压差设定从 0.25 MPa 优化为 0.16~0.20 MPa 浮动。',
    calculationMethod: '根据水泵亲和定律，泵扬程降低 20% 对应水泵功耗降低 35% 以上。',
    riskNotice: '需确保各楼层末端流量传感器通信延时小于 10 秒。',
  },
  {
    id: 'eco-03',
    code: 'ECO-TOWER-03',
    title: '过渡季与夜间冷却塔水侧自由冷却（Free Cooling）',
    subsystem: 'TOWER',
    complexity: 'CAPITAL',
    annualSavingsKWh: 85000,
    annualSavingsCostCNY: 72000,
    investmentCostCNY: 98000,
    paybackMonths: 16.3,
    status: 'IDENTIFIED',
    summary: '在室外湿球温度低于 10°C 的过渡季，利用冷却塔与板式换热器直接供应冷冻水，停开主机。',
    baselineCondition: '过渡季仍需开机单台离心机以 30% 低负荷维持商业大堂与机房制冷，能效极低。',
    proposedCondition: '增设板式换热器并联旁通管路，在湿球温度低于 10°C 时自动切换为自由冷却模式。',
    calculationMethod: '当地过渡季累计湿球温度 ≤ 10°C 时长约 950 小时，折合节约主机运行电耗 85 MWh。',
    riskNotice: '需核查防冻联锁与加药水质控制。',
  },
  {
    id: 'eco-04',
    code: 'ECO-TERM-04',
    title: '非营业时段商铺及办公末端待机能耗自动关断',
    subsystem: 'TERMINAL',
    complexity: 'ZERO_LOW_COST',
    annualSavingsKWh: 29500,
    annualSavingsCostCNY: 23600,
    investmentCostCNY: 0,
    paybackMonths: 0,
    status: 'APPROVED',
    summary: '根据建筑人员在室 schedule 与门禁联动，在 22:00~07:00 自动关断闲置区域末端水阀与风机。',
    baselineCondition: '夜间部分末端未关阀门，导致管网仍有 20% 冷量空耗与循环水力损失。',
    proposedCondition: '通过 BMS 自动下发日程排程，非加班时段强制闭锁水阀。',
    calculationMethod: '按夜间 8 小时待机漏冷量测算，减少冷负荷空损。',
    riskNotice: '支持租户临时加班一键延时申请。',
  },
  {
    id: 'eco-05',
    code: 'ECO-CHW-05',
    title: '冷水机组群控动态负荷加减机策略调优',
    subsystem: 'CHILLER',
    complexity: 'MEDIUM',
    annualSavingsKWh: 38000,
    annualSavingsCostCNY: 31200,
    investmentCostCNY: 15000,
    paybackMonths: 5.8,
    status: 'IN_PROGRESS',
    summary: '优化主机加载减机阈值，避免 2 台离心机长期运行在 40% 低效区，保持单机负荷在 70%~85% 最佳 COP 区。',
    baselineCondition: '传统根据冷冻水回水温度加减机，频繁出现双机轻载并行。',
    proposedCondition: '根据冷负荷实时 kW 闭环加减机，优先将主力机推至 75% 负载再启动次级机。',
    calculationMethod: '单机 75% 负荷 COP (5.5) 明显高于双机 40% 负荷 COP (4.3)。',
    riskNotice: '加减机必须设置 30 分钟防抖延时，防止冷机频繁启停损伤轴承。',
  },
];

const SUBSYSTEM_LABELS = {
  CHILLER: '冷水主机',
  PUMP: '水泵输配',
  TOWER: '冷却塔',
  TERMINAL: '空调末端',
  SYSTEM: '全系统',
};

const COMPLEXITY_LABELS = {
  ZERO_LOW_COST: { label: '零/低成本调优', dotColor: 'bg-emerald-500' },
  MEDIUM: { label: '控制策略改造', dotColor: 'bg-sky-500' },
  CAPITAL: { label: '设备工程改造', dotColor: 'bg-amber-500' },
};

const STATUS_LABELS = {
  IDENTIFIED: { label: '已识别', dotColor: 'bg-muted-foreground' },
  REVIEWING: { label: '方案评审中', dotColor: 'bg-sky-500' },
  APPROVED: { label: '已批准实施', dotColor: 'bg-emerald-500' },
  IN_PROGRESS: { label: '正在执行', dotColor: 'bg-amber-500' },
  VERIFYING: { label: '节能量验证中', dotColor: 'bg-purple-500' },
};

export function OpportunitiesWorkspace({ site }: OpportunitiesProps) {
  const [filterSubsystem, setFilterSubsystem] = useState<string>('ALL');
  const [selectedOpportunity, setSelectedOpportunity] = useState<EcoOpportunity | null>(null);
  const [search, setSearch] = useState('');
  const subsystemPills: readonly DataTableViewPillOption[] = [
    { key: 'ALL', label: '全部系统', count: OPPORTUNITIES_DATA.length },
    { key: 'CHILLER', label: '冷水主机', count: OPPORTUNITIES_DATA.filter((d) => d.subsystem === 'CHILLER').length },
    { key: 'PUMP', label: '水泵输配', count: OPPORTUNITIES_DATA.filter((d) => d.subsystem === 'PUMP').length },
    { key: 'TOWER', label: '冷却塔', count: OPPORTUNITIES_DATA.filter((d) => d.subsystem === 'TOWER').length },
    { key: 'TERMINAL', label: '空调末端', count: OPPORTUNITIES_DATA.filter((d) => d.subsystem === 'TERMINAL').length },
  ];

  const filteredData = useMemo(() => {
    return OPPORTUNITIES_DATA.filter((item) => {
      const matchSubsystem = filterSubsystem === 'ALL' || item.subsystem === filterSubsystem;
      const matchSearch = !search.trim() || item.title.toLowerCase().includes(search.toLowerCase()) || item.code.toLowerCase().includes(search.toLowerCase());
      return matchSubsystem && matchSearch;
    });
  }, [filterSubsystem, search]);

  const totalKWh = useMemo(() => OPPORTUNITIES_DATA.reduce((acc, cur) => acc + cur.annualSavingsKWh, 0), []);
  const totalCost = useMemo(() => OPPORTUNITIES_DATA.reduce((acc, cur) => acc + cur.annualSavingsCostCNY, 0), []);

  const columns = useMemo<Array<ColumnDef<DataTableFeatures, EcoOpportunity>>>(() => [
    {
      id: 'select',
      header: ({ table }) => (
        <Checkbox
          checked={table.getIsAllPageRowsSelected() ? true : table.getIsSomePageRowsSelected() ? 'indeterminate' : false}
          onCheckedChange={(checked) => table.toggleAllPageRowsSelected(Boolean(checked))}
          aria-label="选择全部节能机会"
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
    { id: 'code', header: '编号', cell: ({ row }) => <span className="font-mono text-xs text-muted-foreground">{row.original.code}</span> },
    {
      id: 'title',
      header: '节能改造措施',
      cell: ({ row }) => (
        <div>
          <div className="font-medium text-foreground">{row.original.title}</div>
          <div className="mt-0.5 line-clamp-1 text-xs text-muted-foreground">{row.original.summary}</div>
        </div>
      ),
    },
    { id: 'subsystem', header: '所属系统', cell: ({ row }) => <span className="text-sm">{SUBSYSTEM_LABELS[row.original.subsystem]}</span> },
    {
      id: 'complexity',
      header: '实施难度',
      cell: ({ row }) => (
        <StatusPillBadge
          label={COMPLEXITY_LABELS[row.original.complexity].label}
          tone={row.original.complexity === 'ZERO_LOW_COST' ? 'success' : row.original.complexity === 'MEDIUM' ? 'info' : 'warning'}
        />
      ),
    },
    { id: 'savingsKWh', header: '预期年节电', cell: ({ row }) => <span className="font-mono text-sm font-medium tabular-nums">{row.original.annualSavingsKWh.toLocaleString()} kWh</span> },
    { id: 'savingsCost', header: '预期年节费', cell: ({ row }) => <span className="font-mono text-sm font-semibold text-foreground tabular-nums">¥{row.original.annualSavingsCostCNY.toLocaleString()}</span> },
    { id: 'payback', header: '回收期', cell: ({ row }) => <span className="font-mono text-sm tabular-nums">{row.original.paybackMonths === 0 ? '即时/零成本' : `${row.original.paybackMonths} 个月`}</span> },
    {
      id: 'status',
      header: '推进状态',
      cell: ({ row }) => (
        <StatusPillBadge
          label={STATUS_LABELS[row.original.status].label}
          tone={
            row.original.status === 'APPROVED'
              ? 'success'
              : row.original.status === 'IN_PROGRESS'
                ? 'warning'
                : row.original.status === 'REVIEWING' || row.original.status === 'VERIFYING'
                  ? 'info'
                  : 'neutral'
          }
        />
      ),
    },
    {
      id: 'actions',
      header: '操作',
      cell: ({ row }) => (
        <Button
          variant="ghost"
          size="sm"
          onClick={(event) => {
            event.stopPropagation();
            setSelectedOpportunity(row.original);
          }}
        >
          详情
        </Button>
      ),
      enableSorting: false,
    },
  ], []);

  const table = useDataTable({
    key: 'surface-21-opportunities',
    data: [...filteredData],
    columns,
    pageSize: 10,
    getRowId: (row) => row.id,
  });

  return (
    <Main className="space-y-6">
      {/* 1. Page Header */}
      <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <div className="flex items-center gap-2">
            <h1 className="text-2xl font-bold tracking-tight text-foreground">节能机会</h1>
            <Badge variant="outline" className="text-xs font-normal">
              节能机会台账
            </Badge>
          </div>
          <p className="mt-1 text-sm text-muted-foreground">
            已识别的暖通节能机会、效益测算与回收期评估
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" asChild>
            <a href={siteRoute(site, 'optimize')}>
              <Zap className="mr-1.5 size-3.5" />
              优化方案
            </a>
          </Button>
        </div>
      </div>

      {/* 2. 4-Card Opportunity KPI Strip */}
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">已识别节能机会</CardTitle>
            <Lightbulb className="size-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold tracking-tight text-foreground tabular-nums">{OPPORTUNITIES_DATA.length} 项</div>
            <p className="text-xs text-muted-foreground mt-1">其中 2 项已获批 · 1 项执行中</p>
          </CardContent>
        </Card>

        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">预计年节电量潜力</CardTitle>
            <Zap className="size-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold tracking-tight text-foreground tabular-nums">{(totalKWh / 1000).toFixed(1)} <span className="text-sm font-normal text-muted-foreground">MWh/年</span></div>
            <p className="text-xs text-muted-foreground mt-1">折合约 {totalKWh.toLocaleString()} 度电</p>
          </CardContent>
        </Card>

        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">预计年节约电费</CardTitle>
            <Coins className="size-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold tracking-tight text-foreground tabular-nums">¥{(totalCost / 10000).toFixed(1)} <span className="text-sm font-normal text-muted-foreground">万元/年</span></div>
            <p className="text-xs text-muted-foreground mt-1">综合平均电价 0.825 元/kWh 测算</p>
          </CardContent>
        </Card>

        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">平均投资回收期</CardTitle>
            <Clock className="size-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold tracking-tight text-foreground tabular-nums">4.8 <span className="text-sm font-normal text-muted-foreground">个月</span></div>
            <p className="text-xs text-muted-foreground mt-1">零成本优化项目占比 60%</p>
          </CardContent>
        </Card>
      </div>

      {/* 3. Opportunities Table */}
      <Card>
        <CardHeader className="pb-4">
          <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
            <div>
              <CardTitle className="text-lg">节能机会列表</CardTitle>
              <CardDescription>冷站运行与控制优化措施清单</CardDescription>
            </div>
            <div className="flex flex-wrap items-center gap-2">
              <div className="relative min-w-[200px]">
                <Search className="pointer-events-none absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
                <Input
                  className="pl-8 h-9 text-sm"
                  placeholder="搜索措施名称 / 编号"
                  value={search}
                  onChange={(e) => {
                    setSearch(e.target.value);
                    table.setPageIndex(0);
                  }}
                />
              </div>
              <DataTableViewPills
                options={subsystemPills}
                value={filterSubsystem}
                onValueChange={(val) => {
                  setFilterSubsystem(val);
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
            tableAriaLabel="节能机会列表"
            empty="无匹配的节能机会记录"
            getHeaderCellProps={(header) => ({
              className:
                header.id === 'select' ? 'w-[40px] px-3' :
                header.id === 'code' ? 'w-[120px]' :
                header.id === 'title' ? 'min-w-[260px]' :
                header.id === 'subsystem' || header.id === 'payback' ? 'w-[110px]' :
                header.id === 'complexity' || header.id === 'savingsKWh' || header.id === 'savingsCost' ? 'w-[130px]' :
                header.id === 'status' ? 'w-[120px]' :
                header.id === 'actions' ? 'w-[80px] text-right' :
                undefined,
            })}
            getRowProps={(row) => ({
              className: 'cursor-pointer hover:bg-muted/50',
              onClick: () => setSelectedOpportunity(row.original),
            })}
            getCellProps={(cell) => ({
              className: cell.column.id === 'select' ? 'px-3' : cell.column.id === 'actions' ? 'text-right' : undefined,
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

      {/* 4. Slide-over Sheet for Opportunity Detail */}
      <Sheet open={Boolean(selectedOpportunity)} onOpenChange={(open) => { if (!open) setSelectedOpportunity(null); }}>
        <SheetContent className="w-full sm:max-w-2xl overflow-y-auto">
          <SheetHeader>
            <div className="font-mono text-xs text-muted-foreground">{selectedOpportunity?.code}</div>
            <SheetTitle className="text-xl">{selectedOpportunity?.title}</SheetTitle>
            <SheetDescription>{selectedOpportunity?.summary}</SheetDescription>
            {selectedOpportunity ? (
              <div className="mt-2 flex gap-2">
                <Badge variant="outline" className="gap-1 font-normal text-xs">
                  <span className={cn('size-1.5 rounded-full', COMPLEXITY_LABELS[selectedOpportunity.complexity].dotColor)} />
                  {COMPLEXITY_LABELS[selectedOpportunity.complexity].label}
                </Badge>
                <Badge variant="outline" className="gap-1 font-normal text-xs">
                  <span className={cn('size-1.5 rounded-full', STATUS_LABELS[selectedOpportunity.status].dotColor)} />
                  {STATUS_LABELS[selectedOpportunity.status].label}
                </Badge>
              </div>
            ) : null}
          </SheetHeader>
          <SheetBody className="space-y-6 pt-4">
            {selectedOpportunity ? (
              <div className="space-y-6">
                {/* 经济收益测算 */}
                <div className="space-y-3">
                  <div className="flex items-center gap-2 text-sm font-semibold text-foreground">
                    <Coins className="size-4 text-muted-foreground" />
                    经济与能耗收益测算
                  </div>
                  <div className="grid grid-cols-2 gap-3 rounded-md border p-3 bg-muted/20">
                    <div>
                      <span className="text-xs text-muted-foreground">预期年节电量</span>
                      <div className="text-lg font-bold tabular-nums text-foreground">{selectedOpportunity.annualSavingsKWh.toLocaleString()} kWh/年</div>
                    </div>
                    <div>
                      <span className="text-xs text-muted-foreground">预期年节费</span>
                      <div className="text-lg font-bold tabular-nums text-foreground">¥{selectedOpportunity.annualSavingsCostCNY.toLocaleString()} 元/年</div>
                    </div>
                    <div>
                      <span className="text-xs text-muted-foreground">预估初投资</span>
                      <div className="text-sm font-semibold tabular-nums text-foreground">{selectedOpportunity.investmentCostCNY === 0 ? '0 元 (运行参数微调)' : `¥${selectedOpportunity.investmentCostCNY.toLocaleString()} 元`}</div>
                    </div>
                    <div>
                      <span className="text-xs text-muted-foreground">静态投资回收期</span>
                      <div className="text-sm font-semibold tabular-nums text-foreground">{selectedOpportunity.paybackMonths === 0 ? '即时见效' : `${selectedOpportunity.paybackMonths} 个月`}</div>
                    </div>
                  </div>
                </div>

                {/* 工况对比 */}
                <div className="space-y-3">
                  <div className="flex items-center gap-2 text-sm font-semibold text-foreground">
                    <Layers className="size-4 text-muted-foreground" />
                    基准工况 vs 优化工况对比
                  </div>
                  <div className="space-y-2 text-sm">
                    <div className="rounded-md border p-3 bg-muted/10 space-y-1">
                      <div className="font-semibold text-foreground">改造前当前基准工况</div>
                      <p className="text-muted-foreground">{selectedOpportunity.baselineCondition}</p>
                    </div>
                    <div className="rounded-md border p-3 bg-muted/20 space-y-1">
                      <div className="font-semibold text-foreground">优化后目标运行工况</div>
                      <p className="text-muted-foreground">{selectedOpportunity.proposedCondition}</p>
                    </div>
                  </div>
                </div>

                {/* 计算与验证依据 */}
                <div className="space-y-3">
                  <div className="flex items-center gap-2 text-sm font-semibold text-foreground">
                    <Calculator className="size-4 text-muted-foreground" />
                    节能量计算方法与验证依据
                  </div>
                  <p className="text-xs leading-relaxed text-muted-foreground rounded-md border p-3">
                    {selectedOpportunity.calculationMethod}
                  </p>
                </div>

                {/* 风险防范与安全边界 */}
                <div className="space-y-3">
                  <div className="flex items-center gap-2 text-sm font-semibold text-foreground">
                    <ShieldCheck className="size-4 text-muted-foreground" />
                    调控约束与安全边界
                  </div>
                  <p className="text-xs leading-relaxed text-muted-foreground bg-muted/30 p-3 rounded-md border">
                    {selectedOpportunity.riskNotice}
                  </p>
                </div>
              </div>
            ) : null}
          </SheetBody>
          <SheetFooter className="gap-2 sm:justify-between border-t pt-4">
            <Button variant="outline" size="sm" asChild>
              <a href={siteRoute(site, 'work-orders')}>
                <Wrench className="mr-1.5 size-3.5" />
                生成现场工单
              </a>
            </Button>
            <Button size="sm" asChild>
              <a href={siteRoute(site, 'optimize')}>
                <Zap className="mr-1.5 size-3.5" />
                生成优化方案
              </a>
            </Button>
          </SheetFooter>
        </SheetContent>
      </Sheet>
    </Main>
  );
}
