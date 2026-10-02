import { cn } from "@/lib/utils";
import {
  formatOverviewNumber as number,
  type OverviewDashboardData,
  type OverviewMetric,
} from "../api/overview-types";
const scaled = (metric: OverviewMetric, divisor: number): OverviewMetric =>
  metric.value === null ? metric : { ...metric, value: metric.value / divisor };
export function OverviewKpiGrid({
  data,
}: {
  readonly data: OverviewDashboardData;
}) {
  const metrics = [
    {
      label: "实际用电",
      figure: scaled(data.actualKWh, 1000),
      unit: "MWh",
      emphasis: false,
    },
    {
      label: "节省电量",
      figure: scaled(data.savingsKWh, 1000),
      unit: "MWh",
      emphasis: true,
    },
    {
      label: "节能率",
      figure: data.savingsRate,
      unit: "%",
      emphasis: true,
    },
    {
      label: "节约费用",
      figure: scaled(data.savingsCny, 10000),
      unit: "万元",
      emphasis: true,
    },
  ];
  return (
    <section
      aria-label="节能成果"
      className="grid grid-cols-4 divide-x rounded-xl border bg-card shadow-xs"
    >
      {metrics.map(({ label, figure, unit, emphasis }) => (
        <div key={label} className="space-y-3 px-5 py-5">
          <p className="flex items-center gap-2 text-sm">
            {label}
            {label === "节能率" && (
              <span className="rounded border px-1.5 py-0.5 text-[10px] font-normal">
                待验证
              </span>
            )}
          </p>
          <p className="flex items-baseline gap-1.5">
            {figure.absence === null ? (
              <>
                <span
                  data-testid={"metric-" + label}
                  className={cn(
                    "text-[30px] font-semibold tracking-tight tabular-nums leading-none",
                    emphasis &&
                      figure.value !== null &&
                      figure.value > 0 &&
                      "text-emerald-700 dark:text-emerald-400",
                    emphasis &&
                      figure.value !== null &&
                      figure.value < 0 &&
                      "text-destructive",
                  )}
                >
                  {number(figure.value)}
                </span>
                <span className="text-xs text-muted-foreground">{unit}</span>
              </>
            ) : (
              <span
                data-testid={"metric-" + label}
                className="text-lg font-medium leading-[30px] text-muted-foreground"
              >
                {figure.absence}
              </span>
            )}
          </p>
        </div>
      ))}
    </section>
  );
}
