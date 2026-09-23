import { useMemo } from 'react';
import type { ColumnDef } from '@tanstack/react-table';
import {
  Leaf,
  Factory,
  TrendingDown,
  ShieldCheck,
  RotateCcw,
  Download,
} from 'lucide-react';
import {
  ResponsiveContainer,
  Bar,
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
import { DataTable, StatusPillBadge, type DataTableFeatures } from '@/components/data-table';
import { useDataTable } from '@/hooks/use-data-table';

interface MonthlyCarbonRecord {
  readonly month: string;
  readonly scope1: number; // Gas combustion (tCO2e)
  readonly scope2: number; // Grid electricity (tCO2e)
  readonly offset: number; // Solar / Green power offset (tCO2e)
  readonly netTotal: number;
  readonly budget: number;
}

const MOCK_CARBON_MONTHLY: readonly MonthlyCarbonRecord[] = [
  { month: '1月', scope1: 18.2, scope2: 125.0, offset: 8.5, netTotal: 134.7, budget: 155.0 },
  { month: '2月', scope1: 16.5, scope2: 108.0, offset: 9.2, netTotal: 115.3, budget: 140.0 },
  { month: '3月', scope1: 9.4, scope2: 95.0, offset: 12.4, netTotal: 92.0, budget: 125.0 },
  { month: '4月', scope1: 4.2, scope2: 88.0, offset: 15.6, netTotal: 76.6, budget: 110.0 },
  { month: '5月', scope1: 2.1, scope2: 128.0, offset: 19.8, netTotal: 110.3, budget: 135.0 },
  { month: '6月', scope1: 1.5, scope2: 185.0, offset: 22.4, netTotal: 164.1, budget: 180.0 },
  { month: '7月', scope1: 1.2, scope2: 245.0, offset: 25.2, netTotal: 221.0, budget: 235.0 },
  { month: '8月', scope1: 1.1, scope2: 258.0, offset: 24.8, netTotal: 234.3, budget: 245.0 },
  { month: '9月(至今)', scope1: 0.8, scope2: 112.0, offset: 14.1, netTotal: 98.7, budget: 115.0 },
];

const EMISSION_FACTORS = [
  { source: '电网外购电力 (华东区域电网)', factor: '0.5342', unit: 'kgCO₂/kWh', standard: '生态环境部 2024 年度电网平均排放因子', validFrom: '2024-01-01' },
  { source: '管道天然气燃烧 (常压热水锅炉)', factor: '2.1622', unit: 'kgCO₂/m³', standard: '《公共建筑运营企业温室气体排放核算指南》', validFrom: '2023-06-01' },
  { source: '分布式光伏绿电消纳 (自发自用)', factor: '0.0000', unit: 'kgCO₂/kWh', standard: '绿色电力证书认定 (零间接排放)', validFrom: '永久有效' },
];

const REDUCTION_PROJECTS = [
  { name: '冷源主机大温差与自适应出水温升', savedKWh: '263,000 kWh', carbonSaved: '140.5 tCO₂e', status: '已通过 M&V 核验' },
  { name: '屋顶 320kW 分布式光伏发电并网', savedKWh: '185,400 kWh', carbonSaved: '99.0 tCO₂e', status: '常态自发自用中' },
  { name: '冷冻水二次泵定末端最不利压差控制', savedKWh: '48,000 kWh', carbonSaved: '25.6 tCO₂e', status: '投运验证中' },
];

export function CarbonEmissionsDashboard({ siteId: _siteId }: { readonly siteId?: string }) {
  const annualTotalNet = useMemo(() => {
    return MOCK_CARBON_MONTHLY.reduce((acc, curr) => acc + curr.netTotal, 0);
  }, []);

  const annualBudget = 1597.0;
  const budgetRatio = ((annualTotalNet / annualBudget) * 100).toFixed(1);

  type EmissionFactorRow = (typeof EMISSION_FACTORS)[number];
  type ReductionProjectRow = (typeof REDUCTION_PROJECTS)[number];

  const factorColumns = useMemo<Array<ColumnDef<DataTableFeatures, EmissionFactorRow>>>(() => [
    { id: 'source', header: '排放源类别', cell: ({ row }) => <span className="font-medium text-foreground">{row.original.source}</span> },
    { id: 'factor', header: '因子数值', cell: ({ row }) => <span className="font-mono font-semibold text-primary tabular-nums">{row.original.factor} <span className="text-[11px] font-normal text-muted-foreground">{row.original.unit}</span></span> },
    { id: 'standard', header: '依据标准/版本', cell: ({ row }) => <span className="text-muted-foreground">{row.original.standard}</span> },
    { id: 'validFrom', header: '状态/生效日期', cell: ({ row }) => <StatusPillBadge tone={row.original.factor === '0.0000' ? 'success' : 'neutral'} label={row.original.validFrom} /> },
  ], []);

  const factorTable = useDataTable({
    key: 'surface-19-carbon-emission-factors',
    data: [...EMISSION_FACTORS],
    columns: factorColumns,
    paginate: false,
    getRowId: (row) => row.source,
  });

  const reductionColumns = useMemo<Array<ColumnDef<DataTableFeatures, ReductionProjectRow>>>(() => [
    { id: 'name', header: '节能工程项目', cell: ({ row }) => <span className="font-medium text-foreground">{row.original.name}</span> },
    { id: 'savedKWh', header: '核验节电量', cell: ({ row }) => <span className="font-mono text-muted-foreground tabular-nums">{row.original.savedKWh}</span> },
    { id: 'carbonSaved', header: '减碳贡献', cell: ({ row }) => <span className="font-mono font-semibold text-foreground tabular-nums">{row.original.carbonSaved}</span> },
    {
      id: 'status',
      header: '核查状态',
      cell: ({ row }) => {
        const isVerified = row.original.status.includes('已通过');
        const isRunning = row.original.status.includes('常态');
        return <StatusPillBadge tone={isVerified ? 'success' : isRunning ? 'info' : 'in-progress'} pulse={!isVerified && !isRunning} label={row.original.status} />;
      },
    },
  ], []);

  const reductionTable = useDataTable({
    key: 'surface-19-carbon-reduction-projects',
    data: [...REDUCTION_PROJECTS],
    columns: reductionColumns,
    paginate: false,
    getRowId: (row) => row.name,
  });

  return (
    <Main className="space-y-6">
      {/* Header */}
      <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <div className="flex items-center gap-2">
            <h1 className="text-2xl font-bold tracking-tight text-foreground">碳排放与绿电核算</h1>
            <Badge variant="outline" className="font-normal gap-1.5 text-xs">
              <span className="size-1.5 rounded-full bg-emerald-500" />
              GHG Protocol Scope 1 & 2
            </Badge>
          </div>
          <p className="text-sm text-muted-foreground">
            基于可审计边界、国家电网区域排放因子与光伏消纳凭证，管理站点温室气体排放绩效与双碳目标轨迹。
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" className="h-9 gap-1.5" onClick={() => window.location.reload()}>
            <RotateCcw className="h-4 w-4" />
            刷新核算
          </Button>
          <Button size="sm" className="h-9 gap-1.5">
            <Download className="h-4 w-4" />
            导出温室气体核算表
          </Button>
        </div>
      </div>

      {/* 4-Card Fact Strip */}
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-xs font-medium text-muted-foreground">当年累计净碳排放</CardTitle>
            <Factory className="size-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold tracking-tight text-foreground tabular-nums">
              {annualTotalNet.toFixed(1)} <span className="text-xs font-normal text-muted-foreground">tCO₂e</span>
            </div>
            <p className="mt-1 text-xs text-muted-foreground">
              扣除光伏绿电抵消 152.0 吨，同比去年下降 14.8%
            </p>
          </CardContent>
        </Card>

        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-xs font-medium text-muted-foreground">建筑面积碳排放强度</CardTitle>
            <TrendingDown className="size-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold tracking-tight text-foreground tabular-nums">
              24.8 <span className="text-xs font-normal text-muted-foreground">kgCO₂e/m²</span>
            </div>
            <p className="mt-1 text-xs text-muted-foreground">
              总建筑面积 50,300 m²，优于同类公共建筑基准线 18%
            </p>
          </CardContent>
        </Card>

        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-xs font-medium text-muted-foreground">年度碳配额预算执行</CardTitle>
            <ShieldCheck className="size-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold tracking-tight text-foreground tabular-nums">
              {budgetRatio}%
            </div>
            <div className="mt-2">
              <Progress value={Number(budgetRatio)} className="h-1.5" />
            </div>
            <p className="mt-1 text-xs text-muted-foreground">
              年度限额 1,597 tCO₂e，剩余可用配额 350.1 tCO₂e
            </p>
          </CardContent>
        </Card>

        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-xs font-medium text-muted-foreground">绿电消纳贡献占比</CardTitle>
            <Leaf className="size-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold tracking-tight text-foreground tabular-nums">
              18.5%
            </div>
            <p className="mt-1 text-xs text-muted-foreground">
              分布式光伏年发电 284 MWh，全部就地消纳
            </p>
          </CardContent>
        </Card>
      </div>

      {/* Monthly Emissions Trend Chart */}
      <Card className="shadow-xs">
        <CardHeader className="pb-2">
          <div className="flex flex-col gap-1 sm:flex-row sm:items-center sm:justify-between">
            <div>
              <CardTitle className="text-base font-semibold">月度碳排放与配额控制</CardTitle>
              <CardDescription>
                范围一/二排放及绿电抵消后的净排放量
              </CardDescription>
            </div>
          </div>
        </CardHeader>
        <CardContent>
          <div className="h-[320px] w-full pt-2">
            <ResponsiveContainer width="100%" height="100%">
              <ComposedChart data={MOCK_CARBON_MONTHLY as unknown as Record<string, unknown>[]} margin={{ top: 10, right: 20, left: 0, bottom: 0 }}>
                <CartesianGrid strokeDasharray="3 3" vertical={false} className="stroke-muted/40" />
                <XAxis dataKey="month" className="text-xs" />
                <YAxis
                  className="text-xs"
                  label={{ value: '碳排放量 (tCO₂e)', angle: -90, position: 'insideLeft', offset: 12, className: 'text-[11px] fill-muted-foreground' }}
                />
                <Tooltip
                  content={({ active, payload, label }) => {
                    if (!active || !payload?.length) return null;
                    const d = payload[0].payload as MonthlyCarbonRecord;
                    return (
                      <div className="rounded-lg border bg-background p-3 shadow-md text-xs space-y-1.5">
                        <div className="font-semibold text-foreground">{label} 碳核算</div>
                        <div className="border-t pt-1 space-y-1">
                          <div className="flex justify-between gap-4 text-muted-foreground">
                            <span>范围一 (燃气直接):</span>
                            <span className="tabular-nums text-foreground">{d.scope1} t</span>
                          </div>
                          <div className="flex justify-between gap-4 text-muted-foreground">
                            <span>范围二 (电网电力):</span>
                            <span className="tabular-nums text-foreground">{d.scope2} t</span>
                          </div>
                          <div className="flex justify-between gap-4 text-emerald-600">
                            <span>光伏/绿电抵消:</span>
                            <span className="tabular-nums">-{d.offset} t</span>
                          </div>
                          <div className="border-t pt-1 flex justify-between gap-4 font-semibold text-foreground">
                            <span>净碳排放:</span>
                            <span className="tabular-nums">{d.netTotal} t (限额: {d.budget} t)</span>
                          </div>
                        </div>
                      </div>
                    );
                  }}
                />
                <Legend wrapperStyle={{ fontSize: '12px' }} />
                <Bar dataKey="scope2" name="范围二：外购电网用电" stackId="emission" fill="#3b82f6" />
                <Bar dataKey="scope1" name="范围一：化石燃气直接燃烧" stackId="emission" fill="#f59e0b" />
                <Line type="monotone" dataKey="netTotal" name="净碳排放量" stroke="#10b981" strokeWidth={2.5} dot={{ r: 3 }} />
                <Line type="monotone" dataKey="budget" name="月度配额控制线" stroke="#ef4444" strokeWidth={1.5} strokeDasharray="4 4" dot={false} />
              </ComposedChart>
            </ResponsiveContainer>
          </div>
        </CardContent>
      </Card>

      {/* Factor Provenance & Project Reductions */}
      <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
        {/* Factor Provenance Table */}
        <Card className="shadow-xs">
          <CardHeader className="pb-3">
            <div className="flex items-center justify-between">
              <div>
                <CardTitle className="text-base font-semibold">排放因子依据</CardTitle>
                <CardDescription>
                  依据的官方标准与电网排放因子
                </CardDescription>
              </div>
              <StatusPillBadge tone="info" label="2024基准" />
            </div>
          </CardHeader>
          <CardContent className="p-0">
            <div className="border-t">
              <DataTable
                table={factorTable}
                className="gap-0"
                tableAriaLabel="碳排放因子依据"
                getHeaderRowProps={() => ({ className: 'bg-muted/30 text-xs hover:bg-muted/30' })}
                getHeaderCellProps={(header) => ({ className: header.id === 'validFrom' ? 'text-right' : undefined })}
                getRowProps={() => ({ className: 'text-xs transition-colors hover:bg-muted/40' })}
                getCellProps={(cell) => ({ className: cell.column.id === 'validFrom' ? 'text-right' : undefined })}
              />
            </div>
          </CardContent>
        </Card>

        {/* Verified Reduction Projects */}
        <Card className="shadow-xs">
          <CardHeader className="pb-3">
            <div className="flex items-center justify-between">
              <div>
                <CardTitle className="text-base font-semibold">节能减碳项目贡献</CardTitle>
                <CardDescription>
                  经 M&V 验证的节电量与减碳效果
                </CardDescription>
              </div>
              <StatusPillBadge tone="success" label="3 个实施项目" />
            </div>
          </CardHeader>
          <CardContent className="p-0">
            <div className="border-t">
              <DataTable
                table={reductionTable}
                className="gap-0"
                tableAriaLabel="节能减碳项目贡献"
                getHeaderRowProps={() => ({ className: 'bg-muted/30 text-xs hover:bg-muted/30' })}
                getHeaderCellProps={(header) => ({ className: header.id === 'status' ? 'text-right' : undefined })}
                getRowProps={() => ({ className: 'text-xs transition-colors hover:bg-muted/40' })}
                getCellProps={(cell) => ({ className: cell.column.id === 'status' ? 'text-right' : undefined })}
              />
            </div>
          </CardContent>
        </Card>
      </div>
    </Main>
  );
}
