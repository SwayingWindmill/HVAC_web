import { ArrowUpRight, ShieldCheck, TriangleAlert } from "lucide-react";
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
} from "../api/overview-types";
export function ComfortGuardrailCard({
  data,
  onDetail,
}: {
  readonly data: OverviewDashboardData;
  readonly onDetail: () => void;
}) {
  const comfort = data.comfort;
  return (
    <Card className="h-full rounded-xl gap-4">
      <CardHeader>
        <CardTitle className="text-base">节能的舒适度约束</CardTitle>
        <CardAction>
          <Button variant="ghost" size="sm" onClick={onDetail}>
            查看约束
            <ArrowUpRight />
          </Button>
        </CardAction>
      </CardHeader>
      <CardContent className="flex flex-1 flex-col">
        <div className="mb-4 flex items-center gap-5">
          <ShieldCheck className="size-8 text-muted-foreground" />
          <div>
            <p className="text-[32px] font-semibold tracking-tight leading-none tabular-nums">
              {number(comfort.rate)}
              <span className="ml-1 text-base font-normal text-muted-foreground">
                %
              </span>
            </p>
            <p className="mt-2 text-xs text-muted-foreground">
              {data.mode === "example"
                ? "占用时段舒适合规率"
                : "平台舒适合规率 · 时段口径未提供"}
            </p>
          </div>
          <div className="ml-auto text-right">
            <p className="text-sm font-medium tabular-nums">
              {number(comfort.coverage)}%
            </p>
            <p className="mt-1 text-xs text-muted-foreground">数据覆盖率</p>
          </div>
        </div>
        <div className="mt-auto space-y-2">
          {comfort.exceptions.length ? (
            comfort.exceptions.map((item) => (
              <div
                key={item.location}
                className="flex w-full items-start gap-2.5 rounded-lg border border-amber-500/20 bg-amber-500/5 px-3 py-2.5"
              >
                <TriangleAlert className="mt-0.5 size-4 shrink-0 text-amber-600" />
                <span>
                  <span className="block text-sm font-medium">
                    {item.location}
                  </span>
                  <span className="mt-0.5 block text-xs text-muted-foreground">
                    {item.detail}
                  </span>
                </span>
              </div>
            ))
          ) : (
            <p className="py-3 text-sm text-muted-foreground">
              {comfort.coverage === null
                ? "覆盖率与超限区域尚未提供，不能据此判断全部合规。"
                : "当前没有已记录的超限区域。"}
            </p>
          )}
        </div>
      </CardContent>
    </Card>
  );
}
