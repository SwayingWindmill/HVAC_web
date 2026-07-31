import { useCallback, useEffect, useMemo, useState } from 'react';
import { useInfiniteQuery, useQuery, useQueryClient } from '@tanstack/react-query';
import type { CurrentPrincipalResponse, Site } from '@/api/generated/platformGateway.gen';
import {
  ALARM_LOCAL_ROUTES_ENABLED,
  alarmErrorMessage,
  getScopedAlarm,
  listScopedAlarms,
  type AlarmSeverity,
  type AlarmStatus,
  type ScopedAlarmRequestOptions,
} from '@/api/alarms';
import type { ProtectedScopeResource } from './protected-scope';
import { FocusHeading } from './FocusHeading';
import { alarmStatusLabel, projectRealAlarm } from './real-alarms-projection';
import './real-alarms.css';

interface RealAlarmsProps {
  site: Readonly<Site>;
  principal: CurrentPrincipalResponse;
  registerProtectedResource: (resource: ProtectedScopeResource) => () => void;
}

const ALARM_QUERY_PARAMETER = 'alarm';

function alarmFromLocation(): string {
  return new URLSearchParams(globalThis.location.search).get(ALARM_QUERY_PARAMETER) ?? '';
}

function buildOptions(
  principal: CurrentPrincipalResponse,
  site: Readonly<Site>,
  signal?: AbortSignal,
): ScopedAlarmRequestOptions {
  return {
    trustedOrganizationId: principal.context.actingOrganizationId,
    trustedSiteId: site.id,
    signal,
  };
}

function formatInstant(value: string, timeZone: string): string {
  return new Intl.DateTimeFormat('zh-CN', {
    timeZone,
    dateStyle: 'medium',
    timeStyle: 'medium',
  }).format(new Date(value));
}

function DisabledAlarmSurface({ site, principal }: Pick<RealAlarmsProps, 'site' | 'principal'>) {
  return (
    <section className="real-alarms" data-testid="real-alarms-disabled" data-business-state="DISABLED" data-site-id={site.id}>
      <p className="real-shell-eyebrow">REAL MODE · SITE ALARMS</p>
      <FocusHeading>Alarm</FocusHeading>
      <div className="real-alarms__boundary" role="status">
        <strong>生产 Alarm 读取保持禁用</strong>
        <p>S4 Alarm 路由已登记，但生产流量仍为 0%。此页面不会把设备离线、Telemetry stale 或质量异常推导成 Alarm。</p>
      </div>
      <dl className="real-alarms__facts" aria-label="Alarm authority boundary">
        <div><dt>Site</dt><dd>{site.displayName}</dd></div>
        <div><dt>Registry Site ID</dt><dd>{site.id}</dd></div>
        <div><dt>Acting Organization</dt><dd>{principal.context.actingOrganizationId}</dd></div>
        <div><dt>权威领域</dt><dd>Platform Gateway → Alarm Service</dd></div>
        <div><dt>生产流量</dt><dd>0%</dd></div>
        <div><dt>写操作</dt><dd>未定义</dd></div>
      </dl>
      <p className="real-alarms__honesty">没有 Alarm 数据不等于设备健康；只有 Alarm Service 发布的 durable lifecycle 才能显示为 Alarm。</p>
    </section>
  );
}

export function RealAlarms(props: RealAlarmsProps) {
  if (!ALARM_LOCAL_ROUTES_ENABLED) {
    return <DisabledAlarmSurface site={props.site} principal={props.principal} />;
  }
  return <LocalAlarmWorkbench {...props} />;
}

