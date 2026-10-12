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
    badge: 'bg-emerald-500/10 text-emerald-700 dark:bg-emerald-500/15 dark:text-emerald-400',
    dot: 'bg-emerald-600 dark:bg-emerald-400',
    defaultLabel: '正常',
  },
  running: {
    badge: 'bg-emerald-500/10 text-emerald-700 dark:bg-emerald-500/15 dark:text-emerald-400',
    dot: 'bg-emerald-600 dark:bg-emerald-400',
    defaultLabel: '运行中',
  },
  warning: {
    badge: 'bg-amber-500/10 text-amber-700 dark:bg-amber-500/15 dark:text-amber-400',
    dot: 'bg-amber-500',
    defaultLabel: '警告',
  },
  standby: {
    badge: 'bg-amber-500/10 text-amber-700 dark:bg-amber-500/15 dark:text-amber-400',
    dot: 'bg-amber-500',
    defaultLabel: '待机',
  },
  destructive: {
    badge: 'bg-destructive/10 text-destructive dark:bg-destructive/15',
    dot: 'bg-destructive',
    defaultLabel: '故障',
  },
  alarm: {
    badge: 'bg-destructive/10 text-destructive dark:bg-destructive/15',
    dot: 'bg-destructive',
    defaultLabel: '告警中',
  },
  'in-progress': {
    badge: 'bg-sky-500/10 text-sky-700 dark:bg-sky-500/15 dark:text-sky-400',
    dot: 'bg-sky-500',
    defaultLabel: '处理中',
  },
  info: {
    badge: 'bg-sky-500/10 text-sky-700 dark:bg-sky-500/15 dark:text-sky-400',
    dot: 'bg-sky-500',
    defaultLabel: '信息',
  },
  neutral: {
    badge: 'bg-muted text-muted-foreground',
    dot: 'bg-muted-foreground/60',
    defaultLabel: '未定义',
  },
  offline: {
    badge: 'bg-muted text-muted-foreground',
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
  const shouldPulse = pulse || tone === 'alarm';

  return (
    <Badge
      variant="outline"
      className={cn('h-5.5 gap-1.5 rounded-md border-transparent px-1.5 text-xs font-medium select-none', config.badge, className)}
    >
      {shouldPulse ? (
        <span className="relative flex size-1.5 shrink-0" aria-hidden="true">
          <span className={cn('absolute inline-flex size-full animate-ping rounded-full opacity-75', config.dot)} />
          <span className={cn('relative inline-flex size-1.5 rounded-full', config.dot)} />
        </span>
      ) : (
        <span className={cn('size-1.5 shrink-0 rounded-full', config.dot)} aria-hidden="true" />
      )}
      <span>{children ?? label ?? config.defaultLabel}</span>
    </Badge>
  );
}
