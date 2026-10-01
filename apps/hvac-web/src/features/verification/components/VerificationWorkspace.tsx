import { optimizationFlowQueryOptions } from "@/features/optimization-flow/api/optimization-flow";
import {
  ArrowUpRight,
  ChartColumn,
  ChartLine,
  Download,
  RefreshCw,
  Search,
} from "lucide-react";
import { ChartLegend, ChartLegendContent } from "@/components/ui/chart";
import { DataTableBlock } from "@/blocks/data-table";
import { useSearch, useNavigate } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { useScope } from "@/hooks/use-scope";
import { useDataTable } from "@/hooks/use-data-table";
import { DataTable } from "@/components/data-table/data-table";
import type { ColumnDef } from "@tanstack/react-table";
import type { DataTableFeatures } from "@/components/data-table/data-table-features";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import {
  InputGroup,
  InputGroupAddon,
  InputGroupInput,
} from "@/components/ui/input-group";
import { Card, CardHeader, CardTitle, CardContent } from "@/components/ui/card";
import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
  SheetDescription,
} from "@/components/ui/sheet";
import {
  Select,
  SelectTrigger,
  SelectValue,
  SelectContent,
  SelectItem,
} from "@/components/ui/select";
import { Alert, AlertTitle, AlertDescription } from "@/components/ui/alert";
import { Skeleton } from "@/components/ui/skeleton";
const example = __HVAC_WEB_FRONTEND_REVIEW__ || import.meta.env.DEV;
import type { MvProjectRecord } from "../api/verification-types";
import {
  LineChart,
  Line,
  CartesianGrid,
  XAxis,
  YAxis,
  BarChart,
  Bar,
} from "recharts";
import {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
} from "@/components/ui/chart";
const statusNames = {
  VERIFYING: "验证中",
  CERTIFIED: "已复核",
  ADJUSTMENT_REQUIRED: "需要调整",
};
export function VerificationWorkspace() {
  const { currentScope } = useScope();
  const search = useSearch({ from: "/_app/optimization/verification" });
  const navigate = useNavigate({ from: "/optimization/verification" });
  const query = useQuery(optimizationFlowQueryOptions(currentScope.id));
  const all = query.data?.verifications ?? [];
  const sourceProject = query.data?.projects.find(
    (item) => item.id === search.project,
  );
  const items = all.filter(
    (item) =>
      (!search.project || item.sourceProjectCode === sourceProject?.code) &&
      (!search.q || item.projectName.includes(search.q)) &&
      (!search.status || item.status === search.status),
  );
  const selected = items.find((item) => item.id === search.inspect);
  const focus = search.record
    ? items.find((item) => item.id === search.record)
    : items[0];
  const origin = query.data?.projects.find(
    (item) => item.code === (selected ?? focus)?.sourceProjectCode,
  );
  const columns: ColumnDef<DataTableFeatures, MvProjectRecord>[] = [
    {
      id: "title",
      header: "验证项目",
      cell: ({ row }) => (
        <div className="max-w-[360px] py-2 font-medium">
          {row.original.projectName}
          <p className="mt-1 text-xs font-normal text-muted-foreground">
            {row.original.ipmvpOption} · {row.original.verifier}
          </p>
        </div>
      ),
    },
    {
      id: "period",
      header: "报告期",
      cell: ({ row }) => row.original.reportingPeriod,
    },
    {
      id: "saving",
      header: "报告期节省量",
      cell: ({ row }) => (
        <span className="tabular-nums">
          {(
            row.original.monthlyRecords.reduce(
              (sum, item) => sum + item.adjustedBaselineKWh - item.rawActualKWh,
              0,
            ) / 1000
          ).toFixed(1)}{" "}
          MWh
        </span>
      ),
    },
    {
      id: "quality",
      header: "CV(RMSE)",
      cell: ({ row }) => row.original.cvRmse + "%",
    },
    {
      id: "status",
      header: "复核状态",
      cell: ({ row }) => (
        <Badge variant="outline">{statusNames[row.original.status]}</Badge>
      ),
    },
    {
      id: "action",
      header: "",
      cell: ({ row }) => (
        <Button
          variant="ghost"
          size="sm"
          onClick={() =>
            void navigate({
              search: (previous) => ({
                ...previous,
                inspect: row.original.id,
                record: row.original.id,
              }),
            })
          }
        >
          <ArrowUpRight aria-hidden="true" data-icon="inline-start" />
          查看核算
        </Button>
      ),
    },
  ];
  const table = useDataTable({
    key: "verification-review",
    data: items,
    columns,
    paginate: false,
    getRowId: (item) => item.id,
  });
  const csv = () => {
    if (!focus) return;
    const rows = [
      ["节能验证 · 示例 · 非认证报告", focus.projectName],
      ["月份", "调整后基线 kWh", "实际 kWh", "差额 kWh"],
      ...focus.monthlyRecords.map((item) => [
        item.month,
        item.adjustedBaselineKWh,
        item.rawActualKWh,
        item.adjustedBaselineKWh - item.rawActualKWh,
      ]),
    ];
    const url = URL.createObjectURL(
      new Blob(
        [
          "\uFEFF" +
            rows
              .map((row) =>
                row
                  .map((value) => '"' + String(value).replace(/"/g, '""') + '"')
                  .join(","),
              )
              .join("\n"),
        ],
        { type: "text/csv;charset=utf-8" },
      ),
    );
    const link = document.createElement("a");
    link.href = url;
    link.download = "节能验证核算.csv";
    link.click();
    URL.revokeObjectURL(url);
  };
  return (
    <main className="mx-auto max-w-[1600px] space-y-5 p-6">
      <header className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          <h1 className="text-2xl font-semibold">节能验证</h1>
          {example && <Badge variant="outline">示例数据</Badge>}
        </div>
        <div className="flex gap-2">
          <Button
            variant="outline"
            size="sm"
            onClick={() => void query.refetch()}
          >
            <RefreshCw aria-hidden="true" data-icon="inline-start" />
            刷新
          </Button>
          <Button variant="outline" size="sm" onClick={csv} disabled={!focus}>
            <Download aria-hidden="true" data-icon="inline-start" />
            导出核算
          </Button>
        </div>
      </header>
      {query.isPending ? (
        <Skeleton className="h-96" />
      ) : query.isError ? (
        <Alert variant="destructive">
          <AlertTitle>验证数据不可用</AlertTitle>
          <AlertDescription>
            {query.error.message}
            <Button variant="link" onClick={() => void query.refetch()}>
              重试
            </Button>
          </AlertDescription>
        </Alert>
      ) : (
        <>
          {search.inspect && !selected && (
            <Alert>
              <AlertTitle>核算详情不存在或已不在当前范围</AlertTitle>
              <AlertDescription>
                <Button
                  variant="link"
                  onClick={() =>
                    void navigate({
                      search: (previous) => ({
                        ...previous,
                        inspect: undefined,
                      }),
                    })
                  }
                >
                  关闭详情链接
                </Button>
              </AlertDescription>
            </Alert>
          )}
          <section className="grid grid-cols-3 divide-x rounded-xl border bg-card">
            {[
              {
                label: "复核中的项目",
                value: all.filter((item) => item.status === "VERIFYING").length,
              },
              {
                label: "已复核项目",
                value: all.filter((item) => item.status === "CERTIFIED").length,
              },
              {
                label: "需要调整",
                value: all.filter(
                  (item) => item.status === "ADJUSTMENT_REQUIRED",
                ).length,
              },
            ].map((item) => (
              <div key={item.label} className="space-y-3 p-5">
                <p className="text-sm">{item.label}</p>
                <p className="text-[30px] font-semibold tabular-nums">
                  {item.value}
                  <span className="ml-2 text-xs font-normal text-muted-foreground">
                    项
                  </span>
                </p>
              </div>
            ))}
          </section>
          {search.project && (
            <div className="flex items-center justify-between gap-4 rounded-lg border p-4 text-sm">
              <span>
                {sourceProject
                  ? `关联项目：${sourceProject.title}`
                  : "关联项目不存在"}
              </span>
              <div className="flex gap-2">
                {sourceProject && (
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() =>
                      void navigate({
                        to: "/optimization/projects",
                        search: {
                          scope: currentScope.id,
                          inspect: sourceProject.id,
                        },
                      })
                    }
                  >
                    返回项目推进
                  </Button>
                )}
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={() =>
                    void navigate({
                      search: (previous) => ({
                        ...previous,
                        project: undefined,
                        record: undefined,
                        inspect: undefined,
                      }),
                    })
                  }
                >
                  查看全部验证
                </Button>
              </div>
            </div>
          )}
          {search.record && !focus && (
            <Alert>
              <AlertTitle>验证记录不在当前结果中</AlertTitle>
              <AlertDescription>
                <Button
                  variant="link"
                  onClick={() =>
                    void navigate({
                      search: (previous) => ({
                        ...previous,
                        q: undefined,
                        status: undefined,
                        record: undefined,
                        inspect: undefined,
                      }),
                    })
                  }
                >
                  查看当前项目的验证记录
                </Button>
              </AlertDescription>
            </Alert>
          )}
          {focus && (
            <>
              <div className="flex items-center justify-between">
                <Select
                  value={focus.id}
                  onValueChange={(value) =>
                    void navigate({
                      search: (previous) => ({
                        ...previous,
                        record: value,
                        inspect: undefined,
                      }),
                    })
                  }
                >
                  <SelectTrigger aria-label="验证对象" className="w-[440px]">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {items.map((item) => (
                      <SelectItem key={item.id} value={item.id}>
                        {item.projectName}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                <Button
                  variant="ghost"
                  onClick={() =>
                    void navigate({
                      search: (previous) => ({
                        ...previous,
                        inspect: focus.id,
                      }),
                    })
                  }
                >
                  方法与调整项
                </Button>
              </div>
              <div className="flex items-center justify-between gap-4 text-sm">
                <span>报告期：{focus.reportingPeriod}</span>
                <span>
                  复核负责人：{focus.verifier} · {statusNames[focus.status]}
                </span>
              </div>
              <div className="grid grid-cols-12 gap-5">
                <Card className="col-span-8">
                  <CardHeader>
                    <CardTitle className="flex items-center gap-2">
                      <ChartLine className="size-4" aria-hidden="true" />
                      调整后基线与报告期实际（kWh）
                    </CardTitle>
                  </CardHeader>
                  <CardContent>
                    <ChartContainer
                      className="h-[260px] w-full aspect-auto"
                      config={{
                        adjustedBaselineKWh: {
                          label: "调整后基线（kWh）",
                          color: "var(--muted-foreground)",
                        },
                        rawActualKWh: {
                          label: "实际（kWh）",
                          color: "var(--chart-1)",
                        },
                      }}
                    >
                      <LineChart
                        accessibilityLayer
                        data={focus.monthlyRecords}
                        margin={{ left: 15, right: 20 }}
                      >
                        <CartesianGrid vertical={false} />
                        <XAxis
                          dataKey="month"
                          tickLine={false}
                          axisLine={false}
                        />
                        <YAxis
                          tickFormatter={(value) => value / 1000 + "k"}
                          width={60}
                          tickLine={false}
                          axisLine={false}
                        />
                        <ChartTooltip content={<ChartTooltipContent />} />
                        <Line
                          isAnimationActive={false}
                          dataKey="adjustedBaselineKWh"
                          stroke="var(--color-adjustedBaselineKWh)"
                          strokeDasharray="4 4"
                          dot={false}
                        />
                        <Line
                          isAnimationActive={false}
                          dataKey="rawActualKWh"
                          stroke="var(--color-rawActualKWh)"
                          strokeWidth={2}
                          dot={false}
                        />
                        <ChartLegend content={<ChartLegendContent />} />
                      </LineChart>
                    </ChartContainer>
                  </CardContent>
                </Card>
                <Card className="col-span-4">
                  <CardHeader>
                    <CardTitle className="flex items-center gap-2">
                      <ChartColumn className="size-4" aria-hidden="true" />
                      月度节省量（MWh）
                    </CardTitle>
                  </CardHeader>
                  <CardContent>
                    <ChartContainer
                      className="h-[260px] w-full aspect-auto"
                      config={{
                        savings: {
                          label: "节省量（MWh）",
                          color: "var(--chart-2)",
                        },
                      }}
                    >
                      <BarChart
                        accessibilityLayer
                        data={focus.monthlyRecords.map((item) => ({
                          ...item,
                          savings:
                            (item.adjustedBaselineKWh - item.rawActualKWh) /
                            1000,
                        }))}
                      >
                        <CartesianGrid vertical={false} />
                        <XAxis
                          dataKey="month"
                          tickLine={false}
                          axisLine={false}
                        />
                        <YAxis tickLine={false} axisLine={false} />
                        <ChartTooltip content={<ChartTooltipContent />} />
                        <Bar
                          isAnimationActive={false}
                          dataKey="savings"
                          fill="var(--color-savings)"
                          radius={[4, 4, 0, 0]}
                        />
                      </BarChart>
                    </ChartContainer>
                  </CardContent>
                </Card>
              </div>
            </>
          )}
          <section className="space-y-4">
            <div className="flex gap-3">
              <InputGroup className="max-w-80">
                <InputGroupAddon>
                  <Search aria-hidden="true" />
                </InputGroupAddon>
                <InputGroupInput
                  aria-label="搜索验证项目"
                  placeholder="搜索验证项目"
                  value={search.q ?? ""}
                  onChange={(event) =>
                    void navigate({
                      search: (previous) => ({
                        ...previous,
                        q: event.target.value || undefined,
                        record: undefined,
                        inspect: undefined,
                      }),
                    })
                  }
                />
              </InputGroup>
              <Select
                value={search.status ?? "all"}
                onValueChange={(value) =>
                  void navigate({
                    search: (previous) => ({
                      ...previous,
                      status: value === "all" ? undefined : value,
                      record: undefined,
                      inspect: undefined,
                    }),
                  })
                }
              >
                <SelectTrigger className="w-36" aria-label="验证状态">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="all">全部状态</SelectItem>
                  {Object.entries(statusNames).map(([key, label]) => (
                    <SelectItem key={key} value={key}>
                      {label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <DataTableBlock>
              <DataTable
                table={table}
                tableAriaLabel="项目节能验证账本"
                empty={
                  <div className="space-y-3 py-6">
                    <p>
                      {search.project && !search.q && !search.status
                        ? "该项目尚无测量记录"
                        : "没有匹配的验证记录"}
                    </p>
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={() =>
                        void navigate({
                          search: (previous) => ({
                            ...previous,
                            q: undefined,
                            status: undefined,
                            record: undefined,
                            inspect: undefined,
                          }),
                        })
                      }
                    >
                      清除筛选
                    </Button>
                  </div>
                }
              />
            </DataTableBlock>
          </section>
        </>
      )}
      <Sheet
        modal={false}
        open={!!selected}
        onOpenChange={(open) => {
          if (!open)
            void navigate({
              search: (previous) => ({ ...previous, inspect: undefined }),
            });
        }}
      >
        <SheetContent
          showOverlay={false}
          className="w-[600px] sm:max-w-[600px] overflow-y-auto"
        >
          {selected && (
            <>
              <SheetHeader>
                <SheetTitle>{selected.projectName}</SheetTitle>
                <SheetDescription>
                  {selected.ipmvpOption} · {selected.verifier}
                </SheetDescription>
              </SheetHeader>
              <div className="space-y-5 p-4">
                {origin && (
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() =>
                      void navigate({
                        to: "/optimization/projects",
                        search: { scope: currentScope.id, inspect: origin.id },
                      })
                    }
                  >
                    <ArrowUpRight aria-hidden="true" />
                    查看实施项目
                  </Button>
                )}
                <dl className="space-y-4 text-sm">
                  {[
                    { label: "测量边界", value: selected.measurementBoundary },
                    { label: "基线期", value: selected.baselinePeriod },
                    { label: "报告期", value: selected.reportingPeriod },
                  ].map((item) => (
                    <div key={item.label}>
                      <dt className="font-semibold">{item.label}</dt>
                      <dd className="mt-2">{item.value}</dd>
                    </div>
                  ))}
                </dl>
                <div className="grid grid-cols-3 divide-x rounded-lg border p-4 text-sm">
                  <span>R² {selected.r2}</span>
                  <span className="pl-3">CV(RMSE) {selected.cvRmse}%</span>
                  <span className="pl-3">NMBE {selected.nmbe}%</span>
                </div>
                <h3 className="text-sm font-semibold">基线调整项</h3>
                {selected.adjustments.map((item) => (
                  <section key={item.id} className="rounded-lg border p-4">
                    <div className="flex justify-between gap-3 text-sm font-medium">
                      <span>{item.label}</span>
                      <span className="tabular-nums">
                        {item.adjustmentDeltaKWh.toLocaleString("zh-CN")} kWh
                      </span>
                    </div>
                    <p className="mt-2 text-sm leading-relaxed">
                      {item.description}
                    </p>
                  </section>
                ))}
              </div>
            </>
          )}
        </SheetContent>
      </Sheet>
    </main>
  );
}
