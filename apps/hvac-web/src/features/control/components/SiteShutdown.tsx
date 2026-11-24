import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useId, useState } from 'react';
import { toast } from 'sonner';
import { Alert, AlertTitle } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Label } from '@/components/ui/label';
import { RadioGroup } from '@/components/ui/radio-group';
import { Spinner } from '@/components/ui/spinner';
import { isOneOf } from '@/lib/typed';
import type { SiteControlShutdownMode, SiteControlShutdownResponse } from '@/api/generated/platformGateway.gen';
import { CommandRequestError, commandKeys } from '../command-api';
import { stepUpErrorPresentation, type StepUpErrorPresentation } from '../control-errors';
import { controlStateKeys, executeSiteShutdown, type SiteControlState } from '../control-state';
import { CODE_PATTERN, CodeField } from './CommandConfirmation';
import { ChoiceCard, ReasonField } from './ControlBoundary';

/** What the operator is stopping: the whole Site, or one unit whose one-click sequence is running. */
export type ShutdownSubject =
  | { readonly kind: 'site'; readonly emergencyAvailable: boolean; readonly withdrawCount: number }
  | { readonly kind: 'unit'; readonly unitCode: string; readonly unitName: string; readonly members: readonly string[]; readonly withdrawCount: number };

const SITE_MODES = ['PLANT_SAFE_SHUTDOWN', 'EMERGENCY_DO_RELEASE'] as const satisfies readonly SiteControlShutdownMode[];

const MODE_COPY = {
  PLANT_SAFE_SHUTDOWN: {
    title: '安全停机',
    hint: '先停所有没锁定的冷机，主机停稳后再停对应的水泵和冷却塔；站点切到安全退出',
    submit: '执行安全停机',
    done: '已开始安全停机',
  },
  EMERGENCY_DO_RELEASE: {
    title: '紧急全断',
    hint: '立刻断开全部输出通道，所有设备同时失电，不按顺序；只在现场有危险时用',
    submit: '立刻全断',
    done: '已发出紧急全断',
  },
} as const satisfies Record<(typeof SITE_MODES)[number], Record<string, string>>;

/** Refusals the operator can fix without closing the dialog. */
const IN_PLACE = new Set(['CONTROL_EXIT_REQUEST_INVALID', 'CONTROL_SHUTDOWN_LOCKED', 'EMERGENCY_DO_RELEASE_UNAVAILABLE']);

function outcome(response: SiteControlShutdownResponse): string {
  const parts = [`提交 ${response.submittedCommandIds.length} 条停机指令`];
  if (response.cancelledCommandIds.length > 0) parts.push(`撤回 ${response.cancelledCommandIds.length} 条`);
  if (response.deferredHoldingAssetIds.length > 0) parts.push(`${response.deferredHoldingAssetIds.length} 台水泵或冷却塔等主机停稳后再停`);
  return parts.join('，');
}

export function SiteShutdownDialog({ siteId, csrfToken, state, subject, onClose }: {
  readonly siteId: string;
  readonly csrfToken: string;
  readonly state: SiteControlState;
  readonly subject: ShutdownSubject | null;
  readonly onClose: () => void;
}) {
  const [busy, setBusy] = useState(false);
  return (
    <Dialog open={subject !== null} onOpenChange={(open) => { if (!open && !busy) onClose(); }}>
      <DialogContent
        className="sm:max-w-[560px]"
        onEscapeKeyDown={(event) => { if (busy) event.preventDefault(); }}
        onInteractOutside={(event) => { if (busy) event.preventDefault(); }}
      >
        {subject ? <ShutdownForm siteId={siteId} csrfToken={csrfToken} state={state} subject={subject} onBusyChange={setBusy} onClose={onClose} /> : null}
      </DialogContent>
    </Dialog>
  );
}

