import { Award } from 'lucide-react';
import { formatCurrency, formatEnergy, formatPercent } from '@/lib/units/formatter';
import { MetricCard } from '@/components/common/MetricCard';
import { Badge } from '@/components/ui/badge';

export interface SavingsMetricProps {
  readonly energySaved: number; // in kWh
  readonly costSaved: number; // in CNY
  readonly savingsRatio?: number; // e.g. 0.124
  readonly method?: string; // e.g. "IPMVP Option C"
  readonly label?: string;
  readonly loading?: boolean;
  readonly className?: string;
}

export function SavingsMetric({
  energySaved,
  costSaved,
  savingsRatio,
  method = 'IPMVP 验证',
  label = '累计节能量',
  loading = false,
  className,
}: SavingsMetricProps) {
  const formattedEnergy = formatEnergy(energySaved);
  const formattedCost = formatCurrency(costSaved);

  return (
    <MetricCard
      title={label}
      value={formattedEnergy.value}
      unit={formattedEnergy.unit}
      badge={
        <Badge
          variant="outline"
          className="text-[10px] font-normal px-1.5 py-0 h-4 border-emerald-300 bg-emerald-50 text-emerald-700 dark:border-emerald-800 dark:bg-emerald-950/40 dark:text-emerald-400"
        >
          {method}
        </Badge>
      }
      icon={<Award className="size-4 text-emerald-600 dark:text-emerald-400" />}
      trend={
        savingsRatio !== undefined
          ? {
              value: formatPercent(savingsRatio),
              direction: 'up',
              label: '节能率',
              isGood: true,
            }
          : undefined
      }
      subtext={`折合节省费用: ${formattedCost.formatted}`}
      loading={loading}
      className={className}
    />
  );
}
