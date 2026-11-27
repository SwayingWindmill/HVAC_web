import { TriangleAlert } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';
import { formatCompactTime } from '@/lib/operator-format';
import { SETTLED, WITHDRAWABLE, type Command } from '../command-api';
import { APPROVAL_LABEL, commandSummary, RISK_LABEL, STATUS_PRESENTATION } from '../command-presentation';
import type { CommandLabel } from '../control-model';
import type { CommandActions } from './CommandLog';

/**
 * Layer 2 conditional active execution & blocking channel.
 * Renders inline approval, reconciliation, and withdrawal controls whenever open commands exist;
 * collapses cleanly to zero height when idle.
 */
export function ActiveCommandChannel({
  commands,
  labels,
  assetIdByDeviceId,
  timeZone,
  actions,
  onSelectAsset,
  onInspect,
}: {
  readonly commands: readonly Command[];
  readonly labels: ReadonlyMap<string, CommandLabel>;
  readonly assetIdByDeviceId: ReadonlyMap<string, string>;
  readonly timeZone: string;
  readonly actions: CommandActions;
  readonly onSelectAsset: (assetId: string) => void;
  readonly onInspect: (commandId: string) => void;
}) {
  const live = commands.filter((command) => !SETTLED.has(command.status));
  if (live.length === 0) return null;

  return (
    <section
      aria-label="阻塞与执行通道"
      className="flex flex-wrap items-center gap-2 border-b border-border bg-muted/65 px-4 py-1.5"
    >
      <div className="inline-flex shrink-0 items-center gap-1.5 border-r border-border pr-2.5 text-xs font-semibold text-warning-text">
        <TriangleAlert className="size-3.5 shrink-0" aria-hidden="true" />
        <span>阻塞与执行 ({live.length})</span>
      </div>

      <div className="flex min-w-0 flex-1 flex-wrap items-center gap-2">
        {live.map((command) => {
          const summary = commandSummary(command, labels);
          const targetAssetId = assetIdByDeviceId.get(command.deviceId);
          const isApproval = command.status === 'AWAITING_APPROVAL';
          const isUnknown = command.status === 'OUTCOME_UNKNOWN';
          const pillTone = isApproval
            ? 'bg-warning-soft text-warning-text'
            : isUnknown
              ? 'bg-destructive-soft text-destructive-text'
              : 'bg-primary-soft text-primary';

          return (
            <div
              key={command.commandId}
              className="inline-flex max-w-full flex-wrap items-center gap-1.5 rounded-md border border-border bg-card py-0.5 pr-1.5 pl-2 text-xs shadow-xs"
            >
              <span className={cn('rounded-sm px-1.5 py-0.5 text-[11px] leading-none font-semibold', pillTone)}>
                {STATUS_PRESENTATION[command.status].label}
              </span>
              <button
                type="button"
                className="max-w-[260px] truncate text-left font-medium text-foreground hover:text-primary hover:underline focus-visible:underline"
                onClick={() => {
                  if (targetAssetId) onSelectAsset(targetAssetId);
                  else onInspect(command.commandId);
                }}
              >
                {summary.target} · {summary.title}
              </button>
              <span className="text-[11px] text-muted-foreground">
                {RISK_LABEL[command.risk]}
                {isApproval && command.requiredApprovalCount > 0
                  ? ` · ${APPROVAL_LABEL[command.approvalPolicy]} ${command.approvalCount}/${command.requiredApprovalCount}`
                  : ''}
              </span>
              <span className="font-mono text-[11px] text-muted-foreground">
                {formatCompactTime(command.createdAt, timeZone)}
              </span>

              {isApproval ? (
                <>
                  <Button
                    type="button"
                    size="xs"
                    disabled={actions.busy || !actions.canApprove}
                    title={actions.canApprove ? undefined : '当前控制方式不允许批准下发'}
                    onClick={() => actions.onApprove(command)}
                  >
                    批准
                  </Button>
                  <Button
                    type="button"
                    variant="outline"
                    size="xs"
                    disabled={actions.busy}
                    onClick={() => actions.onCancel(command)}
                  >
                    撤回
                  </Button>
                </>
              ) : isUnknown ? (
                <>
                  <Button
                    type="button"
                    variant="outline"
                    size="xs"
                    disabled={actions.busy}
                    onClick={() => actions.onReconcile(command.commandId, 'APPLIED')}
                  >
                    已生效
                  </Button>
                  <Button
                    type="button"
                    variant="outline"
                    size="xs"
                    disabled={actions.busy}
                    onClick={() => actions.onReconcile(command.commandId, 'NOT_APPLIED')}
                  >
                    没生效
                  </Button>
                </>
              ) : WITHDRAWABLE.has(command.status) ? (
                <Button
                  type="button"
                  variant="outline"
                  size="xs"
                  disabled={actions.busy}
                  onClick={() => actions.onCancel(command)}
                >
                  撤回
                </Button>
              ) : (
                <Button type="button" variant="ghost" size="xs" onClick={() => onInspect(command.commandId)}>
                  经过 ›
                </Button>
              )}
            </div>
          );
        })}
      </div>
    </section>
  );
}
