import { Info } from 'lucide-react';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { cn } from '@/lib/utils';

/**
 * An ⓘ beside a label that carries the explanation (formula, scope, caveat) on hover or focus,
 * so pages print facts rather than paragraphs of grey text.
 */
export function InfoTip({ children, className }: { readonly children: React.ReactNode; readonly className?: string }) {
  const label = typeof children === 'string' ? children : undefined;
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <button
          type="button"
          aria-label={label ?? '说明'}
          className={cn('inline-flex shrink-0 cursor-help rounded-full text-subtle-foreground outline-none hover:text-muted-foreground focus-visible:ring-3 focus-visible:ring-ring-soft', className)}
        >
          <Info className="size-3.5" aria-hidden="true" />
        </button>
      </TooltipTrigger>
      <TooltipContent className="max-w-64 leading-5">{children}</TooltipContent>
    </Tooltip>
  );
}
