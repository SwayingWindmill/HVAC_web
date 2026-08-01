import { lazy, Suspense, useCallback, useEffect, useMemo, useState } from 'react';
import { useInfiniteQuery, useQuery, useQueryClient } from '@tanstack/react-query';
import type { CurrentPrincipalResponse, Site } from '@/api/generated/platformGateway.gen';
import {
  ALARM_LOCAL_ROUTES_ENABLED,
  ALARM_PUBLIC_ROUTES_ENABLED,
  alarmErrorMessage,
  getScopedAlarm,
  listScopedAlarms,
  type AlarmSeverity,
  type AlarmStatus,
  type ScopedAlarmRequestOptions,
} from '@/api/alarms';
import type { ProtectedScopeDraft, ProtectedScopeResource } from './protected-scope';
import { FocusHeading } from './FocusHeading';
import { alarmOperationLabel, alarmStatusLabel, projectRealAlarm } from './real-alarms-projection';
import './real-alarms.css';

interface RealAlarmsProps {
  site: Readonly<Site>;
  principal: CurrentPrincipalResponse;
  registerUnsavedDraft: (draft: ProtectedScopeDraft) => () => void;
  registerProtectedResource: (resource: ProtectedScopeResource) => () => void;
}

const ALARM_QUERY_PARAMETER = 'alarm';
const LocalAlarmLifecycle = ALARM_LOCAL_ROUTES_ENABLED
  ? lazy(() => import('./LocalAlarmLifecycle'))
  : null;

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

function DisabledAlarmSurface({
  site,
  principal,
  reason,
}: Pick<RealAlarmsProps, 'site' | 'principal'> & { reason: 'ROUTE_DISABLED' | 'CAPABILITY_DENIED' }) {
  const capabilityDenied = reason === 'CAPABILITY_DENIED';
  return (
    <section className="real-alarms" data-testid="real-alarms-disabled" data-business-state="DISABLED" data-site-id={site.id}>
      <p className="real-shell-eyebrow">REAL MODE · SITE ALARMS</p>
      <FocusHeading>Alarm</FocusHeading>
      <div className="real-alarms__boundary" role="status">
        <strong>{capabilityDenied ? '当前会话没有 Alarm 列表能力' : 'Alarm 读取路由未启用'}</strong>
        <p>{capabilityDenied
          ? 'IAM 未向当前 Principal 与 acting Organization 发布 alarm.list。浏览器不会根据角色标签或 Site 访问权自行推导 Alarm 权限。'
          : 'S4 Alarm 读取尚未对当前构建开放。此页面不会从 Telemetry、Presence 或 Device 状态补造 Alarm。'}</p>
      </div>
      <dl className="real-alarms__facts" aria-label="Alarm authority boundary">
        <div><dt>Site</dt><dd>{site.displayName}</dd></div>
        <div><dt>Registry Site ID</dt><dd>{site.id}</dd></div>
        <div><dt>Acting Organization</dt><dd>{principal.context.actingOrganizationId}</dd></div>
        <div><dt>权威读取</dt><dd>Platform Gateway → IAM → Alarm Service</dd></div>
        <div><dt>生命周期写入</dt><dd>生产禁用</dd></div>
      </dl>
      <p className="real-alarms__honesty">没有 Alarm 数据不等于设备健康；只有 Alarm Service 发布并持久化的 lifecycle 才能显示为 Alarm。</p>
    </section>
  );
}

export function RealAlarms(props: RealAlarmsProps) {
  if (!ALARM_PUBLIC_ROUTES_ENABLED && !ALARM_LOCAL_ROUTES_ENABLED) {
    return <DisabledAlarmSurface site={props.site} principal={props.principal} reason="ROUTE_DISABLED" />;
  }
  if (!props.principal.authorization.capabilities.includes('alarm.list')) {
    return <DisabledAlarmSurface site={props.site} principal={props.principal} reason="CAPABILITY_DENIED" />;
  }
  return <AlarmWorkbench {...props} />;
}

