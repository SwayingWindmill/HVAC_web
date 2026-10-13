import type { ReactNode } from 'react';

import { Badge } from '@/components/ui/badge';
import { cn } from '@/lib/utils';

interface PageHeaderProps {
  readonly title: ReactNode;
  readonly description?: ReactNode;
  /** Small facts set beside the title, such as a live status or a count; use PageMeta. */
  readonly meta?: ReactNode;
  readonly actions?: ReactNode;
  readonly className?: string;
}

/** The one page title of a route Surface, with its description and local actions. */
export function PageHeader({ title, description, meta, actions, className }: PageHeaderProps) {
  return (
    <header className={cn('flex items-end justify-between gap-4', className)}>
      <div className="flex min-w-0 flex-col gap-1">
        <div className="flex items-center gap-2.5">
          <h1 className="text-[26px] leading-[30px] font-bold tracking-[-0.03em] text-foreground">{title}</h1>
          {__HVAC_WEB_FRONTEND_REVIEW__ ? <Badge variant="outline">评审构建</Badge> : null}
          {meta}
        </div>
        {description ? <p className="text-xs leading-[18px] text-muted-foreground">{description}</p> : null}
      </div>
      {actions ? <div className="flex shrink-0 items-center gap-2">{actions}</div> : null}
    </header>
  );
}

const DOT_TONE = {
  live: 'bg-success',
  idle: 'bg-subtle-foreground',
  warning: 'bg-warning',
} as const;

const CHIP_TONE = {
  live: 'border-transparent bg-success-soft text-success-text',
  idle: 'border-border text-muted-foreground',
  warning: 'border-transparent bg-warning-soft text-warning-text',
} as const;

/**
 * A quiet chip beside the page title: what the page measures, or whether it is live. A dot
 * marks a state; an icon marks a scope.
 */
export function PageMeta({ children, dot, icon, className }: {
  readonly children: ReactNode;
  readonly dot?: keyof typeof DOT_TONE;
  readonly icon?: ReactNode;
  readonly className?: string;
}) {
  return (
    <Badge variant="outline" className={cn('h-[22px] gap-1.5 rounded-full px-[9px] text-xs font-medium [&>svg]:size-3.5!', CHIP_TONE[dot ?? 'idle'], className)}>
      {dot ? <span className={cn('size-1.5 rounded-full', DOT_TONE[dot])} aria-hidden="true" /> : icon}
      {children}
    </Badge>
  );
}
