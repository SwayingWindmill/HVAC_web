import { useEffect, useState } from 'react';
import { zodResolver } from '@hookform/resolvers/zod';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { CalendarOff, Check, ChevronLeft, ChevronRight, Undo2, X } from 'lucide-react';
import { useForm } from 'react-hook-form';
import { toast } from 'sonner';
import { z } from 'zod';
import type {
  OperatingException,
  OperatingExceptionDecision,
  OperatingExceptionStatus,
} from '@/api/generated/platformGateway.gen';
import { DataTableBlock } from '@/blocks/data-table';
import { Panel } from '@/blocks/panel';
import { StateCard } from '@/blocks/state/state-card';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Field, FieldDescription, FieldError, FieldLabel } from '@/components/ui/field';
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
    marker: '◆',
    className: 'bg-destructive-soft text-destructive-text border-destructive/30',
  },
  CANCELLED: {
    marker: '○',
    className: 'bg-muted text-muted-foreground border-border',
  },
};

const decisionFormSchema = z.object({
  decisionNote: z.string().trim().max(500, '处理备注不能超过 500 字'),
});

type DecisionFormValues = z.infer<typeof decisionFormSchema>;

export interface ScheduleExceptionsTabProps {
  readonly siteId: string;
  readonly timezone: string;
  readonly csrfToken: string;
  readonly currentPrincipalId: string;
  readonly items: readonly OperatingException[];
  readonly isLoading: boolean;
  readonly isError: boolean;
  readonly error: unknown;
  readonly statusFilter: OperatingExceptionStatus | undefined;
  readonly page: number;
  readonly pageSize: number;
  readonly canManageSchedule: boolean;
  readonly onRetry: () => void;
  readonly onStatusFilterChange: (next: OperatingExceptionStatus | undefined) => void;
  readonly onPageChange: (nextPage: number) => void;
}

