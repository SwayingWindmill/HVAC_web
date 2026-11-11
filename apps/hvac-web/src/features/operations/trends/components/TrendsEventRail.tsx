import { useState } from 'react';
import { Link } from '@tanstack/react-router';
import { ChevronDown, ChevronRight, Plus, SlidersHorizontal } from 'lucide-react';
import { SeverityMark } from '@/components/severity-mark';
import { Button } from '@/components/ui/button';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import type { AlarmSeverity } from '@/features/alarms/alarm-api';
import {
  SEVERITY_COLORS,
  SEVERITY_LEVEL,
  SeverityBadge,
  severityRank,
} from '@/features/alarms/alarm-presentation';
import { formatCompactTime, formatTime } from '@/lib/operator-format';
import { cn } from '@/lib/utils';
import type {
  TrendOverlayAlarmInput,
  TrendOverlaysModel,
  TrendRailCluster,
} from '../trends-overlays';

function topClusterSeverity(cluster: TrendRailCluster): AlarmSeverity | null {
  let best: AlarmSeverity | null = null;
  for (const ev of cluster.events) {
    if (ev.kind !== 'alarm') continue;
    const sev = ev.span.alarm.currentSeverity;
    if (!best || severityRank(sev) < severityRank(best)) {
      best = sev;
    }
  }
  return best;
}

