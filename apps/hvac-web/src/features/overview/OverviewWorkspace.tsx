import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, getRouteApi, useNavigate } from "@tanstack/react-router";
import { ArrowUpRight, CircleCheck, PiggyBank, RefreshCw } from "lucide-react";
import type { ReactNode } from "react";
import { HeatmapChartCard } from "@/components/heatmap-chart-card";
import { MetricTabsChartCard } from "@/components/metric-tabs-chart-card";
import { PageHeader } from "@/blocks/page-header";
import { Main } from "@/components/layout/Main";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty";
import { Item, ItemActions, ItemContent, ItemDescription, ItemGroup, ItemTitle } from "@/components/ui/item";
import { Separator } from "@/components/ui/separator";
import { Skeleton } from "@/components/ui/skeleton";
import { useDeviceNames } from "@/features/assets/use-device-names";
import { activeAlarmsQuery } from "@/features/alarms/alarm-api";
import { SeverityBadge, sortAlarmsForOperators } from "@/features/alarms/alarm-presentation";
import { completeOnly, relativeChange } from "@/features/energy-analysis/consumption/model";
import { useHourlyHeatmap, useTodayAgainstYesterday } from "@/features/energy-analysis/consumption/query";
import { ReadingMetric, ReadingValue } from "@/features/operations/realtime/device-presentation";
import { useRealtimePlant } from "@/features/operations/realtime/use-realtime-plant";
import {
  EQUIPMENT_CATEGORIES,
  equipmentStatus,
  isRunning,
  plantReading,
  type EquipmentStatus,
  type PlantDevice,
  type PlantView,
} from "@/features/operations/realtime/plant-model";
import { PriorityBadge, sortWorkOrdersForOperators, STATUS_LABELS } from "@/features/work-orders/work-order-presentation";
import { openWorkOrdersQuery } from "@/features/work-orders/work-order-queries";
import { formatDecimal, formatTime, personLabel } from "@/lib/operator-format";
import { cn } from "@/lib/utils";

const siteRoute = getRouteApi("/_app/_site");
const pageRoute = getRouteApi("/_app/_site/overview");
const LIST_LIMIT = 5;
const known = (value: number | null, digits: number) => (value === null ? null : formatDecimal(value, digits));
const HEATMAP_DAYS = 7;
const HOURS = Array.from({ length: 24 }, (_, hour) => `${hour}时`);

type OverviewMetric = "electricityKWh" | "coolingKWh" | "cop";

function TodayAgainstYesterday({ metric, onMetricChange }: { readonly metric: OverviewMetric; readonly onMetricChange: (metric: OverviewMetric) => void }) {
  const comparison = useTodayAgainstYesterday();
  if (comparison.isPending) return <Skeleton className="h-[372px] rounded-xl" />;
  if (comparison.isError) {
    return (
      <Card className="justify-center">
        <CardContent className="py-16 text-center text-sm text-muted-foreground">今日能耗暂不可用</CardContent>
      </Card>
    );
  }
  const { today, yesterday, hours } = comparison.data;
  return (
    <MetricTabsChartCard
      role="region"
      aria-label="今日用能"
      data={completeOnly(hours).map((hour) => ({ ...hour }))}
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
      chartClassName="h-[260px]"
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
      ]}
    />
  );
}

const EQUIPMENT_TONE = {
  running: "bg-chart-1",
  fault: "bg-destructive",
  offline: "border border-dashed border-muted-foreground/60",
  stopped: "bg-muted-foreground/25",
} as const;

function equipmentTone(device: PlantDevice): keyof typeof EQUIPMENT_TONE {
  if (device.connection === "OFFLINE") return "offline";
  if (device.runState === "FAULT") return "fault";
  return isRunning(device) ? "running" : "stopped";
}

function equipmentLead(status: EquipmentStatus): string {
  const issues = [status.faults > 0 ? `${status.faults} 台故障` : null, status.offline > 0 ? `${status.offline} 台离线` : null].filter(Boolean);
  return issues.length > 0 ? issues.join("，") : "没有故障或离线设备";
}

function Fact({ label, children }: { readonly label: string; readonly children: ReactNode }) {
  return (
    <div className="flex flex-col gap-1">
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="text-lg font-semibold tracking-tight">{children}</dd>
    </div>
  );
}

