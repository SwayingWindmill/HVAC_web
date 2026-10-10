import { useQuery } from "@tanstack/react-query";
import { Link, getRouteApi, useNavigate } from "@tanstack/react-router";
import { ArrowUpRight, CircleCheck, RefreshCw } from "lucide-react";
import type { ReactNode } from "react";
import { Area, AreaChart, CartesianGrid, XAxis, YAxis } from "recharts";
import { MetricCard, MetricGrid, MetricValue } from "@/blocks/metric-card";
import { PageHeader } from "@/blocks/page-header";
import { Main } from "@/components/layout/Main";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import {
  ChartContainer,
  ChartLegend,
  ChartLegendContent,
  ChartTooltip,
  ChartTooltipContent,
} from "@/components/ui/chart";
import { Item, ItemActions, ItemContent, ItemDescription, ItemGroup, ItemTitle } from "@/components/ui/item";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { useDeviceNames } from "@/features/assets/use-device-names";
import { activeAlarmsQuery } from "@/features/alarms/alarm-api";
import { SeverityBadge, sortAlarmsForOperators } from "@/features/alarms/alarm-presentation";
import { ENERGY_PERIODS, type EnergyPeriod } from "@/features/energy-analysis/consumption/model";
import { useEnergySummary } from "@/features/energy-analysis/consumption/query";
import { ReadingMetric } from "@/features/operations/realtime/device-presentation";
import { useRealtimePlant } from "@/features/operations/realtime/use-realtime-plant";
import type { PlantDevice, PlantView } from "@/features/operations/realtime/plant-model";
import { PriorityBadge, sortWorkOrdersForOperators, STATUS_LABELS } from "@/features/work-orders/work-order-presentation";
import { openWorkOrdersQuery } from "@/features/work-orders/work-order-queries";
import { formatDecimal, formatTime, personLabel } from "@/lib/operator-format";

const siteRoute = getRouteApi("/_app/_site");
const pageRoute = getRouteApi("/_app/_site/overview");
const LIST_LIMIT = 5;
const PERIODS = Object.keys(ENERGY_PERIODS) as EnergyPeriod[];

const decimal = formatDecimal;

const EQUIPMENT: ReadonlySet<PlantDevice["category"]> = new Set(["CHILLER", "CHILLED_WATER_PUMP", "COOLING_WATER_PUMP", "COOLING_TOWER"]);

/** Running and fault counts follow the realtime page: an offline device is offline whatever it last reported. */
function equipmentStatus(plant: PlantView) {
  const equipment = plant.devices.filter((device) => EQUIPMENT.has(device.category));
  const online = equipment.filter((device) => device.connection !== "OFFLINE");
  return {
    total: equipment.length,
    running: online.filter((device) => device.runState === "RUNNING").length,
    faults: online.filter((device) => device.runState === "FAULT").length,
    offline: plant.devices.filter((device) => device.connection === "OFFLINE").length,
  };
}

function equipmentLead(status: ReturnType<typeof equipmentStatus>): string {
  const issues = [status.faults > 0 ? `${status.faults} 台故障` : null, status.offline > 0 ? `${status.offline} 台离线` : null].filter(Boolean);
  return issues.length > 0 ? issues.join("，") : "没有故障或离线设备";
}

const energyChartConfig = {
  electricityKWh: { label: "空调用电", color: "var(--chart-1)" },
  coolingKWh: { label: "供冷量", color: "var(--chart-2)" },
};

