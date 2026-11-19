import { useQuery, useQueryClient } from "@tanstack/react-query";
import { getRouteApi, useNavigate, useSearch } from "@tanstack/react-router";
import { Download, ReceiptText, RefreshCw, Sparkles, TriangleAlert } from "lucide-react";
import { useMemo, useState } from "react";
import type { SiteTariff } from "@/api/generated/platformGateway.gen";
import { PageHeader } from "@/blocks/page-header";
import { Panel } from "@/blocks/panel";
import { InfoTip } from "@/components/info-tip";
import { MetricTabsChartCard, type MetricTab } from "@/components/metric-tabs-chart-card";
import { Main } from "@/components/layout/Main";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { DeltaBadge } from "@/components/ui/delta-badge";
import { PeriodTabs, type PeriodOption } from "@/components/ui/period-tabs";
import { Skeleton } from "@/components/ui/skeleton";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { CategoryTrend, destinationCsv, EnergyDestination } from "@/features/breakdown/components/EnergyDestination";
import { useAssistantPanel } from "@/features/assistant/assistant-panel-store";
import { useEnergyBreakdown } from "@/features/breakdown/query";
import { DataGapBanner } from "@/features/data-gaps/DataGapBanner";
import { dataGapsKeys } from "@/features/data-gaps/query";
import { TouCostDetail } from "@/features/tariff/components/TouCostDetail";
import { siteTariffQuery } from "@/features/tariff/query";
import {
  PERIOD_LABEL,
  PERIOD_ORDER,
  sumCosts,
  type PeriodCode,
  type SiteTariffWithCalendar,
  type TouCost,
} from "@/features/tariff/tariff-model";
import { formatDecimal, formatTime } from "@/lib/operator-format";
import { isOneOf } from "@/lib/typed";
import { cn } from "@/lib/utils";
import {
  copUnavailableText,
  ENERGY_PERIODS,
  explainQuestion,
  periodWindow,
  relativeChange,
  summaryCostBuckets,
  wallTime,
  type BucketComparison,
  type EnergyPeriod,
  type EnergySummary,
  type Granularity,
} from "../model";
import { useEnergyComparison } from "../query";

const siteRoute = getRouteApi("/_app/_site");

const ENERGY_PERIOD_KEYS = ["today", "7d", "month", "year"] as const satisfies readonly EnergyPeriod[];
const PERIOD_OPTIONS: readonly PeriodOption<EnergyPeriod>[] = ENERGY_PERIOD_KEYS.map((value) => ({ value, label: ENERGY_PERIODS[value] }));

/** What the period is compared with: the same span of the period before. */
const PREVIOUS_LABEL: Record<EnergyPeriod, string> = { today: "昨日同时段", "7d": "前 7 天", month: "上月同期", year: "去年同期" };
const BUCKET_HEADER: Record<Granularity, string> = { hour: "时段", day: "日期", month: "月份" };

type ConsumptionMetric = "electricityKWh" | "coolingKWh" | "cop" | "cost";
type ConsumptionView = "trend" | "tou" | "detail";
const CONSUMPTION_VIEWS = ["trend", "tou", "detail"] as const satisfies readonly ConsumptionView[];

const GRANULARITY_COPY: Readonly<Record<Granularity, {
  readonly detailTitle: string;
  readonly peakLabel: string;
  readonly averageLabel: string;
  readonly incompleteScope: string;
  readonly crossGapNote: string;
}>> = {
  hour: {
    detailTitle: "逐时明细",
    peakLabel: "用电最高时段",
    averageLabel: "每小时平均",
    incompleteScope: "所选时段内部分小时",
    crossGapNote: "期间内另有跨缺口恢复电量已计入上方总量与分时电费，不摊入逐时明细",
  },
  day: {
    detailTitle: "每日明细",
    peakLabel: "最高用电日",
    averageLabel: "日均用电",
    incompleteScope: "所选时段内部分日期",
    crossGapNote: "期间内另有跨缺口恢复电量已计入上方总量与分时电费，不摊入每日明细",
  },
  month: {
    detailTitle: "每月明细",
    peakLabel: "最高用电月",
    averageLabel: "月均用电",
    incompleteScope: "所选时段内部分月份",
    crossGapNote: "",
  },
};

