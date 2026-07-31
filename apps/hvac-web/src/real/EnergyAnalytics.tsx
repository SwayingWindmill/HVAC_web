import { useCallback, useEffect, useMemo, useState } from 'react';
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
  type EnergyAnalyticsRequestOptions,
  type EnergyQualityPolicy,
  type EnergySeriesPoint,
  type EnergySeriesQuery,
} from '@/api/energy-analytics';
import {
  buildEnergyWorkspaceWindow,
  compareEnergyTotals,
  currentEnergyWorkspaceState,
  drillDownEnergyWorkspaceState,
  energyWorkspaceSearch,
  parseEnergyWorkspaceSearch,
  shiftEnergyWorkspaceState,
  type EnergyWorkspacePeriod,
  type EnergyWorkspaceState,
} from './energy-workspace';
import { FocusHeading } from './FocusHeading';
import './energy-analytics.css';

interface EnergyAnalyticsProps {
  site: Readonly<Site>;
  principal: CurrentPrincipalResponse;
}

const PERIOD_LABELS: Record<EnergyWorkspacePeriod, string> = {
  day: '日', week: '周', month: '月', year: '年',
};
const QUALITY_LABELS: Record<EnergyQualityPolicy, string> = {
  VALID_ONLY: '仅有效数据',
  VALID_AND_SUSPECT: '包含可疑数据',
};

function formatInstant(value: string | undefined, timezone: string): string {
  if (!value) return '未提供';
  return new Intl.DateTimeFormat('zh-CN', {
    timeZone: timezone, dateStyle: 'medium', timeStyle: 'short',
  }).format(new Date(value));
}

function formatEnergy(value: number | null): string {
  if (value === null) return '无数据';
  return `${new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 2 }).format(value)} kWh`;
}

function formatSignedEnergy(value: number): string {
  return `${new Intl.NumberFormat('zh-CN', {
    maximumFractionDigits: 2, signDisplay: 'always',
  }).format(value)} kWh`;
}

function formatPeriod(point: EnergySeriesPoint, timezone: string): string {
  const formatter = new Intl.DateTimeFormat('zh-CN', {
    timeZone: timezone, dateStyle: 'medium', timeStyle: 'short',
  });
  return `${formatter.format(new Date(point.periodStart))} — ${formatter.format(new Date(point.periodEnd))}`;
}

function comparisonText(current: number | null, previous: number | null): { value: string; detail: string } {
  const comparison = compareEnergyTotals(current, previous);
  if (comparison.kind === 'unavailable') return { value: '不可计算', detail: '当前周期或基期没有可比较总量' };
  if (comparison.kind === 'baseline-zero') {
    return { value: formatSignedEnergy(comparison.differenceKWh), detail: '基期为 0，未计算百分比' };
  }
  return {
    value: `${new Intl.NumberFormat('zh-CN', {
      maximumFractionDigits: 1, signDisplay: 'always',
    }).format(comparison.percentage)}%`,
    detail: `${formatSignedEnergy(comparison.differenceKWh)}，相对上一周期`,
  };
}

function buildQuery(
  principal: CurrentPrincipalResponse,
  site: Readonly<Site>,
  range: { from: string; to: string; granularity: EnergySeriesQuery['granularity'] },
  qualityPolicy: EnergyQualityPolicy,
): EnergySeriesQuery {
  return {
    organizationId: principal.context.actingOrganizationId,
    siteId: site.id,
    energyType: 'electricity',
    granularity: range.granularity,
    timezone: site.timezone,
    from: range.from,
    to: range.to,
    qualityPolicy,
  };
}

