import { useQuery, useQueryClient } from '@tanstack/react-query';
import { getRouteApi } from '@tanstack/react-router';
import { RefreshCw, TrendingUp } from 'lucide-react';
import { useMemo } from 'react';
import { Area, CartesianGrid, ComposedChart, Line, ReferenceLine, XAxis, YAxis } from 'recharts';
import { MetricCard, MetricGrid, MetricValue } from '@/blocks/metric-card';
import { PageHeader } from '@/blocks/page-header';
import { Main } from '@/components/layout/Main';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { ChartContainer, ChartLegend, ChartLegendContent, ChartTooltip, type ChartConfig } from '@/components/ui/chart';
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from '@/components/ui/empty';
import { ScrollArea } from '@/components/ui/scroll-area';
import { Skeleton } from '@/components/ui/skeleton';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { useNow } from '@/hooks/use-now';
import { formatDecimal, formatTime } from '@/lib/operator-format';
import { cn } from '@/lib/utils';
import { forecastChartRows, forecastSummary, hourlyForecast, nextDay, type ForecastChartRow } from '../forecast-model';
import { siteLoadForecastQuery, useRecentLoad } from '../query';

const siteRoute = getRouteApi('/_app/_site');

const chartConfig = {
  actualKW: { label: '实际负荷', color: 'var(--chart-5)' },
  forecastKW: { label: '预测负荷', color: 'var(--chart-1)' },
  band: { label: '预测区间', color: 'var(--chart-1)' },
} satisfies ChartConfig;

function ChartTooltipBody({ active, payload, timeZone }: { active?: boolean; payload?: { payload: ForecastChartRow }[]; timeZone: string }) {
  const row = payload?.[0]?.payload;
  if (!active || !row) return null;
  return (
    <div className="grid min-w-40 gap-1.5 rounded-lg border bg-background px-2.5 py-1.5 text-xs shadow-xl">
      <p className="font-medium">{formatTime(new Date(row.at).toISOString(), timeZone)}</p>
      {row.actualKW !== null ? <p className="flex justify-between gap-4"><span className="text-muted-foreground">实际</span><span className="tabular-nums">{formatDecimal(row.actualKW, 1)} kW</span></p> : null}
      {row.forecastKW !== null ? <p className="flex justify-between gap-4"><span className="text-muted-foreground">预测</span><span className="tabular-nums">{formatDecimal(row.forecastKW, 1)} kW</span></p> : null}
      {row.band ? <p className="flex justify-between gap-4"><span className="text-muted-foreground">区间</span><span className="tabular-nums">{formatDecimal(row.band[0], 0)}–{formatDecimal(row.band[1], 0)} kW</span></p> : null}
    </div>
  );
}

