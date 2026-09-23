import type { ReactNode } from 'react';
import { cn } from '@/lib/utils';

interface PageIntroProps {
  readonly context?: ReactNode;
  readonly title: string;
  readonly description?: ReactNode;
  readonly meta?: ReactNode;
  readonly actions?: ReactNode;
  readonly className?: string;
}

export function PageIntro({ context, title, description, meta, actions, className }: PageIntroProps) {
  return (
    <header className={cn('flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between', className)}>
      <div className="min-w-0">
        <h1 className="text-2xl font-semibold tracking-tight text-foreground">{title}</h1>
        {description ? <div className="mt-1 max-w-3xl text-sm leading-6 text-muted-foreground">{description}</div> : null}
        {context || meta ? (
          <div className="mt-1.5 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground">
            {context ? <span>{context}</span> : null}
            {meta}
          </div>
        ) : null}
      </div>
      {actions ? <div className="flex shrink-0 flex-wrap items-center gap-2">{actions}</div> : null}
    </header>
  );
}