const CHART_TITLE: Readonly<Record<Granularity, Readonly<Record<ConsumptionMetric, string>>>> = {
  hour: {
    electricityKWh: "每小时空调用电",
    coolingKWh: "每小时供冷量",
    cop: "逐时综合能效走势",
    cost: "每小时估算电费",
  },
  day: {
    electricityKWh: "每日空调用电",
    coolingKWh: "每日供冷量",
    cop: "每日综合能效走势",
    cost: "每日估算电费",
  },
  month: {
    electricityKWh: "每月空调用电",
    coolingKWh: "每月供冷量",
    cop: "每月综合能效走势",
    cost: "每月估算电费",
  },
};

/** Energy in kWh, or in MWh once `scale` (the largest figure shown beside it) reaches 10,000 kWh. */
function energy(value: number | null, scale = value ?? 0): { value: string | null; unit: string } {
  if (value === null) return { value: null, unit: "kWh" };
  return scale >= 10_000 ? { value: formatDecimal(value / 1000, 2), unit: "MWh" } : { value: formatDecimal(value, 1), unit: "kWh" };
}

const dash = <span className="text-muted-foreground">—</span>;
const number = (value: number | null | undefined, digits = 1) => (value === null || value === undefined ? dash : <span className="tabular-nums">{formatDecimal(value, digits)}</span>);

function rangeLabel(period: EnergyPeriod, timeZone: string): string {
  const now = new Date();
  const { from } = periodWindow(period, now, timeZone);
  const day = (instant: Date) => { const local = wallTime(instant, timeZone); return `${String(local.month).padStart(2, "0")}/${String(local.day).padStart(2, "0")}`; };
  return period === "today" ? `今日 ${day(now)}` : `${day(from)} – ${day(now)}`;
}

/** Tariff periods the table splits energy into, hottest first. */
const tariffCodes = (tariff: SiteTariffWithCalendar) => PERIOD_ORDER.filter((code) => tariff.periods.some((period) => period.periodCode === code));

