import type { ReactNode } from 'react';
import { Badge } from '@/components/ui/badge';
import { cn } from '@/lib/utils';

export type StatusTone =
  | 'success'
  | 'warning'
  | 'destructive'
  | 'in-progress'
  | 'info'
  | 'neutral'
  | 'running'
  | 'standby'
  | 'alarm'
  | 'offline';

export interface StatusBadgeProps {
  readonly children?: ReactNode;
  readonly label?: ReactNode;
  readonly tone?: StatusTone;
  readonly pulse?: boolean;
  readonly className?: string;
}

const toneMap: Readonly<Record<StatusTone, { badge: string; dot: string; defaultLabel: string }>> = {
  success: {
    badge: 'border-emerald-200 bg-emerald-50/80 text-emerald-700 dark:border-emerald-800 dark:bg-emerald-950/40 dark:text-emerald-400',
    dot: 'bg-emerald-600 dark:bg-emerald-400',
    defaultLabel: '正常',
  },
  running: {
    badge: 'border-emerald-200 bg-emerald-50/80 text-emerald-700 dark:border-emerald-800 dark:bg-emerald-950/40 dark:text-emerald-400',
    dot: 'bg-emerald-600 dark:bg-emerald-400',
    defaultLabel: '运行中',
  },
  warning: {
    badge: 'border-amber-200 bg-amber-50/80 text-amber-700 dark:border-amber-800 dark:bg-amber-950/40 dark:text-amber-400',
    dot: 'bg-amber-500',
    defaultLabel: '警告',
  },
  standby: {
    badge: 'border-amber-200 bg-amber-50/80 text-amber-700 dark:border-amber-800 dark:bg-amber-950/40 dark:text-amber-400',
    dot: 'bg-amber-500',
    defaultLabel: '待机',
  },
  destructive: {
    badge: 'border-destructive/25 bg-destructive/10 text-destructive dark:border-destructive/40 dark:bg-destructive/15',
    dot: 'bg-destructive',
    defaultLabel: '故障',
  },
  alarm: {
    badge: 'border-destructive/25 bg-destructive/10 text-destructive dark:border-destructive/40 dark:bg-destructive/15',
    dot: 'bg-destructive',
    defaultLabel: '告警中',
  },
  'in-progress': {
    badge: 'border-sky-200 bg-sky-50/80 text-sky-700 dark:border-sky-800 dark:bg-sky-950/40 dark:text-sky-400',
    dot: 'bg-sky-500',
    defaultLabel: '处理中',
  },
  info: {
    badge: 'border-sky-200 bg-sky-50/80 text-sky-700 dark:border-sky-800 dark:bg-sky-950/40 dark:text-sky-400',
    dot: 'bg-sky-500',
    defaultLabel: '信息',
  },
  neutral: {
    badge: 'border-border/80 bg-muted/30 text-muted-foreground',
    dot: 'bg-muted-foreground/60',
    defaultLabel: '未定义',
  },
  offline: {
    badge: 'border-border/80 bg-muted/30 text-muted-foreground',
    dot: 'bg-muted-foreground/40',
    defaultLabel: '离线',
  },
};

export function StatusBadge({
  children,
  label,
  tone = 'neutral',
  pulse = false,
  className,
}: StatusBadgeProps) {
  const config = toneMap[tone] ?? toneMap.neutral;
  const shouldPulse = pulse || tone === 'running' || tone === 'alarm';

  return (
    <Badge
      variant="outline"
      className={cn('gap-1.5 px-2.5 py-0.5 tracking-tight select-none font-medium text-xs', config.badge, className)}
    >
      {shouldPulse ? (
        <span className="relative flex size-2 shrink-0" aria-hidden="true">
          <span className={cn('absolute inline-flex size-full animate-ping rounded-full opacity-75', config.dot)} />
          <span className={cn('relative inline-flex size-2 rounded-full', config.dot)} />
        </span>
      ) : (
        <span className={cn('size-1.5 shrink-0 rounded-full', config.dot)} aria-hidden="true" />
      )}
      <span>{children ?? label ?? config.defaultLabel}</span>
    </Badge>
  );
}
