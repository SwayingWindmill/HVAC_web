import { useMutation, useQueryClient } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';
import { Lock, LockOpen, Power } from 'lucide-react';
import { useId, useState, type ReactNode } from 'react';
import { toast } from 'sonner';
import type { SiteAutoControlLeaseState } from '@/api/generated/platformGateway.gen';
import { Panel } from '@/blocks/panel';
import { StatusBadge } from '@/components/status-badge';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Label } from '@/components/ui/label';
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group';
import { Spinner } from '@/components/ui/spinner';
import { Textarea } from '@/components/ui/textarea';
import { formatCompactTime } from '@/lib/operator-format';
import { isOneOf } from '@/lib/typed';
import { cn } from '@/lib/utils';
import { CommandRequestError, commandKeys, WITHDRAWABLE, type Command } from '../command-api';
import { STATUS_PRESENTATION } from '../command-presentation';
import {
  controlActorLabel,
  controlStateKeys,
  LOCK_PRESENTATION,
  MODE_ORDER,
  MODE_PRESENTATION,
  REASON_LIMIT,
  updateAssetControlLock,
  updateSiteControlMode,
  type AssetControlLockMode,
  type AssetControlLockState,
  type SiteControlMode,
  type SiteControlState,
} from '../control-state';

const TONE_TEXT = { success: 'text-success', warning: 'text-warning', destructive: 'text-destructive' } as const;
const BAR_TONE = {
  REMOTE_AUTO: '',
  MANUAL_OVERRIDE: 'border-warning bg-warning-soft',
  SAFE_SHUTDOWN_EXIT: 'border-destructive bg-destructive-soft',
} as const satisfies Record<SiteControlMode, string>;

/** ● ▲ ◆ in the mode's colour: the shape carries the state for readers who cannot tell the colours apart. */
export function ModeGlyph({ mode, className }: { readonly mode: SiteControlMode; readonly className?: string }) {
  const presentation = MODE_PRESENTATION[mode];
  return <span aria-hidden="true" className={cn('text-[10px] leading-none', TONE_TEXT[presentation.tone], className)}>{presentation.glyph}</span>;
}

/** Refusals the dialog shows in place, because the operator can fix them without closing it. */
const IN_PLACE = new Set(['SAFE_SHUTDOWN_PRECONDITION_FAILED', 'CONTROL_EXIT_REQUEST_INVALID']);

export interface WithdrawnSummary {
  readonly commandId: string;
  readonly text: string;
}

/**
 * The Site's control mode across the top of the control page: which mode, who set it and why,
 * and the other two as a segmented choice that opens the switch dialog.
 */
