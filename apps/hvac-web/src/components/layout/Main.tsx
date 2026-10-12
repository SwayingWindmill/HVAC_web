import type { ComponentProps } from 'react';
import { cn } from '@/lib/utils';

type MainProps = ComponentProps<'div'> & {
  readonly fixed?: boolean;
  readonly fluid?: boolean;
};

/** A route's page: one padding and one vertical rhythm for every Surface. */
export function Main({ fixed = false, fluid = false, className, ...props }: MainProps) {
  return (
    <div
      data-layout={fixed ? 'fixed' : 'auto'}
      className={cn(
        '@container/main flex w-full flex-col gap-6 px-6 pt-6 pb-8',
        fixed && 'min-h-0 flex-1 overflow-hidden',
        !fluid && 'mx-auto max-w-[1600px]',
        className,
      )}
      {...props}
    />
  );
}