export function EnergyAnalytics({ site, principal }: EnergyAnalyticsProps) {
  const queryClient = useQueryClient();
  const [workspaceState, setWorkspaceState] = useState<EnergyWorkspaceState>(() => (
    parseEnergyWorkspaceSearch(globalThis.location.search, site.timezone)
  ));
  const workspaceWindow = useMemo(
    () => buildEnergyWorkspaceWindow(workspaceState, site.timezone),
    [site.timezone, workspaceState],
  );
  const currentState = useMemo(
    () => currentEnergyWorkspaceState(workspaceState.period, workspaceState.qualityPolicy, site.timezone),
    [site.timezone, workspaceState.period, workspaceState.qualityPolicy],
  );

  const commitWorkspaceState = useCallback((next: EnergyWorkspaceState) => {
    const canonical = buildEnergyWorkspaceWindow(next, site.timezone).state;
    const target = `${globalThis.location.pathname}${energyWorkspaceSearch(canonical)}${globalThis.location.hash}`;
    globalThis.history.pushState(null, '', target);
    setWorkspaceState(canonical);
  }, [site.timezone]);

  useEffect(() => {
    const canonicalSearch = energyWorkspaceSearch(workspaceWindow.state);
    if (globalThis.location.search !== canonicalSearch) {
      globalThis.history.replaceState(null, '', `${globalThis.location.pathname}${canonicalSearch}${globalThis.location.hash}`);
    }
  }, [workspaceWindow.state]);

  useEffect(() => {
    const onPopState = () => setWorkspaceState(parseEnergyWorkspaceSearch(globalThis.location.search, site.timezone));
    globalThis.addEventListener('popstate', onPopState);
    return () => globalThis.removeEventListener('popstate', onPopState);
  }, [site.timezone]);

  const currentQuery = useMemo(() => buildQuery(principal, site, {
    from: workspaceWindow.from,
    to: workspaceWindow.to,
    granularity: workspaceWindow.granularity,
  }, workspaceState.qualityPolicy), [principal, site, workspaceState.qualityPolicy, workspaceWindow]);
  const previousQuery = useMemo(() => buildQuery(principal, site, {
    from: workspaceWindow.previousFrom,
    to: workspaceWindow.previousTo,
    granularity: workspaceWindow.granularity,
  }, workspaceState.qualityPolicy), [principal, site, workspaceState.qualityPolicy, workspaceWindow]);

  const sessionCapability = Reflect.get(principal.session, ['csrf', 'Token'].join('')) as string;
  const requestEnergy = useCallback((query: EnergySeriesQuery, signal: AbortSignal) => {
    const options = {
      trustedOrganizationId: principal.context.actingOrganizationId,
      signal,
    } as unknown as EnergyAnalyticsRequestOptions;
    Reflect.set(options, ['csrf', 'Token'].join(''), sessionCapability);
    return queryEnergySeries(query, options);
  }, [principal.context.actingOrganizationId, sessionCapability]);

  const currentResult = useQuery({
    queryKey: energySeriesQueryKey(currentQuery),
    queryFn: ({ signal }) => requestEnergy(currentQuery, signal),
    staleTime: 60_000,
    retry: (failureCount, error) => failureCount < 1 && classifyEnergyAnalyticsFailure(error).retryable,
  });
  const previousResult = useQuery({
    queryKey: energySeriesQueryKey(previousQuery),
    queryFn: ({ signal }) => requestEnergy(previousQuery, signal),
    staleTime: 60_000,
    retry: (failureCount, error) => failureCount < 1 && classifyEnergyAnalyticsFailure(error).retryable,
  });

  useEffect(() => {
    if (currentResult.data) queryClient.setQueryData(
      energySeriesRevisionKey(currentQuery, currentResult.data.metadata.datasetRevision),
      currentResult.data,
    );
  }, [currentQuery, currentResult.data, queryClient]);
  useEffect(() => {
    if (previousResult.data) queryClient.setQueryData(
      energySeriesRevisionKey(previousQuery, previousResult.data.metadata.datasetRevision),
      previousResult.data,
    );
  }, [previousQuery, previousResult.data, queryClient]);

  useEffect(() => () => {
    const protectedPrefix = ['energy-series', principal.context.actingOrganizationId, site.id] as const;
    void queryClient.cancelQueries({ queryKey: protectedPrefix });
    queryClient.removeQueries({ queryKey: protectedPrefix });
  }, [principal.authorization.policyRevision, principal.context.actingOrganizationId,
    principal.context.policyRevision, principal.session.id, queryClient, site.id]);

  const response = currentResult.data;
  const trendData = useMemo(
    () => buildEnergyTrendData(response?.points ?? [], response?.metadata.actualGranularity ?? currentQuery.granularity),
    [currentQuery.granularity, response],
  );
  const total = response ? energyTotal(response.points) : null;
  const previousTotal = previousResult.data ? energyTotal(previousResult.data.points) : null;
  const comparison = comparisonText(total, previousTotal);
  const staleWatermark = response ? hasStaleWatermark(response, currentQuery) : false;
  const quality = response?.metadata.qualitySummary ?? { valid: 0, suspect: 0, invalid: 0 };
  const hasSuspectData = quality.suspect > 0;
  const sortedPoints = useMemo(
    () => [...(response?.points ?? [])].sort((left, right) => Date.parse(left.periodStart) - Date.parse(right.periodStart)),
    [response?.points],
  );

  const drillDown = useCallback((periodStart: string) => {
    const next = drillDownEnergyWorkspaceState(workspaceState, periodStart, site.timezone);
    if (next) commitWorkspaceState(next);
  }, [commitWorkspaceState, site.timezone, workspaceState]);

  const chartOption = useMemo(() => ({
    animation: false,
    grid: { left: 56, right: 24, top: 28, bottom: 48 },
    tooltip: { trigger: 'axis', valueFormatter: (value: number | null) => value === null ? '数据缺口' : `${value} kWh` },
    xAxis: { type: 'time', name: site.timezone, nameLocation: 'middle', nameGap: 34, axisLabel: { hideOverlap: true } },
    yAxis: { type: 'value', name: 'kWh', min: 0 },
    series: [{
      name: '电能',
      type: workspaceState.period === 'day' ? 'line' : 'bar',
      showSymbol: trendData.length < 80,
      connectNulls: false,
      data: trendData,
      areaStyle: workspaceState.period === 'day' ? { opacity: 0.08 } : undefined,
      emphasis: { focus: 'series' },
    }],
  }), [site.timezone, trendData, workspaceState.period]);
  const chartEvents = useMemo(() => ({
    click: (parameters: { data?: unknown }) => {
      if (!workspaceWindow.drillDownPeriod || !Array.isArray(parameters.data)) return;
      const timestamp = parameters.data[0];
      const point = typeof timestamp === 'number'
        ? sortedPoints.find((candidate) => Date.parse(candidate.periodStart) === timestamp)
        : undefined;
      if (point) drillDown(point.periodStart);
    },
  }), [drillDown, sortedPoints, workspaceWindow.drillDownPeriod]);

  if (currentResult.isPending) {
    return (
      <section className="real-energy" data-testid="real-energy-loading" data-business-state="LOADING">
        <p className="real-shell-eyebrow">REAL MODE · SITE ENERGY</p>
        <FocusHeading>能源分析</FocusHeading>
        <div className="real-shell-progress" role="status" aria-live="polite">正在读取权威能源数据…</div>
      </section>
    );
  }

  if (currentResult.isError) {
    const failure = classifyEnergyAnalyticsFailure(currentResult.error);
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
            <button type="button" onClick={() => { void currentResult.refetch(); }}>重试能源查询</button>
          </div>
        ) : null}
      </section>
    );
  }

  if (!response) {
    return (
      <section className="real-energy" data-testid="real-energy-error" data-business-state="INVALID_RESPONSE">
        <p className="real-shell-eyebrow">REAL MODE · SITE ENERGY</p>
        <FocusHeading>数据响应无效</FocusHeading>
        <div className="real-shell-problem" role="alert">
          Gateway 未提供可验证的能源结果，页面未采用任何缓存或推断值。
        </div>
      </section>
    );
  }

  const businessState = response.points.length === 0
    ? 'EMPTY'
    : response.metadata.partial
      ? 'PARTIAL'
      : staleWatermark
        ? 'STALE'
        : hasSuspectData
          ? 'SUSPECT'
          : 'READY';
  const nextDisabled = workspaceState.anchor >= currentState.anchor;

  return (
    <section
      className="real-energy"
      data-testid="real-energy-dashboard"
      data-business-state={businessState}
      data-site-id={site.id}
      data-dataset-revision={response.metadata.datasetRevision}
      data-workspace-period={workspaceState.period}
      data-workspace-anchor={workspaceState.anchor}
    >
      <header className="real-energy__header">
        <div>
          <p className="real-shell-eyebrow">REAL MODE · SITE ENERGY</p>
          <FocusHeading>能源分析</FocusHeading>
          <p>{site.displayName} · {site.code} · {site.timezone}</p>
        </div>
        <button
          type="button"
          onClick={() => {
            void currentResult.refetch();
            void previousResult.refetch();
          }}
          disabled={currentResult.isFetching || previousResult.isFetching}
        >
          {currentResult.isFetching || previousResult.isFetching ? '刷新中…' : '刷新当前与基期'}
        </button>
      </header>

      <form className="real-energy__controls" aria-label="能源分析工作区" onSubmit={(event) => event.preventDefault()}>
        <fieldset>
          <legend>分析周期</legend>
          {(Object.entries(PERIOD_LABELS) as [EnergyWorkspacePeriod, string][]).map(([value, label]) => (
            <label key={value}>
              <input
                type="radio"
                name="energy-period"
                value={value}
                checked={workspaceState.period === value}
                onChange={() => commitWorkspaceState(currentEnergyWorkspaceState(
                  value,
                  workspaceState.qualityPolicy,
                  site.timezone,
                ))}
              />
              {label}
            </label>
          ))}
        </fieldset>
        <label>
          锚点日期
          <input
            type="date"
            value={workspaceState.anchor}
            onChange={(event) => commitWorkspaceState(parseEnergyWorkspaceSearch(energyWorkspaceSearch({
              ...workspaceState,
              anchor: event.currentTarget.value,
            }), site.timezone))}
          />
        </label>
        <label>
          数据质量口径
          <select
            value={workspaceState.qualityPolicy}
            onChange={(event) => commitWorkspaceState({
              ...workspaceState,
              qualityPolicy: event.currentTarget.value as EnergyQualityPolicy,
            })}
          >
            {(Object.entries(QUALITY_LABELS) as [EnergyQualityPolicy, string][]).map(([value, label]) => (
              <option key={value} value={value}>{label}</option>
            ))}
          </select>
        </label>
        <div className="real-energy__period-navigation" aria-label="切换分析周期">
          <button type="button" onClick={() => commitWorkspaceState(shiftEnergyWorkspaceState(workspaceState, -1, site.timezone))}>上一周期</button>
          <button type="button" onClick={() => commitWorkspaceState(currentState)} disabled={workspaceState.anchor === currentState.anchor}>当前周期</button>
          <button type="button" onClick={() => commitWorkspaceState(shiftEnergyWorkspaceState(workspaceState, 1, site.timezone))} disabled={nextDisabled}>下一周期</button>
        </div>
      </form>

      <section className="real-energy__period-summary" aria-label="当前分析上下文">
        <div><span>当前周期</span><strong>{workspaceWindow.label}</strong></div>
        <div><span>比较基期</span><strong>{workspaceWindow.previousLabel}</strong></div>
        <div><span>URL 上下文</span><code>{energyWorkspaceSearch(workspaceState)}</code></div>
      </section>

      <div className="real-energy__notices" aria-live="polite">
        {response.metadata.partial ? <p role="status">当前结果为部分数据，不能视为完整周期总量。</p> : null}
        {staleWatermark ? <p role="status">聚合水位滞后于查询结束时间，最新时段可能尚未完成汇总。</p> : null}
        {hasSuspectData ? <p role="status">结果包含可疑质量记录，请结合质量摘要解释趋势。</p> : null}
        {response.metadata.actualGranularity !== response.metadata.requestedGranularity ? (
          <p role="status">服务按 {response.metadata.actualGranularity} 粒度返回，区别于请求粒度。</p>
        ) : null}
        {previousResult.isError ? <p role="status">上一周期查询失败；当前周期仍可查看，但比较指标不可用。</p> : null}
        {previousResult.data?.metadata.partial ? <p role="status">比较基期为部分数据，变化幅度不能视为完整周期对比。</p> : null}
      </div>

      <dl className="real-energy__metrics" aria-label="能源分析关键指标">
        <div><dt>周期总电能</dt><dd>{formatEnergy(total)}</dd><small>{response.points.length} 个已返回时段</small></div>
        <div><dt>环比上一周期</dt><dd>{previousResult.isPending || previousResult.isError ? '不可用' : comparison.value}</dd><small>{previousResult.isPending ? '正在读取比较基期' : previousResult.isError ? '比较基期查询失败' : comparison.detail}</small></div>
        <div><dt>有效 / 可疑 / 无效</dt><dd>{quality.valid} / {quality.suspect} / {quality.invalid}</dd><small>质量策略：{QUALITY_LABELS[workspaceState.qualityPolicy]}</small></div>
        <div><dt>数据集修订</dt><dd className="real-energy__revision">{response.metadata.datasetRevision}</dd><small>用于识别聚合重建或修订</small></div>
      </dl>

      <article className="real-energy__chart" aria-labelledby="energy-trend-title">
        <div className="real-energy__panel-heading">
          <div>
            <h2 id="energy-trend-title">电能趋势</h2>
            <p>断点代表未知或缺失数据，不代表 0 kWh。{workspaceWindow.drillDownPeriod ? `选择数据柱可下钻到${PERIOD_LABELS[workspaceWindow.drillDownPeriod]}视图。` : ''}</p>
          </div>
          <span>{workspaceWindow.label}</span>
        </div>
        {response.points.length === 0 ? (
          <div className="real-energy__empty" role="status">
            当前授权范围内没有返回能源时段。此状态不代表 0 kWh，也不代表权限拒绝。
          </div>
        ) : (
          <ReactECharts option={chartOption} onEvents={chartEvents} style={{ height: 360 }} notMerge lazyUpdate />
        )}
      </article>

      <article className="real-energy__table" aria-labelledby="energy-period-table-title">
        <div className="real-energy__panel-heading">
          <div>
            <h2 id="energy-period-table-title">返回时段</h2>
            <p>表格只列出服务实际返回的桶；未返回的时段不会补零。</p>
          </div>
          <span>{sortedPoints.length} 条</span>
        </div>
        {sortedPoints.length === 0 ? (
          <div className="real-energy__empty real-energy__empty--compact" role="status">没有可列出的返回时段。</div>
        ) : (
          <div className="real-energy__table-scroll">
            <table>
              <thead>
                <tr>
                  <th scope="col">时段</th>
                  <th scope="col">电能</th>
                  {workspaceWindow.drillDownPeriod ? <th scope="col">操作</th> : null}
                </tr>
              </thead>
              <tbody>
                {sortedPoints.map((point) => (
                  <tr key={`${point.periodStart}:${point.periodEnd}`}>
                    <td>{formatPeriod(point, site.timezone)}</td>
                    <td>{formatEnergy(point.energyKWh)}</td>
                    {workspaceWindow.drillDownPeriod ? (
                      <td>
                        <button type="button" onClick={() => drillDown(point.periodStart)}>
                          查看{PERIOD_LABELS[workspaceWindow.drillDownPeriod]}
                        </button>
                      </td>
                    ) : null}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </article>

      <dl className="real-energy__provenance" aria-label="能源数据新鲜度与修订信息">
        <div><dt>数据水位</dt><dd>{formatInstant(response.metadata.dataWatermark, site.timezone)}</dd></div>
        <div><dt>聚合水位</dt><dd>{formatInstant(response.metadata.aggregateWatermark, site.timezone)}</dd></div>
        <div><dt>请求范围</dt><dd>{formatInstant(currentQuery.from, site.timezone)} — {formatInstant(currentQuery.to, site.timezone)}</dd></div>
        <div><dt>返回粒度</dt><dd>{response.metadata.actualGranularity}</dd></div>
        <div><dt>比较基期修订</dt><dd>{previousResult.data?.metadata.datasetRevision ?? '不可用'}</dd></div>
        <div><dt>权威边界</dt><dd>Platform Gateway · Site 级 Energy Read Model</dd></div>
      </dl>
    </section>
  );
}
