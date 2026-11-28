import { Link2, Terminal } from 'lucide-react';
import { notifyError, notifySuccess } from '@/lib/toast';
import { platformGatewayClient } from '@/api/platform-client';
import { Button } from '@/components/ui/button';
import { ButtonGroup } from '@/components/ui/button-group';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '@/components/ui/sheet';
import { Skeleton } from '@/components/ui/skeleton';
import {
  Timeline,
  TimelineConnector,
  TimelineContent,
  TimelineDescription,
  TimelineHeader,
  TimelineItem,
  TimelineMarker,
  TimelineRail,
  TimelineTime,
  TimelineTitle,
} from '@/components/ui/timeline';
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group';
import { formatClock } from '@/features/operations/realtime/device-presentation';
import { formatCompactTime, formatTime } from '@/lib/operator-format';
import { cn } from '@/lib/utils';
import { WITHDRAWABLE, type Command, type ReconciledOutcome } from '../command-api';
import { APPROVAL_LABEL, COMMAND_SOURCE_LABEL, commandSummary, IN_FLIGHT, PRIORITY_LABEL, RISK_LABEL, STATUS_PRESENTATION, transitionReasonNote } from '../command-presentation';
import type { CommandLabel } from '../control-model';
import { CommandMark } from './CommandMark';

export interface CommandActions {
  readonly busy: boolean;
  readonly canApprove: boolean;
  readonly onApprove: (command: Command) => void;
  readonly onCancel: (command: Command) => void;
  readonly onReconcile: (commandId: string, outcome: ReconciledOutcome) => void;
}

function approvalText(command: Command) {
  return command.requiredApprovalCount > 0 ? `${APPROVAL_LABEL[command.approvalPolicy]} · ${command.approvalCount}/${command.requiredApprovalCount}` : APPROVAL_LABEL[command.approvalPolicy];
}

/** What the operator has to do about the command, when anything. */
function outcomeNote(command: Command): string | null {
  return command.status === 'OUTCOME_UNKNOWN' ? '设备没返回执行结果，去现场看一眼再记录' : null;
}

function ReconcileButtons({ commandId, busy, onReconcile }: { readonly commandId: string; readonly busy: boolean; readonly onReconcile: CommandActions['onReconcile'] }) {
  return (
    <ButtonGroup aria-label="现场确认执行结果">
      <Button size="sm" variant="outline" disabled={busy} onClick={() => onReconcile(commandId, 'APPLIED')}>已生效</Button>
      <Button size="sm" variant="outline" disabled={busy} onClick={() => onReconcile(commandId, 'NOT_APPLIED')}>没生效</Button>
    </ButtonGroup>
  );
}

function Actions({ command, actions }: { readonly command: Command; readonly actions: CommandActions }) {
  if (command.status === 'AWAITING_APPROVAL') {
    return (
      <div className="flex gap-2">
        {actions.canApprove ? <Button size="sm" disabled={actions.busy} onClick={() => actions.onApprove(command)}>批准</Button> : null}
        <Button size="sm" variant="outline" disabled={actions.busy} onClick={() => actions.onCancel(command)}>撤回</Button>
      </div>
    );
  }
  if (WITHDRAWABLE.has(command.status)) {
    return <Button size="sm" variant="outline" className="self-start" disabled={actions.busy} onClick={() => actions.onCancel(command)}>撤回</Button>;
  }
  if (command.status === 'OUTCOME_UNKNOWN') {
    return (
      <div className="flex items-center gap-2">
        <span className="text-xs text-muted-foreground">现场看过了：</span>
        <ReconcileButtons commandId={command.commandId} busy={actions.busy} onReconcile={actions.onReconcile} />
      </div>
    );
  }
  return null;
}

