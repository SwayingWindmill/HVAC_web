import * as React from 'react';
import { Slot } from 'radix-ui';
import { cva, type VariantProps } from 'class-variance-authority';
import { cn } from 'cn';

const itemVariants = cva('group/item flex min-w-0 items-center gap-4 rounded-md text-sm outline-none transition-colors', {
  variants: {
    variant: {
      default: 'bg-transparent',
      outline: 'border bg-background',
      muted: 'bg-muted/50',
    },
    size: {
      default: 'p-4',
      sm: 'px-3 py-2.5',
    },
  },
  defaultVariants: { variant: 'default', size: 'default' },
});

function Item({ asChild = false, className, variant = 'default', size = 'default', ...props }: React.ComponentProps<'div'> & VariantProps<typeof itemVariants> & { asChild?: boolean }) {
  const Component = asChild ? Slot.Root : 'div';
  return <Component data-slot="item" data-variant={variant} data-size={size} className={cn(itemVariants({ variant, size }), className)} {...props} />;
}

function ItemMedia({ className, ...props }: React.ComponentProps<'div'>) {
  return <div data-slot="item-media" className={cn('flex shrink-0 items-center justify-center text-muted-foreground [&>svg]:size-4', className)} {...props} />;
}

function ItemContent({ className, ...props }: React.ComponentProps<'div'>) {
  return <div data-slot="item-content" className={cn('flex min-w-0 flex-1 flex-col gap-1', className)} {...props} />;
}

function ItemTitle({ className, ...props }: React.ComponentProps<'div'>) {
  return <div data-slot="item-title" className={cn('flex min-w-0 items-center gap-2 font-medium leading-snug', className)} {...props} />;
}

function ItemDescription({ className, ...props }: React.ComponentProps<'p'>) {
  return <p data-slot="item-description" className={cn('text-sm leading-relaxed text-muted-foreground', className)} {...props} />;
}

function ItemActions({ className, ...props }: React.ComponentProps<'div'>) {
  return <div data-slot="item-actions" className={cn('ml-auto flex shrink-0 items-center gap-2', className)} {...props} />;
}

function ItemGroup({ className, ...props }: React.ComponentProps<'div'>) {
  return <div role="list" data-slot="item-group" className={cn('flex w-full flex-col', className)} {...props} />;
}

export { Item, ItemMedia, ItemContent, ItemTitle, ItemDescription, ItemActions, ItemGroup };
