import type { ComponentProps, ReactNode } from 'react';
import { Link, type LinkProps } from '@tanstack/react-router';
import { ChevronRight } from 'lucide-react';
import { cn } from '@/lib/utils';

/** A white surface on the page canvas: the one container every page section uses. */
export function Panel({ className, ...props }: ComponentProps<'section'>) {
  return (
    <section
      data-slot="panel"
      className={cn('flex min-w-0 flex-col overflow-hidden rounded-xl border bg-card text-card-foreground shadow-xs', className)}
      {...props}
    />
  );
}

/**
 * The 48px title bar of a panel: what the panel is, a short caption (scope or time, never an
 * explanation), and at most one way onward on the right.
 */
export function PanelHeader({ title, caption, badge, action, className }: {
  readonly title: ReactNode;
  readonly caption?: ReactNode;
  readonly badge?: ReactNode;
  readonly action?: ReactNode;
  readonly className?: string;
}) {
  return (
    <header className={cn('flex h-12 shrink-0 items-center gap-2 border-b border-divider px-4', className)}>
      <h2 className="text-title font-semibold whitespace-nowrap">{title}</h2>
      {badge}
      {caption ? <span className="min-w-0 truncate text-xs text-muted-foreground">{caption}</span> : null}
      {action ? <div className="ml-auto flex shrink-0 items-center gap-2">{action}</div> : null}
    </header>
  );
}

/** The panel's way onward: a quiet "文字 ›" link instead of a button. */
export function PanelLink({ children, className, ...props }: LinkProps & { readonly children: ReactNode; readonly className?: string }) {
  return (
    <Link
      {...props}
      className={cn('inline-flex items-center gap-0.5 rounded-sm text-xs font-medium whitespace-nowrap text-primary outline-none hover:underline focus-visible:ring-3 focus-visible:ring-ring-soft', className)}
    >
      {children}
      <ChevronRight className="size-3.5" aria-hidden="true" />
    </Link>
  );
}

/** A count beside a panel title, e.g. how many items wait. */
export function PanelCount({ count, tone = 'neutral' }: { readonly count: number; readonly tone?: 'neutral' | 'urgent' }) {
  return (
    <span className={cn(
      'rounded-full px-1.5 font-mono text-[11px] leading-[18px] font-semibold',
      tone === 'urgent' ? 'bg-destructive-soft text-destructive-text' : 'bg-secondary text-secondary-foreground',
    )}
    >
      {count}
    </span>
  );
}
