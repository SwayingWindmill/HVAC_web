import { useState } from "react";
import { CheckCheck, ClipboardCheck, UserRound, Activity } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Alert, AlertTitle, AlertDescription } from "@/components/ui/alert";
import {
  Select,
  SelectTrigger,
  SelectValue,
  SelectContent,
  SelectItem,
} from "@/components/ui/select";
import type { WorkOrderItem } from "@/features/work-orders/api/work-order-types";
import type { RecoveryResult } from "../api/operations-simulation";
import { hasRecoveryCriteria } from "../api/operations-simulation";
import type { useOperationsAction } from "../api/operations-flow";
export function WorkProcessing({
  work,
  recovery,
  action,
}: {
  work: WorkOrderItem;
  recovery?: RecoveryResult;
  action: ReturnType<typeof useOperationsAction>;
}) {
  const [condition, setCondition] = useState<"stable" | "persistent">(
    "persistent",
  );
  return (
    <section className="space-y-4 rounded-lg border p-4">
      <h3 className="flex items-center gap-2 text-sm font-semibold">
        <ClipboardCheck className="size-4" aria-hidden="true" />
        处理与恢复验证
      </h3>
      <div className="flex flex-wrap gap-2">
        {work.status === "OPEN" && (
          <Button
            size="sm"
            disabled={action.isPending}
            onClick={() =>
              action.mutate({
                type: "assign",
                id: work.id,
                at: new Date().toISOString(),
              })
            }
          >
            <UserRound aria-hidden="true" />
            {work.assigneeName ? "开始处理" : "领取工单"}
          </Button>
        )}
        {work.status === "IN_PROGRESS" && (
          <Button
            size="sm"
            disabled={
              action.isPending || work.checklist.some((x) => !x.completed)
            }
            onClick={() =>
              action.mutate({
                type: "complete",
                id: work.id,
                at: new Date().toISOString(),
              })
            }
          >
            <CheckCheck aria-hidden="true" />
            提交作业完成
          </Button>
        )}
        <Badge variant="outline">
          {work.status === "COMPLETED"
            ? recovery?.status === "PASSED"
              ? "恢复已验证"
              : recovery?.status === "FAILED"
                ? "恢复验证失败"
                : "等待恢复验证"
            : "作业尚未完成"}
        </Badge>
      </div>
      {work.status === "COMPLETED" &&
        hasRecoveryCriteria(work.id) &&
        recovery?.status !== "PASSED" && (
          <div className="flex items-center gap-3">
            <Select
              value={condition}
              onValueChange={(value) => setCondition(value as typeof condition)}
            >
              <SelectTrigger aria-label="模拟回放工况" className="w-48">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="persistent">异常持续</SelectItem>
                <SelectItem value="stable">维修后稳定运行</SelectItem>
              </SelectContent>
            </Select>
            <Button
              variant="outline"
              size="sm"
              disabled={action.isPending}
              onClick={() =>
                action.mutate({
                  type: "verify",
                  id: work.id,
                  condition,
                  at: new Date().toISOString(),
                })
              }
            >
              <Activity aria-hidden="true" />
              运行恢复验证
            </Button>
          </div>
        )}
      {work.status === "COMPLETED" && !hasRecoveryCriteria(work.id) && (
        <p className="text-sm">尚未配置恢复判据，需要工程师补充测量方案。</p>
      )}
      {recovery && (
        <div className="space-y-3 border-t pt-4">
          <p className="text-sm">
            观测时间：
            {new Date(recovery.at).toLocaleString("zh-CN", {
              timeZone: "Asia/Shanghai",
            })}
          </p>
          {recovery.checks.map((check) => (
            <div
              key={check.pointCode}
              className="flex items-center justify-between gap-4 text-sm"
            >
              <span>
                {check.label}
                <span className="ml-2 text-xs text-muted-foreground">
                  {check.min} — {check.max} {check.unit}
                </span>
              </span>
              <strong
                className={
                  check.actual < check.min || check.actual > check.max
                    ? "text-destructive"
                    : ""
                }
              >
                {check.actual} {check.unit}
              </strong>
            </div>
          ))}
        </div>
      )}
      {action.error && (
        <Alert variant="destructive">
          <AlertTitle>处理未完成</AlertTitle>
          <AlertDescription>{action.error.message}</AlertDescription>
        </Alert>
      )}
    </section>
  );
}
