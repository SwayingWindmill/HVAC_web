import { useId, useState } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { ShieldAlert, Unlock } from 'lucide-react';
import { toast } from 'sonner';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { RadioGroup } from '@/components/ui/radio-group';
import { Spinner } from '@/components/ui/spinner';
import { Textarea } from '@/components/ui/textarea';
import { formatTime } from '@/lib/operator-format';
import { isOneOf } from '@/lib/typed';
import { CommandRequestError } from '../command-api';
import type { ControlTarget } from '../control-model';
import {
  controlStateKeys,
  deleteDevicePriorityLock,
  DEVICE_PRIORITY_TIER_LABEL,
  formatLockRemaining,
  MUTABLE_PRIORITY_TIERS,
  putDevicePriorityLock,
  type DevicePriorityLockState,
  type MutableDevicePriorityTier,
} from '../control-state';
import { ChoiceCard } from './ControlBoundary';

const TIER_ORDER = ['MANUAL_OVERRIDE', 'PROTECTION_RULE', 'SAFETY_INTERLOCK'] as const satisfies readonly MutableDevicePriorityTier[];
const DURATION_PRESETS = [15, 30, 60, 180] as const;
const MIN_DURATION_MINUTES = 5;
const MAX_DURATION_MINUTES = 1440;
const MAX_REASON_LENGTH = 1000;

function parseDurationMinutes(raw: string): number | null {
  const trimmed = raw.trim();
  if (!trimmed) return null;
  const parsed = Number(trimmed);
  if (!Number.isInteger(parsed) || parsed < MIN_DURATION_MINUTES || parsed > MAX_DURATION_MINUTES) {
    return null;
  }
  return parsed;
}

/** Device-level command priority lock dialog: acquire, extend, or release with auditable reason. */
export function PriorityLockDialog({
  target,
  activeLock,
  siteId,
  csrfToken,
  timeZone,
  nowMs,
  onClose,
}: {
  readonly target: ControlTarget | null;
  readonly activeLock: DevicePriorityLockState | undefined;
  readonly siteId: string;
  readonly csrfToken: string;
  readonly timeZone: string;
  readonly nowMs: number;
  readonly onClose: () => void;
}) {
  const [busy, setBusy] = useState(false);
  return (
    <Dialog open={target !== null} onOpenChange={(open) => { if (!open && !busy) onClose(); }}>
      <DialogContent
        className="sm:max-w-[560px]"
        onEscapeKeyDown={(event) => { if (busy) event.preventDefault(); }}
        onInteractOutside={(event) => { if (busy) event.preventDefault(); }}
      >
        {target ? (
          <PriorityLockForm
            key={`${target.deviceId}-${activeLock?.updatedAt ?? 'none'}`}
            target={target}
            activeLock={activeLock}
            siteId={siteId}
            csrfToken={csrfToken}
            timeZone={timeZone}
            nowMs={nowMs}
            onBusyChange={setBusy}
            onClose={onClose}
          />
        ) : null}
      </DialogContent>
    </Dialog>
  );
}

