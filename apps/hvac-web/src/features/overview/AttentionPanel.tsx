import { useState } from 'react';
import { Link } from '@tanstack/react-router';
import { CircleCheck } from 'lucide-react';
import { Panel, PanelCount, PanelHeader, PanelLink } from '@/blocks/panel';
import { SeverityMark, type SeverityLevel } from '@/components/severity-mark';
import { Skeleton } from '@/components/ui/skeleton';
import type { WorkOrder, WorkOrderPriority } from '@/api/work-orders';
import type { Alarm, AlarmSeverity } from '@/features/alarms/alarm-api';
import { ruleFor, severity, type Finding } from '@/features/diagnostics/diagnostics-model';
import { SEVERITY as FINDING_SEVERITY } from '@/features/diagnostics/finding-presentation';
import { PRIORITY_LABELS, STATUS_LABELS } from '@/features/work-orders/work-order-presentation';
import { formatCompactTime, formatDecimal, personLabel } from '@/lib/operator-format';
import { cn } from '@/lib/utils';

type Kind = 'alarm' | 'finding' | 'work';

interface AttentionItem {
  readonly key: string;
  readonly kind: Kind;
  readonly level: SeverityLevel;
  readonly title: string;
  readonly at: string;
  readonly meta: string;
  readonly value?: string;
  readonly to: '/operations/alarms' | '/operations/diagnostics' | '/operations/work-center';
  readonly inspect: string;
}

const LEVEL_RANK: Readonly<Record<SeverityLevel, number>> = { critical: 0, major: 1, minor: 2, warning: 3, info: 4 };
const ALARM_LEVEL: Readonly<Record<AlarmSeverity, SeverityLevel>> = { CRITICAL: 'critical', MAJOR: 'major', MINOR: 'minor', WARNING: 'warning', INFO: 'info' };
const FINDING_LEVEL = { high: 'critical', medium: 'major', low: 'minor' } as const satisfies Record<ReturnType<typeof severity>, SeverityLevel>;
const PRIORITY_LEVEL: Readonly<Record<WorkOrderPriority, SeverityLevel>> = { URGENT: 'critical', HIGH: 'major', MEDIUM: 'minor', LOW: 'info' };
const KIND_LABEL: Readonly<Record<Kind, string>> = { alarm: '告警', finding: '诊断', work: '工单' };
const LIMIT = 5;

interface Source<T> {
  readonly data: readonly T[] | undefined;
  readonly isPending: boolean;
  readonly isError: boolean;
}

/**
 * Everything that waits on the operator in one list, most severe first: active alarms, the last
 * week's diagnosis findings and open work orders. Each row opens the item in its own page.
 */
