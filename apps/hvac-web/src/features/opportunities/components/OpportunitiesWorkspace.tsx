import { optimizationFlowQueryOptions } from "@/features/optimization-flow/api/optimization-flow";
import { ArrowUpRight, Download, RefreshCw, Search } from "lucide-react";
import { DataTableBlock } from "@/blocks/data-table";
import { useNavigate, useSearch } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { Bar, BarChart, CartesianGrid, XAxis, YAxis } from "recharts";
import { useScope } from "@/hooks/use-scope";
import { useDataTable } from "@/hooks/use-data-table";
import { DataTable } from "@/components/data-table/data-table";
import type { ColumnDef } from "@tanstack/react-table";
import type { DataTableFeatures } from "@/components/data-table/data-table-features";
import { Button } from "@/components/ui/button";
import {
  InputGroup,
  InputGroupAddon,
  InputGroupInput,
} from "@/components/ui/input-group";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
} from "@/components/ui/chart";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Alert, AlertTitle, AlertDescription } from "@/components/ui/alert";
import { Skeleton } from "@/components/ui/skeleton";
import type { EcoOpportunity } from "../api/opportunity-types";
import { OpportunityDetailSheet } from "./OpportunityDetailSheet";

export const subsystemNames: Record<EcoOpportunity["subsystem"], string> = {
  CHILLER: "冷水主机",
  PUMP: "水泵输配",
  TOWER: "冷却塔",
  TERMINAL: "空调末端",
  SYSTEM: "系统优化",
};
export const statusNames: Record<EcoOpportunity["status"], string> = {
  IDENTIFIED: "待评估",
  REVIEWING: "评审中",
  APPROVED: "已批准",
  IN_PROGRESS: "实施中",
  VERIFYING: "验证中",
  CLOSED: "已关闭",
};
const example = __HVAC_WEB_FRONTEND_REVIEW__ || import.meta.env.DEV;
const number = (value: number) =>
  value.toLocaleString("zh-CN", { maximumFractionDigits: 1 });

