import { WorkProcessing } from "@/features/operations-flow/components/WorkProcessing";
import {
  operationsFlowQueryOptions,
  useOperationsReset,
  useOperationsAction,
} from "@/features/operations-flow/api/operations-flow";
import {
  ArrowUpRight,
  History,
  Kanban,
  List,
  RefreshCw,
  Search,
} from "lucide-react";
import { DataTableBlock } from "@/blocks/data-table";
import { useQuery } from "@tanstack/react-query";
import { useSearch, useNavigate } from "@tanstack/react-router";
import { useWorkspaceScope } from "@/hooks/use-scope";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Progress } from "@/components/ui/progress";
import {
  InputGroup,
  InputGroupAddon,
  InputGroupInput,
} from "@/components/ui/input-group";

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

import type { WorkOrderItem, WorkOrderStatus } from "../api/work-order-types";
const statuses: Record<WorkOrderStatus, string> = {
  OPEN: "待处理",
  IN_PROGRESS: "进行中",
  BLOCKED: "受阻",
  COMPLETED: "已完成",
  CANCELLED: "已取消",
};
const isOverdue = (work: WorkOrderItem) =>
  !["COMPLETED", "CANCELLED"].includes(work.status) &&
  Date.parse(work.dueAt.replace(" ", "T") + ":00+08:00") < Date.now();
const priorities = { LOW: "低", MEDIUM: "普通", HIGH: "高", URGENT: "紧急" };
const displayTime = (time: string) =>
  time.includes("T")
    ? new Date(time).toLocaleString("zh-CN", { timeZone: "Asia/Shanghai" })
    : time;