export function ControlModeBar({ state, lockCount, pendingCount, myId, timeZone, canManage, autoControlLease, onPick, onShutdown }: {
  readonly state: SiteControlState;
  readonly lockCount: number;
  readonly pendingCount: number;
  readonly myId: string;
  readonly timeZone: string;
  readonly canManage: boolean;
  readonly autoControlLease?: SiteAutoControlLeaseState;
  readonly onPick: (mode: SiteControlMode) => void;
  /** Opens the site shutdown dialog. */
  readonly onShutdown: () => void;
}) {
  const presentation = MODE_PRESENTATION[state.controlMode];
  return (
    <Panel
      aria-label="控制方式"
      className={cn(
        'flex flex-col gap-3 px-4 py-3.5 @4xl/main:flex-row @4xl/main:items-center @4xl/main:justify-between @4xl/main:gap-6',
        BAR_TONE[state.controlMode],
      )}
    >
      <div className="flex min-w-0 flex-1 flex-wrap items-center gap-x-6 gap-y-2">
        <div className="flex shrink-0 flex-col">
          <span className="text-xs text-muted-foreground">控制方式</span>
          <span className="flex items-center gap-2 text-xl leading-7 font-semibold tracking-[-0.02em]">
            <ModeGlyph mode={state.controlMode} className="text-sm" />
            {presentation.label}
          </span>
        </div>
        <div className="flex min-w-0 flex-1 flex-col gap-0.5">
          <div className="flex flex-wrap items-baseline gap-x-4 gap-y-1">
            {state.updatedAt ? (
              <span className="flex items-baseline gap-1.5 text-sm whitespace-nowrap">
                <span className="text-xs text-muted-foreground">设定</span>
                <span className="font-semibold">{controlActorLabel(state, myId)}</span>
                <span className="font-mono text-xs text-muted-foreground">{formatCompactTime(state.updatedAt, timeZone)}</span>
              </span>
            ) : null}
            <span className="flex items-baseline gap-1.5 text-sm whitespace-nowrap"><span className="text-xs text-muted-foreground">锁定设备</span><span className="font-semibold tabular-nums">{lockCount} 台</span></span>
            <span className="flex items-baseline gap-1.5 text-sm whitespace-nowrap"><span className="text-xs text-muted-foreground">待处理指令</span><span className="font-semibold tabular-nums">{pendingCount} 条</span></span>
          </div>
          {state.reason ? <span className="truncate text-xs text-muted-foreground" title={state.reason}>原因：{state.reason}</span> : null}
          {state.activeMaintenanceWindow ? (
            <span className="truncate text-xs text-warning-text" title={state.activeMaintenanceWindow.reason || undefined}>
              检修窗口「{state.activeMaintenanceWindow.title}」{state.activeMaintenanceWindow.startDate} 至 {state.activeMaintenanceWindow.endDate}，只能停机和复位
            </span>
          ) : null}
          {autoControlLease?.confirmationMode === 'AUTO_WITH_LEASE' && autoControlLease.activeLease ? (
            <span className="mt-0.5 flex flex-wrap items-center gap-2 text-xs">
              <StatusBadge tone="info" label="自动控制租约生效中" />
              <span className="text-secondary-foreground">
                有效期至 <span className="font-mono">{formatCompactTime(autoControlLease.activeLease.expiresAt, timeZone)}</span>
              </span>
              <Link
                to="/energy-analysis/optimization"
                search={{ site: autoControlLease.siteId }}
                className="font-medium text-primary hover:underline"
              >
                管理自动租约 ›
              </Link>
            </span>
          ) : null}
        </div>
      </div>
      <div className="flex shrink-0 flex-wrap items-center gap-3">
        <div role="group" aria-label="切换控制方式" className={cn('inline-flex gap-0.5 rounded-lg p-0.5', state.controlMode === 'REMOTE_AUTO' ? 'bg-secondary' : 'bg-card')}>
          {MODE_ORDER.map((mode) => {
            const current = mode === state.controlMode;
            return (
              <button
                key={mode}
                type="button"
                aria-pressed={current}
                disabled={!current && !canManage}
                title={!current && !canManage ? '你没有切换控制方式的权限' : undefined}
                onClick={() => { if (!current) onPick(mode); }}
                className={cn(
                  'inline-flex h-[30px] items-center gap-1.5 rounded-md border border-transparent px-3 text-sm font-medium text-secondary-foreground transition-colors duration-100 outline-none hover:text-foreground focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring-soft disabled:cursor-not-allowed disabled:opacity-50',
                  current && 'border-border bg-card text-foreground shadow-xs',
                  current && state.controlMode !== 'REMOTE_AUTO' && 'bg-secondary',
                )}
              >
                <ModeGlyph mode={mode} />
                {MODE_PRESENTATION[mode].short}
              </button>
            );
          })}
        </div>
        <span className="hidden h-6 w-px bg-divider sm:inline-block" aria-hidden="true" />
        <Button
          variant="outline"
          size="sm"
          className="w-fit shrink-0 border-destructive/35 bg-destructive-soft/45 text-destructive-text hover:border-destructive/60 hover:bg-destructive-soft hover:text-destructive-text"
          disabled={!canManage}
          title={canManage ? undefined : '你没有停机的权限'}
          onClick={onShutdown}
        >
          <Power data-icon="inline-start" />停机…
        </Button>
      </div>
    </Panel>
  );
}

