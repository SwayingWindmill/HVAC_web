import { useState } from 'react';
import { ChevronRight, UserCheck } from 'lucide-react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { getRouteApi, Link } from '@tanstack/react-router';
import { Button } from '@/components/ui/button';
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '@/components/ui/sheet';
import { Skeleton } from '@/components/ui/skeleton';
import { Textarea } from '@/components/ui/textarea';
import {
  assignWorkOrder,
  getWorkOrder,
  transitionWorkOrder,
  workOrderErrorMessage,
  type WorkOrder,
} from '@/api/work-orders';
import { useSiteEquipment } from '@/features/assets/use-device-names';
import { formatTime, personLabel } from '@/lib/operator-format';
import { cn } from '@/lib/utils';
import { PriorityBadge, STATUS_LABELS } from '../work-order-presentation';
import { workOrderKeys } from '../work-order-queries';
import { StatusShape } from './StatusShape';

const siteRoute = getRouteApi('/_app/_site');

type Transition = 'start' | 'block' | 'resume' | 'complete' | 'cancel' | 'reopen';

// Actions offered per status, following the owner's state machine. Transitions whose
// outcome needs an explanation ask for it; the others record a fixed reason.
const ACTIONS: Readonly<Record<string, readonly { transition: Transition; label: string; note?: string; primary?: boolean }[]>> = {
  OPEN: [
    { transition: 'start', label: '开始处理', primary: true },
    { transition: 'cancel', label: '取消工单', note: '取消原因' },
  ],
  IN_PROGRESS: [
    { transition: 'complete', label: '完成', note: '处理结果，例如：已更换冷冻水泵变频器并验证频率可调', primary: true },
    { transition: 'block', label: '标记受阻', note: '受阻原因，例如：等待备件' },
    { transition: 'cancel', label: '取消工单', note: '取消原因' },
  ],
  BLOCKED: [
    { transition: 'resume', label: '恢复处理', primary: true },
    { transition: 'cancel', label: '取消工单', note: '取消原因' },
  ],
  COMPLETED: [{ transition: 'reopen', label: '重新打开', note: '重新打开的原因' }],
  CANCELLED: [{ transition: 'reopen', label: '重新打开', note: '重新打开的原因' }],
};

const FIXED_REASONS: Partial<Record<Transition, string>> = { start: '开始处理', resume: '恢复处理' };

const OPERATION_LABELS: Readonly<Record<string, string>> = {
  CREATE: '创建',
  OPEN: '开放',
  ASSIGN: '指派',
  UNASSIGN: '取消指派',
  SCHEDULE: '计划',
  START: '开始处理',
  BLOCK: '受阻',
  RESUME: '恢复处理',
  COMPLETE: '完成',
  CANCEL: '取消',
  REOPEN: '重新打开',
};

function InspectorFact({ label, children }: { readonly label: string; readonly children: React.ReactNode }) {
  return (
    <div className="flex min-w-0 flex-col gap-0.5">
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="text-sm font-medium">{children}</dd>
    </div>
  );
}

