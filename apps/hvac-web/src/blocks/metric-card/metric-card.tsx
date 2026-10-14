import { cn } from '@/lib/utils';

/** A figure with its unit; an absent figure says so instead of showing zero, a stale one is dimmed. */
export function MetricValue({ value, unit, stale = false }: { readonly value: string | null; readonly unit?: string; readonly stale?: boolean }) {
  if (value === null) return <span className="text-lg text-muted-foreground">暂无数据</span>;
  return (
    <span className={cn(stale && 'text-muted-foreground')} title={stale ? '数据过期或质量降级' : undefined}>
      {value}
      {unit ? <span className="ml-1 text-sm font-normal tracking-normal text-muted-foreground">{unit}</span> : null}
    </span>
  );
}
