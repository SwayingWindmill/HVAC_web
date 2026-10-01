import * as React from 'react';
import { cva, type VariantProps } from 'class-variance-authority';
import { cn } from 'cn';
import { Separator } from '@/components/ui/separator';

function FieldSet({ className, ...props }: React.ComponentProps<'fieldset'>) {
  return <fieldset data-slot="field-set" className={cn('flex flex-col gap-6', className)} {...props} />;
}

const fieldLegendVariants = cva('font-medium', {
  variants: {
    variant: {
      legend: 'text-base',
      label: 'text-sm',
    },
  },
  defaultVariants: {
    variant: 'legend',
  },
});

function FieldLegend({
  className,
  variant,
  ...props
}: React.ComponentProps<'legend'> & VariantProps<typeof fieldLegendVariants>) {
  return <legend data-slot="field-legend" className={cn(fieldLegendVariants({ variant }), className)} {...props} />;
}

function FieldGroup({ className, ...props }: React.ComponentProps<'div'>) {
  return <div data-slot="field-group" className={cn('@container/field-group flex flex-col gap-5', className)} {...props} />;
}

const fieldVariants = cva('group/field flex w-full gap-2 data-[invalid=true]:text-destructive', {
  variants: {
    orientation: {
      vertical: 'flex-col',
      horizontal: 'flex-row items-center',
      responsive: 'flex-col @md/field-group:flex-row @md/field-group:items-start',
    },
  },
  defaultVariants: {
    orientation: 'vertical',
  },
});

function Field({
  className,
  orientation,
  ...props
}: React.ComponentProps<'div'> & VariantProps<typeof fieldVariants>) {
  return <div role="group" data-slot="field" data-orientation={orientation ?? 'vertical'} className={cn(fieldVariants({ orientation }), className)} {...props} />;
}

function FieldContent({ className, ...props }: React.ComponentProps<'div'>) {
  return <div data-slot="field-content" className={cn('flex min-w-0 flex-1 flex-col gap-1', className)} {...props} />;
}

function FieldLabel({ className, ...props }: React.ComponentProps<'label'>) {
  return <label data-slot="field-label" className={cn('flex w-fit items-center gap-2 text-xs font-medium leading-none text-foreground group-data-[disabled=true]/field:pointer-events-none group-data-[disabled=true]/field:opacity-50', className)} {...props} />;
}

function FieldTitle({ className, ...props }: React.ComponentProps<'div'>) {
  return <div data-slot="field-title" className={cn('text-sm font-medium leading-snug', className)} {...props} />;
}

function FieldDescription({ className, ...props }: React.ComponentProps<'p'>) {
  return <p data-slot="field-description" className={cn('text-[11px] leading-5 text-muted-foreground', className)} {...props} />;
}

function FieldError({ className, children, ...props }: React.ComponentProps<'p'>) {
  if (!children) return null;
  return <p data-slot="field-error" role="alert" className={cn('text-[11px] leading-5 text-destructive', className)} {...props}>{children}</p>;
}

function FieldSeparator({ className, children, ...props }: React.ComponentProps<'div'>) {
  return (
    <div data-slot="field-separator" className={cn('relative -my-1 flex items-center gap-3 text-[11px] text-muted-foreground', className)} {...props}>
      <Separator className="flex-1" />
      {children ? <span>{children}</span> : null}
      {children ? <Separator className="flex-1" /> : null}
    </div>
  );
}

export {
  Field,
  FieldContent,
  FieldDescription,
  FieldError,
  FieldGroup,
  FieldLabel,
  FieldLegend,
  FieldSeparator,
  FieldSet,
  FieldTitle,
};
