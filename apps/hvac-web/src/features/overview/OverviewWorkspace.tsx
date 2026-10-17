import { useQuery, useQueryClient } from "@tanstack/react-query";
import { getRouteApi, useNavigate } from "@tanstack/react-router";
import { RefreshCw } from "lucide-react";
import { HeatmapChartCard } from "@/components/heatmap-chart-card";
import { MetricTabsChartCard } from "@/components/metric-tabs-chart-card";
import { PageHeader } from "@/blocks/page-header";
import { Panel, PanelHeader, PanelLink } from "@/blocks/panel";
import { Main } from "@/components/layout/Main";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { useDeviceNames } from "@/features/assets/use-device-names";
import { siteFindingsQuery } from "@/features/diagnostics/query";
import { activeAlarmsQuery } from "@/features/alarms/alarm-api";
import { sortAlarmsForOperators } from "@/features/alarms/alarm-presentation";
import { completeOnly, relativeChange } from "@/features/energy-analysis/consumption/model";
import { useHourlyHeatmap, useTodayAgainstYesterday } from "@/features/energy-analysis/consumption/query";
import { formatClock, LiveIndicator } from "@/features/operations/realtime/device-presentation";
import { siteTariffQuery } from "@/features/tariff/query";
import { costsByLabel } from "@/features/tariff/tariff-model";
import { useRealtimePlant } from "@/features/operations/realtime/use-realtime-plant";
import { sortWorkOrdersForOperators } from "@/features/work-orders/work-order-presentation";
import { openWorkOrdersQuery } from "@/features/work-orders/work-order-queries";
import { formatDecimal } from "@/lib/operator-format";
import { AttentionPanel } from "./AttentionPanel";
import { PlantNowPanel } from "./PlantNowPanel";
import { SavingsPanel } from "./SavingsPanel";

const siteRoute = getRouteApi("/_app/_site");
const pageRoute = getRouteApi("/_app/_site/overview");
const known = (value: number | null, digits: number) => (value === null ? null : formatDecimal(value, digits));
const HEATMAP_DAYS = 7;
const HOURS = Array.from({ length: 24 }, (_, hour) => `${hour}时`);

type OverviewMetric = "electricityKWh" | "coolingKWh" | "cop" | "cost";

const METRIC_TITLE: Readonly<Record<OverviewMetric, string>> = {
  electricityKWh: "空调用电",
  coolingKWh: "供冷量",
  cop: "综合能效",
  cost: "电费",
};

function metricTitle(metric: OverviewMetric): string {
  return METRIC_TITLE[metric];
}

function todayLabel(timeZone: string): string {
  return new Intl.DateTimeFormat("zh-CN", { timeZone, month: "long", day: "numeric", weekday: "short" }).format(new Date()).replace("日", " 日 ").replace("月", " 月 ").replace(/\s+/g, " ").trim();
}

/** The Site's UTC offset as operators write it, e.g. UTC+8. */
function offsetLabel(timeZone: string): string {
  const part = new Intl.DateTimeFormat("en-US", { timeZone, timeZoneName: "shortOffset" }).formatToParts(new Date()).find((piece) => piece.type === "timeZoneName");
  return (part?.value ?? "GMT").replace("GMT", "UTC").replace(/^UTC$/, "UTC+0");
}