export function ReasonField({ id, value, onChange, placeholder }: {
  readonly id: string;
  readonly value: string;
  readonly onChange: (value: string) => void;
  readonly placeholder: string;
}) {
  return (
    <div className="flex flex-col gap-1.5">
      <Label htmlFor={id} className="flex justify-between text-xs">
        原因
        <span className="font-mono font-normal text-muted-foreground">{value.length}/{REASON_LIMIT}</span>
      </Label>
      <Textarea id={id} value={value} placeholder={placeholder} maxLength={REASON_LIMIT} onChange={(event) => onChange(event.target.value)} className="min-h-[72px] resize-none" />
    </div>
  );
}

export function ChoiceCard({ value, title, hint, current, children }: {
  readonly value: string;
  readonly title: string;
  readonly hint: string;
  readonly current?: boolean;
  readonly children?: ReactNode;
}) {
  const id = useId();
  return (
    <Label
      htmlFor={id}
      className="grid cursor-pointer grid-cols-[16px_minmax(0,1fr)] gap-x-2.5 gap-y-0.5 rounded-lg border px-3 py-2.5 font-normal hover:border-input-hover has-[[data-state=checked]]:border-primary has-[[data-state=checked]]:bg-primary-soft"
    >
      <RadioGroupItem id={id} value={value} className="mt-0.5" />
      <span className="flex items-center gap-1.5 text-sm font-semibold">
        {children}
        {title}
        {current ? <span className="rounded-md bg-secondary px-1.5 text-[11px] leading-5 font-medium text-secondary-foreground">当前</span> : null}
      </span>
      <span className="col-start-2 text-xs text-muted-foreground">{hint}</span>
    </Label>
  );
}

/** Switches the Site's control mode; leaving remote auto control withdraws the commands not yet dispatched. */
export function ControlModeDialog({ siteId, csrfToken, state, initial, commands, describe, onClose, onShowUnsettled }: {
  readonly siteId: string;
  readonly csrfToken: string;
  readonly state: SiteControlState;
  readonly initial: SiteControlMode | null;
  readonly commands: readonly Command[];
  readonly describe: (command: Command) => string;
  readonly onClose: () => void;
  readonly onShowUnsettled: () => void;
}) {
  const [busy, setBusy] = useState(false);
  return (
    <Dialog open={initial !== null} onOpenChange={(open) => { if (!open && !busy) onClose(); }}>
      <DialogContent
        className="sm:max-w-[560px]"
        onEscapeKeyDown={(event) => { if (busy) event.preventDefault(); }}
        onInteractOutside={(event) => { if (busy) event.preventDefault(); }}
      >
        {initial !== null ? (
          <ModeForm
            key={initial}
            siteId={siteId}
            csrfToken={csrfToken}
            state={state}
            initial={initial}
            commands={commands}
            describe={describe}
            onBusyChange={setBusy}
            onClose={onClose}
            onShowUnsettled={onShowUnsettled}
          />
        ) : null}
      </DialogContent>
    </Dialog>
  );
}

