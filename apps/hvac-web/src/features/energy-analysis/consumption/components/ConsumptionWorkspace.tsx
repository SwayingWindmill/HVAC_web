import { ArrowUpRight, ChartColumn, ChartLine, Download, List, RefreshCw } from "lucide-react";
import { ChartLegend, ChartLegendContent } from "@/components/ui/chart";
import { DataTableBlock } from "@/blocks/data-table";
import { useState } from "react";
import { useNavigate, useSearch } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import {
  Bar,
  BarChart,
  Line,
  ComposedChart,
  CartesianGrid,
  XAxis,
  YAxis,
} from "recharts";
import { useWorkspaceScope } from "@/hooks/use-scope";
import { useDataTable } from "@/hooks/use-data-table";
import { DataTable } from "@/components/data-table/data-table";
import type { ColumnDef } from "@tanstack/react-table";
import type { DataTableFeatures } from "@/components/data-table/data-table-features";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Card, CardHeader, CardTitle, CardContent } from "@/components/ui/card";
import {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
} from "@/components/ui/chart";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import {
  Select,
  SelectTrigger,
  SelectValue,
  SelectContent,
  SelectItem,
} from "@/components/ui/select";
import {
  Sheet,
  SheetHeader,
  SheetTitle,
  SheetDescription,
  SheetContent,
} from "@/components/ui/sheet";
import { Alert, AlertTitle, AlertDescription } from "@/components/ui/alert";
import { Skeleton } from "@/components/ui/skeleton";
import { createConsumptionExample } from "../api/consumption-example";
import type { DailyConsumptionRecord } from "../api/consumption-types";
const example = __HVAC_WEB_FRONTEND_REVIEW__;
const number = (value: number, digits = 1) =>
  value.toLocaleString("zh-CN", { maximumFractionDigits: digits });
