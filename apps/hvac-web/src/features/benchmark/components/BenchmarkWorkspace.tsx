import { useQuery } from '@tanstack/react-query';
import { useNavigate, useSearch } from '@tanstack/react-router';
import { Download } from 'lucide-react';
import { useMemo, type ReactNode } from 'react';
import { Line, LineChart, ReferenceLine, YAxis } from 'recharts';
import { createPlatformGatewayClient, PlatformApiError, type Site } from '@/api/generated/platformGateway.gen';
import { createS2TelemetryClient } from '@/api/generated/s2Telemetry.gen';
import type { ShellSnapshot } from '@/app/shell-runtime';
import { PageHeader } from '@/blocks/page-header';
import { Panel, PanelHeader } from '@/blocks/panel';
import { InfoTip } from '@/components/info-tip';
import { Main } from '@/components/layout/Main';
import { Button } from '@/components/ui/button';
import { ChartContainer, ChartTooltip, ChartTooltipContent, type ChartConfig } from '@/components/ui/chart';
import { DeltaBadge } from '@/components/ui/delta-badge';
import { PeriodTabs } from '@/components/ui/period-tabs';
import { Skeleton } from '@/components/ui/skeleton';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { periodWindow, summarizeEnergy, wallTime } from '@/features/energy-analysis/consumption/model';
import { CLIMATE_ZONE_LABEL } from '@/lib/climate-zone';
import { formatDecimal } from '@/lib/operator-format';
import { cn } from '@/lib/utils';
import { benchmark, DEGREE_DAY_BASE_C, type BenchmarkSite } from '../benchmark-model';

const client = createPlatformGatewayClient();
const telemetry = createS2TelemetryClient();
type BenchmarkPeriod = '7d' | 'month';
const PERIODS = [{ value: '7d', label: '近 7 天' }, { value: 'month', label: '本月' }];
type RankBy = 'cop' | 'intensity' | 'climate';
// Each ranking: its tab, the panel's caption, its note and the site's place in it.
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

function Figure({ label, value, unit, info, children }: { readonly label: string; readonly value: string | null; readonly unit?: string; readonly info?: string; readonly children: ReactNode }) {
  return (
    <div className="flex min-w-0 flex-col gap-1 px-5 py-4">
      <span className="flex items-center gap-1 text-xs font-medium text-muted-foreground">{label}{info ? <InfoTip>{info}</InfoTip> : null}</span>
      <span className="text-2xl leading-[29px] font-semibold tracking-[-0.03em] tabular-nums">
        {value ?? '—'}
        {value !== null && unit ? <span className="ml-1 text-xs font-medium tracking-normal text-subtle-foreground">{unit}</span> : null}
      </span>
      <span className="truncate text-xs text-muted-foreground">{children}</span>
    </div>
  );
}