export function WorkOrderInspector({ workOrderId, onClose }: {
  readonly workOrderId: string | undefined;
  readonly onClose: () => void;
}) {
  const { site, principal } = siteRoute.useRouteContext();
  const queryClient = useQueryClient();
  const [pending, setPending] = useState<Transition | null>(null);
  const [note, setNote] = useState('');
  const equipment = useSiteEquipment(site.id);
  const myId = principal.principalId;
  const capabilities = principal.authorization.capabilities;
  const options = { siteId: site.id, csrfToken: principal.session.csrfToken };

  const detail = useQuery({
    queryKey: ['work-orders', site.id, 'detail', workOrderId],
    queryFn: ({ signal }) => getWorkOrder(workOrderId!, { siteId: site.id, signal }),
    enabled: Boolean(workOrderId),
  });
  const applyUpdate = (workOrder: WorkOrder) => {
    queryClient.setQueryData(['work-orders', site.id, 'detail', workOrder.workOrderId], workOrder);
    void queryClient.invalidateQueries({ queryKey: workOrderKeys.all(site.id) });
    setPending(null);
    setNote('');
  };
  const claim = useMutation({
    mutationFn: (workOrder: WorkOrder) => assignWorkOrder(workOrder.workOrderId, {
      expectedVersion: workOrder.version, assigneeId: myId, teamId: null, reason: '认领处理',
    }, options),
    onSuccess: applyUpdate,
  });
  const transition = useMutation({
    mutationFn: ({ workOrder, action, text }: { workOrder: WorkOrder; action: Transition; text: string }) =>
      transitionWorkOrder(workOrder.workOrderId, action, {
        expectedVersion: workOrder.version,
        reason: FIXED_REASONS[action] ?? text,
        ...(action === 'complete'
          ? { completionEvidence: [{ kind: 'OPERATOR_NOTE', reference: text, capturedAt: new Date().toISOString() }] }
          : {}),
      }, options),
    onSuccess: applyUpdate,
  });

  const workOrder = detail.data;
  const actions = workOrder && capabilities.includes('work-order.lifecycle') ? ACTIONS[workOrder.status] ?? [] : [];
  const pendingAction = actions.find((action) => action.transition === pending);
  const alarmSource = workOrder?.sourceReferences.find((source) => source.domain === 'ALARM');
  const assetSource = workOrder?.sourceReferences.find((source) => source.domain === 'ASSET');
  const sourceDevice = assetSource ? equipment.deviceByAsset.get(assetSource.resourceId) : undefined;
  const error = claim.error ?? transition.error;
  const tasksIncomplete = workOrder ? workOrder.tasks.completed < workOrder.tasks.total : false;
  const step = workOrder ? (workOrder.status === 'OPEN' ? 0 : workOrder.status === 'IN_PROGRESS' || workOrder.status === 'BLOCKED' ? 1 : 2) : 0;

  const run = (action: Transition, needsNote: boolean) => {
    if (!workOrder) return;
    if (needsNote && pending !== action) {
      setPending(action);
      return;
    }
    transition.mutate({ workOrder, action, text: note.trim() });
  };

  return (
    <Sheet modal={false} open={Boolean(workOrderId)} onOpenChange={(open) => { if (!open) onClose(); }}>
      <SheetContent showOverlay={false} className="overflow-y-auto">
        {!workOrder ? (
          <div className="p-5">
            {detail.isError ? <p className="text-sm text-destructive-text">{workOrderErrorMessage(detail.error)}</p> : <Skeleton className="h-64" />}
          </div>
        ) : (
          <>
            <SheetHeader className="gap-1.5 border-b border-divider px-5 pt-4 pr-12 pb-3.5">
              <div className="flex items-center gap-2 text-xs font-medium">
                <PriorityBadge priority={workOrder.priority} />
                <StatusShape status={workOrder.status} />
                {STATUS_LABELS[workOrder.status]}
              </div>
              <SheetTitle className="text-lg leading-[26px] font-semibold tracking-[-0.01em]">{workOrder.title}</SheetTitle>
              {workOrder.description ? <SheetDescription className="text-sm whitespace-pre-wrap text-secondary-foreground">{workOrder.description}</SheetDescription> : null}
            </SheetHeader>

            <section className="flex flex-col gap-3 border-b border-divider px-5 py-4">
              <h3 className="text-sm font-semibold">处理</h3>
              <ol className="flex items-center gap-2.5 rounded-lg border border-divider bg-muted px-3 py-2.5 text-xs" aria-label="工单进度">
                {['待处理', workOrder.status === 'BLOCKED' ? '受阻' : '处理中', workOrder.status === 'CANCELLED' ? '已取消' : '已完成'].map((label, index) => (
                  <li key={label} className={cn('flex items-center gap-1.5', index === step ? 'font-semibold text-foreground' : 'text-muted-foreground')} aria-current={index === step ? 'step' : undefined}>
                    {index > 0 ? <ChevronRight className="size-3 text-subtle-foreground" aria-hidden="true" /> : null}
                    {label}
                  </li>
                ))}
              </ol>
              {pendingAction?.note ? (
                <Textarea rows={3} autoFocus placeholder={pendingAction.note} value={note} onChange={(event) => setNote(event.target.value)} />
              ) : null}
              <div className="flex flex-wrap gap-2">
                {capabilities.includes('work-order.assign') && workOrder.assigneeId !== myId && (workOrder.status === 'OPEN' || workOrder.status === 'IN_PROGRESS' || workOrder.status === 'BLOCKED') ? (
                  <Button variant={workOrder.assigneeId ? 'outline' : 'default'} disabled={claim.isPending} onClick={() => claim.mutate(workOrder)}>
                    <UserCheck data-icon="inline-start" />由我负责
                  </Button>
                ) : null}
                {actions.map((action) => {
                  const blocked = (action.transition === 'start' && !workOrder.assigneeId)
                    || (action.transition === 'complete' && tasksIncomplete)
                    || (pending === action.transition && !note.trim());
                  const destructive = action.transition === 'cancel';
                  return (
                    <Button
                      key={action.transition}
                      variant={action.primary || pending === action.transition ? 'default' : destructive ? 'ghost' : 'outline'}
                      className={cn(destructive && pending !== action.transition && 'text-destructive-text hover:text-destructive-text')}
                      disabled={transition.isPending || blocked}
                      title={action.transition === 'start' && !workOrder.assigneeId ? '开始处理前需要先确定负责人' : undefined}
                      onClick={() => run(action.transition, Boolean(action.note))}
                    >
                      {pending === action.transition ? `确认${action.label}` : action.label}
                    </Button>
                  );
                })}
                {pending ? <Button variant="ghost" onClick={() => { setPending(null); setNote(''); }}>取消</Button> : null}
              </div>
              {workOrder.status === 'OPEN' && !workOrder.assigneeId ? <p className="text-xs text-muted-foreground">需先指定负责人</p> : null}
              {workOrder.status === 'IN_PROGRESS' && tasksIncomplete ? <p className="text-xs text-muted-foreground">检查项全部完成后才能完成</p> : null}
              {error ? <p className="text-sm text-destructive-text">{workOrderErrorMessage(error)}</p> : null}
            </section>

            <section className="flex flex-col gap-3 border-b border-divider px-5 py-4">
              <h3 className="text-sm font-semibold">概况</h3>
              <dl className="grid grid-cols-2 gap-x-5 gap-y-3.5">
                <InspectorFact label="负责人">{workOrder.assigneeId ? personLabel(workOrder.assigneeId, myId) : <span className="text-muted-foreground">未指派</span>}</InspectorFact>
                <InspectorFact label="来源">
                  {alarmSource ? (
                    <Link to="/operations/alarms" search={{ site: site.id, view: 'all', inspect: alarmSource.resourceId }} className="text-primary hover:underline">
                      查看来源告警
                    </Link>
                  ) : sourceDevice ? (
                    <Link to="/operations/systems-devices" search={{ site: site.id, inspect: sourceDevice.deviceId }} className="text-primary hover:underline">
                      {sourceDevice.name}
                    </Link>
                  ) : '人工创建'}
                </InspectorFact>
                <InspectorFact label="计划开始"><span className="font-mono">{formatTime(workOrder.scheduledStart, site.timezone)}</span></InspectorFact>
                <InspectorFact label="截止"><span className="font-mono">{formatTime(workOrder.dueAt, site.timezone)}</span></InspectorFact>
                <InspectorFact label="检查项">{workOrder.tasks.total > 0 ? `${workOrder.tasks.completed} / ${workOrder.tasks.total} 已完成` : '无'}</InspectorFact>
                <InspectorFact label="创建"><span className="font-mono">{formatTime(workOrder.createdAt, site.timezone)}</span></InspectorFact>
                {workOrder.completionEvidence.map((evidence) => (
                  <div key={`${evidence.kind}-${evidence.capturedAt}`} className="col-span-2">
                    <InspectorFact label="处理结果">{evidence.reference}</InspectorFact>
                  </div>
                ))}
              </dl>
            </section>

            <section className="flex flex-col gap-3 px-5 py-4">
              <h3 className="text-sm font-semibold">处理记录</h3>
              <ol className="flex flex-col">
                {[...workOrder.timeline].reverse().map((entry, index, list) => (
                  <li key={entry.version} className="relative grid grid-cols-[16px_minmax(0,1fr)_auto] gap-x-2.5 pb-3">
                    {index < list.length - 1 ? <span className="absolute top-4 bottom-0 left-[7px] w-px bg-divider" aria-hidden="true" /> : null}
                    <span className={cn('mt-0.5 size-[15px] rounded-full border-[1.5px] bg-card', index === 0 ? 'border-primary bg-primary shadow-[inset_0_0_0_3px_var(--card)]' : 'border-input-hover')} aria-hidden="true" />
                    <span className="flex flex-col leading-[18px]">
                      <b className="text-sm font-semibold">{OPERATION_LABELS[entry.operation] ?? entry.operation}</b>
                      <span className="text-xs text-muted-foreground">{personLabel(entry.actorId, myId)}</span>
                      {entry.reason && !Object.values(FIXED_REASONS).includes(entry.reason) && entry.operation !== 'CREATE' ? (
                        <span className="text-xs text-secondary-foreground">{entry.reason}</span>
                      ) : null}
                    </span>
                    <span className="font-mono text-[11px] text-subtle-foreground">{formatTime(entry.occurredAt, site.timezone)}</span>
                  </li>
                ))}
              </ol>
            </section>
          </>
        )}
      </SheetContent>
    </Sheet>
  );
}
