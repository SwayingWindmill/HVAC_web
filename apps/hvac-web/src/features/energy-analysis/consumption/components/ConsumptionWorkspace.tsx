import { useQuery } from "@tanstack/react-query";
import { getRouteApi, useNavigate, useSearch } from "@tanstack/react-router";
import { Download, ReceiptText, RefreshCw, TriangleAlert } from "lucide-react";
import { useMemo } from "react";
import type { SiteTariff } from "@/api/generated/platformGateway.gen";
import { PageHeader } from "@/blocks/page-header";
import { Panel, PanelHeader } from "@/blocks/panel";
import { InfoTip } from "@/components/info-tip";
import { MetricTabsChartCard, type MetricTab } from "@/components/metric-tabs-chart-card";
import { Main } from "@/components/layout/Main";
import { Button } from "@/components/ui/button";
import { DeltaBadge } from "@/components/ui/delta-badge";
import { PeriodTabs } from "@/components/ui/period-tabs";
import { Skeleton } from "@/components/ui/skeleton";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { TouCostPanel } from "@/features/tariff/components/TouCostPanel";
import { siteTariffQuery } from "@/features/tariff/query";
import { PERIOD_LABEL, touCostByLabel, touEnergyCost, type PeriodCode, type TouCost } from "@/features/tariff/tariff-model";
import { formatDecimal, formatTime } from "@/lib/operator-format";
import { cn } from "@/lib/utils";
import {
  alignedLabel,
  BUCKET_NOUN,
  ENERGY_PERIODS,
  periodWindow,
  relativeChange,
  wallTime,
  type BucketComparison,
  type EnergyPeriod,
  type Granularity,
} from "../model";
import { useEnergyComparison, useHourlyElectricity } from "../query";

const siteRoute = getRouteApi("/_app/_site");

const PERIOD_OPTIONS = (Object.keys(ENERGY_PERIODS) as EnergyPeriod[]).map((value) => ({ value, label: ENERGY_PERIODS[value] }));

/** What the period is compared with: the same span of the period before. */
const PREVIOUS_LABEL: Record<EnergyPeriod, string> = { today: "昨日同时段", "7d": "前 7 天", month: "上月同期", year: "去年同期" };
const CHART_PREFIX: Record<Granularity, string> = { hour: "逐时", day: "每日", month: "每月" };
const BUCKET_HEADER: Record<Granularity, string> = { hour: "时段", day: "日期", month: "月份" };
const PERIOD_ORDER: readonly PeriodCode[] = ["SUPER_PEAK", "PEAK", "FLAT", "VALLEY"];

type ConsumptionMetric = "electricityKWh" | "coolingKWh" | "cop" | "cost";

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
const tariffCodes = (tariff: SiteTariff) => PERIOD_ORDER.filter((code) => tariff.periods.some((period) => period.periodCode === code));

