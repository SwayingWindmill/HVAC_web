import { useMemo } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';
import { AlertCircle, CircleCheck, Lock } from 'lucide-react';
import type { SiteAssetModel } from '@/api/generated/platformGateway.gen';
import { Panel, PanelHeader } from '@/blocks/panel';
import { SeverityMark } from '@/components/severity-mark';
import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { alarmHistoryQuery } from '@/features/alarms/alarm-api';
import { operationLabel, SEVERITY_LABELS, SEVERITY_LEVEL } from '@/features/alarms/alarm-presentation';
import { siteCommandsQuery } from '@/features/control/command-api';
import { commandSummary, STATUS_PRESENTATION } from '@/features/control/command-presentation';
import { CommandMark } from '@/features/control/components/CommandMark';
import { ModeGlyph } from '@/features/control/components/ControlBoundary';
import { commandLabels, controlTargets } from '@/features/control/control-model';
import { activeLocks, controlActorLabel, LOCK_PRESENTATION, MODE_PRESENTATION, siteControlStateQuery } from '@/features/control/control-state';
import { formatCompactTime } from '@/lib/operator-format';
import { cn } from '@/lib/utils';
import type { PlantView } from '../plant-model';

interface PlantEvent {
  readonly key: string;
  readonly at: string;
  readonly mark: React.ReactNode;
  readonly title: string;
  readonly detail: string;
  readonly to?: { readonly path: '/operations/alarms' | '/operations/control'; readonly inspect?: string };
  readonly inspectDeviceId?: string;
}

