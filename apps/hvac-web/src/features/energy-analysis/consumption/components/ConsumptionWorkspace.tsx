import { Download, RefreshCw } from "lucide-react";
import { useNavigate, useSearch } from "@tanstack/react-router";
import type { ColumnDef } from "@tanstack/react-table";
import type { ReactNode } from "react";
import { DataTableBlock } from "@/blocks/data-table";
import { PageHeader } from "@/blocks/page-header";
import { MetricTabsChartCard } from "@/components/metric-tabs-chart-card";
import { DataTable } from "@/components/data-table/data-table";
import type { DataTableFeatures } from "@/components/data-table/data-table-features";
import { Main } from "@/components/layout/Main";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from "@/components/ui/card";
import { DeltaBadge } from "@/components/ui/delta-badge";
import { PeriodTabs } from "@/components/ui/period-tabs";
import { Skeleton } from "@/components/ui/skeleton";
import { useDataTable } from "@/hooks/use-data-table";
import { useWorkspaceScope } from "@/hooks/use-scope";
import { formatDecimal, formatTime } from "@/lib/operator-format";
import {
  BUCKET_NOUN,
  ENERGY_PERIODS,
  relativeChange,
  type BucketComparison,
  type EnergyPeriod,
  type EnergySummary,
} from "../model";
import { useEnergyComparison } from "../query";

const PERIOD_OPTIONS = (Object.keys(ENERGY_PERIODS) as EnergyPeriod[]).map((value) => ({ value, label: ENERGY_PERIODS[value] }));

/** What the period is compared with: the same span of the period before. */
const PREVIOUS_LABEL: Record<EnergyPeriod, string> = { today: "昨日同时段", "7d": "前 7 天", month: "上月同期", year: "去年同期" };

type ConsumptionMetric = "electricityKWh" | "coolingKWh" | "cop";

/** Energy in kWh, switching to MWh from 10,000 kWh. */
function energy(value: number | null): { value: string | null; unit: string } {
  if (value === null) return { value: null, unit: "kWh" };
  return value >= 10_000 ? { value: formatDecimal(value / 1000, 2), unit: "MWh" } : { value: formatDecimal(value, 1), unit: "kWh" };
}

const cell = (value: number | null, digits = 1) =>
  value === null ? <span className="text-muted-foreground">—</span> : <span className="tabular-nums">{formatDecimal(value, digits)}</span>;

function changeCell(current: number | null, previous: number | null) {
  const change = relativeChange(current, previous);
  return change === null ? <span className="text-muted-foreground">—</span> : <DeltaBadge delta={change} invert variant="soft" showIcon={false} />;
}

function ledgerColumns(previousLabel: string): ColumnDef<DataTableFeatures, BucketComparison>[] {
  return [
    { id: "period", header: "时段", accessorFn: (row) => row.label },
    { id: "electricity", header: "空调用电（kWh）", cell: ({ row }) => cell(row.original.electricityKWh) },
    { id: "previous", header: `${previousLabel}（kWh）`, cell: ({ row }) => cell(row.original.previousElectricityKWh) },
    { id: "change", header: "用电变化", cell: ({ row }) => changeCell(row.original.electricityKWh, row.original.previousElectricityKWh) },
    { id: "cooling", header: "供冷量（kWh）", cell: ({ row }) => cell(row.original.coolingKWh) },
    { id: "cop", header: "综合能效（COP）", cell: ({ row }) => cell(row.original.cop, 2) },
  ];
}

function Fact({ label, children, detail }: { readonly label: string; readonly children: ReactNode; readonly detail?: ReactNode }) {
  return (
    <div className="flex items-baseline justify-between gap-4 py-2.5">
      <dt className="text-sm text-muted-foreground">{label}</dt>
      <dd className="flex flex-col items-end gap-0.5 text-right">
        <span className="font-medium tabular-nums">{children}</span>
        {detail ? <span className="text-xs text-muted-foreground">{detail}</span> : null}
      </dd>
    </div>
  );
}