function LivePlant({ plant, registryState, siteId, timeZone }: {
  readonly plant: PlantView;
  readonly registryState: "pending" | "error" | "success";
  readonly siteId: string;
  readonly timeZone: string;
}) {
  const status = equipmentStatus(plant);
  const equipment = plant.devices.filter((device) => EQUIPMENT_CATEGORIES.has(device.category));
  return (
    <Card aria-label="冷站实时" role="region" className="@container/card">
      <CardHeader>
        <CardDescription>冷站实时功率</CardDescription>
        <CardTitle className="text-3xl font-semibold tabular-nums">
          <ReadingMetric reading={plant.totalPower} digits={1} unit="kW" />
        </CardTitle>
        {registryState === "success" ? (
          <CardAction>
            <Badge variant="outline" className="font-normal">{plant.onlineCount} / {plant.devices.length} 在线</Badge>
          </CardAction>
        ) : null}
      </CardHeader>
      <CardContent className="flex flex-1 flex-col gap-5">
        <dl className="grid grid-cols-2 gap-x-6 gap-y-4">
          <Fact label="瞬时制冷量"><ReadingValue reading={plant.coolingCapacity} /></Fact>
          <Fact label="实时 COP">
            {plant.plantCop === null ? <span className="text-muted-foreground">—</span> : <span className="tabular-nums">{formatDecimal(plant.plantCop, 2)}</span>}
          </Fact>
          <Fact label="冷冻水供水"><ReadingValue reading={plantReading(plant, "btu_meter.supply_water_temperature")} /></Fact>
          <Fact label="冷冻水回水"><ReadingValue reading={plantReading(plant, "btu_meter.return_water_temperature")} /></Fact>
        </dl>
        <Separator />
        <section aria-label="主要设备" className="flex flex-col gap-2.5">
          <div className="flex items-baseline justify-between gap-3 text-sm">
            <span className="text-muted-foreground">主要设备</span>
            {registryState === "pending" ? <Skeleton className="h-4 w-16" />
              : registryState === "error" ? <span className="text-muted-foreground">暂不可用</span>
              : status.total === 0 ? <span className="text-muted-foreground">未登记设备</span>
              : <span className="font-medium tabular-nums">{status.running} / {status.total} 台运行</span>}
          </div>
          {equipment.length > 0 ? (
            <div className="flex gap-1" aria-hidden="true">
              {equipment.map((device) => (
                <span key={device.deviceId} title={device.name} className={cn("h-2 flex-1 rounded-full", EQUIPMENT_TONE[equipmentTone(device)])} />
              ))}
            </div>
          ) : null}
          {registryState === "success" ? <p className="text-xs text-muted-foreground">{equipmentLead(status)}</p> : null}
        </section>
      </CardContent>
      <CardFooter className="justify-between gap-3 border-t text-xs text-muted-foreground">
        <span>{plant.latestSampleAt ? `数据时间 ${formatTime(plant.latestSampleAt, timeZone)}` : "等待数据"}</span>
        <Link to="/operations/realtime" search={{ site: siteId }} className="inline-flex items-center gap-1 font-medium text-foreground hover:underline">
          实时监测
          <ArrowUpRight className="size-3.5" aria-hidden="true" />
        </Link>
      </CardFooter>
    </Card>
  );
}

function HourlyElectricity() {
  const heatmap = useHourlyHeatmap(HEATMAP_DAYS);
  if (heatmap.isPending) return <Skeleton className="h-[340px] rounded-xl" />;
  if (heatmap.isError) {
    return (
      <Card className="justify-center">
        <CardContent className="py-16 text-center text-sm text-muted-foreground">逐时用电暂不可用</CardContent>
      </Card>
    );
  }
  return (
    <HeatmapChartCard
      role="region"
      aria-label="近 7 天逐时用电"
      title={`近 ${HEATMAP_DAYS} 天空调用电`}
      deltaLabel="按站点当地时间，每格为一小时；悬停查看该小时用电"
      rows={heatmap.data.map((day) => ({ label: day.label, values: day.values }))}
      columns={HOURS}
      columnLabelEvery={3}
      color="var(--chart-1)"
      unit="kWh"
      valueFormatter={(value) => formatDecimal(value, 1)}
    />
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
    <Empty className="py-8">
      <EmptyHeader>
        <EmptyMedia variant="icon"><CircleCheck /></EmptyMedia>
        <EmptyTitle className="text-sm">{text}</EmptyTitle>
      </EmptyHeader>
    </Empty>
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

  const activeAlarms = sortAlarmsForOperators(alarms.data?.items ?? []);
  const openOrders = sortWorkOrdersForOperators(workOrders.data ?? []);

  return (
    <Main className="@container/main flex flex-col gap-4 md:gap-6">
      <PageHeader
        title="总览"
        description={`${site.displayName} · 冷站运行与今日用能`}
        meta={
          <Badge variant="outline" className="gap-1.5 font-normal">
            <span className={mode === "live" ? "size-1.5 rounded-full bg-success" : "size-1.5 rounded-full bg-muted-foreground"} aria-hidden="true" />
            {mode === "live" ? "实时推送" : mode === "connecting" ? "正在连接" : "定时刷新"}
          </Badge>
        }
        actions={
          <Button
            variant="outline"
            size="sm"
            onClick={() => {
              refresh();
              void queryClient.invalidateQueries({ queryKey: ["energy-series", site.id] });
              void alarms.refetch();
              void workOrders.refetch();
            }}
          >
            <RefreshCw aria-hidden="true" data-icon="inline-start" />
            刷新
          </Button>
        }
      />

      <div className="grid gap-4 md:gap-6 @5xl/main:grid-cols-3">
        <div className="min-w-0 @5xl/main:col-span-2">
          <TodayAgainstYesterday
            metric={search.metric ?? "electricityKWh"}
            onMetricChange={(metric) => void navigate({ search: (previous) => ({ ...previous, metric }), replace: true })}
          />
        </div>
        <LivePlant plant={plant} registryState={registry.status} siteId={site.id} timeZone={site.timezone} />
      </div>

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

      <div className="grid gap-4 md:gap-6 @5xl/main:grid-cols-3">
        <div className="min-w-0 @5xl/main:col-span-2">
          <HourlyElectricity />
        </div>
        <Card role="region" aria-label="节能成效" className="justify-center">
          <Empty>
            <EmptyHeader>
              <EmptyMedia variant="icon"><PiggyBank /></EmptyMedia>
              <EmptyTitle>节能成效未接入</EmptyTitle>
              <EmptyDescription>节能量、节能率与节约费用需要基线模型与电价，接入后在此显示。</EmptyDescription>
            </EmptyHeader>
          </Empty>
        </Card>
      </div>
    </Main>
  );
}
