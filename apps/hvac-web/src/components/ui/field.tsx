import * as React from 'react';
import { cva, type VariantProps } from 'class-variance-authority';
import { cn } from 'cn';
import { Label } from '@/components/ui/label';
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
  return <div data-slot="field-group" className={cn('group/field-group @container/field-group flex w-full flex-col gap-5', className)} {...props} />;
}

const fieldVariants = cva('group/field flex w-full gap-2 data-[invalid=true]:text-destructive', {
  variants: {
    orientation: {
      vertical: 'flex-col *:w-full [&>.sr-only]:w-auto',
      horizontal: 'flex-row items-center has-[>[data-slot=field-content]]:items-start *:data-[slot=field-label]:flex-auto has-[>[data-slot=field-content]]:[&>[role=checkbox],[role=radio]]:mt-px',
      responsive: 'flex-col *:w-full @md/field-group:flex-row @md/field-group:items-center @md/field-group:*:w-auto @md/field-group:has-[>[data-slot=field-content]]:items-start @md/field-group:*:data-[slot=field-label]:flex-auto [&>.sr-only]:w-auto',
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

function FieldLabel({ className, ...props }: React.ComponentProps<typeof Label>) {
  return <Label data-slot="field-label" className={cn('flex w-fit items-center gap-2 text-xs font-medium leading-none text-foreground group-data-[disabled=true]/field:pointer-events-none group-data-[disabled=true]/field:opacity-50', className)} {...props} />;
}

function FieldTitle({ className, ...props }: React.ComponentProps<'div'>) {
  return <div data-slot="field-title" className={cn('text-sm font-medium leading-snug', className)} {...props} />;
}

function FieldDescription({ className, ...props }: React.ComponentProps<'p'>) {
  return <p data-slot="field-description" className={cn('text-[11px] leading-5 text-muted-foreground', className)} {...props} />;
}

function FieldError({
  className,
  children,
  errors,
  ...props
}: React.ComponentProps<'div'> & {
  errors?: Array<{ message?: string } | undefined>;
}) {
  const content = React.useMemo(() => {
    if (children) return children;
    if (!errors?.length) return null;

    const uniqueErrors = [...new Map(errors.map((error) => [error?.message, error])).values()];
    if (uniqueErrors.length === 1) return uniqueErrors[0]?.message;

    return (
      <ul className="ml-4 flex list-disc flex-col gap-1">
        {uniqueErrors.map((error, index) => error?.message ? <li key={index}>{error.message}</li> : null)}
      </ul>
    );
  }, [children, errors]);

  if (!content) return null;
  return <div data-slot="field-error" role="alert" className={cn('text-[11px] leading-5 text-destructive', className)} {...props}>{content}</div>;
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
