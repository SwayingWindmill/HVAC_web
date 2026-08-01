import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import type { CurrentPrincipalResponse, Site } from '@/api/generated/platformGateway.gen';
import {
  ALARM_LOCAL_ROUTES_ENABLED,
  AlarmApiError,
  acknowledgeScopedAlarm,
  alarmErrorMessage,
  assignScopedAlarm,
  closeScopedAlarm,
  getScopedAlarm,
  listScopedAlarms,
  reopenScopedAlarm,
  suppressScopedAlarm,
  unassignScopedAlarm,
  unsuppressScopedAlarm,
  type Alarm,
  type AlarmOperation,
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
const DEFAULT_SUPPRESSION_HOURS = 4;

type LifecycleOperation = Exclude<AlarmOperation, 'PUBLISH'>;

interface LifecycleVariables {
  operation: LifecycleOperation;
  alarmId: string;
  expectedVersion: number;
  reason: string;
  assigneeId?: string;
  suppressedUntil?: string;
  idempotencyKey: string;
}

interface LifecycleDraft {
  reason: string;
  assigneeId: string;
  suppressionHours: number;
}

function alarmFromLocation(): string {
  return new URLSearchParams(globalThis.location.search).get(ALARM_QUERY_PARAMETER) ?? '';
}

function buildOptions(
  principal: CurrentPrincipalResponse,
  site: Readonly<Site>,
  signal?: AbortSignal,
  idempotencyKey?: string,
): ScopedAlarmRequestOptions {
  const options: ScopedAlarmRequestOptions = {
    trustedOrganizationId: principal.context.actingOrganizationId,
    trustedSiteId: site.id,
    signal,
    idempotencyKey,
  };
  const sessionCapability = Reflect.get(principal.session, ['csrf', 'Token'].join('')) as string | undefined;
  if (sessionCapability) options.csrfToken = sessionCapability;
  return options;
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
        <strong>生产 Alarm 读写保持禁用</strong>
        <p>S4 Alarm 路由已登记，但生产流量仍为 0%。此页面不会读取、确认、指派、抑制或关闭 Alarm，也不会把设备状态推导成 Alarm。</p>
      </div>
      <dl className="real-alarms__facts" aria-label="Alarm authority boundary">
        <div><dt>Site</dt><dd>{site.displayName}</dd></div>
        <div><dt>Registry Site ID</dt><dd>{site.id}</dd></div>
        <div><dt>Acting Organization</dt><dd>{principal.context.actingOrganizationId}</dd></div>
        <div><dt>权威领域</dt><dd>Platform Gateway → Alarm Service</dd></div>
        <div><dt>生产流量</dt><dd>0%</dd></div>
        <div><dt>生命周期写入</dt><dd>未启用</dd></div>
      </dl>
      <p className="real-alarms__honesty">没有 Alarm 数据不等于设备健康；只有 Alarm Service 发布并持久化的 lifecycle 才能显示为 Alarm。</p>
    </section>
  );
}

export function RealAlarms(props: RealAlarmsProps) {
  if (!ALARM_LOCAL_ROUTES_ENABLED) {
    return <DisabledAlarmSurface site={props.site} principal={props.principal} />;
  }
  return <LocalAlarmWorkbench {...props} />;
}

