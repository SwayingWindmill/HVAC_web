import { useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { BellRing, CheckCheck, CircleCheck, EyeOff, UserCheck, UserMinus, type LucideIcon } from 'lucide-react';
import { getRouteApi, Link } from '@tanstack/react-router';
import { Fact } from '@/components/common/Fact';
import { Button } from '@/components/ui/button';
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '@/components/ui/sheet';
import { Skeleton } from '@/components/ui/skeleton';
import { Textarea } from '@/components/ui/textarea';
import { listWorkOrders, type WorkOrderPriority } from '@/api/work-orders';
import { CreateWorkOrderDialog } from '@/features/work-orders/CreateWorkOrderDialog';
import { STATUS_LABELS } from '@/features/work-orders/work-order-presentation';
import { workOrderKeys } from '@/features/work-orders/work-order-queries';
import { alarmDetailQuery, type AlarmOperation, type AlarmSeverity } from '../alarm-api';
import { useAlarmActions } from '../use-alarm-actions';
import {
  Timeline,
  TimelineConnector,
  TimelineContent,
  TimelineHeader,
  TimelineItem,
  TimelineMarker,
  TimelineRail,
  TimelineTime,
  TimelineTitle,
} from '@/components/ui/timeline';
import {
  alarmStatusLabel,
  formatDuration,
  operationLabel,
  SEVERITY_LABELS,
} from '../alarm-presentation';
import { formatTime, personLabel } from '@/lib/operator-format';
import { SeverityBadge } from '../alarm-presentation';

const siteRoute = getRouteApi('/_app/_site');

const OPERATION_ICONS: Partial<Record<AlarmOperation, LucideIcon>> = {
  PUBLISH: BellRing,
  ACKNOWLEDGE: CheckCheck,
  ASSIGN: UserCheck,
  UNASSIGN: UserMinus,
  SUPPRESS: EyeOff,
  CLEAR: CircleCheck,
};

const PRIORITY_BY_SEVERITY: Readonly<Record<AlarmSeverity, WorkOrderPriority>> = {
  CRITICAL: 'URGENT',
  MAJOR: 'HIGH',
  MINOR: 'MEDIUM',
  WARNING: 'LOW',
  INFO: 'LOW',
};

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

  const detail = useQuery({ ...alarmDetailQuery(site.id, alarmId ?? ''), enabled: Boolean(alarmId) });
  const workOrders = useQuery({
    queryKey: workOrderKeys.source(site.id, 'ALARM', alarmId ?? ''),
    queryFn: ({ signal }) => listWorkOrders({ sourceDomain: 'ALARM', sourceRef: alarmId, limit: 20 }, { siteId: site.id, signal }),
    enabled: Boolean(alarmId) && capabilities.includes('work-order.list'),
  });

  const { acknowledge, claim, canClaim, canCreateWorkOrder } = useAlarmActions();

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
                    <Link to="/operations/systems-devices" search={{ site: site.id, inspect: alarm.deviceId }} className="hover:underline">
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
                <Fact label="负责人">{alarm.assigneeId ? personLabel(alarm.assigneeId, myId) : '未认领'}</Fact>
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
                    <Button size="sm" disabled={acknowledge.isPending} onClick={() => acknowledge.mutate({ alarm, comment }, { onSuccess: () => setComment('') })}>确认告警</Button>
                  ) : null}
                  {canClaim && alarm.assigneeId !== myId ? (
                    <Button size="sm" variant="outline" disabled={claim.isPending} onClick={() => claim.mutate(alarm)}>由我处理</Button>
                  ) : null}
                  {canCreateWorkOrder ? (
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
                            to="/operations/work-center"
                            search={{ site: site.id, view: 'all', inspect: workOrder.workOrderId }}
                            className="truncate hover:underline"
                          >
                            {workOrder.title}
                          </Link>
                          <span className="shrink-0 text-xs text-muted-foreground">{STATUS_LABELS[workOrder.status]}</span>
                        </li>
                      ))}
                    </ul>
                  )}
                </section>
              ) : null}

              <section>
                <h3 className="mb-3 text-sm font-medium">处理记录</h3>
                <Timeline>
                  {[...alarm.timeline].reverse().map((entry, index) => {
                    const Icon = OPERATION_ICONS[entry.operation];
                    return (
                      <TimelineItem key={entry.version} status={index === 0 ? 'current' : 'done'}>
                        <TimelineRail>
                          <TimelineMarker>{Icon ? <Icon /> : null}</TimelineMarker>
                          <TimelineConnector />
                        </TimelineRail>
                        <TimelineContent>
                          <TimelineHeader>
                            <TimelineTitle>{operationLabel(entry.operation)}</TimelineTitle>
                            <TimelineTime dateTime={entry.occurredAt}>{formatTime(entry.occurredAt, site.timezone)}</TimelineTime>
                          </TimelineHeader>
                          <p className="text-xs text-muted-foreground">
                            {entry.actorType === 'WORKLOAD' ? '系统' : personLabel(entry.actorId, myId)} · {SEVERITY_LABELS[entry.currentSeverity]}
                          </p>
                        </TimelineContent>
                      </TimelineItem>
                    );
                  })}
                </Timeline>
              </section>
            </div>

            <CreateWorkOrderDialog
              open={creatingWorkOrder}
              onOpenChange={setCreatingWorkOrder}
              title="由告警创建工单"
              initial={{
                title: deviceName ? `${deviceName}：${alarm.title}` : alarm.title,
                priority: PRIORITY_BY_SEVERITY[alarm.currentSeverity],
                description: alarm.summary,
              }}
              sourceReferences={[{ domain: 'ALARM', resourceId: alarm.alarmId, relationship: 'ORIGIN' }]}
              onCreated={() => void queryClient.invalidateQueries({ queryKey: workOrderKeys.all(site.id) })}
            />
          </>
        )}
      </SheetContent>
    </Sheet>
  );
}
