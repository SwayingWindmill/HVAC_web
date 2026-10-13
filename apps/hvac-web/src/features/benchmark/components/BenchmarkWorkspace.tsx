import { useQuery } from '@tanstack/react-query';
import { useNavigate, useSearch } from '@tanstack/react-router';
import { Trophy } from 'lucide-react';
import { useMemo } from 'react';
import { CartesianGrid, Line, LineChart, XAxis, YAxis } from 'recharts';
import { createPlatformGatewayClient, PlatformApiError, type Site } from '@/api/generated/platformGateway.gen';
import { createS2TelemetryClient } from '@/api/generated/s2Telemetry.gen';
import type { ShellSnapshot } from '@/app/shell-runtime';
import { FactList, FactRow } from '@/blocks/fact-list';
import { MetricCard, MetricGrid, MetricValue } from '@/blocks/metric-card';
import { PageHeader, PageMeta } from '@/blocks/page-header';
import { Main } from '@/components/layout/Main';
import { Badge } from '@/components/ui/badge';
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { ChartContainer, ChartLegend, ChartLegendContent, ChartTooltip, ChartTooltipContent, type ChartConfig } from '@/components/ui/chart';
import { DeltaBadge } from '@/components/ui/delta-badge';
import { PeriodTabs } from '@/components/ui/period-tabs';
import { Skeleton } from '@/components/ui/skeleton';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { periodWindow, summarizeEnergy } from '@/features/energy-analysis/consumption/model';
import { CLIMATE_ZONE_LABEL } from '@/lib/climate-zone';
import { formatDecimal } from '@/lib/operator-format';
import { cn } from '@/lib/utils';
import { benchmark, DEGREE_DAY_BASE_C, type BenchmarkSite } from '../benchmark-model';

const client = createPlatformGatewayClient();
const telemetry = createS2TelemetryClient();
type BenchmarkPeriod = '7d' | 'month';
const PERIODS = [{ value: '7d', label: '近 7 天' }, { value: 'month', label: '本月' }];
type RankBy = 'cop' | 'intensity' | 'climate';
// Each ranking: its tab, the page's caption, the ranking card's note and the site's place in it.
const RANKINGS: Record<RankBy, { label: string; title: string; note: string; place: (site: BenchmarkSite) => number | null }> = {
  cop: { label: '按综合能效', title: '按冷站综合能效排名', note: '综合能效越高，同样的冷量用电越少；与整体相比按电量加权的整体能效计算', place: (site) => site.rank },
  intensity: { label: '按单位面积用电', title: '按单位面积用电排名', note: '期间空调用电 ÷ 建筑面积，越低越好；没有登记建筑面积的站点不参与此排名', place: (site) => site.intensityRank },
  climate: {
    label: '按度日修正用电',
    title: '按度日修正用电排名',
    note: `期间空调用电 ÷ 建筑面积 ÷ 冷却度日（日均室外温度高于 ${DEGREE_DAY_BASE_C} °C 的度数之和），越低越好；只算有气温记录的日子`,
    place: (site) => site.climateRank,
  },
};
const RANKING_TABS = Object.entries(RANKINGS).map(([value, ranking]) => ({ value, label: ranking.label }));
const DRY_BULB_SOURCE_KEY = 'weather.ambient_dry_bulb_temperature';
const SITE_COLORS = ['var(--chart-1)', 'var(--chart-3)', 'var(--chart-2)', 'var(--chart-4)', 'var(--chart-5)'];

const energy = (value: number | null) => (value === null ? '—' : value >= 10_000 ? `${formatDecimal(value / 1000, 2)} MWh` : `${formatDecimal(value, 0)} kWh`);