export function OpportunitiesWorkspace() {
  const { currentScope } = useScope();
  const search = useSearch({ from: "/_app/optimization/opportunities" });
  const navigate = useNavigate({ from: "/optimization/opportunities" });
  const query = useQuery(optimizationFlowQueryOptions(currentScope.id));
  const all = query.data?.opportunities ?? [];
  const selected = all.find((item) => item.id === search.inspect) ?? null;
  const relatedProject = query.data?.projects.find(
    (project) => project.sourceOpportunityCode === selected?.code,
  );
  const filtered = all.filter(
    (item) =>
      (!search.q ||
        (item.title + item.targetObject + item.owner).includes(search.q)) &&
      (!search.subsystem || item.subsystem === search.subsystem) &&
      (!search.status || item.status === search.status),
  );
  const items = [...filtered].sort((a, b) =>
    search.sort === "confidence"
      ? b.confidence - a.confidence
      : search.sort === "payback"
        ? a.paybackMonths - b.paybackMonths
        : b.annualSavingsCostCNY - a.annualSavingsCostCNY,
  );
  const update = (value: Partial<typeof search>) => {
    void navigate({
      search: (previous) => ({ ...previous, ...value, inspect: undefined }),
    });
  };
  const columns: ColumnDef<DataTableFeatures, EcoOpportunity>[] = [
    {
      id: "title",
      accessorFn: (r) => r.title,
      header: "机会与对象",
      cell: ({ row }) => (
        <div className="max-w-[360px] py-2">
          <p className="font-medium">{row.original.title}</p>
          <p className="mt-1 text-xs text-muted-foreground">
            {row.original.targetObject}
          </p>
        </div>
      ),
    },
    {
      id: "system",
      header: "系统",
      cell: ({ row }) => subsystemNames[row.original.subsystem],
    },
    {
      id: "saving",
      header: "年费用潜力",
      cell: ({ row }) => (
        <div className="text-right tabular-nums font-medium">
          ¥ {number(row.original.annualSavingsCostCNY)}
          <p className="mt-1 text-xs font-normal text-muted-foreground">
            {number(row.original.annualSavingsKWh / 1000)} MWh/年
          </p>
        </div>
      ),
    },
    {
      id: "confidence",
      header: "置信度",
      cell: ({ row }) => (
        <span className="tabular-nums">{row.original.confidence}%</span>
      ),
    },
    {
      id: "payback",
      header: "回收期",
      cell: ({ row }) =>
        row.original.investmentCostCNY === 0
          ? "零成本"
          : number(row.original.paybackMonths) + " 个月",
    },
    { id: "owner", header: "负责人", cell: ({ row }) => row.original.owner },
    {
      id: "status",
      header: "状态",
      cell: ({ row }) => (
        <Badge variant="outline" className="font-normal">
          {statusNames[row.original.status]}
        </Badge>
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
              search: (previous) => ({ ...previous, inspect: row.original.id }),
            })
          }
        >
          <ArrowUpRight aria-hidden="true" data-icon="inline-start" />
          查看证据
        </Button>
      ),
    },
  ];
  const table = useDataTable({
    key: "opportunity-review",
    data: items,
    columns,
    paginate: false,
    getRowId: (r) => r.id,
  });
  const active = all.filter((item) => item.status !== "CLOSED");
  const distribution = Object.entries(subsystemNames)
    .map(([key, name]) => ({
      name,
      value:
        active
          .filter((item) => item.subsystem === key)
          .reduce((sum, item) => sum + item.annualSavingsCostCNY, 0) / 10000,
    }))
    .filter((item) => item.value > 0)
    .sort((a, b) => b.value - a.value);
  const stages = Object.entries(statusNames).map(([key, name]) => ({
    name,
    value: all.filter((item) => item.status === key).length,
  }));
  const exportCsv = () => {
    const quote = (value: string | number) =>
      typeof value === "number"
        ? String(value)
        : '"' +
          (/^[=+@-]/.test(value) ? "'" : "") +
          value.replace(/"/g, '""') +
          '"';
    const rows = [
      ["节能机会 · 示例 · 估算潜力"],
      ["机会", "对象", "年节电 kWh", "年节费 元", "负责人", "状态"],
      ...items.map((item) => [
        item.title,
        item.targetObject,
        item.annualSavingsKWh,
        item.annualSavingsCostCNY,
        item.owner,
        statusNames[item.status],
      ]),
    ];
    const url = URL.createObjectURL(
      new Blob(
        ["\uFEFF" + rows.map((row) => row.map(quote).join(",")).join("\n")],
        { type: "text/csv;charset=utf-8" },
      ),
    );
    const link = document.createElement("a");
    link.href = url;
    link.download = "节能机会.csv";
    link.click();
    URL.revokeObjectURL(url);
  };
  return (
    <main className="mx-auto max-w-[1600px] space-y-5 p-6">
      <header className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          <h1 className="text-2xl font-semibold tracking-tight">节能机会</h1>
          {example && <Badge variant="outline">示例数据</Badge>}
        </div>
        <div className="flex gap-2">
          <Button
            variant="outline"
            size="sm"
            aria-label="刷新机会"
            onClick={() => void query.refetch()}
          >
            <RefreshCw />
          </Button>
          <Button
            variant="outline"
            size="sm"
            disabled={!items.length}
            onClick={exportCsv}
          >
            <Download aria-hidden="true" data-icon="inline-start" />
            导出机会
          </Button>
        </div>
      </header>
      {query.isPending ? (
        <Skeleton className="h-96" />
      ) : query.isError ? (
        <Alert variant="destructive">
          <AlertTitle>机会组合不可用</AlertTitle>
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
              <AlertTitle>机会不存在或已不在当前范围</AlertTitle>
              <AlertDescription>
                <Button variant="link" onClick={() => update({})}>
                  关闭详情链接
                </Button>
              </AlertDescription>
            </Alert>
          )}
          <section
            aria-label="机会组合概况"
            className="grid grid-cols-4 divide-x rounded-xl border bg-card"
          >
            {[
              { label: "待推进机会", value: active.length, unit: "项" },
              {
                label: "高置信度",
                value: active.filter((item) => item.confidence >= 90).length,
                unit: "项",
              },
              {
                label: "零成本改善",
                value: active.filter((item) => item.investmentCostCNY === 0)
                  .length,
                unit: "项",
              },
              {
                label: "实施与验证",
                value: all.filter(
                  (item) =>
                    item.status === "IN_PROGRESS" ||
                    item.status === "VERIFYING",
                ).length,
                unit: "项",
              },
            ].map((metric) => (
              <div key={metric.label} className="space-y-3 px-5 py-5">
                <p className="text-sm">{metric.label}</p>
                <p className="text-[30px] font-semibold tabular-nums">
                  {metric.value}
                  <span className="ml-2 text-xs font-normal text-muted-foreground">
                    {metric.unit}
                  </span>
                </p>
              </div>
            ))}
          </section>
          <div className="grid grid-cols-2 gap-5">
            <Card>
              <CardHeader>
                <CardTitle>系统改善潜力（万元/年）</CardTitle>
              </CardHeader>
              <CardContent>
                <ChartContainer
                  className="h-[190px] w-full aspect-auto"
                  config={{
                    value: {
                      label: "年费用潜力（万元）",
                      color: "var(--chart-1)",
                    },
                  }}
                >
                  <BarChart
                    accessibilityLayer
                    data={distribution}
                    layout="vertical"
                    margin={{ right: 24, left: 0 }}
                  >
                    <CartesianGrid horizontal={false} />
                    <XAxis type="number" tickLine={false} axisLine={false} />
                    <YAxis
                      type="category"
                      dataKey="name"
                      width={72}
                      tickLine={false}
                      axisLine={false}
                    />
                    <ChartTooltip content={<ChartTooltipContent />} />
                    <Bar
                      isAnimationActive={false}
                      dataKey="value"
                      fill="var(--color-value)"
                      radius={4}
                      barSize={16}
                    />
                  </BarChart>
                </ChartContainer>
              </CardContent>
            </Card>
            <Card>
              <CardHeader>
                <CardTitle>机会推进阶段</CardTitle>
              </CardHeader>
              <CardContent>
                <ChartContainer
                  className="h-[190px] w-full aspect-auto"
                  config={{
                    value: { label: "机会数", color: "var(--chart-2)" },
                  }}
                >
                  <BarChart accessibilityLayer data={stages}>
                    <CartesianGrid vertical={false} />
                    <XAxis dataKey="name" axisLine={false} tickLine={false} />
                    <YAxis
                      allowDecimals={false}
                      axisLine={false}
                      tickLine={false}
                    />
                    <ChartTooltip content={<ChartTooltipContent />} />
                    <Bar
                      isAnimationActive={false}
                      dataKey="value"
                      fill="var(--color-value)"
                      radius={[4, 4, 0, 0]}
                      barSize={28}
                    />
                  </BarChart>
                </ChartContainer>
              </CardContent>
            </Card>
          </div>
          <section className="space-y-4">
            <div className="flex items-center gap-3">
              <InputGroup className="max-w-[300px]">
                <InputGroupAddon>
                  <Search aria-hidden="true" />
                </InputGroupAddon>
                <InputGroupInput
                  aria-label="搜索机会"
                  placeholder="搜索机会、设备或负责人"
                  value={search.q ?? ""}
                  onChange={(event) =>
                    update({ q: event.target.value || undefined })
                  }
                />
              </InputGroup>
              <Select
                value={search.subsystem ?? "all"}
                onValueChange={(value) =>
                  update({ subsystem: value === "all" ? undefined : value })
                }
              >
                <SelectTrigger className="w-36" aria-label="系统筛选">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="all">全部系统</SelectItem>
                  {Object.entries(subsystemNames).map(([key, label]) => (
                    <SelectItem key={key} value={key}>
                      {label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <Select
                value={search.status ?? "all"}
                onValueChange={(value) =>
                  update({ status: value === "all" ? undefined : value })
                }
              >
                <SelectTrigger className="w-36" aria-label="状态筛选">
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
              <Select
                value={search.sort ?? "saving"}
                onValueChange={(value) =>
                  update({ sort: value as "saving" | "confidence" | "payback" })
                }
              >
                <SelectTrigger className="ml-auto w-40" aria-label="机会排序">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="saving">费用潜力优先</SelectItem>
                  <SelectItem value="confidence">置信度优先</SelectItem>
                  <SelectItem value="payback">回收期优先</SelectItem>
                </SelectContent>
              </Select>
              <Button
                variant="ghost"
                onClick={() =>
                  update({
                    q: undefined,
                    subsystem: undefined,
                    status: undefined,
                    sort: undefined,
                  })
                }
              >
                重置
              </Button>
            </div>
            <DataTableBlock>
              <DataTable
                table={table}
                tableAriaLabel="节能机会账本"
                empty={
                  <p className="py-12 text-center">
                    当前范围与筛选下没有节能机会
                  </p>
                }
              />
            </DataTableBlock>
            <p className="text-xs text-muted-foreground">
              {items.length} 项机会
            </p>
          </section>
        </>
      )}
      <OpportunityDetailSheet
        key={selected?.id ?? "closed"}
        opportunity={selected}
        open={selected !== null}
        onClose={() =>
          void navigate({
            search: (previous) => ({ ...previous, inspect: undefined }),
          })
        }
        relatedProject={relatedProject}
        onViewProject={() =>
          void navigate({
            to: "/optimization/projects",
            search: {
              scope: currentScope.id,
              opportunity: selected?.id,
              inspect: relatedProject?.id,
            },
          })
        }
      />
    </main>
  );
}
