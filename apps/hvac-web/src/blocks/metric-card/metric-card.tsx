import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { cn } from '@/lib/utils';

/** A figure with its unit; an absent figure says so instead of showing zero, a stale one is dimmed. */
export function MetricValue({ value, unit, stale = false }: { readonly value: string | null; readonly unit?: string; readonly stale?: boolean }) {
  if (value === null) return <span className="text-lg text-muted-foreground">—</span>;
  const content = (
    <span
      tabIndex={stale ? 0 : undefined}
      className={cn(stale && 'text-muted-foreground cursor-help rounded-xs outline-none focus-visible:ring-3 focus-visible:ring-ring-soft')}
    >
      {value}
      {unit ? <span className="ml-1 text-xs font-medium tracking-normal text-subtle-foreground">{unit}</span> : null}
    </span>
  );
  if (!stale) return content;
  return (
    <Tooltip>
      <TooltipTrigger asChild>{content}</TooltipTrigger>
      <TooltipContent>读数过期或不可靠</TooltipContent>
    </Tooltip>
  );
}
