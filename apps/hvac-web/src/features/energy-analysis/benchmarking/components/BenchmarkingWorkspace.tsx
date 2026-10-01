import { ArrowUpRight, RefreshCw, Search } from "lucide-react";
import { ChartLegend, ChartLegendContent } from "@/components/ui/chart";
import { DataTableBlock } from "@/blocks/data-table";
import { useState } from "react";
import { useSearch, useNavigate } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import {
  LineChart,
  Line,
  ScatterChart,
  Scatter,
  CartesianGrid,
  XAxis,
  YAxis,
  ZAxis,
} from "recharts";
import { useWorkspaceScope } from "@/hooks/use-scope";
import { useDataTable } from "@/hooks/use-data-table";
import { DataTable } from "@/components/data-table/data-table";
import type { ColumnDef } from "@tanstack/react-table";
import type { DataTableFeatures } from "@/components/data-table/data-table-features";
import {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
} from "@/components/ui/chart";
import { Card, CardHeader, CardTitle, CardContent } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { InputGroup, InputGroupAddon, InputGroupInput } from "@/components/ui/input-group";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Alert, AlertTitle, AlertDescription } from "@/components/ui/alert";
import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
  SheetDescription,
} from "@/components/ui/sheet";
import { Skeleton } from "@/components/ui/skeleton";
import { benchmarkingService } from "../api/benchmarking-service";
import type { PeerBuildingRanking } from "../api/benchmarking-types";
const example = __HVAC_WEB_FRONTEND_REVIEW__;
export function BenchmarkingWorkspace() {
  const { currentScope } = useWorkspaceScope();
  const search = useSearch({ from: "/_app/_site/energy-analysis/benchmarking" });
  const navigate = useNavigate({ from: "/energy-analysis/benchmarking" });
  const view = search.view ?? "peers";
  const [peerId, setPeerId] = useState<string | null>(null);
  const query = useQuery({
    queryKey: ["benchmark-review", currentScope.id],
    queryFn: () => {
      if (!example)
        throw new Error("当前范围对标数据未接入");
      return benchmarkingService.getBenchmarkingAnalytics();
    },
    retry: false,
  });
  const data = query.data;
  const peer = data?.peers.find((item) => item.id === peerId);
  const peers = [...(data?.peers ?? [])]
    .filter((item) => !search.q || (item.name + item.city).includes(search.q))
    .sort((a, b) => a.annualEuiKWhM2 - b.annualEuiKWhM2);
  const columns: ColumnDef<DataTableFeatures, PeerBuildingRanking>[] = [
    {
      id: "name",
      header: "建筑",
      cell: ({ row }) => (
        <div className="py-2 font-medium">
          {row.original.name}{" "}
          {row.original.isCurrentSite && (
            <Badge variant="outline">当前站点</Badge>
          )}
          <p className="mt-1 text-xs font-normal text-muted-foreground">
            {row.original.city} · {row.original.buildingType}
          </p>
        </div>
      ),
    },
    {
      id: "area",
      header: "建筑面积",
      cell: ({ row }) =>
        row.original.grossFloorAreaM2.toLocaleString("zh-CN") + " m²",
    },
    {
      id: "eui",
      header: "用电强度（kWh/m²·年）",
      cell: ({ row }) => (
        <span className="font-medium tabular-nums">
          {row.original.annualEuiKWhM2}
        </span>
      ),
    },
    {
      id: "cop",
      header: "冷站 COP",
      cell: ({ row }) => row.original.systemCop,
    },
    {
      id: "action",
      header: "",
      cell: ({ row }) => (
        <Button
          variant="ghost"
          size="sm"
          onClick={() => setPeerId(row.original.id)}
        >
          <ArrowUpRight aria-hidden="true" data-icon="inline-start" />查看对象
        </Button>
      ),
    },
  ];
  const table = useDataTable({
    key: "peer-review",
    data: peers,
    columns,
    paginate: false,
    getRowId: (item) => item.id,
  });
  return (
    <main className="mx-auto max-w-[1600px] space-y-5 p-6">
      <header className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          <h1 className="text-2xl font-semibold">绩效与对标</h1>
          {example && <Badge variant="outline">示例数据</Badge>}
        </div>
        <Button
          variant="outline"
          size="sm"
          onClick={() => void query.refetch()}
        >
          <RefreshCw aria-hidden="true" data-icon="inline-start" />刷新
        </Button>
      </header>
      {query.isPending ? (
        <Skeleton className="h-96" />
      ) : query.isError ? (
        <Alert variant="destructive">
          <AlertTitle>对标数据不可用</AlertTitle>
          <AlertDescription>请选择已接入的站点范围。</AlertDescription>
        </Alert>
      ) : (
        data && (
          <>
            <section className="grid grid-cols-3 divide-x rounded-xl border bg-card">
              {[
                {
                  label: "当前用电强度",
                  value: data.summary.actualEuiKWhM2,
                  unit: "kWh/m²·年",
                },
                {
                  label: "冷站综合 COP",
                  value: data.summary.systemCop,
                  unit: "",
                },
                { label: "可比较建筑", value: data.peers.length, unit: "栋" },
              ].map((item) => (
                <div key={item.label} className="space-y-3 p-5">
                  <p className="text-sm">{item.label}</p>
                  <p className="text-[30px] font-semibold tabular-nums">
                    {item.value}
                    <span className="ml-2 text-xs font-normal text-muted-foreground">
                      {item.unit}
                    </span>
                  </p>
                </div>
              ))}
            </section>
            <Tabs
              value={view}
              onValueChange={(value) =>
                void navigate({
                  search: (previous) => ({
                    ...previous,
                    view: value as typeof view,
                  }),
                })
              }
            >
              <TabsList>
                <TabsTrigger value="peers" className="inline-flex items-center gap-2">同类建筑</TabsTrigger>
                <TabsTrigger value="weather" className="inline-flex items-center gap-2">气象调整</TabsTrigger>
              </TabsList>
            </Tabs>
            {view === "peers" ? (
              <>
                <div className="grid grid-cols-12 gap-5">
                  <Card className="col-span-7">
                    <CardHeader>
                      <CardTitle>用电强度与冷站效率</CardTitle>
                    </CardHeader>
                    <CardContent>
                      <ChartContainer
                        className="h-[370px] w-full aspect-auto"
                        config={{
                          peers: { label: "同类建筑", color: "var(--chart-1)" },
                          current: {
                            label: "当前站点",
                            color: "var(--chart-2)",
                          },
                        }}
                      >
                        <ScatterChart
                          accessibilityLayer
                          margin={{ left: 10, right: 20, bottom: 20 }}
                        >
                          <CartesianGrid strokeDasharray="3 3" />
                          <XAxis
                            type="number"
                            dataKey="annualEuiKWhM2"
                            name="年用电强度"
                            unit=" kWh/m²·年"
                            tickFormatter={(value: number) => value.toFixed(0)}
                            domain={["dataMin - 10", "dataMax + 10"]}
                          />
                          <YAxis
                            type="number"
                            dataKey="systemCop"
                            name="COP"
                            tickFormatter={(value: number) => value.toFixed(2)}
                            domain={["dataMin - 0.3", "dataMax + 0.3"]}
                          />
                          <ZAxis range={[70, 70]} />
                          <ChartTooltip content={<ChartTooltipContent />} />
                          <Scatter
                            isAnimationActive={false}
                            name="同类建筑"
                            data={data.peers.filter(
                              (item) => !item.isCurrentSite,
                            )}
                            fill="var(--color-peers)"
                          />
                          <Scatter
                            isAnimationActive={false}
                            name="当前站点"
                            data={data.peers.filter(
                              (item) => item.isCurrentSite,
                            )}
                            fill="var(--color-current)"
                          />
                        </ScatterChart>
                      </ChartContainer>
                    </CardContent>
                  </Card>
                  <Card className="col-span-5">
                    <CardHeader>
                      <CardTitle>工程指标与参考值</CardTitle>
                    </CardHeader>
                    <CardContent className="divide-y">
                      {data.standards.map((item) => (
                        <div key={item.metricName} className="py-3.5">
                          <p className="text-sm font-medium">
                            {item.metricName}
                          </p>
                          <div className="mt-2 flex justify-between text-sm tabular-nums">
                            <span>
                              {item.actualValue} {item.unit}
                            </span>
                            <span className="text-xs text-muted-foreground">
                              参考 {item.gbStandardValue} {item.unit}
                            </span>
                          </div>
                        </div>
                      ))}
                    </CardContent>
                  </Card>
                </div>
                <section className="space-y-4">
                  <InputGroup className="max-w-80"><InputGroupAddon><Search aria-hidden="true" /></InputGroupAddon><InputGroupInput
                    aria-label="搜索对标建筑"
                    value={search.q ?? ""}
                    placeholder="搜索建筑或城市"
                    onChange={(event) =>
                      void navigate({
                        search: (previous) => ({
                          ...previous,
                          q: event.target.value || undefined,
                        }),
                      })
                    }
                   /></InputGroup>
                  <DataTableBlock>
                    <DataTable
                      table={table}
                      tableAriaLabel="同类建筑对标明细"
                    />
                  </DataTableBlock>
                </section>
              </>
            ) : (
              <Card>
                <CardHeader>
                  <CardTitle>月用电与气象调整</CardTitle>
                </CardHeader>
                <CardContent>
                  <ChartContainer
                    className="h-[320px] w-full aspect-auto"
                    config={{
                      actualEnergyMWh: {
                        label: "实际（MWh）",
                        color: "var(--chart-1)",
                      },
                      normalizedEnergyMWh: {
                        label: "气象调整（MWh）",
                        color: "var(--chart-2)",
                      },
                    }}
                  >
                    <LineChart
                      accessibilityLayer
                      data={data.weatherRegression}
                      margin={{ left: 10, right: 20 }}
                    >
                      <CartesianGrid vertical={false} />
                      <XAxis
                        dataKey="month"
                        axisLine={false}
                        tickLine={false}
                      />
                      <YAxis
                        unit=" MWh"
                        width={75}
                        axisLine={false}
                        tickLine={false}
                      />
                      <ChartTooltip content={<ChartTooltipContent />} />
                      <Line
                        isAnimationActive={false}
                        dataKey="actualEnergyMWh"
                        stroke="var(--color-actualEnergyMWh)"
                        dot={false}
                      />
                      <Line
                        isAnimationActive={false}
                        dataKey="normalizedEnergyMWh"
                        stroke="var(--color-normalizedEnergyMWh)"
                        dot={false}
                        strokeDasharray="4 4"
                      />
                      <ChartLegend content={<ChartLegendContent />} />
                    </LineChart>
                  </ChartContainer>
                  <p className="mt-4 text-sm">
                    气象调整用于同条件比较，不能直接认定为实施节能收益。
                  </p>
                </CardContent>
              </Card>
            )}
          </>
        )
      )}
      <Sheet
        modal={false}
        open={!!peer}
        onOpenChange={(open) => {
          if (!open) setPeerId(null);
        }}
      >
        <SheetContent
          showOverlay={false}
          className="w-[440px] sm:max-w-[440px]"
        >
          <SheetHeader>
            <SheetTitle>{peer?.name}</SheetTitle>
            <SheetDescription>
              {peer?.city} · {peer?.buildingType}
            </SheetDescription>
          </SheetHeader>
          {peer && (
            <dl className="m-4 divide-y">
              {[
                {
                  label: "面积",
                  value: peer.grossFloorAreaM2.toLocaleString("zh-CN") + " m²",
                },
                {
                  label: "年用电强度",
                  value: peer.annualEuiKWhM2 + " kWh/m²·年",
                },
                { label: "冷站 COP", value: String(peer.systemCop) },
              ].map((item) => (
                <div
                  key={item.label}
                  className="flex justify-between py-4 text-sm"
                >
                  <dt>{item.label}</dt>
                  <dd className="font-medium tabular-nums">{item.value}</dd>
                </div>
              ))}
            </dl>
          )}
        </SheetContent>
      </Sheet>
    </main>
  );
}