function PeriodFacts({ current, previous, previousLabel, timeZone }: {
  readonly current: EnergySummary;
  readonly previous: EnergySummary;
  readonly previousLabel: string;
  readonly timeZone: string;
}) {
  const noun = BUCKET_NOUN[current.granularity];
  const metered = current.rows.filter((row) => row.electricityKWh !== null);
  const average = metered.length === 0 ? null : current.electricityKWh! / metered.length;
  const before = energy(previous.electricityKWh);
  return (
    <Card role="region" aria-label="期间概况">
      <CardHeader>
        <CardTitle>期间概况</CardTitle>
        <CardDescription>按站点当地时间统计</CardDescription>
      </CardHeader>
      <CardContent className="flex-1">
        <dl className="divide-y">
          <Fact label={`用电最高${noun}`} detail={current.peak ? `${formatDecimal(current.peak.electricityKWh!, 1)} kWh` : undefined}>
            {current.peak?.label ?? <span className="text-muted-foreground">—</span>}
          </Fact>
          <Fact label={`平均每${noun}用电`}>
            {average === null ? <span className="text-muted-foreground">—</span> : `${formatDecimal(average, 1)} kWh`}
          </Fact>
          <Fact label={`${previousLabel}用电`}>
            {before.value === null ? <span className="text-muted-foreground">—</span> : `${before.value} ${before.unit}`}
          </Fact>
          <Fact label="计量质量">
            {current.excludedIntervals > 0 ? `${current.excludedIntervals} 个区间未计入` : "全部有效"}
          </Fact>
          <Fact label="电费" detail="电价与结算尚未接入">
            <Badge variant="outline" className="font-normal">未接入</Badge>
          </Fact>
        </dl>
      </CardContent>
      <CardFooter className="border-t text-xs text-muted-foreground" data-testid="energy-freshness">
        {current.dataWatermark ? `数据截至 ${formatTime(current.dataWatermark, timeZone)}` : "本期间还没有计量数据"}
      </CardFooter>
    </Card>
  );
}

