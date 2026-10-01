import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { getRouteApi, Link } from '@tanstack/react-router';
import { useForm } from 'react-hook-form';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '@/components/ui/sheet';
import { Skeleton } from '@/components/ui/skeleton';
import { Textarea } from '@/components/ui/textarea';
import {
  createWorkOrder,
  listWorkOrders,
  workOrderErrorMessage,
  type WorkOrderPriority,
} from '@/api/work-orders';
import {
  acknowledgeAlarm,
  alarmDetailQuery,
  alarmKeys,
  assignAlarm,
  type Alarm,
  type AlarmSeverity,
} from '../alarm-api';
import {
  alarmStatusLabel,
  formatDuration,
  formatTime,
  operationLabel,
  personLabel,
  SEVERITY_LABELS,
} from '../alarm-presentation';
import { SeverityBadge } from './AlarmsWorkspace';

const siteRoute = getRouteApi('/_app/_site');

const PRIORITY_BY_SEVERITY: Readonly<Record<AlarmSeverity, WorkOrderPriority>> = {
  CRITICAL: 'URGENT',
  MAJOR: 'HIGH',
  MINOR: 'MEDIUM',
  WARNING: 'LOW',
  INFO: 'LOW',
};

const PRIORITY_LABELS: Readonly<Record<WorkOrderPriority, string>> = {
  URGENT: '紧急',
  HIGH: '高',
  MEDIUM: '中',
  LOW: '低',
};

const WORK_ORDER_STATUS_LABELS: Readonly<Record<string, string>> = {
  DRAFT: '草稿',
  OPEN: '待处理',
  IN_PROGRESS: '处理中',
  BLOCKED: '受阻',
  COMPLETED: '已完成',
  CANCELLED: '已取消',
};

function Fact({ label, children }: { readonly label: string; readonly children: React.ReactNode }) {
  return (
    <div className="flex items-baseline justify-between gap-4 py-1.5 text-sm">
      <dt className="shrink-0 text-muted-foreground">{label}</dt>
      <dd className="text-right">{children}</dd>
    </div>
  );
}

interface WorkOrderForm {
  title: string;
  priority: WorkOrderPriority;
  description: string;
}