export function CommandLog({ commands, labels, timeZone, selectedId, filtered, actions, onInspect }: {
  readonly commands: readonly Command[];
  readonly labels: ReadonlyMap<string, CommandLabel>;
  readonly timeZone: string;
  readonly selectedId: string | undefined;
  readonly filtered: boolean;
  readonly actions: CommandActions;
  readonly onInspect: (commandId: string) => void;
}) {
  if (commands.length === 0) {
    return (
      <div className="flex flex-col items-center gap-2 px-6 py-12 text-center">
        <span className="flex size-9 items-center justify-center rounded-xl bg-muted text-muted-foreground"><Terminal className="size-[18px]" aria-hidden="true" /></span>
        <p className="text-sm font-semibold">{filtered ? '没有这类指令' : '还没有指令'}</p>
        <p className="text-xs text-muted-foreground">{filtered ? '切到「全部」看历史记录' : '下发的控制指令会记在这里'}</p>
      </div>
    );
  }
  return (
    <ul aria-label="指令记录">
      {commands.map((command) => {
        const summary = commandSummary(command, labels);
        const note = outcomeNote(command);
        return (
          <li
            key={command.commandId}
            className={cn('grid grid-cols-[12px_minmax(0,1fr)_auto] gap-x-2.5 gap-y-1 border-b border-divider px-4 py-3 last:border-b-0', command.commandId === selectedId && 'bg-primary-soft')}
          >
            <span className="mt-[5px]"><CommandMark status={command.status} /></span>
            <button type="button" className="min-w-0 truncate text-left text-sm outline-none hover:underline focus-visible:underline" onClick={() => onInspect(command.commandId)}>
              <span className="font-semibold">{summary.target}</span> · {summary.title}
            </button>
            <span className="font-mono text-xs whitespace-nowrap text-muted-foreground">{formatCompactTime(command.createdAt, timeZone)}</span>
            <span className="col-start-2 col-end-4 text-xs text-muted-foreground">
              {STATUS_PRESENTATION[command.status].label} · {RISK_LABEL[command.risk]}{command.status === 'AWAITING_APPROVAL' ? ` · ${approvalText(command)}` : ''}{command.priorityTier ? ` · ${PRIORITY_LABEL[command.priorityTier]}` : ''}
            </span>
            {note ? <span className="col-start-2 col-end-4 text-xs text-secondary-foreground">{note}</span> : null}
            <div className="col-start-2 col-end-4 empty:hidden"><Actions command={command} actions={actions} /></div>
          </li>
        );
      })}
    </ul>
  );
}

