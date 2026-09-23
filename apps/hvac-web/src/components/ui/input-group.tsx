import * as React from 'react';
import { cn } from 'cn';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Textarea } from '@/components/ui/textarea';

function InputGroup({ className, ...props }: React.ComponentProps<'div'>) {
  return (
    <div
      data-slot="input-group"
      role="group"
      className={cn(
        'group/input-group flex min-h-8 w-full min-w-0 items-center overflow-hidden rounded-lg border border-input bg-transparent shadow-xs transition-colors focus-within:border-ring focus-within:ring-3 focus-within:ring-ring/50 has-[[aria-invalid=true]]:border-destructive has-[[aria-invalid=true]]:ring-destructive/20',
        className,
      )}
      {...props}
    />
  );
}

function InputGroupInput({ className, ...props }: React.ComponentProps<'input'>) {
  return (
    <Input
      data-slot="input-group-control"
      className={cn('h-8 min-w-0 flex-1 rounded-none border-0 bg-transparent px-2.5 py-1 shadow-none ring-0 focus-visible:ring-0 disabled:bg-transparent aria-invalid:ring-0', className)}
      {...props}
    />
  );
}

function InputGroupTextarea({ className, ...props }: React.ComponentProps<'textarea'>) {
  return (
    <Textarea
      data-slot="input-group-control"
      className={cn('min-h-20 min-w-0 flex-1 resize-y rounded-none border-0 bg-transparent px-2.5 py-2 shadow-none ring-0 focus-visible:ring-0 disabled:bg-transparent aria-invalid:ring-0', className)}
      {...props}
    />
  );
}

function InputGroupAddon({
  className,
  align = 'inline-start',
  ...props
}: React.ComponentProps<'div'> & { align?: 'inline-start' | 'inline-end' | 'block-start' | 'block-end' }) {
  return (
    <div
      data-slot="input-group-addon"
      data-align={align}
      className={cn(
        'flex shrink-0 items-center gap-1.5 px-2.5 text-xs text-muted-foreground [&_svg:not([class*=size-])]:size-4',
        align === 'inline-start' && 'order-first',
        align === 'inline-end' && 'order-last',
        className,
      )}
      {...props}
    />
  );
}

function InputGroupText({ className, ...props }: React.ComponentProps<'span'>) {
  return <span data-slot="input-group-text" className={cn('text-xs text-muted-foreground', className)} {...props} />;
}

function InputGroupButton({ className, ...props }: React.ComponentProps<typeof Button>) {
  return <Button data-slot="input-group-button" type="button" variant="ghost" size="icon-sm" className={cn('m-0.5', className)} {...props} />;
}

export {
  InputGroup,
  InputGroupAddon,
  InputGroupButton,
  InputGroupInput,
  InputGroupText,
  InputGroupTextarea,
};
