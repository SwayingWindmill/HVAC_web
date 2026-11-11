import { useState } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { CalendarOff, Check, Undo2, X } from 'lucide-react';
import { toast } from 'sonner';
import type {
  OperatingException,
  OperatingExceptionDecision,
  OperatingExceptionStatus,
} from '@/api/generated/platformGateway.gen';
import { Panel, PanelHeader } from '@/blocks/panel';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Label } from '@/components/ui/label';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { Textarea } from '@/components/ui/textarea';
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group';
import { formatTime } from '@/lib/operator-format';
import { cn } from '@/lib/utils';
import { decideOperatingException, scheduleErrorMessage, scheduleKeys } from '../query';
import {
  createClientUuidV7,
  EXCEPTION_DECISION_LABELS,
  EXCEPTION_KIND_LABELS,
  EXCEPTION_STATUS_LABELS,
  formatMinuteOfDay,
} from '../schedule-model';

const STATUS_FILTERS: readonly { readonly value: 'ALL' | OperatingExceptionStatus; readonly label: string }[] = [
  { value: 'ALL', label: '全部' },
  { value: 'PENDING_APPROVAL', label: '待审批' },
  { value: 'APPROVED', label: '已生效' },
  { value: 'REJECTED', label: '已驳回' },
  { value: 'CANCELLED', label: '已撤回' },
];

const STATUS_BADGE: Readonly<
  Record<OperatingExceptionStatus, { readonly marker: string; readonly className: string }>
> = {
  PENDING_APPROVAL: {
    marker: '▲',
    className: 'bg-warning-soft text-warning-text border-warning/30',
  },
  APPROVED: {
    marker: '●',
    className: 'bg-success-soft text-success-text border-success/30',
  },
  REJECTED: {
    marker: '×',
    className: 'bg-destructive-soft text-destructive-text border-destructive/30',
  },
  CANCELLED: {
    marker: '—',
    className: 'bg-muted text-muted-foreground border-border',
  },
};

export interface ScheduleExceptionsTabProps {
  readonly siteId: string;
  readonly timezone: string;
  readonly csrfToken: string;
  readonly currentSubject: string;
  readonly items: readonly OperatingException[];
  readonly isLoading: boolean;
  readonly statusFilter: OperatingExceptionStatus | undefined;
  readonly canManageSchedule: boolean;
  readonly onStatusFilterChange: (next: OperatingExceptionStatus | undefined) => void;
}

