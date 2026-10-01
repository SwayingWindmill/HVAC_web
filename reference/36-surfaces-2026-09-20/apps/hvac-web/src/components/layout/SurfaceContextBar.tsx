import type { ComponentProps, ReactNode } from 'react';
import { cn } from '@/lib/utils';

interface SurfaceContextBarProps extends ComponentProps<'div'> {
  readonly children: ReactNode;
}

interface SurfaceContextItemProps extends ComponentProps<'div'> {
  readonly emphasis?: boolean;
  readonly children: ReactNode;
}

export function SurfaceContextBar({ className, children, ...props }: SurfaceContextBarProps) {
  return (
    <div className={cn('flex flex-wrap items-center justify-end gap-2 text-sm', className)} {...props}>
      {children}
    </div>
  );
}

export function SurfaceContextItem({ className, emphasis = false, children, ...props }: SurfaceContextItemProps) {
  return (
    <div
      className={cn(
        'inline-flex h-9 items-center gap-2 rounded-md border bg-background px-3 shadow-xs [&>svg]:size-4 [&>svg]:shrink-0 [&>svg]:text-muted-foreground',
        emphasis ? 'font-medium' : 'text-muted-foreground',
        className,
      )}
      {...props}
    >
      {children}
    </div>
  );
}
