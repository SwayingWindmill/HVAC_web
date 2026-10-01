import { ArrowUpRight, History, RefreshCw, Search, ShieldCheck, Workflow } from "lucide-react";
import { DataTableBlock } from "@/blocks/data-table";
import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { useSearch, useNavigate } from "@tanstack/react-router";
import { useScope } from "@/hooks/use-scope";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { InputGroup, InputGroupAddon, InputGroupInput } from "@/components/ui/input-group";
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
  isWorkspaceExample,
} from "@/components/analysis/workspace-parts";
import type { ColumnDef } from "@tanstack/react-table";
import type { DataTableFeatures } from "@/components/data-table/data-table-features";
import { useDataTable } from "@/hooks/use-data-table";
import { DataTable } from "@/components/data-table/data-table";

import { controlService } from "../api/control-service";
import type { ControlCommandItem } from "../api/control-types";
const commandStates = {
  REQUESTED: "已请求",
  ACKNOWLEDGED: "现场已接收",
  READBACK_MATCHED: "读回一致",
  VERIFIED: "结果已验证",
  FAILED: "执行失败",
};
export function ControlWorkspace() {
  const { currentScope } = useScope();
  const search = useSearch({ from: "/_app/operations/control" });
  const navigate = useNavigate({ from: "/operations/control" });
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const query = useQuery({
    queryKey: ["control-evidence-review", currentScope.id],
    queryFn: async () => {
      if (!isWorkspaceExample || currentScope.id !== "site:site-01")
        throw new Error("控制执行与安全约束未接入");
      return controlService.getControlWorkspaceData();
    },
    retry: false,
  });
  const data = query.data;
  const items = (data?.commands ?? []).filter(
    (x) =>
      !search.q ||
      (x.targetAssetName + x.capabilityLabel + x.operator + x.reason).includes(
        search.q,
      ),
  );
  const selected = data?.commands.find((x) => x.id === selectedId);
  const columns: ColumnDef<DataTableFeatures, ControlCommandItem>[] = [
    {
      id: "target",
      header: "目标与动作",
      cell: ({ row }) => (
        <div className="py-2 font-medium">
          {row.original.targetAssetName}
          <p className="mt-1 text-xs font-normal text-muted-foreground">
            {row.original.capabilityLabel}
          </p>
        </div>
      ),
    },
    {
      id: "requested",
      header: "请求值",
      cell: ({ row }) => `${row.original.requestedValue} ${row.original.unit}`,
    },
    {
      id: "readback",
      header: "现场读回",
      cell: ({ row }) => `${row.original.readbackValue} ${row.original.unit}`,
    },
    {
      id: "state",
      header: "执行阶段",
      cell: ({ row }) => (
        <Badge
          variant={row.original.state === "FAILED" ? "destructive" : "outline"}
        >
          {commandStates[row.original.state]}
        </Badge>
      ),
    },
    {
      id: "operator",
      header: "发起人",
      cell: ({ row }) => row.original.operator,
    },
    {
      id: "time",
      header: "执行时间",
      cell: ({ row }) => row.original.executedAt,
    },
    {
      id: "action",
      header: "",
      cell: ({ row }) => (
        <Button
          size="sm"
          variant="ghost"
          onClick={() => setSelectedId(row.original.id)}
        >
          <ArrowUpRight aria-hidden="true" data-icon="inline-start" />查看执行
        </Button>
      ),
    },
  ];
  const table = useDataTable({
    key: "control-evidence-review",
    data: items,
    columns,
    paginate: false,
    getRowId: (x) => x.id,
  });
  return (
    <main className="mx-auto max-w-[1600px] space-y-5 p-6">
      <WorkspaceHeader
        title="控制与策略"
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
      {data && (
        <>
          <MetricStrip
            items={[
              {
                label: "生效策略",
                value: data.strategies.filter((x) => x.status === "ACTIVE")
                  .length,
              },
              { label: "人工覆盖", value: data.summary.activeOverrideCount },
              {
                label: "记录中待验证",
                value: data.commands.filter(
                  (x) => !["VERIFIED", "FAILED"].includes(x.state),
                ).length,
              },
              {
                label: "异常安全约束",
                value: data.interlocks.filter((x) => x.status !== "HEALTHY")
                  .length,
              },
            ]}
          />
          <div className="flex items-center justify-between rounded-lg border px-5 py-3 text-sm">
            <span>当前控制权限</span>
            <strong>{data.summary.activeAuthorityLabel}</strong>
          </div>
          <Tabs
            value={search.view ?? "commands"}
            onValueChange={(view) =>
              void navigate({
                search: (prev) => ({
                  ...prev,
                  view: view as "commands" | "strategies" | "interlocks",
                }),
              })
            }
          >
            <TabsList>
              <TabsTrigger value="commands" className="inline-flex items-center gap-2"><History className="size-4" aria-hidden="true" />执行记录</TabsTrigger>
              <TabsTrigger value="strategies" className="inline-flex items-center gap-2"><Workflow className="size-4" aria-hidden="true" />策略方案</TabsTrigger>
              <TabsTrigger value="interlocks" className="inline-flex items-center gap-2"><ShieldCheck className="size-4" aria-hidden="true" />安全约束</TabsTrigger>
            </TabsList>
            <TabsContent value="commands" className="space-y-4">
              <InputGroup className="w-80"><InputGroupAddon><Search aria-hidden="true" /></InputGroupAddon><InputGroupInput
                aria-label="搜索执行记录"
                placeholder="搜索目标、动作或发起人"
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
              <DataTableBlock>
                <DataTable table={table} />
              </DataTableBlock>
            </TabsContent>
            <TabsContent value="strategies" className="space-y-4">
              {data.strategies.map((strategy) => (
                <Card key={strategy.id}>
                  <CardHeader className="flex flex-row items-center justify-between">
                    <CardTitle>{strategy.name}</CardTitle>
                    <Badge variant="outline">
                      {
                        {
                          ACTIVE: "运行中",
                          STANDBY: "待机",
                          CANDIDATE: "候选",
                          DISABLED: "已停用",
                        }[strategy.status]
                      }
                    </Badge>
                  </CardHeader>
                  <CardContent>
                    <p className="mb-4 max-w-4xl text-sm leading-6">
                      {strategy.description}
                    </p>
                    <Table>
                      <TableHeader>
                        <TableRow>
                          <TableHead>工程参数</TableHead>
                          <TableHead>当前设置</TableHead>
                          <TableHead>原始设置</TableHead>
                        </TableRow>
                      </TableHeader>
                      <TableBody>
                        {strategy.parameters.map((x) => (
                          <TableRow key={x.name}>
                            <TableCell>{x.name}</TableCell>
                            <TableCell className="font-medium">
                              {x.value}
                            </TableCell>
                            <TableCell>{x.defaultVal}</TableCell>
                          </TableRow>
                        ))}
                      </TableBody>
                    </Table>
                    <p className="mt-4 text-xs text-muted-foreground">
                      最近启用 {strategy.lastActivatedAt} · {strategy.author}
                    </p>
                  </CardContent>
                </Card>
              ))}
            </TabsContent>
            <TabsContent value="interlocks">
              <Table>
                <TableHeader>
                  <TableRow>
                    {[
                      "安全约束",
                      "触发条件",
                      "保护动作",
                      "状态",
                      "最近检验",
                    ].map((x) => (
                      <TableHead key={x}>{x}</TableHead>
                    ))}
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {data.interlocks.map((x) => (
                    <TableRow key={x.id}>
                      <TableCell className="max-w-64 font-medium">
                        {x.name}
                        <p className="mt-1 text-xs font-normal text-muted-foreground">
                          {x.scope}
                        </p>
                      </TableCell>
                      <TableCell className="max-w-64 whitespace-normal">
                        {x.condition}
                      </TableCell>
                      <TableCell className="max-w-64 whitespace-normal">
                        {x.action}
                      </TableCell>
                      <TableCell>
                        <Badge
                          variant={
                            x.status !== "HEALTHY" ? "destructive" : "outline"
                          }
                        >
                          {
                            {
                              HEALTHY: "正常",
                              ALERT: "触发",
                              BYPASSED: "已旁路",
                            }[x.status]
                          }
                        </Badge>
                      </TableCell>
                      <TableCell>{x.lastTested}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </TabsContent>
          </Tabs>
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
          className="w-[580px] sm:max-w-[580px] overflow-y-auto"
        >
          <SheetHeader>
            <SheetTitle>{selected?.capabilityLabel}</SheetTitle>
            <SheetDescription>
              {selected?.targetAssetName} · {selected?.executedAt}
            </SheetDescription>
          </SheetHeader>
          {selected && (
            <div className="space-y-6 p-6">
              <Badge
                variant={
                  selected.state === "FAILED" ? "destructive" : "outline"
                }
              >
                {commandStates[selected.state]}
              </Badge>
              <MetricStrip
                items={[
                  {
                    label: "请求值",
                    value: selected.requestedValue,
                    unit: selected.unit,
                  },
                  {
                    label: "现场读回",
                    value: selected.readbackValue,
                    unit: selected.unit,
                  },
                ]}
              />
              <section>
                <h3 className="font-medium">执行原因</h3>
                <p className="mt-2 text-sm leading-6">{selected.reason}</p>
              </section>
              <section>
                <h3 className="font-medium">影响范围</h3>
                <p className="mt-2 text-sm">{selected.impactRadius}</p>
              </section>
              <section>
                <h3 className="font-medium">
                  前置约束 · {selected.interlocksPassed ? "检查通过" : "未通过"}
                </h3>
                <ul className="mt-3 list-disc space-y-2 pl-5 text-sm">
                  {selected.preconditions.map((x) => (
                    <li key={x}>{x}</li>
                  ))}
                </ul>
              </section>
              <p className="text-sm">发起人：{selected.operator}</p>
            </div>
          )}
        </SheetContent>
      </Sheet>
    </main>
  );
}