export function ConsumptionWorkspace() {
  const { site } = siteRoute.useRouteContext();
  const queryClient = useQueryClient();
  const search = useSearch({ from: "/_app/_site/energy-analysis/consumption" });
  const navigate = useNavigate({ from: "/energy-analysis/consumption" });
  const period: EnergyPeriod = search.period ?? "today";
  const metric: ConsumptionMetric = search.metric ?? "electricityKWh";
  const view: ConsumptionView = search.view ?? "trend";
  const previousLabel = PREVIOUS_LABEL[period];
  const query = useEnergyComparison(period);
  const tariff = useQuery(siteTariffQuery(site.id));
  const destination = useEnergyBreakdown(period);
  const comparison = query.data;

  // Each bucket as the owner priced it, the previous period's lined up with the current one's.
  const pricing = useMemo(() => {
    if (!tariff.data || !comparison) return null;
    const byLabel = (pick: (bucket: BucketComparison) => { electricityKWh: number | null; cost: BucketComparison["cost"] }) =>
      new Map(comparison.buckets.filter((bucket) => pick(bucket).electricityKWh !== null).map((bucket) => [bucket.label, sumCosts([pick(bucket)])]));
    return {
      current: sumCosts(summaryCostBuckets(comparison.current)),
      previous: sumCosts(summaryCostBuckets(comparison.previous)),
      byLabel: byLabel((bucket) => ({ electricityKWh: bucket.electricityKWh, cost: bucket.cost })),
      previousByLabel: byLabel((bucket) => ({ electricityKWh: bucket.previousElectricityKWh, cost: bucket.previousCost })),
      codes: tariffCodes(tariff.data),
    };
  }, [tariff.data, comparison]);

  const setSearch = (patch: { period?: EnergyPeriod; metric?: ConsumptionMetric; view?: ConsumptionView }) =>
    void navigate({ search: (previous) => ({ ...previous, ...patch }), replace: true });

  const exportCsv = () => {
    if (!comparison) return;
    const codes = pricing?.codes ?? [];
    const lines = [
      [site.displayName, ENERGY_PERIODS[period], "站点当地时间"],
      [BUCKET_HEADER[comparison.current.granularity], "空调用电 kWh", `${previousLabel}用电 kWh`, ...codes.map((code) => `${PERIOD_LABEL[code]} kWh`), "供冷量 kWh", "综合能效 COP", ...(pricing ? ["估算电费 元"] : [])],
      ...comparison.buckets.map((row) => {
        const cost = pricing?.byLabel.get(row.label);
        return [
          row.label, row.electricityKWh ?? "", row.previousElectricityKWh ?? "",
          ...codes.map((code) => cost?.byPeriod.find((usage) => usage.code === code)?.energyKWh.toFixed(1) ?? ""),
          row.coolingKWh ?? "", row.cop?.toFixed(2) ?? "", ...(pricing ? [cost?.total.toFixed(0) ?? ""] : []),
        ];
      }),
      ...destinationCsv(destination, previousLabel),
    ];
    const url = URL.createObjectURL(new Blob(["﻿" + lines.map((line) => line.join(",")).join("\n")], { type: "text/csv;charset=utf-8" }));
    const link = document.createElement("a");
    link.href = url;
    link.download = `${site.displayName}-能耗-${ENERGY_PERIODS[period]}.csv`;
    link.click();
    URL.revokeObjectURL(url);
  };

  return (
    <Main>
      <PageHeader
        title="能耗分析"
        description={`${site.displayName} · 用了多少电、用在哪台设备、花了多少钱`}
        actions={
          <>
            <span className="text-xs text-muted-foreground">
              {rangeLabel(period, site.timezone)} · 对比{previousLabel}
              {comparison ? (
                <span data-testid="energy-freshness">
                  {comparison.current.dataWatermark ? ` · 数据截至 ${formatTime(comparison.current.dataWatermark, site.timezone)}` : " · 所选时段暂无计量数据"}
                </span>
              ) : null}
            </span>
            <PeriodTabs aria-label="统计期间" options={PERIOD_OPTIONS} value={period} onValueChange={(value) => setSearch({ period: value })} />
            <Button variant="outline" size="icon" aria-label="刷新能耗" onClick={() => { void query.refetch(); void destination.query.refetch(); void destination.previous.refetch(); void queryClient.invalidateQueries({ queryKey: dataGapsKeys.site(site.id) }); }}>
              <RefreshCw />
            </Button>
            <Button variant="outline" disabled={!comparison?.buckets.length} onClick={exportCsv}>
              <Download aria-hidden="true" data-icon="inline-start" />
              导出
            </Button>
          </>
        }
      />
      <DataGapBanner siteId={site.id} timeZone={site.timezone} />
      {query.isPending ? (
        <Skeleton className="h-[440px] rounded-xl" />
      ) : query.isError || !comparison ? (
        <Panel aria-label="期间用能">
          <div className="flex flex-col items-start gap-2 px-5 py-8">
            <p className="text-sm font-semibold">能耗数据暂时读不到</p>
            <p className="text-xs text-muted-foreground">暂时未能获取所选时段的能耗数据。</p>
            <Button variant="outline" size="sm" onClick={() => void query.refetch()}>重试</Button>
          </div>
        </Panel>
      ) : (
        <>
          <IncompleteTelemetryNotice summary={comparison.current} />
          <PeriodChart comparison={comparison} pricing={pricing} previousLabel={previousLabel} metric={metric} onMetricChange={(next) => setSearch({ metric: next })} timeZone={site.timezone} />
          <EnergyDestination result={destination} previousLabel={previousLabel} />
          <Panel aria-label="能耗明细">
            <Tabs value={view} onValueChange={(value) => { if (isOneOf(CONSUMPTION_VIEWS, value)) setSearch({ view: value }); }} className="gap-0">
              <header className="flex h-12 shrink-0 items-center gap-4 border-b border-divider px-5">
                <TabsList variant="line" className="h-12 gap-5 p-0">
                  <TabsTrigger value="trend" className="h-12 flex-none px-0 after:bottom-0!">分项趋势</TabsTrigger>
                  <TabsTrigger value="tou" className="h-12 flex-none px-0 after:bottom-0!">分时电费</TabsTrigger>
                  <TabsTrigger value="detail" className="h-12 flex-none px-0 after:bottom-0!">{GRANULARITY_COPY[comparison.current.granularity].detailTitle}</TabsTrigger>
                </TabsList>
                <span className="ml-auto flex min-w-0 items-center gap-1 truncate text-xs text-muted-foreground">
                  {view === "trend" ? "未走完的时段以浅色显示"
                    : view === "tou" ? (tariff.data ? <>{tariff.data.displayName}<InfoTip>按站点电价估算，正式电费以结算为准</InfoTip></> : null)
                      : "kWh · 最新在前"}
                </span>
              </header>
              <TabsContent value="trend">
                <CategoryTrend result={destination} granularity={comparison.current.granularity} timeZone={site.timezone} />
              </TabsContent>
              <TabsContent value="tou">
                <TouSection tariff={tariff} pricing={pricing} pending={tariff.isPending} timeZone={site.timezone} />
              </TabsContent>
              <TabsContent value="detail">
                <DetailTable comparison={comparison} pricing={pricing} previousLabel={previousLabel} timeZone={site.timezone} />
              </TabsContent>
            </Tabs>
          </Panel>
        </>
      )}
    </Main>
  );
}

