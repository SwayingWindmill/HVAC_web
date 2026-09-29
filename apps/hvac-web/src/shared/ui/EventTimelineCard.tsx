import type { ReactNode } from 'react';
import { Circle } from 'lucide-react';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Skeleton } from '@/components/ui/skeleton';
import { cn } from '@/lib/utils';

export type EventTimelineTone = 'default' | 'success' | 'warning' | 'error' | 'info';

export interface EventTimelineItem {
  readonly key: string;
  readonly title: ReactNode;
  readonly description?: ReactNode;
  readonly time?: ReactNode;
  readonly tone?: EventTimelineTone;
  readonly icon?: ReactNode;
}

export interface EventTimelineCardProps {
  readonly title: ReactNode;
  readonly items: readonly EventTimelineItem[];
  readonly extra?: ReactNode;
  readonly loading?: boolean;
  readonly emptyDescription?: ReactNode;
  readonly className?: string;
}

function toneClass(tone: EventTimelineTone): string {
  switch (tone) {
    case 'success': return 'text-emerald-600 dark:text-emerald-400';
    case 'warning': return 'text-amber-600 dark:text-amber-400';
    case 'error': return 'text-destructive';
    case 'info': return 'text-blue-600 dark:text-blue-400';
    default: return 'text-muted-foreground';
  }
}

export function EventTimelineCard({
  title,
  items,
  extra,
  loading = false,
  emptyDescription = '暂无事件',
  className,
}: EventTimelineCardProps) {
  return (
    <Card className={cn('gap-0 py-0 shadow-none', className)}>
      <CardHeader className="flex-row items-center justify-between gap-3 border-b py-4">
        <CardTitle className="text-sm">{title}</CardTitle>
        {extra}
      </CardHeader>
      <CardContent className="p-4">
        {loading ? (
          <div className="space-y-3" aria-label="正在读取事件">
            {Array.from({ length: 4 }, (_, index) => <Skeleton key={index} className="h-10 w-full" />)}
          </div>
        ) : items.length ? (
          <ol className="space-y-0" aria-label="事件时间线">
            {items.map((item, index) => (
              <li key={item.key} className="relative grid grid-cols-[1rem_minmax(0,1fr)] gap-3 pb-4 last:pb-0">
                {index < items.length - 1 ? <span className="absolute left-[7px] top-4 h-[calc(100%-0.25rem)] w-px bg-border" aria-hidden="true" /> : null}
                <span className={cn('relative z-10 mt-1 flex size-4 items-center justify-center bg-card', toneClass(item.tone ?? 'default'))} aria-hidden="true">
                  {item.icon ?? <Circle className="size-2.5 fill-current" />}
                </span>
                <div className="min-w-0">
                  <div className="flex flex-wrap items-start justify-between gap-x-4 gap-y-1 text-sm">
                    <strong className="font-medium">{item.title}</strong>
                    {item.time != null ? <span className="text-xs tabular-nums text-muted-foreground">{item.time}</span> : null}
                  </div>
                  {item.description != null ? <div className="mt-1 text-xs leading-5 text-muted-foreground">{item.description}</div> : null}
                </div>
              </li>
            ))}
          </ol>
        ) : (
          <div className="rounded-md border border-dashed px-4 py-8 text-center text-sm text-muted-foreground">{emptyDescription}</div>
        )}
      </CardContent>
    </Card>
  );
}