/** Each authorized Site's daily electricity and cooling over the period, on its own clock. */
function useSiteSummaries(sites: readonly Site[], period: BenchmarkPeriod, csrfToken: string) {
  return useQuery({
    queryKey: ['benchmark', period, sites.map((site) => site.id).join(',')],
    enabled: sites.length > 0,
    queryFn: async ({ signal }) => Promise.all(sites.map(async (site) => {
      const profile = { siteId: site.id, name: site.displayName, floorAreaM2: site.floorAreaM2, climateZone: site.climateZone };
      const { from, to } = periodWindow(period, new Date(), site.timezone);
      const read = (energyType: 'electricity' | 'cooling') => client.queryEnergySeries({
        tenantId: site.tenantId, siteId: site.id, energyType, granularity: 'day', timezone: site.timezone,
        from: from.toISOString(), to: to.toISOString(), qualityPolicy: 'VALID_ONLY',
      }, csrfToken, { signal }).then((response) => response.data);
      try {
        const [electricity, cooling] = await Promise.all([read('electricity'), read('cooling')]);
        return { ...profile, summary: summarizeEnergy(electricity, cooling, 'day', site.timezone) };
      } catch (error) {
        // A Site the principal may see but not analyze stays listed, outside the comparison.
        if (error instanceof PlatformApiError && error.problem.status === 403) return { ...profile, summary: null };
        throw error;
      }
    })),
    refetchInterval: 5 * 60_000,
    retry: false,
  });
}

/**
 * Each Site's daily mean outdoor temperature at its weather station over the period: null for a Site
 * without one, 'unavailable' when its temperature could not be read, which leaves the other Sites compared.
 */
function useOutdoorMeans(sites: readonly Site[], period: BenchmarkPeriod, csrfToken: string) {
  return useQuery({
    queryKey: ['benchmark', 'outdoor', period, sites.map((site) => site.id).join(',')],
    enabled: sites.length > 0,
    queryFn: async ({ signal }) => new Map(await Promise.all(sites.map(async (site): Promise<readonly [string, ReadonlyMap<string, number> | null | 'unavailable']> => {
      try {
        const model = (await client.getSiteAssetModel(site.id, { signal })).data;
        const point = model.telemetryPoints.find((candidate) => candidate.sourceKey === DRY_BULB_SOURCE_KEY && candidate.status === 'ACTIVE');
        if (!point) return [site.id, null];
        const { from, to } = periodWindow(period, new Date(), site.timezone);
        const response = await telemetry.queryDeviceHistoryAggregate({
          deviceId: point.reportingDeviceId, keys: [point.pointCode], from: from.toISOString(), to: to.toISOString(),
          granularity: 'DAY', timezone: site.timezone, qualityPolicy: 'USABLE',
        }, { csrfToken, signal });
        return [site.id, new Map(response.buckets.flatMap((bucket) => (bucket.gauge ? [[new Date(bucket.periodStart).toISOString(), bucket.gauge.average] as const] : [])))];
      } catch (error) {
        if (signal.aborted) throw error;
        return [site.id, 'unavailable'];
      }
    }))),
    refetchInterval: 5 * 60_000,
    retry: false,
  });
}

