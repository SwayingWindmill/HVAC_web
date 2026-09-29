import { useCallback, useEffect, useRef, useState } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { CheckCircle2, LoaderCircle, ShieldAlert, UserRoundCog } from 'lucide-react';
import type { CurrentPrincipalResponse, Site } from '@/api/generated/platformGateway.gen';
import {
  AlarmApiError,
  acknowledgeScopedAlarm,
  alarmErrorMessage,
  assignScopedAlarm,
  suppressScopedAlarm,
  unassignScopedAlarm,
  unsuppressScopedAlarm,
  type Alarm,
  type AlarmOperation,
  type ScopedAlarmRequestOptions,
} from '@/api/alarms';
import type { ProtectedScopeDraft } from '@/app/protected-scope';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Field, FieldDescription, FieldGroup, FieldLabel } from '@/components/ui/field';
import { Input } from '@/components/ui/input';
import {
  SelectGroup,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Textarea } from '@/components/ui/textarea';
import type { AlarmProjection } from './alarm-projection';

const DEFAULT_SUPPRESSION_HOURS = 4;

type LifecycleOperation = Exclude<AlarmOperation, 'PUBLISH' | 'CLEAR'>;

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
  ackComment: string;
  assigneeId: string;
  suppressionHours: number;
}

interface LocalAlarmLifecycleProps {
  alarm: Alarm;
  projection: AlarmProjection;
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
    trustedTenantId: principal.context.tenantId,
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
  const [ackComment, setAckComment] = useState('');
  const [ackDialogOpen, setAckDialogOpen] = useState(false);
  const [assigneeId, setAssigneeId] = useState('');
  const [suppressionHours, setSuppressionHours] = useState(DEFAULT_SUPPRESSION_HOURS);
  const draftRef = useRef<LifecycleDraft>({ reason: '', ackComment: '', assigneeId: '', suppressionHours: DEFAULT_SUPPRESSION_HOURS });
  const idempotencyRef = useRef<{ fingerprint: string; key: string; suppressedUntil?: string } | null>(null);
  const mutationControllerRef = useRef<AbortController | undefined>(undefined);
  const ackConfirmRef = useRef<HTMLButtonElement>(null);

  const resetDraft = useCallback(() => {
    setReason('');
    setAckComment('');
    setAckDialogOpen(false);
    setAssigneeId('');
    setSuppressionHours(DEFAULT_SUPPRESSION_HOURS);
    draftRef.current = { reason: '', ackComment: '', assigneeId: '', suppressionHours: DEFAULT_SUPPRESSION_HOURS };
    idempotencyRef.current = null;
  }, []);

  useEffect(() => registerUnsavedDraft({
    id: `real-alarm-lifecycle-draft:${site.id}`,
    label: `Alarm lifecycle draft for ${site.displayName}`,
    isDirty: () => draftRef.current.reason.trim().length > 0
      || draftRef.current.ackComment.trim().length > 0
      || draftRef.current.assigneeId.trim().length > 0
      || draftRef.current.suppressionHours !== DEFAULT_SUPPRESSION_HOURS,
  }), [registerUnsavedDraft, site.displayName, site.id]);

  useEffect(() => () => {
    mutationControllerRef.current?.abort();
    mutationControllerRef.current = undefined;
    resetDraft();
  }, [alarm.alarmId, principal.authorization.policyRevision, principal.context.policyRevision, principal.session.id, resetDraft, site.id]);

  useEffect(() => {
    if (ackDialogOpen) ackConfirmRef.current?.focus({ preventScroll: true });
  }, [ackDialogOpen]);

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
          return acknowledgeScopedAlarm(variables.alarmId, { comment: variables.reason }, options);
        case 'ASSIGN':
          return assignScopedAlarm(variables.alarmId, { ...baseInput, assigneeId: variables.assigneeId ?? '' }, options);
        case 'UNASSIGN':
          return unassignScopedAlarm(variables.alarmId, baseInput, options);
        case 'SUPPRESS':
          return suppressScopedAlarm(variables.alarmId, { ...baseInput, suppressedUntil: variables.suppressedUntil ?? '' }, options);
        case 'UNSUPPRESS':
          return unsuppressScopedAlarm(variables.alarmId, baseInput, options);
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