function EnergyTrend({ period, onPeriodChange }: { readonly period: EnergyPeriod; readonly onPeriodChange: (period: EnergyPeriod) => void }) {
  const energy = useEnergySummary(period);
  const rows = energy.data?.rows ?? [];
  return (
    <Card className="@container/card">
      <CardHeader>
        <CardTitle>用电与供冷</CardTitle>
        <CardDescription>按站点当地时间统计的空调用电与供冷量（kWh）</CardDescription>
        <CardAction>
          <ToggleGroup
            type="single"
            variant="outline"
            value={period}
            onValueChange={(value) => { if (value) onPeriodChange(value as EnergyPeriod); }}
            aria-label="统计期间"
            className="hidden @[640px]/card:flex"
          >
            {PERIODS.map((key) => (
              <ToggleGroupItem key={key} value={key} className="px-3">{ENERGY_PERIODS[key]}</ToggleGroupItem>
            ))}
          </ToggleGroup>
          <Select value={period} onValueChange={(value) => onPeriodChange(value as EnergyPeriod)}>
            <SelectTrigger className="w-28 min-w-0 @[640px]/card:hidden" aria-label="统计期间">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {PERIODS.map((key) => <SelectItem key={key} value={key}>{ENERGY_PERIODS[key]}</SelectItem>)}
            </SelectContent>
          </Select>
        </CardAction>
      </CardHeader>
      <CardContent className="px-2 sm:px-6">
        {energy.isPending ? (
          <Skeleton className="h-[260px] w-full" />
        ) : energy.isError ? (
          <p className="grid h-[260px] place-items-center text-sm text-muted-foreground">能耗数据暂不可用</p>
        ) : rows.length === 0 ? (
          <p className="grid h-[260px] place-items-center text-sm text-muted-foreground">本期间还没有计量数据</p>
        ) : (
          <ChartContainer config={energyChartConfig} className="aspect-auto h-[260px] w-full">
            <AreaChart data={[...rows]} margin={{ left: 4, right: 4 }}>
              <defs>
                {(["electricityKWh", "coolingKWh"] as const).map((key) => (
                  <linearGradient key={key} id={`overview-fill-${key}`} x1="0" y1="0" x2="0" y2="1">
                    <stop offset="5%" stopColor={`var(--color-${key})`} stopOpacity={0.5} />
                    <stop offset="95%" stopColor={`var(--color-${key})`} stopOpacity={0.05} />
                  </linearGradient>
                ))}
              </defs>
              <CartesianGrid vertical={false} />
              <XAxis dataKey="label" tickLine={false} axisLine={false} tickMargin={8} minTickGap={32} />
              <YAxis tickLine={false} axisLine={false} width={52} tickFormatter={(value: number) => value.toLocaleString("zh-CN")} />
              <ChartTooltip cursor={false} content={<ChartTooltipContent indicator="dot" />} />
              <Area dataKey="coolingKWh" type="monotone" fill="url(#overview-fill-coolingKWh)" stroke="var(--color-coolingKWh)" strokeWidth={2} isAnimationActive={false} />
              <Area dataKey="electricityKWh" type="monotone" fill="url(#overview-fill-electricityKWh)" stroke="var(--color-electricityKWh)" strokeWidth={2} isAnimationActive={false} />
              <ChartLegend content={<ChartLegendContent />} />
            </AreaChart>
          </ChartContainer>
        )}
      </CardContent>
    </Card>
  );
}

function ListCard({ title, description, href, siteId, children }: {
  readonly title: string;
  readonly description: string;
  readonly href: "/operations/alarms" | "/operations/work-center";
  readonly siteId: string;
  readonly children: ReactNode;
}) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>{title}</CardTitle>
        <CardDescription>{description}</CardDescription>
        <CardAction>
          <Button variant="ghost" size="sm" asChild>
            <Link to={href} search={{ site: siteId }}>
              查看全部
              <ArrowUpRight data-icon="inline-end" aria-hidden="true" />
            </Link>
          </Button>
        </CardAction>
      </CardHeader>
      <CardContent>{children}</CardContent>
    </Card>
  );
}

function EmptyList({ text }: { readonly text: string }) {
  return (
    <p className="flex items-center justify-center gap-2 py-10 text-sm text-muted-foreground">
      <CircleCheck className="size-4" aria-hidden="true" />
      {text}
    </p>
  );
}

