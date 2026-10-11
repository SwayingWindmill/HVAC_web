import type { ReactNode } from "react";

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
