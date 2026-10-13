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

// DS 1.0 soft tints: a tinted label with a still dot; the alarm dot alone pulses.
const SUCCESS = { badge: 'bg-success-soft text-success-text', dot: 'bg-success' };
const WARNING = { badge: 'bg-warning-soft text-warning-text', dot: 'bg-warning' };
const DANGER = { badge: 'bg-destructive-soft text-destructive-text', dot: 'bg-destructive' };
const INFO = { badge: 'bg-primary-soft text-primary', dot: 'bg-primary' };
const NEUTRAL = { badge: 'bg-secondary text-secondary-foreground', dot: 'bg-subtle-foreground' };

const toneMap: Readonly<Record<StatusTone, { badge: string; dot: string; defaultLabel: string }>> = {
  success: { ...SUCCESS, defaultLabel: '正常' },
  running: { ...SUCCESS, defaultLabel: '运行中' },
  warning: { ...WARNING, defaultLabel: '警告' },
  standby: { ...WARNING, defaultLabel: '待机' },
  destructive: { ...DANGER, defaultLabel: '故障' },
  alarm: { ...DANGER, defaultLabel: '告警中' },
  'in-progress': { ...INFO, defaultLabel: '处理中' },
  info: { ...INFO, defaultLabel: '信息' },
  neutral: { ...NEUTRAL, defaultLabel: '未定义' },
  offline: { badge: 'bg-secondary text-muted-foreground', dot: 'border border-dashed border-muted-foreground bg-transparent', defaultLabel: '离线' },
};

/** The dot alone, for places that print the label themselves. */
export function statusDotClass(tone: StatusTone): string {
  return (toneMap[tone] ?? toneMap.neutral).dot;
}

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
      className={cn('h-5 gap-1.5 rounded-md border-transparent px-[7px] text-[11px] font-medium select-none', config.badge, className)}
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