export function ConsumptionWorkspace() {
  const { currentScope } = useWorkspaceScope();
  const search = useSearch({ from: "/_app/_site/energy-analysis/consumption" });
  const navigate = useNavigate({ from: "/energy-analysis/consumption" });
  const period: EnergyPeriod = search.period ?? "today";
  const metric: ConsumptionMetric = search.metric ?? "electricityKWh";
  const previousLabel = PREVIOUS_LABEL[period];
  const query = useEnergyComparison(period);
  const comparison = query.data;
  const table = useDataTable({
    key: "energy-consumption-ledger",
    data: comparison?.buckets ?? [],
    columns: ledgerColumns(previousLabel),
    paginate: false,
    getRowId: (row) => row.label,
  });
  const exportCsv = () => {
    if (!comparison) return;
    const lines = [
      [currentScope.name, ENERGY_PERIODS[period], "站点当地时间"],
      ["时段", "空调用电 kWh", `${previousLabel}用电 kWh`, "供冷量 kWh", "综合能效 COP"],
      ...comparison.buckets.map((row) => [row.label, row.electricityKWh ?? "", row.previousElectricityKWh ?? "", row.coolingKWh ?? "", row.cop?.toFixed(2) ?? ""]),
    ];
    const url = URL.createObjectURL(new Blob(["﻿" + lines.map((line) => line.join(",")).join("\n")], { type: "text/csv;charset=utf-8" }));
    const link = document.createElement("a");
    link.href = url;
    link.download = `${currentScope.name}-能耗-${ENERGY_PERIODS[period]}.csv`;
    link.click();
    URL.revokeObjectURL(url);
  };
  const setSearch = (patch: { period?: EnergyPeriod; metric?: ConsumptionMetric }) =>
    void navigate({ search: (previous) => ({ ...previous, ...patch }), replace: true });

  return (
    <Main className="@container/main flex flex-col gap-4 md:gap-6">
      <PageHeader
        title="能耗与成本"
        description={`${currentScope.name} · 本期与${previousLabel}对比`}
        actions={
          <>
            <PeriodTabs aria-label="统计期间" options={PERIOD_OPTIONS} value={period} onValueChange={(value) => setSearch({ period: value as EnergyPeriod })} />
            <Button variant="outline" size="icon" aria-label="刷新能耗" onClick={() => void query.refetch()}>
              <RefreshCw />
            </Button>
            <Button variant="outline" disabled={!comparison?.buckets.length} onClick={exportCsv}>
              <Download aria-hidden="true" data-icon="inline-start" />
              导出明细
            </Button>
          </>
        }
      />
      {query.isPending ? (
        <Skeleton className="h-[420px] rounded-xl" />
      ) : query.isError || !comparison ? (
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
          <div className="grid gap-4 md:gap-6 @5xl/main:grid-cols-3">
            <div className="min-w-0 @5xl/main:col-span-2">
              <PeriodComparison
                current={comparison.current}
                previous={comparison.previous}
                buckets={comparison.buckets}
                previousLabel={previousLabel}
                metric={metric}
                onMetricChange={(next) => setSearch({ metric: next })}
              />
            </div>
            <PeriodFacts current={comparison.current} previous={comparison.previous} previousLabel={previousLabel} timeZone={currentScope.timezone} />
          </div>
          <DataTableBlock title={`分${BUCKET_NOUN[comparison.current.granularity]}能耗`} description={`本期每${BUCKET_NOUN[comparison.current.granularity]}的空调用电、${previousLabel}用电、供冷量与综合能效。`}>
            <DataTable table={table} tableAriaLabel="分时段能耗明细" />
          </DataTableBlock>
        </>
      )}
    </Main>
  );
}

function PeriodComparison({ current, previous, buckets, previousLabel, metric, onMetricChange }: {
  readonly current: EnergySummary;
  readonly previous: EnergySummary;
  readonly buckets: readonly BucketComparison[];
  readonly previousLabel: string;
  readonly metric: ConsumptionMetric;
  readonly onMetricChange: (metric: ConsumptionMetric) => void;
}) {
  const electricity = energy(current.electricityKWh);
  const cooling = energy(current.coolingKWh);
  return (
    <MetricTabsChartCard
      role="region"
      aria-label="期间用能"
      className="h-full"
      // Both series cover the same span, so the running bucket is partial on both sides.
      data={buckets.filter((bucket) => bucket.complete).map((bucket) => ({ ...bucket }))}
      xKey="label"
      selected={metric}
      onSelectedChange={(key) => onMetricChange(key as ConsumptionMetric)}
      color="var(--chart-1)"
      compareColor="var(--chart-5)"
      compareLabel={previousLabel}
      showYAxis
      showLegend
      dots
      chartClassName="h-[300px]"
      empty="本期间还没有计量数据"
      metrics={[
        {
          key: "electricityKWh",
          label: "空调用电",
          compareKey: "previousElectricityKWh",
          value: electricity.value,
          unit: electricity.unit,
          fractionDigits: 1,
          delta: relativeChange(current.electricityKWh, previous.electricityKWh),
          invertDelta: true,
        },
        {
          key: "coolingKWh",
          label: "供冷量",
          compareKey: "previousCoolingKWh",
          value: cooling.value,
          unit: cooling.unit,
          fractionDigits: 1,
          delta: relativeChange(current.coolingKWh, previous.coolingKWh),
          neutralDelta: true,
        },
        {
          key: "cop",
          label: "冷站综合能效",
          compareKey: "previousCop",
          value: current.cop === null ? null : formatDecimal(current.cop, 2),
          unit: "COP",
          fractionDigits: 2,
          delta: relativeChange(current.cop, previous.cop),
        },
      ]}
    />
  );
}
