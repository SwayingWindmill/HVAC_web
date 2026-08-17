import type { ComponentProps } from 'react';
import ReactECharts from 'echarts-for-react';

export type TimeSeriesChartProps = ComponentProps<typeof ReactECharts>;

/** Shared Real chart primitive. Feature code supplies data/options only. */
export function TimeSeriesChart(props: TimeSeriesChartProps) {
  return <ReactECharts {...props} notMerge lazyUpdate />;
}

export function EnergyTrendChart(props: TimeSeriesChartProps) {
  return <TimeSeriesChart {...props} aria-label={props['aria-label'] ?? '能源趋势图'} />;
}

export function ForecastChart(props: TimeSeriesChartProps) {
  return <TimeSeriesChart {...props} aria-label={props['aria-label'] ?? '预测趋势图'} />;
}

export function DispatchPlanChart(props: TimeSeriesChartProps) {
  return <TimeSeriesChart {...props} aria-label={props['aria-label'] ?? '调度计划图'} />;
}