/** Today at the plant, newest first: alarms, commands and devices whose data stopped. */
export function RecentEvents({ plant, siteId, myId, timeZone, assetModel, canListAlarms, onInspect }: {
  readonly plant: PlantView;
  readonly siteId: string;
  readonly myId: string;
  readonly timeZone: string;
  readonly assetModel: (Parameters<typeof controlTargets>[0] & Pick<SiteAssetModel, 'assets'>) | undefined;
  readonly canListAlarms: boolean;
  readonly onInspect: (deviceId: string) => void;
}) {
  const alarms = useQuery({ ...alarmHistoryQuery(siteId), enabled: canListAlarms });
  const commands = useQuery(siteCommandsQuery(siteId));
  const controlState = useQuery(siteControlStateQuery(siteId));
  const modeState = controlState.data;
  const labels = useMemo(() => (assetModel ? commandLabels(controlTargets(assetModel, new Date())) : new Map()), [assetModel]);
  const assetName = (assetId: string) => assetModel?.assets.find((asset) => asset.id === assetId)?.displayName ?? '设备';
  const deviceName = (deviceId: string | undefined) => (deviceId ? plant.devices.find((device) => device.deviceId === deviceId)?.name ?? '设备' : '站点');
  const dayFmt = new Intl.DateTimeFormat('en-CA', { timeZone, year: 'numeric', month: '2-digit', day: '2-digit' });
  const today = dayFmt.format(new Date());
  const isToday = (value: string) => { const ms = Date.parse(value); return Number.isFinite(ms) && dayFmt.format(new Date(ms)) === today; };
  const sortTs = (at: string) => { const ms = Date.parse(at); return Number.isFinite(ms) ? ms : 0; };

  const events: PlantEvent[] = [
    ...(canListAlarms ? alarms.data?.alarms ?? [] : []).flatMap((alarm) => alarm.timeline
      .filter((entry) => isToday(entry.occurredAt) && (entry.operation === 'PUBLISH' || entry.operation === 'ACKNOWLEDGE' || entry.operation === 'CLEAR'))
      .map((entry): PlantEvent => ({
        key: `alarm-${alarm.alarmId}-${entry.version}`,
        at: entry.occurredAt,
        mark: entry.operation === 'CLEAR'
          ? <CircleCheck className="size-3 text-success" aria-hidden="true" />
          : <SeverityMark level={SEVERITY_LEVEL[entry.currentSeverity]} label="" />,
        title: `${deviceName(alarm.deviceId)} · ${alarm.title}`,
        detail: `${SEVERITY_LABELS[entry.currentSeverity]}告警 · ${operationLabel(entry.operation)}`,
        to: { path: '/operations/alarms', inspect: alarm.alarmId },
      }))),
    ...(commands.data ?? []).filter((command) => isToday(command.updatedAt)).map((command): PlantEvent => {
      const summary = commandSummary(command, labels);
      return {
        key: `command-${command.commandId}`,
        at: command.updatedAt,
        mark: <CommandMark status={command.status} />,
        title: `${summary.target} · ${summary.title}`,
        detail: `指令 · ${STATUS_PRESENTATION[command.status].label}`,
        to: { path: '/operations/control', inspect: command.commandId },
      };
    }),
    ...(modeState?.updatedAt && isToday(modeState.updatedAt) ? [{
      key: `control-mode-${modeState.revision}`,
      at: modeState.updatedAt,
      mark: <ModeGlyph mode={modeState.controlMode} />,
      title: `控制方式改为${MODE_PRESENTATION[modeState.controlMode].label}`,
      detail: [controlActorLabel(modeState, myId), modeState.reason || '控制方式'].join(' · '),
      to: { path: '/operations/control' },
    } satisfies PlantEvent] : []),
    ...[...activeLocks(controlState.data).values()].filter((lock) => lock.updatedAt && isToday(lock.updatedAt)).map((lock): PlantEvent => ({
      key: `control-lock-${lock.assetId}-${lock.revision}`,
      at: lock.updatedAt ?? '',
      mark: <Lock className="size-3 text-muted-foreground" aria-hidden="true" />,
      title: `${assetName(lock.assetId)} · ${lock.lockMode === 'UNLOCKED' ? '解除锁定' : LOCK_PRESENTATION[lock.lockMode].label}`,
      detail: [controlActorLabel(lock, myId), lock.reason || '设备锁定'].join(' · '),
      to: { path: '/operations/control' },
    })),
    ...plant.devices.filter((device) => device.hasStaleData || device.connection === 'OFFLINE').map((device): PlantEvent => ({
      key: `data-${device.deviceId}`,
      at: device.latestSampleAt ?? '',
      mark: <svg viewBox="0 0 10 10" className="size-2.5 shrink-0" aria-hidden="true"><path d="M1.5 1.5h7v7h-7z" fill="none" strokeWidth="1.5" className="stroke-warning" /></svg>,
      title: `${device.row.device.displayName !== device.name ? `${device.name}（${device.row.device.displayName}）` : device.name} · ${device.connection === 'OFFLINE' ? '通信离线' : '读数中断'}`,
      detail: device.connection === 'OFFLINE' ? '设备离线' : '通信在线，没有新数据',
      inspectDeviceId: device.deviceId,
    })),
  ].sort((left, right) => sortTs(right.at) - sortTs(left.at)).slice(0, 7);

  const pending = (canListAlarms && alarms.isPending) || commands.isPending;
  const hasError = (canListAlarms && alarms.isError) || commands.isError;
  return (
    <Panel role="region" aria-label="最近事件" className="min-h-0">
      <PanelHeader title="最近事件" caption="今天" />
      {pending ? <div className="flex flex-col gap-2 p-4"><Skeleton className="h-10" /><Skeleton className="h-10" /><Skeleton className="h-10" /></div> : events.length === 0 && hasError ? (
        <div className="flex flex-1 flex-col items-center justify-center gap-2 px-6 py-10 text-center">
          <AlertCircle className="size-5 text-destructive-text" aria-hidden="true" />
          <p className="text-sm font-semibold">最近事件暂时读不到</p>
          <Button variant="outline" size="sm" onClick={() => { if (canListAlarms) void alarms.refetch(); void commands.refetch(); }}>重试</Button>
        </div>
      ) : events.length === 0 ? (
        <div className="flex flex-1 flex-col items-center justify-center gap-2 px-6 py-10 text-center">
          <CircleCheck className="size-5 text-success" aria-hidden="true" />
          <p className="text-sm font-semibold">今天没有告警、指令和控制变更</p>
          <p className="text-xs text-muted-foreground">设备数据均为最新</p>
        </div>
      ) : (
        <ul className="flex flex-col py-1">
          {events.map((event) => {
            const body = (
              <>
                <span className="font-mono text-[11px] leading-5 text-subtle-foreground">{event.at ? formatCompactTime(event.at, timeZone) : '—'}</span>
                <span className="mt-[5px] flex justify-center">{event.mark}</span>
                <span className="flex min-w-0 flex-col">
                  <span className="truncate text-[13px] font-semibold">{event.title}</span>
                  <span className="truncate text-xs text-muted-foreground">{event.detail}</span>
                </span>
              </>
            );
            const className = 'grid grid-cols-[52px_12px_minmax(0,1fr)] gap-x-2.5 px-4 py-2 outline-none';
            return (
              <li key={event.key}>
                {event.to ? (
                  <Link to={event.to.path} search={{ site: siteId, inspect: event.to.inspect }} className={cn(className, 'hover:bg-accent focus-visible:bg-accent')}>{body}</Link>
                ) : event.inspectDeviceId ? (
                  <button type="button" onClick={() => onInspect(event.inspectDeviceId!)} className={cn(className, 'w-full text-left hover:bg-accent focus-visible:bg-accent')}>{body}</button>
                ) : <div className={className}>{body}</div>}
              </li>
            );
          })}
        </ul>
      )}
      {hasError && events.length > 0 ? <p className="px-4 pb-3 text-xs text-muted-foreground">{canListAlarms && alarms.isError ? '告警' : '指令'}记录暂时读不到</p> : null}
    </Panel>
  );
}
