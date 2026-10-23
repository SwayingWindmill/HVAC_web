import { useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { getRouteApi } from '@tanstack/react-router';
import { RefreshCw } from 'lucide-react';
import { PageHeader, PageMeta } from '@/blocks/page-header';
import { Panel } from '@/blocks/panel';
import { Main } from '@/components/layout/Main';
import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group';
import { activeAlarmsQuery, recentlyClearedAlarmsQuery, type Alarm } from '@/features/alarms/alarm-api';
import { formatDuration, sortAlarmsForOperators } from '@/features/alarms/alarm-presentation';
import { AlarmDetailPane } from '@/features/alarms/components/AlarmDetailPane';
import { AlarmGroupHeader, AlarmListRow } from '@/features/alarms/components/AlarmListRow';
import { useDeviceNames } from '@/features/assets/use-device-names';
import { useNow } from '@/hooks/use-now';
import { formatCompactTime } from '@/lib/operator-format';

const siteRoute = getRouteApi('/_app/_site');
const pageRoute = getRouteApi('/_app/_site/notifications');

const clearedAt = (alarm: Alarm) => alarm.clearedAt ?? alarm.lastOccurredAt;

/**
 * Alarms of the same name cleared in the same minute are one row: a gateway coming back clears a
 * dozen 「…读数长时间没有更新」 at once. The row opens one nobody acknowledged if there is one, else
 * the newest, as the mini program does.
 */
function mergeClearedTogether(alarms: readonly Alarm[]): { readonly alarm: Alarm; readonly count: number }[] {
  const rows = new Map<string, { alarm: Alarm; count: number }>();
  for (const alarm of alarms) {
    const key = `${alarm.title}\n${clearedAt(alarm).slice(0, 16)}`;
    const row = rows.get(key);
    if (row === undefined) rows.set(key, { alarm, count: 1 });
    else {
      row.count += 1;
      if (row.alarm.acknowledgement && !alarm.acknowledgement) row.alarm = alarm;
    }
  }
  return [...rows.values()];
}

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
  const keeps = (alarm: Alarm) => !unacknowledgedOnly || !alarm.acknowledgement;

  const happening = useMemo(() => sortAlarmsForOperators(active.data?.items ?? []), [active.data]);
  const waiting = happening.filter((alarm) => !alarm.acknowledgement).length;
  const happeningRows = happening.filter(keeps).map((alarm) => ({ alarm, count: 1 }));
  const clearedRows = mergeClearedTogether((cleared.data ?? []).filter(keeps));
  const groups = [
    {
      label: '正在发生',
      rows: happeningRows,
      empty: unacknowledgedOnly && happening.length > 0 ? `正在发生的 ${happening.length} 条都有人确认了` : '现在没有正在发生的告警',
    },
    { label: '最近恢复', rows: clearedRows, empty: unacknowledgedOnly ? '最近恢复的都有人确认了' : '最近没有恢复的告警' },
  ];
  const shown = groups.flatMap((group) => group.rows);
  const selectedId = search.inspect ?? shown[0]?.alarm.alarmId;
  const pending = active.isPending || cleared.isPending;
  const failed = active.isError || cleared.isError;
  const refresh = () => { void active.refetch(); void cleared.refetch(); };
  const setSearch = (patch: { unack?: true; inspect?: string }) =>
    void navigate({ search: (previous) => ({ ...previous, ...patch }), replace: true });

  return (
    <Main fixed>
      <PageHeader
        title="通知"
        description={site.displayName}
        meta={active.isSuccess ? <PageMeta dot={waiting > 0 ? 'warning' : 'idle'}>{waiting > 0 ? `${waiting} 条正在发生、没人确认` : '正在发生的都有人确认了'}</PageMeta> : undefined}
        actions={(
          <Button variant="outline" size="sm" onClick={refresh} disabled={active.isFetching || cleared.isFetching}>
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
            {([['all', '全部'], ['unack', '只看未确认']] as const).map(([value, label]) => (
              <ToggleGroupItem
                key={value}
                value={value}
                className="h-full flex-none gap-1.5 rounded-none border-0 px-0 text-[13px] font-medium text-muted-foreground shadow-[inset_0_-2px_0_transparent] hover:bg-transparent hover:text-foreground data-[state=on]:bg-transparent data-[state=on]:text-foreground data-[state=on]:shadow-[inset_0_-2px_0_var(--primary)]"
              >
                {label}
              </ToggleGroupItem>
            ))}
          </ToggleGroup>

          <div className="min-h-0 flex-1 overflow-y-auto">
            {failed ? (
              <div className="flex flex-col items-start gap-2 px-4 py-6">
                <p className="text-sm text-destructive-text">告警暂时读不到</p>
                <Button variant="outline" size="sm" onClick={refresh}>重试</Button>
              </div>
            ) : pending ? (
              <div className="flex flex-col gap-2 p-4"><Skeleton className="h-14" /><Skeleton className="h-14" /><Skeleton className="h-14" /></div>
            ) : (
              <ul aria-label="告警列表">
                {groups.map((group) => (
                  <li key={group.label}>
                    <AlarmGroupHeader label={group.label} count={group.rows.length} />
                    {group.rows.length === 0 ? (
                      <p className="border-b border-divider px-4 py-4 text-xs text-muted-foreground">{group.empty}</p>
                    ) : (
                      <ul>
                        {group.rows.map(({ alarm, count }) => {
                          const happeningNow = alarm.condition === 'ACTIVE';
                          return (
                            <AlarmListRow
                              key={alarm.alarmId}
                              alarm={alarm}
                              selected={alarm.alarmId === selectedId}
                              onSelect={() => setSearch({ inspect: alarm.alarmId })}
                              muted={!happeningNow}
                              when={happeningNow
                                ? `${formatCompactTime(alarm.firstOccurredAt, site.timezone, now)} 开始`
                                : `${formatCompactTime(clearedAt(alarm), site.timezone, now)} 恢复`}
                              facts={<>
                                {alarm.deviceId ? deviceNames.get(alarm.deviceId) ?? '—' : '站点'}
                                {' · '}{happeningNow ? '已经持续' : '持续了'} {formatDuration(alarm.firstOccurredAt, alarm.clearedAt, now)}
                                {' · '}{alarm.acknowledgement ? '已确认' : '未确认'}
                                {count > 1 ? ` · 同时恢复共 ${count} 条` : ''}
                              </>}
                            />
                          );
                        })}
                      </ul>
                    )}
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
