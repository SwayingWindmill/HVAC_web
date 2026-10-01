import { cn } from "@/lib/utils";
import {
  formatOverviewNumber as number,
  type OverviewDashboardData,
} from "../api/overview-types";
export function OverviewKpiGrid({
  data,
}: {
  readonly data: OverviewDashboardData;
}) {
  const metrics = [
    {
      label: "实际用电",
      value: data.actualKWh === null ? null : data.actualKWh / 1000,
      unit: "MWh",
      emphasis: false,
    },
    {
      label: "节省电量",
      value: data.savingsKWh === null ? null : data.savingsKWh / 1000,
      unit: "MWh",
      emphasis: true,
    },
    {
      label: "节能率",
      value: data.savingsRate,
      unit: "%",
      emphasis: true,
    },
    {
      label: "节约费用",
      value: data.savingsCny === null ? null : data.savingsCny / 10000,
      unit: "万元",
      emphasis: true,
    },
  ];
  return (
    <section
      aria-label="节能成果"
      className="grid grid-cols-4 divide-x rounded-xl border bg-card shadow-xs"
    >
      {metrics.map((metric) => (
        <div key={metric.label} className="space-y-3 px-5 py-5">
          <p className="flex items-center gap-2 text-sm">
            {metric.label}
            {metric.label === "节能率" && (
              <span className="rounded border px-1.5 py-0.5 text-[10px] font-normal">
                待验证
              </span>
            )}
          </p>
          <p className="flex items-baseline gap-1.5">
            <span
              data-testid={"metric-" + metric.label}
              className={cn(
                "text-[30px] font-semibold tracking-tight tabular-nums leading-none",
                metric.emphasis &&
                  metric.value !== null &&
                  metric.value > 0 &&
                  "text-emerald-700 dark:text-emerald-400",
                metric.emphasis &&
                  metric.value !== null &&
                  metric.value < 0 &&
                  "text-destructive",
              )}
            >
              {number(metric.value)}
            </span>
            <span className="text-xs text-muted-foreground">{metric.unit}</span>
          </p>
        </div>
      ))}
    </section>
  );
}
