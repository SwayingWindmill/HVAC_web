import { useQuery } from '@tanstack/react-query';
import { useNavigate, useSearch } from '@tanstack/react-router';
import { Trophy } from 'lucide-react';
import { useMemo } from 'react';
import { CartesianGrid, Line, LineChart, XAxis, YAxis } from 'recharts';
import { createPlatformGatewayClient, PlatformApiError, type Site } from '@/api/generated/platformGateway.gen';
import type { ShellSnapshot } from '@/app/shell-runtime';
import { FactList, FactRow } from '@/blocks/fact-list';
import { MetricCard, MetricGrid, MetricValue } from '@/blocks/metric-card';
import { PageHeader, PageMeta } from '@/blocks/page-header';
import { Main } from '@/components/layout/Main';
import { Badge } from '@/components/ui/badge';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { ChartContainer, ChartLegend, ChartLegendContent, ChartTooltip, ChartTooltipContent, type ChartConfig } from '@/components/ui/chart';
import { DeltaBadge } from '@/components/ui/delta-badge';
import { PeriodTabs } from '@/components/ui/period-tabs';
import { Skeleton } from '@/components/ui/skeleton';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { periodWindow, summarizeEnergy } from '@/features/energy-analysis/consumption/model';
import { formatDecimal } from '@/lib/operator-format';
import { cn } from '@/lib/utils';
import { benchmark } from '../benchmark-model';

const client = createPlatformGatewayClient();
type BenchmarkPeriod = '7d' | 'month';
const PERIODS = [{ value: '7d', label: '近 7 天' }, { value: 'month', label: '本月' }];
const SITE_COLORS = ['var(--chart-1)', 'var(--chart-3)', 'var(--chart-2)', 'var(--chart-4)', 'var(--chart-5)'];

const energy = (value: number | null) => (value === null ? '—' : value >= 10_000 ? `${formatDecimal(value / 1000, 2)} MWh` : `${formatDecimal(value, 0)} kWh`);

/** Each authorized Site's daily electricity and cooling over the period, on its own clock. */
function useSiteSummaries(sites: readonly Site[], period: BenchmarkPeriod, csrfToken: string) {
  return useQuery({
    queryKey: ['benchmark', period, sites.map((site) => site.id).join(',')],
    enabled: sites.length > 0,
    queryFn: async ({ signal }) => Promise.all(sites.map(async (site) => {
      const { from, to } = periodWindow(period, new Date(), site.timezone);
      const read = (energyType: 'electricity' | 'cooling') => client.queryEnergySeries({
        tenantId: site.tenantId, siteId: site.id, energyType, granularity: 'day', timezone: site.timezone,
        from: from.toISOString(), to: to.toISOString(), qualityPolicy: 'VALID_ONLY',
      }, csrfToken, { signal }).then((response) => response.data);
      try {
        const [electricity, cooling] = await Promise.all([read('electricity'), read('cooling')]);
        return { siteId: site.id, name: site.displayName, summary: summarizeEnergy(electricity, cooling, 'day', site.timezone) };
      } catch (error) {
        // A Site the principal may see but not analyze stays listed, outside the comparison.
        if (error instanceof PlatformApiError && error.problem.status === 403) return { siteId: site.id, name: site.displayName, summary: null };
        throw error;
      }
    })),
    refetchInterval: 5 * 60_000,
    retry: false,
  });
}