export function AttentionPanel({ siteId, timeZone, principalId, alarms, findings, workOrders, deviceName, assetName }: {
  readonly siteId: string;
  readonly timeZone: string;
  readonly principalId: string;
  readonly alarms: Source<Alarm>;
  readonly findings: Source<Finding>;
  readonly workOrders: Source<WorkOrder>;
  readonly deviceName: (deviceId: string) => string;
  readonly assetName: (assetId: string) => string;
}) {
  const [tab, setTab] = useState<Kind | 'all'>('all');
  const items: AttentionItem[] = [
    ...(alarms.data ?? []).map((alarm): AttentionItem => ({
      key: `alarm-${alarm.alarmId}`,
      kind: 'alarm',
      level: ALARM_LEVEL[alarm.currentSeverity],
      title: alarm.title,
      at: alarm.lastOccurredAt,
      meta: `${alarm.deviceId ? deviceName(alarm.deviceId) : '站点'} · ${alarm.acknowledgement ? '已确认' : '未确认'}`,
      to: '/operations/alarms',
      inspect: alarm.alarmId,
    })),
    ...(findings.data ?? []).map((finding): AttentionItem => ({
      key: `finding-${finding.id}`,
      kind: 'finding',
      level: FINDING_LEVEL[severity(finding)],
      title: ruleFor(finding.findingType)?.title ?? '诊断发现',
      at: finding.evaluationTo,
      meta: `${assetName(finding.assetId)} · ${FINDING_SEVERITY[severity(finding)].label}`,
      value: `${formatDecimal(finding.observedValue, 1)} ${finding.valueUnit}`,
      to: '/operations/diagnostics',
      inspect: finding.id,
    })),
    ...(workOrders.data ?? []).map((order): AttentionItem => ({
      key: `work-${order.workOrderId}`,
      kind: 'work',
      level: PRIORITY_LEVEL[order.priority],
      title: order.title,
      at: order.createdAt,
      meta: `${PRIORITY_LABELS[order.priority]} · ${order.assigneeId ? personLabel(order.assigneeId, principalId) : '未指派'} · ${STATUS_LABELS[order.status]}`,
      to: '/operations/work-center',
      inspect: order.workOrderId,
    })),
  ].sort((left, right) => LEVEL_RANK[left.level] - LEVEL_RANK[right.level] || Date.parse(right.at) - Date.parse(left.at));

  const counts: Readonly<Record<Kind | 'all', number>> = {
    all: items.length,
    alarm: items.filter((item) => item.kind === 'alarm').length,
    finding: items.filter((item) => item.kind === 'finding').length,
    work: items.filter((item) => item.kind === 'work').length,
  };
  const shown = tab === 'all' ? items : items.filter((item) => item.kind === tab);
  const pending = alarms.isPending || findings.isPending || workOrders.isPending;
  const failed = [alarms.isError && '告警', findings.isError && '诊断发现', workOrders.isError && '工单'].filter(Boolean);
  const urgent = items.some((item) => item.level === 'critical');
  // What is clear, said plainly, so a short list does not read as missing data.
  const quiet = [
    alarms.data && counts.alarm === 0 ? '无活动告警' : null,
    workOrders.data && counts.work === 0 ? '无未完成工单' : null,
    findings.data && counts.finding === 0 ? '近 7 天无诊断发现' : null,
  ].filter((text): text is string => text !== null);
  const onward = tab === 'finding' ? '/operations/diagnostics' : tab === 'work' ? '/operations/work-center' : '/operations/alarms';

  return (
    <Panel role="region" aria-label="需要处理" className="min-h-0 flex-1">
      <PanelHeader
        title="需要处理"
        badge={pending ? null : <PanelCount count={items.length} tone={urgent ? 'urgent' : 'neutral'} />}
        action={<PanelLink to="/operations/alarms" search={{ site: siteId }}>运维</PanelLink>}
      />
      <div className="px-4 pt-3 pb-1">
        <div role="tablist" aria-label="事项类型" className="grid grid-cols-4 gap-0.5 rounded-lg border border-divider bg-muted p-0.5">
          {(['all', 'alarm', 'finding', 'work'] as const).map((key) => (
            <button
              key={key}
              type="button"
              role="tab"
              aria-selected={tab === key}
              onClick={() => setTab(key)}
              className={cn(
                'flex h-[26px] items-center justify-center gap-1 rounded-md text-xs font-medium text-muted-foreground transition-colors duration-100 outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring-soft',
                tab === key && 'bg-card text-foreground shadow-xs ring-1 ring-border',
              )}
            >
              {key === 'all' ? '全部' : KIND_LABEL[key]}
              <span className="font-mono text-subtle-foreground">{counts[key]}</span>
            </button>
          ))}
        </div>
      </div>
      {pending ? (
        <div className="flex flex-col gap-2 p-4"><Skeleton className="h-12" /><Skeleton className="h-12" /><Skeleton className="h-12" /></div>
      ) : shown.length === 0 ? (
        <div className="flex flex-1 flex-col items-center justify-center gap-2 px-6 py-10 text-center">
          <span className="flex size-9 items-center justify-center rounded-xl bg-success-soft text-success-text"><CircleCheck className="size-[18px]" aria-hidden="true" /></span>
          <p className="text-sm font-semibold">{tab === 'all' ? '没有需要处理的事项' : `没有需要处理的${KIND_LABEL[tab]}`}</p>
          <p className="text-xs text-muted-foreground">无活动告警 · 无未完成工单 · 近 7 天无诊断发现</p>
        </div>
      ) : (
        <ul className="flex flex-col" aria-label="需要处理的事项">
          {shown.slice(0, LIMIT).map((item) => (
            <li key={item.key} className="border-b border-divider last:border-b-0">
              <Link
                to={item.to}
                search={{ site: siteId, inspect: item.inspect }}
                className="grid grid-cols-[10px_minmax(0,1fr)_auto] items-start gap-x-2.5 gap-y-0.5 px-4 py-[11px] outline-none hover:bg-accent focus-visible:bg-accent"
              >
                <SeverityMark level={item.level} className="mt-[5px]" />
                <span className="truncate text-sm font-semibold">{item.title}</span>
                <span className="font-mono text-[11px] leading-5 text-subtle-foreground">{formatCompactTime(item.at, timeZone)}</span>
                <span className="col-start-2 truncate text-xs text-muted-foreground">{KIND_LABEL[item.kind]} · {item.meta}</span>
                {item.value ? <span className="text-xs font-semibold tabular-nums">{item.value}</span> : null}
              </Link>
            </li>
          ))}
        </ul>
      )}
      {failed.length > 0 ? <p className="px-4 py-2 text-xs text-muted-foreground">{failed.join('、')}暂不可用</p> : null}
      {tab === 'all' && !pending && shown.length > 0 && shown.length <= LIMIT && quiet.length > 0 ? (
        <p className="mt-auto flex items-center gap-1.5 border-t border-divider px-4 py-2.5 text-xs text-muted-foreground">
          <CircleCheck className="size-3.5 text-success" aria-hidden="true" />
          {quiet.join(' · ')}
        </p>
      ) : null}
      {shown.length > LIMIT ? (
        <div className="mt-auto flex items-center justify-between border-t border-divider px-4 py-2.5 text-xs text-muted-foreground">
          还有 {shown.length - LIMIT} 条
          <PanelLink to={onward} search={{ site: siteId }}>前往运维</PanelLink>
        </div>
      ) : null}
    </Panel>
  );
}