  const submitLifecycle = useCallback((operation: LifecycleOperation, reasonOverride = reason) => {
    const normalizedReason = reasonOverride.trim();
    const normalizedAssignee = assigneeId.trim();
    if (operation !== 'ACKNOWLEDGE' && !normalizedReason) return;
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
    <section className="space-y-4" aria-labelledby="real-alarm-lifecycle-title" data-testid="real-alarm-local-lifecycle">
      <Card className="shadow-none">
        <CardHeader>
          <div className="flex items-center gap-2">
            <UserRoundCog className="size-4 text-muted-foreground" />
            <CardTitle id="real-alarm-lifecycle-title">处置操作</CardTitle>
          </div>
          <CardDescription>
            确认、指派和抑制只记录处置事实，不改变告警的物理活动或恢复状态。版本冲突会重新读取最新记录，不覆盖他人操作。
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <FieldGroup className="grid gap-4 lg:grid-cols-2">
            <Field>
              <FieldLabel htmlFor="alarm-operation-reason">操作原因</FieldLabel>
              <Textarea
                id="alarm-operation-reason"
                data-testid="real-alarm-reason"
                maxLength={256}
                value={reason}
                onChange={(event) => {
                  const value = event.currentTarget.value;
                  setReason(value);
                  draftRef.current = { ...draftRef.current, reason: value };
                  idempotencyRef.current = null;
                }}
                placeholder="填写指派、取消指派或抑制的业务原因"
              />
              <FieldDescription>原因会写入权威时间线；相同草稿重试沿用同一幂等请求。</FieldDescription>
            </Field>

            <FieldGroup className="gap-3">
              {alarm.condition === 'ACTIVE' ? (
                <Field>
                  <FieldLabel htmlFor="alarm-assignee">指派对象</FieldLabel>
                  <Input
                    id="alarm-assignee"
                    data-testid="real-alarm-assignee"
                    maxLength={256}
                    value={assigneeId}
                    placeholder="负责人标识"
                    onChange={(event) => {
                      const value = event.currentTarget.value;
                      setAssigneeId(value);
                      draftRef.current = { ...draftRef.current, assigneeId: value };
                      idempotencyRef.current = null;
                    }}
                  />
                </Field>
              ) : null}

              {projection.canSuppress ? (
                <Field>
                  <FieldLabel htmlFor="alarm-suppression-hours">抑制时长</FieldLabel>
                  <Select
                    value={String(suppressionHours)}
                    onValueChange={(value) => {
                      const next = Number(value);
                      setSuppressionHours(next);
                      draftRef.current = { ...draftRef.current, suppressionHours: next };
                      idempotencyRef.current = null;
                    }}
                  >
                    <SelectTrigger id="alarm-suppression-hours" data-testid="real-alarm-suppression-hours" className="w-full">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent><SelectGroup>
                      <SelectItem value="1">1 小时</SelectItem>
                      <SelectItem value="4">4 小时</SelectItem>
                      <SelectItem value="24">24 小时</SelectItem>
                      <SelectItem value="168">7 天</SelectItem>
                    </SelectGroup></SelectContent>
                  </Select>
                </Field>
              ) : null}
            </FieldGroup>
          </FieldGroup>

          <div className="flex flex-wrap items-center gap-2 border-t pt-4">
            {projection.canAcknowledge ? (
              <Button
                data-testid="real-alarm-acknowledge"
                type="button"
                disabled={lifecycleMutation.isPending}
                onClick={() => {
                  setAckComment('');
                  draftRef.current = { ...draftRef.current, ackComment: '' };
                  setAckDialogOpen(true);
                }}
              >
                <CheckCircle2 />确认告警
              </Button>
            ) : null}
            {projection.canAssign ? <Button data-testid="real-alarm-assign" type="button" variant="outline" disabled={mutationDisabled || !assigneeId.trim()} onClick={() => submitLifecycle('ASSIGN')}>指派</Button> : null}
            {projection.canUnassign ? <Button data-testid="real-alarm-unassign" type="button" variant="outline" disabled={mutationDisabled} onClick={() => submitLifecycle('UNASSIGN')}>取消指派</Button> : null}
            {projection.canSuppress ? <Button data-testid="real-alarm-suppress" type="button" variant="outline" disabled={mutationDisabled} onClick={() => submitLifecycle('SUPPRESS')}>抑制</Button> : null}
            {projection.canUnsuppress ? <Button data-testid="real-alarm-unsuppress" type="button" variant="outline" disabled={mutationDisabled} onClick={() => submitLifecycle('UNSUPPRESS')}>解除抑制</Button> : null}
            <div className="ml-auto flex flex-wrap items-center gap-2">
              <Badge variant="outline">{projection.statusLabel}</Badge>
              <Badge variant="outline">{projection.sourceLabel}</Badge>
            </div>
          </div>

          {lifecycleMutation.isPending ? (
            <div className="flex items-center gap-2 rounded-md border bg-muted/20 p-3 text-xs text-muted-foreground" role="status">
              <LoaderCircle className="size-4 animate-spin" />正在提交告警处置操作…
            </div>
          ) : null}

          {lifecycleMutation.isError ? (
            <div className="flex items-start gap-3 rounded-md border border-destructive/30 bg-destructive/5 p-3 text-destructive" role="alert" data-testid="real-alarm-mutation-error">
              <ShieldAlert className="mt-0.5 size-4 shrink-0" />
              <div><strong className="text-xs font-medium">告警处置操作失败</strong><p className="mt-1 text-[11px] leading-5">{alarmErrorMessage(lifecycleMutation.error)}</p></div>
            </div>
          ) : null}
        </CardContent>
      </Card>

      <Dialog open={ackDialogOpen} onOpenChange={(open) => {
        if (lifecycleMutation.isPending) return;
        setAckDialogOpen(open);
        if (!open) setAckComment('');
      }}>
        <DialogContent className="sm:max-w-xl" data-testid="real-alarm-ack-dialog">
          <DialogHeader>
            <DialogTitle>确认告警</DialogTitle>
            <DialogDescription>确认只表示值班人员已经知晓并接手，不代表故障已经恢复；告警状态仍由实时数据和恢复条件判定。</DialogDescription>
          </DialogHeader>
          <div className="grid gap-3 rounded-md border bg-muted/20 p-3 text-xs sm:grid-cols-2">
            <div><span className="block text-[11px] text-muted-foreground">告警</span><strong className="mt-1 block font-medium">{alarm.title}</strong></div>
            <div><span className="block text-[11px] text-muted-foreground">当前状态</span><strong className="mt-1 block font-medium">{projection.statusLabel}</strong></div>
            <div><span className="block text-[11px] text-muted-foreground">来源</span><strong className="mt-1 block font-medium">{projection.sourceLabel}</strong></div>
            <div><span className="block text-[11px] text-muted-foreground">触发摘要</span><strong className="mt-1 block font-medium">{alarm.summary}</strong></div>
          </div>
          <Field>
            <FieldLabel htmlFor="alarm-ack-comment">确认备注（可选）</FieldLabel>
            <Textarea
              id="alarm-ack-comment"
              data-testid="real-alarm-ack-comment"
              maxLength={1000}
              value={ackComment}
              onChange={(event) => {
                const value = event.currentTarget.value;
                setAckComment(value);
                draftRef.current = { ...draftRef.current, ackComment: value };
                idempotencyRef.current = null;
              }}
              placeholder="补充值班确认信息"
            />
            <FieldDescription>确认备注写入处置记录；留空不会影响确认事实。</FieldDescription>
          </Field>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => setAckDialogOpen(false)} disabled={lifecycleMutation.isPending}>取消</Button>
            <Button ref={ackConfirmRef} type="button" onClick={() => submitLifecycle('ACKNOWLEDGE', ackComment)} disabled={lifecycleMutation.isPending}>
              {lifecycleMutation.isPending ? <LoaderCircle className="animate-spin" /> : <CheckCircle2 />}确认告警
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </section>
  );
}
