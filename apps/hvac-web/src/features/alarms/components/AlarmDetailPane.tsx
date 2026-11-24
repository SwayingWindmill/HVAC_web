import { useEffect, useId, useMemo, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { getRouteApi, Link } from '@tanstack/react-router';
import { Bell, BellOff, BellRing, CheckCheck, CircleCheck, Cpu, EyeOff, LineChart, Link2, Plus, UserCheck, UserMinus, WifiOff, type LucideIcon } from 'lucide-react';
import { BrandMark } from '@/components/brand-mark';
import { InfoTip } from '@/components/info-tip';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Label } from '@/components/ui/label';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group';
import { Skeleton } from '@/components/ui/skeleton';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
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
import { siteAssetModelQuery } from '@/api/site-asset-model';
import { useSiteEquipment } from '@/features/assets/use-device-names';
import { buildTrendCatalog } from '@/features/operations/trends/trends-model';
import { buildAlarmTrendSearch } from '@/features/operations/trends/trends-overlays';
import { CreateWorkOrderDialog } from '@/features/work-orders/CreateWorkOrderDialog';
import { STATUS_LABELS } from '@/features/work-orders/work-order-presentation';
import { workOrderKeys } from '@/features/work-orders/work-order-queries';
import { formatTime, personLabel } from '@/lib/operator-format';
import { cn } from '@/lib/utils';
import { alarmDetailQuery, alarmEvaluationsQuery, type AlarmOperation, type AlarmSeverity } from '../alarm-api';
import {
  edgeSourceLabel,
  formatEdgeActorName,
  formatEdgeBundleTag,
  formatEdgeGatewayName,
  formatEdgeTimelineNote,
  formatEdgeTriggeredActionsSummary,
} from '../alarm-operations';
import {
  alarmStatusLabel,
  EVALUATION_EFFECT_LABELS,
  EVALUATION_STATUS_LABELS,
  formatDuration,
  formatEvaluationSnapshot,
  formatQualityBlocker,
  operationLabel,
  recoveryReason,
  SEVERITY_LABELS,
  SeverityBadge,
} from '../alarm-presentation';
import { useAlarmActions } from '../use-alarm-actions';

const siteRoute = getRouteApi('/_app/_site');

const SUPPRESS_PRESETS = [
  { hours: '1', label: '1 小时' },
  { hours: '4', label: '4 小时' },
  { hours: '24', label: '24 小时' },
] as const;

const OPERATION_ICONS: Partial<Record<AlarmOperation, LucideIcon>> = {
  PUBLISH: BellRing,
  ACKNOWLEDGE: CheckCheck,
  ASSIGN: UserCheck,
  UNASSIGN: UserMinus,
  SUPPRESS: EyeOff,
  UNSUPPRESS: Bell,
  CLEAR: CircleCheck,
};

export const PRIORITY_BY_SEVERITY: Readonly<Record<AlarmSeverity, WorkOrderPriority>> = {
  CRITICAL: 'URGENT',
  MAJOR: 'HIGH',
  MINOR: 'MEDIUM',
  WARNING: 'LOW',
  INFO: 'LOW',
};