export function CommandSheet({ command, labels, timeZone, actions, onClose }: {
  readonly command: Command | undefined;
  readonly labels: ReadonlyMap<string, CommandLabel>;
  readonly timeZone: string;
  readonly actions: CommandActions;
  readonly onClose: () => void;
}) {
  const summary = command ? commandSummary(command, labels) : null;
  const copyLink = () => {
    void navigator.clipboard.writeText(window.location.href).then(() => notifySuccess('链接已复制'), () => notifyError('无法复制链接'));
  };
  return (
    <Sheet open={Boolean(command)} onOpenChange={(open) => { if (!open) onClose(); }}>
      <SheetContent className="overflow-y-auto sm:max-w-[480px]">
        {command && summary ? (
          <>
            <SheetHeader className="gap-2 border-b border-divider px-6 pt-5 pb-4">
              <div className="flex items-center gap-2 pr-8 text-xs">
                <span className="inline-flex items-center gap-1.5 font-medium"><CommandMark status={command.status} />{STATUS_PRESENTATION[command.status].label}</span>
                <span className="rounded-md bg-secondary px-1.5 leading-5 text-secondary-foreground">{RISK_LABEL[command.risk]}</span>
                {command.priorityTier ? (
                  <span className="rounded-md bg-muted px-1.5 leading-5 text-secondary-foreground">{PRIORITY_LABEL[command.priorityTier]}</span>
                ) : null}
                <Button variant="ghost" size="icon-sm" className="ml-auto text-muted-foreground" aria-label="复制链接" onClick={copyLink}><Link2 className="size-3.5" /></Button>
              </div>
              <SheetTitle className="text-lg">{summary.title}</SheetTitle>
              <SheetDescription>{summary.target} · 提交于 <span className="font-mono">{formatTime(command.createdAt, timeZone)}</span></SheetDescription>
            </SheetHeader>
            <section className="flex flex-col gap-3 border-b border-divider px-6 py-4">
              <dl className="grid grid-cols-2 gap-x-6 gap-y-3 text-sm">
                <div className="flex flex-col gap-0.5"><dt className="text-xs text-muted-foreground">审批</dt><dd className="font-medium">{approvalText(command)}</dd></div>
                <div className="flex flex-col gap-0.5"><dt className="text-xs text-muted-foreground">最后更新</dt><dd className="font-mono font-medium">{formatTime(command.updatedAt, timeZone)}</dd></div>
                {command.priorityTier ? (
                  <div className="flex flex-col gap-0.5">
                    <dt className="text-xs text-muted-foreground">控制优先级</dt>
                    <dd className="font-medium">{PRIORITY_LABEL[command.priorityTier]}</dd>
                  </div>
                ) : null}
                {command.commandSource ? (
                  <div className="flex flex-col gap-0.5">
                    <dt className="text-xs text-muted-foreground">控制来源</dt>
                    <dd className="font-medium">{COMMAND_SOURCE_LABEL[command.commandSource]}</dd>
                  </div>
                ) : null}
              </dl>
              {outcomeNote(command) ? <p className="text-sm text-secondary-foreground">{outcomeNote(command)}</p> : null}
              <Actions command={command} actions={actions} />
            </section>
            <section aria-label="指令经过" className="flex flex-col gap-3 px-6 py-4">
              <h3 className="text-sm font-semibold">经过</h3>
              <Timeline>
                {command.transitions.map((transition, index) => (
                  <TimelineItem key={transition.version} status={index === command.transitions.length - 1 ? 'current' : 'done'}>
                    <TimelineRail>
                      <TimelineMarker />
                      {index < command.transitions.length - 1 ? <TimelineConnector /> : null}
                    </TimelineRail>
                    <TimelineContent>
                      <TimelineHeader>
                        <TimelineTitle>{STATUS_PRESENTATION[transition.toStatus].label}</TimelineTitle>
                        <TimelineTime dateTime={transition.occurredAt} className="font-mono">{formatClock(transition.occurredAt, timeZone)}</TimelineTime>
                      </TimelineHeader>
                      <TimelineDescription>
                        {transition.actorType === 'PRINCIPAL' ? '人员操作' : '系统处理'}
                        {transitionReasonNote(transition.reason) ? ` · ${transitionReasonNote(transition.reason)}` : ''}
                      </TimelineDescription>
                    </TimelineContent>
                  </TimelineItem>
                ))}
              </Timeline>
            </section>
          </>
        ) : null}
      </SheetContent>
    </Sheet>
  );
}

/**
 * A high-risk approval needs a two-step sign-in in the last 10 minutes. The sign-in returns to this
 * command, where the operator approves again.
 */