export function TrendsEventRail({
  siteId,
  timezone,
  overlays,
  canCreateWorkOrder,
  onCreateWorkOrderForAlarm,
}: {
  readonly siteId: string;
  readonly timezone: string;
  readonly overlays: TrendOverlaysModel;
  readonly canCreateWorkOrder: boolean;
  readonly onCreateWorkOrderForAlarm: (alarm: TrendOverlayAlarmInput) => void;
}) {
  const [collapsed, setCollapsed] = useState(false);
  const [openClusterId, setOpenClusterId] = useState<string | null>(null);

  const alarmCount = overlays.alarmSpans.length;
  const commandCount = overlays.commandMarks.length;

  return (
    <section
      role="region"
      aria-label="事件时间轴"
      className="flex flex-col gap-2 border-b border-divider bg-muted/10 px-5 py-2.5"
    >
      <div className="flex flex-wrap items-center justify-between gap-2 text-xs">
        <button
          type="button"
          onClick={() => setCollapsed((prev) => !prev)}
          className="inline-flex items-center gap-1.5 font-medium text-foreground hover:text-primary"
        >
          {collapsed ? (
            <ChevronRight className="size-3.5 text-muted-foreground" aria-hidden="true" />
          ) : (
            <ChevronDown className="size-3.5 text-muted-foreground" aria-hidden="true" />
          )}
          <span>事件时间轴</span>
          <span className="font-normal text-muted-foreground">
            · 告警 {alarmCount} 条 · 控制指令 {commandCount} 条
          </span>
        </button>

        {!collapsed && overlays.railItems.length === 0 ? (
          <span className="text-muted-foreground">所选时段内无关联告警或控制指令</span>
        ) : null}
      </div>

      {!collapsed && overlays.railItems.length > 0 ? (
        <div className="relative mr-3 ml-12 h-8 rounded-md border border-divider bg-background">
          <div className="absolute inset-x-0 top-1/2 h-px -translate-y-1/2 bg-divider" aria-hidden="true" />

          {overlays.railItems.map((cluster: TrendRailCluster) => {
            const clampedLeft = Math.min(98, Math.max(2, cluster.anchorPercent));
            const isCluster = cluster.events.length > 1;
            const firstEvent = cluster.events[0]!;
            const isOpen = openClusterId === cluster.id;

            const buttonLabel = isCluster
              ? `${cluster.events.length} 条事件`
              : firstEvent.kind === 'alarm'
                ? firstEvent.span.alarm.title
                : firstEvent.mark.summaryLabel;

            const alarmSeverity = topClusterSeverity(cluster);
            const severityColor = alarmSeverity ? SEVERITY_COLORS[alarmSeverity] : null;

            return (
              <Popover
                key={cluster.id}
                open={isOpen}
                onOpenChange={(nextOpen) => setOpenClusterId(nextOpen ? cluster.id : null)}
              >
                <PopoverTrigger asChild>
                  <button
                    type="button"
                    aria-label={buttonLabel}
                    style={{
                      left: `${clampedLeft}%`,
                      ...(severityColor
                        ? {
                            color: severityColor,
                            borderColor: `color-mix(in oklab, ${severityColor} 35%, transparent)`,
                            backgroundColor: `color-mix(in oklab, ${severityColor} 12%, var(--popover))`,
                          }
                        : {}),
                    }}
                    className={cn(
                      'absolute top-1/2 inline-flex max-w-44 -translate-x-1/2 -translate-y-1/2 items-center gap-1.5 rounded-full border px-2 py-0.5 text-[11px] font-medium shadow-2xs transition-opacity hover:opacity-90',
                      !severityColor && 'border-primary/35 bg-primary-soft text-primary',
                    )}
                  >
                    {alarmSeverity ? (
                      <SeverityMark
                        level={SEVERITY_LEVEL[alarmSeverity]}
                        label=""
                        className="size-2 shrink-0"
                      />
                    ) : (
                      <SlidersHorizontal className="size-3 shrink-0" aria-hidden="true" />
                    )}
                    <span className="truncate">
                      {isCluster
                        ? `+${cluster.events.length} 条事件`
                        : firstEvent.kind === 'alarm'
                          ? firstEvent.span.alarm.title
                          : firstEvent.mark.deviceName}
                    </span>
                  </button>
                </PopoverTrigger>
                <PopoverContent align="center" className="flex w-88 flex-col gap-2.5 p-3">
                  <p className="text-xs font-semibold text-muted-foreground">
                    {isCluster ? `该时段共 ${cluster.events.length} 条关联事件` : '关联事件详情'}
                  </p>
                  <div className="flex max-h-72 flex-col divide-y divide-divider overflow-y-auto">
                    {cluster.events.map((ev) => {
                      if (ev.kind === 'alarm') {
                        const { alarm, deviceName } = ev.span;
                        return (
                          <div
                            key={`alarm-${alarm.alarmId}`}
                            className="flex flex-col gap-1.5 py-2 first:pt-0 last:pb-0"
                          >
                            <div className="flex items-center gap-2 text-xs">
                              <SeverityBadge severity={alarm.currentSeverity} />
                              <span className="font-medium text-foreground">
                                {alarm.condition === 'ACTIVE' ? '活动' : '已恢复'}
                              </span>
                              <span className="ml-auto font-mono text-[11px] text-muted-foreground">
                                {formatCompactTime(alarm.firstOccurredAt, timezone)}
                              </span>
                            </div>
                            <p className="text-xs font-semibold text-foreground">
                              {alarm.title} · {deviceName}
                            </p>
                            <p className="text-xs leading-relaxed text-secondary-foreground">
                              {alarm.summary}
                            </p>
                            <div className="mt-1 flex items-center justify-between gap-2">
                              <Link
                                to="/operations/alarms"
                                search={{ site: siteId, inspect: alarm.alarmId, view: 'all' }}
                                className="text-xs font-medium text-primary hover:underline"
                              >
                                查看告警详情 ›
                              </Link>
                              {canCreateWorkOrder ? (
                                <Button
                                  size="sm"
                                  variant="outline"
                                  className="h-7 px-2 text-xs"
                                  onClick={() => {
                                    setOpenClusterId(null);
                                    onCreateWorkOrderForAlarm(alarm);
                                  }}
                                >
                                  <Plus data-icon="inline-start" />
                                  创建工单
                                </Button>
                              ) : null}
                            </div>
                          </div>
                        );
                      }

                      const { mark } = ev;
                      return (
                        <div
                          key={`cmd-${mark.command.commandId}`}
                          className="flex flex-col gap-1 py-2 first:pt-0 last:pb-0"
                        >
                          <div className="flex items-center justify-between gap-2 text-xs">
                            <span className="inline-flex items-center rounded bg-primary-soft px-1.5 py-0.5 text-[11px] font-medium text-primary">
                              控制指令 · {mark.statusLabel}
                            </span>
                            <span className="font-mono text-[11px] text-muted-foreground">
                              {formatTime(new Date(mark.atMs).toISOString(), timezone).slice(-8)}
                            </span>
                          </div>
                          <p className="text-xs font-medium text-foreground">{mark.summaryLabel}</p>
                          <Link
                            to="/operations/control"
                            search={{ site: siteId }}
                            className="text-xs font-medium text-primary hover:underline"
                          >
                            查看运行调节 ›
                          </Link>
                        </div>
                      );
                    })}
                  </div>
                </PopoverContent>
              </Popover>
            );
          })}
        </div>
      ) : null}
    </section>
  );
}