interface Pricing {
  readonly current: TouCost;
  readonly previous: TouCost;
  readonly byLabel: ReadonlyMap<string, TouCost>;
  readonly previousByLabel: ReadonlyMap<string, TouCost>;
  readonly codes: readonly PeriodCode[];
}

function IncompleteTelemetryNotice({ summary }: { readonly summary: EnergySummary }) {
  if (summary.missingMeters.length === 0 && summary.incompleteBuckets === 0) return null;
  const scope = GRANULARITY_COPY[summary.granularity].incompleteScope;
  const detail = summary.missingMeters.length > 0
    ? `${scope}缺少 ${summary.missingMeters.join("、")} 读数`
    : `${scope}有 ${summary.incompleteBuckets} 个时段读数不完整`;
  return (
    <div
      role="status"
      aria-label="计量不完整提示"
      data-testid="consumption-incomplete-notice"
      className="flex flex-wrap items-center gap-2 rounded-lg border border-warning-soft bg-warning-soft/45 px-4 py-2.5 text-xs text-foreground"
    >
      <Badge variant="warning">▲ 不完整</Badge>
      <span className="font-medium text-warning-text">{detail}</span>
      <span className="text-muted-foreground">
        空调用电未包含缺失设备，综合能效可能偏高，对比时请留意。
      </span>
    </div>
  );
}

