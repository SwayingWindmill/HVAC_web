import type { ReactNode } from 'react';
import {
  AlertOctagon,
  AlertTriangle,
  CheckCircle2,
  Circle,
  Sparkles,
  type LucideIcon,
} from 'lucide-react';
import { cn } from '@/lib/utils';

export type StatusTone =
  | 'success' // e.g. 运行中, 已完成, 正常, 已放行
  | 'warning' // e.g. 需关注, 临界, 待复核
  | 'destructive' // e.g. 故障, 越权拦截, 离线, 严重
  | 'in-progress' // e.g. 处理中, 仿真中, 调试中
  | 'info' // alias to in-progress
  | 'neutral'; // e.g. 待机, 待处理, 草稿, 计划

interface StatusPillBadgeProps {
  readonly children?: ReactNode;
  readonly label?: ReactNode;
  readonly tone?: StatusTone;
  readonly icon?: LucideIcon | null;
  readonly pulse?: boolean;
  readonly className?: string;
}

const TONE_STYLES: Record<StatusTone, { readonly border: string; readonly dot: string; readonly icon: LucideIcon }> = {
  success: {
    border: 'border-emerald-200 bg-emerald-50/80 text-emerald-700 dark:bg-emerald-950/40 dark:border-emerald-800 dark:text-emerald-400',
    dot: 'bg-emerald-600 dark:bg-emerald-400',
    icon: CheckCircle2,
  },
  warning: {
    border: 'border-amber-200 bg-amber-50/80 text-amber-700 dark:bg-amber-950/40 dark:border-amber-800 dark:text-amber-400',
    dot: 'bg-amber-500',
    icon: AlertTriangle,
  },
  destructive: {
    border: 'border-rose-200 bg-rose-50/80 text-rose-700 dark:bg-rose-950/40 dark:border-rose-800 dark:text-rose-400',
    dot: 'bg-rose-600 dark:bg-rose-400',
    icon: AlertOctagon,
  },
  'in-progress': {
    border: 'border-sky-200 bg-sky-50/80 text-sky-700 dark:bg-sky-950/40 dark:border-sky-800 dark:text-sky-400',
    dot: 'bg-sky-500',
    icon: Sparkles,
  },
  info: {
    border: 'border-sky-200 bg-sky-50/80 text-sky-700 dark:bg-sky-950/40 dark:border-sky-800 dark:text-sky-400',
    dot: 'bg-sky-500',
    icon: Sparkles,
  },
  neutral: {
    border: 'border-border/80 bg-muted/30 text-muted-foreground',
    dot: 'bg-muted-foreground/60',
    icon: Circle,
  },
};

/**
 * Modern status pill badge matching shadcn/ui blocks table designs.
 * Features an elegant pill contour, soft semantic background, and subtle icon/indicator.
 */
export function StatusPillBadge({
  children,
  label,
  tone = 'neutral',
  icon: PropIcon,
  pulse = false,
  className,
}: StatusPillBadgeProps) {
  const style = TONE_STYLES[tone] ?? TONE_STYLES.neutral;
  const Icon = PropIcon !== undefined ? PropIcon : style.icon;

  return (
    <span
      className={cn(
        'inline-flex items-center gap-1.5 rounded-full border px-2.5 py-0.5 text-xs font-medium tracking-tight select-none transition-colors',
        style.border,
        className,
      )}
    >
      {pulse ? (
        <span className="relative flex size-2 shrink-0">
          <span className={cn('absolute inline-flex size-full animate-ping rounded-full opacity-75', style.dot)} />
          <span className={cn('relative inline-flex size-2 rounded-full', style.dot)} />
        </span>
      ) : Icon ? (
        <Icon className="size-3 shrink-0" aria-hidden="true" />
      ) : (
        <span className={cn('size-1.5 shrink-0 rounded-full', style.dot)} aria-hidden="true" />
      )}
      <span>{children ?? label}</span>
    </span>
  );
}
