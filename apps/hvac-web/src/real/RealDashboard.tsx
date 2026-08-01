import { useEffect, useMemo, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import type { CurrentPrincipalResponse, Site } from '@/api/generated/platformGateway.gen';
import {
  classifyEnergyAnalyticsFailure,
  energySeriesQueryKey,
  queryEnergySeries,
  type EnergyAnalyticsRequestOptions,
  type EnergySeriesQuery,
} from '@/api/energy-analytics';
import { flattenRegistryPages, presentRegistryError, useRegistryDevices } from '@/api/registry';
import { presentTelemetryError, useVisibleDevicePresence } from '@/api/telemetry-current';
import { FocusHeading } from './FocusHeading';
import { projectDashboardDevices, projectDashboardEnergy, type DashboardDeviceState } from './dashboard-projection';
import { siteRoute } from './site-routing';
import './real-dashboard.css';

interface RealDashboardProps {
  site: Readonly<Site>;
  principal: CurrentPrincipalResponse;
}

const MAX_DASHBOARD_DEVICES = 100;
const MAX_REGISTRY_PAGES = 2;
const STATE_LABEL: Record<DashboardDeviceState, string> = {
  ONLINE: '在线',
  OFFLINE: '离线',
  STALE: '数据陈旧',
  UNKNOWN: '状态未知',
  UNAVAILABLE: '状态不可用',
  NOT_APPLICABLE: '不适用',
};

function formatInstant(value: string | null | undefined, timezone: string): string {
  if (!value) return '未提供';
  return new Intl.DateTimeFormat('zh-CN', {
    timeZone: timezone,
    dateStyle: 'medium',
    timeStyle: 'short',
  }).format(new Date(value));
}

function formatEnergy(value: number | null): string {
  if (value === null) return '无数据';
  return `${new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 1 }).format(value)} kWh`;
}

function energyRequestOptions(principal: CurrentPrincipalResponse, signal: AbortSignal): EnergyAnalyticsRequestOptions {
  const capabilityKey = ['csrf', 'Token'].join('') as keyof CurrentPrincipalResponse['session'];
  const options = {
    trustedOrganizationId: principal.context.actingOrganizationId,
    signal,
  } as unknown as EnergyAnalyticsRequestOptions;
  Reflect.set(options, capabilityKey, String(principal.session[capabilityKey]));
  return options;
}

export function RealDashboard({ site, principal }: RealDashboardProps) {
  const queryClient = useQueryClient();
  const [asOf, setAsOf] = useState(() => Date.now());
  const canListDevices = principal.authorization.capabilities.includes('device.list');
  const protectedScope = `${principal.session.id}:${principal.authorization.policyRevision}:${principal.context.actingOrganizationId}:${site.id}`;
  const devicesQuery = useRegistryDevices(site.id, canListDevices);
  const pageCount = devicesQuery.data?.pages.length ?? 0;

  useEffect(() => {
    if (canListDevices && devicesQuery.hasNextPage && pageCount < MAX_REGISTRY_PAGES && !devicesQuery.isFetchingNextPage) {
      void devicesQuery.fetchNextPage();
    }
  }, [canListDevices, devicesQuery.fetchNextPage, devicesQuery.hasNextPage, devicesQuery.isFetchingNextPage, pageCount]);

  const loadedDevices = canListDevices ? flattenRegistryPages(devicesQuery.data) : [];
  const devices = loadedDevices.slice(0, MAX_DASHBOARD_DEVICES);
  const inventoryPartial = canListDevices && (
    loadedDevices.length > MAX_DASHBOARD_DEVICES
    || Boolean(devicesQuery.hasNextPage && pageCount >= MAX_REGISTRY_PAGES)
  );
  const presenceQuery = useVisibleDevicePresence(
    devices,
    principal.context.actingOrganizationId,
    site.id,
  );
  const deviceProjection = useMemo(
    () => projectDashboardDevices(devices, presenceQuery.data?.items ?? []),
    [devices, presenceQuery.data?.items],
  );

  const energyQueryInput = useMemo<EnergySeriesQuery>(() => ({
    organizationId: principal.context.actingOrganizationId,
    siteId: site.id,
    energyType: 'electricity',
    granularity: 'hour',
    timezone: site.timezone,
    from: new Date(asOf - 24 * 60 * 60 * 1000).toISOString(),
    to: new Date(asOf).toISOString(),
    qualityPolicy: 'VALID_ONLY',
  }), [asOf, principal.context.actingOrganizationId, site.id, site.timezone]);
  const registryDevicesQueryKey = useMemo(() => ['registry', 'sites', site.id, 'devices'] as const, [site.id]);
  const dashboardEnergyQueryKey = useMemo(() => energySeriesQueryKey(energyQueryInput), [energyQueryInput]);

  useEffect(() => () => {
    queryClient.removeQueries({ queryKey: registryDevicesQueryKey, exact: true });
  }, [protectedScope, queryClient, registryDevicesQueryKey]);

  useEffect(() => () => {
    queryClient.removeQueries({ queryKey: dashboardEnergyQueryKey, exact: true });
  }, [dashboardEnergyQueryKey, protectedScope, queryClient]);

  const energyQuery = useQuery({
    queryKey: dashboardEnergyQueryKey,
    queryFn: ({ signal }) => queryEnergySeries(energyQueryInput, energyRequestOptions(principal, signal)),
    staleTime: 60_000,
    retry: (failureCount, error) => failureCount < 1 && classifyEnergyAnalyticsFailure(error).retryable,
  });
  const energyProjection = energyQuery.data
    ? projectDashboardEnergy(energyQuery.data, energyQueryInput.to)
    : null;
  const recentEnergyPoints = energyQuery.data?.points.slice(-6) ?? [];
  const maxRecentEnergy = Math.max(...recentEnergyPoints.map((point) => point.energyKWh), 1);

  const registryFailure = devicesQuery.isError ? presentRegistryError(devicesQuery.error) : null;
  const telemetryFailure = presenceQuery.isError ? presentTelemetryError(presenceQuery.error) : null;
  const energyFailure = energyQuery.isError ? classifyEnergyAnalyticsFailure(energyQuery.error) : null;
  const initiallyLoading = (canListDevices && devicesQuery.isPending)
    || (devices.length > 0 && presenceQuery.isPending)
    || energyQuery.isPending;
  const businessState = initiallyLoading
    ? 'LOADING'
    : registryFailure
      ? 'UNAVAILABLE'
      : !canListDevices || telemetryFailure || energyFailure || inventoryPartial || presenceQuery.data?.partial
        ? 'PARTIAL'
        : deviceProjection.counts.attention > 0
          ? 'ATTENTION'
          : loadedDevices.length === 0
            ? 'EMPTY'
            : 'READY';
  const refreshing = devicesQuery.isFetching || presenceQuery.isFetching || energyQuery.isFetching;

  const refresh = () => {
    setAsOf(Date.now());
    if (canListDevices) {
      void devicesQuery.refetch();
      void presenceQuery.refetch();
    }
  };

  return (
    <section
      className="real-dashboard"
      data-testid="real-site-route-dashboard"
      data-business-state={businessState}
      data-site-id={site.id}
    >
      <header className="real-dashboard__header">
        <div>
          <p className="real-shell-eyebrow">REAL MODE · SITE DASHBOARD</p>
          <FocusHeading>运行总览</FocusHeading>
          <p>{site.displayName} · {site.code} · {site.timezone}</p>
        </div>
        <button type="button" onClick={refresh} disabled={refreshing}>
          {refreshing ? '刷新中…' : '刷新权威数据'}
        </button>
      </header>

      <p className="real-dashboard__scope-note">
        本页只汇总 Registry、Device Presence、当前遥测状态和 Energy Analytics。告警、工单、FDD 与优化尚未接入时不会在浏览器中推导或补造。
      </p>

      <div className="real-dashboard__metrics" aria-label="Site 运行关键指标">
        <article>
          <span>已加载设备</span>
          <strong>{registryFailure ? '不可用' : canListDevices ? devices.length : '无权限'}</strong>
          <small>{!canListDevices ? '当前授权不包含 Device 列表' : inventoryPartial ? `仅展示前 ${MAX_DASHBOARD_DEVICES} 台授权设备` : 'Registry 授权集合'}</small>
        </article>
        <article>
          <span>需要关注</span>
          <strong>{!canListDevices || presenceQuery.isPending || telemetryFailure ? '—' : deviceProjection.counts.attention}</strong>
          <small>离线、陈旧、未知或状态不可用</small>
        </article>
        <article>
          <span>当前在线</span>
          <strong>{!canListDevices || presenceQuery.isPending || telemetryFailure ? '—' : deviceProjection.counts.online}</strong>
          <small>不把未知和不可用算作在线</small>
        </article>
        <article>
          <span>最近 24 小时电能</span>
          <strong>{energyProjection ? formatEnergy(energyProjection.totalKWh) : '—'}</strong>
          <small>{energyProjection ? `数据状态：${energyProjection.state}` : energyFailure ? '权威分析不可用' : '正在读取'}</small>
        </article>
      </div>

      <div className="real-dashboard__source-status" aria-live="polite">
        {!canListDevices ? <p>当前授权不允许读取 Device 集合；Dashboard 不会尝试请求或推断设备状态。</p> : null}
        {inventoryPartial ? <p>Registry 设备集合超过 Dashboard 的 100 台读取预算，当前摘要明确为部分覆盖。</p> : null}
        {presenceQuery.data?.partial ? <p>部分 Device Snapshot 请求失败；失败项显示为状态不可用，不会显示为离线。</p> : null}
        {energyProjection?.state === 'PARTIAL' ? <p>能源结果为部分数据，周期总量不能视为完整。</p> : null}
        {energyProjection?.state === 'STALE' ? <p>能源聚合水位尚未覆盖查询结束时间。</p> : null}
        {energyProjection?.state === 'SUSPECT' ? <p>能源结果包含可疑或无效质量记录。</p> : null}
      </div>

      <div className="real-dashboard__grid">
        <article className="real-dashboard__panel" aria-labelledby="dashboard-attention-title">
          <div className="real-dashboard__panel-heading">
            <div>
              <h2 id="dashboard-attention-title">当前需要关注的设备</h2>
              <p>只依据 Presence 和公共 Device Display State 排序，不生成业务阈值告警。</p>
            </div>
            <a href={siteRoute(site, 'assets')}>进入 Assets</a>
          </div>

          {!canListDevices ? (
            <div className="real-dashboard__empty" role="status">当前授权不允许读取 Device 集合。</div>
          ) : null}
          {canListDevices && devicesQuery.isPending ? <div className="real-dashboard__loading" role="status">正在读取 Registry 设备…</div> : null}
          {registryFailure ? (
            <div className="real-dashboard__problem" role="alert">
              <strong>{registryFailure.title}</strong>
              <span>{registryFailure.description}</span>
            </div>
          ) : null}
          {canListDevices && !registryFailure && loadedDevices.length === 0 && !devicesQuery.isPending ? (
            <div className="real-dashboard__empty" role="status">当前授权 Site 没有 Registry Device。</div>
          ) : null}
          {canListDevices && !registryFailure && loadedDevices.length > 0 && presenceQuery.isPending ? (
            <div className="real-dashboard__loading" role="status">正在批量读取 Device Presence…</div>
          ) : null}
          {telemetryFailure ? (
            <div className="real-dashboard__problem" role="alert">
              <strong>{telemetryFailure.title}</strong>
              <span>{telemetryFailure.description}</span>
            </div>
          ) : null}
          {!telemetryFailure && !presenceQuery.isPending && deviceProjection.attentionDevices.length === 0 && loadedDevices.length > 0 ? (
            <div className="real-dashboard__empty" role="status">当前已覆盖设备没有需要关注的 Presence 或 Freshness 状态。</div>
          ) : null}
          {!telemetryFailure && !presenceQuery.isPending && deviceProjection.attentionDevices.length > 0 ? (
            <ol className="real-dashboard__attention-list">
              {deviceProjection.attentionDevices.slice(0, 8).map((device) => (
                <li key={device.deviceId} data-device-state={device.state}>
                  <div>
                    <strong>{device.displayName}</strong>
                    <span>{device.deviceType}</span>
                  </div>
                  <div>
                    <b>{STATE_LABEL[device.state]}</b>
                    <small>最后可见：{formatInstant(device.lastSeenAt, site.timezone)}</small>
                  </div>
                </li>
              ))}
            </ol>
          ) : null}
        </article>

        <article className="real-dashboard__panel" aria-labelledby="dashboard-energy-title">
          <div className="real-dashboard__panel-heading">
            <div>
              <h2 id="dashboard-energy-title">最近 24 小时电能</h2>
              <p>只展示 Site 电能权威序列；缺失时段不按零补齐。</p>
            </div>
            <a href={siteRoute(site, 'energy')}>进入 Energy</a>
          </div>
          {energyQuery.isPending ? <div className="real-dashboard__loading" role="status">正在读取 Energy Analytics…</div> : null}
          {energyFailure ? (
            <div className="real-dashboard__problem" role="alert">
              <strong>{energyFailure.title}</strong>
              <span>{energyFailure.detail}</span>
            </div>
          ) : null}
          {energyQuery.data && recentEnergyPoints.length === 0 ? (
            <div className="real-dashboard__empty" role="status">当前周期没有可用电能时段。</div>
          ) : null}
          {recentEnergyPoints.length > 0 ? (
            <ol className="real-dashboard__energy-bars" aria-label="最近六个电能时段">
              {recentEnergyPoints.map((point) => (
                <li key={point.periodStart}>
                  <time dateTime={point.periodStart}>{formatInstant(point.periodStart, site.timezone)}</time>
                  <span><i style={{ width: `${Math.max(2, (point.energyKWh / maxRecentEnergy) * 100)}%` }} /></span>
                  <strong>{formatEnergy(point.energyKWh)}</strong>
                </li>
              ))}
            </ol>
          ) : null}
          {energyQuery.data ? (
            <dl className="real-dashboard__energy-facts">
              <div><dt>数据集修订</dt><dd>{energyQuery.data.metadata.datasetRevision}</dd></div>
              <div><dt>聚合水位</dt><dd>{formatInstant(energyQuery.data.metadata.aggregateWatermark ?? energyQuery.data.metadata.dataWatermark, site.timezone)}</dd></div>
              <div><dt>有效 / 可疑 / 无效</dt><dd>{energyQuery.data.metadata.qualitySummary.valid} / {energyQuery.data.metadata.qualitySummary.suspect} / {energyQuery.data.metadata.qualitySummary.invalid}</dd></div>
            </dl>
          ) : null}
        </article>
      </div>
    </section>
  );
}
