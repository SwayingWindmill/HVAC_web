import type { ComponentType, ReactNode } from 'react';
import { cn } from '@/lib/utils';

type Tone = 'neutral' | 'success' | 'warning' | 'danger' | 'primary';

const TONE: Record<Tone, string> = {
  neutral: 'border bg-card text-muted-foreground',
  success: 'bg-success-soft text-success-text',
  warning: 'bg-warning-soft text-warning-text',
  danger: 'bg-destructive-soft text-destructive-text',
  primary: 'bg-primary-soft text-primary',
};

/**
 * The one shape every empty, failed, forbidden or not-connected state takes (DESIGN.md, 通用状态):
 * what is missing, what to do next, and the way to it.
 */
export function StateCard({ icon: Icon, tone = 'neutral', title, description, children, className, ...props }: {
  readonly icon?: ComponentType<{ className?: string }>;
  readonly tone?: Tone;
  /** A heading element, so a route surface can pass its focusable page heading. */
  readonly title: ReactNode;
  readonly description?: ReactNode;
  /** Actions or quick links under the description. */
  readonly children?: ReactNode;
  readonly className?: string;
} & Omit<React.ComponentProps<'section'>, 'title'>) {
  return (
    <section className={cn('flex flex-col items-center gap-2 px-6 py-12 text-center', className)} {...props}>
      {Icon ? (
        <span className={cn('mb-1 flex size-10 items-center justify-center rounded-xl', TONE[tone])}>
          <Icon className="size-5" aria-hidden="true" />
        </span>
      ) : null}
      {title}
      {description ? <p className="max-w-lg text-sm text-muted-foreground">{description}</p> : null}
      {children ? <div className="mt-3 flex flex-wrap items-center justify-center gap-2">{children}</div> : null}
    </section>
  );
}
