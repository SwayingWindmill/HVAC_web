import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { getRouteApi, Link } from '@tanstack/react-router';
import { Fact } from '@/components/common/Fact';
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
import { STATUS_LABELS } from '../work-order-presentation';
import { workOrderKeys } from '../work-order-queries';
import { PriorityBadge } from './WorkCenterWorkspace';

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
      <SheetContent showOverlay={false} className="w-[480px] overflow-y-auto sm:max-w-[480px]">
        {!workOrder ? (
          <div className="p-4">
            {detail.isError ? <p className="text-sm text-destructive">{workOrderErrorMessage(detail.error)}</p> : <Skeleton className="h-64" />}
          </div>
        ) : (
          <>
            <SheetHeader>
              <div className="flex items-center gap-2">
                <PriorityBadge priority={workOrder.priority} />
                <span className="text-xs text-muted-foreground">{STATUS_LABELS[workOrder.status]}</span>
              </div>
              <SheetTitle className="text-lg">{workOrder.title}</SheetTitle>
              {workOrder.description ? <SheetDescription className="whitespace-pre-wrap">{workOrder.description}</SheetDescription> : null}
            </SheetHeader>

            <div className="space-y-5 px-4 pb-6">
              <dl className="divide-y rounded-lg border px-3">
                <Fact label="负责人">{workOrder.assigneeId ? personLabel(workOrder.assigneeId, myId) : '未指派'}</Fact>
                <Fact label="来源">
                  {alarmSource ? (
                    <Link to="/operations/alarms" search={{ site: site.id, view: 'all', inspect: alarmSource.resourceId }} className="hover:underline">
                      查看来源告警
                    </Link>
                  ) : sourceDevice ? (
                    <Link to="/operations/systems-devices" search={{ site: site.id, inspect: sourceDevice.deviceId }} className="hover:underline">
                      {sourceDevice.name}
                    </Link>
                  ) : '人工创建'}
                </Fact>
                <Fact label="检查项">{workOrder.tasks.total > 0 ? `${workOrder.tasks.completed} / ${workOrder.tasks.total} 已完成` : '无'}</Fact>
                <Fact label="计划开始">{formatTime(workOrder.scheduledStart, site.timezone)}</Fact>
                <Fact label="截止">{formatTime(workOrder.dueAt, site.timezone)}</Fact>
                <Fact label="创建">{formatTime(workOrder.createdAt, site.timezone)}</Fact>
                {workOrder.completionEvidence.map((evidence) => (
                  <Fact key={`${evidence.kind}-${evidence.capturedAt}`} label="处理结果">{evidence.reference}</Fact>
                ))}
              </dl>

              <section className="space-y-2">
                {pendingAction?.note ? (
                  <Textarea rows={3} autoFocus placeholder={pendingAction.note} value={note} onChange={(event) => setNote(event.target.value)} />
                ) : null}
                <div className="flex flex-wrap gap-2">
                  {capabilities.includes('work-order.assign') && workOrder.assigneeId !== myId && (workOrder.status === 'OPEN' || workOrder.status === 'IN_PROGRESS' || workOrder.status === 'BLOCKED') ? (
                    <Button size="sm" variant={workOrder.assigneeId ? 'outline' : 'default'} disabled={claim.isPending} onClick={() => claim.mutate(workOrder)}>
                      由我负责
                    </Button>
                  ) : null}
                  {actions.map((action) => {
                    const blocked = (action.transition === 'start' && !workOrder.assigneeId)
                      || (action.transition === 'complete' && tasksIncomplete)
                      || (pending === action.transition && !note.trim());
                    return (
                      <Button
                        key={action.transition}
                        size="sm"
                        variant={action.primary || pending === action.transition ? 'default' : 'outline'}
                        disabled={transition.isPending || blocked}
                        onClick={() => run(action.transition, Boolean(action.note))}
                      >
                        {pending === action.transition ? `确认${action.label}` : action.label}
                      </Button>
                    );
                  })}
                  {pending ? <Button size="sm" variant="ghost" onClick={() => { setPending(null); setNote(''); }}>取消</Button> : null}
                </div>
                {workOrder.status === 'OPEN' && !workOrder.assigneeId ? <p className="text-xs text-muted-foreground">开始处理前需要先确定负责人。</p> : null}
                {workOrder.status === 'IN_PROGRESS' && tasksIncomplete ? <p className="text-xs text-muted-foreground">检查项全部完成后才能完成工单。</p> : null}
                {error ? <p className="text-sm text-destructive">{workOrderErrorMessage(error)}</p> : null}
              </section>

              <section>
                <h3 className="mb-2 text-sm font-medium">处理记录</h3>
                <ol className="space-y-2 border-l pl-4">
                  {[...workOrder.timeline].reverse().map((entry) => (
                    <li key={entry.version} className="text-sm">
                      <span className="font-medium">{OPERATION_LABELS[entry.operation] ?? entry.operation}</span>
                      <span className="ml-2 text-xs text-muted-foreground">
                        {formatTime(entry.occurredAt, site.timezone)} · {personLabel(entry.actorId, myId)}
                      </span>
                      {entry.reason && !Object.values(FIXED_REASONS).includes(entry.reason) && entry.operation !== 'CREATE' ? (
                        <p className="text-xs text-muted-foreground">{entry.reason}</p>
                      ) : null}
                    </li>
                  ))}
                </ol>
              </section>
            </div>
          </>
        )}
      </SheetContent>
    </Sheet>
  );
}