function TodayAgainstYesterday({ metric, onMetricChange }: { readonly metric: OverviewMetric; readonly onMetricChange: (metric: OverviewMetric) => void }) {
  const { site } = siteRoute.useRouteContext();
  const comparison = useTodayAgainstYesterday();
  const tariff = useQuery(siteTariffQuery(site.id));
  if (comparison.isPending || comparison.isError) {
    return (
      <Panel role="region" aria-label="今日用能">
        <PanelHeader title="今日用能" action={<PanelLink to="/energy-analysis/consumption" search={{ site: site.id }}>能源分析</PanelLink>} />
        {comparison.isPending ? <Skeleton className="m-4 h-[300px]" /> : (
          <div className="flex h-[300px] flex-col items-center justify-center gap-3 text-sm text-muted-foreground">
            今日能耗暂时读不到
            <Button variant="outline" size="sm" onClick={() => void comparison.refetch()}><RefreshCw data-icon="inline-start" aria-hidden="true" />重试</Button>
          </div>
        )}
      </Panel>
    );
  }
  const { today, yesterday, wholeYesterday, hours } = comparison.data;
  // Without a Tariff the day is not priced, and the cost tab is left out.
  const priced = tariff.data
    ? { today: costsByLabel(today.rows), yesterday: costsByLabel(wholeYesterday.rows), sameHours: costsByLabel(yesterday.rows) }
    : null;
  const sum = (costs: Map<string, number>) => (costs.size === 0 ? null : [...costs.values()].reduce((total, cost) => total + cost, 0));
  return (
    <MetricTabsChartCard
      role="region"
      aria-label="今日用能"
      title="今日用能"
      caption={`累计 · 00:00 至 ${new Intl.DateTimeFormat("zh-CN", { timeZone: site.timezone, hour: "2-digit", minute: "2-digit", hour12: false }).format(new Date())}`}
      action={<PanelLink to="/energy-analysis/consumption" search={{ site: site.id }}>能源分析</PanelLink>}
      chartTitle={<><span className="font-semibold">逐时{metricTitle(metric)}</span><span className="text-xs text-muted-foreground">按站点当地时间，与昨日同时段对比</span></>}
      data={completeOnly(hours).map((hour) => ({
        ...hour,
        cost: hour.electricityKWh === null ? null : priced?.today.get(hour.label) ?? null,
        previousCost: priced?.yesterday.get(hour.label) ?? null,
      }))}
      xKey="label"
      selected={metric}
      onSelectedChange={(key) => onMetricChange(key as OverviewMetric)}
      color="var(--chart-1)"
      compareColor="var(--chart-5)"
      compareLabel="昨日"
      showYAxis
      showLegend
      dots
      yDomain="zero"
      chartClassName="h-[220px]"
      empty="今日还没有计量数据"
      metrics={[
        {
          key: "electricityKWh",
          label: "今日空调用电",
          compareKey: "previousElectricityKWh",
          value: known(today.electricityKWh, 1),
          delta: relativeChange(today.electricityKWh, yesterday.electricityKWh),
          unit: "kWh",
          fractionDigits: 1,
          invertDelta: true,
        },
        {
          key: "coolingKWh",
          label: "今日供冷量",
          compareKey: "previousCoolingKWh",
          value: known(today.coolingKWh, 1),
          delta: relativeChange(today.coolingKWh, yesterday.coolingKWh),
          unit: "kWh",
          fractionDigits: 1,
          neutralDelta: true,
        },
        {
          key: "cop",
          label: "今日综合能效",
          compareKey: "previousCop",
          value: known(today.cop, 2),
          delta: relativeChange(today.cop, yesterday.cop),
          unit: "COP",
          fractionDigits: 2,
        },
        ...(priced ? [{
          key: "cost",
          label: "今日电费（估算）",
          compareKey: "previousCost",
          value: known(sum(priced.today), 0),
          delta: relativeChange(sum(priced.today), sum(priced.sameHours)),
          unit: "元",
          fractionDigits: 0,
          invertDelta: true,
        }] : []),
      ]}
    />
  );
}

