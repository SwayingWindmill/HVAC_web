import { useState, useMemo } from 'react';
import type { ColumnDef } from '@tanstack/react-table';
import {
  TrendingUp,
  CheckCircle2,
  AlertTriangle,
  RotateCcw,
  Download,
  ShieldCheck,
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
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet';

export interface MVMonthlyRecord {
  readonly month: string;
  readonly actualKWh: number;
  readonly adjustedBaselineKWh: number;
  readonly savedKWh: number;
  readonly savedCostRmb: number;
  readonly carbonSavedTon: number;
  readonly cdd: number; // Cooling Degree Days
  readonly snapbackRisk: 'normal' | 'slight_decay' | 'alert';
}

const MOCK_MV_DATA: readonly MVMonthlyRecord[] = [
  { month: '2026-05', actualKWh: 88500, adjustedBaselineKWh: 122000, savedKWh: 33500, savedCostRmb: 27800, carbonSavedTon: 17.9, cdd: 85, snapbackRisk: 'normal' },
  { month: '2026-06', actualKWh: 142000, adjustedBaselineKWh: 215000, savedKWh: 73000, savedCostRmb: 61200, carbonSavedTon: 39.0, cdd: 180, snapbackRisk: 'normal' },
  { month: '2026-07', actualKWh: 198000, adjustedBaselineKWh: 295000, savedKWh: 97000, savedCostRmb: 82500, carbonSavedTon: 51.8, cdd: 260, snapbackRisk: 'normal' },
  { month: '2026-08', actualKWh: 215000, adjustedBaselineKWh: 318000, savedKWh: 103000, savedCostRmb: 88100, carbonSavedTon: 55.0, cdd: 285, snapbackRisk: 'normal' },
  { month: '2026-09 (至今)', actualKWh: 154000, adjustedBaselineKWh: 231700, savedKWh: 77700, savedCostRmb: 61400, carbonSavedTon: 41.5, cdd: 195, snapbackRisk: 'normal' },
];

export function MeasurementVerificationWorkspace({ siteId: _siteId }: { readonly siteId?: string }) {
  const [selectedRecord, setSelectedRecord] = useState<MVMonthlyRecord | null>(null);
  const [viewFilter, setViewFilter] = useState<string>('ALL');
  const viewPills: readonly DataTableViewPillOption[] = [
    { key: 'ALL', label: '全部月份', count: MOCK_MV_DATA.length },
    { key: 'SUMMER', label: '盛夏高峰期 (7-8月)', count: 2 },
    { key: 'TRANSITION', label: '过渡季节', count: 3 },
  ];

  const filteredData = useMemo(() => {
    if (viewFilter === 'SUMMER') {
      return MOCK_MV_DATA.filter((d) => d.month.includes('07') || d.month.includes('08'));
    }
    if (viewFilter === 'TRANSITION') {
      return MOCK_MV_DATA.filter((d) => !d.month.includes('07') && !d.month.includes('08'));
    }
    return MOCK_MV_DATA;
  }, [viewFilter]);

  const totalSavedKWh = useMemo(() => {
    return MOCK_MV_DATA.reduce((acc, curr) => acc + curr.savedKWh, 0);
  }, []);

  const totalSavedCost = useMemo(() => {
    return MOCK_MV_DATA.reduce((acc, curr) => acc + curr.savedCostRmb, 0);
  }, []);

  const totalCarbonSaved = useMemo(() => {
    return MOCK_MV_DATA.reduce((acc, curr) => acc + curr.carbonSavedTon, 0);
  }, []);

  const columns = useMemo<Array<ColumnDef<DataTableFeatures, MVMonthlyRecord>>>(() => [
    {
      id: 'select',
      header: ({ table }) => (
        <Checkbox
          checked={table.getIsAllPageRowsSelected() ? true : table.getIsSomePageRowsSelected() ? 'indeterminate' : false}
          onCheckedChange={(checked) => table.toggleAllPageRowsSelected(Boolean(checked))}
          aria-label="全选月度核验记录"
        />
      ),
      cell: ({ row }) => (
        <Checkbox
          checked={row.getIsSelected()}
          onCheckedChange={(checked) => row.toggleSelected(Boolean(checked))}
          aria-label={`选择 ${row.original.month}`}
          onClick={(event) => event.stopPropagation()}
        />
      ),
      enableSorting: false,
      enableHiding: false,
    },
    { id: 'month', header: '核算月份', cell: ({ row }) => <span className="font-mono font-medium text-foreground">{row.original.month}</span> },
    { id: 'cdd', header: '自变量 CDD', cell: ({ row }) => <span className="font-mono text-muted-foreground tabular-nums">{row.original.cdd} °C·d</span> },
    { id: 'baseline', header: '天气校正基准能耗', cell: ({ row }) => <span className="font-mono text-muted-foreground tabular-nums">{row.original.adjustedBaselineKWh.toLocaleString()} kWh</span> },
    { id: 'actual', header: '实测能耗', cell: ({ row }) => <span className="font-mono font-semibold text-foreground tabular-nums">{row.original.actualKWh.toLocaleString()} kWh</span> },
    { id: 'savedKWh', header: '核验净节电量', cell: ({ row }) => <span className="font-mono font-semibold text-emerald-600 tabular-nums">+{row.original.savedKWh.toLocaleString()} kWh</span> },
    { id: 'savedCost', header: '核验节电费 (元)', cell: ({ row }) => <span className="font-mono text-foreground tabular-nums">¥{row.original.savedCostRmb.toLocaleString()}</span> },
    { id: 'carbon', header: '减碳效益 (tCO₂e)', cell: ({ row }) => <span className="font-mono text-muted-foreground tabular-nums">{row.original.carbonSavedTon.toFixed(1)} t</span> },
    {
      id: 'snapback',
      header: '节能持久性',
      cell: ({ row }) => (
        <StatusPillBadge
          tone={row.original.snapbackRisk === 'alert' ? 'destructive' : row.original.snapbackRisk === 'slight_decay' ? 'warning' : 'success'}
          pulse={row.original.snapbackRisk === 'alert'}
          label={row.original.snapbackRisk === 'alert' ? '告警回弹' : row.original.snapbackRisk === 'slight_decay' ? '轻微衰减' : '正常持续'}
        />
      ),
    },
    {
      id: 'actions',
      header: '操作/凭证',
      cell: ({ row }) => (
        <Button
          variant="ghost"
          size="sm"
          className="h-7 px-2 text-xs"
          onClick={(event) => {
            event.stopPropagation();
            setSelectedRecord(row.original);
          }}
        >
          审计凭据
        </Button>
      ),
      enableSorting: false,
    },
  ], []);

  const table = useDataTable({
    key: 'surface-27-measurement-verification',
    data: [...filteredData],
    columns,
    pageSize: 10,
    getRowId: (row) => row.month,
  });

  return (
    <Main className="space-y-6">
      {/* Header */}
      <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <div className="flex items-center gap-2">
            <h1 className="text-2xl font-bold tracking-tight text-foreground">节能量核验</h1>
            <Badge variant="outline" className="text-xs font-normal">节能量核验</Badge>
          </div>
          <p className="text-sm text-muted-foreground">
            对比基准用电量与实际计量，核算节能改造与优化运行后的实际节电量与节费金额
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" className="h-9 gap-1.5" onClick={() => window.location.reload()}>
            <RotateCcw className="h-4 w-4" />
            刷新核算
          </Button>
          <Button size="sm" className="h-9 gap-1.5">
            <Download className="h-4 w-4" />
            导出节能量报告
          </Button>
        </div>
      </div>

      {/* 4-Card Fact Strip */}
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">累计核验节电量</CardTitle>
            <TrendingUp className="size-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold tracking-tight text-foreground tabular-nums">
              {(totalSavedKWh / 1000).toFixed(1)} <span className="text-xs font-normal text-muted-foreground">MWh</span>
            </div>
            <p className="mt-1 text-xs text-muted-foreground">
              较气象校正基准能耗下降 32.4%
            </p>
          </CardContent>
        </Card>

        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">累计核验节约电费</CardTitle>
            <CheckCircle2 className="size-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold tracking-tight text-foreground tabular-nums">
              ¥{(totalSavedCost / 10000).toFixed(2)} <span className="text-xs font-normal text-muted-foreground">万元</span>
            </div>
            <p className="mt-1 text-xs text-muted-foreground">
              累计减碳 {totalCarbonSaved.toFixed(1)} 吨 CO₂e
            </p>
          </CardContent>
        </Card>

        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">基线模型质量 (ASHRAE 14)</CardTitle>
            <ShieldCheck className="size-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold tracking-tight text-foreground tabular-nums">
              R² = 0.942
            </div>
            <p className="mt-1 text-xs text-muted-foreground">
              CV-RMSE: 8.4% · NMBE: +1.2%
            </p>
          </CardContent>
        </Card>

        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">能效衰减与持久性 (Snapback)</CardTitle>
            <AlertTriangle className="size-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold tracking-tight text-foreground tabular-nums">
              0 <span className="text-xs font-normal text-muted-foreground">项反弹</span>
            </div>
            <p className="mt-1 text-xs text-muted-foreground">
              近 5 个月节能量稳态保持，未发生回退
            </p>
          </CardContent>
        </Card>
      </div>

      {/* Project Selector & Protocol Details */}
      <Card className="shadow-xs">
        <CardHeader className="pb-3">
          <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
            <div>
              <CardTitle className="text-base font-semibold">项目 M&V 规程与基准模型参数</CardTitle>
              <CardDescription>
                当前项目：2026 制冷机房综合能效智控升级工程 (IPMVP Option C: Whole Facility)
              </CardDescription>
            </div>
            <div className="flex items-center gap-2">
              <Badge variant="secondary" className="tabular-nums text-xs">基准期: 2025-05 ~ 2025-09</Badge>
              <Badge variant="outline" className="tabular-nums text-xs">报告期: 2026-05 ~ 至今</Badge>
            </div>
          </div>
        </CardHeader>
        <CardContent>
          <div className="grid grid-cols-1 gap-4 md:grid-cols-3">
            <div className="rounded-lg border bg-muted/20 p-3 text-xs space-y-1">
              <div className="font-semibold text-foreground">基线多元回归模型方程 (EnB)</div>
              <div className="tabular-nums text-primary font-medium py-1">
                E = 142.3 &times; CDD + 28.5 &times; RH + 850
              </div>
              <p className="text-muted-foreground">
                自变量为室外制冷度日 (CDD) 与平均相对湿度 (RH)。
              </p>
            </div>

            <div className="rounded-lg border bg-muted/20 p-3 text-xs space-y-1">
              <div className="font-semibold text-foreground">常规与非常规调整机制</div>
              <div className="text-foreground">已校正 2026 年夏季极端高温（CDD 偏高 14.2%）</div>
              <p className="text-muted-foreground">
                剔除 6 月 12 日变压器春检计划停电 8 小时非正常运行区间。
              </p>
            </div>

            <div className="rounded-lg border bg-muted/20 p-3 text-xs space-y-1">
              <div className="font-semibold text-foreground">核验机构与认证资质</div>
              <div className="text-foreground">CMVP 认证节能量验证工程师已签字核验</div>
              <p className="text-muted-foreground">
                符合《节能量测量和验证技术通则 GB/T 28750-2012》。
              </p>
            </div>
          </div>
        </CardContent>
      </Card>

      {/* Main Composed Chart: Adjusted Baseline vs Actual Consumption */}
      <Card className="shadow-xs">
        <CardHeader className="pb-2">
          <CardTitle className="text-base font-semibold">天气校正基准能耗 vs 实测能耗与净节电量趋势</CardTitle>
          <CardDescription>
            对比同一气象负荷条件下未改造基线模拟能耗与改造后实测用电量。
          </CardDescription>
        </CardHeader>
        <CardContent>
          <div className="h-[320px] w-full pt-2">
            <ResponsiveContainer width="100%" height="100%">
              <ComposedChart data={MOCK_MV_DATA as unknown as Record<string, unknown>[]} margin={{ top: 10, right: 20, left: 0, bottom: 0 }}>
                <CartesianGrid strokeDasharray="3 3" vertical={false} className="stroke-muted/40" />
                <XAxis dataKey="month" className="text-xs" />
                <YAxis
                  yAxisId="kwh"
                  className="text-xs"
                  tickFormatter={(val) => `${(val / 1000).toFixed(0)}k`}
                  label={{ value: '能耗 (kWh)', angle: -90, position: 'insideLeft', offset: 12, className: 'text-[11px] fill-muted-foreground' }}
                />
                <YAxis
                  yAxisId="saved"
                  orientation="right"
                  className="text-xs"
                  tickFormatter={(val) => `${(val / 1000).toFixed(0)}k`}
                  label={{ value: '净节电量 (kWh)', angle: 90, position: 'insideRight', offset: 12, className: 'text-[11px] fill-muted-foreground' }}
                />
                <Tooltip
                  content={({ active, payload, label }) => {
                    if (!active || !payload?.length) return null;
                    const d = payload[0].payload as MVMonthlyRecord;
                    return (
                      <div className="rounded-lg border bg-background p-3 shadow-md text-xs space-y-1.5">
                        <div className="font-semibold text-foreground">{label}</div>
                        <div className="text-muted-foreground">制冷度日 (CDD): {d.cdd} °C·d</div>
                        <div className="border-t pt-1 space-y-1">
                          <div className="text-muted-foreground flex justify-between gap-4">
                            <span>校正基线:</span>
                            <span className="font-mono font-medium text-foreground">{d.adjustedBaselineKWh.toLocaleString()} kWh</span>
                          </div>
                          <div className="text-muted-foreground flex justify-between gap-4">
                            <span>实际用电:</span>
                            <span className="font-mono font-medium text-foreground">{d.actualKWh.toLocaleString()} kWh</span>
                          </div>
                          <div className="text-emerald-600 font-semibold flex justify-between gap-4">
                            <span>核验净节约:</span>
                            <span className="font-mono">+{d.savedKWh.toLocaleString()} kWh (¥{d.savedCostRmb.toLocaleString()})</span>
                          </div>
                        </div>
                      </div>
                    );
                  }}
                />
                <Legend wrapperStyle={{ fontSize: '12px' }} />
                <Bar yAxisId="kwh" dataKey="adjustedBaselineKWh" name="校正基线能耗" fill="#94a3b8" radius={[4, 4, 0, 0]} opacity={0.6} />
                <Bar yAxisId="kwh" dataKey="actualKWh" name="实测能耗" fill="#3b82f6" radius={[4, 4, 0, 0]} />
                <Line yAxisId="saved" type="monotone" dataKey="savedKWh" name="核验节电量" stroke="#10b981" strokeWidth={2.5} dot={{ r: 4 }} />
              </ComposedChart>
            </ResponsiveContainer>
          </div>
        </CardContent>
      </Card>

      {/* Monthly Verified Ledger */}
      <Card className="shadow-xs">
        <CardHeader className="pb-3">
          <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
            <div>
              <CardTitle className="text-base font-semibold">月度节能量核算台账 (Monthly Verification Ledger)</CardTitle>
              <CardDescription>
                经气象变量回归校正后的官方审计节能量明细。
              </CardDescription>
            </div>
            <div className="flex flex-wrap items-center gap-2">
              <DataTableViewPills
                options={viewPills}
                value={viewFilter}
                onValueChange={(value) => {
                  setViewFilter(value);
                  table.setPageIndex(0);
                }}
              />
              <DataTableViewOptions table={table} />
            </div>
          </div>
        </CardHeader>
        <CardContent className="p-0">
          <DataTable
            table={table}
            className="gap-0 border-t"
            tableAriaLabel="月度节能量核算台账"
            getHeaderRowProps={() => ({ className: 'bg-muted/30 text-xs hover:bg-muted/30' })}
            getHeaderCellProps={(header) => ({
              className:
                header.id === 'select' ? 'w-10' :
                header.id === 'month' ? 'w-[120px]' :
                header.id === 'actions' ? 'text-right' :
                undefined,
            })}
            getRowProps={(row) => ({
              className: 'cursor-pointer text-xs transition-colors hover:bg-muted/40',
              onClick: () => setSelectedRecord(row.original),
            })}
            getCellProps={(cell) => ({
              className: cell.column.id === 'actions' ? 'text-right' : undefined,
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

      {/* Record Inspector Sheet */}
      <Sheet open={Boolean(selectedRecord)} onOpenChange={(open) => !open && setSelectedRecord(null)}>
        <SheetContent className="sm:max-w-md w-full">
          {selectedRecord && (
            <div className="space-y-6 py-2">
              <SheetHeader className="space-y-2 border-b pb-4 text-left">
                <SheetTitle className="text-lg font-bold text-foreground">
                  {selectedRecord.month} 节电量核验明细
                </SheetTitle>
                <SheetDescription className="text-xs text-muted-foreground">
                  对比气象校正基准用电量计算
                </SheetDescription>
              </SheetHeader>

              <div className="space-y-3 rounded-lg border bg-muted/20 p-4 text-xs space-y-2">
                <div className="flex justify-between">
                  <span className="text-muted-foreground">实际用电计量：</span>
                  <span className="tabular-nums font-bold text-foreground">{selectedRecord.actualKWh.toLocaleString()} kWh</span>
                </div>
                <div className="flex justify-between">
                  <span className="text-muted-foreground">气象校准基线能耗：</span>
                  <span className="tabular-nums text-foreground">{selectedRecord.adjustedBaselineKWh.toLocaleString()} kWh</span>
                </div>
                <div className="border-t pt-2 flex justify-between text-emerald-600 font-bold">
                  <span>核验节电量：</span>
                  <span className="tabular-nums">+{selectedRecord.savedKWh.toLocaleString()} kWh</span>
                </div>
                <div className="flex justify-between">
                  <span className="text-muted-foreground">核验节电费：</span>
                  <span className="tabular-nums font-medium text-foreground">¥{selectedRecord.savedCostRmb.toLocaleString()}</span>
                </div>
                <div className="flex justify-between">
                  <span className="text-muted-foreground">二氧化碳减排量：</span>
                  <span className="tabular-nums text-foreground">{selectedRecord.carbonSavedTon} 吨</span>
                </div>
              </div>

              <div className="rounded-lg border p-4 text-xs space-y-2">
                <div className="font-semibold text-foreground">不确定度与置信区间</div>
                <p className="text-muted-foreground">
                  在 90% 置信水平下，本月核验节电量相对精度为 &plusmn;4.1%，符合节能减排国家标准。
                </p>
              </div>

              <div className="border-t pt-4 flex justify-end">
                <Button variant="outline" size="sm" onClick={() => setSelectedRecord(null)}>
                  关闭
                </Button>
              </div>
            </div>
          )}
        </SheetContent>
      </Sheet>
    </Main>
  );
}
