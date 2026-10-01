import type { ReactNode } from 'react';
import { KpiCard, type KpiCardProps } from '@/components/ui/kpi-card';
import { formatEnergy, formatCurrency } from '@/lib/units/formatter';

export type SavingsMetricType = 'energy' | 'cost' | 'rate' | 'potential';

export interface SavingsMetricCardProps extends Omit<KpiCardProps, 'value' | 'label'> {
  readonly value: number;
  readonly type: SavingsMetricType;
  readonly label?: string;
  readonly subtext?: ReactNode;
}

export function SavingsMetricCard({
  value,
  type,
  label,
  delta,
  deltaLabel,
  trend,
  subtext,
  icon,
  className,
  ...props
}: SavingsMetricCardProps) {
  let displayValue: string | number = value;
  let suffix: ReactNode = '';
  let defaultLabel = '已节约电量';

  switch (type) {
    case 'energy': {
      const formatted = formatEnergy(value);
      displayValue = formatted.value;
      suffix = formatted.unit;
      defaultLabel = '已节约电量';
      break;
    }
    case 'cost': {
      const formatted = formatCurrency(value);
      displayValue = formatted.value;
      suffix = formatted.unit;
      defaultLabel = '已节省费用';
      break;
    }
    case 'rate': {
      displayValue = (value * 100).toFixed(1);
      suffix = '%';
      defaultLabel = '综合节能率';
      break;
    }
    case 'potential': {
      const formatted = formatCurrency(value);
      displayValue = formatted.value;
      suffix = `${formatted.unit}/年`;
      defaultLabel = '待优化空间';
      break;
    }
  }

  return (
    <KpiCard
      label={label ?? defaultLabel}
      value={displayValue}
      suffix={suffix}
      delta={delta}
      deltaLabel={deltaLabel}
      invertDelta={false}
      trend={trend}
      icon={icon}
      className={className}
      {...props}
    >
      {subtext}
    </KpiCard>
  );
}
