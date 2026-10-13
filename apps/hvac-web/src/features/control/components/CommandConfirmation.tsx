import { useMutation, useQueryClient } from '@tanstack/react-query';
import { ArrowRight } from 'lucide-react';
import { Fragment } from 'react';
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
  /** What the operator should see before sending: the device's state, the allowed range, the reading's time. */
  readonly facts?: readonly { readonly label: string; readonly value: string }[];
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

/** How far the change moves the reading, e.g. +1.0 °C; nothing when the current reading is unknown. */
function delta(change: NonNullable<PendingCommand['change']>): string | null {
  const from = change.from?.state === 'PRESENT' ? change.from.numeric : null;
  if (from === null) return null;
  const by = change.to - from;
  return `${by >= 0 ? '+' : '−'}${formatDecimal(Math.abs(by), 1)}${change.unit ? ` ${change.unit}` : ''}`;
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
              <AlertDialogDescription>指令先经过风险评估；中高风险的指令要审批后才会下发到设备。</AlertDialogDescription>
            </AlertDialogHeader>
            <div className="flex flex-col gap-3 rounded-lg border bg-muted/40 p-4">
              <span className="text-sm text-muted-foreground">{pending.change ? pending.label : `「${pending.label}」`}</span>
              {pending.change ? (
                <span className="flex items-baseline gap-2.5 text-lg">
                  <ReadingValue reading={pending.change.from} className="font-semibold" />
                  <ArrowRight className="size-4 self-center text-muted-foreground" aria-label="改为" />
                  <span className="font-semibold tabular-nums">{formatDecimal(pending.change.to, 1)}{pending.change.unit ? ` ${pending.change.unit}` : ''}</span>
                  {delta(pending.change) ? <span className="rounded-md bg-primary-soft px-1.5 text-xs font-medium text-primary">{delta(pending.change)}</span> : null}
                </span>
              ) : null}
              {pending.facts && pending.facts.length > 0 ? (
                <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1.5 border-t border-divider pt-3 text-xs">
                  {pending.facts.map((fact) => (
                    <Fragment key={fact.label}>
                      <dt className="text-muted-foreground">{fact.label}</dt>
                      <dd className="font-medium">{fact.value}</dd>
                    </Fragment>
                  ))}
                </dl>
              ) : null}
            </div>
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
