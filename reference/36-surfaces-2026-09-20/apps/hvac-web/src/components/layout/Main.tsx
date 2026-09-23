import type { ComponentProps } from 'react';
import { cn } from '@/lib/utils';

type MainProps = ComponentProps<'div'> & {
  readonly fixed?: boolean;
  readonly fluid?: boolean;
};

export function Main({ fixed = false, fluid = false, className, ...props }: MainProps) {
  return (
    <div
      data-layout={fixed ? 'fixed' : 'auto'}
      className={cn(
        'w-full px-4 py-6 md:px-6',
        fixed && 'flex min-h-0 flex-1 flex-col overflow-hidden',
        !fluid && 'mx-auto max-w-[1600px]',
        className,
      )}
      {...props}
    />
  );
}
