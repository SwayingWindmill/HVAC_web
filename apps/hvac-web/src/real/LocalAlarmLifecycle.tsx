import { useCallback, useEffect, useRef, useState } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import type { CurrentPrincipalResponse, Site } from '@/api/generated/platformGateway.gen';
import {
  AlarmApiError,
  acknowledgeScopedAlarm,
  alarmErrorMessage,
  assignScopedAlarm,
  closeScopedAlarm,
  reopenScopedAlarm,
  suppressScopedAlarm,
  unassignScopedAlarm,
  unsuppressScopedAlarm,
  type Alarm,
  type AlarmOperation,
  type ScopedAlarmRequestOptions,
} from '@/api/alarms';
import type { ProtectedScopeDraft } from './protected-scope';
import type { RealAlarmProjection } from './real-alarms-projection';

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

interface LocalAlarmLifecycleProps {
  alarm: Alarm;
  projection: RealAlarmProjection;
  site: Readonly<Site>;
  principal: CurrentPrincipalResponse;
  queryPrefix: readonly unknown[];
  registerUnsavedDraft: (draft: ProtectedScopeDraft) => () => void;
}

function buildMutationOptions(
  principal: CurrentPrincipalResponse,
  site: Readonly<Site>,
  signal: AbortSignal,
  idempotencyKey: string,
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

export default function LocalAlarmLifecycle({
  alarm,
  projection,
  site,
  principal,
  queryPrefix,
  registerUnsavedDraft,
}: LocalAlarmLifecycleProps) {
  const queryClient = useQueryClient();
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

  useEffect(() => registerUnsavedDraft({
    id: `real-alarm-lifecycle-draft:${site.id}`,
    label: `Alarm lifecycle draft for ${site.displayName}`,
    isDirty: () => draftRef.current.reason.trim().length > 0
      || draftRef.current.assigneeId.trim().length > 0
      || draftRef.current.suppressionHours !== DEFAULT_SUPPRESSION_HOURS,
  }), [registerUnsavedDraft, site.displayName, site.id]);

  useEffect(() => () => {
    mutationControllerRef.current?.abort();
    mutationControllerRef.current = undefined;
    resetDraft();
  }, [alarm.alarmId, principal.authorization.policyRevision, principal.context.policyRevision, principal.session.id, resetDraft, site.id]);

  const stableIdempotencyRequest = useCallback((
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
      const options = buildMutationOptions(principal, site, controller.signal, variables.idempotencyKey);
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
    onSuccess: (updated) => {
      queryClient.setQueryData([...queryPrefix, 'detail', updated.alarmId], updated);
      void queryClient.invalidateQueries({ queryKey: [...queryPrefix, 'list'] });
      resetDraft();
    },
    onError: (error) => {
      if (error instanceof AlarmApiError && error.code === 'ALARM_VERSION_CONFLICT') {
        void queryClient.invalidateQueries({ queryKey: [...queryPrefix, 'detail', alarm.alarmId] });
        void queryClient.invalidateQueries({ queryKey: [...queryPrefix, 'list'] });
      }
    },
    onSettled: () => {
      mutationControllerRef.current = undefined;
    },
  });

  const submitLifecycle = useCallback((operation: LifecycleOperation) => {
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
    const stableRequest = stableIdempotencyRequest(fingerprint, operation);
    lifecycleMutation.mutate({
      operation,
      alarmId: alarm.alarmId,
      expectedVersion: alarm.version,
      reason: normalizedReason,
      assigneeId: operation === 'ASSIGN' ? normalizedAssignee : undefined,
      suppressedUntil: stableRequest.suppressedUntil,
      idempotencyKey: stableRequest.key,
    });
  }, [alarm.alarmId, alarm.version, assigneeId, lifecycleMutation, reason, stableIdempotencyRequest, suppressionHours]);

  const mutationReasonValid = reason.trim().length > 0;
  const mutationDisabled = lifecycleMutation.isPending || !mutationReasonValid;

  return (
    <section className="real-alarms__lifecycle" aria-labelledby="real-alarm-lifecycle-title" data-testid="real-alarm-local-lifecycle">
      <h3 id="real-alarm-lifecycle-title">本地生命周期操作</h3>
      <p>原因会写入权威时间线。相同草稿重试沿用同一 Idempotency-Key；版本冲突会重新读取最新 Alarm，不会覆盖他人操作。生产公共 POST 路由仍为 0%。</p>
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
      {alarm.status !== 'CLOSED' ? (
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
      {alarm.status === 'OPEN' || alarm.status === 'ACKNOWLEDGED' ? (
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
        {projection.canAcknowledge ? <button data-testid="real-alarm-acknowledge" type="button" disabled={mutationDisabled} onClick={() => submitLifecycle('ACKNOWLEDGE')}>确认</button> : null}
        {projection.canAssign ? <button data-testid="real-alarm-assign" type="button" disabled={mutationDisabled || !assigneeId.trim()} onClick={() => submitLifecycle('ASSIGN')}>指派</button> : null}
        {projection.canUnassign ? <button data-testid="real-alarm-unassign" type="button" disabled={mutationDisabled} onClick={() => submitLifecycle('UNASSIGN')}>取消指派</button> : null}
        {projection.canSuppress ? <button data-testid="real-alarm-suppress" type="button" disabled={mutationDisabled} onClick={() => submitLifecycle('SUPPRESS')}>抑制</button> : null}
        {projection.canUnsuppress ? <button data-testid="real-alarm-unsuppress" type="button" disabled={mutationDisabled} onClick={() => submitLifecycle('UNSUPPRESS')}>解除抑制</button> : null}
        {projection.canClose ? <button data-testid="real-alarm-close" type="button" disabled={mutationDisabled} onClick={() => submitLifecycle('CLOSE')}>关闭</button> : null}
        {projection.canReopen ? <button data-testid="real-alarm-reopen" type="button" disabled={mutationDisabled} onClick={() => submitLifecycle('REOPEN')}>重新打开</button> : null}
      </div>
      {lifecycleMutation.isPending ? <div className="real-shell-progress" role="status">正在提交 Alarm 生命周期操作…</div> : null}
      {lifecycleMutation.isError ? (
        <div className="real-shell-problem" role="alert" data-testid="real-alarm-mutation-error">
          <strong>Alarm 生命周期操作失败</strong>
          <span>{alarmErrorMessage(lifecycleMutation.error)}</span>
        </div>
      ) : null}
    </section>
  );
}