function LocalAlarmWorkbench({ site, principal, registerProtectedResource }: RealAlarmsProps) {
  const queryClient = useQueryClient();
  const organizationId = principal.context.actingOrganizationId;
  const queryPrefix = useMemo(() => ['real-alarms', organizationId, site.id] as const, [organizationId, site.id]);
  const [status, setStatus] = useState<AlarmStatus | ''>('');
  const [severity, setSeverity] = useState<AlarmSeverity | ''>('');
  const [selectedAlarmId, setSelectedAlarmId] = useState(alarmFromLocation);

  const purgeAlarmState = useCallback(async () => {
    await queryClient.cancelQueries({ queryKey: queryPrefix });
    queryClient.removeQueries({ queryKey: queryPrefix });
  }, [queryClient, queryPrefix]);

  useEffect(() => registerProtectedResource({
    id: `real-alarms-cache:${organizationId}:${site.id}`,
    kind: 'query-cache',
    purge: purgeAlarmState,
  }), [organizationId, purgeAlarmState, registerProtectedResource, site.id]);

  useEffect(() => () => {
    void purgeAlarmState();
  }, [principal.authorization.policyRevision, principal.context.policyRevision, principal.session.id, purgeAlarmState]);

  useEffect(() => {
    const handlePopState = () => setSelectedAlarmId(alarmFromLocation());
    globalThis.addEventListener('popstate', handlePopState);
    return () => globalThis.removeEventListener('popstate', handlePopState);
  }, []);

  const listQuery = useInfiniteQuery({
    queryKey: [...queryPrefix, 'list', status || 'ALL', severity || 'ALL'],
    initialPageParam: null as string | null,
    queryFn: ({ signal, pageParam }) => listScopedAlarms({
      status: status || undefined,
      severity: severity || undefined,
      cursor: pageParam ?? undefined,
      limit: 50,
    }, buildOptions(principal, site, signal)),
    getNextPageParam: (lastPage) => lastPage.nextCursor ?? undefined,
    staleTime: 30_000,
  });

  const alarms = useMemo(() => {
    const seen = new Set<string>();
    return (listQuery.data?.pages ?? []).flatMap((page) => page.items).filter((alarm) => {
      if (seen.has(alarm.alarmId)) return false;
      seen.add(alarm.alarmId);
      return true;
    });
  }, [listQuery.data]);

  const detailQuery = useQuery({
    queryKey: [...queryPrefix, 'detail', selectedAlarmId],
    queryFn: ({ signal }) => getScopedAlarm(selectedAlarmId, buildOptions(principal, site, signal)),
    enabled: selectedAlarmId.length > 0,
    staleTime: 15_000,
  });

  const publishAlarmId = useCallback((alarmId: string) => {
    const parameters = new URLSearchParams(globalThis.location.search);
    parameters.set(ALARM_QUERY_PARAMETER, alarmId);
    globalThis.history.pushState(null, '', `${globalThis.location.pathname}?${parameters.toString()}${globalThis.location.hash}`);
    setSelectedAlarmId(alarmId);
  }, []);

  const businessState = listQuery.isPending
    ? 'LOADING'
    : listQuery.isError && alarms.length === 0
      ? 'UNAVAILABLE'
      : listQuery.isFetchNextPageError
        ? 'PARTIAL'
        : alarms.length === 0
          ? 'EMPTY'
          : 'READY';
  const detail = detailQuery.data;
  const detailProjection = detail ? projectRealAlarm(detail) : null;

  return (
    <section
      className="real-alarms"
      data-testid="real-alarms-workbench"
      data-business-state={businessState}
      data-site-id={site.id}
      data-alarm-id={detail?.alarmId ?? ''}
    >
      <header className="real-alarms__header">
        <div>
          <p className="real-shell-eyebrow">REAL MODE · SITE ALARMS</p>
          <FocusHeading>Alarm</FocusHeading>
          <p>{site.displayName} · {site.code} · {site.timezone}</p>
        </div>
        <span className="real-alarms__local-marker">LOCAL / READ-ONLY / PRODUCTION DISABLED</span>
      </header>

      <div className="real-alarms__boundary" role="status">
        <strong>本地 S4 权威读模型</strong>
        <p>页面只读取 Alarm Service 发布的 durable Alarm。Telemetry、Presence 和 Device 状态不会在浏览器中转译为 Alarm。</p>
      </div>

      <section className="real-alarms__filters" aria-label="Alarm filters">
        <label>
          生命周期
          <select data-testid="real-alarm-status-filter" value={status} onChange={(event) => setStatus(event.currentTarget.value as AlarmStatus | '')}>
            <option value="">全部</option>
            <option value="OPEN">未处理</option>
            <option value="ACKNOWLEDGED">已确认</option>
            <option value="SUPPRESSED">已抑制</option>
            <option value="CLOSED">已关闭</option>
          </select>
        </label>
        <label>
          严重度
          <select data-testid="real-alarm-severity-filter" value={severity} onChange={(event) => setSeverity(event.currentTarget.value as AlarmSeverity | '')}>
            <option value="">全部</option>
            <option value="CRITICAL">严重</option>
            <option value="MAJOR">重要</option>
            <option value="WARNING">警告</option>
            <option value="INFO">提示</option>
          </select>
        </label>
      </section>

      {listQuery.isPending ? <div className="real-shell-progress" role="status">正在读取当前 Site 的权威 Alarm…</div> : null}
      {listQuery.isError && alarms.length === 0 ? (
        <div className="real-shell-problem" role="alert">
          <strong>Alarm 列表不可用</strong>
          <span>{alarmErrorMessage(listQuery.error)}</span>
        </div>
      ) : null}
      {alarms.length === 0 && !listQuery.isPending && !listQuery.isError ? (
        <div className="real-alarms__empty" data-testid="real-alarms-empty">
          <strong>当前筛选条件下为 0 条 Alarm</strong>
          <p>这是权威空集合，不表示设备健康，也不会从 Telemetry 补造 Alarm。</p>
        </div>
      ) : null}

      {alarms.length > 0 ? (
        <div className="real-alarms__workspace">
          <ol className="real-alarms__list" aria-label="Authoritative Alarm list">
            {alarms.map((alarm) => {
              const projection = projectRealAlarm(alarm);
              return (
                <li key={alarm.alarmId} data-alarm-status={alarm.status} data-alarm-severity={alarm.severity}>
                  <button type="button" onClick={() => publishAlarmId(alarm.alarmId)} aria-current={selectedAlarmId === alarm.alarmId ? 'true' : undefined}>
                    <span className="real-alarms__list-heading"><strong>{alarm.title}</strong><em>{projection.severityLabel}</em></span>
                    <span>{projection.statusLabel} · {projection.occurrenceLabel}</span>
                    <small>{projection.sourceLabel} · {formatInstant(alarm.lastOccurredAt, site.timezone)}</small>
                  </button>
                </li>
              );
            })}
          </ol>

          <section className="real-alarms__detail-shell" aria-label="Alarm detail">
            {!selectedAlarmId ? <div className="real-alarms__empty"><strong>选择一条 Alarm 查看权威详情</strong></div> : null}
            {selectedAlarmId && detailQuery.isPending ? <div className="real-shell-progress" role="status">正在读取 Alarm 详情…</div> : null}
            {detailQuery.isError ? <div className="real-shell-problem" role="alert">{alarmErrorMessage(detailQuery.error)}</div> : null}
            {detail && detailProjection ? (
              <article className="real-alarms__detail" data-testid="real-alarm-detail" data-alarm-status={detail.status}>
                <div className="real-alarms__detail-heading">
                  <div><h2>{detail.title}</h2><p>{detail.summary}</p></div>
                  <strong>{detailProjection.severityLabel} · {detailProjection.statusLabel}</strong>
                </div>
                <dl className="real-alarms__facts" aria-label="Alarm authoritative projection">
                  <div><dt>Alarm ID</dt><dd><code>{detail.alarmId}</code></dd></div>
                  <div><dt>Site ID</dt><dd><code>{detail.siteId}</code></dd></div>
                  <div><dt>Device ID</dt><dd>{detail.deviceId ? <code>{detail.deviceId}</code> : '不适用'}</dd></div>
                  <div><dt>Source</dt><dd>{detailProjection.sourceLabel} · <code>{detail.sourceReference}</code></dd></div>
                  <div><dt>发生次数</dt><dd>{detail.occurrenceCount}</dd></div>
                  <div><dt>首次发生</dt><dd>{formatInstant(detail.firstOccurredAt, site.timezone)}</dd></div>
                  <div><dt>最近发生</dt><dd>{formatInstant(detail.lastOccurredAt, site.timezone)}</dd></div>
                  <div><dt>Alarm Version</dt><dd>{detail.version}</dd></div>
                </dl>
                <section className="real-alarms__evidence" aria-labelledby="real-alarm-evidence-title">
                  <h3 id="real-alarm-evidence-title">证据引用</h3>
                  {detail.evidence.length === 0 ? <p>Alarm owner 未附带外部证据引用。</p> : (
                    <ul>{detail.evidence.map((evidence) => <li key={`${evidence.kind}:${evidence.reference}`}><strong>{evidence.kind}</strong><code>{evidence.reference}</code><small>{formatInstant(evidence.capturedAt, site.timezone)}</small></li>)}</ul>
                  )}
                </section>
                <section className="real-alarms__timeline" aria-labelledby="real-alarm-timeline-title">
                  <h3 id="real-alarm-timeline-title">生命周期时间线</h3>
                  <ol>{detail.transitions.map((transition) => <li key={transition.version}><strong>{alarmStatusLabel(transition.toStatus)}</strong><span>{transition.reason}</span><small>{transition.actorType} · v{transition.version} · {formatInstant(transition.occurredAt, site.timezone)}</small></li>)}</ol>
                </section>
                <p className="real-alarms__read-only">此切片只读。确认、指派、抑制和关闭必须通过后续独立认证的状态转换契约。</p>
              </article>
            ) : null}
          </section>
        </div>
      ) : null}

      {listQuery.hasNextPage ? (
        <button className="real-alarms__load-more" type="button" disabled={listQuery.isFetchingNextPage} onClick={() => void listQuery.fetchNextPage()}>
          {listQuery.isFetchingNextPage ? '正在读取…' : '加载更多 Alarm'}
        </button>
      ) : null}
      {listQuery.isFetchNextPageError ? <div className="real-shell-problem" role="alert">已保留当前结果；下一页暂时不可用。</div> : null}
    </section>
  );
}
