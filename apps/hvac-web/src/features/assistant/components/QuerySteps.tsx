import type { ReactNode } from 'react';
import { Check, ChevronDown, LoaderCircle, X } from 'lucide-react';
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible';
import { toolPresentation } from '../tool-labels';

/** The data reads of one answer, folded to a single line unless the operator opens them. */
export function QuerySteps({ count, running, children }: { readonly count: number; readonly running: boolean; readonly children: ReactNode }) {
  return (
    <Collapsible className="rounded-lg border bg-muted/50 px-3 py-2">
      <CollapsibleTrigger className="group flex w-full items-center gap-1.5 text-xs text-muted-foreground outline-none focus-visible:ring-3 focus-visible:ring-ring-soft">
        {running ? <span className="size-1.5 animate-pulse rounded-full bg-primary motion-reduce:animate-none" aria-hidden="true" /> : null}
        <span className="font-medium text-foreground">{running ? '正在查询数据' : `查询了 ${count} 项数据`}</span>
        <span className="flex-1" />
        <ChevronDown className="size-3.5 transition-transform group-data-[state=open]:rotate-180" aria-hidden="true" />
      </CollapsibleTrigger>
      <CollapsibleContent>
        <ol className="m-0 mt-2 flex list-none flex-col gap-2 p-0">{children}</ol>
      </CollapsibleContent>
    </Collapsible>
  );
}

export function QueryStep({ toolName, result, isError }: { readonly toolName: string; readonly result?: unknown; readonly isError?: boolean }) {
  const done = result !== undefined;
  return (
    <li className="flex items-center gap-2 text-xs">
      {!done ? (
        <LoaderCircle className="size-3.5 shrink-0 animate-spin text-primary motion-reduce:animate-none" aria-label="进行中" />
      ) : isError ? (
        <X className="size-3.5 shrink-0 text-destructive" aria-label="失败" />
      ) : (
        <Check className="size-3.5 shrink-0 text-success" aria-label="完成" />
      )}
      <span className={done ? 'text-foreground' : 'font-medium text-foreground'}>{toolPresentation(toolName).label}</span>
      {isError ? <span className="text-muted-foreground">没有读到数据</span> : null}
    </li>
  );
}
