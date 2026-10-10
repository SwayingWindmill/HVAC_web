import { Download, RefreshCw } from "lucide-react";
import { useNavigate, useSearch } from "@tanstack/react-router";
import type { ColumnDef } from "@tanstack/react-table";
import { Bar, BarChart, CartesianGrid, Line, LineChart, XAxis, YAxis } from "recharts";
import { DataTableBlock } from "@/blocks/data-table";
import { PageHeader } from "@/blocks/page-header";
import { FactStrip } from "@/blocks/fact-strip";
import { DataTable } from "@/components/data-table/data-table";
import type { DataTableFeatures } from "@/components/data-table/data-table-features";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import {
  ChartContainer,
  ChartLegend,
  ChartLegendContent,
  ChartTooltip,
  ChartTooltipContent,
} from "@/components/ui/chart";
import { Main } from "@/components/layout/Main";
import { Skeleton } from "@/components/ui/skeleton";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { useDataTable } from "@/hooks/use-data-table";
import { useWorkspaceScope } from "@/hooks/use-scope";
import { formatTime } from "@/lib/operator-format";
import { BUCKET_NOUN, ENERGY_PERIODS, type EnergyPeriod, type EnergyRow } from "../model";
import { useEnergySummary } from "../query";

const decimal = (value: number, digits: number) =>
  value.toLocaleString("zh-CN", { minimumFractionDigits: digits, maximumFractionDigits: digits });

/** Energy in kWh, switching to MWh from 10,000 kWh. */
function energy(value: number | null): { value: string; unit: string } {
  if (value === null) return { value: "暂无数据", unit: "" };
  return value >= 10_000 ? { value: decimal(value / 1000, 2), unit: "MWh" } : { value: decimal(value, 1), unit: "kWh" };
}

const cell = (value: number | null, digits = 1) =>
  value === null ? <span className="text-muted-foreground">—</span> : <span className="tabular-nums">{decimal(value, digits)}</span>;

const columns: ColumnDef<DataTableFeatures, EnergyRow>[] = [
  { id: "period", header: "时段", accessorFn: (row) => row.label },
  { id: "electricity", header: "空调用电（kWh）", cell: ({ row }) => cell(row.original.electricityKWh) },
  { id: "cooling", header: "供冷量（kWh）", cell: ({ row }) => cell(row.original.coolingKWh) },
  { id: "cop", header: "综合能效（COP）", cell: ({ row }) => cell(row.original.cop, 2) },
];

const chartConfig = {
  electricityKWh: { label: "空调用电", color: "var(--chart-1)" },
  coolingKWh: { label: "供冷量", color: "var(--chart-2)" },
  cop: { label: "综合能效", color: "var(--chart-1)" },
};