export function BenchmarkWorkspace({ snapshot }: { readonly snapshot: ShellSnapshot }) {
  const search = useSearch({ from: '/_app/benchmark' });
  const navigate = useNavigate({ from: '/benchmark' });
  const period: BenchmarkPeriod = search.period ?? '7d';
  const sites = useMemo(() => [...(snapshot.sites?.items ?? [])].filter((site) => site.status === 'ACTIVE'), [snapshot.sites?.items]);
  const summaries = useSiteSummaries(sites, period, snapshot.principal!.session.csrfToken);
  const result = useMemo(
    () => (summaries.data ? benchmark(summaries.data.flatMap((site) => (site.summary ? [{ ...site, summary: site.summary }] : []))) : null),
    [summaries.data],
  );
  const denied = summaries.data?.filter((site) => site.summary === null) ?? [];
  const colorOf = (siteId: string) => SITE_COLORS[Math.max(0, sites.findIndex((site) => site.id === siteId)) % SITE_COLORS.length];
  const chartConfig = useMemo(
    () => Object.fromEntries(sites.map((site, index) => [site.id, { label: site.displayName, color: SITE_COLORS[index % SITE_COLORS.length] }])) satisfies ChartConfig,
    [sites],
  );
  const ranked = result?.sites.filter((site) => site.cop !== null) ?? [];
  const best = ranked[0];
  const worst = ranked.at(-1);
  const maxCop = Math.max(...ranked.map((site) => site.cop ?? 0), 0);

  return (
    <Main>
      <PageHeader
        title="站点对标"
        description="各授权站点的空调用电、供冷量与冷站综合能效对比"
        meta={<PageMeta icon={<Trophy />}>按冷站综合能效排名</PageMeta>}
        actions={<PeriodTabs aria-label="对比期间" options={PERIODS} value={period} onValueChange={(value) => void navigate({ search: { period: value as BenchmarkPeriod }, replace: true })} />}
      />

      {summaries.isPending || !result ? (
        summaries.isError ? <Card><CardContent className="py-16 text-center text-sm text-muted-foreground">站点能耗暂不可用</CardContent></Card> : <Skeleton className="h-[640px] rounded-xl" />
      ) : (
        <>
          <MetricGrid ariaLabel="对标概况">
            <MetricCard
              label="对标站点"
              value={<MetricValue value={String(result.sites.length + denied.length)} unit="个" />}
              lead={`${ranked.length} 个站点有完整的电量与冷量${denied.length ? `，${denied.length} 个无权限` : ''}`}
              detail="当前账号授权的站点"
            />
            <MetricCard
              label="空调总用电"
              value={<MetricValue value={result.fleet.electricityKWh === null ? null : formatDecimal(result.fleet.electricityKWh, 0)} unit="kWh" />}
              lead={`供冷 ${energy(result.fleet.coolingKWh)}`}
              detail="各站点合计"
            />
            <MetricCard
              label="整体综合能效"
              value={<MetricValue value={result.fleet.cop === null ? null : formatDecimal(result.fleet.cop, 2)} unit="COP" />}
              lead="按各站点电量加权"
              detail="总供冷量 ÷ 总用电"
            />
            <MetricCard
              label="能效差距"
              value={<MetricValue value={best && worst && best !== worst ? formatDecimal((best.cop ?? 0) - (worst.cop ?? 0), 2) : null} unit="COP" />}
              lead={best && worst && best !== worst ? `最好 ${best.name} · 最差 ${worst.name}` : '至少两个站点有能效数据才可比较'}
              detail="最好与最差站点之差"
            />
          </MetricGrid>

          <Card role="region" aria-label="站点排名" className="pb-0">
            <CardHeader>
              <CardTitle>站点排名</CardTitle>
              <CardDescription>综合能效越高，同样的冷量用电越少；与整体相比按电量加权的整体能效计算</CardDescription>
            </CardHeader>
            <CardContent className="border-t px-0">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead className="w-16">排名</TableHead>
                    <TableHead>站点</TableHead>
                    <TableHead className="w-[28%]">综合能效（COP）</TableHead>
                    <TableHead className="w-28">与整体相比</TableHead>
                    <TableHead className="w-32 text-right">空调用电</TableHead>
                    <TableHead className="w-32 text-right">日均用电</TableHead>
                    <TableHead className="w-32 text-right">供冷量</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {result.sites.map((site) => (
                    <TableRow key={site.siteId}>
                      <TableCell>
                        {site.rank === null ? <span className="text-muted-foreground">—</span> : (
                          <span className={cn('inline-flex size-6 items-center justify-center rounded-md text-xs font-semibold tabular-nums', site.rank === 1 ? 'bg-primary/10 text-primary' : 'bg-muted text-muted-foreground')}>{site.rank}</span>
                        )}
                      </TableCell>
                      <TableCell>
                        <span className="flex items-center gap-2 font-medium">
                          <span className="size-2 rounded-[2px]" style={{ background: colorOf(site.siteId) }} aria-hidden="true" />
                          {site.name}
                        </span>
                      </TableCell>
                      <TableCell>
                        {site.cop === null ? <span className="text-sm text-muted-foreground">缺少电量或冷量</span> : (
                          <div className="flex items-center gap-3">
                            <span className="w-10 font-medium tabular-nums">{formatDecimal(site.cop, 2)}</span>
                            <span className="h-1.5 flex-1 overflow-hidden rounded-full bg-muted">
                              <span className="block h-full rounded-full" style={{ width: `${maxCop > 0 ? (site.cop / maxCop) * 100 : 0}%`, background: colorOf(site.siteId) }} />
                            </span>
                          </div>
                        )}
                      </TableCell>
                      <TableCell>{site.copVsFleet === null ? <span className="text-muted-foreground">—</span> : <DeltaBadge delta={site.copVsFleet} variant="soft" showIcon={false} />}</TableCell>
                      <TableCell className="text-right tabular-nums">{energy(site.electricityKWh)}</TableCell>
                      <TableCell className="text-right tabular-nums">{energy(site.dailyElectricityKWh)}</TableCell>
                      <TableCell className="text-right tabular-nums">{energy(site.coolingKWh)}</TableCell>
                    </TableRow>
                  ))}
                  {denied.map((site) => (
                    <TableRow key={site.siteId} className="text-muted-foreground">
                      <TableCell>—</TableCell>
                      <TableCell>
                        <span className="flex items-center gap-2 font-medium">
                          <span className="size-2 rounded-[2px] bg-muted-foreground/40" aria-hidden="true" />
                          {site.name}
                        </span>
                      </TableCell>
                      <TableCell colSpan={5}>没有该站点的能耗分析权限，未参与对比</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </CardContent>
          </Card>

          <div className="grid gap-6 @5xl/main:grid-cols-[minmax(0,2fr)_minmax(0,1fr)]">
            <Card role="region" aria-label="每日综合能效">
              <CardHeader>
                <CardTitle>每日综合能效</CardTitle>
                <CardDescription>各站点每天的供冷量 ÷ 空调用电</CardDescription>
              </CardHeader>
              <CardContent>
                <ChartContainer config={chartConfig} className="aspect-auto h-[280px] w-full">
                  <LineChart data={[...result.daily]} margin={{ left: 4, right: 12, top: 8 }}>
                    <CartesianGrid vertical={false} />
                    <XAxis dataKey="label" tickLine={false} axisLine={false} tickMargin={8} minTickGap={16} tickFormatter={(label: string) => label.split(' ')[0]} />
                    <YAxis tickLine={false} axisLine={false} width={36} domain={['auto', 'auto']} />
                    <ChartTooltip content={<ChartTooltipContent indicator="dot" />} />
                    {sites.map((site) => (
                      <Line key={site.id} dataKey={site.id} stroke={`var(--color-${site.id})`} strokeWidth={2} dot={{ r: 3, strokeWidth: 0, fill: `var(--color-${site.id})` }} connectNulls isAnimationActive={false} />
                    ))}
                    <ChartLegend content={<ChartLegendContent />} />
                  </LineChart>
                </ChartContainer>
              </CardContent>
            </Card>

            <Card role="region" aria-label="更多对标维度">
              <CardHeader>
                <CardTitle>更多对标维度</CardTitle>
                <CardDescription>需要站点档案或外部基准，接入后在此对比</CardDescription>
              </CardHeader>
              <CardContent>
                <FactList>
                  <FactRow label="单位面积能耗" detail="需要站点建筑面积"><Badge variant="outline" className="font-normal">未接入</Badge></FactRow>
                  <FactRow label="气候修正" detail="需要站点所在气候区"><Badge variant="outline" className="font-normal">未接入</Badge></FactRow>
                  <FactRow label="行业基准" detail="需要可信的同类建筑基准数据"><Badge variant="outline" className="font-normal">未接入</Badge></FactRow>
                </FactList>
              </CardContent>
            </Card>
          </div>
        </>
      )}
    </Main>
  );
}
