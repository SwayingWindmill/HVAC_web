import { useMutation, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';
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
import { ReadingValue } from '@/features/operations/realtime/device-presentation';
import type { PlantReading } from '@/features/operations/realtime/plant-model';
import { formatDecimal } from '@/lib/operator-format';
import { CommandRequestError, commandKeys, createCommand } from '../command-api';
import { APPROVAL_LABEL } from '../command-presentation';
import type { ControlTarget } from '../control-model';

/** A command waiting for the operator's confirmation. */
export interface PendingCommand {
  readonly target: ControlTarget;
  readonly label: string;
  readonly pointId: string;
  readonly parameters: Readonly<Record<string, number>>;
  readonly change?: { readonly from: PlantReading | null; readonly to: number; readonly unit: string | null };
}

export function commandFailure(error: unknown) {
  toast.error(error instanceof CommandRequestError ? error.message : '指令服务暂时不可用');
}

/** Submits a confirmed command to Command Governance and refreshes the Site's command log. */
export function useSubmitCommand(siteId: string, csrfToken: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (pending: PendingCommand) =>
      createCommand({ assetId: pending.target.assetId, commandPointId: pending.pointId, parameters: pending.parameters }, csrfToken),
    onSuccess: (command) => {
      toast.success(command.status === 'AWAITING_APPROVAL' ? `指令已提交，${APPROVAL_LABEL[command.approvalPolicy]}` : '指令已提交，正在下发');
      void queryClient.invalidateQueries({ queryKey: commandKeys.site(siteId) });
    },
    onError: commandFailure,
  });
}

export function ConfirmCommand({ pending, submitting, onCancel, onConfirm }: {
  readonly pending: PendingCommand | null;
  readonly submitting: boolean;
  readonly onCancel: () => void;
  readonly onConfirm: () => void;
}) {
  return (
    <AlertDialog open={Boolean(pending)} onOpenChange={(open) => { if (!open && !submitting) onCancel(); }}>
      <AlertDialogContent>
        {pending ? (
          <>
            <AlertDialogHeader>
              <AlertDialogTitle>向 {pending.target.assetName} 下发指令？</AlertDialogTitle>
              <AlertDialogDescription>
                {pending.change ? (
                  <span className="flex flex-col gap-2">
                    <span>{pending.label}</span>
                    <span className="flex items-baseline gap-2 text-base text-foreground">
                      <ReadingValue reading={pending.change.from} className="font-semibold" />
                      <span aria-hidden="true">→</span>
                      <span className="font-semibold tabular-nums">{formatDecimal(pending.change.to, 1)}{pending.change.unit ? ` ${pending.change.unit}` : ''}</span>
                    </span>
                  </span>
                ) : `「${pending.label}」`}
                <span className="mt-3 block">指令先经过风险评估；中高风险的指令要审批后才会下发到设备。</span>
              </AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel disabled={submitting}>取消</AlertDialogCancel>
              <AlertDialogAction disabled={submitting} onClick={(event) => { event.preventDefault(); onConfirm(); }}>
                {submitting ? <Spinner data-icon="inline-start" /> : null}
                确认下发
              </AlertDialogAction>
            </AlertDialogFooter>
          </>
        ) : null}
      </AlertDialogContent>
    </AlertDialog>
  );
}