export function OverviewWorkspace() {
  const { site, principal } = siteRoute.useRouteContext();
  const search = pageRoute.useSearch();
  const navigate = useNavigate({ from: "/overview" });
  const period: EnergyPeriod = search.period ?? "today";
  const { plant, mode, registry, refresh } = useRealtimePlant();
  const today = useEnergySummary("today");
  const deviceNames = useDeviceNames(site.id);
  const alarms = useQuery(activeAlarmsQuery(site.id));
  const workOrders = useQuery(openWorkOrdersQuery(site.id));

  const cooling = plant.coolingCapacity;
  const coolingPresent = cooling && cooling.state === "PRESENT" && cooling.numeric !== null;
  const equipment = equipmentStatus(plant);
  const energy = today.data;
  const activeAlarms = sortAlarmsForOperators(alarms.data?.items ?? []);
  const openOrders = sortWorkOrdersForOperators(workOrders.data ?? []);
  const asOf = (instant: string | null | undefined) => (instant ? `数据时间 ${formatTime(instant, site.timezone)}` : "等待数据");

  return (
    <Main className="@container/main flex flex-col gap-4 md:gap-6">
      <PageHeader
        title="总览看板"
        description={`${site.displayName} · 冷站运行与今日用能`}
        meta={
          <Badge variant="outline" className="gap-1.5 font-normal">
            <span className={mode === "live" ? "size-1.5 rounded-full bg-success" : "size-1.5 rounded-full bg-muted-foreground"} aria-hidden="true" />
            {mode === "live" ? "实时推送" : mode === "connecting" ? "正在连接" : "定时刷新"}
          </Badge>
        }
        actions={
          <Button variant="outline" size="sm" onClick={() => { refresh(); void today.refetch(); void alarms.refetch(); void workOrders.refetch(); }}>
            <RefreshCw aria-hidden="true" data-icon="inline-start" />
            刷新
          </Button>
        }
      />

      <MetricGrid ariaLabel="冷站概况">
        <MetricCard
          label="冷站实时功率"
          value={<ReadingMetric reading={plant.totalPower} digits={1} unit="kW" />}
          lead={coolingPresent ? `瞬时制冷量 ${decimal(cooling.numeric!, 0)} kW${cooling.current ? "" : "（数据过期）"}` : "瞬时制冷量暂无数据"}
          detail={asOf(plant.latestSampleAt)}
        />
        <MetricCard
          label="今日空调用电"
          value={<MetricValue value={energy?.electricityKWh == null ? null : decimal(energy.electricityKWh, 1)} unit="kWh" />}
          lead={energy?.coolingKWh == null ? "供冷量暂无数据" : `供冷量 ${decimal(energy.coolingKWh, 1)} kWh`}
          detail={energy?.dataWatermark ? `截至 ${formatTime(energy.dataWatermark, site.timezone)}` : "站点当地时间今日"}
        />
        <MetricCard
          label="今日冷站综合能效"
          value={<MetricValue value={energy?.cop == null ? null : decimal(energy.cop, 2)} unit="COP" />}
          lead={plant.plantCop === null ? "实时能效暂无数据" : `实时 COP ${decimal(plant.plantCop, 2)}`}
          detail="供冷量 ÷ 空调用电"
        />
        <MetricCard
          label="主要设备运行"
          value={registry.isPending ? <Skeleton className="h-8 w-24" /> : registry.isError ? <span className="text-muted-foreground">暂不可用</span>
            : equipment.total === 0 ? <span className="text-muted-foreground">未登记设备</span>
            : <MetricValue value={`${equipment.running} / ${equipment.total}`} unit="台运行" />}
          badge={registry.isSuccess ? <Badge variant="outline">{plant.onlineCount} / {plant.devices.length} 在线</Badge> : undefined}
          lead={registry.isSuccess ? equipmentLead(equipment) : "正在读取设备台账"}
          detail={
            <Link to="/operations/systems-devices" search={{ site: site.id }} className="hover:text-foreground hover:underline">
              查看系统与设备
            </Link>
          }
        />
      </MetricGrid>

      <EnergyTrend period={period} onPeriodChange={(next) => void navigate({ search: (previous) => ({ ...previous, period: next }) })} />

      <div className="grid gap-4 md:gap-6 @5xl/main:grid-cols-2">
        <ListCard
          title="未结告警"
          description={alarms.data ? `${activeAlarms.length} 条活动告警` : "正在读取告警"}
          href="/operations/alarms"
          siteId={site.id}
        >
          {alarms.isPending ? <Skeleton className="h-40" /> : alarms.isError ? (
            <p className="py-10 text-center text-sm text-muted-foreground">告警暂不可用</p>
          ) : activeAlarms.length === 0 ? <EmptyList text="当前没有活动告警" /> : (
            <ItemGroup className="gap-1">
              {activeAlarms.slice(0, LIST_LIMIT).map((alarm) => (
                <div role="listitem" key={alarm.alarmId}>
                  <Item size="sm" asChild className="hover:bg-muted/60">
                    <Link to="/operations/alarms" search={{ site: site.id, inspect: alarm.alarmId }}>
                      <SeverityBadge severity={alarm.currentSeverity} />
                      <ItemContent>
                        <ItemTitle className="truncate">{alarm.title}</ItemTitle>
                        <ItemDescription className="truncate text-xs">
                          {alarm.deviceId ? deviceNames.get(alarm.deviceId) ?? "—" : "站点"} · {formatTime(alarm.firstOccurredAt, site.timezone)} 开始
                        </ItemDescription>
                      </ItemContent>
                      <ItemActions className="text-xs text-muted-foreground">{alarm.acknowledgement ? "已确认" : "未确认"}</ItemActions>
                    </Link>
                  </Item>
                </div>
              ))}
            </ItemGroup>
          )}
        </ListCard>
        <ListCard
          title="待办工单"
          description={workOrders.data ? `${openOrders.length} 张未完成工单` : "正在读取工单"}
          href="/operations/work-center"
          siteId={site.id}
        >
          {workOrders.isPending ? <Skeleton className="h-40" /> : workOrders.isError ? (
            <p className="py-10 text-center text-sm text-muted-foreground">工单暂不可用</p>
          ) : openOrders.length === 0 ? <EmptyList text="当前没有未完成工单" /> : (
            <ItemGroup className="gap-1">
              {openOrders.slice(0, LIST_LIMIT).map((order) => (
                <div role="listitem" key={order.workOrderId}>
                  <Item size="sm" asChild className="hover:bg-muted/60">
                    <Link to="/operations/work-center" search={{ site: site.id, inspect: order.workOrderId }}>
                      <PriorityBadge priority={order.priority} />
                      <ItemContent>
                        <ItemTitle className="truncate">{order.title}</ItemTitle>
                        <ItemDescription className="truncate text-xs">
                          {order.assigneeId ? personLabel(order.assigneeId, principal.principalId) : "未指派"} · {formatTime(order.createdAt, site.timezone)} 创建
                        </ItemDescription>
                      </ItemContent>
                      <ItemActions className="text-xs text-muted-foreground">{STATUS_LABELS[order.status]}</ItemActions>
                    </Link>
                  </Item>
                </div>
              ))}
            </ItemGroup>
          )}
        </ListCard>
      </div>

      <section aria-label="节能成效" className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-dashed px-4 py-3 text-sm">
        <span className="font-medium">节能量、节能率与节约费用</span>
        <span className="text-muted-foreground">未接入：需要基线模型与电价，接入后在此显示</span>
      </section>
    </Main>
  );
}
