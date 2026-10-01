import { useId, type ComponentProps, type ReactNode } from 'react';

import { cn } from '@/lib/utils';

export interface DataTableBlockProps extends Omit<ComponentProps<'section'>, 'title'> {
  readonly title?: ReactNode;
  readonly description?: ReactNode;
  readonly actions?: ReactNode;
  readonly controls?: ReactNode;
  readonly titleId?: string;
  readonly children: ReactNode;
}

export function DataTableBlock({
  title,
  description,
  actions,
  controls,
  titleId,
  children,
  className,
  ...props
}: DataTableBlockProps) {
  const generatedTitleId = useId();
  const resolvedTitleId = title ? (titleId ?? generatedTitleId) : undefined;
  const hasHeader = Boolean(title || description || actions);

  return (
    <section
      aria-labelledby={resolvedTitleId}
      className={cn('min-w-0 space-y-3', className)}
      {...props}
    >
      {hasHeader ? (
        <div className="flex flex-col gap-2 sm:flex-row sm:items-end sm:justify-between">
          <div className="min-w-0">
            {title ? (
              <h2
                id={resolvedTitleId}
                className="text-base font-semibold tracking-tight text-foreground"
              >
                {title}
              </h2>
            ) : null}
            {description ? (
              <div className={cn('text-sm text-muted-foreground', title && 'mt-0.5')}>
                {description}
              </div>
            ) : null}
          </div>
          {actions ? <div className="shrink-0">{actions}</div> : null}
        </div>
      ) : null}

      {controls}
      {children}
    </section>
  );
}
