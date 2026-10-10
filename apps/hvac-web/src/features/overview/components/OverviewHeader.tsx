import { ArrowDownToLine, RefreshCw } from "lucide-react";
import { Button } from "@/components/ui/button";
import { PageHeader } from "@/blocks/page-header";
import { OVERVIEW_PERIODS, type OverviewPeriod } from "../api/overview-types";
export function OverviewHeader({
  period,
  onPeriodChange,
  onExport,
  onRefresh,
  refreshing,
  hasData,
}: {
  readonly period: OverviewPeriod;
  readonly onPeriodChange: (period: OverviewPeriod) => void;
  readonly onExport: () => void;
  readonly onRefresh: () => void;
  readonly refreshing: boolean;
  readonly hasData: boolean;
}) {
  return (
    <PageHeader
      title="节能运营总览"
      actions={
        <div className="flex items-center gap-3">
          <div
            className="flex gap-1 rounded-lg bg-muted/70 p-1"
            aria-label="分析期间"
          >
            {(Object.keys(OVERVIEW_PERIODS) as OverviewPeriod[]).map((value) => (
              <Button
                key={value}
                size="sm"
                variant={value === period ? "outline" : "ghost"}
                aria-pressed={value === period}
                onClick={() => onPeriodChange(value)}
                className="h-8 px-3"
              >
                {OVERVIEW_PERIODS[value]}
              </Button>
            ))}
          </div>
          <Button
            size="sm"
            variant="outline"
            aria-label="刷新总览"
            disabled={refreshing}
            onClick={onRefresh}
          >
            <RefreshCw className={refreshing ? "animate-spin" : ""} />
          </Button>
          <Button
            size="sm"
            variant="outline"
            disabled={!hasData}
            onClick={onExport}
          >
            <ArrowDownToLine />
            导出摘要
          </Button>
        </div>
      }
    />
  );
}
