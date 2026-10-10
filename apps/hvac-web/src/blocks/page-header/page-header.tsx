import type { ReactNode } from 'react';

import { Badge } from '@/components/ui/badge';
import { cn } from '@/lib/utils';

interface PageHeaderProps {
  readonly title: ReactNode;
  readonly description?: ReactNode;
  /** Small facts set beside the title, such as a live status or a count. */
  readonly meta?: ReactNode;
  readonly actions?: ReactNode;
  readonly className?: string;
}

/** The one page title of a route Surface, with its description and local actions. */
export function PageHeader({ title, description, meta, actions, className }: PageHeaderProps) {
  return (
    <header className={cn('flex flex-wrap items-start justify-between gap-x-6 gap-y-3', className)}>
      <div className="min-w-0 space-y-1">
        <div className="flex flex-wrap items-center gap-2.5">
          <h1 className="text-xl font-semibold tracking-tight text-foreground sm:text-2xl">{title}</h1>
          {__HVAC_WEB_FRONTEND_REVIEW__ ? <Badge variant="outline">评审构建</Badge> : null}
          {meta}
        </div>
        {description ? <p className="text-sm text-muted-foreground">{description}</p> : null}
      </div>
      {actions ? <div className="flex flex-wrap items-center gap-2">{actions}</div> : null}
    </header>
  );
}