function ModeForm({ siteId, csrfToken, state, initial, commands, describe, onBusyChange, onClose, onShowUnsettled }: {
  readonly siteId: string;
  readonly csrfToken: string;
  readonly state: SiteControlState;
  readonly initial: SiteControlMode;
  readonly commands: readonly Command[];
  readonly describe: (command: Command) => string;
  readonly onBusyChange: (busy: boolean) => void;
  readonly onClose: () => void;
  readonly onShowUnsettled: () => void;
}) {
  const queryClient = useQueryClient();
  const reasonId = useId();
  const safeExitId = useId();
  const [target, setTarget] = useState<SiteControlMode>(initial);
  const [reason, setReason] = useState('');
  const [safeExitConfirmed, setSafeExitConfirmed] = useState(false);
  const [refusal, setRefusal] = useState<string | null>(null);
  const withdrawn = state.controlMode === 'REMOTE_AUTO' && target !== 'REMOTE_AUTO'
    ? commands.filter((command) => WITHDRAWABLE.has(command.status))
    : [];
  // Safe exit waits for every command already on its way, or whose outcome nobody has confirmed.
  const unsettled = target === 'SAFE_SHUTDOWN_EXIT'
    ? commands.filter((command) => command.status === 'DISPATCHING' || command.status === 'OUTCOME_UNKNOWN')
    : [];
  const save = useMutation({
    mutationFn: () => updateSiteControlMode(siteId, { controlMode: target, reason: reason.trim(), expectedRevision: state.revision }, csrfToken),
    onMutate: () => onBusyChange(true),
    onSettled: () => onBusyChange(false),
    onSuccess: ({ cancelledCommandIds, ...next }) => {
      queryClient.setQueryData(controlStateKeys.site(siteId), next);
      void queryClient.invalidateQueries({ queryKey: commandKeys.site(siteId) });
      toast.success(`已切换到${MODE_PRESENTATION[next.controlMode].label}${cancelledCommandIds.length > 0 ? `，撤回了 ${cancelledCommandIds.length} 条指令` : ''}`);
      onClose();
    },
    onError: (error) => {
      if (error instanceof CommandRequestError && error.code && IN_PLACE.has(error.code)) {
        setRefusal(error.message);
        return;
      }
      if (error instanceof CommandRequestError && error.code === 'REGISTRY_REVISION_CONFLICT') {
        void queryClient.invalidateQueries({ queryKey: controlStateKeys.site(siteId) });
      }
      toast.error(error instanceof CommandRequestError ? error.message : '没有保存成功，请稍后再试');
      onClose();
    },
  });
  const unchanged = target === state.controlMode;
  const needsSafeExitConfirm = target === 'SAFE_SHUTDOWN_EXIT';
  const canSubmit = !save.isPending && !unchanged && Boolean(reason.trim()) && unsettled.length === 0 && (!needsSafeExitConfirm || safeExitConfirmed);
  return (
    <form
      className="flex flex-col gap-4"
      onSubmit={(event) => { event.preventDefault(); if (canSubmit) save.mutate(); }}
    >
      <DialogHeader>
        <DialogTitle>切换到{MODE_PRESENTATION[target].label}？</DialogTitle>
        <DialogDescription className="sr-only">选择控制方式并填写原因</DialogDescription>
      </DialogHeader>
      <RadioGroup
        value={target}
        onValueChange={(value) => {
          if (isOneOf(MODE_ORDER, value)) {
            setTarget(value);
            setSafeExitConfirmed(false);
            setRefusal(null);
          }
        }}
        aria-label="控制方式"
        className="gap-2"
      >
        {MODE_ORDER.map((mode) => (
          <ChoiceCard key={mode} value={mode} title={MODE_PRESENTATION[mode].label} hint={MODE_PRESENTATION[mode].hint} current={mode === state.controlMode}>
            <ModeGlyph mode={mode} />
          </ChoiceCard>
        ))}
      </RadioGroup>
      <ReasonField id={reasonId} value={reason} onChange={setReason} placeholder="例如：夜班人工巡检，暂停远程启停" />
      {needsSafeExitConfirm ? (
        <div className="flex items-start gap-2 rounded-lg border border-destructive/30 bg-destructive-soft/35 p-3">
          <Checkbox
            id={safeExitId}
            checked={safeExitConfirmed}
            onCheckedChange={(value) => setSafeExitConfirmed(value === true)}
            className="mt-0.5"
          />
          <Label htmlFor={safeExitId} className="text-sm font-normal">
            我确认进入安全退出模式，停止平台远程及自动控制并撤回待下发指令
          </Label>
        </div>
      ) : null}
      {withdrawn.length > 0 ? (
        <div className="flex flex-col gap-1.5 rounded-lg border border-divider bg-muted p-3">
          <span className="text-xs font-medium text-secondary-foreground">切换后会撤回 {withdrawn.length} 条还没下发的指令</span>
          <ul className="flex flex-col gap-1 text-sm">
            {withdrawn.map((command) => <li key={command.commandId}>{describe(command)}（{STATUS_PRESENTATION[command.status].label}）</li>)}
          </ul>
        </div>
      ) : null}
      {unsettled.length > 0 ? (
        <Alert variant="destructive">
          <AlertTitle>还有 {unsettled.length} 条指令在下发或等现场确认，现在不能安全退出</AlertTitle>
          <AlertDescription>
            <span>{unsettled.map(describe).join('、')}。处理完再退出。</span>
            <Button type="button" variant="link" className="h-auto p-0 text-xs" onClick={onShowUnsettled}>去处理 ›</Button>
          </AlertDescription>
        </Alert>
      ) : refusal ? (
        <Alert variant="destructive"><AlertTitle>{refusal}</AlertTitle></Alert>
      ) : null}
      <DialogFooter>
        <Button type="button" variant="outline" disabled={save.isPending} onClick={onClose}>取消</Button>
        <Button
          type="submit"
          variant={target === 'SAFE_SHUTDOWN_EXIT' ? 'destructive' : 'default'}
          disabled={!canSubmit}
        >
          {save.isPending ? <Spinner data-icon="inline-start" /> : null}
          {save.isPending ? '正在切换' : unchanged ? '已是当前方式' : `切换到${MODE_PRESENTATION[target].label}`}
        </Button>
      </DialogFooter>
    </form>
  );
}