export function WorkCenterWorkspace() {
  const { currentScope } = useWorkspaceScope();
  const search = useSearch({ from: "/_app/_site/operations/work-center" });
  const navigate = useNavigate({ from: "/operations/work-center" });
  const query = useQuery(operationsFlowQueryOptions(currentScope.id));
  const action = useOperationsAction(currentScope.id);
  const reset = useOperationsReset(currentScope.id);
  const all = query.data?.works ?? [];
  const items = all.filter(
    (x) =>
      (!search.alarm ||
        x.sourceAlarmCode ===
          query.data?.alarms.find((alarm) => alarm.id === search.alarm)
            ?.code) &&
      (!search.q ||
        (x.title + x.deviceName + x.assigneeName + x.teamName).includes(
          search.q,
        )) &&
      (!search.status || x.status === search.status),
  );
  const selected = items.find((x) => x.id === search.inspect);
  const columns: ColumnDef<DataTableFeatures, WorkOrderItem>[] = [
    {
      id: "title",
      header: "工单与对象",
      cell: ({ row }) => (
        <div className="max-w-[380px] py-2 font-medium">
          {row.original.title}
          <p className="mt-1 text-xs font-normal text-muted-foreground">
            {row.original.deviceName} · {row.original.location}
          </p>
        </div>
      ),
    },
    {
      id: "priority",
      header: "优先级",
      cell: ({ row }) => (
        <Badge
          variant={
            row.original.priority === "URGENT" ? "destructive" : "outline"
          }
        >
          {priorities[row.original.priority]}
        </Badge>
      ),
    },
    {
      id: "status",
      header: "当前状态",
      cell: ({ row }) => statuses[row.original.status],
    },
    {
      id: "owner",
      header: "负责人 / 班组",
      cell: ({ row }) => (
        <div>
          {row.original.assigneeName ?? "未分派"}
          <p className="mt-1 text-xs text-muted-foreground">
            {row.original.teamName}
          </p>
        </div>
      ),
    },
    {
      id: "due",
      header: "截止时间",
      cell: ({ row }) => (
        <span className={isOverdue(row.original) ? "text-destructive" : ""}>
          {row.original.dueAt}
        </span>
      ),
    },
    {
      id: "checklist",
      header: "任务进度",
      cell: ({ row }) => (
        <div className="min-w-24 space-y-2">
          <span className="text-sm tabular-nums">
            {row.original.tasksCompleted} / {row.original.tasksTotal}
          </span>
          {row.original.tasksTotal > 0 && (
            <Progress
              aria-label={`${row.original.title}检查项完成进度`}
              value={
                (100 * row.original.tasksCompleted) / row.original.tasksTotal
              }
              className="h-1.5"
            />
          )}
        </div>
      ),
    },
    {
      id: "action",
      header: "",
      cell: ({ row }) => (
        <Button
          size="sm"
          variant="ghost"
          onClick={() =>
            void navigate({
              search: (previous) => ({ ...previous, inspect: row.original.id }),
            })
          }
        >
          <ArrowUpRight aria-hidden="true" data-icon="inline-start" />
          查看工单
        </Button>
      ),
    },
  ];
  columns.splice(columns.length - 1, 0, {
    id: "recovery",
    header: "恢复验证",
    cell: ({ row }) => {
      if (row.original.status !== "COMPLETED") return "作业未完成";
      const result = query.data?.recovery[row.original.id];
      return result
        ? { PASSED: "已通过", FAILED: "未通过" }[result.status]
        : "待验证";
    },
  });
  const table = useDataTable({
    key: "work-queue-review",
    data: items,
    columns,
    paginate: false,
    getRowId: (x) => x.id,
  });
  return (
    <main className="mx-auto max-w-[1600px] space-y-5 p-6">
      <WorkspaceHeader
        title="工作中心"
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
              size="sm"
              variant="outline"
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
          {search.alarm && (
            <div className="flex items-center justify-between gap-4 rounded-lg border p-4 text-sm">
              <span>
                来源告警：
                {query.data.alarms.find((x) => x.id === search.alarm)?.title ??
                  "告警不存在"}
              </span>
              <Button
                variant="ghost"
                size="sm"
                onClick={() =>
                  void navigate({
                    search: (previous) => ({
                      ...previous,
                      alarm: undefined,
                      inspect: undefined,
                    }),
                  })
                }
              >
                查看全部工单
              </Button>
            </div>
          )}
          <MetricStrip
            items={[
              {
                label: "待处理",
                value: all.filter((x) => x.status === "OPEN").length,
              },
              {
                label: "进行中",
                value: all.filter((x) => x.status === "IN_PROGRESS").length,
              },
              {
                label: "受阻",
                value: all.filter((x) => x.status === "BLOCKED").length,
              },
              {
                label: "逾期未完成",
                value: all.filter(
                  (x) =>
                    isOverdue(x) &&
                    !["COMPLETED", "CANCELLED"].includes(x.status),
                ).length,
              },
            ]}
          />
          <div className="flex items-center gap-3">
            <InputGroup className="w-80">
              <InputGroupAddon>
                <Search aria-hidden="true" />
              </InputGroupAddon>
              <InputGroupInput
                aria-label="搜索工单"
                placeholder="搜索工单、设备或负责人"
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
              value={search.status ?? "all"}
              onValueChange={(status) =>
                void navigate({
                  search: (prev) => ({
                    ...prev,
                    status:
                      status === "all"
                        ? undefined
                        : (status as WorkOrderStatus),
                  }),
                })
              }
            >
              <SelectTrigger aria-label="工单状态" className="w-40">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">全部状态</SelectItem>
                {Object.entries(statuses).map(([value, label]) => (
                  <SelectItem key={value} value={value}>
                    {label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Tabs
              className="ml-auto"
              value={search.view ?? "list"}
              onValueChange={(view) =>
                void navigate({
                  search: (prev) => ({
                    ...prev,
                    view: view as "list" | "board",
                  }),
                })
              }
            >
              <TabsList>
                <TabsTrigger
                  value="list"
                  className="inline-flex items-center gap-2"
                >
                  <List className="size-4" aria-hidden="true" />
                  工单账本
                </TabsTrigger>
                <TabsTrigger
                  value="board"
                  className="inline-flex items-center gap-2"
                >
                  <Kanban className="size-4" aria-hidden="true" />
                  进度看板
                </TabsTrigger>
              </TabsList>
            </Tabs>
          </div>
          {(search.view ?? "list") === "list" ? (
            <DataTableBlock>
              <DataTable table={table} />
            </DataTableBlock>
          ) : (
            <div className="grid grid-cols-4 gap-4">
              {Object.entries(statuses)
                .filter(([key]) => key !== "CANCELLED")
                .map(([stage, label]) => (
                  <section
                    key={stage}
                    className="rounded-xl border bg-muted/20 p-3"
                  >
                    <h2 className="mb-3 flex justify-between text-sm font-semibold">
                      {label}
                      <span>
                        {items.filter((x) => x.status === stage).length}
                      </span>
                    </h2>
                    <div className="space-y-3">
                      {items
                        .filter((x) => x.status === stage)
                        .map((x) => (
                          <button
                            key={x.id}
                            className="w-full rounded-lg border bg-card p-4 text-left shadow-xs hover:border-primary/50 focus-visible:outline-2 focus-visible:outline-primary"
                            onClick={() =>
                              void navigate({
                                search: (previous) => ({
                                  ...previous,
                                  inspect: x.id,
                                }),
                              })
                            }
                          >
                            <Badge
                              variant={
                                x.priority === "URGENT"
                                  ? "destructive"
                                  : "outline"
                              }
                            >
                              {priorities[x.priority]}
                            </Badge>
                            <h3 className="mt-3 text-sm font-medium leading-6">
                              {x.title}
                            </h3>
                            <p className="mt-2 text-xs text-muted-foreground">
                              {x.deviceName}
                            </p>
                            <div className="mt-4 flex justify-between text-xs">
                              <span>{x.assigneeName ?? "未分派"}</span>
                              <span>
                                {x.tasksCompleted}/{x.tasksTotal} 项
                              </span>
                            </div>
                          </button>
                        ))}
                    </div>
                  </section>
                ))}
            </div>
          )}
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
          className="w-[620px] sm:max-w-[620px] overflow-y-auto"
        >
          <SheetHeader>
            <SheetTitle>{selected?.title}</SheetTitle>
            <SheetDescription>
              {selected?.deviceName} · {selected?.location}
            </SheetDescription>
          </SheetHeader>
          {selected && (
            <div className="space-y-5 p-6">
              <div className="flex items-center gap-3">
                <Badge variant="outline">{statuses[selected.status]}</Badge>
                <span className="text-sm">
                  {selected.assigneeName ?? "未分派"} · {selected.teamName}
                </span>
              </div>
              <p className="text-sm leading-6">{selected.description}</p>
              <WorkProcessing
                key={selected.id}
                work={selected}
                recovery={query.data?.recovery[selected.id]}
                action={action}
              />
              <div className="flex gap-2">
                {query.data?.alarms
                  .filter((alarm) => alarm.code === selected.sourceAlarmCode)
                  .map((alarm) => (
                    <Button
                      key={alarm.id}
                      variant="outline"
                      size="sm"
                      onClick={() =>
                        void navigate({
                          to: "/operations/alarms",
                          search: { site: currentScope.siteId, inspect: alarm.id },
                        })
                      }
                    >
                      查看来源告警
                    </Button>
                  ))}
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
                  查看设备状态
                </Button>
              </div>
              {selected.blockReason && (
                <div className="rounded-lg border border-amber-300 bg-amber-50 p-4 text-sm text-amber-950">
                  受阻原因：{selected.blockReason}
                </div>
              )}
              <Tabs key={selected.id} defaultValue="tasks">
                <TabsList>
                  <TabsTrigger
                    value="tasks"
                    className="inline-flex items-center gap-2"
                  >
                    作业检查项
                  </TabsTrigger>
                  <TabsTrigger
                    value="parts"
                    className="inline-flex items-center gap-2"
                  >
                    备件
                  </TabsTrigger>
                  <TabsTrigger
                    value="timeline"
                    className="inline-flex items-center gap-2"
                  >
                    <History className="size-4" aria-hidden="true" />
                    执行记录
                  </TabsTrigger>
                </TabsList>
                <TabsContent value="tasks" className="space-y-3 py-4">
                  {selected.checklist.map((x) => (
                    <section key={x.id} className="rounded-lg border p-4">
                      <div className="flex items-start justify-between gap-3">
                        <h3 className="text-sm font-medium">{x.title}</h3>
                        <Badge variant="outline">
                          {x.completed ? "已完成" : "待执行"}
                        </Badge>
                      </div>
                      <p className="mt-2 text-sm leading-6">{x.requirement}</p>
                      {!x.completed && (
                        <Button
                          className="mt-3"
                          variant="outline"
                          size="sm"
                          disabled={
                            action.isPending ||
                            selected.status !== "IN_PROGRESS"
                          }
                          onClick={() =>
                            action.mutate({
                              type: "task",
                              id: selected.id,
                              taskId: x.id,
                              at: new Date().toISOString(),
                            })
                          }
                        >
                          记录检查项完成
                        </Button>
                      )}
                      {x.completed && (
                        <p className="mt-3 text-xs text-muted-foreground">
                          {x.operator} ·{" "}
                          {x.completedAt && displayTime(x.completedAt)}
                        </p>
                      )}
                    </section>
                  ))}
                </TabsContent>
                <TabsContent value="parts" className="py-4">
                  <Table>
                    <TableHeader>
                      <TableRow>
                        <TableHead>备件与规格</TableHead>
                        <TableHead>需求</TableHead>
                        <TableHead>库存</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {selected.parts.map((x) => (
                        <TableRow key={x.id}>
                          <TableCell>
                            {x.name}
                            <p className="mt-1 text-xs text-muted-foreground">
                              {x.specification}
                            </p>
                          </TableCell>
                          <TableCell>
                            {x.quantity} {x.unit}
                          </TableCell>
                          <TableCell>
                            {x.inStock ? "有库存" : "需采购"}
                          </TableCell>
                        </TableRow>
                      ))}
                    </TableBody>
                  </Table>
                  {selected.parts.length === 0 && (
                    <p className="py-8 text-center text-sm">无需备件</p>
                  )}
                </TabsContent>
                <TabsContent value="timeline" className="py-4">
                  <Timeline>
                    {selected.timeline.map((x, i) => (
                      <TimelineItem key={x.id}>
                        <TimelineRail>
                          <TimelineMarker />
                          {i < selected.timeline.length - 1 && (
                            <TimelineConnector />
                          )}
                        </TimelineRail>
                        <TimelineContent>
                          <p className="text-sm font-medium">
                            {x.action} · {x.operator}
                          </p>
                          <p className="mt-1 text-xs text-muted-foreground">
                            {displayTime(x.time)}
                          </p>
                          <p className="mt-2 text-sm">{x.detail}</p>
                        </TimelineContent>
                      </TimelineItem>
                    ))}
                  </Timeline>
                </TabsContent>
              </Tabs>
            </div>
          )}
        </SheetContent>
      </Sheet>
    </main>
  );
}
