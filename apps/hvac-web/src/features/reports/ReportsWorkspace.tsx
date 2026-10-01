import { ArrowUpRight, CalendarDays, Download, FileChartColumn, FileCheck2, RefreshCw, Search, UserRound } from "lucide-react";
import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { useSearch, useNavigate } from "@tanstack/react-router";
import { useScope } from "@/hooks/use-scope";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { InputGroup, InputGroupAddon, InputGroupInput } from "@/components/ui/input-group";
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

import {
  WorkspaceHeader,
  MetricStrip,
  WorkspaceQueryState,
  isWorkspaceExample,
} from "@/components/analysis/workspace-parts";

import { createConsumptionExample } from "@/features/energy-analysis/consumption/api/consumption-example";
type ReportEntry = {
  id: string;
  title: string;
  type: "energy" | "verification";
  period: string;
  owner: string;
  generatedAt: string;
  status: string;
  sections: string[];
};
const reports: ReportEntry[] = [
  {
    id: "energy-aug",
    title: "2026年8月能源与费用分析",
    type: "energy",
    period: "2026-08-01 — 2026-08-31",
    owner: "能源管理组",
    generatedAt: "2026-09-01 08:30",
    status: "待复核",
    sections: ["用电与费用汇总", "逐日电量和分时费用", "系统用电结构"],
  },
  {
    id: "mv-aug",
    title: "冷站运行优化节能核算",
    type: "verification",
    period: "2026-08-01 — 2026-08-31",
    owner: "节能核验工程师",
    generatedAt: "2026-09-02 10:15",
    status: "待复核",
    sections: [
      "测量边界与报告期间",
      "调整后基线与实际用电",
      "模型质量与非例行调整",
    ],
  },
];
export function ReportsWorkspace() {
  const { currentScope } = useScope();
  const search = useSearch({ from: "/_app/reports" });
  const navigate = useNavigate({ from: "/reports" });
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const query = useQuery({
    queryKey: ["report-register-review", currentScope.id],
    queryFn: async () => {
      if (!isWorkspaceExample || currentScope.id !== "site:site-01")
        throw new Error("报告登记与正式文件未接入");
      return reports;
    },
    retry: false,
  });
  const all = query.data ?? [];
  const items = all.filter(
    (x) =>
      (!search.q || (x.title + x.owner).includes(search.q)) &&
      (!search.type || x.type === search.type),
  );
  const selected = all.find((x) => x.id === selectedId);
  const snapshot = createConsumptionExample("last-month", currentScope.id);
  function download() {
    const rows = [
      ["示例能源报告 · 非正式发布", currentScope.name],
      ["期间", "2026年8月"],
      ["日期", "用电 kWh", "费用 元"],
      ...snapshot.dailyRecords.map((x) => [x.date, x.totalKWh, x.totalCostCNY]),
    ];
    const csv = rows
      .map((row) =>
        row.map((x) => '"' + String(x).replace(/"/g, '""') + '"').join(","),
      )
      .join("\r\n");
    const url = URL.createObjectURL(
      new Blob(["\uFEFF" + csv], { type: "text/csv;charset=utf-8" }),
    );
    const a = document.createElement("a");
    a.href = url;
    a.download = "示例能源报告-2026-08.csv";
    a.click();
    URL.revokeObjectURL(url);
  }
  return (
    <main className="mx-auto max-w-[1600px] space-y-5 p-6">
      <WorkspaceHeader
        title="报表中心"
        actions={
          <Button
            variant="outline"
            size="sm"
            onClick={() => void query.refetch()}
          >
            <RefreshCw aria-hidden="true" data-icon="inline-start" />刷新
          </Button>
        }
      />
      <WorkspaceQueryState pending={query.isPending} error={query.error} />
      {query.data && (
        <>
          <MetricStrip
            items={[
              { label: "报告记录", value: all.length },
              {
                label: "待复核",
                value: all.filter((x) => x.status === "待复核").length,
              },
              { label: "正式发布", value: 0 },
            ]}
          />
          <div className="flex gap-3">
            <InputGroup className="w-80"><InputGroupAddon><Search aria-hidden="true" /></InputGroupAddon><InputGroupInput
              aria-label="搜索报告"
              placeholder="搜索报告或负责人"
              value={search.q ?? ""}
              onChange={(e) =>
                void navigate({
                  search: (prev) => ({
                    ...prev,
                    q: e.target.value || undefined,
                  }),
                })
              }
             /></InputGroup>
            <Select
              value={search.type ?? "all"}
              onValueChange={(type) =>
                void navigate({
                  search: (prev) => ({
                    ...prev,
                    type:
                      type === "all"
                        ? undefined
                        : (type as "energy" | "verification"),
                  }),
                })
              }
            >
              <SelectTrigger aria-label="报告类型" className="w-40">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">全部类型</SelectItem>
                <SelectItem value="energy">能源分析</SelectItem>
                <SelectItem value="verification">节能核算</SelectItem>
              </SelectContent>
            </Select>
          </div>
          <div className="grid grid-cols-2 items-stretch gap-5">
            {items.map((report) => {
              const Icon = report.type === "energy" ? FileChartColumn : FileCheck2;
              return <Card key={report.id} className="flex flex-col gap-0">
                <CardHeader className="flex flex-row items-start gap-3 border-b pb-5">
                  <div className="shrink-0 rounded-lg border p-3"><Icon className="size-6" aria-hidden="true" /></div>
                  <div className="min-w-0 flex-1 space-y-2">
                    <CardTitle className="leading-6">{report.title}</CardTitle>
                    <Badge variant="outline">{report.status}</Badge>
                  </div>
                </CardHeader>
                <CardContent className="flex flex-1 flex-col gap-5 pt-5">
                  <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-3 text-sm">
                    <dt className="flex items-center gap-2"><CalendarDays className="size-4" aria-hidden="true" />期间</dt><dd className="text-right tabular-nums">{report.period}</dd>
                    <dt className="flex items-center gap-2"><UserRound className="size-4" aria-hidden="true" />负责人</dt><dd className="text-right">{report.owner}</dd>
                    <dt>生成时间</dt><dd className="text-right tabular-nums">{report.generatedAt}</dd>
                  </dl>
                  <ol className="space-y-3 border-t pt-5 text-sm">
                    {report.sections.map((section, i) => <li key={section} className="flex gap-3"><span className="tabular-nums">{String(i + 1).padStart(2, "0")}</span>{section}</li>)}
                  </ol>
                  <div className="mt-auto flex items-center justify-between gap-3 border-t pt-5">
                    <Button variant="ghost" size="sm" onClick={() => void navigate({to: report.type === "energy" ? "/energy-analysis/consumption" : "/optimization/verification", search: {scope: currentScope.id}})}>查看分析来源<ArrowUpRight aria-hidden="true" /></Button>
                    <Button variant="outline" size="sm" onClick={() => setSelectedId(report.id)}>查看报告<ArrowUpRight aria-hidden="true" /></Button>
                  </div>
                </CardContent>
              </Card>;
            })}
          </div>
          {items.length === 0 && <div className="rounded-xl border py-16 text-center text-sm">没有匹配的报告</div>}
        </>
      )}
      <Sheet
        modal={false}
        open={!!selected}
        onOpenChange={(open) => {
          if (!open) setSelectedId(null);
        }}
      >
        <SheetContent
          showOverlay={false}
          className="w-[600px] sm:max-w-[600px] overflow-y-auto"
        >
          <SheetHeader>
            <SheetTitle>{selected?.title}</SheetTitle>
            <SheetDescription>
              {currentScope.name} · {selected?.period}
            </SheetDescription>
          </SheetHeader>
          {selected && (
            <div className="space-y-6 p-6">
              <Badge variant="outline">{selected.status}</Badge>
              <section>
                <h3 className="font-medium">报告内容</h3>
                <ol className="mt-3 list-decimal space-y-3 pl-5 text-sm">
                  {selected.sections.map((x) => (
                    <li key={x}>{x}</li>
                  ))}
                </ol>
              </section>
              {selected.type === "energy" ? (
                <>
                  <MetricStrip
                    items={[
                      {
                        label: "用电",
                        value: (snapshot.summary.totalEnergyKWh / 1000).toFixed(
                          1,
                        ),
                        unit: "MWh",
                      },
                      {
                        label: "费用",
                        value: (snapshot.summary.totalCostCNY / 10000).toFixed(
                          2,
                        ),
                        unit: "万元",
                      },
                    ]}
                  />
                  <Button variant="outline" onClick={download}>
                    <Download aria-hidden="true" data-icon="inline-start" />下载示例报告数据
                  </Button>
                </>
              ) : (
                <Button
                  variant="outline"
                  onClick={() =>
                    void navigate({
                      to: "/optimization/verification",
                      search: { scope: currentScope.id },
                    })
                  }
                >
                  <ArrowUpRight aria-hidden="true" data-icon="inline-start" />查看核算证据
                </Button>
              )}
              <dl className="space-y-3 text-sm">
                <div className="flex justify-between">
                  <dt>负责人</dt>
                  <dd>{selected.owner}</dd>
                </div>
                <div className="flex justify-between">
                  <dt>生成时间</dt>
                  <dd>{selected.generatedAt}</dd>
                </div>
              </dl>
            </div>
          )}
        </SheetContent>
      </Sheet>
    </main>
  );
}