function PeriodChart({ comparison, pricing, previousLabel, metric, onMetricChange, timeZone }: {
  readonly comparison: NonNullable<ReturnType<typeof useEnergyComparison>["data"]>;
  readonly pricing: Pricing | null;
  readonly previousLabel: string;
  readonly metric: ConsumptionMetric;
  readonly onMetricChange: (metric: ConsumptionMetric) => void;
  readonly timeZone: string;
}) {
  const { current, previous, buckets } = comparison;
  const granularity = current.granularity;
  const explain = useAssistantPanel((state) => state.explain);
  // The bucket 解释 asks about: the one clicked, else the period's highest. A running bucket holds partial energy, so it is not offered.
  const [picked, setPicked] = useState<string | null>(null);
  const explainable = (bucket: BucketComparison) => bucket.complete && bucket.electricityKWh !== null;
  const explained = buckets.find((bucket) => bucket.label === picked && explainable(bucket))
    ?? buckets.find((bucket) => bucket.label === current.peak?.label && explainable(bucket));
  // One unit for both energies, so electricity and cooling read side by side.
  const scale = Math.max(current.electricityKWh ?? 0, current.coolingKWh ?? 0);
  const electricity = energy(current.electricityKWh, scale);
  const cooling = energy(current.coolingKWh, scale);
  const beforeCop = (value: number | null) => `${previousLabel} ${value === null ? "--" : formatDecimal(value, 2)}`;
  const previousEnergy = (value: number | null) => { const shown = energy(value, scale); return `${previousLabel} ${shown.value ?? "--"}${shown.value ? ` ${shown.unit}` : ""}`; };

  // The running bucket holds partial values: drawn hatched apart from the complete ones.
  const split = (bucket: BucketComparison, key: string, value: number | null) =>
    bucket.complete ? { [key]: value } : { [`${key}Partial`]: value };
  const data = buckets.map((bucket) => ({
    label: bucket.label,
    previousElectricityKWh: bucket.previousElectricityKWh,
    previousCoolingKWh: bucket.previousCoolingKWh,
    previousCop: bucket.previousCop === null ? null : Number(bucket.previousCop.toFixed(2)),
    previousCost: pricing?.previousByLabel.get(bucket.label)?.total ?? null,
    ...split(bucket, "electricityKWh", bucket.electricityKWh),
    ...split(bucket, "coolingKWh", bucket.coolingKWh),
    ...split(bucket, "cop", bucket.cop === null ? null : Number(bucket.cop.toFixed(2))),
    ...split(bucket, "cost", pricing?.byLabel.get(bucket.label)?.total ?? null),
  }));

  const hasMissingTelemetry = current.missingMeters.length > 0 || current.incompleteBuckets > 0;
  const electricityCaption = current.missingMeters.length > 0
    ? (
      <span
        className="inline-flex items-center gap-1 text-warning-text"
        title={`不完整，缺 ${current.missingMeters.join("、")}`}
      >
        <TriangleAlert className="size-3 shrink-0" aria-hidden="true" />
        不完整，缺 {current.missingMeters.join("、")}
      </span>
    )
    : current.incompleteBuckets > 0
      ? (
        <span className="inline-flex items-center gap-1 text-warning-text">
          <TriangleAlert className="size-3 shrink-0" aria-hidden="true" />
          部分时段电表不完整
        </span>
      )
      : previousEnergy(previous.electricityKWh);

  const copCaption = current.cop === null
    ? copUnavailableText(current.copUnavailableReason, current.missingMeters)
    : current.missingMeters.length > 0
      ? (
        <span
          className="inline-flex items-center gap-1 text-warning-text"
          title={`缺 ${current.missingMeters.join("、")}，综合能效可能偏高`}
        >
          <TriangleAlert className="size-3 shrink-0" aria-hidden="true" />
          缺 {current.missingMeters.join("、")}，能效可能偏高
        </span>
      )
      : hasMissingTelemetry
        ? (
          <span className="inline-flex items-center gap-1 text-warning-text">
            <TriangleAlert className="size-3 shrink-0" aria-hidden="true" />
            部分时段不完整，能效可能偏高
          </span>
        )
        : beforeCop(previous.cop);

  const metrics: MetricTab<ConsumptionMetric>[] = [
    {
      key: "electricityKWh", label: "空调用电", compareKey: "previousElectricityKWh", partialKey: "electricityKWhPartial",
      value: electricity.value, unit: electricity.unit, fractionDigits: 1, invertDelta: true,
      delta: relativeChange(current.electricityKWh, previous.electricityKWh),
      caption: electricityCaption,
    },
    {
      key: "coolingKWh", label: "供冷量", compareKey: "previousCoolingKWh", partialKey: "coolingKWhPartial",
      value: cooling.value, unit: cooling.unit, fractionDigits: 1, neutralDelta: true,
      delta: relativeChange(current.coolingKWh, previous.coolingKWh), caption: previousEnergy(previous.coolingKWh),
    },
    {
      key: "cop", label: "综合能效", compareKey: "previousCop", partialKey: "copPartial",
      info: "期间总供冷量 ÷ 期间总空调用电（按有配对读数的时段合计）。冷机待机、低负荷、温差不确定度过大或读数超出合理范围时不计算。",
      value: current.cop === null ? "--" : formatDecimal(current.cop, 2),
      ...(current.cop === null ? {} : { unit: "COP" }),
      fractionDigits: 2,
      delta: relativeChange(current.cop, previous.cop),
      caption: copCaption,
    },
    ...(pricing ? [{
      key: "cost", label: "估算电费", compareKey: "previousCost", partialKey: "costPartial", info: "按站点电价估算，正式电费以结算为准",
      value: `¥${formatDecimal(pricing.current.total, 0)}`, fractionDigits: 0, invertDelta: true,
      delta: relativeChange(pricing.current.total, pricing.previous.total),
      caption: pricing.current.energyKWh - pricing.current.unpricedKWh > 0
        ? `均价 ${formatDecimal(pricing.current.total / (pricing.current.energyKWh - pricing.current.unpricedKWh), 3)} 元/kWh · 用电 ${formatDecimal(pricing.current.energyKWh, 0)} kWh`
        : "所选时段暂无计量数据",
    } satisfies MetricTab<ConsumptionMetric>] : []),
  ];
  const active = metrics.find((candidate) => candidate.key === metric) ?? metrics[0];
  const unit = active.key === "cop" ? "COP" : active.key === "cost" ? "元" : "kWh";
  const metered = current.rows.filter((row) => row.electricityKWh !== null);
  const meteredElectricityKWh = metered.reduce((sum, row) => sum + row.electricityKWh!, 0);
  const granularityCopy = GRANULARITY_COPY[granularity];

  return (
    <MetricTabsChartCard
      role="region"
      aria-label="期间用能"
      data={data}
      xKey="label"
      selected={active.key}
      onSelectedChange={onMetricChange}
      color="var(--chart-1)"
      compareColor="var(--chart-5)"
      compareLabel={previousLabel}
      chartType="bar"
      onPointClick={(index) => setPicked(buckets[index]?.label ?? null)}
      showYAxis
      showLegend
      yDomain="zero"
      chartClassName="h-[280px]"
      empty="所选时段暂无计量数据"
      metrics={metrics}
      chartTitle={(
        <>
          <span className="font-semibold">{CHART_TITLE[granularity][active.key]}</span>
          <span className="text-xs text-muted-foreground">{unit}</span>
          <span className="ml-auto flex items-center gap-3 text-xs text-muted-foreground">
            {current.peak ? <span>{granularityCopy.peakLabel} <span className="font-medium text-foreground">{current.peak.label}</span> {formatDecimal(current.peak.electricityKWh!, 1)} kWh</span> : null}
            {metered.length > 0 ? <span>{granularityCopy.averageLabel} {formatDecimal(meteredElectricityKWh / metered.length, 1)} kWh</span> : null}
            {current.crossGapElectricityKWh > 0 ? (
              <span className="flex items-center gap-1 text-foreground">
                跨缺口电量 <span className="font-medium tabular-nums">{formatDecimal(current.crossGapElectricityKWh, 1)} kWh</span>
                <InfoTip>累计读数间隔超过 1 小时的恢复电量：计入期间总量与电费，不摊入逐时或每日曲线</InfoTip>
              </span>
            ) : null}
            {current.excludedIntervals > 0 ? (
              <span className="flex items-center gap-1 text-warning-text">
                <TriangleAlert className="size-3.5" aria-hidden="true" />
                缺 {current.excludedIntervals} 段数据
                <InfoTip>电表读数可疑或无效的时段不计入合计</InfoTip>
              </span>
            ) : null}
            {explained ? (
              <Button variant="outline" size="sm" className="h-7 gap-1.5" onClick={() => explain(explainQuestion(explained, granularity, timeZone))}>
                <Sparkles className="size-3.5 text-primary" aria-hidden="true" />
                分析 {explained.label}
              </Button>
            ) : null}
          </span>
        </>
      )}
    />
  );
}

