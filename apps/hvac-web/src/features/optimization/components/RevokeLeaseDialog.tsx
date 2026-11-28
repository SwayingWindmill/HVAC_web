import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useId, useState } from 'react';
import { notifySuccess } from '@/lib/toast';
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
import { Spinner } from '@/components/ui/spinner';
import { ReasonField } from '@/features/control/components/ControlBoundary';
import { formatDecimal, formatTime } from '@/lib/operator-format';
import {
  autoControlKeys,
  AutoControlRequestError,
  revokeSiteAutoControlLease,
  type SiteAutoControlLeaseState,
} from '../auto-control-api';
import { resolveAutoControlExecutionMode } from '../auto-control-model';

export function RevokeLeaseDialog({
  siteId,
  csrfToken,
  timeZone,
  leaseState,
  open,
  onOpenChange,
}: {
  readonly siteId: string;
  readonly csrfToken: string;
  readonly timeZone: string;
  readonly leaseState: SiteAutoControlLeaseState;
  readonly open: boolean;
  readonly onOpenChange: (open: boolean) => void;
}) {
  const [busy, setBusy] = useState(false);
  return (
    <Dialog open={open} onOpenChange={(nextOpen) => { if (!nextOpen && !busy) onOpenChange(false); else if (nextOpen) onOpenChange(true); }}>
      <DialogContent
        showCloseButton={!busy}
        onEscapeKeyDown={(event) => {
          if (busy) event.preventDefault();
        }}
        onInteractOutside={(event) => {
          if (busy) event.preventDefault();
        }}
        className="sm:max-w-[480px]"
      >
        {open && leaseState.activeLease ? (
          <RevokeLeaseForm
            siteId={siteId}
            csrfToken={csrfToken}
            timeZone={timeZone}
            activeLease={leaseState.activeLease}
            onBusyChange={setBusy}
            onClose={() => onOpenChange(false)}
          />
        ) : null}
      </DialogContent>
    </Dialog>
  );
}

function RevokeLeaseForm({
  siteId,
  csrfToken,
  timeZone,
  activeLease,
  onBusyChange,
  onClose,
}: {
  readonly siteId: string;
  readonly csrfToken: string;
  readonly timeZone: string;
  readonly activeLease: NonNullable<SiteAutoControlLeaseState['activeLease']>;
  readonly onBusyChange: (busy: boolean) => void;
  readonly onClose: () => void;
}) {
  const queryClient = useQueryClient();
  const reasonId = useId();
  const [reason, setReason] = useState('');
  const [alertMessage, setAlertMessage] = useState<string | null>(null);

  const mutation = useMutation({
    mutationFn: () =>
      revokeSiteAutoControlLease(
        siteId,
        { reason: reason.trim(), expectedRevision: activeLease.revision },
        csrfToken,
      ),
    onMutate: () => onBusyChange(true),
    onSettled: () => onBusyChange(false),
    onSuccess: (next) => {
      queryClient.setQueryData(autoControlKeys.lease(siteId), next);
      void queryClient.invalidateQueries({ queryKey: autoControlKeys.lease(siteId) });
      void queryClient.invalidateQueries({ queryKey: autoControlKeys.monitors(siteId) });
      notifySuccess('已撤回自动控制租约，恢复为人工确认下发');
      onClose();
    },
    onError: (error) => {
      if (error instanceof AutoControlRequestError && error.code === 'REGISTRY_REVISION_CONFLICT') {
        void queryClient.invalidateQueries({ queryKey: autoControlKeys.lease(siteId) });
      }
      setAlertMessage(error instanceof AutoControlRequestError ? error.message : '撤回自动控制租约失败，请稍后再试');
    },
  });

  const ready = reason.trim().length > 0 && reason.trim().length <= 1000;

  return (
    <form
      className="flex flex-col gap-4"
      onSubmit={(event) => {
        event.preventDefault();
        if (ready && !mutation.isPending) mutation.mutate();
      }}
    >
      <DialogHeader>
        <DialogTitle>撤回自动控制租约？</DialogTitle>
        <DialogDescription>
          撤回后，站点立即恢复为「人工确认下发」模式，平台不再自动向冷机下发优化建议。
        </DialogDescription>
      </DialogHeader>

      <div className="rounded-lg border border-divider bg-muted px-3.5 py-2.5 text-xs text-secondary-foreground">
        当前{resolveAutoControlExecutionMode(activeLease) === 'SHADOW' ? '影子跟跑' : '自动控制'}租约有效期至{' '}
        <span className="font-mono font-medium text-foreground">{formatTime(activeLease.expiresAt, timeZone)}</span>，
        供水范围 {formatDecimal(activeLease.supplyTempMinC, 1)} 至 {formatDecimal(activeLease.supplyTempMaxC, 1)} °C。
      </div>

      <ReasonField
        id={reasonId}
        value={reason}
        onChange={(next) => {
          setReason(next);
          setAlertMessage(null);
        }}
        placeholder="例如：夜间巡检，暂停自动下发"
      />

      {alertMessage ? (
        <Alert variant="destructive">
          <AlertTitle>未能撤回租约</AlertTitle>
          <AlertDescription>{alertMessage}</AlertDescription>
        </Alert>
      ) : null}

      <DialogFooter>
        <Button type="button" variant="outline" disabled={mutation.isPending} onClick={onClose}>
          取消
        </Button>
        <Button type="submit" variant="destructive" disabled={mutation.isPending || !ready}>
          {mutation.isPending ? <Spinner data-icon="inline-start" /> : null}
          {mutation.isPending ? '正在撤回' : '确认撤回租约'}
        </Button>
      </DialogFooter>
    </form>
  );
}