function ShutdownForm({ siteId, csrfToken, state, subject, onBusyChange, onClose }: {
  readonly siteId: string;
  readonly csrfToken: string;
  readonly state: SiteControlState;
  readonly subject: ShutdownSubject;
  readonly onBusyChange: (busy: boolean) => void;
  readonly onClose: () => void;
}) {
  const queryClient = useQueryClient();
  const reasonId = useId();
  const codeId = useId();
  const ackId = useId();
  const emergencyId = useId();
  // One key per confirmation: a retried submit must not stop the plant a second time.
  const [idempotencyKey] = useState(() => `control-shutdown-${crypto.randomUUID()}`);
  const [siteMode, setSiteMode] = useState<(typeof SITE_MODES)[number]>('PLANT_SAFE_SHUTDOWN');
  const [reason, setReason] = useState('');
  const [code, setCode] = useState('');
  const [codeError, setCodeError] = useState<StepUpErrorPresentation | null>(null);
  const [acknowledged, setAcknowledged] = useState(false);
  const [emergencyConfirmed, setEmergencyConfirmed] = useState(false);
  const [refusal, setRefusal] = useState<string | null>(null);

  const mode: SiteControlShutdownMode = subject.kind === 'unit' ? 'ABORT_UNIT_SEQUENCE' : siteMode;
  const emergency = mode === 'EMERGENCY_DO_RELEASE';
  const run = useMutation({
    mutationFn: () => executeSiteShutdown(siteId, {
      mode,
      reason: reason.trim(),
      verificationCode: code,
      expectedRevision: state.revision,
      ...(subject.kind === 'unit' ? { unitCode: subject.unitCode } : {}),
      ...(emergency ? { confirmEmergencyDoRelease: true } : {}),
    }, idempotencyKey, csrfToken),
    onMutate: () => onBusyChange(true),
    onSettled: () => onBusyChange(false),
    onSuccess: (response) => {
      queryClient.setQueryData(controlStateKeys.site(siteId), response.siteControlState);
      void queryClient.invalidateQueries({ queryKey: commandKeys.site(siteId) });
      const done = subject.kind === 'unit' ? `已中止${subject.unitName}` : MODE_COPY[siteMode].done;
      toast.success(`${done}：${outcome(response)}`);
      onClose();
    },
    onError: (error) => {
      if (error instanceof CommandRequestError && error.code === 'STEP_UP_REQUIRED') {
        setCode('');
        setCodeError(stepUpErrorPresentation(error.reason, true));
        return;
      }
      if (error instanceof CommandRequestError && error.code && IN_PLACE.has(error.code)) {
        setRefusal(error.message);
        return;
      }
      if (error instanceof CommandRequestError && error.code === 'REGISTRY_REVISION_CONFLICT') {
        void queryClient.invalidateQueries({ queryKey: controlStateKeys.site(siteId) });
      }
      toast.error(error instanceof CommandRequestError ? error.message : '停机请求没有送达，请稍后再试');
      onClose();
    },
  });

  const ready = reason.trim() !== '' && CODE_PATTERN.test(code) && acknowledged && (!emergency || emergencyConfirmed);
  const title = subject.kind === 'unit' ? `中止${subject.unitName}的一键启停？` : `${MODE_COPY[siteMode].title}整个站点？`;
  const submitLabel = subject.kind === 'unit' ? '中止并停机' : MODE_COPY[siteMode].submit;
  return (
    <form className="flex flex-col gap-4" onSubmit={(event) => { event.preventDefault(); if (ready) run.mutate(); }}>
      <DialogHeader>
        <DialogTitle>{title}</DialogTitle>
        <DialogDescription className="sr-only">选择停机方式，填写原因和动态口令后执行</DialogDescription>
      </DialogHeader>
      {subject.kind === 'site' && subject.emergencyAvailable ? (
        <RadioGroup
          value={siteMode}
          onValueChange={(value) => { if (isOneOf(SITE_MODES, value)) { setSiteMode(value); setRefusal(null); } }}
          aria-label="停机方式"
          className="gap-2"
        >
          {SITE_MODES.map((option) => <ChoiceCard key={option} value={option} title={MODE_COPY[option].title} hint={MODE_COPY[option].hint} />)}
        </RadioGroup>
      ) : (
        <div className="flex flex-col gap-1.5 rounded-lg border border-divider bg-muted p-3 text-sm">
          {subject.kind === 'unit' ? (
            <>
              <span>撤回这组还没下发的指令，先停主机，主机停稳后再停{subject.members.length > 0 ? subject.members.join('、') : '对应的水泵和冷却塔'}。</span>
              <span className="text-xs text-muted-foreground">控制方式不变，其他机组照常运行。</span>
            </>
          ) : (
            <span>{MODE_COPY.PLANT_SAFE_SHUTDOWN.hint}。</span>
          )}
        </div>
      )}
      {subject.withdrawCount > 0 ? (
        <span className="text-xs text-secondary-foreground">会撤回 {subject.withdrawCount} 条还没下发的指令</span>
      ) : null}
      <ReasonField id={reasonId} value={reason} onChange={(value) => { setReason(value); setRefusal(null); }} placeholder="例如：冷冻水管漏水，停机检修" />
      <CodeField
        id={codeId}
        value={code}
        error={codeError?.message ?? null}
        showEnrollLink={codeError?.showEnrollLink}
        onNavigateEnroll={onClose}
        onSubmit={() => { if (ready && !run.isPending) run.mutate(); }}
        onChange={(value) => { setCode(value); setCodeError(null); }}
      />
      <div className="flex items-start gap-2">
        <Checkbox id={ackId} checked={acknowledged} onCheckedChange={(value) => setAcknowledged(value === true)} className="mt-0.5" />
        <Label htmlFor={ackId} className="text-sm font-normal">我已和现场确认可以停机</Label>
      </div>
      {emergency ? (
        <div className="flex items-start gap-2">
          <Checkbox id={emergencyId} checked={emergencyConfirmed} onCheckedChange={(value) => setEmergencyConfirmed(value === true)} className="mt-0.5" />
          <Label htmlFor={emergencyId} className="text-sm font-normal">我确认要立刻断开全部输出通道</Label>
        </div>
      ) : null}
      {refusal ? <Alert variant="destructive"><AlertTitle>{refusal}</AlertTitle></Alert> : null}
      <DialogFooter>
        <Button type="button" variant="outline" disabled={run.isPending} onClick={onClose}>取消</Button>
        <Button type="submit" variant="destructive" disabled={run.isPending || !ready}>
          {run.isPending ? <Spinner data-icon="inline-start" /> : null}
          {run.isPending ? '正在提交' : submitLabel}
        </Button>
      </DialogFooter>
    </form>
  );
}