function PriorityLockForm({
  target,
  activeLock,
  siteId,
  csrfToken,
  timeZone,
  nowMs,
  onBusyChange,
  onClose,
}: {
  readonly target: ControlTarget;
  readonly activeLock: DevicePriorityLockState | undefined;
  readonly siteId: string;
  readonly csrfToken: string;
  readonly timeZone: string;
  readonly nowMs: number;
  readonly onBusyChange: (busy: boolean) => void;
  readonly onClose: () => void;
}) {
  const queryClient = useQueryClient();
  const durationId = useId();
  const durationErrorId = useId();
  const reasonId = useId();
  const reasonErrorId = useId();

  const initialTier: MutableDevicePriorityTier =
    activeLock && isOneOf(TIER_ORDER, activeLock.lockedPriority) ? activeLock.lockedPriority : 'MANUAL_OVERRIDE';

  const [priority, setPriority] = useState<MutableDevicePriorityTier>(initialTier);
  const [durationDraft, setDurationDraft] = useState('60');
  const [durationTouched, setDurationTouched] = useState(false);
  const [reason, setReason] = useState('');
  const [reasonTouched, setReasonTouched] = useState(false);
  const [submitAttempted, setSubmitAttempted] = useState(false);
  const [refusal, setRefusal] = useState<string | null>(null);

  const durationMinutes = parseDurationMinutes(durationDraft);
  const durationError = (durationTouched || submitAttempted) && durationMinutes === null
    ? `锁定时长须为 ${MIN_DURATION_MINUTES} 到 ${MAX_DURATION_MINUTES} 分钟的整数`
    : null;
  const trimmedReason = reason.trim();
  const reasonError = (reasonTouched || submitAttempted) && (!trimmedReason || trimmedReason.length > MAX_REASON_LENGTH)
    ? `请填写说明原因（1 到 ${MAX_REASON_LENGTH} 字）`
    : null;

  const refreshState = () => queryClient.invalidateQueries({ queryKey: controlStateKeys.site(siteId) });

  const lockMutation = useMutation({
    mutationFn: (input: { readonly lockedPriority: MutableDevicePriorityTier; readonly durationMinutes: number; readonly reason: string }) =>
      putDevicePriorityLock(siteId, target.deviceId, input, csrfToken),
    onMutate: () => onBusyChange(true),
    onSettled: () => onBusyChange(false),
    onSuccess: (saved) => {
      toast.success(`已为 ${target.assetName} 设置${DEVICE_PRIORITY_TIER_LABEL[saved.lockedPriority]}锁定`);
      void refreshState();
      onClose();
    },
    onError: (error) => {
      const message = error instanceof CommandRequestError ? error.message : '设置设备优先级锁失败，请稍后重试';
      setRefusal(message);
    },
  });

  const releaseMutation = useMutation({
    mutationFn: (releaseReason: string) => deleteDevicePriorityLock(siteId, target.deviceId, releaseReason, csrfToken),
    onMutate: () => onBusyChange(true),
    onSettled: () => onBusyChange(false),
    onSuccess: () => {
      toast.success(`已解除 ${target.assetName} 的优先级锁定`);
      void refreshState();
      onClose();
    },
    onError: (error) => {
      const message = error instanceof CommandRequestError ? error.message : '解除优先级锁定失败，请稍后重试';
      setRefusal(message);
    },
  });

  const busy = lockMutation.isPending || releaseMutation.isPending;

  const submitLock = () => {
    setSubmitAttempted(true);
    setRefusal(null);
    if (durationMinutes === null || !trimmedReason || trimmedReason.length > MAX_REASON_LENGTH) {
      return;
    }
    lockMutation.mutate({
      lockedPriority: priority,
      durationMinutes,
      reason: trimmedReason,
    });
  };

  const submitRelease = () => {
    setSubmitAttempted(true);
    setRefusal(null);
    if (!trimmedReason || trimmedReason.length > MAX_REASON_LENGTH) {
      return;
    }
    releaseMutation.mutate(trimmedReason);
  };

  return (
    <form
      className="flex flex-col gap-4"
      onSubmit={(event) => {
        event.preventDefault();
        submitLock();
      }}
    >
      <DialogHeader>
        <DialogTitle className="flex items-center gap-2">
          <ShieldAlert className="size-4 text-primary" aria-hidden="true" />
          {target.assetName} 优先级仲裁锁
        </DialogTitle>
        <DialogDescription>
          锁定期间仅允许不低于所选优先级的指令下发，其余低优先级指令会被网关拦截。
        </DialogDescription>
      </DialogHeader>

      {activeLock ? (
        <Alert>
          <AlertTitle>
            当前已锁定：{DEVICE_PRIORITY_TIER_LABEL[activeLock.lockedPriority]}（{formatLockRemaining(activeLock.lockedUntil, nowMs)}）
          </AlertTitle>
          <AlertDescription className="flex flex-col gap-1 text-xs">
            <span>
              操作人：{activeLock.lockedByName ?? '系统规则'} · 到期时间：{formatTime(activeLock.lockedUntil, timeZone)}
            </span>
            <span>原锁定说明：{activeLock.reason}</span>
          </AlertDescription>
        </Alert>
      ) : null}

      <fieldset className="m-0 flex flex-col gap-2 border-0 p-0">
        <legend className="mb-1.5 text-sm font-medium text-foreground">锁定时优先等级</legend>
        <RadioGroup
          value={priority}
          onValueChange={(value) => {
            if (isOneOf(TIER_ORDER, value)) {
              setPriority(value);
              setRefusal(null);
            }
          }}
          className="gap-2"
        >
          {MUTABLE_PRIORITY_TIERS.map((tier) => (
            <ChoiceCard
              key={tier.value}
              value={tier.value}
              title={tier.label}
              hint={tier.hint}
              current={activeLock?.lockedPriority === tier.value}
            />
          ))}
        </RadioGroup>
      </fieldset>

      <div className="flex flex-col gap-1.5">
        <Label htmlFor={durationId}>锁定时长（分钟）</Label>
        <div className="flex flex-wrap items-center gap-2">
          <Input
            id={durationId}
            inputMode="numeric"
            value={durationDraft}
            aria-invalid={durationError !== null}
            aria-describedby={durationError ? durationErrorId : undefined}
            onChange={(event) => {
              setDurationDraft(event.target.value);
              setRefusal(null);
            }}
            onBlur={() => setDurationTouched(true)}
            onKeyDown={(event) => {
              if (event.key !== 'Enter') return;
              if (event.nativeEvent.isComposing || event.keyCode === 229) {
                event.preventDefault();
                return;
              }
              event.preventDefault();
              submitLock();
            }}
            className="w-28 tabular-nums"
          />
          {DURATION_PRESETS.map((minutes) => (
            <Button
              key={minutes}
              type="button"
              size="sm"
              variant={durationDraft.trim() === String(minutes) ? 'default' : 'outline'}
              onClick={() => {
                setDurationDraft(String(minutes));
                setDurationTouched(true);
                setRefusal(null);
              }}
            >
              {minutes} 分钟
            </Button>
          ))}
        </div>
        {durationError ? (
          <p id={durationErrorId} className="text-xs text-destructive-text">{durationError}</p>
        ) : null}
      </div>

      <div className="flex flex-col gap-1.5">
        <Label htmlFor={reasonId}>{activeLock ? '更新或解除原因' : '锁定原因'}</Label>
        <Textarea
          id={reasonId}
          value={reason}
          placeholder={activeLock ? '例如：现场故障排查完成，恢复自动控制' : '例如：3# 冷机手动带载测试，暂停 AI 自动卸载'}
          maxLength={MAX_REASON_LENGTH}
          aria-invalid={reasonError !== null}
          aria-describedby={reasonError ? reasonErrorId : undefined}
          onChange={(event) => {
            setReason(event.target.value);
            setRefusal(null);
          }}
          onBlur={() => setReasonTouched(true)}
          className="min-h-[72px] resize-none"
        />
        {reasonError ? (
          <p id={reasonErrorId} className="text-xs text-destructive-text">{reasonError}</p>
        ) : null}
      </div>

      {refusal ? (
        <Alert variant="destructive">
          <AlertTitle>{refusal}</AlertTitle>
        </Alert>
      ) : null}

      <DialogFooter className="gap-2 sm:justify-between">
        <div>
          {activeLock ? (
              <Button
              type="button"
              variant="destructive"
              disabled={busy}
              onClick={submitRelease}
            >
              {releaseMutation.isPending ? <Spinner data-icon="inline-start" /> : <Unlock data-icon="inline-start" />}
              {releaseMutation.isPending ? '解除中…' : '解除锁定'}
            </Button>
          ) : null}
        </div>
        <div className="flex items-center gap-2">
          <Button type="button" variant="outline" disabled={busy} onClick={onClose}>取消</Button>
          <Button type="submit" disabled={busy}>
            {lockMutation.isPending ? <Spinner data-icon="inline-start" /> : null}
            {lockMutation.isPending ? '提交中…' : activeLock ? '更新锁定' : '确认锁定'}
          </Button>
        </div>
      </DialogFooter>
    </form>
  );
}