export function ConsumptionWorkspace() {
  const { currentScope } = useWorkspaceScope();
  const search = useSearch({ from: "/_app/_site/energy-analysis/consumption" });
  const navigate = useNavigate({ from: "/energy-analysis/consumption" });
  const period = search.period ?? "current-month",
    view = search.view ?? "energy";
  const [dayId, setDayId] = useState<string | null>(null);
  const query = useQuery({
    queryKey: ["consumption-review", currentScope.id, period],
    queryFn: async () => {
      if (!example) throw new Error("能耗与成本聚合尚未接入");
      return createConsumptionExample(period, currentScope.id);
    },
    retry: false,
  });
  const data = query.data;
  const day = data?.dailyRecords.find((item) => item.id === dayId);
  const update = (next: Partial<typeof search>) => {
    setDayId(null);
    void navigate({ search: (previous) => ({ ...previous, ...next }) });
  };
  const columns: ColumnDef<DataTableFeatures, DailyConsumptionRecord>[] = [
    {
      id: "date",
      header: "日期",
      accessorFn: (row) => row.date,
      cell: ({ row }) => (
        <span>
          {row.original.date}{" "}
          <span className="ml-2 text-xs text-muted-foreground">
            {row.original.dayOfWeek}
          </span>
        </span>
      ),
    },
    {
      id: "energy",
      header: "用电量（kWh）",
      cell: ({ row }) => (
        <span className="tabular-nums">{number(row.original.totalKWh)}</span>
      ),
    },
    {
      id: "cost",
      header: "电费（元）",
      cell: ({ row }) => (
        <span className="font-medium tabular-nums">
          {number(row.original.totalCostCNY, 2)}
        </span>
      ),
    },
    {
      id: "price",
      header: "均价（元/kWh）",
      cell: ({ row }) => number(row.original.averageRate, 3),
    },
    {
      id: "temperature",
      header: "室外均温",
      cell: ({ row }) => number(row.original.outdoorTempAvg) + " °C",
    },
    {
      id: "actions",
      header: "",
      cell: ({ row }) => (
        <Button
          variant="ghost"
          size="sm"
          onClick={() => setDayId(row.original.id)}
        >
          <ArrowUpRight aria-hidden="true" data-icon="inline-start" />查看分时
        </Button>
      ),
    },
  ];
  const table = useDataTable({
    key: "consumption-ledger",
    data: [...(data?.dailyRecords ?? [])],
    columns,
    paginate: false,
    getRowId: (item) => item.id,
  });
  const exportCsv = () => {
    if (!data) return;
    const rows = [
      ["能耗与成本 · 示例数据", data.periodRange.label, currentScope.name],
      ["日期", "用电量 kWh", "费用 元"],
      ...data.dailyRecords.map((item) => [
        item.date,
        item.totalKWh,
        item.totalCostCNY,
      ]),
    ];
    const url = URL.createObjectURL(
      new Blob(["\uFEFF" + rows.map((row) => row.join(",")).join("\n")], {
        type: "text/csv;charset=utf-8",
      }),
    );
    const link = document.createElement("a");
    link.href = url;
    link.download = "能耗与成本.csv";
    link.click();
    URL.revokeObjectURL(url);
  };
  return (
    <main className="mx-auto max-w-[1600px] space-y-5 p-6">
      <header className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          <h1 className="text-2xl font-semibold tracking-tight">能耗与成本</h1>
          {example && <Badge variant="outline">示例数据</Badge>}
        </div>
        <div className="flex items-center gap-2">
          <Select
            value={period}
            onValueChange={(value) =>
              update({ period: value as typeof period })
            }
          >
            <SelectTrigger className="w-36" aria-label="分析期间">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="current-month">本月</SelectItem>
              <SelectItem value="last-month">上月</SelectItem>
              <SelectItem value="quarter">本季度</SelectItem>
              <SelectItem value="year">本年迄今</SelectItem>
            </SelectContent>
          </Select>
          <Button
            variant="outline"
            size="sm"
            aria-label="刷新能耗"
            onClick={() => void query.refetch()}
          >
            <RefreshCw />
          </Button>
          <Button
            variant="outline"
            size="sm"
            disabled={!data}
            onClick={exportCsv}
          >
            <Download aria-hidden="true" data-icon="inline-start" />导出明细
          </Button>
        </div>
      </header>
      {query.isPending ? (
        <Skeleton className="h-96" />
      ) : query.isError ? (
        <Alert variant="destructive">
          <AlertTitle>分析数据不可用</AlertTitle>
          <AlertDescription>
            当前范围的期间聚合尚未接入。
            <Button variant="link" onClick={() => void query.refetch()}>
              重试
            </Button>
          </AlertDescription>
        </Alert>
      ) : (
        data && (
          <>
            <p className="text-xs">
              {data.periodRange.start} – {data.periodRange.end} / 电力
            </p>
            <section
              aria-label="用能与费用概况"
              className="grid grid-cols-4 divide-x rounded-xl border bg-card"
            >
              {[
                {
                  label: "累计用电",
                  value: data.summary.totalEnergyKWh / 1000,
                  unit: "MWh",
                },
                {
                  label: "电量费用",
                  value: data.summary.totalCostCNY / 10000,
                  unit: "万元",
                },
                {
                  label: "平均电价",
                  value: data.summary.averageUnitCost,
                  unit: "元/kWh",
                },
                {
                  label: "峰段费用占比",
                  value: data.summary.peakTariffShare,
                  unit: "%",
                },
              ].map((item) => (
                <div key={item.label} className="space-y-3 p-5">
                  <p className="text-sm">{item.label}</p>
                  <p
                    className="text-[30px] font-semibold tabular-nums"
                    data-testid={"consumption-" + item.label}
                  >
                    {number(item.value, item.unit === "元/kWh" ? 3 : 1)}
                    <span className="ml-2 text-xs font-normal text-muted-foreground">
                      {item.unit}
                    </span>
                  </p>
                </div>
              ))}
            </section>
            <Tabs
              value={view}
              onValueChange={(value) => update({ view: value as typeof view })}
            >
              <TabsList>
                <TabsTrigger value="energy" className="inline-flex items-center gap-2"><ChartLine className="size-4" aria-hidden="true" />用电趋势</TabsTrigger>
                <TabsTrigger value="cost" className="inline-flex items-center gap-2"><ChartColumn className="size-4" aria-hidden="true" />成本分析</TabsTrigger>
                <TabsTrigger value="ledger" className="inline-flex items-center gap-2"><List className="size-4" aria-hidden="true" />逐日明细</TabsTrigger>
              </TabsList>
            </Tabs>
            {view !== "ledger" ? (
              <>
                <Card>
                  <CardHeader>
                    <CardTitle>
                      {view === "cost" ? "逐日电费" : "日用电量与基线"}
                    </CardTitle>
                  </CardHeader>
                  <CardContent>
                    <ChartContainer
                      className="h-[280px] w-full aspect-auto"
                      config={{
                        totalKWh: {
                          label: "实际（kWh）",
                          color: "var(--chart-1)",
                        },
                        baselineKWh: {
                          label: "基线（kWh）",
                          color: "var(--muted-foreground)",
                        },
                        totalCostCNY: {
                          label: "费用（元）",
                          color: "var(--chart-2)",
                        },
                      }}
                    >
                      <ComposedChart
                        accessibilityLayer
                        data={data.dailyRecords}
                        margin={{ left: 10, right: 18 }}
                      >
                        <CartesianGrid vertical={false} />
                        <XAxis
                          dataKey="date"
                          tickFormatter={(value) => value.slice(5)}
                          axisLine={false}
                          tickLine={false}
                          minTickGap={30}
                        />
                        <YAxis
                          axisLine={false}
                          tickLine={false}
                          tickFormatter={(value) => number(value / 1000) + "k"}
                        />
                        <ChartTooltip content={<ChartTooltipContent />} />
                        <Bar
                          isAnimationActive={false}
                          dataKey={
                            view === "cost" ? "totalCostCNY" : "totalKWh"
                          }
                          fill={
                            view === "cost"
                              ? "var(--color-totalCostCNY)"
                              : "var(--color-totalKWh)"
                          }
                          radius={[3, 3, 0, 0]}
                        />
                        {view === "energy" && (
                          <Line
                            isAnimationActive={false}
                            dataKey="baselineKWh"
                            stroke="var(--color-baselineKWh)"
                            strokeDasharray="4 4"
                            dot={false}
                          />
                        )}
                        <ChartLegend content={<ChartLegendContent />} />
                      </ComposedChart>
                    </ChartContainer>
                  </CardContent>
                </Card>
                <div className="grid grid-cols-2 gap-5">
                  <Card>
                    <CardHeader>
                      <CardTitle>分时电量与成本占比</CardTitle>
                    </CardHeader>
                    <CardContent>
                      <ChartContainer
                        className="h-[210px] w-full aspect-auto"
                        config={{
                          energyShare: {
                            label: "电量占比（%）",
                            color: "var(--chart-1)",
                          },
                          costShare: {
                            label: "费用占比（%）",
                            color: "var(--chart-2)",
                          },
                        }}
                      >
                        <BarChart accessibilityLayer data={data.touBreakdown}>
                          <CartesianGrid vertical={false} />
                          <XAxis
                            dataKey="period"
                            axisLine={false}
                            tickLine={false}
                          />
                          <YAxis unit="%" axisLine={false} tickLine={false} />
                          <ChartTooltip content={<ChartTooltipContent />} />
                          <Bar
                            isAnimationActive={false}
                            dataKey="energyShare"
                            fill="var(--color-energyShare)"
                            radius={[4, 4, 0, 0]}
                          />
                          <Bar
                            isAnimationActive={false}
                            dataKey="costShare"
                            fill="var(--color-costShare)"
                            radius={[4, 4, 0, 0]}
                          />
                        </BarChart>
                      </ChartContainer>
                      <div className="mt-2 flex justify-center gap-6 text-xs">
                        <span style={{ color: "var(--chart-1)" }}>
                          电量占比
                        </span>
                        <span style={{ color: "var(--chart-2)" }}>
                          费用占比
                        </span>
                      </div>
                    </CardContent>
                  </Card>
                  <Card>
                    <CardHeader>
                      <CardTitle>子系统费用</CardTitle>
                    </CardHeader>
                    <CardContent>
                      <ChartContainer
                        className="h-[210px] w-full aspect-auto"
                        config={{
                          costCNY: {
                            label: "费用（元）",
                            color: "var(--chart-1)",
                          },
                        }}
                      >
                        <BarChart
                          accessibilityLayer
                          layout="vertical"
                          data={data.subsystemCosts}
                          margin={{ right: 20 }}
                        >
                          <CartesianGrid horizontal={false} />
                          <XAxis
                            type="number"
                            tickFormatter={(value) =>
                              number(value / 10000) + "万"
                            }
                            axisLine={false}
                            tickLine={false}
                          />
                          <YAxis
                            type="category"
                            dataKey="name"
                            width={76}
                            axisLine={false}
                            tickLine={false}
                          />
                          <ChartTooltip content={<ChartTooltipContent />} />
                          <Bar
                            isAnimationActive={false}
                            dataKey="costCNY"
                            fill="var(--color-costCNY)"
                            radius={4}
                            barSize={22}
                          />
                        </BarChart>
                      </ChartContainer>
                    </CardContent>
                  </Card>
                </div>
              </>
            ) : (
              <DataTableBlock>
                <DataTable table={table} tableAriaLabel="逐日用能明细" />
              </DataTableBlock>
            )}
          </>
        )
      )}
      <Sheet
        modal={false}
        open={!!day}
        onOpenChange={(open) => {
          if (!open) setDayId(null);
        }}
      >
        <SheetContent
          showOverlay={false}
          className="w-[440px] sm:max-w-[440px]"
        >
          <SheetHeader>
            <SheetTitle>{day?.date} 分时用能</SheetTitle>
            <SheetDescription>{currentScope.name}</SheetDescription>
          </SheetHeader>
          {day && (
            <dl className="m-4 divide-y">
              {[
                { name: "尖峰", energy: day.sharpKWh, rate: 1.485 },
                { name: "高峰", energy: day.peakKWh, rate: 1.12 },
                { name: "平段", energy: day.flatKWh, rate: 0.76 },
                { name: "低谷", energy: day.valleyKWh, rate: 0.38 },
              ].map((item) => (
                <div key={item.name} className="flex justify-between py-4">
                  <dt>
                    {item.name}
                    <p className="mt-1 text-xs text-muted-foreground">
                      {item.rate} 元/kWh
                    </p>
                  </dt>
                  <dd className="text-right tabular-nums">
                    {number(item.energy)} kWh
                    <p className="mt-1 text-sm">
                      ¥ {number(item.energy * item.rate, 2)}
                    </p>
                  </dd>
                </div>
              ))}
            </dl>
          )}
        </SheetContent>
      </Sheet>
    </main>
  );
}
