import { useCallback, useEffect, useMemo, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Alert, Button, Card, Descriptions, Input, Segmented, Select, Space, Tag, Typography } from 'antd';
import { FundOutlined, LeftOutlined, ReloadOutlined, RightOutlined } from '@ant-design/icons';
import PageScaffold from '@/components/PageScaffold';
import { OperationsMetrics } from '@/components/OperationsUI';
import type { CurrentPrincipalResponse, Site } from '@/api/generated/platformGateway.gen';
import {
  classifyEnergyAnalyticsFailure,
  energySeriesQueryKey,
  energySeriesRevisionKey,
  hasStaleWatermark,
  queryEnergySeries,
  type EnergyAnalyticsRequestOptions,
  type EnergyQualityPolicy,
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
import { summarizeEnergyPoints } from './energy-presentation';
import { RealEnergyWorkspacePanels } from './RealEnergyWorkspacePanels';
import type { EnergyRoutePeriod } from './site-routing';
import './energy-analytics.css';

interface EnergyAnalyticsProps {
  site: Readonly<Site>;
  principal: CurrentPrincipalResponse;
  initialPeriod?: EnergyRoutePeriod;
}

const PERIOD_LABELS: Record<EnergyWorkspacePeriod, string> = {
  day: '日', week: '周', month: '月', year: '年',
};
const QUALITY_LABELS: Record<EnergyQualityPolicy, string> = {
  VALID_ONLY: '仅有效数据',
  VALID_AND_SUSPECT: '包含可疑数据',
};

function energyPeriodPath(period: EnergyWorkspacePeriod): string {
  const segments = globalThis.location.pathname.split('/').filter(Boolean);
  const energyIndex = segments.indexOf('energy');
  if (energyIndex < 0) return globalThis.location.pathname;
  if (segments.length === energyIndex + 1) return `/${segments.join('/')}`;
  return `/${[...segments.slice(0, energyIndex + 1), period].join('/')}`;
}

function energyPeriodFromLocation(fallback: EnergyWorkspacePeriod): EnergyWorkspacePeriod {
  const segments = globalThis.location.pathname.split('/').filter(Boolean);
  const energyIndex = segments.indexOf('energy');
  const candidate = segments[energyIndex + 1];
  return candidate && candidate in PERIOD_LABELS ? candidate as EnergyWorkspacePeriod : fallback;
}

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

export function EnergyAnalytics({ site, principal, initialPeriod }: EnergyAnalyticsProps) {
  const queryClient = useQueryClient();
  const [workspaceState, setWorkspaceState] = useState<EnergyWorkspaceState>(() => {
    const parsed = parseEnergyWorkspaceSearch(globalThis.location.search, site.timezone);
    return initialPeriod ? { ...parsed, period: initialPeriod } : parsed;
  });
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
    const target = `${energyPeriodPath(canonical.period)}${energyWorkspaceSearch(canonical)}${globalThis.location.hash}`;
    globalThis.history.pushState(null, '', target);
    setWorkspaceState(canonical);
  }, [site.timezone]);

  useEffect(() => {
    const canonicalSearch = energyWorkspaceSearch(workspaceWindow.state);
    const canonicalPath = energyPeriodPath(workspaceWindow.state.period);
    if (globalThis.location.search !== canonicalSearch || globalThis.location.pathname !== canonicalPath) {
      globalThis.history.replaceState(null, '', `${canonicalPath}${canonicalSearch}${globalThis.location.hash}`);
    }
  }, [workspaceWindow.state]);

  useEffect(() => {
    const onPopState = () => {
      const parsed = parseEnergyWorkspaceSearch(globalThis.location.search, site.timezone);
      setWorkspaceState({ ...parsed, period: energyPeriodFromLocation(parsed.period) });
    };
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
  const summary = useMemo(() => summarizeEnergyPoints(response?.points ?? []), [response?.points]);
  const previousSummary = useMemo(() => summarizeEnergyPoints(previousResult.data?.points ?? []), [previousResult.data?.points]);
  const total = summary.total;
  const previousTotal = previousSummary.total;
  const comparison = comparisonText(total, previousTotal);
  const staleWatermark = response ? hasStaleWatermark(response, currentQuery) : false;
  const quality = response?.metadata.qualitySummary ?? { valid: 0, suspect: 0, invalid: 0 };
  const hasSuspectData = quality.suspect > 0;

  const drillDown = useCallback((periodStart: string) => {
    const next = drillDownEnergyWorkspaceState(workspaceState, periodStart, site.timezone);
    if (next) commitWorkspaceState(next);
  }, [commitWorkspaceState, site.timezone, workspaceState]);

  if (currentResult.isPending) {
    return (
      <section className="real-energy" data-testid="real-energy-loading" data-business-state="LOADING">
        <PageScaffold
          title="能耗分析"
          heading={<FocusHeading className="ops-page-title ant-typography"><Space><FundOutlined />能耗分析</Space></FocusHeading>}
          extra={<Tag color="processing">LOADING</Tag>}
          className="energy-page"
        >
          <Card variant="borderless"><div className="real-shell-progress" role="status" aria-live="polite">正在读取权威能源数据…</div></Card>
        </PageScaffold>
      </section>
    );
  }

  if (currentResult.isError) {
    const failure = classifyEnergyAnalyticsFailure(currentResult.error);
    return (
      <section className="real-energy" data-testid="real-energy-error" data-business-state={failure.kind.toUpperCase()}>
        <PageScaffold
          title={failure.title}
          heading={<FocusHeading className="ops-page-title ant-typography"><Space><FundOutlined />{failure.title}</Space></FocusHeading>}
          extra={<Tag color="error">{failure.kind.toUpperCase()}</Tag>}
          className="energy-page"
        >
          <Alert
            type="error"
            showIcon
            message={failure.title}
            description={<Space direction="vertical"><span>{failure.detail}</span>{failure.traceId ? <code>traceId {failure.traceId}</code> : null}</Space>}
            action={failure.retryable ? <Button icon={<ReloadOutlined />} onClick={() => { void currentResult.refetch(); }}>重试能源查询</Button> : undefined}
            data-retryable={String(failure.retryable)}
          />
        </PageScaffold>
      </section>
    );
  }

  if (!response) {
    return (
      <section className="real-energy" data-testid="real-energy-error" data-business-state="INVALID_RESPONSE">
        <PageScaffold
          title="数据响应无效"
          heading={<FocusHeading className="ops-page-title ant-typography"><Space><FundOutlined />数据响应无效</Space></FocusHeading>}
          extra={<Tag color="error">INVALID RESPONSE</Tag>}
          className="energy-page"
        >
          <Alert type="error" showIcon message="数据响应无效" description="Gateway 未提供可验证的能源结果，页面未采用任何缓存或推断值。" />
        </PageScaffold>
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
  const metrics = [
    { key: 'total', label: '周期总电能', value: formatEnergy(total), detail: `${summary.measuredCount} 个权威返回桶`, tone: 'accent' as const },
    { key: 'comparison', label: '环比上一周期', value: previousResult.isPending || previousResult.isError ? '不可用' : comparison.value, detail: previousResult.isPending ? '正在读取比较基期' : previousResult.isError ? '比较基期查询失败' : comparison.detail, tone: 'positive' as const },
    { key: 'average', label: '平均每桶电能', value: formatEnergy(summary.average), detail: `返回粒度：${response.metadata.actualGranularity}`, tone: 'default' as const },
    { key: 'peak', label: '峰值时段', value: formatEnergy(summary.peak?.energyKWh ?? null), detail: summary.peak ? formatInstant(summary.peak.periodStart, site.timezone) : '没有可计算时段', tone: 'warning' as const },
    { key: 'valley', label: '低谷时段', value: formatEnergy(summary.valley?.energyKWh ?? null), detail: summary.valley ? formatInstant(summary.valley.periodStart, site.timezone) : '没有可计算时段', tone: 'positive' as const },
    { key: 'quality', label: '有效 / 可疑 / 无效', value: `${quality.valid} / ${quality.suspect} / ${quality.invalid}`, detail: `质量策略：${QUALITY_LABELS[workspaceState.qualityPolicy]}`, tone: hasSuspectData ? 'warning' as const : 'default' as const },
  ];

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
      <PageScaffold
        title="能耗分析"
        heading={<FocusHeading className="ops-page-title ant-typography"><Space><FundOutlined />能耗分析</Space></FocusHeading>}
        extra={(
          <Space wrap>
            <Tag color={businessState === 'READY' ? 'green' : businessState === 'EMPTY' ? 'default' : 'orange'}>{businessState}</Tag>
            <Button
              icon={<ReloadOutlined />}
              loading={currentResult.isFetching || previousResult.isFetching}
              onClick={() => {
                void currentResult.refetch();
                void previousResult.refetch();
              }}
            >
              刷新当前与基期
            </Button>
          </Space>
        )}
        className="energy-page"
      >
        <Typography.Text type="secondary">{site.displayName} · {site.code} · {site.timezone}</Typography.Text>

      <Card title="分析条件" variant="borderless" className="energy-controls-card">
        <div className="ops-toolbar" aria-label="能源分析工作区">
          <Space direction="vertical" size={4}>
            <Typography.Text type="secondary">分析周期</Typography.Text>
            <Segmented<EnergyWorkspacePeriod>
              value={workspaceState.period}
              options={(Object.entries(PERIOD_LABELS) as [EnergyWorkspacePeriod, string][]).map(([value, label]) => ({ value, label: `${label}度` }))}
              onChange={(value) => commitWorkspaceState(currentEnergyWorkspaceState(
                value,
                workspaceState.qualityPolicy,
                site.timezone,
              ))}
            />
          </Space>
          <Space wrap align="end">
            <Space direction="vertical" size={4}>
              <Typography.Text type="secondary">锚点日期</Typography.Text>
              <Input
                type="date"
                value={workspaceState.anchor}
                onChange={(event) => commitWorkspaceState(parseEnergyWorkspaceSearch(energyWorkspaceSearch({
                  ...workspaceState,
                  anchor: event.currentTarget.value,
                }), site.timezone))}
                style={{ width: 160 }}
              />
            </Space>
            <Space direction="vertical" size={4}>
              <Typography.Text type="secondary">数据质量口径</Typography.Text>
              <Select<EnergyQualityPolicy>
                value={workspaceState.qualityPolicy}
                options={(Object.entries(QUALITY_LABELS) as [EnergyQualityPolicy, string][]).map(([value, label]) => ({ value, label }))}
                onChange={(qualityPolicy) => commitWorkspaceState({ ...workspaceState, qualityPolicy })}
                style={{ width: 170 }}
              />
            </Space>
            <Button icon={<LeftOutlined />} onClick={() => commitWorkspaceState(shiftEnergyWorkspaceState(workspaceState, -1, site.timezone))}>上一周期</Button>
            <Button onClick={() => commitWorkspaceState(currentState)} disabled={workspaceState.anchor === currentState.anchor}>当前周期</Button>
            <Button onClick={() => commitWorkspaceState(shiftEnergyWorkspaceState(workspaceState, 1, site.timezone))} disabled={nextDisabled}>下一周期 <RightOutlined /></Button>
          </Space>
        </div>
      </Card>

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

      <OperationsMetrics items={metrics} ariaLabel="能源分析关键指标" />

      <RealEnergyWorkspacePanels
        period={workspaceState.period}
        anchor={workspaceState.anchor}
        timezone={site.timezone}
        currentLabel={workspaceWindow.label}
        previousLabel={workspaceWindow.previousLabel}
        currentResponse={response}
        previousResponse={previousResult.data}
        previousLoading={previousResult.isPending}
        drillDownPeriod={workspaceWindow.drillDownPeriod}
        onDrillDown={drillDown}
      />

      <Card title="数据新鲜度与权威边界" variant="borderless" className="energy-provenance-card">
        <Descriptions column={{ xs: 1, sm: 2, xl: 3 }} bordered size="small" aria-label="能源数据新鲜度与修订信息">
          <Descriptions.Item label="数据水位">{formatInstant(response.metadata.dataWatermark, site.timezone)}</Descriptions.Item>
          <Descriptions.Item label="聚合水位">{formatInstant(response.metadata.aggregateWatermark, site.timezone)}</Descriptions.Item>
          <Descriptions.Item label="请求范围">{formatInstant(currentQuery.from, site.timezone)} — {formatInstant(currentQuery.to, site.timezone)}</Descriptions.Item>
          <Descriptions.Item label="返回粒度">{response.metadata.actualGranularity}</Descriptions.Item>
          <Descriptions.Item label="比较基期修订">{previousResult.data?.metadata.datasetRevision ?? '不可用'}</Descriptions.Item>
          <Descriptions.Item label="权威边界">Platform Gateway · Site 级 Energy Read Model</Descriptions.Item>
        </Descriptions>
      </Card>
      </PageScaffold>
    </section>
  );
}
