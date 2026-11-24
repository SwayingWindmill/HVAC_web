import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog';
import { Button } from '@/components/ui/button';
import { Spinner } from '@/components/ui/spinner';
import type { Command, ReconciledOutcome } from '../command-api';

export type PendingCommandAction =
  | { readonly kind: 'approve'; readonly command: Command }
  | { readonly kind: 'reconcile'; readonly command: Command; readonly outcome: ReconciledOutcome };

export function CommandActionConfirmDialog({
  pendingAction,
  summary,
  busy,
  onConfirm,
  onClose,
}: {
  readonly pendingAction: PendingCommandAction | null;
  readonly summary?: string;
  readonly busy: boolean;
  readonly onConfirm: (action: PendingCommandAction) => void;
  readonly onClose: () => void;
}) {
  const title = !pendingAction
    ? ''
    : pendingAction.kind === 'approve'
      ? '确认批准下发该控制指令？'
      : pendingAction.outcome === 'APPLIED'
        ? '确认登记为「设备已按要求动作」？'
        : '确认登记为「设备未按要求动作」？';

  const description = !pendingAction
    ? ''
    : pendingAction.kind === 'approve'
      ? '批准后将推进指令审批并向边缘网关下发执行，请确认设备名称与动作内容无误。'
      : '请先在现场或控制器画面核实设备实际状态。登记结果一旦写入即不可撤销，设备操作冻结也将随之解除。';

  const confirmLabel = !pendingAction
    ? ''
    : pendingAction.kind === 'approve'
      ? busy ? '批准中…' : '确认批准'
      : pendingAction.outcome === 'APPLIED'
        ? busy ? '登记中…' : '确认已执行'
        : busy ? '登记中…' : '确认未执行';

  return (
    <AlertDialog
      open={pendingAction !== null}
      onOpenChange={(open) => {
        if (!open && !busy) onClose();
      }}
    >
      <AlertDialogContent
        onEscapeKeyDown={(event) => {
          if (busy) event.preventDefault();
        }}
      >
        <AlertDialogHeader>
          <AlertDialogTitle>{title}</AlertDialogTitle>
          <AlertDialogDescription>{description}</AlertDialogDescription>
        </AlertDialogHeader>
        {pendingAction ? (
          <div className="flex flex-col gap-1 rounded-md border border-divider bg-muted/30 px-3 py-2 text-xs text-muted-foreground">
            {summary ? <span className="font-medium text-foreground">{summary}</span> : null}
            <span>
              指令编号：<span className="font-mono text-foreground">{pendingAction.command.commandId}</span>
            </span>
          </div>
        ) : null}
        <AlertDialogFooter>
          <AlertDialogCancel disabled={busy}>取消</AlertDialogCancel>
          <Button
            type="button"
            variant={pendingAction?.kind === 'reconcile' ? 'destructive' : 'default'}
            disabled={busy || !pendingAction}
            onClick={() => {
              if (pendingAction && !busy) onConfirm(pendingAction);
            }}
          >
            {busy ? <Spinner data-icon="inline-start" /> : null}
            {confirmLabel}
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
