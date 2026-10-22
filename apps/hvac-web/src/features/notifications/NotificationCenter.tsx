import { useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { getRouteApi } from '@tanstack/react-router';
import { CircleCheck, RefreshCw } from 'lucide-react';
import { PageHeader, PageMeta } from '@/blocks/page-header';
import { Panel } from '@/blocks/panel';
import { Main } from '@/components/layout/Main';
import { SeverityMark } from '@/components/severity-mark';
import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group';
import { activeAlarmsQuery, recentlyClearedAlarmsQuery, type Alarm } from '@/features/alarms/alarm-api';
import { formatDuration, SEVERITY_LABELS, SEVERITY_LEVEL, sortAlarmsForOperators } from '@/features/alarms/alarm-presentation';
import { AlarmDetailPane } from '@/features/alarms/components/AlarmDetailPane';
import { useDeviceNames } from '@/features/assets/use-device-names';
import { useNow } from '@/hooks/use-now';
import { formatRelative, formatTime } from '@/lib/operator-format';
import { cn } from '@/lib/utils';

const siteRoute = getRouteApi('/_app/_site');
const pageRoute = getRouteApi('/_app/_site/notifications');

/**
 * 通知 is the Site's alarms themselves, as the mini program shows them: what is happening now,
 * most severe first, then what cleared lately. Picking one opens it beside the list to acknowledge.
 */
export function NotificationCenter() {
  const { site } = siteRoute.useRouteContext();
  const search = pageRoute.useSearch();
  const navigate = pageRoute.useNavigate();
  const deviceNames = useDeviceNames(site.id);
  const now = useNow(30_000);
  const [creatingWorkOrder, setCreatingWorkOrder] = useState(false);

  const active = useQuery(activeAlarmsQuery(site.id));
  const cleared = useQuery(recentlyClearedAlarmsQuery(site.id));
  const unacknowledgedOnly = search.unack === true;

  const happening = useMemo(() => sortAlarmsForOperators(active.data?.items ?? []), [active.data]);
  const unacknowledged = happening.filter((alarm) => !alarm.acknowledgement);
  const groups = [
    { label: '正在发生', alarms: unacknowledgedOnly ? unacknowledged : happening },
    { label: '最近恢复', alarms: (cleared.data ?? []).filter((alarm) => !unacknowledgedOnly || !alarm.acknowledgement) },
  ].filter((group) => group.alarms.length > 0);
  const shown = groups.flatMap((group) => group.alarms);
  const selectedId = search.inspect ?? shown[0]?.alarmId;
  const pending = active.isPending || cleared.isPending;
  const failed = active.isError || cleared.isError;
  const setSearch = (patch: { unack?: true; inspect?: string }) =>
    void navigate({ search: (previous) => ({ ...previous, ...patch }), replace: true });

  return (
    <Main fixed>
      <PageHeader
        title="通知"
        description={site.displayName}
        meta={active.isSuccess ? <PageMeta dot={unacknowledged.length > 0 ? 'warning' : 'idle'}>{unacknowledged.length} 条正在发生、没人确认</PageMeta> : undefined}
        actions={(
          <Button variant="outline" size="sm" onClick={() => { void active.refetch(); void cleared.refetch(); }} disabled={active.isFetching || cleared.isFetching}>
            <RefreshCw data-icon="inline-start" className={active.isFetching || cleared.isFetching ? 'animate-spin' : undefined} />
            刷新
          </Button>
        )}
      />

      <Panel aria-label="通知" className="grid h-[calc(100svh-180px)] min-h-[520px] grid-cols-[420px_minmax(0,1fr)]">
        <div className="flex min-h-0 flex-col border-r">
          <ToggleGroup
            type="single"
            value={unacknowledgedOnly ? 'unack' : 'all'}
            onValueChange={(value) => { if (value) setSearch({ unack: value === 'unack' ? true : undefined, inspect: undefined }); }}
            aria-label="告警范围"
            className="h-11 w-full shrink-0 justify-start gap-5 rounded-none border-b border-divider px-4"
          >
            {([['all', '全部', happening.length + (cleared.data?.length ?? 0)], ['unack', '只看未确认', null]] as const).map(([value, label, count]) => (
              <ToggleGroupItem
                key={value}
                value={value}
                className="h-full flex-none gap-1.5 rounded-none border-0 px-0 text-[13px] font-medium text-muted-foreground shadow-[inset_0_-2px_0_transparent] hover:bg-transparent hover:text-foreground data-[state=on]:bg-transparent data-[state=on]:text-foreground data-[state=on]:shadow-[inset_0_-2px_0_var(--primary)]"
              >
                {label}
                {count !== null && !pending ? <span className="font-mono text-[11px] text-subtle-foreground">{count}</span> : null}
              </ToggleGroupItem>
            ))}
          </ToggleGroup>

          <div className="min-h-0 flex-1 overflow-y-auto">
            {failed ? (
              <div className="flex flex-col items-start gap-2 px-4 py-6">
                <p className="text-sm text-destructive-text">告警暂时读不到</p>
                <Button variant="outline" size="sm" onClick={() => { void active.refetch(); void cleared.refetch(); }}>重试</Button>
              </div>
            ) : pending ? (
              <div className="flex flex-col gap-2 p-4"><Skeleton className="h-14" /><Skeleton className="h-14" /><Skeleton className="h-14" /></div>
            ) : shown.length === 0 ? (
              <div className="flex flex-col items-center gap-2 px-6 py-12 text-center">
                <span className="flex size-9 items-center justify-center rounded-xl bg-success-soft text-success-text"><CircleCheck className="size-[18px]" aria-hidden="true" /></span>
                <p className="text-sm font-semibold">{unacknowledgedOnly ? '没有等人确认的告警' : '现在没有告警'}</p>
                <p className="text-xs text-muted-foreground">{unacknowledgedOnly && happening.length > 0 ? `正在发生的 ${happening.length} 条都有人确认了` : '告警出现或恢复时会列在这里'}</p>
              </div>
            ) : (
              <ul aria-label="告警列表">
                {groups.map((group) => (
                  <li key={group.label}>
                    <div className="flex h-[34px] items-center bg-muted px-4 text-xs font-medium text-muted-foreground">
                      {group.label}
                      <span className="ml-auto font-mono text-subtle-foreground">{group.alarms.length}</span>
                    </div>
                    <ul>
                      {group.alarms.map((alarm) => (
                        <AlarmRow
                          key={alarm.alarmId}
                          alarm={alarm}
                          selected={alarm.alarmId === selectedId}
                          deviceName={alarm.deviceId ? deviceNames.get(alarm.deviceId) ?? '—' : '站点'}
                          timeZone={site.timezone}
                          now={now}
                          onSelect={() => setSearch({ inspect: alarm.alarmId })}
                        />
                      ))}
                    </ul>
                  </li>
                ))}
              </ul>
            )}
            {active.data && !active.data.complete ? <p className="px-4 py-3 text-xs text-muted-foreground">正在发生的告警太多，这里只列出了一部分，全部在「告警」页</p> : null}
          </div>
        </div>

        <AlarmDetailPane
          alarmId={selectedId}
          empty={!pending && shown.length === 0}
          deviceNames={deviceNames}
          creatingWorkOrder={creatingWorkOrder}
          onCreatingWorkOrderChange={setCreatingWorkOrder}
        />
      </Panel>
    </Main>
  );
}

function AlarmRow({ alarm, selected, deviceName, timeZone, now, onSelect }: {
  readonly alarm: Alarm;
  readonly selected: boolean;
  readonly deviceName: string;
  readonly timeZone: string;
  readonly now: number;
  readonly onSelect: () => void;
}) {
  const happening = alarm.condition === 'ACTIVE';
  const at = happening ? alarm.firstOccurredAt : alarm.clearedAt ?? alarm.lastOccurredAt;
  return (
    <li>
      <button
        type="button"
        aria-current={selected ? 'true' : undefined}
        onClick={onSelect}
        className={cn(
          'relative grid w-full grid-cols-[12px_minmax(0,1fr)_auto] gap-x-2.5 gap-y-[3px] border-b border-divider px-4 pt-[11px] pb-3 text-left outline-none hover:bg-accent focus-visible:bg-accent',
          selected && 'bg-primary-soft hover:bg-primary-soft before:absolute before:inset-y-0 before:left-0 before:w-0.5 before:bg-primary',
        )}
      >
        <SeverityMark level={SEVERITY_LEVEL[alarm.currentSeverity]} label={SEVERITY_LABELS[alarm.currentSeverity]} className="row-span-2 mt-[5px]" />
        <span className={cn('truncate text-sm', happening ? 'font-semibold' : 'font-medium text-secondary-foreground')}>{alarm.title}</span>
        <span className="text-xs whitespace-nowrap text-subtle-foreground" title={formatTime(at, timeZone)}>
          {happening ? '' : '恢复于 '}{formatRelative(at, now)}
        </span>
        <span className="col-span-2 col-start-2 truncate text-xs text-muted-foreground">
          {deviceName}
          {' · '}{happening ? '已经持续' : '持续了'} {formatDuration(alarm.firstOccurredAt, alarm.clearedAt, now)}
          {' · '}{alarm.acknowledgement ? '已确认' : '未确认'}
        </span>
      </button>
    </li>
  );
}
