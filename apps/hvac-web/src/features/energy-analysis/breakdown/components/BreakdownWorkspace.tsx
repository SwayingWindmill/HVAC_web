import { RefreshCw, Search } from "lucide-react";
import { PageHeader } from "@/blocks/page-header";
import { Main } from "@/components/layout/Main";
import { DataTableBlock } from "@/blocks/data-table";
import { useMemo } from "react";
import { useSearch, useNavigate } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import type { EChartsOption } from "echarts";
import { useWorkspaceScope } from "@/hooks/use-scope";
import { EngineeringChart } from "@/components/analysis/EngineeringChart";
import { Card, CardHeader, CardTitle, CardContent } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Alert, AlertTitle, AlertDescription } from "@/components/ui/alert";
import { Skeleton } from "@/components/ui/skeleton";
const example = __HVAC_WEB_FRONTEND_REVIEW__;
import { breakdownService } from "../api/breakdown-service";
import { useDataTable } from "@/hooks/use-data-table";
import { DataTable } from "@/components/data-table/data-table";
import type { ColumnDef } from "@tanstack/react-table";
import type { DataTableFeatures } from "@/components/data-table/data-table-features";
import type { SubmeteringPointRecord } from "../api/breakdown-types";
import { InputGroup, InputGroupAddon, InputGroupInput } from "@/components/ui/input-group";
export function BreakdownWorkspace() {
  const { currentScope } = useWorkspaceScope();
  const search = useSearch({ from: "/_app/_site/energy-analysis/breakdown" });
  const navigate = useNavigate({ from: "/energy-analysis/breakdown" });
  const view = search.view ?? "flow";
  const query = useQuery({
    queryKey: ["breakdown-review", currentScope.id],
    queryFn: () => {
      if (!example)
        throw new Error("当前范围分项数据未接入");
      return breakdownService.getBreakdownAnalytics();
    },
    retry: false,
  });
  const data = query.data;
  const total =
    data?.subsystems.reduce((sum, item) => sum + item.energyKWh, 0) ?? 0;
  const option = useMemo<EChartsOption>(
    () => ({
      tooltip: {
        trigger: "item",
        valueFormatter: (value) =>
          Number(value).toLocaleString("zh-CN") + " kWh",
      },
      series: [
        {
          type: "sankey",
          left: 20,
          right: 200,
          top: 20,
          bottom: 20,
          nodeWidth: 16,
          nodeGap: 22,
          draggable: false,
          emphasis: { focus: "adjacency" },
          lineStyle: { color: "gradient", opacity: 0.22 },
          data: [
            { name: "HVAC 总用电", itemStyle: { color: "#2563eb" } },
            ...(data?.subsystems ?? []).map((item) => ({
              name: item.categoryLabel,
              itemStyle: { color: "#60a5fa" },
            })),
          ],
          links: (data?.subsystems ?? []).map((item) => ({
            source: "HVAC 总用电",
            target: item.categoryLabel,
            value: item.energyKWh,
          })),
          label: { color: "#64748b", fontSize: 12 },
        },
      ],
    }),
    [data],
  );
  const meters = (data?.submeters ?? []).filter(
    (item) =>
      !search.q || (item.meterName + item.systemPath).includes(search.q),
  );
  const columns: ColumnDef<DataTableFeatures, SubmeteringPointRecord>[] = [
    {
      id: "name",
      header: "计量对象",
      cell: ({ row }) => (
        <div className="py-2 font-medium">
          {row.original.meterName}
          <p className="mt-1 text-xs font-normal text-muted-foreground">
            {row.original.systemPath}
          </p>
        </div>
      ),
    },
    {
      id: "month",
      header: "本月用电",
      cell: ({ row }) => row.original.monthKWh.toLocaleString("zh-CN") + " kWh",
    },
    {
      id: "power",
      header: "当前功率",
      cell: ({ row }) => row.original.currentPowerKW + " kW",
    },
    {
      id: "quality",
      header: "连接状态",
      cell: ({ row }) => (
        <Badge variant="outline">
          {
            { ONLINE: "在线", WARNING: "需检查", OFFLINE: "离线" }[
              row.original.healthStatus
            ]
          }
        </Badge>
      ),
    },
    {
      id: "updated",
      header: "更新时间",
      cell: ({ row }) => row.original.lastActiveTime,
    },
  ];
  const table = useDataTable({
    key: "submetering-review",
    data: meters,
    columns,
    paginate: false,
    getRowId: (item) => item.id,
  });
  return (
    <Main className="space-y-5">
      <PageHeader
        title="分项与能流"
        actions={
          <>
            <Button
          variant="outline"
          size="sm"
          onClick={() => void query.refetch()}
        >
          <RefreshCw aria-hidden="true" data-icon="inline-start" />刷新
        </Button>
          </>
        }
      />
      {query.isPending ? (
        <Skeleton className="h-96" />
      ) : query.isError ? (
        <Alert variant="destructive">
          <AlertTitle>分项数据不可用</AlertTitle>
          <AlertDescription>请选择已接入的站点范围。</AlertDescription>
        </Alert>
      ) : (
        data && (
          <>
            <p className="text-xs">2026年9月 / HVAC 电量边界</p>
            <section className="grid grid-cols-3 divide-x rounded-xl border bg-card">
              {[
                {
                  label: "分项累计电量",
                  value: (total / 1000).toFixed(1),
                  unit: "MWh",
                },
                {
                  label: "分项系统",
                  value: data.subsystems.length,
                  unit: "个",
                },
                {
                  label: "在线计量点",
                  value:
                    data.submeters.filter(
                      (item) => item.healthStatus === "ONLINE",
                    ).length +
                    "/" +
                    data.submeters.length,
                  unit: "个",
                },
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
                <TabsTrigger value="flow" className="inline-flex items-center gap-2">用电分配</TabsTrigger>
                <TabsTrigger value="ledger" className="inline-flex items-center gap-2">计量明细</TabsTrigger>
              </TabsList>
            </Tabs>
            {view === "flow" ? (
              <>
                <Card>
                  <CardHeader>
                    <CardTitle>分项电量流向</CardTitle>
                  </CardHeader>
                  <CardContent>
                    <EngineeringChart
                      option={option}
                      label="HVAC电量按分项系统分配，不包含制冷量与散热量"
                      height={330}
                    />
                  </CardContent>
                </Card>
                <div className="rounded-xl border bg-card divide-y">
                  {[...data.subsystems]
                    .sort((a, b) => b.energyKWh - a.energyKWh)
                    .map((item) => (
                      <div
                        key={item.id}
                        className="grid grid-cols-[180px_1fr_110px_70px] items-center gap-5 px-5 py-3.5"
                      >
                        <span className="text-sm font-medium">
                          {item.categoryLabel}
                        </span>
                        <div className="h-2 rounded bg-muted">
                          <div
                            className="h-full rounded bg-blue-500"
                            style={{
                              width: (item.energyKWh / total) * 100 + "%",
                            }}
                          />
                        </div>
                        <span className="text-right text-sm tabular-nums">
                          {(item.energyKWh / 1000).toFixed(1)} MWh
                        </span>
                        <span className="text-right text-sm tabular-nums">
                          {((item.energyKWh / total) * 100).toFixed(1)}%
                        </span>
                      </div>
                    ))}
                </div>
              </>
            ) : (
              <section className="space-y-4">
                <InputGroup className="max-w-80"><InputGroupAddon><Search aria-hidden="true" /></InputGroupAddon><InputGroupInput
                  value={search.q ?? ""}
                  aria-label="搜索计量对象"
                  placeholder="搜索计量对象或系统"
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
                  <DataTable table={table} tableAriaLabel="计量点明细" />
                </DataTableBlock>
              </section>
            )}
          </>
        )
      )}
    </Main>
  );
}
