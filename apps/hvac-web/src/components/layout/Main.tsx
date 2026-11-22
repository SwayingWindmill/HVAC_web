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
        '@container/main flex w-full flex-col gap-[18px] px-7 pt-[22px] pb-6',
        fixed && 'min-h-0 flex-1 @4xl/content:h-[max(calc(100svh-var(--header-height)),680px)] @4xl/content:flex-none @4xl/content:overflow-hidden',
        !fluid && 'mx-auto max-w-[1600px]',
        'print:max-w-none print:p-0',
        className,
      )}
      {...props}
    />
  );
}