function AlarmWorkbench({
  site,
  principal,
  registerUnsavedDraft,
  registerProtectedResource,
}: RealAlarmsProps) {
  const queryClient = useQueryClient();
  const organizationId = principal.context.actingOrganizationId;
  const queryPrefix = useMemo(() => ['real-alarms', organizationId, site.id] as const, [organizationId, site.id]);
  const [status, setStatus] = useState<AlarmStatus | ''>('');
  const [severity, setSeverity] = useState<AlarmSeverity | ''>('');
  const [selectedAlarmId, setSelectedAlarmId] = useState(alarmFromLocation);
  const canReadDetail = principal.authorization.capabilities.includes('alarm.read');

  const purgeAlarmState = useCallback(async () => {
    await queryClient.cancelQueries({ queryKey: queryPrefix });
    queryClient.removeQueries({ queryKey: queryPrefix });
    setSelectedAlarmId('');
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
    enabled: selectedAlarmId.length > 0 && canReadDetail,
    staleTime: 15_000,
  });

  const publishAlarmId = useCallback((alarmId: string) => {
    if (!canReadDetail) return;
    const parameters = new URLSearchParams(globalThis.location.search);
    parameters.set(ALARM_QUERY_PARAMETER, alarmId);
    globalThis.history.pushState(null, '', `${globalThis.location.pathname}?${parameters.toString()}${globalThis.location.hash}`);
    setSelectedAlarmId(alarmId);
  }, [canReadDetail]);

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
        <span className="real-alarms__local-marker">{ALARM_LOCAL_ROUTES_ENABLED
          ? 'LOCAL / LIFECYCLE / PRODUCTION WRITES DISABLED'
          : 'GATEWAY / IAM AUTHORIZED / 1% INTERNAL CANARY'}</span>
      </header>

      <div className="real-alarms__boundary" role="status">
        <strong>{ALARM_LOCAL_ROUTES_ENABLED ? '本地 S4 权威生命周期' : '生产 Alarm 权威只读 canary'}</strong>
        <p>{ALARM_LOCAL_ROUTES_ENABLED
          ? '页面只读取和更新 Alarm Service 发布的 durable Alarm。每次写入都带 expected version、CSRF 和稳定 Idempotency-Key；公共生产 POST 路由保持 0%。'
          : '列表与详情经 Platform Gateway 和 IAM 精确授权后读取 Alarm Service。生命周期写入仍为 0%，Telemetry、Presence 和 Device 状态不会在浏览器中转译为 Alarm。'}</p>
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
                  <button type="button" disabled={!canReadDetail} onClick={() => publishAlarmId(alarm.alarmId)} aria-current={selectedAlarmId === alarm.alarmId ? 'true' : undefined}>
                    <span className="real-alarms__list-heading"><strong>{alarm.title}</strong><em>{projection.severityLabel}</em></span>
                    <span>{projection.statusLabel} · {projection.occurrenceLabel}</span>
                    <small>{projection.sourceLabel} · {formatInstant(alarm.lastOccurredAt, site.timezone)}</small>
                  </button>
                </li>
              );
            })}
          </ol>

          <section className="real-alarms__detail-shell" aria-label="Alarm detail">
            {!canReadDetail ? (
              <div className="real-alarms__empty" data-testid="real-alarm-detail-denied">
                <strong>当前会话没有 Alarm 详情能力</strong>
                <p>IAM 未发布 alarm.read。列表不会被浏览器角色或其他 Site 能力扩展为详情访问。</p>
              </div>
            ) : null}
            {canReadDetail && !selectedAlarmId ? <div className="real-alarms__empty"><strong>选择一条 Alarm 查看权威详情</strong></div> : null}
            {canReadDetail && selectedAlarmId && detailQuery.isPending ? <div className="real-shell-progress" role="status">正在读取 Alarm 详情…</div> : null}
            {detailQuery.isError ? <div className="real-shell-problem" role="alert">{alarmErrorMessage(detailQuery.error)}</div> : null}
            {detail && detailProjection ? (
              <article className="real-alarms__detail" data-testid="real-alarm-detail" data-alarm-status={detail.status} data-alarm-version={detail.version}>
                <div className="real-alarms__detail-heading">
                  <div><h2>{detail.title}</h2><p>{detail.summary}</p></div>
                  <strong>{detailProjection.severityLabel} · {detailProjection.statusLabel}</strong>
                </div>
                <dl className="real-alarms__facts" aria-label="Alarm authoritative projection">
                  <div><dt>Alarm ID</dt><dd><code>{detail.alarmId}</code></dd></div>
                  <div><dt>Site ID</dt><dd><code>{detail.siteId}</code></dd></div>
                  <div><dt>Device ID</dt><dd>{detail.deviceId ? <code>{detail.deviceId}</code> : '不适用'}</dd></div>
                  <div><dt>Source</dt><dd>{detailProjection.sourceLabel} · <code>{detail.sourceReference}</code></dd></div>
                  <div><dt>指派对象</dt><dd>{detail.assigneeId ?? '未指派'}</dd></div>
                  <div><dt>抑制截止</dt><dd>{detail.suppressedUntil ? formatInstant(detail.suppressedUntil, site.timezone) : '不适用'}</dd></div>
                  <div><dt>发生次数</dt><dd>{detail.occurrenceCount}</dd></div>
                  <div><dt>首次发生</dt><dd>{formatInstant(detail.firstOccurredAt, site.timezone)}</dd></div>
                  <div><dt>最近发生</dt><dd>{formatInstant(detail.lastOccurredAt, site.timezone)}</dd></div>
                  <div><dt>Alarm Version</dt><dd>{detail.version}</dd></div>
                </dl>

                {LocalAlarmLifecycle ? (
                  <Suspense fallback={<div className="real-shell-progress" role="status">正在载入本地 Alarm 生命周期工具…</div>}>
                    <LocalAlarmLifecycle
                      alarm={detail}
                      projection={detailProjection}
                      site={site}
                      principal={principal}
                      queryPrefix={queryPrefix}
                      registerUnsavedDraft={registerUnsavedDraft}
                    />
                  </Suspense>
                ) : (
                  <section className="real-alarms__lifecycle" data-testid="real-alarm-read-only" aria-labelledby="real-alarm-read-only-title">
                    <h3 id="real-alarm-read-only-title">只读 Alarm canary</h3>
                    <p>当前生产阶段仅允许经 Gateway 与 IAM 授权的列表和详情读取。确认、指派、抑制、关闭和重新打开仍保持 0% 公共流量。</p>
                  </section>
                )}

                <section className="real-alarms__evidence" aria-labelledby="real-alarm-evidence-title">
                  <h3 id="real-alarm-evidence-title">证据引用</h3>
                  {detail.evidence.length === 0 ? <p>Alarm owner 未附带外部证据引用。</p> : (
                    <ul>{detail.evidence.map((evidence) => <li key={`${evidence.kind}:${evidence.reference}`}><strong>{evidence.kind}</strong><code>{evidence.reference}</code><small>{formatInstant(evidence.capturedAt, site.timezone)}</small></li>)}</ul>
                  )}
                </section>
                <section className="real-alarms__timeline" aria-labelledby="real-alarm-timeline-title">
                  <h3 id="real-alarm-timeline-title">生命周期时间线</h3>
                  <ol>{detail.transitions.map((transition) => (
                    <li key={transition.version}>
                      <strong>{alarmStatusLabel(transition.toStatus)}{transition.operation ? ` · ${alarmOperationLabel(transition.operation)}` : ''}</strong>
                      <span>{transition.reason}</span>
                      {transition.assigneeId ? <span>指派：{transition.assigneeId}</span> : null}
                      {transition.suppressedUntil ? <span>抑制截止：{formatInstant(transition.suppressedUntil, site.timezone)}</span> : null}
                      <small>{transition.actorType}{transition.actorId ? ` · ${transition.actorId}` : ''} · v{transition.version} · {formatInstant(transition.occurredAt, site.timezone)}</small>
                    </li>
                  ))}</ol>
                </section>
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