export function StepUpDialog({ command, describe, onClose }: {
  readonly command: Command | null;
  readonly describe: (command: Command) => string;
  readonly onClose: () => void;
}) {
  const verify = () => {
    const returnTo = new URL(window.location.href);
    if (command) returnTo.searchParams.set('inspect', command.commandId);
    window.location.assign(platformGatewayClient.loginUrl({ returnTo: `${returnTo.pathname}${returnTo.search}`, assurance: 'high' }));
  };
  return (
    <Dialog open={command !== null} onOpenChange={(open) => { if (!open) onClose(); }}>
      <DialogContent className="sm:max-w-[440px]">
        <DialogHeader>
          <DialogTitle>批准前要再验证一次身份</DialogTitle>
          <DialogDescription>高风险指令只能在两步验证后 10 分钟内批准。验证完会回到这条指令，再点一次「批准」。</DialogDescription>
        </DialogHeader>
        {command ? (
          <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 rounded-lg border border-divider bg-muted p-3 text-xs">
            <dt className="text-muted-foreground">指令</dt><dd className="font-medium">{describe(command)}</dd>
          </dl>
        ) : null}
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>取消</Button>
          <Button onClick={verify}>去验证身份</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

export type ControlLogFilter = 'approval' | 'inflight' | 'unknown';

const HISTORY_FILTERS: readonly {
  readonly value: ControlLogFilter | 'all';
  readonly label: string;
  readonly matches: (command: Command) => boolean;
}[] = [
  { value: 'all', label: '全部', matches: () => true },
  { value: 'approval', label: '待审批', matches: (command) => command.status === 'AWAITING_APPROVAL' },
  { value: 'inflight', label: '执行中', matches: (command) => IN_FLIGHT.has(command.status) },
  { value: 'unknown', label: '待现场确认', matches: (command) => command.status === 'OUTCOME_UNKNOWN' },
];

/** On-demand site-wide command audit drawer showing recent 50 commands with status filtering. */
export function CommandHistoryDrawer({
  open,
  statusFilter,
  commands,
  isPending,
  errorMessage,
  labels,
  timeZone,
  selectedId,
  actions,
  onFilterChange,
  onRetry,
  onInspect,
  onClose,
}: {
  readonly open: boolean;
  readonly statusFilter: ControlLogFilter | undefined;
  readonly commands: readonly Command[];
  readonly isPending: boolean;
  readonly errorMessage: string | null;
  readonly labels: ReadonlyMap<string, CommandLabel>;
  readonly timeZone: string;
  readonly selectedId: string | undefined;
  readonly actions: CommandActions;
  readonly onFilterChange: (next: ControlLogFilter | undefined) => void;
  readonly onRetry: () => void;
  readonly onInspect: (commandId: string) => void;
  readonly onClose: () => void;
}) {
  const filter = HISTORY_FILTERS.find((candidate) => candidate.value === (statusFilter ?? 'all'))!;
  return (
    <Sheet open={open} onOpenChange={(nextOpen) => { if (!nextOpen) onClose(); }}>
      <SheetContent className="flex flex-col p-0 sm:max-w-[460px]">
        <SheetHeader className="gap-1 border-b border-divider px-5 pt-4 pb-3">
          <SheetTitle className="text-base">指令记录 ({commands.length})</SheetTitle>
          <SheetDescription className="text-xs">近 50 条全站控制指令与审批流转记录</SheetDescription>
        </SheetHeader>

        <ToggleGroup
          type="single"
          value={filter.value}
          onValueChange={(value) => {
            const matched = HISTORY_FILTERS.find((option) => option.value === value);
            if (matched) onFilterChange(matched.value === 'all' ? undefined : matched.value);
          }}
          aria-label="指令状态"
          className="h-10 w-full shrink-0 justify-start gap-5 rounded-none border-b border-divider px-5"
        >
          {HISTORY_FILTERS.map((option) => (
            <ToggleGroupItem
              key={option.value}
              value={option.value}
              className="h-full flex-none gap-1.5 rounded-none border-0 px-0 text-[13px] font-medium text-muted-foreground shadow-[inset_0_-2px_0_transparent] hover:bg-transparent hover:text-foreground data-[state=on]:bg-transparent data-[state=on]:text-foreground data-[state=on]:shadow-[inset_0_-2px_0_var(--primary)]"
            >
              {option.label}
              {!isPending && !errorMessage ? (
                <span className="font-mono text-[11px] text-subtle-foreground">{commands.filter(option.matches).length}</span>
              ) : null}
            </ToggleGroupItem>
          ))}
        </ToggleGroup>

        <div className="min-h-0 flex-1 overflow-y-auto">
          {isPending ? (
            <div className="flex flex-col gap-2 p-4"><Skeleton className="h-14" /><Skeleton className="h-14" /><Skeleton className="h-14" /></div>
          ) : errorMessage ? (
            <div className="flex flex-col items-start gap-2 px-5 py-6">
              <p className="text-sm text-destructive-text">指令记录暂时读不到：{errorMessage}</p>
              <Button variant="outline" size="sm" onClick={onRetry}>重试</Button>
            </div>
          ) : (
            <CommandLog
              commands={commands.filter(filter.matches)}
              labels={labels}
              timeZone={timeZone}
              selectedId={selectedId}
              filtered={filter.value !== 'all'}
              actions={actions}
              onInspect={onInspect}
            />
          )}
        </div>
      </SheetContent>
    </Sheet>
  );
}
