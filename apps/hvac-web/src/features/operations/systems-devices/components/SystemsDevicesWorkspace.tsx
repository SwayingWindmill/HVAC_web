import {
  operationsFlowQueryOptions,
  useOperationsReset,
} from "@/features/operations-flow/api/operations-flow";
import {
  ArrowLeftRight,
  ArrowUpRight,
  Building2,
  Fan,
  Flame,
  FlaskConical,
  Gauge,
  Network,
  RefreshCw,
  Search,
  Snowflake,
  Waves,
  Wind,
  Zap,
} from "lucide-react";
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
  WorkspaceHeader,
  MetricStrip,
  WorkspaceQueryState,
} from "@/components/analysis/workspace-parts";
import type { ColumnDef } from "@tanstack/react-table";
import type { DataTableFeatures } from "@/components/data-table/data-table-features";
import { useDataTable } from "@/hooks/use-data-table";
import { DataTable } from "@/components/data-table/data-table";

import type { DeviceCategory, DeviceItem } from "../api/systems-devices-types";
const deviceIcons = {
  chiller: Snowflake,
  pump: Waves,
  tower: Fan,
  ahu: Wind,
  boiler: Flame,
  transformer: Zap,
  "heat-exchanger": ArrowLeftRight,
  dosing: FlaskConical,
} satisfies Record<DeviceCategory, typeof Snowflake>;
export function SystemsDevicesWorkspace() {
  const { currentScope } = useWorkspaceScope();
  const search = useSearch({ from: "/_app/_site/operations/systems-devices" });
  const navigate = useNavigate({ from: "/operations/systems-devices" });
  const query = useQuery(operationsFlowQueryOptions(currentScope.id));
  const reset = useOperationsReset(currentScope.id);
  const all = query.data?.devices ?? [];
  const items = all.filter(
    (item) =>
      (!search.q ||
        (
          item.name +
          item.systemName +
          item.spaceName +
          item.specs.manufacturer
        ).includes(search.q)) &&
      (!search.status || item.status === search.status) &&
      (!search.category || item.category === search.category),
  );
  const selected = items.find((item) => item.id === search.inspect);
  const lens = search.lens ?? "system";
  const columns: ColumnDef<DataTableFeatures, DeviceItem>[] = [
    {
      id: "name",
      header: "设备",
      cell: ({ row }) => {
        const Icon = deviceIcons[row.original.category];
        return (
          <div className="flex items-center gap-3 py-2 font-medium">
            <Icon className="size-5 shrink-0" aria-hidden="true" />
            <div>
              {row.original.name}
              <p className="mt-1 text-xs font-normal text-muted-foreground">
                {row.original.categoryLabel}
              </p>
            </div>
          </div>
        );
      },
    },
    {
      id: "context",
      header:
        lens === "system"
          ? "所属系统"
          : lens === "space"
            ? "所在空间"
            : "计量归属",
      cell: ({ row }) =>
        lens === "system"
          ? row.original.systemName
          : lens === "space"
            ? row.original.spaceName
            : row.original.meterName,
    },
    {
      id: "status",
      header: "当前状态",
      cell: ({ row }) => (
        <Badge
          variant={row.original.status === "alarm" ? "destructive" : "outline"}
        >
          {row.original.statusLabel}
        </Badge>
      ),
    },
    {
      id: "power",
      header: "功率 · kW",
      cell: ({ row }) => (
        <span className="tabular-nums">
          {row.original.powerKw.toLocaleString("zh-CN")}
        </span>
      ),
    },
    {
      id: "alarms",
      header: "当前告警",
      cell: ({ row }) => row.original.activeAlarmsCount,
    },
    {
      id: "owner",
      header: "负责人",
      cell: ({ row }) => row.original.specs.responsiblePerson,
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
          查看设备
        </Button>
      ),
    },
  ];
  const table = useDataTable({
    key: "device-inventory-review",
    data: items,
    columns,
    paginate: false,
    getRowId: (item) => item.id,
  });
  return (
    <main className="mx-auto max-w-[1600px] space-y-5 p-6">
      <WorkspaceHeader
        title="系统与设备"
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
          <MetricStrip
            items={[
              { label: "设备总数", value: all.length },
              {
                label: "运行中",
                value: all.filter((x) => x.status === "running").length,
              },
              {
                label: "存在告警",
                value: all.filter((x) => x.activeAlarmsCount > 0).length,
              },
              {
                label: "离线设备",
                value: all.filter((x) => x.status === "offline").length,
              },
            ]}
          />
          <div className="flex flex-wrap items-center gap-3">
            <InputGroup className="w-72">
              <InputGroupAddon>
                <Search aria-hidden="true" />
              </InputGroupAddon>
              <InputGroupInput
                aria-label="搜索设备"
                placeholder="搜索设备、系统或位置"
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
              value={search.category ?? "all"}
              onValueChange={(category) =>
                void navigate({
                  search: (prev) => ({
                    ...prev,
                    category: category === "all" ? undefined : category,
                  }),
                })
              }
            >
              <SelectTrigger aria-label="设备类型" className="w-40">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">全部类型</SelectItem>
                {[
                  ...new Map(
                    all.map((x) => [x.category, x.categoryLabel]),
                  ).entries(),
                ].map(([value, label]) => (
                  <SelectItem key={value} value={value}>
                    {label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Select
              value={search.status ?? "all"}
              onValueChange={(status) =>
                void navigate({
                  search: (prev) => ({
                    ...prev,
                    status:
                      status === "all"
                        ? undefined
                        : (status as
                            "running" | "standby" | "alarm" | "offline"),
                  }),
                })
              }
            >
              <SelectTrigger aria-label="设备状态" className="w-36">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">全部状态</SelectItem>
                <SelectItem value="running">运行</SelectItem>
                <SelectItem value="standby">待机</SelectItem>
                <SelectItem value="alarm">告警</SelectItem>
                <SelectItem value="offline">离线</SelectItem>
              </SelectContent>
            </Select>
            <Tabs
              className="ml-auto"
              value={lens}
              onValueChange={(lens) =>
                void navigate({
                  search: (prev) => ({
                    ...prev,
                    lens: lens as "system" | "space" | "meter",
                  }),
                })
              }
            >
              <TabsList>
                <TabsTrigger
                  value="system"
                  className="inline-flex items-center gap-2"
                >
                  <Network className="size-4" aria-hidden="true" />
                  按系统
                </TabsTrigger>
                <TabsTrigger
                  value="space"
                  className="inline-flex items-center gap-2"
                >
                  <Building2 className="size-4" aria-hidden="true" />
                  按空间
                </TabsTrigger>
                <TabsTrigger
                  value="meter"
                  className="inline-flex items-center gap-2"
                >
                  <Gauge className="size-4" aria-hidden="true" />
                  按计量
                </TabsTrigger>
              </TabsList>
            </Tabs>
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
          if (!open)
            void navigate({
              search: (previous) => ({ ...previous, inspect: undefined }),
            });
        }}
      >
        <SheetContent
          showOverlay={false}
          className="w-[580px] sm:max-w-[580px] overflow-y-auto"
        >
          <SheetHeader>
            <SheetTitle>{selected?.name}</SheetTitle>
            <SheetDescription>
              {selected?.systemName} · {selected?.spaceName}
            </SheetDescription>
          </SheetHeader>
          {selected && (
            <div className="space-y-5 p-6">
              <MetricStrip
                items={[
                  { label: "当前功率", value: selected.powerKw, unit: "kW" },
                  { label: "负荷率", value: selected.loadPercent, unit: "%" },
                ]}
              />
              <Tabs defaultValue="identity" key={selected.id}>
                <TabsList>
                  <TabsTrigger
                    value="identity"
                    className="inline-flex items-center gap-2"
                  >
                    资产信息
                  </TabsTrigger>
                  <TabsTrigger
                    value="operation"
                    className="inline-flex items-center gap-2"
                  >
                    运行与告警
                  </TabsTrigger>
                </TabsList>
                <TabsContent value="identity">
                  <dl className="space-y-4 py-4">
                    {[
                      ["制造商", selected.specs.manufacturer],
                      ["型号", selected.specs.model],
                      ["额定功率", selected.specs.ratedPowerKw + " kW"],
                      ["计量归属", selected.meterName],
                      ["安装日期", selected.specs.installDate],
                      ["下次保养", selected.specs.nextMaintenanceDate],
                      ["负责人", selected.specs.responsiblePerson],
                    ].map(([label, value]) => (
                      <div
                        key={label}
                        className="flex justify-between gap-6 border-b pb-3 text-sm"
                      >
                        <dt>{label}</dt>
                        <dd className="text-right font-medium">{value}</dd>
                      </div>
                    ))}
                  </dl>
                </TabsContent>
                <TabsContent value="operation" className="space-y-4 py-4">
                  <Badge variant="outline">{selected.statusLabel}</Badge>
                  {selected.supplyTemp !== undefined && (
                    <p>
                      供 / 回水温度 {selected.supplyTemp} /{" "}
                      {selected.returnTemp} °C
                    </p>
                  )}
                  {selected.cop !== undefined && <p>COP {selected.cop}</p>}
                  {selected.activeAlarms?.map((alarm) => (
                    <div key={alarm.id} className="rounded-lg border p-3">
                      <p className="font-medium">{alarm.message}</p>
                      <p className="mt-1 text-xs text-muted-foreground">
                        {alarm.triggeredAt}
                      </p>
                    </div>
                  ))}
                  <Button
                    variant="outline"
                    onClick={() =>
                      void navigate({
                        to: "/operations/alarms",
                        search: { site: currentScope.siteId },
                      })
                    }
                  >
                    <ArrowUpRight aria-hidden="true" data-icon="inline-start" />
                    查看相关告警
                  </Button>
                </TabsContent>
              </Tabs>
            </div>
          )}
        </SheetContent>
      </Sheet>
    </main>
  );
}
