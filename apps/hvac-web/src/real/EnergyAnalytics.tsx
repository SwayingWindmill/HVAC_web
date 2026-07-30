import { useEffect, useMemo, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import ReactECharts from 'echarts-for-react';
import type { CurrentPrincipalResponse, Site } from '@/api/generated/platformGateway.gen';
import {
  buildEnergyTrendData,
  classifyEnergyAnalyticsFailure,
  energySeriesQueryKey,
  energySeriesRevisionKey,
  energyTotal,
  hasStaleWatermark,
  queryEnergySeries,
  type EnergyGranularity,
  type EnergyQualityPolicy,
  type EnergySeriesQuery,
} from '@/api/energy-analytics';
import { FocusHeading } from './FocusHeading';
import './energy-analytics.css';

interface EnergyAnalyticsProps {
  site: Readonly<Site>;
  principal: CurrentPrincipalResponse;
}

type RangePreset = '24h' | '30d' | '12m';

const RANGE_PRESETS: Record<RangePreset, { label: string; granularity: EnergyGranularity; milliseconds: number }> = {
  '24h': { label: '最近 24 小时', granularity: 'hour', milliseconds: 24 * 60 * 60 * 1000 },
  '30d': { label: '最近 30 天', granularity: 'day', milliseconds: 30 * 24 * 60 * 60 * 1000 },
  '12m': { label: '最近 12 个月', granularity: 'month', milliseconds: 365 * 24 * 60 * 60 * 1000 },
};

const QUALITY_LABELS: Record<EnergyQualityPolicy, string> = {
  VALID_ONLY: '仅有效数据',
  VALID_AND_SUSPECT: '包含可疑数据',
};

function formatInstant(value: string | undefined, timezone: string): string {
  if (!value) return '未提供';
  return new Intl.DateTimeFormat('zh-CN', {
    timeZone: timezone,
    dateStyle: 'medium',
    timeStyle: 'short',
  }).format(new Date(value));
}

function formatEnergy(value: number | null): string {
  if (value === null) return '无数据';
  return `${new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 2 }).format(value)} kWh`;
}

export function EnergyAnalytics({ site, principal }: EnergyAnalyticsProps) {
  const queryClient = useQueryClient();
  const [rangePreset, setRangePreset] = useState<RangePreset>('30d');
  const [qualityPolicy, setQualityPolicy] = useState<EnergyQualityPolicy>('VALID_ONLY');
  const [asOf, setAsOf] = useState(() => Date.now());
  const range = RANGE_PRESETS[rangePreset];
  const query = useMemo<EnergySeriesQuery>(() => ({
    organizationId: principal.context.actingOrganizationId,
    siteId: site.id,
    energyType: 'electricity',
    granularity: range.granularity,
    timezone: site.timezone,
    from: new Date(asOf - range.milliseconds).toISOString(),
    to: new Date(asOf).toISOString(),
    qualityPolicy,
  }), [asOf, principal.context.actingOrganizationId, qualityPolicy, range.granularity, range.milliseconds, site.id, site.timezone]);

  const sessionCapability = principal.session.csrfToken;
  const result = useQuery({
    queryKey: energySeriesQueryKey(query),
    queryFn: ({ signal }) => queryEnergySeries(query, {
      csrfToken: sessionCapability,
      trustedOrganizationId: principal.context.actingOrganizationId,
      signal,
    }),
    staleTime: 60_000,
    retry: (failureCount, error) => failureCount < 1 && classifyEnergyAnalyticsFailure(error).retryable,
  });

  useEffect(() => {
    if (!result.data) return;
    queryClient.setQueryData(
      energySeriesRevisionKey(query, result.data.metadata.datasetRevision),
      result.data,
    );
  }, [query, queryClient, result.data]);

  const trendData = useMemo(
    () => buildEnergyTrendData(result.data?.points ?? [], result.data?.metadata.actualGranularity ?? query.granularity),
    [query.granularity, result.data],
  );
  const total = result.data ? energyTotal(result.data.points) : null;
  const staleWatermark = result.data ? hasStaleWatermark(result.data, query) : false;
  const quality = result.data?.metadata.qualitySummary ?? { valid: 0, suspect: 0, invalid: 0 };
  const hasSuspectData = quality.suspect > 0;

  const chartOption = useMemo(() => ({
    animation: false,
    grid: { left: 56, right: 24, top: 28, bottom: 48 },
    tooltip: {
      trigger: 'axis',
      valueFormatter: (value: number | null) => value === null ? '数据缺口' : `${value} kWh`,
    },
    xAxis: {
      type: 'time',
      name: site.timezone,
      nameLocation: 'middle',
      nameGap: 34,
      axisLabel: { hideOverlap: true },
    },
    yAxis: { type: 'value', name: 'kWh', min: 0 },
    series: [{
      name: '电能',
      type: 'line',
      showSymbol: trendData.length < 80,
      connectNulls: false,
      data: trendData,
      areaStyle: { opacity: 0.08 },
      emphasis: { focus: 'series' },
    }],
  }), [site.timezone, trendData]);

  if (result.isPending) {
    return (
      <section className="real-energy" data-testid="real-energy-loading" data-business-state="LOADING">
        <p className="real-shell-eyebrow">REAL MODE · SITE ENERGY</p>
        <FocusHeading>能源分析</FocusHeading>
        <div className="real-shell-progress" role="status" aria-live="polite">正在读取权威能源数据…</div>
      </section>
    );
  }

  if (result.isError) {
    const failure = classifyEnergyAnalyticsFailure(result.error);
    return (
      <section className="real-energy" data-testid="real-energy-error" data-business-state={failure.kind.toUpperCase()}>
        <p className="real-shell-eyebrow">REAL MODE · SITE ENERGY</p>
        <FocusHeading>{failure.title}</FocusHeading>
        <div className="real-shell-problem" role="alert" data-retryable={String(failure.retryable)}>
          <span>{failure.detail}</span>
          {failure.traceId ? <code>traceId {failure.traceId}</code> : null}
        </div>
        {failure.retryable ? (
          <div className="real-shell-actions">
            <button type="button" onClick={() => { void result.refetch(); }}>重试能源查询</button>
          </div>
        ) : null}
      </section>
    );
  }

  const response = result.data;
  const businessState = response.points.length === 0
    ? 'EMPTY'
    : response.metadata.partial
      ? 'PARTIAL'
      : staleWatermark
        ? 'STALE'
        : hasSuspectData
          ? 'SUSPECT'
          : 'READY';

  return (
    <section
      className="real-energy"
      data-testid="real-energy-dashboard"
      data-business-state={businessState}
      data-site-id={site.id}
      data-dataset-revision={response.metadata.datasetRevision}
    >
      <header className="real-energy__header">
        <div>
          <p className="real-shell-eyebrow">REAL MODE · SITE ENERGY</p>
          <FocusHeading>能源分析</FocusHeading>
          <p>{site.displayName} · {site.code} · {site.timezone}</p>
        </div>
        <button type="button" onClick={() => setAsOf(Date.now())} disabled={result.isFetching}>
          {result.isFetching ? '刷新中…' : '刷新数据'}
        </button>
      </header>

      <form className="real-energy__controls" aria-label="能源查询条件" onSubmit={(event) => event.preventDefault()}>
        <fieldset>
          <legend>时间范围</legend>
          {Object.entries(RANGE_PRESETS).map(([value, preset]) => (
            <label key={value}>
              <input
                type="radio"
                name="energy-range"
                value={value}
                checked={rangePreset === value}
                onChange={() => {
                  setRangePreset(value as RangePreset);
                  setAsOf(Date.now());
                }}
              />
              {preset.label}
            </label>
          ))}
        </fieldset>
        <label>
          数据质量口径
          <select
            value={qualityPolicy}
            onChange={(event) => setQualityPolicy(event.currentTarget.value as EnergyQualityPolicy)}
          >
            {Object.entries(QUALITY_LABELS).map(([value, label]) => (
              <option key={value} value={value}>{label}</option>
            ))}
          </select>
        </label>
      </form>

      <div className="real-energy__notices" aria-live="polite">
        {response.metadata.partial ? <p role="status">当前结果为部分数据，不能视为完整周期总量。</p> : null}
        {staleWatermark ? <p role="status">聚合水位滞后于查询结束时间，最新时段可能尚未完成汇总。</p> : null}
        {hasSuspectData ? <p role="status">结果包含可疑质量记录，请结合质量摘要解释趋势。</p> : null}
        {response.metadata.actualGranularity !== response.metadata.requestedGranularity ? (
          <p role="status">服务按 {response.metadata.actualGranularity} 粒度返回，区别于请求粒度。</p>
        ) : null}
      </div>

      <dl className="real-energy__metrics" aria-label="能源分析关键指标">
        <div><dt>周期总电能</dt><dd>{formatEnergy(total)}</dd><small>{response.points.length} 个已返回时段</small></div>
        <div><dt>有效记录</dt><dd>{quality.valid}</dd><small>质量策略：{QUALITY_LABELS[qualityPolicy]}</small></div>
        <div><dt>可疑 / 无效</dt><dd>{quality.suspect} / {quality.invalid}</dd><small>未将缺失时段按零处理</small></div>
        <div><dt>数据集修订</dt><dd className="real-energy__revision">{response.metadata.datasetRevision}</dd><small>用于识别聚合重建或修订</small></div>
      </dl>

      <article className="real-energy__chart" aria-labelledby="energy-trend-title">
        <div className="real-energy__panel-heading">
          <div>
            <h2 id="energy-trend-title">电能趋势</h2>
            <p>折线中的断点代表未知或缺失数据，不代表 0 kWh。</p>
          </div>
          <span>{range.label}</span>
        </div>
        {response.points.length === 0 ? (
          <div className="real-energy__empty" role="status">
            当前授权范围内没有返回能源时段。此状态不代表 0 kWh，也不代表权限拒绝。
          </div>
        ) : (
          <ReactECharts option={chartOption} style={{ height: 360 }} notMerge lazyUpdate />
        )}
      </article>

      <dl className="real-energy__provenance" aria-label="能源数据新鲜度与修订信息">
        <div><dt>数据水位</dt><dd>{formatInstant(response.metadata.dataWatermark, site.timezone)}</dd></div>
        <div><dt>聚合水位</dt><dd>{formatInstant(response.metadata.aggregateWatermark, site.timezone)}</dd></div>
        <div><dt>请求范围</dt><dd>{formatInstant(query.from, site.timezone)} — {formatInstant(query.to, site.timezone)}</dd></div>
        <div><dt>返回粒度</dt><dd>{response.metadata.actualGranularity}</dd></div>
      </dl>
    </section>
  );
}