function TouSection({ tariff, pricing, pending, timeZone }: {
  readonly tariff: { readonly data?: SiteTariff | null; readonly isError: boolean };
  readonly pricing: Pricing | null;
  readonly pending: boolean;
  readonly timeZone: string;
}) {
  if (pending) return <Skeleton className="m-5 h-[140px] rounded-lg" />;
  if (tariff.data === null) {
    return (
      <div role="region" aria-label="分时电费" className="flex flex-col items-center gap-2 px-6 py-8 text-center">
        <span className="flex size-9 items-center justify-center rounded-xl bg-muted text-muted-foreground"><ReceiptText className="size-[18px]" aria-hidden="true" /></span>
        <p className="text-sm font-semibold">站点还没有配置电价</p>
        <p className="text-xs text-muted-foreground">为站点的电网进线发布电价后，这里按峰平谷时段估算电费</p>
      </div>
    );
  }
  if (tariff.isError || !tariff.data || !pricing) {
    return <p role="region" aria-label="分时电费" className="px-5 py-8 text-center text-sm text-muted-foreground">电价暂时读不到</p>;
  }
  return <TouCostDetail tariff={tariff.data} cost={pricing.current} timeZone={timeZone} />;
}

function DetailTable({ comparison, pricing, previousLabel, timeZone }: {
  readonly comparison: NonNullable<ReturnType<typeof useEnergyComparison>["data"]>;
  readonly pricing: Pricing | null;
  readonly previousLabel: string;
  readonly timeZone: string;
}) {
  const granularity = comparison.current.granularity;
  const explain = useAssistantPanel((state) => state.explain);
  const title = GRANULARITY_COPY[granularity].detailTitle;
  const codes = pricing?.codes ?? [];
  // Newest first: the bucket still running leads.
  const rows = [...comparison.buckets].filter((bucket) => bucket.electricityKWh !== null || bucket.coolingKWh !== null || bucket.complete).reverse();
  return (
    <Table aria-label={title} className="[&_td]:whitespace-nowrap [&_th]:whitespace-nowrap">
        <TableHeader>
          <TableRow className="hover:bg-transparent">
            <TableHead className="pl-5">{BUCKET_HEADER[granularity]}</TableHead>
            <TableHead className="text-right">空调用电</TableHead>
            <TableHead className="text-right">{previousLabel}</TableHead>
            <TableHead className="text-right">变化</TableHead>
            {codes.map((code) => <TableHead key={code} className="text-right">{PERIOD_LABEL[code]}</TableHead>)}
            <TableHead className="text-right">供冷量</TableHead>
            <TableHead className="pr-5 text-right">综合能效</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {granularity !== "month" && comparison.current.crossGapElectricityKWh > 0 ? (
            <TableRow className="bg-muted/25 hover:bg-muted/25">
              <TableCell colSpan={6 + codes.length} className="pl-5 pr-5 py-2 text-xs text-muted-foreground">
                {GRANULARITY_COPY[granularity].crossGapNote}
              </TableCell>
            </TableRow>
          ) : null}
          {rows.length === 0 && comparison.current.crossGapElectricityKWh <= 0 ? (
            <TableRow className="hover:bg-transparent"><TableCell colSpan={6 + codes.length} className="py-8 text-center text-muted-foreground">所选时段暂无计量数据</TableCell></TableRow>
          ) : rows.map((row) => {
            const change = relativeChange(row.electricityKWh, row.previousElectricityKWh);
            const cost = pricing?.byLabel.get(row.label);
            return (
              <TableRow key={row.label} className="group">
                <TableCell className="pl-5">
                  <span className="flex items-center gap-2">
                    <span className="font-medium">{row.label}</span>
                    {!row.complete ? <Badge>进行中</Badge> : null}
                    {row.incomplete || row.missingMeters.length > 0 ? (
                      <Badge variant="warning">
                        {row.missingMeters.length > 0 ? `▲ 不完整，缺 ${row.missingMeters.join("、")}` : "▲ 数据不完整"}
                      </Badge>
                    ) : null}
                    {row.complete && row.electricityKWh !== null ? (
                      <Button
                        variant="ghost"
                        size="sm"
                        className="h-6 gap-1 px-1.5 text-xs text-primary opacity-0 group-hover:opacity-100 focus-visible:opacity-100"
                        aria-label={`分析 ${row.label}`}
                        onClick={() => explain(explainQuestion(row, granularity, timeZone))}
                      >
                        <Sparkles className="size-3" aria-hidden="true" />
                        分析原因
                      </Button>
                    ) : null}
                  </span>
                </TableCell>
                <TableCell className="text-right font-medium">{number(row.electricityKWh)}</TableCell>
                <TableCell className="text-right text-muted-foreground">{number(row.previousElectricityKWh)}</TableCell>
                <TableCell className="text-right">{change === null ? dash : <DeltaBadge delta={change} invert variant="text" showIcon={false} />}</TableCell>
                {codes.map((code) => {
                  const used = cost?.byPeriod.find((usage) => usage.code === code)?.energyKWh;
                  return <TableCell key={code} className={cn("text-right", !used && "text-muted-foreground")}>{used ? number(used, 0) : dash}</TableCell>;
                })}
                <TableCell className="text-right">{number(row.coolingKWh)}</TableCell>
                <TableCell className="pr-5 text-right">{row.cop === null ? <span className="text-muted-foreground">--</span> : number(row.cop, 2)}</TableCell>
              </TableRow>
            );
          })}
        </TableBody>
    </Table>
  );
}
