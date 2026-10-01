import {
  operationsFlowQueryOptions,
  useOperationsReset,
  useOperationsAction,
} from "@/features/operations-flow/api/operations-flow";
import { ArrowUpRight, RefreshCw, Search } from "lucide-react";
import { DataTableBlock } from "@/blocks/data-table";
import { useQuery } from "@tanstack/react-query";
import { useSearch, useNavigate } from "@tanstack/react-router";
import { useWorkspaceScope } from "@/hooks/use-scope";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import {
  InputGroup,
  InputGroupAddon,
  InputGroupInput,
} from "@/components/ui/input-group";
import { Card, CardHeader, CardTitle, CardContent } from "@/components/ui/card";
import { Tabs, TabsList, TabsTrigger, TabsContent } from "@/components/ui/tabs";
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
} from "@/components/analysis/workspace-parts";
import type { ColumnDef } from "@tanstack/react-table";
import type { DataTableFeatures } from "@/components/data-table/data-table-features";
import { useDataTable } from "@/hooks/use-data-table";
import { DataTable } from "@/components/data-table/data-table";
import {
  Timeline,
  TimelineItem,
  TimelineRail,
  TimelineMarker,
  TimelineConnector,
  TimelineContent,
} from "@/components/ui/timeline";

import type {
  AlarmIssueItem,
  AlarmSeverity,
  IssueState,
} from "../api/alarm-types";
const severityLabels: Record<AlarmSeverity, string> = {
  CRITICAL: "紧急",
  MAJOR: "重要",
  MINOR: "一般",
  WARNING: "预警",
  INFO: "提示",
};
const stateLabels: Record<IssueState, string> = {
  OPEN: "待处理",
  INVESTIGATING: "调查中",
  ACTION_PENDING: "待执行",
  VERIFYING: "验证中",
  RESOLVED: "已解决",
};
const diagnosisLabels = {
  PENDING: "待诊断",
  PUBLISHED: "已发布",
  EVIDENCE_LIMITED: "证据不足",
  ROOT_CAUSE_CONFIRMED: "根因已确认",
};
export function AlarmsWorkspace() {
  const { currentScope } = useWorkspaceScope();
  const search = useSearch({ from: "/_app/_site/operations/alarms" });
  const navigate = useNavigate({ from: "/operations/alarms" });
  const query = useQuery(operationsFlowQueryOptions(currentScope.id));
  const action = useOperationsAction(currentScope.id);
  const reset = useOperationsReset(currentScope.id);
  const all = query.data?.alarms ?? [];
  const active = all.filter((x) => x.state !== "RESOLVED");
  const items = all
    .filter(
      (x) =>
        (!search.device || x.deviceId === search.device) &&
        (!search.q ||
          (x.title + x.deviceLabel + x.locationLabel + x.assigneeName).includes(
            search.q,
          )) &&
        (!search.severity || x.severity === search.severity) &&
        (!search.state || x.state === search.state),
    )
    .sort(
      (a, b) =>
        Object.keys(severityLabels).indexOf(a.severity) -
        Object.keys(severityLabels).indexOf(b.severity),
    );
  const selected = items.find((x) => x.id === search.inspect);
  const distribution = Object.entries(severityLabels).map(([key, name]) => ({
    name,
    count: active.filter((x) => x.severity === key).length,
  }));
  const recurring = [...new Set(all.map((x) => x.deviceLabel))]
    .map((name) => ({
      name,
      count: all
        .filter((x) => x.deviceLabel === name)
        .reduce((sum, x) => sum + x.occurrenceCount, 0),
    }))
    .sort((a, b) => b.count - a.count)
    .slice(0, 5);
  const columns: ColumnDef<DataTableFeatures, AlarmIssueItem>[] = [
    {
      id: "title",
      header: "问题与设备",
      cell: ({ row }) => (
        <div className="max-w-[370px] py-2 font-medium">
          {row.original.title}
          <p className="mt-1 text-xs font-normal text-muted-foreground">
            {row.original.deviceLabel} · {row.original.locationLabel}
          </p>
        </div>
      ),
    },
    {
      id: "severity",
      header: "级别",
      cell: ({ row }) => (
        <Badge
          variant={
            row.original.severity === "CRITICAL" ? "destructive" : "outline"
          }
        >
          {severityLabels[row.original.severity]}
        </Badge>
      ),
    },
    {
      id: "state",
      header: "处理状态",
      cell: ({ row }) => stateLabels[row.original.state],
    },
    {
      id: "ack",
      header: "确认状态",
      cell: ({ row }) => (row.original.acknowledged ? "已确认" : "未确认"),
    },
    {
      id: "diagnosis",
      header: "诊断",
      cell: ({ row }) => diagnosisLabels[row.original.diagnosisState],
    },
    {
      id: "owner",
      header: "负责人",
      cell: ({ row }) => row.original.assigneeName ?? "未分派",
    },
    {
      id: "time",
      header: "持续时间",
      cell: ({ row }) => row.original.durationFormatted,
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
          调查问题
        </Button>
      ),
    },
  ];
  const table = useDataTable({
    key: "alarm-investigation-review",
    data: items,
    columns,
    paginate: false,
    getRowId: (x) => x.id,
  });
  return (
    <main className="mx-auto max-w-[1600px] space-y-5 p-6">
      <WorkspaceHeader
        title="告警与诊断"
        actions={
          <div className="flex gap-2">
            <Button
              variant="outline"
              size="sm"
              disabled={reset.isPending}
              onClick={() => reset.mutate()}
            >
              重置模拟
            </Button>
            <Button
              variant="outline"
              size="sm"
              onClick={() => void query.refetch()}
            >
              <RefreshCw aria-hidden="true" data-icon="inline-start" />
              刷新
            </Button>
          </div>
        }
      />
      <WorkspaceQueryState pending={query.isPending} error={query.error} />
      {query.data && search.inspect && !selected && (
        <div role="alert" className="rounded-lg border p-4 text-sm">
          详情不存在或不属于当前结果
          <Button
            variant="link"
            onClick={() =>
              void navigate({
                search: (previous) => ({ ...previous, inspect: undefined }),
              })
            }
          >
            返回列表
          </Button>
        </div>
      )}
      {query.data && (
        <>
          {search.device && (
            <div className="flex items-center justify-between gap-4 rounded-lg border p-4 text-sm">
              <span>
                关联设备：
                {query.data.devices.find((x) => x.id === search.device)?.name ??
                  "设备不存在"}
              </span>
              <Button
                variant="ghost"
                size="sm"
                onClick={() =>
                  void navigate({
                    search: (previous) => ({
                      ...previous,
                      device: undefined,
                      inspect: undefined,
                    }),
                  })
                }
              >
                查看全部告警
              </Button>
            </div>
          )}
          <MetricStrip
            items={[
              { label: "未解决", value: active.length },
              {
                label: "紧急问题",
                value: active.filter((x) => x.severity === "CRITICAL").length,
              },
              {
                label: "未确认",
                value: all.filter((x) => !x.acknowledged).length,
              },
              {
                label: "证据不足",
                value: active.filter(
                  (x) => x.diagnosisState === "EVIDENCE_LIMITED",
                ).length,
              },
            ]}
          />
          <div className="grid grid-cols-2 gap-5">
            {[
              { title: "未解决问题 · 级别分布", data: distribution },
              { title: "设备重复发生次数", data: recurring },
            ].map((chart) => (
              <Card key={chart.title}>
                <CardHeader>
                  <CardTitle>{chart.title}</CardTitle>
                </CardHeader>
                <CardContent>
                  <ChartContainer
                    className="h-[190px] w-full"
                    config={{
                      count: { label: "次数", color: "var(--chart-1)" },
                    }}
                  >
                    <BarChart
                      data={chart.data}
                      layout="vertical"
                      margin={{ left: 0, right: 16 }}
                      accessibilityLayer
                    >
                      <CartesianGrid horizontal={false} />
                      <XAxis
                        type="number"
                        allowDecimals={false}
                        tickLine={false}
                        axisLine={false}
                      />
                      <YAxis
                        type="category"
                        dataKey="name"
                        width={160}
                        tickLine={false}
                        axisLine={false}
                      />
                      <ChartTooltip content={<ChartTooltipContent />} />
                      <Bar
                        isAnimationActive={false}
                        dataKey="count"
                        fill="var(--color-count)"
                        radius={[0, 4, 4, 0]}
                        maxBarSize={22}
                      />
                    </BarChart>
                  </ChartContainer>
                </CardContent>
              </Card>
            ))}
          </div>
          <div className="flex gap-3">
            <InputGroup className="w-80">
              <InputGroupAddon>
                <Search aria-hidden="true" />
              </InputGroupAddon>
              <InputGroupInput
                aria-label="搜索问题"
                placeholder="搜索问题、设备或负责人"
                value={search.q ?? ""}
                onChange={(e) =>
                  void navigate({
                    search: (prev) => ({
                      ...prev,
                      q: e.target.value || undefined,
                      inspect: undefined,
                    }),
                  })
                }
              />
            </InputGroup>
            <Select
              value={search.severity ?? "all"}
              onValueChange={(severity) =>
                void navigate({
                  search: (prev) => ({
                    ...prev,
                    severity:
                      severity === "all"
                        ? undefined
                        : (severity as AlarmSeverity),
                  }),
                })
              }
            >
              <SelectTrigger aria-label="告警级别" className="w-36">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">全部级别</SelectItem>
                {Object.entries(severityLabels).map(([value, label]) => (
                  <SelectItem key={value} value={value}>
                    {label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Select
              value={search.state ?? "all"}
              onValueChange={(state) =>
                void navigate({
                  search: (prev) => ({
                    ...prev,
                    state: state === "all" ? undefined : (state as IssueState),
                  }),
                })
              }
            >
              <SelectTrigger aria-label="处理状态" className="w-36">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">全部状态</SelectItem>
                {Object.entries(stateLabels).map(([value, label]) => (
                  <SelectItem key={value} value={value}>
                    {label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <DataTableBlock>
            <DataTable table={table} />
          </DataTableBlock>
        </>
      )}
      <Sheet
        modal={false}
        open={!!selected}
        onOpenChange={(open) => {
          if (!open) action.reset();
          if (!open)
            void navigate({
              search: (previous) => ({ ...previous, inspect: undefined }),
            });
        }}
      >
        <SheetContent
          showOverlay={false}
          className="w-[640px] sm:max-w-[640px] overflow-y-auto"
        >
          <SheetHeader>
            <SheetTitle>{selected?.title}</SheetTitle>
            <SheetDescription>
              {selected?.deviceLabel} · {selected?.locationLabel}
            </SheetDescription>
          </SheetHeader>
          {selected && (
            <div className="space-y-5 p-6">
              <div className="flex gap-2">
                <Badge
                  variant={
                    selected.severity === "CRITICAL" ? "destructive" : "outline"
                  }
                >
                  {severityLabels[selected.severity]}
                </Badge>
                <Badge variant="outline">{stateLabels[selected.state]}</Badge>
                <Badge variant="outline">
                  {selected.acknowledged ? "已确认" : "未确认"}
                </Badge>
              </div>
              <p className="text-sm leading-6">{selected.findingSummary}</p>
              <div className="flex gap-2">
                {!selected.acknowledged && (
                  <Button
                    variant="outline"
                    size="sm"
                    disabled={action.isPending}
                    onClick={() =>
                      action.mutate({
                        type: "acknowledge",
                        id: selected.id,
                        at: new Date().toISOString(),
                      })
                    }
                  >
                    确认告警
                  </Button>
                )}
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() =>
                    void navigate({
                      to: "/operations/systems-devices",
                      search: {
                        site: currentScope.siteId,
                        inspect: selected.deviceId,
                      },
                    })
                  }
                >
                  查看关联设备
                </Button>
              </div>
              {action.error && (
                <p role="alert" className="text-sm text-destructive">
                  {action.error.message}
                </p>
              )}
              <Tabs defaultValue="evidence" key={selected.id}>
                <TabsList>
                  <TabsTrigger
                    value="evidence"
                    className="inline-flex items-center gap-2"
                  >
                    诊断证据
                  </TabsTrigger>
                  <TabsTrigger
                    value="hypotheses"
                    className="inline-flex items-center gap-2"
                  >
                    原因与验证
                  </TabsTrigger>
                  <TabsTrigger
                    value="timeline"
                    className="inline-flex items-center gap-2"
                  >
                    处理记录
                  </TabsTrigger>
                </TabsList>
                <TabsContent value="evidence" className="space-y-4 py-4">
                  <p className="text-sm">诊断时测点快照</p>
                  <Table>
                    <TableHeader>
                      <TableRow>
                        <TableHead>测点</TableHead>
                        <TableHead>实际</TableHead>
                        <TableHead>参考</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {selected.telemetryReadings.map((x) => (
                        <TableRow key={x.pointCode}>
                          <TableCell>{x.pointName}</TableCell>
                          <TableCell>
                            {x.currentValue} {x.unit}
                          </TableCell>
                          <TableCell>
                            {x.baselineValue} {x.unit}
                          </TableCell>
                        </TableRow>
                      ))}
                    </TableBody>
                  </Table>
                  {selected.evidences.map((x) => (
                    <section key={x.id} className="rounded-lg border p-4">
                      <h3 className="font-medium">{x.sensorName}</h3>
                      <p className="mt-2 text-sm">
                        实际 {x.actualRange} · 预期 {x.expectedRange}
                      </p>
                      <p className="mt-2 text-sm leading-6">{x.detail}</p>
                    </section>
                  ))}
                </TabsContent>
                <TabsContent value="hypotheses" className="space-y-4 py-4">
                  {selected.hypotheses.map((x) => (
                    <section className="rounded-lg border p-4" key={x.id}>
                      <h3 className="font-medium">{x.title}</h3>
                      <p className="mt-2 text-sm">
                        {
                          {
                            SUPPORTED: "证据支持",
                            WEAKENED: "证据削弱",
                            REJECTED: "已排除",
                            CANDIDATE: "候选原因",
                            CONFIRMED: "已确认",
                          }[x.status]
                        }{" "}
                        · 置信度 {x.confidence}%
                      </p>
                      <p className="mt-2 text-sm leading-6">{x.rationale}</p>
                      <p className="mt-3 text-sm">
                        <strong>验证方法：</strong>
                        {x.verificationMethod}
                      </p>
                    </section>
                  ))}
                </TabsContent>
                <TabsContent value="timeline" className="py-4">
                  <Timeline>
                    {selected.timeline.map((x, i) => (
                      <TimelineItem key={x.timestamp + x.operation}>
                        <TimelineRail>
                          <TimelineMarker />
                          {i < selected.timeline.length - 1 && (
                            <TimelineConnector />
                          )}
                        </TimelineRail>
                        <TimelineContent>
                          <p className="text-sm font-medium">
                            {x.operation} · {x.operator}
                          </p>
                          <p className="mt-1 text-xs text-muted-foreground">
                            {x.timestamp}
                          </p>
                          <p className="mt-2 text-sm">{x.description}</p>
                        </TimelineContent>
                      </TimelineItem>
                    ))}
                  </Timeline>
                </TabsContent>
              </Tabs>
              <Button
                variant="outline"
                onClick={() =>
                  void navigate({
                    to: "/operations/work-center",
                    search: {
                      site: currentScope.siteId,
                      alarm: selected.id,
                      inspect: query.data?.works.find(
                        (work) => work.sourceAlarmCode === selected.code,
                      )?.id,
                    },
                  })
                }
              >
                <ArrowUpRight aria-hidden="true" data-icon="inline-start" />
                查看相关工单
              </Button>
            </div>
          )}
        </SheetContent>
      </Sheet>
    </main>
  );
}