export function ConsumptionWorkspace() {
  const { currentScope } = useWorkspaceScope();
  const search = useSearch({ from: "/_app/_site/energy-analysis/consumption" });
  const navigate = useNavigate({ from: "/energy-analysis/consumption" });
  const period: EnergyPeriod = search.period ?? "today";
  const query = useEnergySummary(period);
  const summary = query.data;
  const table = useDataTable({
    key: "energy-consumption-ledger",
    data: [...(summary?.rows ?? [])],
    columns,
    paginate: false,
    getRowId: (row) => row.periodStart,
  });
  const exportCsv = () => {
    if (!summary) return;
    const lines = [
      [currentScope.name, ENERGY_PERIODS[period], "站点当地时间"],
      ["时段", "空调用电 kWh", "供冷量 kWh", "综合能效 COP"],
      ...summary.rows.map((row) => [row.label, row.electricityKWh ?? "", row.coolingKWh ?? "", row.cop?.toFixed(2) ?? ""]),
    ];
    const url = URL.createObjectURL(new Blob(["\uFEFF" + lines.map((line) => line.join(",")).join("\n")], { type: "text/csv;charset=utf-8" }));
    const link = document.createElement("a");
    link.href = url;
    link.download = `${currentScope.name}-能耗-${ENERGY_PERIODS[period]}.csv`;
    link.click();
    URL.revokeObjectURL(url);
  };
  const electricity = energy(summary?.electricityKWh ?? null);
  const cooling = energy(summary?.coolingKWh ?? null);
  const peak = summary?.peak;
  return (
    <Main className="space-y-5">
      <PageHeader
        title="能耗与成本"
        description={`${currentScope.name} · 按站点当地时间统计`}
        actions={
          <>
          <ToggleGroup
            type="single"
            variant="outline"
            value={period}
            onValueChange={(value) => {
              if (value) void navigate({ search: (previous) => ({ ...previous, period: value as EnergyPeriod }) });
            }}
            aria-label="统计期间"
          >
            {(Object.keys(ENERGY_PERIODS) as EnergyPeriod[]).map((key) => (
              <ToggleGroupItem key={key} value={key} className="px-3">
                {ENERGY_PERIODS[key]}
              </ToggleGroupItem>
            ))}
          </ToggleGroup>
          <Button variant="outline" size="icon" aria-label="刷新能耗" onClick={() => void query.refetch()}>
            <RefreshCw />
          </Button>
          <Button variant="outline" disabled={!summary?.rows.length} onClick={exportCsv}>
            <Download aria-hidden="true" data-icon="inline-start" />
            导出明细
          </Button>
          </>
        }
      />
      {query.isPending ? (
        <Skeleton className="h-[480px]" />
      ) : query.isError || !summary ? (
        <Alert variant="destructive">
          <AlertTitle>能耗数据暂不可用</AlertTitle>
          <AlertDescription>
            能耗分析服务没有返回本期间的数据，请稍后重试。
            <Button variant="link" className="h-auto p-0" onClick={() => void query.refetch()}>
              重试
            </Button>
          </AlertDescription>
        </Alert>
      ) : (
        <>
          <p className="text-xs text-muted-foreground" data-testid="energy-freshness">
            {summary.dataWatermark ? `数据截至 ${formatTime(summary.dataWatermark, currentScope.timezone)}` : "本期间还没有计量数据"}
            {summary.excludedIntervals > 0 ? ` · ${summary.excludedIntervals} 个质量存疑或无效的计量区间未计入` : ""}
          </p>
          <FactStrip
            ariaLabel="期间用能概况"
            items={[
              { key: "electricity", label: "空调用电", value: electricity.value, suffix: electricity.unit, detail: "空调总电表累计" },
              { key: "cooling", label: "供冷量", value: cooling.value, suffix: cooling.unit, detail: "冷量表累计" },
              {
                key: "cop",
                label: "冷站综合能效",
                value: summary.cop === null ? "暂无数据" : decimal(summary.cop, 2),
                suffix: summary.cop === null ? undefined : "COP",
                detail: "供冷量 ÷ 空调用电",
              },
              {
                key: "peak",
                label: `用电最高${BUCKET_NOUN[summary.granularity]}`,
                value: peak ? peak.label : "暂无数据",
                detail: peak ? `${decimal(peak.electricityKWh!, 1)} kWh` : undefined,
              },
              { key: "cost", label: "电费", value: "未接入", detail: "电价与结算尚未接入" },
            ]}
          />
          {summary.rows.length === 0 ? (
            <p className="rounded-lg border py-16 text-center text-sm text-muted-foreground">本期间还没有能耗数据</p>
          ) : (
            <div className="grid gap-5 xl:grid-cols-[2fr_1fr]">
              <Card>
                <CardHeader>
                  <CardTitle>用电与供冷</CardTitle>
                  <CardDescription>每{BUCKET_NOUN[summary.granularity]}能量（kWh）</CardDescription>
                </CardHeader>
                <CardContent>
                  <ChartContainer className="aspect-auto h-[280px] w-full" config={chartConfig}>
                    <BarChart accessibilityLayer data={[...summary.rows]} margin={{ left: 4, right: 4 }}>
                      <CartesianGrid vertical={false} />
                      <XAxis dataKey="label" axisLine={false} tickLine={false} minTickGap={24} />
                      <YAxis axisLine={false} tickLine={false} width={56} tickFormatter={(value: number) => value.toLocaleString("zh-CN")} />
                      <ChartTooltip content={<ChartTooltipContent />} />
                      <Bar dataKey="electricityKWh" fill="var(--color-electricityKWh)" radius={[3, 3, 0, 0]} maxBarSize={40} isAnimationActive={false} />
                      <Bar dataKey="coolingKWh" fill="var(--color-coolingKWh)" radius={[3, 3, 0, 0]} maxBarSize={40} isAnimationActive={false} />
                      <ChartLegend content={<ChartLegendContent />} />
                    </BarChart>
                  </ChartContainer>
                </CardContent>
              </Card>
              <Card>
                <CardHeader>
                  <CardTitle>综合能效</CardTitle>
                  <CardDescription>每{BUCKET_NOUN[summary.granularity]}供冷量 ÷ 空调用电（COP）</CardDescription>
                </CardHeader>
                <CardContent>
                  <ChartContainer className="aspect-auto h-[280px] w-full" config={chartConfig}>
                    <LineChart accessibilityLayer data={[...summary.rows]} margin={{ left: 4, right: 12 }}>
                      <CartesianGrid vertical={false} />
                      <XAxis dataKey="label" axisLine={false} tickLine={false} minTickGap={24} />
                      <YAxis axisLine={false} tickLine={false} width={32} domain={[0, "auto"]} />
                      <ChartTooltip content={<ChartTooltipContent />} />
                      <Line dataKey="cop" stroke="var(--color-cop)" strokeWidth={2} dot={summary.rows.length === 1} connectNulls={false} isAnimationActive={false} />
                    </LineChart>
                  </ChartContainer>
                </CardContent>
              </Card>
            </div>
          )}
          <DataTableBlock title="时段能耗" description="每个时段的空调用电、供冷量与综合能效。">
            <DataTable table={table} tableAriaLabel="分时段能耗明细" />
          </DataTableBlock>
        </>
      )}
    </Main>
  );
}
