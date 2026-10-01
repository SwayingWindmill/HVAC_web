import type { ReactNode } from 'react';
import { KpiCard, type KpiCardProps } from '@/components/ui/kpi-card';
import { formatEnergy } from '@/lib/units/formatter';
import { COMMODITY_CONFIG, type EnergyCommodity } from '@/lib/energy-tokens';

export interface EnergyMetricCardProps extends Omit<KpiCardProps, 'value' | 'label'> {
  readonly value: number; // in kWh
  readonly label?: string;
  readonly commodity?: EnergyCommodity;
  readonly baselineValue?: number;
  readonly subtext?: ReactNode;
}

export function EnergyMetricCard({
  value,
  label,
  commodity = 'electricity',
  baselineValue,
  delta,
  deltaLabel,
  trend,
  subtext,
  icon,
  className,
  ...props
}: EnergyMetricCardProps) {
  const config = COMMODITY_CONFIG[commodity] ?? COMMODITY_CONFIG.electricity;
  const formatted = formatEnergy(value);

  let calculatedDelta = delta;
  if (calculatedDelta === undefined && baselineValue !== undefined && baselineValue > 0) {
    calculatedDelta = (value - baselineValue) / baselineValue;
  }

  return (
    <KpiCard
      label={label ?? `${config.label}能耗`}
      value={formatted.value}
      suffix={formatted.unit}
      delta={calculatedDelta}
      deltaLabel={deltaLabel ?? (baselineValue ? 'vs.基线' : undefined)}
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
