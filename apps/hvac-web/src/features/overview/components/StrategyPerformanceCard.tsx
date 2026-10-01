import { ArrowUpRight, ChevronRight } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardHeader,
  CardTitle,
  CardAction,
  CardContent,
} from "@/components/ui/card";
import {
  formatOverviewNumber as number,
  type OverviewDashboardData,
  type OverviewStrategy,
} from "../api/overview-types";
export function StrategyPerformanceCard({
  data,
  onSelect,
  onNavigate,
}: {
  readonly data: OverviewDashboardData;
  readonly onSelect: (strategy: OverviewStrategy) => void;
  readonly onNavigate: (path: string) => void;
}) {
  return (
    <Card className="h-full rounded-xl gap-3">
      <CardHeader>
        <CardTitle className="text-base">哪些策略正在贡献节能</CardTitle>
        <CardAction>
          <Button
            variant="ghost"
            size="sm"
            onClick={() => onNavigate("/operations/control")}
          >
            策略中心
            <ArrowUpRight />
          </Button>
        </CardAction>
      </CardHeader>
      <CardContent className="flex-1 divide-y">
        {data.strategies.length ? (
          data.strategies.map((strategy) => (
            <button
              key={strategy.title}
              className="flex w-full items-center gap-4 py-3.5 text-left rounded-sm hover:bg-muted/40 focus-visible:outline-2 focus-visible:outline-ring"
              onClick={() => onSelect(strategy)}
            >
              <div className="min-w-0 flex-1">
                <div className="flex items-center gap-2">
                  <span className="text-sm font-medium">{strategy.title}</span>
                  <span className="text-xs text-muted-foreground">
                    {strategy.status}
                  </span>
                </div>
                <p className="mt-1 text-xs text-muted-foreground">
                  {strategy.description}
                </p>
              </div>
              <div className="shrink-0 text-right">
                <p className="text-sm font-semibold tabular-nums">
                  {number(
                    strategy.savingKWh === null
                      ? null
                      : strategy.savingKWh / 1000,
                  )}{" "}
                  <span className="font-normal text-xs text-muted-foreground">
                    MWh
                  </span>
                </p>
                <p className="mt-1 text-xs text-muted-foreground">估算贡献</p>
              </div>
              <ChevronRight className="size-4 shrink-0 text-muted-foreground" />
            </button>
          ))
        ) : (
          <p className="py-8 text-sm text-muted-foreground">尚无策略运行记录</p>
        )}
      </CardContent>
    </Card>
  );
}
