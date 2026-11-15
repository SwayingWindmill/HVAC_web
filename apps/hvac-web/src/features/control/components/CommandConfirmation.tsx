import { useMutation, useQueryClient } from '@tanstack/react-query';
import { ArrowRight } from 'lucide-react';
import { Fragment, useId, useState } from 'react';
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
import { Checkbox } from '@/components/ui/checkbox';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Spinner } from '@/components/ui/spinner';
import { ReadingValue } from '@/features/operations/realtime/device-presentation';
import type { PlantReading } from '@/features/operations/realtime/plant-model';
import { formatDecimal } from '@/lib/operator-format';
import { CommandRequestError, commandKeys, createCommand, type Command } from '../command-api';
import { APPROVAL_LABEL } from '../command-presentation';
import type { ControlTarget } from '../control-model';

/** A command waiting for the operator's confirmation. */
export interface PendingCommand {
  readonly target: ControlTarget;
  /** The question the dialog asks, e.g. 一键停机 C 机组？ */
  readonly title?: string;
  readonly label: string;
  readonly pointId: string;
  readonly parameters: Readonly<Record<string, number>>;
  readonly change?: { readonly from: PlantReading | null; readonly to: number; readonly unit: string | null };
  /** The order a unit sequence runs, by asset name. */
  readonly steps?: readonly string[];
  /** What the operator should see before sending: the device's state, the allowed range, the reading's time. */
  readonly facts?: readonly { readonly label: string; readonly value: string }[];
  /** Ask for the authenticator code up front: starting or stopping equipment is high risk. */
  readonly requiresCode?: boolean;
  /** A stop or start the operator must confirm with the site first; the submit stays disabled until ticked. */
  readonly acknowledgement?: string;
  readonly submitLabel?: string;
}

const CODE_PATTERN = /^\d{6}$/;

export function commandFailure(error: unknown) {
  toast.error(error instanceof CommandRequestError ? error.message : '指令服务暂时不可用，请稍后再试');
}

/** Submits a confirmed command to Command Governance and refreshes the Site's command log. */
export function useSubmitCommand(siteId: string, csrfToken: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ pending, verificationCode }: { readonly pending: PendingCommand; readonly verificationCode?: string }) =>
      createCommand({ assetId: pending.target.assetId, commandPointId: pending.pointId, parameters: pending.parameters, verificationCode }, csrfToken),
    onSuccess: (command: Command) => {
      toast.success(command.status === 'AWAITING_APPROVAL' ? `已提交，${APPROVAL_LABEL[command.approvalPolicy]}后下发` : '已提交，正在下发');
      void queryClient.invalidateQueries({ queryKey: commandKeys.site(siteId) });
    },
  });
}

/** How far the change moves the reading, e.g. +1.0 °C; nothing when the current reading is unknown. */
function delta(change: NonNullable<PendingCommand['change']>): string | null {
  const from = change.from?.state === 'PRESENT' ? change.from.numeric : null;
  if (from === null) return null;
  const by = change.to - from;
  return `${by >= 0 ? '+' : '−'}${formatDecimal(Math.abs(by), 1)}${change.unit ? ` ${change.unit}` : ''}`;
}

export function ConfirmCommand({ pending, siteId, csrfToken, onClose }: {
  readonly pending: PendingCommand | null;
  readonly siteId: string;
  readonly csrfToken: string;
  readonly onClose: () => void;
}) {
  return (
    <AlertDialog open={Boolean(pending)} onOpenChange={(open) => { if (!open) onClose(); }}>
      <AlertDialogContent>
        {pending ? <ConfirmBody key={pending.pointId + JSON.stringify(pending.parameters)} pending={pending} siteId={siteId} csrfToken={csrfToken} onClose={onClose} /> : null}
      </AlertDialogContent>
    </AlertDialog>
  );
}

