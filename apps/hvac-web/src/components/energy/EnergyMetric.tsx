import { Zap } from 'lucide-react';
import { formatEnergy, formatPercent } from '@/lib/units/formatter';
import { COMMODITY_CONFIG, type EnergyCommodity } from '@/lib/energy-tokens';
import { MetricCard } from '@/components/common/MetricCard';
import { Badge } from '@/components/ui/badge';
import { cn } from '@/lib/utils';

export interface EnergyMetricProps {
  readonly value: number; // in kWh
  readonly commodity?: EnergyCommodity;
  readonly baselineValue?: number; // in kWh
  readonly label?: string;
  readonly subtext?: string;
  readonly loading?: boolean;
  readonly className?: string;
}

export function EnergyMetric({
  value,
  commodity = 'electricity',
  baselineValue,
  label,
  subtext,
  loading = false,
  className,
}: EnergyMetricProps) {
  const config = COMMODITY_CONFIG[commodity] ?? COMMODITY_CONFIG.electricity;
  const formatted = formatEnergy(value);

  let trend;
  if (baselineValue !== undefined && baselineValue > 0) {
    const diff = value - baselineValue;
    const ratio = diff / baselineValue;
    const direction = ratio > 0.005 ? 'up' : ratio < -0.005 ? 'down' : 'flat';
    // For energy consumption, down is good (saved energy), up is bad (more consumption)
    const isGood = direction === 'down';
    trend = {
      value: formatPercent(Math.abs(ratio), { showSign: false }),
      direction: direction as 'up' | 'down' | 'flat',
      label: 'vs. 基线',
      isGood,
    };
  }

  return (
    <MetricCard
      title={label ?? `${config.label}能耗`}
      value={formatted.value}
      unit={formatted.unit}
      badge={
        <Badge
          variant="outline"
          className={cn('text-[10px] font-normal px-1.5 py-0 h-4 border-none', config.bgLight, config.textClass)}
        >
          {config.label}
        </Badge>
      }
      icon={<Zap className={cn('size-4', config.textClass)} />}
      trend={trend}
      subtext={subtext}
      loading={loading}
      className={className}
    />
  );
}
