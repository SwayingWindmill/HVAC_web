import type { ReactNode } from "react";
import { Badge } from "@/components/ui/badge";
import { Alert, AlertTitle, AlertDescription } from "@/components/ui/alert";
import { Skeleton } from "@/components/ui/skeleton";
export const isWorkspaceExample =
  __HVAC_WEB_FRONTEND_REVIEW__ || import.meta.env.DEV;
export function WorkspaceHeader({
  title,
  actions,
}: {
  title: string;
  actions?: ReactNode;
}) {
  return (
    <header className="flex items-center justify-between gap-4">
      <div className="flex items-center gap-3">
        <h1 className="text-2xl font-semibold">{title}</h1>
        {isWorkspaceExample && <Badge variant="outline">示例数据</Badge>}
      </div>
      {actions}
    </header>
  );
}
export function MetricStrip({
  items,
}: {
  items: { label: string; value: ReactNode; unit?: string }[];
}) {
  return (
    <div className="flex divide-x rounded-xl border bg-card">
      {items.map((item) => (
        <div key={item.label} className="min-w-0 flex-1 px-5 py-4">
          <div className="text-sm">{item.label}</div>
          <div className="mt-2 flex items-baseline gap-2">
            <span className="text-3xl font-semibold tabular-nums">
              {item.value}
            </span>
            {item.unit && (
              <span className="text-sm text-muted-foreground">{item.unit}</span>
            )}
          </div>
        </div>
      ))}
    </div>
  );
}
export function WorkspaceQueryState({
  pending,
  error,
}: {
  pending: boolean;
  error: Error | null;
}) {
  return pending ? (
    <Skeleton className="h-96" />
  ) : error ? (
    <Alert variant="destructive">
      <AlertTitle>当前范围暂不可用</AlertTitle>
      <AlertDescription>{error.message}</AlertDescription>
    </Alert>
  ) : null;
}
