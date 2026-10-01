import { ChartColumn, Fan, RefreshCw, Server, SlidersHorizontal, Snowflake, Waves } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { useSearch, useNavigate } from "@tanstack/react-router";
import { useScope } from "@/hooks/use-scope";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";

import { Card, CardHeader, CardTitle, CardContent } from "@/components/ui/card";
import { Tabs, TabsList, TabsTrigger, TabsContent } from "@/components/ui/tabs";

import {
  Table,
  TableHeader,
  TableBody,
  TableHead,
  TableRow,
  TableCell,
} from "@/components/ui/table";
import {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
} from "@/components/ui/chart";
import { BarChart, Bar, XAxis, YAxis, CartesianGrid } from "recharts";
import {
  WorkspaceHeader,
  MetricStrip,
  WorkspaceQueryState,
  isWorkspaceExample,
} from "@/components/analysis/workspace-parts";

import { realtimeService } from "../api/realtime-service";
const status = {
  RUNNING: "运行",
  STANDBY: "待机",
  FAULT: "故障",
  MAINTENANCE: "检修",
  OFFLINE: "离线",
};
export function RealtimeWorkspace() {
  const { currentScope } = useScope();
  const search = useSearch({ from: "/_app/operations/realtime" });
  const navigate = useNavigate({ from: "/operations/realtime" });
  const query = useQuery({
    queryKey: ["realtime-snapshot-review", currentScope.id],
    queryFn: async () => {
      if (!isWorkspaceExample || currentScope.id !== "site:site-01")
        throw new Error("运行快照未接入");
      return realtimeService.getRealtimeOperations();
    },
    retry: false,
  });
  const data = query.data;
  return (
    <main className="mx-auto max-w-[1600px] space-y-5 p-6">
      <WorkspaceHeader
        title="实时运行"
        actions={
          <Button
            variant="outline"
            size="sm"
            onClick={() => void query.refetch()}
          >
            <RefreshCw aria-hidden="true" data-icon="inline-start" />刷新快照
          </Button>
        }
      />
      <WorkspaceQueryState pending={query.isPending} error={query.error} />
      {data && (
        <>
          <MetricStrip
            items={[
              {
                label: "冷站功率",
                value: data.summary.instantPowerKW,
                unit: "kW",
              },
              { label: "系统 COP", value: data.summary.instantCop },
              {
                label: "冷负荷率",
                value: data.summary.coolingLoadPercent,
                unit: "%",
              },
              {
                label: "设备故障",
                value: data.summary.activeFaultCount,
                unit: "项",
              },
            ]}
          />
          <Tabs
            value={search.view ?? "plant"}
            onValueChange={(view) =>
              void navigate({
                search: (prev) => ({
                  ...prev,
                  view: view as "plant" | "loops",
                }),
              })
            }
          >
            <TabsList>
              <TabsTrigger value="plant" className="inline-flex items-center gap-2"><Snowflake className="size-4" aria-hidden="true" />冷站工况</TabsTrigger>
              <TabsTrigger value="loops" className="inline-flex items-center gap-2"><SlidersHorizontal className="size-4" aria-hidden="true" />控制回路</TabsTrigger>
            </TabsList>
            <TabsContent value="plant" className="space-y-5">
              <div className="grid grid-cols-12 gap-5">
                <Card className="col-span-8">
                  <CardHeader>
                    <CardTitle className="flex items-center gap-2"><ChartColumn className="size-4" aria-hidden="true" />冷机负荷分配</CardTitle>
                  </CardHeader>
                  <CardContent>
                    <ChartContainer
                      className="h-[260px] w-full"
                      config={{
                        loadPercent: {
                          label: "负荷率",
                          color: "var(--chart-1)",
                        },
                      }}
                    >
                      <BarChart data={[...data.chillers]} accessibilityLayer>
                        <CartesianGrid vertical={false} />
                        <XAxis
                          dataKey="name"
                          tickLine={false}
                          axisLine={false}
                        />
                        <YAxis
                          unit="%"
                          domain={[0, 100]}
                          tickLine={false}
                          axisLine={false}
                        />
                        <ChartTooltip content={<ChartTooltipContent />} />
                        <Bar
                          isAnimationActive={false}
                          dataKey="loadPercent"
                          fill="var(--color-loadPercent)"
                          radius={[4, 4, 0, 0]}
                          maxBarSize={72}
                        />
                      </BarChart>
                    </ChartContainer>
                  </CardContent>
                </Card>
                <Card className="col-span-4">
                  <CardHeader>
                    <CardTitle className="flex items-center gap-2"><Waves className="size-4" aria-hidden="true" />水侧工况</CardTitle>
                  </CardHeader>
                  <CardContent className="space-y-6">
                    <section>
                      <h3 className="text-sm font-medium">冷冻水 · °C</h3>
                      <div className="mt-3 flex items-center justify-between">
                        <div>
                          <p className="text-xs text-muted-foreground">供水</p>
                          <p className="text-3xl font-semibold tabular-nums">
                            {data.summary.chwSupplyTemp}
                          </p>
                        </div>
                        <div className="text-center text-sm">
                          温差
                          <br />
                          <strong>
                            {(
                              data.summary.chwReturnTemp -
                              data.summary.chwSupplyTemp
                            ).toFixed(1)}
                          </strong>
                        </div>
                        <div className="text-right">
                          <p className="text-xs text-muted-foreground">回水</p>
                          <p className="text-3xl font-semibold tabular-nums">
                            {data.summary.chwReturnTemp}
                          </p>
                        </div>
                      </div>
                    </section>
                    <section className="border-t pt-4">
                      <h3 className="text-sm font-medium">冷却水 · °C</h3>
                      <div className="mt-2 flex justify-between text-xl font-semibold tabular-nums">
                        <span><span className="mr-2 text-sm font-normal">供水</span>{data.summary.cwSupplyTemp}</span>
                        <span><span className="mr-2 text-sm font-normal">回水</span>{data.summary.cwReturnTemp}</span>
                      </div>
                    </section>
                    <div className="flex justify-between border-t pt-4 text-sm">
                      <span>系统压差</span>
                      <strong>{data.summary.systemDeltaPMPa} MPa</strong>
                    </div>
                  </CardContent>
                </Card>
              </div>
              <Card>
                <CardHeader>
                  <CardTitle className="flex items-center gap-2"><Server className="size-4" aria-hidden="true" />设备运行快照</CardTitle>
                </CardHeader>
                <CardContent>
                  <Table>
                    <TableHeader>
                      <TableRow>
                        {[
                          "设备",
                          "状态",
                          "功率 · kW",
                          "负荷 / 频率",
                          "工况",
                        ].map((x) => (
                          <TableHead key={x}>{x}</TableHead>
                        ))}
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      <TableRow className="bg-muted/10"><TableCell colSpan={5}><span className="flex items-center gap-2 font-medium"><Snowflake className="size-4" aria-hidden="true" />冷水机组 <span className="ml-auto text-sm font-normal">{data.chillers.length} 台</span></span></TableCell></TableRow>
                      {data.chillers.map((item) => (
                        <TableRow key={item.id}>
                          <TableCell className="font-medium">
                            {item.name}
                          </TableCell>
                          <TableCell>
                            <Badge
                              variant={
                                item.status === "FAULT"
                                  ? "destructive"
                                  : "outline"
                              }
                            >
                              {status[item.status]}
                            </Badge>
                          </TableCell>
                          <TableCell>{item.powerKW}</TableCell>
                          <TableCell>{item.loadPercent}%</TableCell>
                          <TableCell>
                            {item.status === "RUNNING" && <>COP {item.cop} · </>}冷冻水 {item.chwSupplyTemp} /{" "}
                            {item.chwReturnTemp} °C
                          </TableCell>
                        </TableRow>
                      ))}
                      <TableRow className="bg-muted/10"><TableCell colSpan={5}><span className="flex items-center gap-2 font-medium"><Waves className="size-4" aria-hidden="true" />循环水泵 <span className="ml-auto text-sm font-normal">{data.chwPumps.length + data.cwPumps.length} 台</span></span></TableCell></TableRow>
                      {[...data.chwPumps, ...data.cwPumps].map((item) => (
                        <TableRow key={item.id}>
                          <TableCell className="font-medium">
                            {item.name}
                          </TableCell>
                          <TableCell>
                            <Badge variant="outline">
                              {status[item.status]}
                            </Badge>
                          </TableCell>
                          <TableCell>{item.powerKW}</TableCell>
                          <TableCell>{item.frequencyHz} Hz</TableCell>
                          <TableCell>
                            流量 {item.flowM3H} m³/h · 扬程 {item.headM} m
                          </TableCell>
                        </TableRow>
                      ))}
                      <TableRow className="bg-muted/10"><TableCell colSpan={5}><span className="flex items-center gap-2 font-medium"><Fan className="size-4" aria-hidden="true" />冷却塔 <span className="ml-auto text-sm font-normal">{data.towers.length} 台</span></span></TableCell></TableRow>
                      {data.towers.map((item) => (
                        <TableRow key={item.id}>
                          <TableCell className="font-medium">
                            {item.name}
                          </TableCell>
                          <TableCell>
                            <Badge variant="outline">
                              {status[item.status]}
                            </Badge>
                          </TableCell>
                          <TableCell>{item.powerKW}</TableCell>
                          <TableCell>{item.frequencyHz} Hz</TableCell>
                          <TableCell>
                            进 / 出水 {item.inletTemp} / {item.outletTemp} °C
                          </TableCell>
                        </TableRow>
                      ))}
                    </TableBody>
                  </Table>
                </CardContent>
              </Card>
            </TabsContent>
            <TabsContent value="loops">
              <Card>
                <CardHeader>
                  <CardTitle className="flex items-center gap-2"><SlidersHorizontal className="size-4" aria-hidden="true" />设定与反馈</CardTitle>
                </CardHeader>
                <CardContent>
                  <Table>
                    <TableHeader>
                      <TableRow>
                        {[
                          "回路",
                          "设定值",
                          "实际值",
                          "偏差",
                          "输出",
                          "模式",
                          "状态",
                        ].map((x) => (
                          <TableHead key={x}>{x}</TableHead>
                        ))}
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {data.controlLoops.map((loop) => (
                        <TableRow key={loop.id}>
                          <TableCell className="font-medium">
                            {loop.name}
                          </TableCell>
                          <TableCell>
                            {loop.setpoint} {loop.unit}
                          </TableCell>
                          <TableCell>
                            {loop.actualValue} {loop.unit}
                          </TableCell>
                          <TableCell>
                            {loop.deviation} {loop.unit}
                          </TableCell>
                          <TableCell>{loop.outputPercent}%</TableCell>
                          <TableCell>
                            {
                              { AUTO: "自动", MANUAL: "手动", CASCADE: "串级" }[
                                loop.mode
                              ]
                            }
                          </TableCell>
                          <TableCell>
                            <Badge
                              variant={
                                loop.status === "DEVIATING"
                                  ? "destructive"
                                  : "outline"
                              }
                            >
                              {
                                {
                                  TRACKING: "跟踪",
                                  STABLE: "稳定",
                                  DEVIATING: "偏离",
                                }[loop.status]
                              }
                            </Badge>
                          </TableCell>
                        </TableRow>
                      ))}
                    </TableBody>
                  </Table>
                </CardContent>
              </Card>
            </TabsContent>
          </Tabs>
        </>
      )}
    </main>
  );
}