export function ConsumptionWorkspace() {
  const { site } = siteRoute.useRouteContext();
  const search = useSearch({ from: "/_app/_site/energy-analysis/consumption" });
  const navigate = useNavigate({ from: "/energy-analysis/consumption" });
  const period: EnergyPeriod = search.period ?? "today";
  const metric: ConsumptionMetric = search.metric ?? "electricityKWh";
  const previousLabel = PREVIOUS_LABEL[period];
  const query = useEnergyComparison(period);
  const tariff = useQuery(siteTariffQuery(site.id));
  const hourly = useHourlyElectricity(period);
  const comparison = query.data;

  // Each bucket priced by time of use, the previous period's lined up with the current one's.
  const pricing = useMemo(() => {
    if (!tariff.data || !hourly.data) return null;
    const { granularity, from, previousFrom } = hourly.data;
    return {
      current: touEnergyCost(hourly.data.current, tariff.data, site.timezone),
      previous: touEnergyCost(hourly.data.previous, tariff.data, site.timezone),
      byLabel: touCostByLabel(hourly.data.current, tariff.data, site.timezone, (start) => alignedLabel(start, from, from, granularity, site.timezone)),
      previousByLabel: touCostByLabel(hourly.data.previous, tariff.data, site.timezone, (start) => alignedLabel(start, previousFrom, from, granularity, site.timezone)),
      codes: tariffCodes(tariff.data),
    };
  }, [tariff.data, hourly.data, site.timezone]);

  const setSearch = (patch: { period?: EnergyPeriod; metric?: ConsumptionMetric }) =>
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
        description={site.displayName}
        actions={
          <>
            <span className="text-xs text-muted-foreground">
              {rangeLabel(period, site.timezone)} · 对比{previousLabel}
              {comparison ? (
                <span data-testid="energy-freshness">
                  {comparison.current.dataWatermark ? ` · 数据截至 ${formatTime(comparison.current.dataWatermark, site.timezone)}` : " · 本期间还没有计量数据"}
                </span>
              ) : null}
            </span>
            <PeriodTabs aria-label="统计期间" options={PERIOD_OPTIONS} value={period} onValueChange={(value) => setSearch({ period: value as EnergyPeriod })} />
            <Button variant="outline" size="icon" aria-label="刷新能耗" onClick={() => { void query.refetch(); void hourly.refetch(); }}>
              <RefreshCw />
            </Button>
            <Button variant="outline" disabled={!comparison?.buckets.length} onClick={exportCsv}>
              <Download aria-hidden="true" data-icon="inline-start" />
              导出
            </Button>
          </>
        }
      />
      {query.isPending ? (
        <Skeleton className="h-[440px] rounded-xl" />
      ) : query.isError || !comparison ? (
        <Panel aria-label="期间用能">
          <div className="flex flex-col items-start gap-2 px-5 py-8">
            <p className="text-sm font-semibold">能耗数据暂时读不到</p>
            <p className="text-xs text-muted-foreground">能耗分析服务没有返回本期间的数据。</p>
            <Button variant="outline" size="sm" onClick={() => void query.refetch()}>重试</Button>
          </div>
        </Panel>
      ) : (
        <>
          <PeriodChart comparison={comparison} pricing={pricing} previousLabel={previousLabel} metric={metric} onMetricChange={(next) => setSearch({ metric: next })} />
          <TouSection tariff={tariff} pricing={pricing} hourlyFailed={hourly.isError} pending={tariff.isPending || (Boolean(tariff.data) && hourly.isPending)} timeZone={site.timezone} />
          <DetailTable comparison={comparison} pricing={pricing} previousLabel={previousLabel} />
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

function PeriodChart({ comparison, pricing, previousLabel, metric, onMetricChange }: {
  readonly comparison: NonNullable<ReturnType<typeof useEnergyComparison>["data"]>;
  readonly pricing: Pricing | null;
  readonly previousLabel: string;
  readonly metric: ConsumptionMetric;
  readonly onMetricChange: (metric: ConsumptionMetric) => void;
}) {
  const { current, previous, buckets } = comparison;
  const granularity = current.granularity;
  // One unit for both energies, so electricity and cooling read side by side.
  const scale = Math.max(current.electricityKWh ?? 0, current.coolingKWh ?? 0);
  const electricity = energy(current.electricityKWh, scale);
  const cooling = energy(current.coolingKWh, scale);
  const before = (value: number | null, digits = 1, unit = "") => `${previousLabel} ${value === null ? "—" : `${formatDecimal(value, digits)}${unit}`}`;
  const previousEnergy = (value: number | null) => { const shown = energy(value, scale); return `${previousLabel} ${shown.value ?? "—"}${shown.value ? ` ${shown.unit}` : ""}`; };

  // The running bucket holds partial values: drawn hatched apart from the complete ones.
  const split = (bucket: BucketComparison, key: string, value: number | null) =>
    bucket.complete ? { [key]: value } : { [`${key}Partial`]: value };
  const data = buckets.map((bucket) => ({
    label: bucket.label,
    previousElectricityKWh: bucket.previousElectricityKWh,
    previousCoolingKWh: bucket.previousCoolingKWh,
    previousCop: bucket.previousCop,
    previousCost: pricing?.previousByLabel.get(bucket.label)?.total ?? null,
    ...split(bucket, "electricityKWh", bucket.electricityKWh),
    ...split(bucket, "coolingKWh", bucket.coolingKWh),
    ...split(bucket, "cop", bucket.cop === null ? null : Number(bucket.cop.toFixed(2))),
    ...split(bucket, "cost", pricing?.byLabel.get(bucket.label)?.total ?? null),
  }));

  const metrics: MetricTab[] = [
    {
      key: "electricityKWh", label: "空调用电", compareKey: "previousElectricityKWh", partialKey: "electricityKWhPartial",
      value: electricity.value, unit: electricity.unit, fractionDigits: 1, invertDelta: true,
      delta: relativeChange(current.electricityKWh, previous.electricityKWh), caption: previousEnergy(previous.electricityKWh),
    },
    {
      key: "coolingKWh", label: "供冷量", compareKey: "previousCoolingKWh", partialKey: "coolingKWhPartial",
      value: cooling.value, unit: cooling.unit, fractionDigits: 1, neutralDelta: true,
      delta: relativeChange(current.coolingKWh, previous.coolingKWh), caption: previousEnergy(previous.coolingKWh),
    },
    {
      key: "cop", label: "综合能效", compareKey: "previousCop", partialKey: "copPartial", info: "供冷量 ÷ 空调用电",
      value: current.cop === null ? null : formatDecimal(current.cop, 2), unit: "COP", fractionDigits: 2,
      delta: relativeChange(current.cop, previous.cop), caption: before(previous.cop, 2),
    },
    ...(pricing ? [{
      key: "cost", label: "估算电费", compareKey: "previousCost", partialKey: "costPartial", info: "按站点电价估算，正式电费以结算为准",
      value: `¥${formatDecimal(pricing.current.total, 0)}`, fractionDigits: 0, invertDelta: true,
      delta: relativeChange(pricing.current.total, pricing.previous.total),
      caption: pricing.current.energyKWh - pricing.current.unpricedKWh > 0
        ? `均价 ${formatDecimal(pricing.current.total / (pricing.current.energyKWh - pricing.current.unpricedKWh), 3)} 元/kWh · 用电 ${formatDecimal(pricing.current.energyKWh, 0)} kWh`
        : "本期间还没有计量数据",
    } satisfies MetricTab] : []),
  ];
  const active = metrics.find((candidate) => candidate.key === metric) ?? metrics[0];
  const unit = active.key === "cop" ? "COP" : active.key === "cost" ? "元" : "kWh";
  const metered = current.rows.filter((row) => row.electricityKWh !== null);
  const noun = BUCKET_NOUN[granularity];

  return (
    <MetricTabsChartCard
      role="region"
      aria-label="期间用能"
      data={data}
      xKey="label"
      selected={active.key}
      onSelectedChange={(key) => onMetricChange(key as ConsumptionMetric)}
      color="var(--chart-1)"
      compareColor="var(--chart-5)"
      compareLabel={previousLabel}
      chartType="bar"
      showYAxis
      showLegend
      yDomain="zero"
      chartClassName="h-[280px]"
      empty="本期间还没有计量数据"
      metrics={metrics}
      chartTitle={(
        <>
          <span className="font-semibold">{CHART_PREFIX[granularity]}{active.label}</span>
          <span className="text-xs text-muted-foreground">{unit}</span>
          <span className="ml-auto flex items-center gap-3 text-xs text-muted-foreground">
            {current.peak ? <span>用电最高{noun} <span className="font-medium text-foreground">{current.peak.label}</span> {formatDecimal(current.peak.electricityKWh!, 1)} kWh</span> : null}
            {metered.length > 0 ? <span>平均每{noun} {formatDecimal(current.electricityKWh! / metered.length, 1)} kWh</span> : null}
            {current.excludedIntervals > 0 ? (
              <span className="flex items-center gap-1 text-warning-text">
                <TriangleAlert className="size-3.5" aria-hidden="true" />
                {current.excludedIntervals} 段数据未计入
                <InfoTip>电表读数可疑或无效的区间不计入合计</InfoTip>
              </span>
            ) : null}
          </span>
        </>
      )}
    />
  );
}

function TouSection({ tariff, pricing, hourlyFailed, pending, timeZone }: {
  readonly tariff: { readonly data?: SiteTariff | null; readonly isError: boolean };
  readonly pricing: Pricing | null;
  readonly hourlyFailed: boolean;
  readonly pending: boolean;
  readonly timeZone: string;
}) {
  if (pending) return <Skeleton className="h-[188px] rounded-xl" />;
  if (tariff.data === null) {
    return (
      <Panel aria-label="分时电费">
        <PanelHeader title="分时电费" />
        <div className="flex flex-col items-center gap-2 px-6 py-8 text-center">
          <span className="flex size-9 items-center justify-center rounded-xl bg-muted text-muted-foreground"><ReceiptText className="size-[18px]" aria-hidden="true" /></span>
          <p className="text-sm font-semibold">站点还没有配置电价</p>
          <p className="text-xs text-muted-foreground">为站点的电网进线发布电价后，这里按峰平谷时段估算电费</p>
        </div>
      </Panel>
    );
  }
  if (tariff.isError || hourlyFailed || !tariff.data || !pricing) {
    return (
      <Panel aria-label="分时电费">
        <PanelHeader title="分时电费" />
        <p className="px-5 py-8 text-center text-sm text-muted-foreground">电价或逐时用电暂时读不到</p>
      </Panel>
    );
  }
  return <TouCostPanel tariff={tariff.data} cost={pricing.current} timeZone={timeZone} />;
}

function DetailTable({ comparison, pricing, previousLabel }: {
  readonly comparison: NonNullable<ReturnType<typeof useEnergyComparison>["data"]>;
  readonly pricing: Pricing | null;
  readonly previousLabel: string;
}) {
  const granularity = comparison.current.granularity;
  const title = `分${BUCKET_NOUN[granularity]}明细`;
  const codes = pricing?.codes ?? [];
  // Newest first: the bucket still running leads.
  const rows = [...comparison.buckets].filter((bucket) => bucket.electricityKWh !== null || bucket.coolingKWh !== null || bucket.complete).reverse();
  return (
    <Panel aria-label={title}>
      <PanelHeader title={title} caption={`kWh · ${rows.length} ${granularity === "hour" ? "个时段" : granularity === "day" ? "天" : "个月"}`} />
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
          {rows.length === 0 ? (
            <TableRow className="hover:bg-transparent"><TableCell colSpan={6 + codes.length} className="py-8 text-center text-muted-foreground">本期间还没有计量数据</TableCell></TableRow>
          ) : rows.map((row) => {
            const change = relativeChange(row.electricityKWh, row.previousElectricityKWh);
            const cost = pricing?.byLabel.get(row.label);
            return (
              <TableRow key={row.label}>
                <TableCell className="pl-5">
                  <span className="flex items-center gap-2">
                    <span className="font-medium">{row.label}</span>
                    {!row.complete ? <span className="rounded-md bg-primary-soft px-1.5 text-[11px] leading-[18px] font-medium text-primary">进行中</span> : null}
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
                <TableCell className="pr-5 text-right">{number(row.cop, 2)}</TableCell>
              </TableRow>
            );
          })}
        </TableBody>
      </Table>
    </Panel>
  );
}
