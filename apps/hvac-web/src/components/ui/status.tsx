import type * as React from "react";

import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "cn";
import { Slot as SlotPrimitive } from "radix-ui";

const statusVariants = cva(
  "inline-flex w-fit shrink-0 items-center gap-1.5 overflow-hidden rounded-full border px-2.5 py-1 text-xs font-medium whitespace-nowrap transition-colors",
  {
    variants: {
      variant: {
        default:
          "border-transparent bg-muted text-muted-foreground **:data-[slot=status-indicator]:bg-muted-foreground",
        success:
          "border-success/20 bg-success/10 text-success **:data-[slot=status-indicator]:bg-success",
        error:
          "border-destructive/20 bg-destructive/10 text-destructive **:data-[slot=status-indicator]:bg-destructive",
        warning:
          "border-warning/20 bg-warning/10 text-warning **:data-[slot=status-indicator]:bg-warning",
        info: "border-information/20 bg-information/10 text-information **:data-[slot=status-indicator]:bg-information",
      },
    },
    defaultVariants: {
      variant: "default",
    },
  },
);

interface StatusProps
  extends VariantProps<typeof statusVariants>, React.ComponentProps<"div"> {
  asChild?: boolean;
}

function Status(props: StatusProps) {
  const { className, variant = "default", asChild, ...rootProps } = props;

  const RootPrimitive = asChild ? SlotPrimitive.Slot : "div";

  return (
    <RootPrimitive
      data-slot="status"
      data-variant={variant}
      {...rootProps}
      className={cn(statusVariants({ variant }), className)}
    />
  );
}

function StatusIndicator(props: React.ComponentProps<"div">) {
  const { className, ...indicatorProps } = props;

  return (
    <div
      data-slot="status-indicator"
      {...indicatorProps}
      className={cn(
        "relative flex size-2 shrink-0 rounded-full",
        "before:absolute before:inset-0 before:rounded-full before:bg-inherit motion-safe:before:animate-ping",
        "after:absolute after:inset-[2px] after:rounded-full after:bg-inherit",
        className,
      )}
    />
  );
}

function StatusLabel(props: React.ComponentProps<"div">) {
  const { className, ...labelProps } = props;

  return (
    <div
      data-slot="status-label"
      {...labelProps}
      className={cn("leading-none", className)}
    />
  );
}

export { Status, StatusIndicator, StatusLabel, statusVariants };
