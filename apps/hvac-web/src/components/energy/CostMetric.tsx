import { Coins } from 'lucide-react';
import { formatCurrency, formatPercent } from '@/lib/units/formatter';
import { MetricCard } from '@/components/common/MetricCard';

export interface CostMetricProps {
  readonly amount: number; // in CNY
  readonly budgetAmount?: number; // in CNY
  readonly unitCost?: number; // e.g. 0.85 ¥/kWh
  readonly label?: string;
  readonly subtext?: string;
  readonly loading?: boolean;
  readonly className?: string;
}

export function CostMetric({
  amount,
  budgetAmount,
  unitCost,
  label = '能源总费用',
  subtext,
  loading = false,
  className,
}: CostMetricProps) {
  const formatted = formatCurrency(amount);

  let trend;
  if (budgetAmount !== undefined && budgetAmount > 0) {
    const diff = amount - budgetAmount;
    const ratio = diff / budgetAmount;
    const direction = ratio > 0.005 ? 'up' : ratio < -0.005 ? 'down' : 'flat';
    // For cost, down is good (within budget), up is bad (over budget)
    const isGood = direction === 'down';
    trend = {
      value: formatPercent(Math.abs(ratio), { showSign: false }),
      direction: direction as 'up' | 'down' | 'flat',
      label: 'vs. 预算',
      isGood,
    };
  }

  const extraSubtext = subtext ?? (unitCost !== undefined ? `平均电价: ¥${unitCost.toFixed(2)}/kWh` : undefined);

  return (
    <MetricCard
      title={label}
      value={formatted.value}
      unit={formatted.unit}
      icon={<Coins className="size-4 text-emerald-600 dark:text-emerald-400" />}
      trend={trend}
      subtext={extraSubtext}
      loading={loading}
      className={className}
    />
  );
}