function HourlyElectricity() {
  const heatmap = useHourlyHeatmap(HEATMAP_DAYS);
  if (heatmap.isPending || heatmap.isError) {
    return (
      <Panel role="region" aria-label="近 7 天逐时用电">
        <PanelHeader title={`近 ${HEATMAP_DAYS} 天空调用电`} />
        {heatmap.isPending ? <Skeleton className="m-4 h-[240px]" /> : (
          <div className="flex h-[240px] flex-col items-center justify-center gap-3 text-sm text-muted-foreground">
            逐时用电暂时读不到
            <Button variant="outline" size="sm" onClick={() => void heatmap.refetch()}><RefreshCw data-icon="inline-start" aria-hidden="true" />重试</Button>
          </div>
        )}
      </Panel>
    );
  }
  return (
    <HeatmapChartCard
      role="region"
      aria-label="近 7 天逐时用电"
      title={`近 ${HEATMAP_DAYS} 天空调用电`}
      deltaLabel="每格一小时 · 空白为无数据"
      rows={heatmap.data.map((day) => ({ label: day.label, values: day.values }))}
      columns={HOURS}
      columnLabelEvery={3}
      color="var(--chart-1)"
      unit="kWh"
      valueFormatter={(value) => formatDecimal(value, 1)}
    />
  );
}

export function OverviewWorkspace() {
  const { site, principal } = siteRoute.useRouteContext();
  const search = pageRoute.useSearch();
  const navigate = useNavigate({ from: "/overview" });
  const queryClient = useQueryClient();
  const { plant, mode, registry, refresh } = useRealtimePlant();
  const deviceNames = useDeviceNames(site.id);
  const alarms = useQuery(activeAlarmsQuery(site.id));
  const workOrders = useQuery(openWorkOrdersQuery(site.id));
  const findings = useQuery(siteFindingsQuery(site.id));

  const activeAlarms = sortAlarmsForOperators(alarms.data?.items ?? []);
  const openOrders = sortWorkOrdersForOperators(workOrders.data ?? []);
  const weekAgo = Date.now() - 7 * 86_400_000;
  const recentFindings = (findings.data ?? []).filter((finding) => Date.parse(finding.evaluationTo) >= weekAgo);
  const assetName = (assetId: string) => registry.data?.assetModel.assets.find((asset) => asset.id === assetId)?.displayName ?? "—";

  return (
    <Main>
      <PageHeader
        title="总览"
        description={`${site.displayName} · ${todayLabel(site.timezone)} · 站点时间 ${offsetLabel(site.timezone)}`}
        meta={<LiveIndicator mode={mode} />}
        actions={
          <>
            {plant.latestSampleAt ? (
              <span className="text-xs text-muted-foreground">数据时间 <span className="font-mono">{formatClock(plant.latestSampleAt, site.timezone)}</span></span>
            ) : null}
            <Button
              variant="outline"
              onClick={() => {
                refresh();
                void queryClient.invalidateQueries({ queryKey: ["energy-series", site.id] });
                void alarms.refetch();
                void workOrders.refetch();
                void findings.refetch();
              }}
            >
              <RefreshCw aria-hidden="true" data-icon="inline-start" />
              刷新
            </Button>
          </>
        }
      />

      <div className="grid items-stretch gap-4 @5xl/main:grid-cols-[minmax(0,1fr)_352px]">
        <div className="flex min-w-0 flex-col gap-4">
          <TodayAgainstYesterday
            metric={search.metric ?? "electricityKWh"}
            onMetricChange={(metric) => void navigate({ search: (previous) => ({ ...previous, metric }), replace: true })}
          />
          <PlantNowPanel plant={plant} registryState={registry.status} siteId={site.id} timeZone={site.timezone} />
          <HourlyElectricity />
        </div>
        <div className="flex min-w-0 flex-col gap-4">
          <AttentionPanel
            siteId={site.id}
            timeZone={site.timezone}
            principalId={principal.principalId}
            alarms={{ data: activeAlarms, isPending: alarms.isPending, isError: alarms.isError }}
            findings={{ data: recentFindings, isPending: findings.isPending, isError: findings.isError }}
            workOrders={{ data: openOrders, isPending: workOrders.isPending, isError: workOrders.isError }}
            deviceName={(deviceId) => deviceNames.get(deviceId) ?? "—"}
            assetName={assetName}
          />
          <SavingsPanel siteId={site.id} timeZone={site.timezone} />
        </div>
      </div>
    </Main>
  );
}
