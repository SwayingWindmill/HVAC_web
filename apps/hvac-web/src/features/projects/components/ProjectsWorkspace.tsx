import { optimizationFlowQueryOptions } from "@/features/optimization-flow/api/optimization-flow";
import { PageHeader } from "@/blocks/page-header";
import { Main } from "@/components/layout/Main";
import { ArrowUpRight, Kanban, List, RefreshCw, Search } from "lucide-react";
import { DataTableBlock } from "@/blocks/data-table";
import { useSearch, useNavigate } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { useWorkspaceScope } from "@/hooks/use-scope";
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
import { Alert, AlertTitle, AlertDescription } from "@/components/ui/alert";
import { Skeleton } from "@/components/ui/skeleton";
import type { EnergySavingProject, ProjectStage } from "../api/project-types";
import {
  Timeline,
  TimelineItem,
  TimelineRail,
  TimelineMarker,
  TimelineConnector,
  TimelineContent,
} from "@/components/ui/timeline";
const stages: Record<ProjectStage, string> = {
  PLANNING: "规划",
  DESIGN: "设计",
  IMPLEMENTING: "实施",
  COMMISSIONING: "调试",
  MV_VERIFYING: "验证",
  COMPLETED: "完成",
};
export function ProjectsWorkspace() {
  const { currentScope } = useWorkspaceScope();
  const search = useSearch({ from: "/_app/_site/optimization/projects" });
  const navigate = useNavigate({ from: "/optimization/projects" });
  const query = useQuery(optimizationFlowQueryOptions(currentScope.id));
  const all = query.data?.projects ?? [];
  const sourceOpportunity = query.data?.opportunities.find(
    (item) => item.id === search.opportunity,
  );
  const items = all.filter(
    (item) =>
      (!search.opportunity ||
        item.sourceOpportunityCode === sourceOpportunity?.code) &&
      (!search.q || (item.title + item.owner).includes(search.q)) &&
      (!search.stage || item.stage === search.stage),
  );
  const project = items.find((item) => item.id === search.inspect);
  const origin = query.data?.opportunities.find(
    (item) => item.code === project?.sourceOpportunityCode,
  );
  const verification = query.data?.verifications.find(
    (item) => item.sourceProjectCode === project?.code,
  );
  const view = search.view ?? "list";
  const columns: ColumnDef<DataTableFeatures, EnergySavingProject>[] = [
    {
      id: "title",
      header: "项目与对象",
      cell: ({ row }) => (
        <div className="max-w-[380px] py-2 font-medium">
          {row.original.title}
          <p className="mt-1 text-xs font-normal text-muted-foreground">
            {row.original.location}
          </p>
        </div>
      ),
    },
    {
      id: "stage",
      header: "当前阶段",
      cell: ({ row }) => (
        <Badge variant="outline">{stages[row.original.stage]}</Badge>
      ),
    },
    { id: "owner", header: "负责人", cell: ({ row }) => row.original.owner },
    {
      id: "cost",
      header: "已投入 / 预算",
      cell: ({ row }) => (
        <span className="tabular-nums">
          ¥ {row.original.spentCostCNY.toLocaleString("zh-CN")} /{" "}
          {row.original.budgetCNY.toLocaleString("zh-CN")}
        </span>
      ),
    },
    {
      id: "deadline",
      header: "计划完成",
      cell: ({ row }) => row.original.targetCompletionDate,
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
          查看推进
        </Button>
      ),
    },
  ];
  const table = useDataTable({
    key: "projects-review",
    data: items,
    columns,
    paginate: false,
    getRowId: (item) => item.id,
  });
  return (
    <Main className="space-y-5">
      <PageHeader
        title="节能项目"
        actions={
          <Button
            variant="outline"
            size="sm"
            onClick={() => void query.refetch()}
          >
            <RefreshCw aria-hidden="true" data-icon="inline-start" />
            刷新
          </Button>
        }
      />
      {query.isPending ? (
        <Skeleton className="h-96" />
      ) : query.isError ? (
        <Alert variant="destructive">
          <AlertTitle>项目数据不可用</AlertTitle>
          <AlertDescription>
            {query.error.message}
            <Button variant="link" onClick={() => void query.refetch()}>
              重试
            </Button>
          </AlertDescription>
        </Alert>
      ) : (
        <>
          {search.inspect && !project && (
            <Alert>
              <AlertTitle>项目不存在或已不在当前范围</AlertTitle>
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
                label: "推进中的项目",
                value: all.filter((item) => item.stage !== "COMPLETED").length,
                unit: "项",
              },
              {
                label: "项目预算",
                value: (
                  all.reduce((sum, item) => sum + item.budgetCNY, 0) / 10000
                ).toFixed(1),
                unit: "万元",
              },
              {
                label: "实际投入",
                value: (
                  all.reduce((sum, item) => sum + item.spentCostCNY, 0) / 10000
                ).toFixed(1),
                unit: "万元",
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
          {search.opportunity && (
            <div className="flex items-center justify-between gap-4 rounded-lg border p-4 text-sm">
              <span>
                {sourceOpportunity
                  ? `来源机会：${sourceOpportunity.title}`
                  : "来源机会不存在"}
              </span>
              <Button
                variant="ghost"
                size="sm"
                onClick={() =>
                  void navigate({
                    search: (previous) => ({
                      ...previous,
                      opportunity: undefined,
                      inspect: undefined,
                    }),
                  })
                }
              >
                查看全部项目
              </Button>
            </div>
          )}
          <div className="flex items-center gap-3">
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
                <TabsTrigger
                  value="list"
                  className="inline-flex items-center gap-2"
                >
                  <List className="size-4" aria-hidden="true" />
                  项目账本
                </TabsTrigger>
                <TabsTrigger
                  value="board"
                  className="inline-flex items-center gap-2"
                >
                  <Kanban className="size-4" aria-hidden="true" />
                  阶段看板
                </TabsTrigger>
              </TabsList>
            </Tabs>
            <InputGroup className="ml-auto max-w-72">
              <InputGroupAddon>
                <Search aria-hidden="true" />
              </InputGroupAddon>
              <InputGroupInput
                aria-label="搜索项目"
                placeholder="搜索项目或负责人"
                value={search.q ?? ""}
                onChange={(event) =>
                  void navigate({
                    search: (previous) => ({
                      ...previous,
                      q: event.target.value || undefined,
                      inspect: undefined,
                    }),
                  })
                }
              />
            </InputGroup>
            <Select
              value={search.stage ?? "all"}
              onValueChange={(value) =>
                void navigate({
                  search: (previous) => ({
                    ...previous,
                    stage: value === "all" ? undefined : value,
                    inspect: undefined,
                  }),
                })
              }
            >
              <SelectTrigger className="w-36" aria-label="项目阶段">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">全部阶段</SelectItem>
                {Object.entries(stages).map(([key, label]) => (
                  <SelectItem key={key} value={key}>
                    {label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          {view === "list" ? (
            <DataTableBlock>
              <DataTable
                table={table}
                tableAriaLabel="节能项目账本"
                empty={
                  <div className="space-y-3 py-6">
                    <p>没有匹配的节能项目</p>
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={() =>
                        void navigate({
                          search: (previous) => ({
                            site: previous.site,
                            view: previous.view,
                          }),
                        })
                      }
                    >
                      查看全部项目
                    </Button>
                  </div>
                }
              />
            </DataTableBlock>
          ) : (
            <div className="grid grid-cols-3 gap-5">
              {Object.entries(stages).map(([stage, label]) => (
                <section
                  key={stage}
                  className="space-y-3 rounded-xl border bg-muted/20 p-4"
                >
                  <h2 className="flex items-center justify-between text-sm font-semibold">
                    {label}
                    <Badge variant="outline">
                      {items.filter((item) => item.stage === stage).length}
                    </Badge>
                  </h2>
                  {items
                    .filter((item) => item.stage === stage)
                    .map((item) => (
                      <button
                        key={item.id}
                        className="block w-full rounded-lg border bg-card p-4 text-left hover:border-ring focus-visible:outline-2 focus-visible:outline-ring"
                        onClick={() =>
                          void navigate({
                            search: (previous) => ({
                              ...previous,
                              inspect: item.id,
                            }),
                          })
                        }
                      >
                        <p className="text-sm font-medium">{item.title}</p>
                        <p className="mt-3 text-xs">{item.owner}</p>
                        <p className="mt-2 text-xs text-muted-foreground">
                          计划完成 {item.targetCompletionDate}
                        </p>
                      </button>
                    ))}
                </section>
              ))}
            </div>
          )}
        </>
      )}
      <Sheet
        modal={false}
        open={!!project}
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
          {project && (
            <>
              <SheetHeader>
                <SheetTitle>{project.title}</SheetTitle>
                <SheetDescription>
                  {project.owner} · {stages[project.stage]}
                </SheetDescription>
              </SheetHeader>
              <div className="space-y-5 p-4">
                <p className="text-sm leading-6">{project.summary}</p>
                <dl className="space-y-3 text-sm">
                  <div>
                    <dt className="font-medium">实施期间</dt>
                    <dd className="mt-1">
                      {project.startDate} — {project.targetCompletionDate}
                    </dd>
                  </div>
                  <div>
                    <dt className="font-medium">关联对象</dt>
                    <dd className="mt-1">{project.targetDevices.join("、")}</dd>
                  </div>
                </dl>
                <div className="flex flex-wrap gap-2 border-y py-4">
                  {origin && (
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={() =>
                        void navigate({
                          to: "/optimization/opportunities",
                          search: {
                            site: currentScope.siteId,
                            inspect: origin.id,
                          },
                        })
                      }
                    >
                      <ArrowUpRight aria-hidden="true" />
                      查看来源机会
                    </Button>
                  )}
                  {verification && (
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={() =>
                        void navigate({
                          to: "/optimization/verification",
                          search: {
                            site: currentScope.siteId,
                            project: project.id,
                            record: verification.id,
                          },
                        })
                      }
                    >
                      <ArrowUpRight aria-hidden="true" />
                      查看节能验证
                    </Button>
                  )}
                </div>
                <Tabs key={project.id} defaultValue="milestones">
                  <TabsList>
                    <TabsTrigger
                      value="milestones"
                      className="inline-flex items-center gap-2"
                    >
                      里程碑
                    </TabsTrigger>
                    <TabsTrigger
                      value="handover"
                      className="inline-flex items-center gap-2"
                    >
                      工程交接
                    </TabsTrigger>
                    <TabsTrigger
                      value="verification"
                      className="inline-flex items-center gap-2"
                    >
                      验证计划
                    </TabsTrigger>
                  </TabsList>
                  <TabsContent value="milestones" className="pt-4">
                    <Timeline>
                      {project.milestones.map((item) => (
                        <TimelineItem
                          key={item.id}
                          status={
                            item.status === "DONE"
                              ? "done"
                              : item.status === "IN_PROGRESS"
                                ? "current"
                                : "default"
                          }
                        >
                          <TimelineRail>
                            <TimelineMarker />
                            <TimelineConnector />
                          </TimelineRail>
                          <TimelineContent className="pb-5">
                            <p className="text-sm font-medium">{item.title}</p>
                            <Badge variant="outline" className="mt-2">
                              {
                                {
                                  DONE: "已完成",
                                  IN_PROGRESS: "进行中",
                                  PENDING: "待启动",
                                }[item.status]
                              }
                            </Badge>
                            <p className="mt-1 text-xs text-muted-foreground">
                              {item.targetDate} · {item.owner}
                            </p>
                            {item.remark && (
                              <p className="mt-2 text-sm">{item.remark}</p>
                            )}
                          </TimelineContent>
                        </TimelineItem>
                      ))}
                    </Timeline>
                  </TabsContent>
                  <TabsContent value="handover" className="space-y-4 pt-4">
                    {[
                      {
                        label: "设定变更",
                        value: project.technicalHandover.setpointChanges,
                      },
                      {
                        label: "联锁要求",
                        value: project.technicalHandover.interlockRules,
                      },
                      {
                        label: "回滚触发",
                        value: project.technicalHandover.rollbackTrigger,
                      },
                      {
                        label: "安全边界",
                        value: project.technicalHandover.safetyBoundaries,
                      },
                    ].map((item) => (
                      <section key={item.label}>
                        <h3 className="text-sm font-semibold">{item.label}</h3>
                        <p className="mt-2 text-sm leading-relaxed">
                          {item.value}
                        </p>
                      </section>
                    ))}
                  </TabsContent>
                  <TabsContent value="verification" className="space-y-4 pt-4">
                    <Badge variant="outline">
                      {project.mvPlan.ipmvpOption}
                    </Badge>
                    <p className="text-sm">
                      {project.mvPlan.measurementBoundary}
                    </p>
                    <p className="text-sm">{project.mvPlan.baselineModel}</p>
                    <p className="text-sm">{project.mvPlan.reportingPeriod}</p>
                    {!verification && (
                      <p className="text-sm">
                        尚未开始测量，当前仅有验证计划。
                      </p>
                    )}
                    {verification && (
                      <Button
                        variant="outline"
                        onClick={() =>
                          void navigate({
                            to: "/optimization/verification",
                            search: {
                              site: currentScope.siteId,
                              project: project.id,
                              record: verification?.id,
                            },
                          })
                        }
                      >
                        进入节能验证
                      </Button>
                    )}
                  </TabsContent>
                </Tabs>
              </div>
            </>
          )}
        </SheetContent>
      </Sheet>
    </Main>
  );
}
