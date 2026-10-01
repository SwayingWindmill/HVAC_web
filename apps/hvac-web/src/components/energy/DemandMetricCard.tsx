import type { ReactNode } from 'react';
import { Gauge } from 'lucide-react';
import { KpiCard, type KpiCardProps } from '@/components/ui/kpi-card';

export interface DemandMetricCardProps extends Omit<KpiCardProps, 'value' | 'label'> {
  readonly currentDemand: number; // in kW
  readonly maxLimit: number; // in kW
  readonly label?: string;
  readonly subtext?: ReactNode;
}

export function DemandMetricCard({
  currentDemand,
  maxLimit,
  label = '最大需量负荷',
  delta,
  deltaLabel,
  trend,
  subtext,
  className,
  ...props
}: DemandMetricCardProps) {
  const ratio = maxLimit > 0 ? (currentDemand / maxLimit) * 100 : 0;
  const isHighRisk = ratio > 90;

  return (
    <KpiCard
      label={label}
      value={new Intl.NumberFormat('zh-CN').format(currentDemand)}
      suffix="kW"
      delta={delta}
      deltaLabel={deltaLabel ?? `核定上限: ${new Intl.NumberFormat('zh-CN').format(maxLimit)} kW`}
      invertDelta={true}
      trend={trend}
      trendColor={isHighRisk ? 'var(--color-amber-500, #f59e0b)' : undefined}
      icon={<Gauge className="size-3.5 text-blue-500" />}
      className={className}
      {...props}
    >
      {subtext}
    </KpiCard>
  );
}