/** One row of what the page can and cannot compare yet. */
function Dimension({ label, detail, connected }: { readonly label: string; readonly detail: ReactNode; readonly connected: boolean | null }) {
  return (
    <div className="flex items-center justify-between gap-4 border-b border-divider px-5 py-3 last:border-b-0">
      <span className="flex min-w-0 flex-col gap-0.5">
        <span className="text-sm font-medium">{label}</span>
        <span className="text-xs text-muted-foreground">{detail}</span>
      </span>
      {connected === null ? <Skeleton className="h-5 w-12" /> : (
        <span className={cn('inline-flex shrink-0 items-center gap-1.5 rounded-md px-1.5 text-xs leading-5 font-medium', connected ? 'bg-success-soft text-success-text' : 'bg-secondary text-secondary-foreground')}>
          <span className={cn('size-1.5 rounded-full', connected ? 'bg-success' : 'bg-subtle-foreground')} aria-hidden="true" />
          {connected ? '已接入' : '未接入'}
        </span>
      )}
    </div>
  );
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
  const ranked = result?.sites.filter((site) => site.cop !== null) ?? [];
  // Sites with a place in the chosen ranking come first in its order; the rest follow, outside it.
  const listed = result ? [...result.sites].sort((left, right) => (RANKINGS[rankBy].place(left) ?? Infinity) - (RANKINGS[rankBy].place(right) ?? Infinity)) : [];
  const inRanking = listed.filter((site) => RANKINGS[rankBy].place(site) !== null);
  const outside = listed.filter((site) => RANKINGS[rankBy].place(site) === null);
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
  const range = (() => {
    if (sites.length === 0) return '';
    const zone = sites[0].timezone;
    const day = (instant: Date) => { const local = wallTime(instant, zone); return `${String(local.month).padStart(2, '0')}/${String(local.day).padStart(2, '0')}`; };
    return `${day(periodWindow(period, new Date(), zone).from)} – ${day(new Date())}`;
  })();
  const total = result?.fleet.electricityKWh ?? null;

  const exportCsv = () => {
    if (!result) return;
    const lines = [
      ['站点', '气候区', '综合能效 COP', '与整体相比 %', '空调用电 kWh', '供冷量 kWh', '建筑面积 m²', '单位面积用电 kWh/m²', '度日数 ℃·d', '单位度日用电 kWh/m²·℃·d', '日均用电 kWh'],
      ...listed.map((site) => [
        site.name, site.climateZone ? CLIMATE_ZONE_LABEL[site.climateZone] : '', site.cop?.toFixed(2) ?? '', site.copVsFleet === null ? '' : (site.copVsFleet * 100).toFixed(1),
        site.electricityKWh?.toFixed(1) ?? '', site.coolingKWh?.toFixed(1) ?? '', site.floorAreaM2 ?? '', site.intensityKWhPerM2?.toFixed(3) ?? '',
        site.degreeDays?.toFixed(1) ?? '', site.climateIntensity?.toFixed(5) ?? '', site.dailyElectricityKWh?.toFixed(1) ?? '',
      ]),
      ...denied.map((site) => [site.name, '没有能耗分析权限']),
    ];
    const url = URL.createObjectURL(new Blob(['﻿' + lines.map((line) => line.join(',')).join('\n')], { type: 'text/csv;charset=utf-8' }));
    const link = document.createElement('a');
    link.href = url;
    link.download = `站点对标-${PERIODS.find((option) => option.value === period)!.label}.csv`;
    link.click();
    URL.revokeObjectURL(url);
  };

  const row = (site: BenchmarkSite, place: number | null) => (
    <TableRow key={site.siteId}>
      <TableCell className="pl-5">
        {place === null ? <span className="text-muted-foreground">—</span> : (
          <span className={cn('inline-flex size-6 items-center justify-center rounded-md text-xs font-semibold tabular-nums', place === 1 ? 'bg-primary-soft text-primary' : 'bg-muted text-muted-foreground')}>{place}</span>
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
        {site.cop === null ? <span className="text-sm text-warning-text">缺少电量或冷量</span> : (
          <div className="flex items-center gap-3">
            <span className="w-10 font-semibold tabular-nums">{formatDecimal(site.cop, 2)}</span>
            <span className="h-1.5 w-20 overflow-hidden rounded-full bg-muted">
              <span className="block h-full rounded-full" style={{ width: `${maxCop > 0 ? (site.cop / maxCop) * 100 : 0}%`, background: colorOf(site.siteId) }} />
            </span>
          </div>
        )}
      </TableCell>
      <TableCell>{site.copVsFleet === null ? <span className="text-muted-foreground">—</span> : <DeltaBadge delta={site.copVsFleet} variant="text" showIcon={false} />}</TableCell>
      <TableCell className="text-right whitespace-nowrap tabular-nums">{energy(site.electricityKWh)}</TableCell>
      <TableCell className="text-right whitespace-nowrap tabular-nums">
        {site.intensityKWhPerM2 === null
          ? <span className="text-muted-foreground">{site.floorAreaM2 === null ? '未登记建筑面积' : '—'}</span>
          : `${formatDecimal(site.intensityKWhPerM2, 2)} kWh/m²`}
        {site.floorAreaM2 !== null ? <span className="block text-xs text-muted-foreground">建筑面积 {formatDecimal(site.floorAreaM2, 0)} m²</span> : null}
      </TableCell>
      <TableCell className="text-right whitespace-nowrap tabular-nums">{climateCell(site)}</TableCell>
      <TableCell className="text-right whitespace-nowrap tabular-nums">{energy(site.dailyElectricityKWh)}</TableCell>
      <TableCell className="pr-5 text-right whitespace-nowrap tabular-nums">{energy(site.coolingKWh)}</TableCell>
    </TableRow>
  );

  return (
    <Main>
      <PageHeader
        title="站点对标"
        description={`全部授权站点 · ${sites.length} 个${denied.length ? ` · ${denied.length} 个无权限` : ''}`}
        actions={(
          <>
            {range ? <span className="text-xs text-muted-foreground">{range}</span> : null}
            <PeriodTabs aria-label="对比期间" options={PERIODS} value={period} onValueChange={(value) => void navigate({ search: (previous) => ({ ...previous, period: value as BenchmarkPeriod }), replace: true })} />
            <Button variant="outline" disabled={!result} onClick={exportCsv}>
              <Download data-icon="inline-start" aria-hidden="true" />
              导出
            </Button>
          </>
        )}
      />

      {summaries.isPending || !result ? (
        summaries.isError ? (
          <Panel aria-label="对标概况">
            <div className="flex flex-col items-start gap-2 px-5 py-8">
              <p className="text-sm font-semibold">站点能耗暂时读不到</p>
              <Button variant="outline" size="sm" onClick={() => void summaries.refetch()}>重试</Button>
            </div>
          </Panel>
        ) : <Skeleton className="h-[640px] rounded-xl" />
      ) : (
        <>
          <Panel aria-label="对标概况" className="grid grid-cols-3 divide-x divide-divider">
            <Figure label="整体综合能效" info="各站点总供冷量 ÷ 总用电" value={result.fleet.cop === null ? null : formatDecimal(result.fleet.cop, 2)} unit="COP">
              按各站点电量加权 · {ranked.length} 个站点有完整的电量与冷量
            </Figure>
            <Figure
              label="空调总用电"
              value={total === null ? null : total >= 10_000 ? formatDecimal(total / 1000, 2) : formatDecimal(total, 0)}
              unit={total !== null && total >= 10_000 ? 'MWh' : 'kWh'}
            >
              供冷 {energy(result.fleet.coolingKWh)}
            </Figure>
            <Figure label="能效差距" value={best && worst && best !== worst ? formatDecimal((best.cop ?? 0) - (worst.cop ?? 0), 2) : null} unit="COP">
              {best && worst && best !== worst ? `最高 ${best.name} · 最低 ${worst.name}` : '至少两个站点有能效数据才可比较'}
            </Figure>
          </Panel>

          <Panel aria-label="站点排名">
            <PanelHeader
              title={<span className="flex items-center gap-1.5">站点排名<InfoTip>{RANKINGS[rankBy].note}</InfoTip></span>}
              caption={RANKINGS[rankBy].title}
              action={<PeriodTabs aria-label="排名依据" options={RANKING_TABS} value={rankBy} onValueChange={(value) => void navigate({ search: (previous) => ({ ...previous, rank: value as RankBy }), replace: true })} />}
            />
            {rankBy !== 'cop' ? <p className="border-b border-divider px-5 py-2 text-xs text-muted-foreground">{RANKINGS[rankBy].note}</p> : null}
            <Table>
              <TableHeader>
                <TableRow className="hover:bg-transparent">
                  <TableHead className="w-16 pl-5">排名</TableHead>
                  <TableHead>站点</TableHead>
                  <TableHead>综合能效</TableHead>
                  <TableHead>与整体相比</TableHead>
                  <TableHead className="text-right">空调用电</TableHead>
                  <TableHead className="text-right">单位面积用电</TableHead>
                  <TableHead className="text-right">单位度日用电（kWh/m²·℃·d）</TableHead>
                  <TableHead className="text-right">日均用电</TableHead>
                  <TableHead className="pr-5 text-right">供冷量</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {inRanking.map((site) => row(site, RANKINGS[rankBy].place(site)))}
                {outside.length + denied.length > 0 ? (
                  <TableRow className="hover:bg-transparent">
                    <TableCell colSpan={9} className="bg-muted px-5 py-1.5 text-xs font-medium text-muted-foreground">
                      未参与此排名 <span className="ml-1 font-mono text-subtle-foreground">{outside.length + denied.length}</span>
                    </TableCell>
                  </TableRow>
                ) : null}
                {outside.map((site) => row(site, null))}
                {denied.map((site) => (
                  <TableRow key={site.siteId} className="text-muted-foreground">
                    <TableCell className="pl-5">—</TableCell>
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
          </Panel>

          <div className="grid items-start gap-[18px] grid-cols-[minmax(0,2fr)_minmax(0,1fr)]">
            <Panel aria-label="每日综合能效">
              <PanelHeader title="每日综合能效" caption="各站点每天的供冷量 ÷ 空调用电 · 虚线为整体" />
              {ranked.length === 0 ? (
                <p className="px-5 py-10 text-center text-sm text-muted-foreground">本期间还没有站点有完整的电量与冷量</p>
              ) : (
                <div className="grid grid-cols-2 gap-3 p-4">
                  {ranked.map((site) => {
                    const values = result.daily.map((day) => day[site.siteId]).filter((value): value is number => typeof value === 'number');
                    const config = { [site.siteId]: { label: site.name, color: colorOf(site.siteId) } } satisfies ChartConfig;
                    return (
                      <div key={site.siteId} className="flex flex-col gap-1.5 rounded-lg border border-divider px-4 py-3">
                        <span className="flex items-center gap-2 text-sm">
                          <span className="size-2 rounded-[2px]" style={{ background: colorOf(site.siteId) }} aria-hidden="true" />
                          <span className="font-medium">{site.name}</span>
                          <span className="ml-auto text-xs text-muted-foreground tabular-nums">
                            {values.length ? `${formatDecimal(Math.min(...values), 2)} – ${formatDecimal(Math.max(...values), 2)}` : '—'}
                          </span>
                        </span>
                        <ChartContainer config={config} className="aspect-auto h-16 w-full">
                          <LineChart data={[...result.daily]} margin={{ left: 2, right: 2, top: 4, bottom: 4 }}>
                            <YAxis hide domain={['auto', 'auto']} />
                            <ChartTooltip content={<ChartTooltipContent indicator="dot" labelKey="label" />} />
                            {result.fleet.cop !== null ? <ReferenceLine y={result.fleet.cop} stroke="var(--muted-foreground)" strokeDasharray="3 3" /> : null}
                            <Line dataKey={site.siteId} stroke={`var(--color-${site.siteId})`} strokeWidth={2} dot={false} connectNulls isAnimationActive={false} />
                          </LineChart>
                        </ChartContainer>
                        <span className="flex justify-between text-[11px] text-muted-foreground tabular-nums">
                          <span>{String(result.daily[0]?.label ?? '').split(' ')[0]}</span>
                          <span>{String(result.daily.at(-1)?.label ?? '').split(' ')[0]}</span>
                        </span>
                      </div>
                    );
                  })}
                </div>
              )}
            </Panel>

            <Panel aria-label="更多对比项">
              <PanelHeader title={<span className="flex items-center gap-1.5">更多对比项<InfoTip>站点档案在系统管理的注册中心里维护；外部数据接入后在此对比</InfoTip></span>} />
              <Dimension label="单位面积用电" detail={`${withArea} / ${result.sites.length} 个站点登记了建筑面积`} connected />
              <Dimension
                label="气候修正"
                detail={outdoor.data
                  ? `${withStation} / ${result.sites.length} 个站点有气象站，按冷却度日（CDD${DEGREE_DAY_BASE_C}）修正${unreadStations ? `；${unreadStations} 个站点气温暂不可用` : ''}`
                  : '正在读取各站点的气象站'}
                connected={outdoor.data ? withStation > 0 : null}
              />
              <Dimension label="行业基准" detail="需要可信的同类建筑基准数据" connected={false} />
            </Panel>
          </div>
        </>
      )}
    </Main>
  );
}
