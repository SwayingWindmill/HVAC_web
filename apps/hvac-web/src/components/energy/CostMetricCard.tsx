import type { ReactNode } from 'react';
import { KpiCard, type KpiCardProps } from '@/components/ui/kpi-card';
import { formatCurrency } from '@/lib/units/formatter';

export interface CostMetricCardProps extends Omit<KpiCardProps, 'value' | 'label'> {
  readonly value: number; // in CNY
  readonly label?: string;
  readonly budgetValue?: number;
  readonly subtext?: ReactNode;
}

export function CostMetricCard({
  value,
  label = '能源费用',
  budgetValue,
  delta,
  deltaLabel,
  trend,
  subtext,
  icon,
  className,
  ...props
}: CostMetricCardProps) {
  const formatted = formatCurrency(value);

  let calculatedDelta = delta;
  if (calculatedDelta === undefined && budgetValue !== undefined && budgetValue > 0) {
    calculatedDelta = (value - budgetValue) / budgetValue;
  }

  return (
    <KpiCard
      label={label}
      value={formatted.value}
      suffix={formatted.unit}
      delta={calculatedDelta}
      deltaLabel={deltaLabel ?? (budgetValue ? 'vs.预算' : undefined)}
      invertDelta={true}
      trend={trend}
      icon={icon}
      className={className}
      {...props}
    >
      {subtext}
    </KpiCard>
  );
}
