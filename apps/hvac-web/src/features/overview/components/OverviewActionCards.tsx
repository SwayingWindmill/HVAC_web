import { ArrowUpRight, CircleAlert, ClipboardList } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardHeader,
  CardTitle,
  CardAction,
  CardContent,
} from "@/components/ui/card";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { cn } from "@/lib/utils";
import type {
  OverviewDashboardData,
  OverviewOpportunity,
} from "../api/overview-types";
export function OpportunitySummaryCard({
  data,
  onSelect,
  onNavigate,
}: {
  readonly data: OverviewDashboardData;
  readonly onSelect: (item: OverviewOpportunity) => void;
  readonly onNavigate: (path: string) => void;
}) {
  return (
    <Card className="h-full rounded-xl gap-3">
      <CardHeader>
        <CardTitle className="text-base">下一步值得优化什么</CardTitle>
        <CardAction>
          <Button
            variant="ghost"
            size="sm"
            onClick={() => onNavigate("/optimization/opportunities")}
          >
            全部机会
            <ArrowUpRight />
          </Button>
        </CardAction>
      </CardHeader>
      <CardContent className="px-0">
        {data.opportunities.length ? (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="pl-4 text-xs">机会与对象</TableHead>
                <TableHead className="text-right text-xs">估算潜力</TableHead>
                <TableHead className="pr-4 text-right text-xs">
                  下一步
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {data.opportunities.map((item) => (
                <TableRow key={item.title}>
                  <TableCell className="pl-4 py-3">
                    <p className="text-sm font-medium">{item.title}</p>
                    <p className="mt-1 text-xs text-muted-foreground">
                      {item.object}
                    </p>
                  </TableCell>
                  <TableCell className="text-right whitespace-nowrap font-medium tabular-nums">
                    {item.potential}
                  </TableCell>
                  <TableCell className="pr-4 text-right">
                    <Button
                      variant="ghost"
                      size="sm"
                      onClick={() => onSelect(item)}
                    >
                      {item.action}
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        ) : (
          <p className="px-4 py-8 text-sm text-muted-foreground">
            暂无可评估的节能机会
          </p>
        )}
      </CardContent>
    </Card>
  );
}
export function AttentionSummaryCard({
  data,
  onNavigate,
}: {
  readonly data: OverviewDashboardData;
  readonly onNavigate: (path: string) => void;
}) {
  return (
    <Card className="h-full rounded-xl gap-3">
      <CardHeader>
        <CardTitle className="text-base">需要处理的事项</CardTitle>
        <CardAction>
          <Button
            variant="ghost"
            size="sm"
            onClick={() => onNavigate("/operations/work-center")}
          >
            工作中心
            <ArrowUpRight />
          </Button>
        </CardAction>
      </CardHeader>
      <CardContent className="flex flex-1 flex-col divide-y">
        {data.attention.length ? (
          data.attention.map((item) => (
            <div
              key={item.title}
              className="flex flex-1 items-center gap-3 py-4"
            >
              {item.severity === "info" ? (
                <ClipboardList className="mt-0.5 size-4 text-muted-foreground" />
              ) : (
                <CircleAlert
                  className={cn(
                    "mt-0.5 size-4",
                    item.severity === "risk"
                      ? "text-destructive"
                      : "text-amber-600",
                  )}
                />
              )}
              <div className="flex-1">
                <p className="text-sm font-medium">{item.title}</p>
                <p className="mt-1.5 text-xs text-muted-foreground">
                  {item.location}
                </p>
              </div>
              <Button
                variant="outline"
                size="sm"
                onClick={() =>
                  onNavigate(
                    item.severity === "info"
                      ? "/operations/work-center"
                      : "/operations/alarms",
                  )
                }
              >
                {item.action}
              </Button>
            </div>
          ))
        ) : (
          <p className="py-8 text-sm text-muted-foreground">
            当前没有已记录的待处理事项
          </p>
        )}
      </CardContent>
    </Card>
  );
}