export interface LockSubject {
  readonly assetId: string;
  readonly assetName: string;
  readonly lock: AssetControlLockState | undefined;
  /** Commands on this asset that locking would withdraw. */
  readonly withdrawable: readonly WithdrawnSummary[];
}

/** Locks, relocks or unlocks one asset; a lock withdraws its commands not yet dispatched. */
export function AssetLockDialog({ siteId, csrfToken, subject, myId, timeZone, onClose }: {
  readonly siteId: string;
  readonly csrfToken: string;
  readonly subject: LockSubject | null;
  readonly myId: string;
  readonly timeZone: string;
  readonly onClose: () => void;
}) {
  const [busy, setBusy] = useState(false);
  return (
    <Dialog open={subject !== null} onOpenChange={(open) => { if (!open && !busy) onClose(); }}>
      <DialogContent
        className="sm:max-w-[440px]"
        onEscapeKeyDown={(event) => { if (busy) event.preventDefault(); }}
        onInteractOutside={(event) => { if (busy) event.preventDefault(); }}
      >
        {subject ? <LockForm key={subject.assetId} siteId={siteId} csrfToken={csrfToken} subject={subject} myId={myId} timeZone={timeZone} onBusyChange={setBusy} onClose={onClose} /> : null}
      </DialogContent>
    </Dialog>
  );
}