export function ForecastWorkspace() {
  const { site } = siteRoute.useRouteContext();
  const queryClient = useQueryClient();
  const now = useNow(60_000);
  const forecast = useQuery(siteLoadForecastQuery(site.id));
  const recent = useRecentLoad();
  const data = useMemo(() => (forecast.data ? nextDay(forecast.data) : forecast.data), [forecast.data]);

  const summary = useMemo(() => (data ? forecastSummary(data) : null), [data]);
  const hours = useMemo(() => (data ? hourlyForecast(data, site.timezone) : []), [data, site.timezone]);
  const rows = useMemo(() => (data ? forecastChartRows(recent.data ?? [], data) : []), [data, recent.data]);
  const tick = (at: number) => formatTime(new Date(at).toISOString(), site.timezone).slice(-5);
  // Whole hours every four hours, so the axis reads as a clock rather than odd minutes.
  const ticks = useMemo(() => {
    if (rows.length === 0) return [];
    const first = Math.ceil(rows[0].at / 3_600_000) * 3_600_000;
    const last = rows[rows.length - 1].at;
    return Array.from({ length: Math.floor((last - first) / 14_400_000) + 1 }, (_, index) => first + index * 14_400_000);
  }, [rows]);

  return (
    <Main className="@container/main flex flex-col gap-4 md:gap-6">
      <PageHeader
        title="负荷预测"
        description={`${site.displayName} · 冷站用电负荷，未来 24 小时，每小时更新`}
        meta={data ? (
          <Badge variant="outline" className="font-normal">
            {data.quality === 'VALID' ? '按近 7 天同时段负荷预测' : '历史不足，暂按最近读数延续'}
          </Badge>
        ) : undefined}
        actions={
          <Button variant="outline" size="sm" onClick={() => void queryClient.invalidateQueries({ queryKey: ['forecast', site.id] })}>
            <RefreshCw data-icon="inline-start" />
            刷新
          </Button>
        }
      />

      {forecast.isPending ? (
        <Skeleton className="h-[520px] rounded-xl" />
      ) : forecast.isError ? (
        <Card><CardContent className="py-16 text-center text-sm text-muted-foreground">负荷预测暂不可用</CardContent></Card>
      ) : data === null || data === undefined || !summary ? (
        <Card>
          <Empty className="py-16">
            <EmptyHeader>
              <EmptyMedia variant="icon"><TrendingUp /></EmptyMedia>
              <EmptyTitle>还没有负荷预测</EmptyTitle>
              <EmptyDescription>预测需要至少一小时的 15 分钟负荷数据，系统每小时第 7 分钟自动更新。</EmptyDescription>
            </EmptyHeader>
          </Empty>
        </Card>
      ) : (
        <>
          <MetricGrid ariaLabel="预测概况">
            <MetricCard
              label="预测峰值负荷"
              value={<MetricValue value={formatDecimal(summary.peak.loadKW, 1)} unit="kW" />}
              lead={`出现在 ${formatTime(summary.peak.at, site.timezone)}`}
              detail="未来 24 小时的最高 15 分钟负荷"
            />
            <MetricCard
              label="预测谷值负荷"
              value={<MetricValue value={formatDecimal(summary.trough.loadKW, 1)} unit="kW" />}
              lead={`出现在 ${formatTime(summary.trough.at, site.timezone)}`}
              detail="未来 24 小时的最低 15 分钟负荷"
            />
            <MetricCard
              label="未来 24 小时用电"
              value={<MetricValue value={formatDecimal(summary.energyKWh, 0)} unit="kWh" />}
              lead="按预测负荷累计"
              detail="空调总电表口径"
            />
            <MetricCard
              label="预测更新于"
              value={<MetricValue value={formatTime(data.origin, site.timezone).slice(-5)} />}
              lead={formatTime(data.origin, site.timezone)}
              detail="每小时第 7 分钟自动更新"
            />
          </MetricGrid>

          <div className="grid items-start gap-4 md:gap-6 @5xl/main:grid-cols-[minmax(0,2fr)_minmax(0,1fr)]">
            <Card role="region" aria-label="实际与预测">
              <CardHeader>
                <CardTitle>实际与预测</CardTitle>
                <CardDescription>过去 24 小时逐时实际负荷，未来 24 小时每 15 分钟预测（kW）</CardDescription>
              </CardHeader>
              <CardContent className="px-2 sm:px-6">
                <ChartContainer config={chartConfig} className="aspect-auto h-[360px] w-full">
                  <ComposedChart data={rows} margin={{ left: 4, right: 12 }}>
                    <CartesianGrid vertical={false} />
                    <XAxis dataKey="at" type="number" scale="time" domain={['dataMin', 'dataMax']} ticks={ticks} tickFormatter={tick} tickLine={false} axisLine={false} />
                    <YAxis tickLine={false} axisLine={false} width={44} domain={[0, 'auto']} />
                    <ChartTooltip cursor={{ strokeDasharray: '3 3' }} content={<ChartTooltipBody timeZone={site.timezone} />} />
                    <Area dataKey="band" stroke="none" fill="var(--color-band)" fillOpacity={0.12} connectNulls={false} isAnimationActive={false} />
                    <Line dataKey="actualKW" stroke="var(--color-actualKW)" strokeWidth={2} dot={false} connectNulls={false} isAnimationActive={false} />
                    <Line dataKey="forecastKW" stroke="var(--color-forecastKW)" strokeWidth={2} dot={false} connectNulls={false} isAnimationActive={false} />
                    <ReferenceLine x={now} stroke="var(--muted-foreground)" strokeDasharray="4 4" label={{ value: '现在', position: 'insideTopRight', fontSize: 11, fill: 'var(--muted-foreground)' }} />
                    <ChartLegend content={<ChartLegendContent />} />
                  </ComposedChart>
                </ChartContainer>
              </CardContent>
            </Card>

            <Card role="region" aria-label="逐时预测">
              <CardHeader>
                <CardTitle>逐时预测</CardTitle>
                <CardDescription>每小时平均负荷与区间，峰值时段已标出</CardDescription>
              </CardHeader>
              <CardContent className="px-0">
                <ScrollArea className="h-[360px]">
                  <Table aria-label="逐时预测负荷">
                    <TableHeader>
                      <TableRow>
                        <TableHead className="pl-6">时段</TableHead>
                        <TableHead className="text-right">负荷（kW）</TableHead>
                        <TableHead className="pr-6 text-right">区间（kW）</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {hours.map((hour) => (
                        <TableRow key={hour.label} className={cn(hour.peak && 'bg-primary/5')}>
                          <TableCell className="pl-6">
                            {hour.label}
                            {hour.peak ? <Badge variant="outline" className="ml-2 border-primary/30 font-normal text-primary">峰值</Badge> : null}
                          </TableCell>
                          <TableCell className="text-right font-medium tabular-nums">{formatDecimal(hour.loadKW, 1)}</TableCell>
                          <TableCell className="pr-6 text-right text-muted-foreground tabular-nums">
                            {hour.lowerKW !== null && hour.upperKW !== null ? `${formatDecimal(hour.lowerKW, 0)}–${formatDecimal(hour.upperKW, 0)}` : '—'}
                          </TableCell>
                        </TableRow>
                      ))}
                    </TableBody>
                  </Table>
                </ScrollArea>
              </CardContent>
            </Card>
          </div>
        </>
      )}
    </Main>
  );
}