export function ScheduleExceptionsTab({
  siteId,
  timezone,
  csrfToken,
  currentSubject,
  items,
  isLoading,
  statusFilter,
  canManageSchedule,
  onStatusFilterChange,
}: ScheduleExceptionsTabProps) {
  const queryClient = useQueryClient();
  const [pendingDecision, setPendingDecision] = useState<{
    readonly item: OperatingException;
    readonly decision: OperatingExceptionDecision;
  } | null>(null);
  const [decisionNote, setDecisionNote] = useState('');
  const [decisionError, setDecisionError] = useState<string | null>(null);

  const decideMutation = useMutation({
    mutationFn: (payload: {
      readonly item: OperatingException;
      readonly decision: OperatingExceptionDecision;
      readonly note?: string;
    }) =>
      decideOperatingException(
        siteId,
        payload.item.id,
        {
          decision: payload.decision,
          ...(payload.note ? { decisionNote: payload.note } : {}),
          meta: {
            expectedRevision: payload.item.revision,
            idempotencyKey: `exc-decide-${createClientUuidV7()}`,
            reason: payload.note || EXCEPTION_DECISION_LABELS[payload.decision],
          },
        },
        csrfToken,
      ),
    onSuccess: (_updated, variables) => {
      void queryClient.invalidateQueries({ queryKey: scheduleKeys.exceptionsRoot(siteId) });
      toast.success(
        variables.decision === 'APPROVE'
          ? '已审批通过，运行例外现已生效'
          : variables.decision === 'REJECT'
            ? '已驳回该运行例外申请'
            : '已撤回该运行例外',
      );
      setPendingDecision(null);
      setDecisionNote('');
      setDecisionError(null);
    },
    onError: (error) => {
      const msg = scheduleErrorMessage(error, '处理运行例外失败');
      setDecisionError(msg);
      toast.error(msg);
    },
  });

  const openDecisionModal = (item: OperatingException, decision: OperatingExceptionDecision) => {
    setPendingDecision({ item, decision });
    setDecisionNote('');
    setDecisionError(null);
  };

  return (
    <>
      <Panel>
        <PanelHeader
          title="运行例外"
          caption="按站点本地日历日全天（00:00–24:00）覆盖常规周计划；已生效例外日期不可重叠"
          action={
            <div className="flex flex-wrap items-center gap-2">
              <ToggleGroup
                type="single"
                size="sm"
                variant="outline"
                value={statusFilter ?? 'ALL'}
                onValueChange={(value) => {
                  if (!value) return;
                  onStatusFilterChange(
                    value === 'ALL' ? undefined : (value as OperatingExceptionStatus),
                  );
                }}
                aria-label="运行例外状态筛选"
              >
                {STATUS_FILTERS.map((filter) => (
                  <ToggleGroupItem key={filter.value} value={filter.value} className="text-xs">
                    {filter.label}
                  </ToggleGroupItem>
                ))}
              </ToggleGroup>
            </div>
          }
        />

        <div className="border-t border-divider">
          {isLoading ? (
            <div className="px-6 py-10 text-center text-sm text-muted-foreground">
              正在加载运行例外…
            </div>
          ) : items.length === 0 ? (
            <div className="flex flex-col items-center justify-center gap-2.5 px-6 py-12 text-center">
              <div className="flex size-10 items-center justify-center rounded-full bg-muted text-muted-foreground">
                <CalendarOff className="size-5" aria-hidden="true" />
              </div>
              <p className="text-sm font-medium text-foreground">
                {statusFilter
                  ? `当前没有「${EXCEPTION_STATUS_LABELS[statusFilter]}」状态的运行例外`
                  : '尚未登记任何运行例外'}
              </p>
              <p className="max-w-md text-xs text-muted-foreground">
                遇到法定节假日停机、冷站设备维保停机或租户临时加班供冷时，可在此申请运行例外以覆盖对应日期的常规周计划。
              </p>
            </div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className="pl-5">例外名称与原因</TableHead>
                  <TableHead>类型</TableHead>
                  <TableHead>覆盖日期（全天）</TableHead>
                  <TableHead>当日供冷安排</TableHead>
                  <TableHead>状态</TableHead>
                  <TableHead>申请与审批记录</TableHead>
                  <TableHead className="pr-5 text-right">操作</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {items.map((item) => {
                  const isRequester =
                    item.requestedBy === currentSubject ||
                    item.requestedBy.endsWith(`|${currentSubject}`);
                  const canApproveOrReject =
                    item.status === 'PENDING_APPROVAL' && canManageSchedule;
                  const canCancel =
                    (item.status === 'PENDING_APPROVAL' &&
                      (canManageSchedule || isRequester)) ||
                    (item.status === 'APPROVED' && canManageSchedule);
                  const badge = STATUS_BADGE[item.status];

                  return (
                    <TableRow key={item.id}>
                      <TableCell className="max-w-64 pl-5 align-top">
                        <div className="font-medium text-foreground">{item.title}</div>
                        <div className="mt-0.5 line-clamp-2 text-xs text-muted-foreground">
                          {item.reason}
                        </div>
                      </TableCell>

                      <TableCell className="align-top whitespace-nowrap">
                        <span className="rounded-sm border border-border bg-muted/40 px-2 py-0.5 text-xs font-medium text-foreground">
                          {EXCEPTION_KIND_LABELS[item.kind]}
                        </span>
                      </TableCell>

                      <TableCell className="align-top whitespace-nowrap tabular-nums">
                        {item.startDate === item.endDate
                          ? item.startDate
                          : `${item.startDate} 至 ${item.endDate}`}
                      </TableCell>

                      <TableCell className="align-top text-xs tabular-nums">
                        {item.kind !== 'OVERTIME_COOLING' || item.replacementSlots.length === 0 ? (
                          <span className="text-muted-foreground">全天停机</span>
                        ) : (
                          <div className="space-y-0.5">
                            {item.replacementSlots.map((slot, idx) => (
                              <div key={idx}>
                                {formatMinuteOfDay(slot.startMinute)}–
                                {formatMinuteOfDay(slot.endMinute)}
                                {slot.targetSupplyTempC !== undefined
                                  ? `（参考 ${slot.targetSupplyTempC.toFixed(1)} °C）`
                                  : ''}
                              </div>
                            ))}
                          </div>
                        )}
                      </TableCell>

                      <TableCell className="align-top whitespace-nowrap">
                        <span
                          className={cn(
                            'inline-flex items-center gap-1 rounded-sm border px-2 py-0.5 text-xs font-medium',
                            badge.className,
                          )}
                        >
                          <span aria-hidden="true">{badge.marker}</span>
                          {EXCEPTION_STATUS_LABELS[item.status]}
                        </span>
                      </TableCell>

                      <TableCell className="max-w-60 align-top text-xs text-muted-foreground">
                        <div>
                          申请：{item.requestedByDisplay} · {formatTime(item.createdAt, timezone)}
                        </div>
                        {item.decidedByDisplay ? (
                          <div className="mt-0.5">
                            处理：{item.decidedByDisplay}
                            {item.decidedAt ? ` · ${formatTime(item.decidedAt, timezone)}` : ''}
                          </div>
                        ) : null}
                        {item.decisionNote ? (
                          <div className="mt-0.5 text-foreground/80">备注：{item.decisionNote}</div>
                        ) : null}
                      </TableCell>

                      <TableCell className="pr-5 text-right align-top whitespace-nowrap">
                        <div className="inline-flex items-center justify-end gap-1.5">
                          {canApproveOrReject ? (
                            <>
                              <Button
                                size="sm"
                                variant="default"
                                disabled={decideMutation.isPending}
                                onClick={() =>
                                  decideMutation.mutate({
                                    item,
                                    decision: 'APPROVE',
                                  })
                                }
                              >
                                <Check data-icon="inline-start" />
                                审批通过
                              </Button>
                              <Button
                                size="sm"
                                variant="outline"
                                disabled={decideMutation.isPending}
                                onClick={() => openDecisionModal(item, 'REJECT')}
                              >
                                <X data-icon="inline-start" />
                                驳回
                              </Button>
                            </>
                          ) : null}
                          {canCancel ? (
                            <Button
                              size="sm"
                              variant="ghost"
                              disabled={decideMutation.isPending}
                              onClick={() => openDecisionModal(item, 'CANCEL')}
                            >
                              <Undo2 data-icon="inline-start" />
                              撤回
                            </Button>
                          ) : null}
                        </div>
                      </TableCell>
                    </TableRow>
                  );
                })}
              </TableBody>
            </Table>
          )}
        </div>
      </Panel>

      <Dialog
        open={pendingDecision !== null}
        onOpenChange={(open) => {
          if (!open) {
            setPendingDecision(null);
            setDecisionError(null);
          }
        }}
      >
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>
              {pendingDecision
                ? `${EXCEPTION_DECISION_LABELS[pendingDecision.decision]} · ${pendingDecision.item.title}`
                : ''}
            </DialogTitle>
            <DialogDescription>
              {pendingDecision?.decision === 'REJECT'
                ? '驳回后该例外不会覆盖周运行计划，申请人可在此查看处理备注。'
                : '撤回后该日期范围将恢复执行常规周运行计划，并释放日期占用。'}
            </DialogDescription>
          </DialogHeader>

          <form
            className="space-y-4"
            onSubmit={(event) => {
              event.preventDefault();
              if (!pendingDecision) return;
              decideMutation.mutate({
                item: pendingDecision.item,
                decision: pendingDecision.decision,
                ...(decisionNote.trim() ? { note: decisionNote.trim() } : {}),
              });
            }}
          >
            <div className="space-y-1.5">
              <Label htmlFor="decision-note">处理备注（选填）</Label>
              <Textarea
                id="decision-note"
                rows={3}
                maxLength={500}
                value={decisionNote}
                onChange={(event) => setDecisionNote(event.target.value)}
                placeholder="例如：租户已改期至下周末 / 维保作业提前结束恢复供冷"
              />
            </div>

            {decisionError ? <p className="text-sm text-destructive">{decisionError}</p> : null}

            <DialogFooter>
              <Button
                type="button"
                variant="outline"
                onClick={() => {
                  setPendingDecision(null);
                  setDecisionError(null);
                }}
              >
                取消
              </Button>
              <Button
                type="submit"
                variant={pendingDecision?.decision === 'REJECT' ? 'destructive' : 'default'}
                disabled={decideMutation.isPending}
              >
                {decideMutation.isPending
                  ? '正在处理…'
                  : pendingDecision
                    ? EXCEPTION_DECISION_LABELS[pendingDecision.decision]
                    : '确认'}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </>
  );
}
