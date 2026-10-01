import {
  Area,
  CartesianGrid,
  ComposedChart,
  Line,
  XAxis,
  YAxis,
} from "recharts";
import { ArrowUpRight } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardHeader,
  CardTitle,
  CardDescription,
  CardContent,
  CardAction,
} from "@/components/ui/card";
import {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from "@/components/ui/chart";
import {
  formatOverviewNumber as number,
  type OverviewDashboardData,
} from "../api/overview-types";
const config = {
  actual: { label: "实际用电", color: "var(--chart-1)" },
  baseline: { label: "基线", color: "var(--muted-foreground)" },
  difference: { label: "低于基线的部分", color: "#10b981" },
} satisfies ChartConfig;
export function EnergyBaselineChart({
  data,
  onDetail,
  onNavigate,
}: {
  readonly data: OverviewDashboardData;
  readonly onDetail: () => void;
  readonly onNavigate: (path: string) => void;
}) {
  const points = data.trendSeries.map((point) => ({
    ...point,
    difference:
      point.actual === null ||
      point.baseline === null ||
      point.actual > point.baseline
        ? null
        : [point.actual, point.baseline],
  }));
  return (
    <Card className="h-full gap-4 rounded-xl">
      <CardHeader>
        <CardTitle className="text-base">基线与实际</CardTitle>
        <CardDescription>
          {data.trendUnit === "kWh" ? "期间用电量" : "实时负荷"}（
          {data.trendUnit}）
        </CardDescription>
        <CardAction className="flex items-center gap-2">
          <Button variant="ghost" size="sm" onClick={onDetail}>
            基线与计算口径
          </Button>
          <Button
            variant="ghost"
            size="sm"
            onClick={() => onNavigate("/energy-analysis/consumption")}
          >
            能源分析
            <ArrowUpRight />
          </Button>
        </CardAction>
      </CardHeader>
      <CardContent>
        <div className="mb-3 flex items-center gap-5 text-xs">
          <span className="flex items-center gap-2">
            <span className="h-0.5 w-5 bg-[var(--chart-1)]" />
            实际
          </span>
          <span className="flex items-center gap-2 text-muted-foreground">
            <span className="w-5 border-t-2 border-dashed border-muted-foreground" />
            基线
          </span>
          <span className="flex items-center gap-2 text-muted-foreground">
            <span className="size-2 rounded-sm bg-emerald-500/20" />
            低于基线
          </span>
        </div>
        {points.length ? (
          <ChartContainer
            config={config}
            className="h-[235px] w-full aspect-auto"
          >
            <ComposedChart
              accessibilityLayer
              data={points}
              margin={{ left: 5, right: 12, top: 12, bottom: 0 }}
            >
              <CartesianGrid vertical={false} strokeDasharray="3 3" />
              <XAxis
                dataKey="time"
                padding={{ left: 8, right: 18 }}
                tickLine={false}
                axisLine={false}
                tickMargin={10}
                minTickGap={30}
              />
              <YAxis
                tickLine={false}
                axisLine={false}
                width={48}
                tickFormatter={(value) =>
                  value >= 1000 ? (value / 1000).toFixed(0) + "k" : value
                }
              />
              <ChartTooltip
                content={
                  <ChartTooltipContent
                    hideIndicator
                    formatter={(value, name) => (
                      <div className="flex w-full justify-between gap-6 text-xs">
                        <span className="text-muted-foreground">
                          {name === "actual" ? "实际" : "基线"}
                        </span>
                        <span className="font-medium tabular-nums">
                          {number(typeof value === "number" ? value : null)}{" "}
                          {data.trendUnit}
                        </span>
                      </div>
                    )}
                  />
                }
              />
              <Area
                type="linear"
                dataKey="difference"
                fill="var(--color-difference)"
                fillOpacity={0.14}
                stroke="none"
                legendType="none"
                tooltipType="none"
                isAnimationActive={false}
              />
              <Line
                type="linear"
                dataKey="baseline"
                stroke="var(--color-baseline)"
                strokeDasharray="5 5"
                strokeWidth={1.5}
                dot={false}
                isAnimationActive={false}
              />
              <Line
                type="linear"
                dataKey="actual"
                stroke="var(--color-actual)"
                strokeWidth={2.5}
                dot={false}
                isAnimationActive={false}
              />
            </ComposedChart>
          </ChartContainer>
        ) : (
          <div className="flex h-[235px] items-center justify-center text-sm text-muted-foreground">
            暂无可用的对比序列
          </div>
        )}
      </CardContent>
    </Card>
  );
}