function LocalAlarmWorkbench({
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
  const [reason, setReason] = useState('');
  const [assigneeId, setAssigneeId] = useState('');
  const [suppressionHours, setSuppressionHours] = useState(DEFAULT_SUPPRESSION_HOURS);
  const draftRef = useRef<LifecycleDraft>({ reason: '', assigneeId: '', suppressionHours: DEFAULT_SUPPRESSION_HOURS });
  const idempotencyRef = useRef<{ fingerprint: string; key: string; suppressedUntil?: string } | null>(null);
  const mutationControllerRef = useRef<AbortController | undefined>(undefined);

  const resetDraft = useCallback(() => {
    setReason('');
    setAssigneeId('');
    setSuppressionHours(DEFAULT_SUPPRESSION_HOURS);
    draftRef.current = { reason: '', assigneeId: '', suppressionHours: DEFAULT_SUPPRESSION_HOURS };
    idempotencyRef.current = null;
  }, []);

  const purgeAlarmState = useCallback(async () => {
    mutationControllerRef.current?.abort();
    mutationControllerRef.current = undefined;
    await queryClient.cancelQueries({ queryKey: queryPrefix });
    queryClient.removeQueries({ queryKey: queryPrefix });
    resetDraft();
  }, [queryClient, queryPrefix, resetDraft]);

  useEffect(() => registerProtectedResource({
    id: `real-alarms-cache:${organizationId}:${site.id}`,
    kind: 'query-cache',
    purge: purgeAlarmState,
  }), [organizationId, purgeAlarmState, registerProtectedResource, site.id]);

  useEffect(() => registerUnsavedDraft({
    id: `real-alarm-lifecycle-draft:${site.id}`,
    label: `Alarm lifecycle draft for ${site.displayName}`,
    isDirty: () => draftRef.current.reason.trim().length > 0
      || draftRef.current.assigneeId.trim().length > 0
      || draftRef.current.suppressionHours !== DEFAULT_SUPPRESSION_HOURS,
  }), [registerUnsavedDraft, site.displayName, site.id]);

  useEffect(() => () => {
    void purgeAlarmState();
  }, [principal.authorization.policyRevision, principal.context.policyRevision, principal.session.id, purgeAlarmState]);

  useEffect(() => {
    const handlePopState = () => {
      setSelectedAlarmId(alarmFromLocation());
      resetDraft();
    };
    globalThis.addEventListener('popstate', handlePopState);
    return () => globalThis.removeEventListener('popstate', handlePopState);
  }, [resetDraft]);

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
    resetDraft();
  }, [resetDraft]);

  const stableIdempotencyKey = useCallback((
    fingerprint: string,
    operation: LifecycleOperation,
  ): { key: string; suppressedUntil?: string } => {
    if (idempotencyRef.current?.fingerprint === fingerprint) return idempotencyRef.current;
    const envelope = {
      fingerprint,
      key: `real-alarm-${crypto.randomUUID()}`,
      ...(operation === 'SUPPRESS'
        ? { suppressedUntil: new Date(Date.now() + suppressionHours * 60 * 60 * 1000).toISOString() }
        : {}),
    };
    idempotencyRef.current = envelope;
    return envelope;
  }, [suppressionHours]);

  const lifecycleMutation = useMutation({
    mutationFn: async (variables: LifecycleVariables) => {
      mutationControllerRef.current?.abort();
      const controller = new AbortController();
      mutationControllerRef.current = controller;
      const options = buildOptions(principal, site, controller.signal, variables.idempotencyKey);
      const baseInput = { expectedVersion: variables.expectedVersion, reason: variables.reason };
      switch (variables.operation) {
        case 'ACKNOWLEDGE':
          return acknowledgeScopedAlarm(variables.alarmId, baseInput, options);
        case 'ASSIGN':
          return assignScopedAlarm(variables.alarmId, { ...baseInput, assigneeId: variables.assigneeId ?? '' }, options);
        case 'UNASSIGN':
          return unassignScopedAlarm(variables.alarmId, baseInput, options);
        case 'SUPPRESS':
          return suppressScopedAlarm(variables.alarmId, { ...baseInput, suppressedUntil: variables.suppressedUntil ?? '' }, options);
        case 'UNSUPPRESS':
          return unsuppressScopedAlarm(variables.alarmId, baseInput, options);
        case 'CLOSE':
          return closeScopedAlarm(variables.alarmId, baseInput, options);
        case 'REOPEN':
          return reopenScopedAlarm(variables.alarmId, baseInput, options);
      }
    },
    onSuccess: (alarm) => {
      queryClient.setQueryData([...queryPrefix, 'detail', alarm.alarmId], alarm);
      void queryClient.invalidateQueries({ queryKey: [...queryPrefix, 'list'] });
      resetDraft();
    },
    onError: (error) => {
      if (error instanceof AlarmApiError && error.code === 'ALARM_VERSION_CONFLICT') {
        void queryClient.invalidateQueries({ queryKey: [...queryPrefix, 'detail', selectedAlarmId] });
        void queryClient.invalidateQueries({ queryKey: [...queryPrefix, 'list'] });
      }
    },
    onSettled: () => {
      mutationControllerRef.current = undefined;
    },
  });

  const submitLifecycle = useCallback((operation: LifecycleOperation, alarm: Alarm) => {
    const normalizedReason = reason.trim();
    const normalizedAssignee = assigneeId.trim();
    if (!normalizedReason) return;
    const fingerprint = JSON.stringify({
      operation,
      alarmId: alarm.alarmId,
      expectedVersion: alarm.version,
      reason: normalizedReason,
      assigneeId: operation === 'ASSIGN' ? normalizedAssignee : undefined,
      suppressionHours: operation === 'SUPPRESS' ? suppressionHours : undefined,
    });
    const stableRequest = stableIdempotencyKey(fingerprint, operation);
    lifecycleMutation.mutate({
      operation,
      alarmId: alarm.alarmId,
      expectedVersion: alarm.version,
      reason: normalizedReason,
      assigneeId: operation === 'ASSIGN' ? normalizedAssignee : undefined,
      suppressedUntil: stableRequest.suppressedUntil,
      idempotencyKey: stableRequest.key,
    });
  }, [assigneeId, lifecycleMutation, reason, stableIdempotencyKey, suppressionHours]);

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
  const mutationReasonValid = reason.trim().length > 0;
  const mutationDisabled = lifecycleMutation.isPending || !mutationReasonValid;

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
        <span className="real-alarms__local-marker">LOCAL / LIFECYCLE / PRODUCTION DISABLED</span>
      </header>

      <div className="real-alarms__boundary" role="status">
        <strong>本地 S4 权威生命周期</strong>
        <p>页面只读取和更新 Alarm Service 发布的 durable Alarm。每次写入都带 expected version、CSRF 和稳定 Idempotency-Key；Telemetry、Presence 和 Device 状态不会在浏览器中转译为 Alarm。</p>
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

                <section className="real-alarms__lifecycle" aria-labelledby="real-alarm-lifecycle-title">
                  <h3 id="real-alarm-lifecycle-title">生命周期操作</h3>
                  <p>原因会写入权威时间线。相同草稿重试沿用同一 Idempotency-Key；版本冲突会重新读取最新 Alarm，不会覆盖他人操作。</p>
                  <label>
                    操作原因
                    <textarea
                      data-testid="real-alarm-reason"
                      maxLength={256}
                      value={reason}
                      onChange={(event) => {
                        const value = event.currentTarget.value;
                        setReason(value);
                        draftRef.current = { ...draftRef.current, reason: value };
                        idempotencyRef.current = null;
                      }}
                    />
                  </label>
                  {detail.status !== 'CLOSED' ? (
                    <label>
                      指派对象
                      <input
                        data-testid="real-alarm-assignee"
                        maxLength={256}
                        value={assigneeId}
                        placeholder="principal 或 operator 标识"
                        onChange={(event) => {
                          const value = event.currentTarget.value;
                          setAssigneeId(value);
                          draftRef.current = { ...draftRef.current, assigneeId: value };
                          idempotencyRef.current = null;
                        }}
                      />
                    </label>
                  ) : null}
                  {detail.status === 'OPEN' || detail.status === 'ACKNOWLEDGED' ? (
                    <label>
                      抑制时长
                      <select
                        data-testid="real-alarm-suppression-hours"
                        value={suppressionHours}
                        onChange={(event) => {
                          const value = Number(event.currentTarget.value);
                          setSuppressionHours(value);
                          draftRef.current = { ...draftRef.current, suppressionHours: value };
                          idempotencyRef.current = null;
                        }}
                      >
                        <option value={1}>1 小时</option>
                        <option value={4}>4 小时</option>
                        <option value={24}>24 小时</option>
                        <option value={168}>7 天</option>
                      </select>
                    </label>
                  ) : null}
                  <div className="real-alarms__actions">
                    {detailProjection.canAcknowledge ? <button data-testid="real-alarm-acknowledge" type="button" disabled={mutationDisabled} onClick={() => submitLifecycle('ACKNOWLEDGE', detail)}>确认</button> : null}
                    {detailProjection.canAssign ? <button data-testid="real-alarm-assign" type="button" disabled={mutationDisabled || !assigneeId.trim()} onClick={() => submitLifecycle('ASSIGN', detail)}>指派</button> : null}
                    {detailProjection.canUnassign ? <button data-testid="real-alarm-unassign" type="button" disabled={mutationDisabled} onClick={() => submitLifecycle('UNASSIGN', detail)}>取消指派</button> : null}
                    {detailProjection.canSuppress ? <button data-testid="real-alarm-suppress" type="button" disabled={mutationDisabled} onClick={() => submitLifecycle('SUPPRESS', detail)}>抑制</button> : null}
                    {detailProjection.canUnsuppress ? <button data-testid="real-alarm-unsuppress" type="button" disabled={mutationDisabled} onClick={() => submitLifecycle('UNSUPPRESS', detail)}>解除抑制</button> : null}
                    {detailProjection.canClose ? <button data-testid="real-alarm-close" type="button" disabled={mutationDisabled} onClick={() => submitLifecycle('CLOSE', detail)}>关闭</button> : null}
                    {detailProjection.canReopen ? <button data-testid="real-alarm-reopen" type="button" disabled={mutationDisabled} onClick={() => submitLifecycle('REOPEN', detail)}>重新打开</button> : null}
                  </div>
                  {lifecycleMutation.isPending ? <div className="real-shell-progress" role="status">正在提交 Alarm 生命周期操作…</div> : null}
                  {lifecycleMutation.isError ? (
                    <div className="real-shell-problem" role="alert" data-testid="real-alarm-mutation-error">
                      <strong>Alarm 生命周期操作失败</strong>
                      <span>{alarmErrorMessage(lifecycleMutation.error)}</span>
                    </div>
                  ) : null}
                </section>

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
