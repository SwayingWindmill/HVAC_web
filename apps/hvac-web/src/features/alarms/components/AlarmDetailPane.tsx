import { useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { getRouteApi, Link } from '@tanstack/react-router';
import { BellRing, CheckCheck, CircleCheck, EyeOff, Link2, Plus, Sparkles, UserCheck, UserMinus, type LucideIcon } from 'lucide-react';
import { InfoTip } from '@/components/info-tip';
import { Button } from '@/components/ui/button';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { Skeleton } from '@/components/ui/skeleton';
import { Textarea } from '@/components/ui/textarea';
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
import { listWorkOrders, type WorkOrderPriority } from '@/api/work-orders';
import { useAssistantPanel } from '@/features/assistant/assistant-panel-store';
import { CreateWorkOrderDialog } from '@/features/work-orders/CreateWorkOrderDialog';
import { STATUS_LABELS } from '@/features/work-orders/work-order-presentation';
import { workOrderKeys } from '@/features/work-orders/work-order-queries';
import { formatTime, personLabel } from '@/lib/operator-format';
import { cn } from '@/lib/utils';
import { alarmDetailQuery, type AlarmOperation, type AlarmSeverity } from '../alarm-api';
import { alarmStatusLabel, formatDuration, operationLabel, recoveryReason, SEVERITY_LABELS, SeverityBadge } from '../alarm-presentation';
import { useAlarmActions } from '../use-alarm-actions';

const siteRoute = getRouteApi('/_app/_site');

const OPERATION_ICONS: Partial<Record<AlarmOperation, LucideIcon>> = {
  PUBLISH: BellRing,
  ACKNOWLEDGE: CheckCheck,
  ASSIGN: UserCheck,
  UNASSIGN: UserMinus,
  SUPPRESS: EyeOff,
  CLEAR: CircleCheck,
};

export const PRIORITY_BY_SEVERITY: Readonly<Record<AlarmSeverity, WorkOrderPriority>> = {
  CRITICAL: 'URGENT',
  MAJOR: 'HIGH',
  MINOR: 'MEDIUM',
  WARNING: 'LOW',
  INFO: 'LOW',
};

function Fact({ label, children }: { readonly label: string; readonly children: React.ReactNode }) {
  return (
    <div className="flex min-w-0 flex-col gap-0.5">
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="truncate text-sm font-medium">{children}</dd>
    </div>
  );
}

/** What 解释 asks the assistant: the alarm by its title, when it started in Site time, whether it has cleared, and its device. */
function explainAlarmQuestion(title: string, startedAt: string, cleared: boolean, deviceName: string | undefined, timeZone: string): string {
  const parts = Object.fromEntries(new Intl.DateTimeFormat('en-CA', {
    timeZone, year: 'numeric', month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit', hourCycle: 'h23',
  }).formatToParts(new Date(startedAt)).map((part) => [part.type, part.value]));
  const when = `${parts.year}年${Number(parts.month)}月${Number(parts.day)}日 ${parts.hour}:${parts.minute}`;
  return `${when} 开始${cleared ? '、已恢复' : ''}的告警「${title}」${deviceName ? `（${deviceName}）` : ''}是怎么回事？`;
}

/**
 * The selected alarm beside the list: what it is and its state, what the operator can do now,
 * its facts, its history and the work raised from it.
 */
export function AlarmDetailPane({ alarmId, empty, deviceNames, creatingWorkOrder, onCreatingWorkOrderChange }: {
  readonly alarmId: string | undefined;
  /** The list has nothing to select. */
  readonly empty: boolean;
  readonly deviceNames: ReadonlyMap<string, string>;
  readonly creatingWorkOrder: boolean;
  readonly onCreatingWorkOrderChange: (open: boolean) => void;
}) {
  const { site, principal } = siteRoute.useRouteContext();
  const queryClient = useQueryClient();
  const [comment, setComment] = useState('');
  const [ackOpen, setAckOpen] = useState(false);
  const [copied, setCopied] = useState(false);
  const myId = principal.principalId;
  const capabilities = principal.authorization.capabilities;

  const detail = useQuery({ ...alarmDetailQuery(site.id, alarmId ?? ''), enabled: Boolean(alarmId) });
  const workOrders = useQuery({
    queryKey: workOrderKeys.source(site.id, 'ALARM', alarmId ?? ''),
    queryFn: ({ signal }) => listWorkOrders({ sourceDomain: 'ALARM', sourceRef: alarmId, limit: 20 }, { siteId: site.id, signal }),
    enabled: Boolean(alarmId) && capabilities.includes('work-order.list'),
  });
  const { acknowledge, claim, canClaim, canCreateWorkOrder } = useAlarmActions();
  const explain = useAssistantPanel((state) => state.explain);

  if (!alarmId) {
    return (
      <div className="flex flex-1 items-center justify-center p-10 text-sm text-muted-foreground">{empty ? '没有可查看的告警' : '从左侧选择一条告警'}</div>
    );
  }
  const alarm = detail.data;
  if (!alarm) {
    return (
      <div className="flex flex-col gap-3 p-6" aria-busy={!detail.isError}>
        {detail.isError ? <p className="text-sm text-destructive-text">告警详情暂时读不到：{detail.error.message}</p> : <><Skeleton className="h-6 w-48" /><Skeleton className="h-8 w-80" /><Skeleton className="h-40" /></>}
      </div>
    );
  }

  const deviceName = alarm.deviceId ? deviceNames.get(alarm.deviceId) : undefined;
  const actionError = acknowledge.error ?? claim.error;
  const conditionTone = alarm.condition === 'CLEARED' ? 'bg-success' : alarm.acknowledgement ? 'bg-warning' : 'bg-destructive';

  return (
    <section role="region" aria-label="告警详情" className="flex min-w-0 flex-1 flex-col overflow-y-auto">
      <header className="flex flex-col gap-2 border-b border-divider px-6 pt-[18px] pb-4">
        <div className="flex items-center gap-2.5 text-xs">
          <SeverityBadge severity={alarm.currentSeverity} />
          <span className="flex items-center gap-1.5 font-medium">
            <span className={cn('size-2 rounded-full', conditionTone)} aria-hidden="true" />
            {alarmStatusLabel(alarm)}
          </span>
          <span className="text-muted-foreground">· <span className="font-mono">{formatTime(alarm.firstOccurredAt, site.timezone)}</span> 起</span>
          <Button
            variant="ghost"
            size="icon-sm"
            className="ml-auto text-muted-foreground"
            aria-label={copied ? '已复制链接' : '复制链接'}
            onClick={() => {
              void navigator.clipboard?.writeText(window.location.href).then(() => setCopied(true));
              window.setTimeout(() => setCopied(false), 1500);
            }}
          >
            <Link2 className="size-3.5" />
          </Button>
        </div>
        <h2 className="text-xl leading-7 font-semibold tracking-[-0.015em]">{alarm.title}</h2>
        <p className="text-sm leading-[22px] text-secondary-foreground">{alarm.summary}</p>
        <div className="mt-1 flex flex-wrap items-center gap-2">
          {!alarm.acknowledgement ? (
            <Popover open={ackOpen} onOpenChange={setAckOpen}>
              <PopoverTrigger asChild>
                <Button disabled={acknowledge.isPending}>确认告警</Button>
              </PopoverTrigger>
              <PopoverContent align="start" className="flex w-80 flex-col gap-3">
                <p className="text-sm font-semibold">确认这条告警？</p>
                <Textarea
                  rows={2}
                  aria-label="确认说明"
                  placeholder="确认说明（可选），例如：已到现场查看"
                  value={comment}
                  onChange={(event) => setComment(event.target.value)}
                />
                <div className="flex justify-end gap-2">
                  <Button variant="ghost" size="sm" onClick={() => setAckOpen(false)}>取消</Button>
                  <Button
                    size="sm"
                    disabled={acknowledge.isPending}
                    onClick={() => acknowledge.mutate({ alarm, comment }, { onSuccess: () => { setComment(''); setAckOpen(false); } })}
                  >
                    {acknowledge.isPending ? '正在确认…' : '确认'}
                  </Button>
                </div>
              </PopoverContent>
            </Popover>
          ) : null}
          {!alarm.acknowledgement ? <InfoTip>确认只表示已有人跟进，不代表故障已解除；运行恢复正常后由系统自动恢复</InfoTip> : null}
          {canClaim && alarm.assigneeId !== myId ? (
            <Button variant="outline" disabled={claim.isPending} onClick={() => claim.mutate(alarm)}><UserCheck data-icon="inline-start" />由我处理</Button>
          ) : null}
          {canCreateWorkOrder ? (
            <Button variant="outline" onClick={() => onCreatingWorkOrderChange(true)}><Plus data-icon="inline-start" />创建工单</Button>
          ) : null}
          <Button variant="outline" onClick={() => explain(explainAlarmQuestion(alarm.title, alarm.firstOccurredAt, alarm.condition === 'CLEARED', deviceName, site.timezone))}>
            <Sparkles data-icon="inline-start" className="text-primary" />解释
          </Button>
        </div>
        {actionError ? <p className="text-sm text-destructive-text">{actionError.message}</p> : null}
      </header>

      <div className="flex flex-col gap-3 border-b border-divider px-6 py-4">
        <h3 className="text-sm font-semibold">概况</h3>
        <dl className="grid grid-cols-4 gap-x-5 gap-y-3.5">
          <Fact label="设备">
            {alarm.deviceId ? (
              <Link to="/operations/systems-devices" search={{ site: site.id, inspect: alarm.deviceId }} className="text-primary hover:underline">
                {deviceName ?? '查看设备'} ›
              </Link>
            ) : '站点级'}
          </Fact>
          <Fact label="负责人">{alarm.assigneeId ? personLabel(alarm.assigneeId, myId) : <span className="text-muted-foreground">未分配</span>}</Fact>
          <Fact label="持续">{formatDuration(alarm.firstOccurredAt, alarm.clearedAt, Date.now())}</Fact>
          <Fact label="发生次数">{alarm.occurrenceCount}</Fact>
          {alarm.condition === 'CLEARED' && alarm.clearedAt ? <Fact label="恢复时间"><span className="font-mono">{formatTime(alarm.clearedAt, site.timezone)}</span></Fact> : null}
          {alarm.lastOccurredAt !== alarm.firstOccurredAt ? <Fact label="最近发生"><span className="font-mono">{formatTime(alarm.lastOccurredAt, site.timezone)}</span></Fact> : null}
          {alarm.peakSeverity !== alarm.currentSeverity ? <Fact label="最高严重度">{SEVERITY_LABELS[alarm.peakSeverity]}</Fact> : null}
          {alarm.acknowledgement ? (
            <Fact label="确认">{personLabel(alarm.acknowledgement.acknowledgedBy, myId)} · <span className="font-mono">{formatTime(alarm.acknowledgement.acknowledgedAt, site.timezone)}</span></Fact>
          ) : null}
        </dl>
        {alarm.acknowledgement?.comment ? (
          <p className="rounded-lg bg-muted px-3 py-2 text-sm text-secondary-foreground">“{alarm.acknowledgement.comment}”</p>
        ) : null}
      </div>

      <div className="grid grid-cols-2 gap-8 px-6 py-4">
        <section className="flex flex-col gap-3">
          <h3 className="text-sm font-semibold">处理记录</h3>
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
                      <TimelineTime dateTime={entry.occurredAt} className="font-mono">{formatTime(entry.occurredAt, site.timezone)}</TimelineTime>
                    </TimelineHeader>
                    <p className="text-xs text-muted-foreground">
                      {entry.actorType === 'WORKLOAD' ? '系统' : personLabel(entry.actorId, myId)}
                      {recoveryReason(entry) ? ` · ${recoveryReason(entry)}` : ''} · {SEVERITY_LABELS[entry.currentSeverity]}
                    </p>
                  </TimelineContent>
                </TimelineItem>
              );
            })}
          </Timeline>
        </section>
        {workOrders.data ? (
          <section className="flex flex-col gap-3">
            <h3 className="text-sm font-semibold">关联工单</h3>
            {workOrders.data.items.length === 0 ? (
              <div className="flex flex-col items-start gap-2">
                <p className="text-xs text-muted-foreground">尚未创建工单</p>
                {canCreateWorkOrder ? (
                  <Button variant="outline" size="sm" onClick={() => onCreatingWorkOrderChange(true)}><Plus data-icon="inline-start" />由此告警创建工单</Button>
                ) : null}
              </div>
            ) : (
              <ul className="flex flex-col gap-2">
                {workOrders.data.items.map((workOrder) => (
                  <li key={workOrder.workOrderId}>
                    <Link
                      to="/operations/work-center"
                      search={{ site: site.id, view: 'all', inspect: workOrder.workOrderId }}
                      className="flex items-center gap-3 rounded-lg border px-3 py-2.5 text-sm outline-none hover:bg-accent focus-visible:ring-3 focus-visible:ring-ring-soft"
                    >
                      <span className="min-w-0 flex-1 truncate font-medium">{workOrder.title}</span>
                      <span className="shrink-0 text-xs text-muted-foreground">{STATUS_LABELS[workOrder.status]} ›</span>
                    </Link>
                  </li>
                ))}
              </ul>
            )}
          </section>
        ) : null}
      </div>

      <CreateWorkOrderDialog
        open={creatingWorkOrder}
        onOpenChange={onCreatingWorkOrderChange}
        title="由告警创建工单"
        initial={{
          title: deviceName ? `${deviceName}：${alarm.title}` : alarm.title,
          priority: PRIORITY_BY_SEVERITY[alarm.currentSeverity],
          description: alarm.summary,
        }}
        sourceReferences={[{ domain: 'ALARM', resourceId: alarm.alarmId, relationship: 'ORIGIN' }]}
        onCreated={() => void queryClient.invalidateQueries({ queryKey: workOrderKeys.all(site.id) })}
      />
    </section>
  );
}