function CreateWorkOrderDialog({ alarm, deviceName, open, onOpenChange }: {
  readonly alarm: Alarm;
  readonly deviceName: string | undefined;
  readonly open: boolean;
  readonly onOpenChange: (open: boolean) => void;
}) {
  const { site, principal } = siteRoute.useRouteContext();
  const queryClient = useQueryClient();
  const form = useForm<WorkOrderForm>({
    values: {
      title: deviceName ? `${deviceName}：${alarm.title}` : alarm.title,
      priority: PRIORITY_BY_SEVERITY[alarm.currentSeverity],
      description: alarm.summary,
    },
  });
  const create = useMutation({
    mutationFn: (input: WorkOrderForm) => createWorkOrder({
      title: input.title.trim(),
      description: input.description.trim(),
      priority: input.priority,
      sourceReferences: [{ domain: 'ALARM', resourceId: alarm.alarmId, relationship: 'ORIGIN' }],
      assigneeId: null,
      teamId: null,
      scheduledStart: null,
      dueAt: null,
    }, { siteId: site.id, csrfToken: principal.session.csrfToken }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['alarm-work-orders', site.id, alarm.alarmId] });
      onOpenChange(false);
    },
  });

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>由告警创建工单</DialogTitle>
        </DialogHeader>
        <form className="space-y-4" onSubmit={form.handleSubmit((values) => create.mutate(values))}>
          <div className="space-y-1.5">
            <Label htmlFor="work-order-title">标题</Label>
            <Input id="work-order-title" {...form.register('title', { required: true })} />
          </div>
          <div className="space-y-1.5">
            <Label>优先级</Label>
            <Select value={form.watch('priority')} onValueChange={(value) => form.setValue('priority', value as WorkOrderPriority)}>
              <SelectTrigger className="w-32"><SelectValue /></SelectTrigger>
              <SelectContent>
                {(Object.keys(PRIORITY_LABELS) as WorkOrderPriority[]).map((priority) => (
                  <SelectItem key={priority} value={priority}>{PRIORITY_LABELS[priority]}</SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="work-order-description">说明</Label>
            <Textarea id="work-order-description" rows={4} {...form.register('description')} />
          </div>
          {create.isError ? <p className="text-sm text-destructive">{workOrderErrorMessage(create.error)}</p> : null}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>取消</Button>
            <Button type="submit" disabled={create.isPending}>创建工单</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

export function AlarmInspector({ alarmId, deviceNames, onClose }: {
  readonly alarmId: string | undefined;
  readonly deviceNames: ReadonlyMap<string, string>;
  readonly onClose: () => void;
}) {
  const { site, principal } = siteRoute.useRouteContext();
  const queryClient = useQueryClient();
  const [comment, setComment] = useState('');
  const [creatingWorkOrder, setCreatingWorkOrder] = useState(false);
  const myId = principal.principalId;
  const capabilities = principal.authorization.capabilities;
  const csrfToken = principal.session.csrfToken;

  const detail = useQuery({ ...alarmDetailQuery(site.id, alarmId ?? ''), enabled: Boolean(alarmId) });
  const workOrders = useQuery({
    queryKey: ['alarm-work-orders', site.id, alarmId],
    queryFn: ({ signal }) => listWorkOrders({ sourceDomain: 'ALARM', sourceRef: alarmId, limit: 20 }, { siteId: site.id, signal }),
    enabled: Boolean(alarmId) && capabilities.includes('work-order.list'),
  });

  const applyUpdate = (alarm: Alarm) => {
    queryClient.setQueryData(alarmKeys.detail(site.id, alarm.alarmId), alarm);
    void queryClient.invalidateQueries({ queryKey: alarmKeys.all(site.id) });
  };
  const acknowledge = useMutation({
    mutationFn: (alarm: Alarm) => acknowledgeAlarm(alarm.alarmId, comment, csrfToken),
    onSuccess: (alarm) => {
      setComment('');
      applyUpdate(alarm);
    },
  });
  const claim = useMutation({
    mutationFn: (alarm: Alarm) => assignAlarm(alarm, myId, csrfToken),
    onSuccess: applyUpdate,
  });

  const alarm = detail.data;
  const deviceName = alarm?.deviceId ? deviceNames.get(alarm.deviceId) : undefined;
  const actionError = acknowledge.error ?? claim.error;

  return (
    <Sheet modal={false} open={Boolean(alarmId)} onOpenChange={(open) => { if (!open) onClose(); }}>
      <SheetContent showOverlay={false} className="w-[480px] overflow-y-auto sm:max-w-[480px]">
        {!alarm ? (
          <div className="space-y-3 p-4">
            {detail.isError ? <p className="text-sm text-destructive">{detail.error.message}</p> : <Skeleton className="h-64" />}
          </div>
        ) : (
          <>
            <SheetHeader>
              <div className="flex items-center gap-2">
                <SeverityBadge severity={alarm.currentSeverity} />
                <span className="text-xs text-muted-foreground">{alarmStatusLabel(alarm)}</span>
              </div>
              <SheetTitle className="text-lg">{alarm.title}</SheetTitle>
              <SheetDescription>{alarm.summary}</SheetDescription>
            </SheetHeader>

            <div className="space-y-5 px-4 pb-6">
              <dl className="divide-y rounded-lg border px-3">
                <Fact label="设备">
                  {alarm.deviceId ? (
                    <Link to="/sites/$siteId/devices/$deviceId" params={{ siteId: site.id, deviceId: alarm.deviceId }} className="hover:underline">
                      {deviceName ?? '查看设备'}
                    </Link>
                  ) : '站点级'}
                </Fact>
                <Fact label="开始时间">{formatTime(alarm.firstOccurredAt, site.timezone)}</Fact>
                <Fact label={alarm.condition === 'CLEARED' ? '恢复时间' : '最近发生'}>
                  {formatTime(alarm.clearedAt ?? alarm.lastOccurredAt, site.timezone)}
                </Fact>
                <Fact label="持续">{formatDuration(alarm.firstOccurredAt, alarm.clearedAt, Date.now())}</Fact>
                <Fact label="发生次数">{alarm.occurrenceCount}</Fact>
                {alarm.peakSeverity !== alarm.currentSeverity ? <Fact label="最高严重度">{SEVERITY_LABELS[alarm.peakSeverity]}</Fact> : null}
                <Fact label="负责人">{personLabel(alarm.assigneeId, myId)}</Fact>
                <Fact label="确认">
                  {alarm.acknowledgement
                    ? `${personLabel(alarm.acknowledgement.acknowledgedBy, myId)} · ${formatTime(alarm.acknowledgement.acknowledgedAt, site.timezone)}`
                    : '未确认'}
                </Fact>
                {alarm.acknowledgement?.comment ? <Fact label="确认说明">{alarm.acknowledgement.comment}</Fact> : null}
              </dl>

              <section className="space-y-2">
                {!alarm.acknowledgement ? (
                  <div className="space-y-2">
                    <Textarea
                      rows={2}
                      placeholder="确认说明（可选），例如：已到现场查看"
                      value={comment}
                      onChange={(event) => setComment(event.target.value)}
                    />
                  </div>
                ) : null}
                <div className="flex flex-wrap gap-2">
                  {!alarm.acknowledgement ? (
                    <Button size="sm" disabled={acknowledge.isPending} onClick={() => acknowledge.mutate(alarm)}>确认告警</Button>
                  ) : null}
                  {capabilities.includes('alarm.assign') && alarm.assigneeId !== myId ? (
                    <Button size="sm" variant="outline" disabled={claim.isPending} onClick={() => claim.mutate(alarm)}>由我处理</Button>
                  ) : null}
                  {capabilities.includes('work-order.create') ? (
                    <Button size="sm" variant="outline" onClick={() => setCreatingWorkOrder(true)}>创建工单</Button>
                  ) : null}
                </div>
                {actionError ? <p className="text-sm text-destructive">{actionError.message}</p> : null}
                <p className="text-xs text-muted-foreground">告警在工况恢复正常后由系统自动恢复；确认表示已有人跟进，不代表故障已解除。</p>
              </section>

              {workOrders.data ? (
                <section>
                  <h3 className="mb-2 text-sm font-medium">关联工单</h3>
                  {workOrders.data.items.length === 0 ? (
                    <p className="text-sm text-muted-foreground">尚未创建工单</p>
                  ) : (
                    <ul className="divide-y rounded-lg border">
                      {workOrders.data.items.map((workOrder) => (
                        <li key={workOrder.workOrderId} className="flex items-center justify-between gap-3 px-3 py-2 text-sm">
                          <Link
                            to="/sites/$siteId/work-orders/$workOrderId"
                            params={{ siteId: site.id, workOrderId: workOrder.workOrderId }}
                            className="truncate hover:underline"
                          >
                            {workOrder.title}
                          </Link>
                          <span className="shrink-0 text-xs text-muted-foreground">{WORK_ORDER_STATUS_LABELS[workOrder.status]}</span>
                        </li>
                      ))}
                    </ul>
                  )}
                </section>
              ) : null}

              <section>
                <h3 className="mb-2 text-sm font-medium">处理记录</h3>
                <ol className="space-y-2 border-l pl-4">
                  {[...alarm.timeline].reverse().map((entry) => (
                    <li key={entry.version} className="text-sm">
                      <span className="font-medium">{operationLabel(entry.operation)}</span>
                      <span className="ml-2 text-xs text-muted-foreground">
                        {formatTime(entry.occurredAt, site.timezone)} · {entry.actorType === 'WORKLOAD' ? '系统' : personLabel(entry.actorId, myId)}
                      </span>
                    </li>
                  ))}
                </ol>
              </section>
            </div>

            <CreateWorkOrderDialog alarm={alarm} deviceName={deviceName} open={creatingWorkOrder} onOpenChange={setCreatingWorkOrder} />
          </>
        )}
      </SheetContent>
    </Sheet>
  );
}
