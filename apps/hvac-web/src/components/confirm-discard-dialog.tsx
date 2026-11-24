import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog';
import { Spinner } from '@/components/ui/spinner';

export interface ConfirmDiscardDialogProps {
  readonly open: boolean;
  readonly onOpenChange: (open: boolean) => void;
  readonly busy?: boolean;
  readonly title?: string;
  readonly description?: string;
  readonly keepLabel?: string;
  readonly discardLabel?: string;
  readonly busyLabel?: string;
  readonly onConfirmDiscard: () => void;
}

/** Shared confirmation dialog before discarding unsaved edits or confirming an irreversible action. */
export function ConfirmDiscardDialog({
  open,
  onOpenChange,
  busy = false,
  title = '放弃未保存的修改吗？',
  description = '当前有还没保存的修改，放弃后将恢复到修改前的状态。',
  keepLabel = '继续编辑',
  discardLabel = '放弃修改',
  busyLabel,
  onConfirmDiscard,
}: ConfirmDiscardDialogProps) {
  return (
    <AlertDialog
      open={open}
      onOpenChange={(nextOpen) => {
        if (!nextOpen && busy) return;
        onOpenChange(nextOpen);
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
        <AlertDialogFooter>
          <AlertDialogCancel disabled={busy}>{keepLabel}</AlertDialogCancel>
          <AlertDialogAction
            variant="destructive"
            disabled={busy}
            onClick={(event) => {
              if (busyLabel !== undefined) event.preventDefault();
              if (!busy) onConfirmDiscard();
            }}
          >
            {busy ? <Spinner data-icon="inline-start" /> : null}
            {busy ? (busyLabel ?? discardLabel) : discardLabel}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
