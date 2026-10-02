import { useEffect, useState } from "react";
import { useNavigate, useSearch } from "@tanstack/react-router";
import { ArrowRight, CircleAlert } from "lucide-react";
import { useWorkspaceScope } from "@/hooks/use-scope";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet";
import {
  isOverviewExample,
  OverviewUnsupportedError,
} from "../api/overview-service";
import {
  formatOverviewNumber as number,
  type OverviewOpportunity,
  type OverviewStrategy,
} from "../api/overview-types";
import { downloadOverviewSummary } from "../api/overview-export";
import { useOverviewDashboard } from "../queries/use-overview-query";
import { OverviewHeader } from "./OverviewHeader";
import { OverviewKpiGrid } from "./OverviewKpiGrid";
import { EnergyBaselineChart } from "./EnergyBaselineChart";
import { StrategyPerformanceCard } from "./StrategyPerformanceCard";
import { ComfortGuardrailCard } from "./ComfortGuardrailCard";
import {
  OpportunitySummaryCard,
  AttentionSummaryCard,
} from "./OverviewActionCards";

interface OverviewQuickDetail {
  readonly title: string;
  readonly description: string;
  readonly facts: readonly { readonly label: string; readonly value: string }[];
  readonly path: string;
  readonly action: string;
}
export function OverviewDashboard() {
  const { currentScope } = useWorkspaceScope();
  const search = useSearch({ from: "/_app/_site/overview" });
  const period = search.period ?? (isOverviewExample ? "month" : "today");
  const navigate = useNavigate({ from: "/overview" });
  const [detail, setDetail] = useState<OverviewQuickDetail | null>(null);
  useEffect(() => {
    setDetail(null);
  }, [currentScope.id, period]);
  const { data, isPending, isError, error, refetch, isFetching } =
    useOverviewDashboard(currentScope.id, currentScope.name, period);
  const onNavigate = (path: string) => {
    setDetail(null);
    void navigate({
      to: path,
      search: () => ({ site: currentScope.siteId, period }),
    });
  };
  const baselineDetail = () => {
    if (!data) return;
    setDetail({
      title: "基线与节能计算口径",
      description:
        "在相同期间与计量边界内比较基线和实际，避免将同比下降当作核证收益。",
      facts: [
        { label: "统计期间", value: data.dateRange },
        {
          label: "计量边界",
          value:
            data.mode === "example"
              ? "当前范围内 HVAC 用电"
              : "平台当前概况 · 详细边界未提供",
        },
        {
          label: "基线方法",
          value: data.baselineMethod ?? "尚未提供，暂不能核查调整方法",
        },
        {
          label: "节省关系",
          value:
            data.mode === "example"
              ? "调整后基线用电 − 实际用电"
              : "平台返回的节省量 · 计算明细未提供",
        },
        {
          label: "验证状态",
          value:
            data.mode === "example"
              ? "示例估算 · 非认证结果"
              : "待验证 · 尚无核证证据",
        },
        {
          label: "费用口径",
          value:
            data.mode === "example"
              ? "示例均价 0.843 元/kWh，未计入移峰与需量收益"
              : "平台提供的估算值，结算与移峰明细尚未提供",
        },
      ],
      path: "/optimization/verification",
      action: "进入节能验证",
    });
  };
  const comfortDetail = () => {
    if (!data) return;
    setDetail({
      title: "舒适度约束",
      description: "舒适合规与数据覆盖是独立事实，缺测不能解释为正常。",
      facts: [
        {
          label: data.mode === "example" ? "占用时段合规率" : "平台舒适合规率",
          value: number(data.comfort.rate) + "%",
        },
        {
          label: "数据覆盖率",
          value:
            data.comfort.coverage === null
              ? "尚未提供"
              : number(data.comfort.coverage) + "%",
        },
        {
          label: "统计范围",
          value:
            data.mode === "example"
              ? "示例：室内温度与 CO₂，占用时段；非全建筑核证"
              : "平台尚未提供统计对象与占用时段说明",
        },
        ...data.comfort.exceptions.map((item) => ({
          label: item.location,
          value: item.detail,
        })),
      ],
      path: "/operations/realtime",
      action: "进入实时运行",
    });
  };
  const strategyDetail = (strategy: OverviewStrategy) =>
    setDetail({
      title: strategy.title,
      description:
        "策略运行状态不代表收益已经验证，控制动作需在策略中心按权限执行。",
      facts: [
        { label: "当前状态", value: strategy.status },
        { label: "作用范围", value: strategy.description },
        { label: "期间估算贡献", value: number(strategy.savingKWh) + " kWh" },
        {
          label: "核证说明",
          value: "贡献可能与其它策略重叠，不直接相加作为全站收益",
        },
      ],
      path: "/operations/control",
      action: "进入策略中心",
    });
  const opportunityDetail = (item: OverviewOpportunity) =>
    setDetail({
      title: item.title,
      description: item.reason,
      facts: [
        { label: "关联对象", value: item.object },
        { label: "估算潜力", value: item.potential },
        { label: "下一步", value: item.action },
        {
          label: "实施状态",
          value: "先检查证据，在机会工作区评估并创建实施任务",
        },
      ],
      path: "/optimization/opportunities",
      action: "进入节能机会",
    });
  return (
    <div className="mx-auto flex min-h-full w-full max-w-[1600px] flex-col gap-5 p-6">
      <OverviewHeader
        period={period}
        example={isOverviewExample}
        onPeriodChange={(next) => {
          setDetail(null);
          void navigate({
            search: (previous) => ({ ...previous, period: next }),
          });
        }}
        onExport={() => {
          if (data) downloadOverviewSummary(data);
        }}
        onRefresh={() => {
          void refetch();
        }}
        refreshing={isFetching}
        hasData={!!data && !isError}
      />
      {isPending ? (
        <div className="space-y-5" role="status" aria-label="正在加载节能概况">
          <Skeleton className="h-32 w-full" />
          <div className="grid grid-cols-3 gap-5">
            <Skeleton className="col-span-2 h-[350px]" />
            <Skeleton className="h-[350px]" />
          </div>
        </div>
      ) : isError ? (
        <Alert variant="destructive">
          <CircleAlert />
          <AlertTitle>当前范围的节能概况不可用</AlertTitle>
          <AlertDescription>
            <p>
              {error instanceof OverviewUnsupportedError
                ? error.message
                : "暂时无法读取平台数据，请重试。"}
            </p>
            <div className="mt-3 flex gap-2">
              <Button
                size="sm"
                variant="outline"
                onClick={() => void refetch()}
              >
                重试
              </Button>
              <Button
                size="sm"
                variant="outline"
                onClick={() =>
                  void navigate({
                    search: (previous) => ({ ...previous, period: "today" }),
                  })
                }
              >
                查看今日
              </Button>
            </div>
          </AlertDescription>
        </Alert>
      ) : (
        data && (
          <>
            <div className="flex items-center justify-between gap-4 text-xs">
              <p>
                {data.dateRange} <span className="mx-2 text-border">/</span>{" "}
                {data.mode === "example" ? "HVAC 用电" : "平台当前概况"}
              </p>
              <p>
                更新于{" "}
                {new Date(data.asOf).toLocaleString("zh-CN", {
                  timeZone: "Asia/Shanghai",
                  month: "2-digit",
                  day: "2-digit",
                  hour: "2-digit",
                  minute: "2-digit",
                  hour12: false,
                })}
              </p>
            </div>
            {data.attention.some((item) => item.severity === "risk") && (
              <Alert variant="destructive">
                <CircleAlert />
                <AlertTitle>有高风险运行异常需要优先处理</AlertTitle>
                <AlertDescription>
                  <Button
                    variant="link"
                    className="p-0"
                    onClick={() => onNavigate("/operations/alarms")}
                  >
                    进入异常与告警
                    <ArrowRight />
                  </Button>
                </AlertDescription>
              </Alert>
            )}
            <OverviewKpiGrid data={data} />
            <EnergyBaselineChart
              data={data}
              onDetail={baselineDetail}
              onNavigate={onNavigate}
            />
            <div className="grid grid-cols-2 items-stretch gap-5">
              <StrategyPerformanceCard
                data={data}
                onSelect={strategyDetail}
                onNavigate={onNavigate}
              />
              <ComfortGuardrailCard data={data} onDetail={comfortDetail} />
            </div>
            <div className="grid grid-cols-12 items-stretch gap-5">
              <div className="col-span-7">
                <OpportunitySummaryCard
                  data={data}
                  onSelect={opportunityDetail}
                  onNavigate={onNavigate}
                />
              </div>
              <div className="col-span-5">
                <AttentionSummaryCard data={data} onNavigate={onNavigate} />
              </div>
            </div>
            <section
              aria-labelledby="system-performance"
              className="rounded-xl border bg-card px-5 py-4"
            >
              <div className="mb-4 flex items-center justify-between">
                <h2 id="system-performance" className="text-sm font-semibold">
                  系统效率概况
                </h2>
                <Button
                  size="sm"
                  variant="ghost"
                  onClick={() => onNavigate("/energy-analysis/benchmarking")}
                >
                  绩效与对标
                  <ArrowRight />
                </Button>
              </div>
              <div className="grid grid-cols-3 divide-x">
                {data.systems.map((system) => (
                  <div key={system.name} className="px-5 first:pl-0">
                    <div className="flex items-center justify-between">
                      <span className="text-sm">{system.name}</span>
                      <span className="text-xs text-muted-foreground">
                        {system.metric}
                      </span>
                    </div>
                    {system.value.absence === null ? (
                      <p className="mt-2 text-xl font-semibold tabular-nums">
                        {number(system.value.value, 2)}{" "}
                        <span className="text-xs font-normal text-muted-foreground">
                          {system.unit}
                        </span>
                      </p>
                    ) : (
                      <p className="mt-2 text-base font-medium leading-7 text-muted-foreground">
                        {system.value.absence}
                      </p>
                    )}
                    <p className="mt-1 text-xs text-muted-foreground">
                      {system.reference}
                    </p>
                  </div>
                ))}
              </div>
            </section>
          </>
        )
      )}
      <Sheet
        modal={false}
        open={detail !== null}
        onOpenChange={(open) => {
          if (!open) setDetail(null);
        }}
      >
        <SheetContent
          showOverlay={false}
          className="w-[440px] sm:max-w-[440px] overflow-y-auto"
        >
          {detail && (
            <>
              <SheetHeader>
                <SheetTitle>{detail.title}</SheetTitle>
                <SheetDescription>{detail.description}</SheetDescription>
              </SheetHeader>
              <dl className="mx-4 mt-5 divide-y">
                {detail.facts.map((fact) => (
                  <div key={fact.label} className="py-4">
                    <dt className="mb-1 text-xs text-muted-foreground">
                      {fact.label}
                    </dt>
                    <dd className="text-sm leading-relaxed">{fact.value}</dd>
                  </div>
                ))}
              </dl>
              <div className="p-4">
                <Button onClick={() => onNavigate(detail.path)}>
                  {detail.action}
                  <ArrowRight />
                </Button>
              </div>
            </>
          )}
        </SheetContent>
      </Sheet>
    </div>
  );
}