function Fact({ label, title, span, children }: {
  readonly label: string;
  readonly title?: string;
  readonly span?: 1 | 2;
  readonly children: React.ReactNode;
}) {
  return (
    <div className={cn('flex min-w-0 flex-col gap-0.5', span === 2 && 'col-span-2')}>
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="truncate text-sm font-medium" title={title}>{children}</dd>
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
  const [prevAlarmId, setPrevAlarmId] = useState(alarmId);
  const [comment, setComment] = useState('');
  const [ackOpen, setAckOpen] = useState(false);
  const [suppressOpen, setSuppressOpen] = useState(false);
  const [suppressHours, setSuppressHours] = useState<string>('4');
  const [suppressReason, setSuppressReason] = useState('');
  const [copied, setCopied] = useState(false);
  if (alarmId !== prevAlarmId) {
    setPrevAlarmId(alarmId);
    setComment('');
    setAckOpen(false);
    setSuppressOpen(false);
    setSuppressHours('4');
    setSuppressReason('');
    setCopied(false);
  }
  const suppressionNoteId = useId();
  const suppressPresetPrefix = useId();
  const suppressReasonId = useId();
  const evalRegionId = useId();
  const myId = principal.principalId;
  const capabilities = principal.authorization.capabilities;

  const detail = useQuery({ ...alarmDetailQuery(site.id, alarmId ?? ''), enabled: Boolean(alarmId) });
  const evaluations = useQuery({ ...alarmEvaluationsQuery(site.id, alarmId ?? '', 10), enabled: Boolean(alarmId) });
  const canReadTrends = capabilities.includes('telemetry.history.read');
  const assetModel = useQuery({ ...siteAssetModelQuery(site.id), enabled: canReadTrends });
  const trendCatalog = useMemo(
    () => (assetModel.data ? buildTrendCatalog(assetModel.data) : null),
    [assetModel.data],
  );
  const workOrders = useQuery({
    queryKey: workOrderKeys.source(site.id, 'ALARM', alarmId ?? ''),
    queryFn: ({ signal }) => listWorkOrders({ sourceDomain: 'ALARM', sourceRef: alarmId, limit: 20 }, { siteId: site.id, signal }),
    enabled: Boolean(alarmId) && capabilities.includes('work-order.list'),
  });
  const { acknowledge, claim, unassign, suppress, unsuppress, resetErrors, canClaim, canCreateWorkOrder } = useAlarmActions();
  const equipment = useSiteEquipment(site.id);
  const explain = useAssistantPanel((state) => state.explain);

  useEffect(() => {
    resetErrors();
  }, [alarmId, resetErrors]);

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
  const actionError = acknowledge.error ?? claim.error ?? unassign.error ?? suppress.error ?? unsuppress.error;
  const suppressReasonBytes = new TextEncoder().encode(suppressReason.trim()).length;
  const suppressReasonTooLong = suppressReasonBytes > 256;
  const evaluationContext = { deviceNames: equipment.nameByDevice, pointByReading: equipment.pointByReading };
  const conditionTone = alarm.condition === 'CLEARED' ? 'bg-success' : alarm.acknowledgement ? 'bg-warning' : 'bg-destructive';
  const edgeProjection = alarm.edgeProjection;
  const edgeIsOffline = Boolean(edgeProjection && (edgeProjection.offline || edgeProjection.clearOffline));
  const edgeBundleTag = edgeProjection ? formatEdgeBundleTag(edgeProjection) : null;
  const edgeRaiseActions = edgeProjection ? formatEdgeTriggeredActionsSummary(edgeProjection.triggeredActions, deviceNames) : null;
  const edgeClearActions = edgeProjection ? formatEdgeTriggeredActionsSummary(edgeProjection.clearTriggeredActions, deviceNames) : null;

  return (
    <section role="region" aria-label="告警详情" className="flex min-h-0 min-w-0 flex-1 flex-col overflow-y-auto">
      <header className="flex flex-col gap-2 border-b border-divider px-6 pt-[18px] pb-4">
        <div className="flex items-center gap-2.5 text-xs">
          <SeverityBadge severity={alarm.currentSeverity} />
          <span className="flex items-center gap-1.5 font-medium">
            <span className={cn('size-2 rounded-full', conditionTone)} aria-hidden="true" />
            {alarmStatusLabel(alarm)}
          </span>
          <span className="text-muted-foreground">· <span className="font-mono">{formatTime(alarm.firstOccurredAt, site.timezone)}</span> 起</span>
          {edgeProjection ? (
            <Badge
              variant="outline"
              className={cn(
                'gap-1 border-transparent',
                edgeIsOffline ? 'bg-warning-soft text-warning-text' : 'bg-primary-soft text-primary',
              )}
            >
              {edgeIsOffline ? <WifiOff className="size-3" aria-hidden="true" /> : <Cpu className="size-3" aria-hidden="true" />}
              {edgeSourceLabel(edgeProjection)}
            </Badge>
          ) : null}
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
          {canClaim && alarm.condition === 'ACTIVE' && alarm.assigneeId !== myId ? (
            <Button variant="outline" disabled={claim.isPending} onClick={() => claim.mutate(alarm)}><UserCheck data-icon="inline-start" />由我处理</Button>
          ) : null}
          {canClaim && alarm.condition === 'ACTIVE' && alarm.assigneeId ? (
            <Button
              variant="outline"
              disabled={unassign.isPending}
              onClick={() => unassign.mutate({ alarm, reason: '交回班组待分配' })}
            >
              <UserMinus data-icon="inline-start" />
              {unassign.isPending ? '正在取消…' : '取消分配'}
            </Button>
          ) : null}
          {canClaim && alarm.condition === 'ACTIVE' && !alarm.suppression ? (
            <Popover
              open={suppressOpen}
              onOpenChange={(nextOpen) => {
                if (nextOpen) resetErrors();
                setSuppressOpen(nextOpen);
              }}
            >
              <PopoverTrigger asChild>
                <Button variant="outline" disabled={suppress.isPending}>
                  <BellOff data-icon="inline-start" />
                  抑制通知
                </Button>
              </PopoverTrigger>
              <PopoverContent align="start" className="flex w-80 flex-col gap-3">
                <p className="text-sm font-semibold">暂时抑制这条告警的通知？</p>
                <fieldset className="flex flex-col gap-2">
                  <legend className="text-xs font-medium text-muted-foreground">抑制时长</legend>
                  <RadioGroup value={suppressHours} onValueChange={setSuppressHours} className="grid grid-cols-3 gap-2">
                    {SUPPRESS_PRESETS.map((preset) => {
                      const itemId = `${suppressPresetPrefix}-${preset.hours}`;
                      return (
                        <div key={preset.hours} className="flex items-center gap-1.5 rounded-md border px-2.5 py-1.5">
                          <RadioGroupItem value={preset.hours} id={itemId} />
                          <Label htmlFor={itemId} className="cursor-pointer text-xs font-normal">{preset.label}</Label>
                        </div>
                      );
                    })}
                  </RadioGroup>
                </fieldset>
                <div className="flex flex-col gap-1.5">
                  <div className="flex items-center justify-between gap-2">
                    <Label htmlFor={suppressReasonId} className="text-xs font-medium text-muted-foreground">抑制原因</Label>
                    <span className={cn('font-mono text-[11px]', suppressReasonTooLong ? 'text-destructive-text' : 'text-muted-foreground')}>
                      {suppressReasonBytes}/256 字节
                    </span>
                  </div>
                  <Textarea
                    id={suppressReasonId}
                    rows={2}
                    placeholder="例如：正在清洗冷却塔，预计 4 小时后恢复"
                    value={suppressReason}
                    onChange={(event) => setSuppressReason(event.target.value)}
                  />
                  {suppressReasonTooLong ? <p className="text-xs text-destructive-text">抑制原因过长，最多 256 字节（约 85 个汉字）。</p> : null}
                  {suppress.error ? <p className="text-xs text-destructive-text">{suppress.error.message}</p> : null}
                </div>
                <div className="flex justify-end gap-2">
                  <Button variant="ghost" size="sm" onClick={() => setSuppressOpen(false)}>取消</Button>
                  <Button
                    size="sm"
                    disabled={suppress.isPending || suppressReason.trim().length === 0 || suppressReasonTooLong}
                    onClick={() => {
                      const hours = Number(suppressHours) || 4;
                      const until = new Date(Date.now() + hours * 3_600_000).toISOString();
                      suppress.mutate(
                        { alarm, suppressedUntil: until, reason: suppressReason },
                        { onSuccess: () => { setSuppressReason(''); setSuppressOpen(false); } },
                      );
                    }}
                  >
                    {suppress.isPending ? '正在抑制…' : '确认抑制'}
                  </Button>
                </div>
              </PopoverContent>
            </Popover>
          ) : null}
          {canClaim && alarm.condition === 'ACTIVE' && alarm.suppression ? (
            <Button
              variant="outline"
              aria-describedby={suppressionNoteId}
              disabled={unsuppress.isPending}
              onClick={() => unsuppress.mutate({ alarm, reason: '恢复告警通知' })}
            >
              <Bell data-icon="inline-start" />
              {unsuppress.isPending ? '正在解除…' : '解除抑制'}
            </Button>
          ) : null}
          {canCreateWorkOrder ? (
            <Button variant="outline" onClick={() => onCreatingWorkOrderChange(true)}><Plus data-icon="inline-start" />创建工单</Button>
          ) : null}
          {canReadTrends ? (
            <Button variant="outline" asChild>
              <Link
                to="/operations/trends"
                search={buildAlarmTrendSearch({
                  siteId: site.id,
                  alarm,
                  catalog: trendCatalog,
                })}
              >
                <LineChart data-icon="inline-start" />
                查看当时趋势
              </Link>
            </Button>
          ) : null}
          <Button variant="outline" onClick={() => explain(explainAlarmQuestion(alarm.title, alarm.firstOccurredAt, alarm.condition === 'CLEARED', deviceName, site.timezone))}>
            <BrandMark data-icon="inline-start" className="size-3.5 text-primary" />分析原因
          </Button>
        </div>
        {alarm.condition === 'ACTIVE' && alarm.suppression ? (
          <div id={suppressionNoteId} className="rounded-lg border bg-muted/50 px-3 py-2 text-xs text-secondary-foreground">
            已抑制通知至 <span className="font-mono font-medium">{formatTime(alarm.suppression.expiresAt, site.timezone)}</span> · 原因：{alarm.suppression.reason}
          </div>
        ) : null}
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
          {edgeProjection ? (
            <>
              <Fact label="评估来源">{edgeProjection.offline ? '边缘网关 · 离网补传' : '边缘网关 · 在线触发'}</Fact>
              <Fact label="上报网关" span={2} title={formatEdgeGatewayName(edgeProjection.gatewayId, deviceNames)}>
                {formatEdgeGatewayName(edgeProjection.gatewayId, deviceNames)}
              </Fact>
              {edgeBundleTag ? <Fact label="边缘规则包"><span className="font-mono">{edgeBundleTag}</span></Fact> : null}
              {edgeProjection.offline && edgeProjection.receivedAt ? (
                <Fact label="云端补传时间"><span className="font-mono">{formatTime(edgeProjection.receivedAt, site.timezone)}</span></Fact>
              ) : null}
            </>
          ) : null}
        </dl>
        {alarm.acknowledgement?.comment ? (
          <p className="rounded-lg bg-muted px-3 py-2 text-sm text-secondary-foreground">“{alarm.acknowledgement.comment}”</p>
        ) : null}
        {edgeRaiseActions || edgeClearActions ? (
          <div className="rounded-lg border bg-muted/50 px-3 py-2 text-xs text-secondary-foreground">
            {edgeRaiseActions ? <p>边缘侧已执行联动：{edgeRaiseActions}</p> : null}
            {edgeClearActions ? <p className={edgeRaiseActions ? 'mt-1' : undefined}>恢复时边缘联动：{edgeClearActions}</p> : null}
          </div>
        ) : null}
      </div>

      <div className="grid grid-cols-2 gap-8 px-6 py-4">
        <section className="flex flex-col gap-3">
          <h3 className="text-sm font-semibold">处理记录</h3>
          <Timeline>
            {[...alarm.timeline].reverse().map((entry, index) => {
              const Icon = OPERATION_ICONS[entry.operation];
              const edgeActor = formatEdgeActorName(entry.actorId, deviceNames);
              const actorLabel = edgeActor ?? (entry.actorType === 'WORKLOAD' ? '系统' : personLabel(entry.actorId, myId));
              const edgeNote = formatEdgeTimelineNote(entry, edgeProjection, deviceNames);
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
                      {actorLabel}
                      {recoveryReason(entry) ? ` · ${recoveryReason(entry)}` : ''} · {SEVERITY_LABELS[entry.currentSeverity]}
                    </p>
                    {edgeNote ? <p className="mt-0.5 text-xs text-secondary-foreground">{edgeNote}</p> : null}
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

      <section role="region" aria-labelledby={evalRegionId} className="flex flex-col gap-3 border-t border-divider px-6 py-4">
        <h3 id={evalRegionId} className="text-sm font-semibold">触发评估记录</h3>
        {evaluations.isError ? (
          <p className="text-xs text-muted-foreground">评估快照暂时读不到，不影响处理当前告警。</p>
        ) : evaluations.isPending ? (
          <Skeleton className="h-20" />
        ) : !evaluations.data || evaluations.data.items.length === 0 ? (
          <p className="text-xs text-muted-foreground">暂无评估快照记录</p>
        ) : (
          <Table aria-label="规则评估明细表">
            <TableHeader>
              <TableRow>
                <TableHead scope="col" className="w-40">评估时间</TableHead>
                <TableHead scope="col" className="w-20">判定</TableHead>
                <TableHead scope="col" className="w-28">动作</TableHead>
                <TableHead scope="col">当时读数与说明</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {evaluations.data.items.map((record) => {
                const blocker = formatQualityBlocker(record.qualityBlocker, evaluationContext);
                return (
                  <TableRow key={`${record.evaluatedAt}-${record.inputRevision}`}>
                    <TableCell className="font-mono text-xs whitespace-nowrap text-muted-foreground">
                      {formatTime(record.evaluatedAt, site.timezone)}
                    </TableCell>
                    <TableCell className="text-xs font-medium">{EVALUATION_STATUS_LABELS[record.status]}</TableCell>
                    <TableCell className="text-xs text-muted-foreground">{EVALUATION_EFFECT_LABELS[record.effect]}</TableCell>
                    <TableCell className="text-xs whitespace-normal text-secondary-foreground">
                      {formatEvaluationSnapshot(record.snapshot, evaluationContext)}
                      {blocker ? ` · ${blocker}` : ''}
                    </TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        )}
      </section>

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