export function ScheduleExceptionsTab({
  siteId,
  timezone,
  csrfToken,
  currentPrincipalId,
  items,
  isLoading,
  isError,
  error,
  statusFilter,
  page,
  pageSize,
  canManageSchedule,
  onRetry,
  onStatusFilterChange,
  onPageChange,
}: ScheduleExceptionsTabProps) {
  const queryClient = useQueryClient();
  const [pendingDecision, setPendingDecision] = useState<{
    readonly item: OperatingException;
    readonly decision: OperatingExceptionDecision;
  } | null>(null);
  const [decisionError, setDecisionError] = useState<string | null>(null);

  const decisionForm = useForm<DecisionFormValues>({
    resolver: zodResolver(decisionFormSchema),
    mode: 'onBlur',
    defaultValues: { decisionNote: '' },
  });

  useEffect(() => {
    if (pendingDecision) {
      decisionForm.reset({ decisionNote: '' });
      setDecisionError(null);
    }
  }, [pendingDecision, decisionForm]);

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
      setDecisionError(null);
    },
    onError: (err, variables) => {
      const msg = scheduleErrorMessage(err, '处理运行例外失败，请稍后重试', 'exception');
      if (pendingDecision) {
        setDecisionError(msg);
      } else {
        toast.error(msg, {
          duration: Infinity,
          action: {
            label: '重试',
            onClick: () => decideMutation.mutate(variables),
          },
        });
      }
    },
  });

  const totalCount = items.length;
  const totalPages = Math.max(1, Math.ceil(totalCount / pageSize));
  const safePage = Math.min(Math.max(1, page), totalPages);
  const pagedItems = items.slice((safePage - 1) * pageSize, safePage * pageSize);

  return (
    <>
      <DataTableBlock
        title="运行例外"
        description="按站点本地日历日全天（00:00–24:00）覆盖常规周计划；已生效例外日期不可重叠"
        actions={
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
        }
      >
        {isError ? (
          <StateCard
            tone="danger"
            title={<h3 className="text-base font-semibold">无法读取运行例外记录</h3>}
            description={scheduleErrorMessage(error, '请检查网络连接或稍后重试', 'exception')}
          >
            <Button variant="outline" size="sm" onClick={onRetry}>
              重试
            </Button>
          </StateCard>
        ) : (
          <Panel className="overflow-hidden">
            {isLoading ? (
              <div className="px-6 py-10 text-center text-sm text-muted-foreground">
                正在加载运行例外…
              </div>
            ) : totalCount === 0 ? (
              <div className="flex flex-col items-center justify-center gap-2.5 px-6 py-12 text-center">
                <div className="flex size-10 items-center justify-center rounded-full bg-muted text-muted-foreground">
                  <CalendarOff className="size-5" aria-hidden="true" />
                </div>
                <p className="text-sm font-medium text-foreground">
                  {statusFilter
                    ? `筛选条件下没有「${EXCEPTION_STATUS_LABELS[statusFilter]}」状态的运行例外`
                    : '尚未登记任何运行例外'}
                </p>
                <p className="max-w-md text-xs text-muted-foreground">
                  {statusFilter
                    ? '可切换上方状态筛选查看其他记录。'
                    : '遇到法定节假日停机、冷站设备维保停机或租户临时加班供冷时，可申请运行例外以覆盖对应日期的常规周计划。'}
                </p>
              </div>
            ) : (
              <>
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
                    {pagedItems.map((item) => {
                      const isRequester = item.requestedBy === currentPrincipalId;
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
                            {item.kind !== 'OVERTIME_COOLING' ||
                            item.replacementSlots.length === 0 ? (
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
                              <div className="mt-0.5 text-foreground/80">
                                备注：{item.decisionNote}
                              </div>
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
                                    onClick={() => setPendingDecision({ item, decision: 'REJECT' })}
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
                                  onClick={() => setPendingDecision({ item, decision: 'CANCEL' })}
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

                <div className="flex items-center justify-between border-t border-divider px-5 py-2.5 text-xs text-muted-foreground tabular-nums">
                  <span>共 {totalCount} 条</span>
                  <div className="inline-flex items-center gap-2">
                    <span>
                      第 {safePage} / {totalPages} 页
                    </span>
                    <Button
                      type="button"
                      variant="outline"
                      size="icon-sm"
                      aria-label="上一页"
                      disabled={safePage <= 1}
                      onClick={() => onPageChange(safePage - 1)}
                    >
                      <ChevronLeft className="size-4" />
                    </Button>
                    <Button
                      type="button"
                      variant="outline"
                      size="icon-sm"
                      aria-label="下一页"
                      disabled={safePage >= totalPages}
                      onClick={() => onPageChange(safePage + 1)}
                    >
                      <ChevronRight className="size-4" />
                    </Button>
                  </div>
                </div>
              </>
            )}
          </Panel>
        )}
      </DataTableBlock>

      <Dialog
        open={pendingDecision !== null}
        onOpenChange={(open) => {
          if (!open && !decideMutation.isPending) {
            setPendingDecision(null);
            setDecisionError(null);
          }
        }}
      >
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>
              {pendingDecision?.decision === 'REJECT'
                ? '确认驳回该运行例外申请吗？'
                : '确认撤回该运行例外吗？'}
            </DialogTitle>
            <DialogDescription>
              {pendingDecision ? `目标例外：${pendingDecision.item.title}。` : ''}
              {pendingDecision?.decision === 'REJECT'
                ? '驳回后该例外不会覆盖周运行计划，申请人可在此查看处理备注。'
                : '撤回后该日期范围将恢复执行常规周运行计划，并释放日期占用。'}
            </DialogDescription>
          </DialogHeader>

          <form
            className="space-y-4"
            onSubmit={decisionForm.handleSubmit((values) => {
              if (!pendingDecision) return;
              decideMutation.mutate({
                item: pendingDecision.item,
                decision: pendingDecision.decision,
                ...(values.decisionNote ? { note: values.decisionNote } : {}),
              });
            })}
          >
            <Field data-invalid={Boolean(decisionForm.formState.errors.decisionNote)}>
              <FieldLabel htmlFor="decision-note">处理备注（选填）</FieldLabel>
              <Textarea
                id="decision-note"
                rows={3}
                maxLength={500}
                placeholder="例如：租户已改期至下周末 / 维保作业提前结束恢复供冷"
                {...decisionForm.register('decisionNote')}
              />
              {decisionForm.formState.errors.decisionNote ? (
                <FieldError errors={[decisionForm.formState.errors.decisionNote]} />
              ) : (
                <FieldDescription>处理备注将与操作人一起记入运行例外记录</FieldDescription>
              )}
            </Field>

            {decisionError ? (
              <p role="alert" className="text-xs text-destructive">
                {decisionError}
              </p>
            ) : null}

            <DialogFooter>
              <Button
                type="button"
                variant="outline"
                disabled={decideMutation.isPending}
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
                    : '提交'}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </>
  );
}