function LockForm({ siteId, csrfToken, subject, myId, timeZone, onBusyChange, onClose }: {
  readonly siteId: string;
  readonly csrfToken: string;
  readonly subject: LockSubject;
  readonly myId: string;
  readonly timeZone: string;
  readonly onBusyChange: (busy: boolean) => void;
  readonly onClose: () => void;
}) {
  const queryClient = useQueryClient();
  const reasonId = useId();
  const current = subject.lock;
  const choices: readonly AssetControlLockMode[] = current
    ? ['UNLOCKED', current.lockMode === 'MANUAL_LOCK' ? 'MAINTENANCE_LOCK' : 'MANUAL_LOCK']
    : ['MANUAL_LOCK', 'MAINTENANCE_LOCK'];
  const [mode, setMode] = useState<AssetControlLockMode>(choices[0]);
  const [reason, setReason] = useState('');
  const save = useMutation({
    mutationFn: () => updateAssetControlLock(subject.assetId, { lockMode: mode, reason: reason.trim(), expectedRevision: current?.revision }, csrfToken),
    onMutate: () => onBusyChange(true),
    onSettled: () => onBusyChange(false),
    onSuccess: ({ cancelledCommandIds }) => {
      void queryClient.invalidateQueries({ queryKey: controlStateKeys.site(siteId) });
      void queryClient.invalidateQueries({ queryKey: commandKeys.site(siteId) });
      toast.success(mode === 'UNLOCKED' ? `已解除 ${subject.assetName} 的锁定` : `已${LOCK_PRESENTATION[mode].label} ${subject.assetName}${cancelledCommandIds.length > 0 ? `，撤回了 ${cancelledCommandIds.length} 条指令` : ''}`);
      onClose();
    },
    onError: (error) => {
      if (error instanceof CommandRequestError && error.code === 'REGISTRY_REVISION_CONFLICT') {
        void queryClient.invalidateQueries({ queryKey: controlStateKeys.site(siteId) });
      }
      toast.error(error instanceof CommandRequestError ? error.message : '没有保存成功，请稍后再试');
      onClose();
    },
  });
  const label = (choice: AssetControlLockMode) => choice === 'UNLOCKED'
    ? { title: '解除锁定', hint: '恢复平台控制和节能建议' }
    : { title: current ? `改为${LOCK_PRESENTATION[choice].label}` : LOCK_PRESENTATION[choice].label, hint: LOCK_PRESENTATION[choice].hint };
  return (
    <form className="flex flex-col gap-4" onSubmit={(event) => { event.preventDefault(); if (reason.trim()) save.mutate(); }}>
      <DialogHeader>
        <DialogTitle>{current ? (mode === 'UNLOCKED' ? `解除 ${subject.assetName} 的锁定？` : `修改 ${subject.assetName} 的锁定？`) : `锁定 ${subject.assetName}？`}</DialogTitle>
        <DialogDescription className="sr-only">选择锁定方式并填写原因</DialogDescription>
      </DialogHeader>
      {current ? (
        <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1.5 rounded-lg border border-divider bg-muted p-3 text-xs">
          <dt className="text-muted-foreground">现在</dt>
          <dd className="font-medium">{current.lockMode === 'UNLOCKED' ? '未锁定' : LOCK_PRESENTATION[current.lockMode].label} · {controlActorLabel(current, myId)}{current.updatedAt ? ` ${formatCompactTime(current.updatedAt, timeZone)}` : ''}</dd>
          {current.reason ? <><dt className="text-muted-foreground">原因</dt><dd className="font-medium">{current.reason}</dd></> : null}
        </dl>
      ) : null}
      <RadioGroup value={mode} onValueChange={(value) => { const picked = choices.find((choice) => choice === value); if (picked) setMode(picked); }} aria-label="锁定方式" className="gap-2">
        {choices.map((choice) => <ChoiceCard key={choice} value={choice} title={label(choice).title} hint={label(choice).hint} />)}
      </RadioGroup>
      <ReasonField id={reasonId} value={reason} onChange={setReason} placeholder="例如：轴承异响，等维保" />
      {mode !== 'UNLOCKED' && subject.withdrawable.length > 0 ? (
        <div className="flex flex-col gap-1.5 rounded-lg border border-divider bg-muted p-3">
          <span className="text-xs font-medium text-secondary-foreground">锁定后会撤回这台设备 {subject.withdrawable.length} 条还没下发的指令</span>
          <ul className="flex flex-col gap-1 text-sm">{subject.withdrawable.map((item) => <li key={item.commandId}>{item.text}</li>)}</ul>
        </div>
      ) : null}
      <DialogFooter>
        <Button type="button" variant="outline" disabled={save.isPending} onClick={onClose}>取消</Button>
        <Button type="submit" disabled={save.isPending || !reason.trim()}>
          {save.isPending ? <Spinner data-icon="inline-start" /> : mode === 'UNLOCKED' ? <LockOpen data-icon="inline-start" /> : <Lock data-icon="inline-start" />}
          {save.isPending ? '正在保存' : label(mode).title}
        </Button>
      </DialogFooter>
    </form>
  );
}
