import type { FindingChart as FindingChartData } from '@/api/generated/operationsAgentSessions.gen';
import { BarList } from '@/components/ui/bar-list';
import { ComposedChart } from '@/components/ui/composed-chart';
import { formatDecimal } from '@/lib/operator-format';

const formatValue = (value: number): string => formatDecimal(value, Number.isInteger(value) ? 0 : 1);

/**
 * The card's chart (#640). The Agent drew it from one read, so every number is one the answer rests
 * on: hours as a line, days and periods as bars, machines as a ranked bar list.
 */
export function FindingChart({ chart }: { readonly chart: FindingChartData }) {
  const caption = chart.unit === '' ? chart.title : `${chart.title}（${chart.unit}）`;
  return (
    <figure aria-label={chart.title} className="m-0 flex flex-col gap-2" data-testid="assistant-chart">
      <figcaption className="text-xs text-muted-foreground">{caption}</figcaption>
      {chart.kind === 'HORIZONTAL_BAR' ? (
        <BarList
          data={chart.categories.flatMap((name, index) => {
            const value = chart.series[0]!.values[index];
            return value === null || value === undefined ? [] : [{ name, value }];
          })}
          valueFormatter={formatValue}
        />
      ) : (
        <ComposedChart
          className="aspect-auto h-40 w-full"
          data={chart.categories.map((category, index) => ({
            category,
            ...Object.fromEntries(chart.series.map((series, position) => [`series${position}`, series.values[index]])),
          }))}
          xKey="category"
          series={chart.series.map((series, position) => ({
            key: `series${position}`,
            label: series.name,
            type: chart.kind === 'LINE' ? 'line' as const : 'bar' as const,
          }))}
          showYAxis
          showLegend={chart.series.length > 1}
          yFormatter={formatValue}
          animate={false}
        />
      )}
    </figure>
  );
}