export function BenchmarkWorkspace({ snapshot }: { readonly snapshot: ShellSnapshot }) {
  const search = useSearch({ from: '/_app/benchmark' });
  const navigate = useNavigate({ from: '/benchmark' });
  const period: BenchmarkPeriod = search.period ?? '7d';
  const rankBy: RankBy = search.rank ?? 'cop';
  const sites = useMemo(() => [...(snapshot.sites?.items ?? [])].filter((site) => site.status === 'ACTIVE'), [snapshot.sites?.items]);
  const summaries = useSiteSummaries(sites, period, snapshot.principal!.session.csrfToken);
  const outdoor = useOutdoorMeans(sites, period, snapshot.principal!.session.csrfToken);
  const result = useMemo(
    () => (summaries.data
      ? benchmark(summaries.data.flatMap((site) => {
        const outdoorMeanC = outdoor.data?.get(site.siteId);
        return site.summary ? [{ ...site, outdoorMeanC: outdoorMeanC instanceof Map ? outdoorMeanC : null, summary: site.summary }] : [];
      }))
      : null),
    [summaries.data, outdoor.data],
  );
  const denied = summaries.data?.filter((site) => site.summary === null) ?? [];
  const colorOf = (siteId: string) => SITE_COLORS[Math.max(0, sites.findIndex((site) => site.id === siteId)) % SITE_COLORS.length];
  const analyzed = useMemo(() => sites.filter((site) => result?.sites.some((entry) => entry.siteId === site.id)), [sites, result]);
  const chartConfig = useMemo(
    () => Object.fromEntries(analyzed.map((site) => [site.id, { label: site.displayName, color: colorOf(site.id) }])) satisfies ChartConfig,
    [analyzed, sites],
  );
  const ranked = result?.sites.filter((site) => site.cop !== null) ?? [];
  // Sites with a place in the chosen ranking come first in its order; the rest keep the COP order.
  const listed = result
    ? (rankBy === 'cop' ? result.sites : [...result.sites].sort((left, right) => (RANKINGS[rankBy].place(left) ?? Infinity) - (RANKINGS[rankBy].place(right) ?? Infinity)))
    : [];
  const withArea = result?.sites.filter((site) => site.floorAreaM2 !== null).length ?? 0;
  const stationOf = (siteId: string) => outdoor.data?.get(siteId);
  const withStation = result?.sites.filter((site) => stationOf(site.siteId) instanceof Map).length ?? 0;
  const unreadStations = result?.sites.filter((site) => stationOf(site.siteId) === 'unavailable').length ?? 0;
  const climateCell = (site: BenchmarkSite) => {
    if (!outdoor.data) return <span className="text-muted-foreground">—</span>;
    if (stationOf(site.siteId) === 'unavailable') return <span className="text-muted-foreground">气温暂不可用</span>;
    const reason = site.degreeDays === null ? '未接入气象站' : site.floorAreaM2 === null ? '未登记建筑面积' : site.degreeDays === 0 ? '无供冷度日' : null;
    return (
      <>
        {reason ? <span className="text-muted-foreground">{reason}</span> : formatDecimal(site.climateIntensity!, 4)}
        {site.degreeDays !== null ? <span className="block text-xs text-muted-foreground">度日数 {formatDecimal(site.degreeDays, 1)} ℃·d</span> : null}
      </>
    );
  };
  const best = ranked[0];
  const worst = ranked.at(-1);
  const maxCop = Math.max(...ranked.map((site) => site.cop ?? 0), 0);

  return (
    <Main>
      <PageHeader
        title="站点对标"
        description="各授权站点的空调用电、供冷量与冷站综合能效对比"
        meta={<PageMeta icon={<Trophy />}>{RANKINGS[rankBy].title}</PageMeta>}
        actions={<PeriodTabs aria-label="对比期间" options={PERIODS} value={period} onValueChange={(value) => void navigate({ search: (previous) => ({ ...previous, period: value as BenchmarkPeriod }), replace: true })} />}
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
              <CardDescription>{RANKINGS[rankBy].note}</CardDescription>
              <CardAction>
                <PeriodTabs aria-label="排名依据" options={RANKING_TABS} value={rankBy} onValueChange={(value) => void navigate({ search: (previous) => ({ ...previous, rank: value as RankBy }), replace: true })} />
              </CardAction>
            </CardHeader>
            <CardContent className="border-t px-0">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead className="w-16">排名</TableHead>
                    <TableHead>站点</TableHead>
                    <TableHead className="w-[16%]">综合能效（COP）</TableHead>
                    <TableHead className="w-28">与整体相比</TableHead>
                    <TableHead className="w-32 text-right">空调用电</TableHead>
                    <TableHead className="w-36 text-right">单位面积用电</TableHead>
                    <TableHead className="w-44 text-right">单位度日用电（kWh/m²·℃·d）</TableHead>
                    <TableHead className="w-32 text-right">日均用电</TableHead>
                    <TableHead className="w-32 text-right">供冷量</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {listed.map((site) => {
                    const place = RANKINGS[rankBy].place(site);
                    return (
                    <TableRow key={site.siteId}>
                      <TableCell>
                        {place === null ? <span className="text-muted-foreground">—</span> : (
                          <span className={cn('inline-flex size-6 items-center justify-center rounded-md text-xs font-semibold tabular-nums', place === 1 ? 'bg-primary/10 text-primary' : 'bg-muted text-muted-foreground')}>{place}</span>
                        )}
                      </TableCell>
                      <TableCell>
                        <span className="flex items-center gap-2 font-medium whitespace-nowrap">
                          <span className="size-2 rounded-[2px]" style={{ background: colorOf(site.siteId) }} aria-hidden="true" />
                          {site.name}
                        </span>
                        <span className="block pl-4 text-xs whitespace-nowrap text-muted-foreground">
                          {site.climateZone ? CLIMATE_ZONE_LABEL[site.climateZone] : '气候区未登记'}
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
                      <TableCell className="text-right whitespace-nowrap tabular-nums">{energy(site.electricityKWh)}</TableCell>
                      <TableCell className="text-right whitespace-nowrap tabular-nums">
                        {site.intensityKWhPerM2 === null
                          ? <span className="text-muted-foreground">{site.floorAreaM2 === null ? '未登记建筑面积' : '—'}</span>
                          : `${formatDecimal(site.intensityKWhPerM2, 2)} kWh/m²`}
                        {site.floorAreaM2 !== null ? <span className="block text-xs text-muted-foreground">建筑面积 {formatDecimal(site.floorAreaM2, 0)} m²</span> : null}
                      </TableCell>
                      <TableCell className="text-right whitespace-nowrap tabular-nums">{climateCell(site)}</TableCell>
                      <TableCell className="text-right whitespace-nowrap tabular-nums">{energy(site.dailyElectricityKWh)}</TableCell>
                      <TableCell className="text-right whitespace-nowrap tabular-nums">{energy(site.coolingKWh)}</TableCell>
                    </TableRow>
                    );
                  })}
                  {denied.map((site) => (
                    <TableRow key={site.siteId} className="text-muted-foreground">
                      <TableCell>—</TableCell>
                      <TableCell>
                        <span className="flex items-center gap-2 font-medium">
                          <span className="size-2 rounded-[2px] bg-muted-foreground/40" aria-hidden="true" />
                          {site.name}
                        </span>
                      </TableCell>
                      <TableCell colSpan={7}>没有该站点的能耗分析权限，未参与对比</TableCell>
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
                    <YAxis tickLine={false} axisLine={false} width={40} domain={['auto', 'auto']} tickFormatter={(value: number) => formatDecimal(value, 2)} />
                    <ChartTooltip content={<ChartTooltipContent indicator="dot" />} />
                    {analyzed.map((site) => (
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
                <CardDescription>站点档案在系统管理的注册中心里维护；外部数据接入后在此对比</CardDescription>
              </CardHeader>
              <CardContent>
                <FactList>
                  <FactRow label="单位面积用电" detail={`${withArea} / ${result.sites.length} 个站点登记了建筑面积`}><Badge variant="secondary" className="font-normal">已接入</Badge></FactRow>
                  {outdoor.data ? (
                    <FactRow
                      label="气候修正"
                      detail={`${withStation} / ${result.sites.length} 个站点有气象站，按冷却度日（CDD${DEGREE_DAY_BASE_C}）修正${unreadStations ? `；${unreadStations} 个站点气温暂不可用` : ''}`}
                    >
                      <Badge variant={withStation ? 'secondary' : 'outline'} className="font-normal">{withStation ? '已接入' : '未接入'}</Badge>
                    </FactRow>
                  ) : (
                    <FactRow label="气候修正" detail="正在读取各站点的气象站"><Skeleton className="h-5 w-12" /></FactRow>
                  )}
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