function ConfirmBody({ pending, siteId, csrfToken, onClose }: {
  readonly pending: PendingCommand;
  readonly siteId: string;
  readonly csrfToken: string;
  readonly onClose: () => void;
}) {
  const submit = useSubmitCommand(siteId, csrfToken);
  const codeId = useId();
  const ackId = useId();
  const [askCode, setAskCode] = useState(pending.requiresCode === true);
  const [code, setCode] = useState('');
  const [acknowledged, setAcknowledged] = useState(false);
  const [codeError, setCodeError] = useState<string | null>(null);
  const ready = (!askCode || CODE_PATTERN.test(code)) && (!pending.acknowledgement || acknowledged);
  const confirm = () => {
    submit.mutate({ pending, verificationCode: askCode ? code : undefined }, {
      onSuccess: onClose,
      onError: (error) => {
        // A refused or missing code keeps the dialog open so the operator types the next one.
        if (error instanceof CommandRequestError && error.code === 'STEP_UP_REQUIRED') {
          setAskCode(true);
          setCode('');
          setCodeError(askCode ? '动态口令不对或已经用过，请输入认证器上新的 6 位数字。' : '这条指令被评为高风险，请输入动态口令。');
          return;
        }
        commandFailure(error);
        onClose();
      },
    });
  };
  return (
    <>
      <AlertDialogHeader>
        <AlertDialogTitle>{pending.title ?? `向 ${pending.target.assetName} 下发指令？`}</AlertDialogTitle>
        <AlertDialogDescription className="sr-only">确认指令内容后提交</AlertDialogDescription>
      </AlertDialogHeader>
      <div className="flex flex-col gap-3 rounded-lg border border-divider bg-muted p-3">
        {pending.steps && pending.steps.length > 0 ? (
          <div className="flex flex-col gap-2">
            <ol aria-label="执行顺序" className="flex flex-wrap items-center gap-1.5">
              {pending.steps.map((step, index) => (
                <li key={step} className="inline-flex h-6 items-center gap-1.5 rounded-md border bg-card px-2 text-xs">
                  <span className="font-mono text-[11px] text-muted-foreground">{index + 1}</span>{step}
                </li>
              ))}
            </ol>
            <span className="text-xs text-muted-foreground">依次执行，任一步失败即停下</span>
          </div>
        ) : null}
        {pending.change ? (
          <div className="flex flex-col gap-1">
            <span className="text-xs text-muted-foreground">{pending.label}</span>
            <span className="flex items-baseline gap-2.5 text-lg">
              <ReadingValue reading={pending.change.from} className="font-semibold" />
              <ArrowRight className="size-4 self-center text-muted-foreground" aria-label="改为" />
              <span className="font-semibold tabular-nums">{formatDecimal(pending.change.to, 1)}{pending.change.unit ? ` ${pending.change.unit}` : ''}</span>
              {delta(pending.change) ? <span className="rounded-md bg-primary-soft px-1.5 text-xs font-medium text-primary">{delta(pending.change)}</span> : null}
            </span>
          </div>
        ) : null}
        {pending.facts && pending.facts.length > 0 ? (
          <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1.5 text-xs">
            {pending.facts.map((fact) => (
              <Fragment key={fact.label}>
                <dt className="text-muted-foreground">{fact.label}</dt>
                <dd className="font-medium">{fact.value}</dd>
              </Fragment>
            ))}
          </dl>
        ) : null}
      </div>
      {askCode ? (
        <div className="flex flex-col gap-1.5">
          <Label htmlFor={codeId} className="flex justify-between text-xs">
            动态口令
            <span className="font-normal text-muted-foreground">打开身份验证器查看 6 位数字</span>
          </Label>
          <Input
            id={codeId}
            inputMode="numeric"
            autoComplete="one-time-code"
            placeholder="······"
            value={code}
            aria-invalid={codeError !== null}
            aria-describedby={codeError ? `${codeId}-error` : undefined}
            onChange={(event) => { setCode(event.target.value.replace(/\D/g, '').slice(0, 6)); setCodeError(null); }}
            className="h-10 text-center font-mono text-xl tracking-[0.5em]"
          />
          {codeError ? <p id={`${codeId}-error`} className="text-xs text-destructive-text">{codeError}</p> : null}
        </div>
      ) : null}
      {pending.acknowledgement ? (
        <div className="flex items-start gap-2">
          <Checkbox id={ackId} checked={acknowledged} onCheckedChange={(value) => setAcknowledged(value === true)} className="mt-0.5" />
          <Label htmlFor={ackId} className="text-sm font-normal">{pending.acknowledgement}</Label>
        </div>
      ) : null}
      <AlertDialogFooter>
        <AlertDialogCancel disabled={submit.isPending}>取消</AlertDialogCancel>
        <AlertDialogAction
          variant={pending.acknowledgement ? 'destructive' : 'default'}
          disabled={submit.isPending || !ready}
          onClick={(event) => { event.preventDefault(); confirm(); }}
        >
          {submit.isPending ? <Spinner data-icon="inline-start" /> : null}
          {submit.isPending ? '正在提交' : (pending.submitLabel ?? '确认下发')}
        </AlertDialogAction>
      </AlertDialogFooter>
    </>
  );
}
