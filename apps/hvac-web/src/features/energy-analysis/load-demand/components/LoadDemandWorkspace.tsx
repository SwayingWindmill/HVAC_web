import { ArrowUpRight, RefreshCw } from "lucide-react";
import { useMemo, useState } from "react";
import { useSearch, useNavigate } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import type { EChartsOption } from "echarts";
import { useScope } from "@/hooks/use-scope";
import { EngineeringChart } from "@/components/analysis/EngineeringChart";
import { Card, CardHeader, CardTitle, CardContent } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Alert, AlertTitle, AlertDescription } from "@/components/ui/alert";
import { Skeleton } from "@/components/ui/skeleton";
const example = __HVAC_WEB_FRONTEND_REVIEW__ || import.meta.env.DEV;
import { demandService } from "../api/demand-service";
import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
  SheetDescription,
} from "@/components/ui/sheet";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
export function LoadDemandWorkspace() {
  const { currentScope } = useScope();
  const search = useSearch({ from: "/_app/energy-analysis/load-demand" });
  const navigate = useNavigate({ from: "/energy-analysis/load-demand" });
  const view = search.view ?? "profile";
  const [candidateId, setCandidateId] = useState<string | null>(null);
  const query = useQuery({
    queryKey: ["demand-review", currentScope.id],
    queryFn: () => {
      if (!example || currentScope.id !== "site:site-01")
        throw new Error("当前范围需量分析未接入");
      return demandService.getDemandAnalytics();
    },
    retry: false,
  });
  const data = query.data;
  const peak = data
    ? Math.max(...data.rollingDemand.map((point) => point.rolling15MinKW))
    : 0;
  const contract = data?.summary.contractDemandKW ?? 0;
  const candidate = data?.sheddingCandidates.find(
    (item) => item.id === candidateId,
  );
  const option = useMemo<EChartsOption>(
    () => ({
      color: ["#2563eb", "#10b981"],
      tooltip: { trigger: "axis" },
      legend: { top: 0 },
      grid: { left: 65, right: 32, top: 45, bottom: 70 },
      xAxis: {
        type: "category",
        data:
          view === "profile"
            ? data?.rollingDemand.map((point) => point.time)
            : data?.loadDuration.map((point) => point.percentile + "%"),
      },
      yAxis: {
        type: "value",
        name: "kW",
        ...(view === "profile"
          ? { max: Math.ceil(Math.max(contract, peak) / 500) * 500 }
          : {}),
      },
      dataZoom: [{ type: "inside" }, { type: "slider", bottom: 5, height: 20 }],
      series:
        view === "profile"
          ? [
              {
                name: "15分钟需量",
                type: "line",
                showSymbol: false,
                data: data?.rollingDemand.map((point) => point.rolling15MinKW),
                areaStyle: { opacity: 0.08 },
                markLine: {
                  symbol: "none",
                  data: [
                    {
                      name: "契约需量",
                      yAxis: contract,
                      lineStyle: { color: "#dc2626" },
                      label: {
                        formatter: "契约 {c} kW",
                        position: "insideEndTop",
                      },
                    },
                  ],
                },
              },
              {
                name: "瞬时负荷",
                type: "line",
                showSymbol: false,
                data: data?.rollingDemand.map((point) => point.actualDemandKW),
                lineStyle: { width: 1.5, type: "dashed" },
              },
            ]
          : [
              {
                name: "负荷",
                type: "line",
                showSymbol: false,
                data: data?.loadDuration.map((point) => point.loadKW),
                areaStyle: { opacity: 0.08 },
              },
            ],
    }),
    [data, view, contract, peak],
  );
  return (
    <main className="mx-auto max-w-[1600px] space-y-5 p-6">
      <header className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          <h1 className="text-2xl font-semibold">负荷与需量</h1>
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
          <AlertTitle>需量数据不可用</AlertTitle>
          <AlertDescription>请选择已接入的站点范围。</AlertDescription>
        </Alert>
      ) : (
        data && (
          <>
            <section className="grid grid-cols-4 divide-x rounded-xl border bg-card">
              {[
                { label: "曲线峰值需量", value: peak },
                { label: "契约需量", value: contract },
                { label: "峰值余量", value: contract - peak },
                {
                  label: "可评估削峰潜力",
                  value: data.sheddingCandidates
                    .filter((item) => item.flexibilityStatus === "READY")
                    .reduce((sum, item) => sum + item.sheddingPotentialKW, 0),
                },
              ].map((item) => (
                <div key={item.label} className="space-y-3 p-5">
                  <p className="text-sm">{item.label}</p>
                  <p className="text-[30px] font-semibold tabular-nums">
                    {item.value.toLocaleString("zh-CN")}
                    <span className="ml-2 text-xs font-normal text-muted-foreground">
                      kW
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
                <TabsTrigger value="profile" className="inline-flex items-center gap-2">需量曲线</TabsTrigger>
                <TabsTrigger value="duration" className="inline-flex items-center gap-2">负荷持续曲线</TabsTrigger>
              </TabsList>
            </Tabs>
            <Card>
              <CardHeader>
                <CardTitle>
                  {view === "profile"
                    ? "今日负荷与15分钟需量"
                    : "本月负荷持续分布"}
                </CardTitle>
              </CardHeader>
              <CardContent>
                <EngineeringChart
                  option={option}
                  label={
                    view === "profile"
                      ? "负荷与契约需量曲线"
                      : "负荷持续分布，横轴为持续时间比例"
                  }
                  height={340}
                />
              </CardContent>
            </Card>
            <Card>
              <CardHeader>
                <CardTitle>可评估的削峰资源</CardTitle>
              </CardHeader>
              <CardContent>
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>可调对象</TableHead>
                      <TableHead>削峰潜力</TableHead>
                      <TableHead>响应时间</TableHead>
                      <TableHead>可持续</TableHead>
                      <TableHead>可用性</TableHead>
                      <TableHead />
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {data.sheddingCandidates.map((item) => (
                      <TableRow key={item.id}>
                        <TableCell className="py-4 font-medium">
                          {item.name}
                          <p className="mt-1 text-xs font-normal text-muted-foreground">
                            {item.systemPath}
                          </p>
                        </TableCell>
                        <TableCell>{item.sheddingPotentialKW} kW</TableCell>
                        <TableCell>{item.responseTimeMinutes} 分钟</TableCell>
                        <TableCell>
                          {item.availableDurationMinutes} 分钟
                        </TableCell>
                        <TableCell>
                          {
                            {
                              READY: "可评估",
                              STANDBY: "备用",
                              LOCKED: "受限",
                            }[item.flexibilityStatus]
                          }
                        </TableCell>
                        <TableCell>
                          <Button
                            variant="ghost"
                            size="sm"
                            onClick={() => setCandidateId(item.id)}
                          >
                            <ArrowUpRight aria-hidden="true" data-icon="inline-start" />查看约束
                          </Button>
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </CardContent>
            </Card>
          </>
        )
      )}
      <Sheet
        modal={false}
        open={!!candidate}
        onOpenChange={(open) => {
          if (!open) setCandidateId(null);
        }}
      >
        <SheetContent
          showOverlay={false}
          className="w-[440px] sm:max-w-[440px]"
        >
          <SheetHeader>
            <SheetTitle>{candidate?.name}</SheetTitle>
            <SheetDescription>{candidate?.systemPath}</SheetDescription>
          </SheetHeader>
          {candidate && (
            <div className="space-y-5 p-4">
              <p className="text-sm leading-relaxed">
                {candidate.constraintDescription}
              </p>
              <p className="text-sm">
                舒适影响：
                {
                  { LOW: "低", MEDIUM: "中", HIGH: "高" }[
                    candidate.comfortImpactScore
                  ]
                }
              </p>
              <Button
                variant="outline"
                onClick={() =>
                  void navigate({
                    to: "/operations/control",
                    search: () => ({ scope: currentScope.id }),
                  })
                }
              >
                进入策略与控制
              </Button>
            </div>
          )}
        </SheetContent>
      </Sheet>
    </main>
  );
}
