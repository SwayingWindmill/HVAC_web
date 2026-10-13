import { useEffect, useMemo, useState } from 'react';
import { useInfiniteQuery, useQuery } from '@tanstack/react-query';
import { getRouteApi, useNavigate } from '@tanstack/react-router';
import { CircleCheck, RefreshCw, Search, X } from 'lucide-react';
import { PageHeader } from '@/blocks/page-header';
import { Panel } from '@/blocks/panel';
import { Main } from '@/components/layout/Main';
import { SeverityMark } from '@/components/severity-mark';
import { Button } from '@/components/ui/button';
import {
  Faceted,
  FacetedBadgeList,
  FacetedContent,
  FacetedEmpty,
  FacetedGroup,
  FacetedItem,
  FacetedList,
  FacetedTrigger,
} from '@/components/ui/faceted';
import { InputGroup, InputGroupAddon, InputGroupInput } from '@/components/ui/input-group';
import { Kbd } from '@/components/ui/kbd';
import { Skeleton } from '@/components/ui/skeleton';
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group';
import { useDeviceNames } from '@/features/assets/use-device-names';
import { useDebouncedCallback } from '@/hooks/use-debounced-callback';
import { useNow } from '@/hooks/use-now';
import { formatRelative, formatTime, personLabel } from '@/lib/operator-format';
import { cn } from '@/lib/utils';
import {
  activeAlarmsQuery,
  alarmHistoryQuery,
  alarmKeys,
  listAlarms,
  type Alarm,
  type AlarmListFilter,
  type AlarmSeverity,
} from '../alarm-api';
import { alarmDailyCounts, alarmsByDevice, alarmsInWindow, meanTimeToClear } from '../alarm-history';
import {
  formatDuration,
  formatSpan,
  SEVERITY_COLORS,
  SEVERITY_LABELS,
  SEVERITY_LEVEL,
  SEVERITY_ORDER,
  sortAlarmsForOperators,
} from '../alarm-presentation';
import { useAlarmActions } from '../use-alarm-actions';
import { AlarmDetailPane } from './AlarmDetailPane';

const siteRoute = getRouteApi('/_app/_site');
const pageRoute = getRouteApi('/_app/_site/operations/alarms');

export type AlarmView = 'active' | 'unack' | 'cleared' | 'all';

const VIEWS: readonly { readonly value: AlarmView; readonly label: string; readonly filter: AlarmListFilter }[] = [
  { value: 'active', label: '活动', filter: { condition: 'ACTIVE' } },
  { value: 'unack', label: '未确认', filter: { condition: 'ACTIVE', acknowledged: false } },
  { value: 'cleared', label: '已恢复', filter: { condition: 'CLEARED' } },
  { value: 'all', label: '全部', filter: {} },
];

// The Alarm owner has no stream yet; lists follow it on a short interval.
const REFRESH_MS = 15_000;

/** The rows of the list, grouped the way an operator triages them. */
function groupAlarms(alarms: readonly Alarm[]): { readonly label: string; readonly alarms: readonly Alarm[] }[] {
  const groups = [
    { label: '未确认 · 等待有人跟进', alarms: alarms.filter((alarm) => alarm.condition === 'ACTIVE' && !alarm.acknowledgement) },
    { label: '已确认 · 跟进中', alarms: alarms.filter((alarm) => alarm.condition === 'ACTIVE' && alarm.acknowledgement) },
    { label: '已恢复', alarms: alarms.filter((alarm) => alarm.condition === 'CLEARED') },
  ];
  return groups.filter((group) => group.alarms.length > 0);
}

function SummaryCell({ label, value, caption, children, className }: {
  readonly label: string;
  readonly value?: React.ReactNode;
  readonly caption?: React.ReactNode;
  readonly children?: React.ReactNode;
  readonly className?: string;
}) {
  return (
    <div className={cn('flex min-w-0 flex-col justify-center gap-1 border-r border-divider px-5 py-3 last:border-r-0', className)}>
      <span className="text-xs text-muted-foreground">{label}</span>
      {value !== undefined ? <span className="text-lg leading-6 font-semibold tracking-[-0.02em] tabular-nums">{value}</span> : null}
      {children}
      {caption ? <span className="truncate text-xs text-muted-foreground">{caption}</span> : null}
    </div>
  );
}

/** Recent history at a glance: how fast alarms clear, how many arrive each day and where they come from. */
function AlarmSummary({ alarms, days, timeZone, now, deviceNames, onDevice }: {
  readonly alarms: readonly Alarm[] | undefined;
  readonly days: number;
  readonly timeZone: string;
  readonly now: number;
  readonly deviceNames: ReadonlyMap<string, string>;
  readonly onDevice: (deviceId: string) => void;
}) {
  const daily = useMemo(() => (alarms ? alarmDailyCounts(alarms, timeZone, days, now) : []), [alarms, timeZone, days, now]);
  const devices = useMemo(() => (alarms ? alarmsByDevice(alarms).slice(0, 3) : []), [alarms]);
  const mttr = alarms ? meanTimeToClear(alarms) : null;
  const cleared = alarms?.filter((alarm) => alarm.condition === 'CLEARED').length ?? 0;
  const total = (row: (typeof daily)[number]) => SEVERITY_ORDER.reduce((sum, severity) => sum + row[severity], 0);
  const peak = Math.max(1, ...daily.map(total));
  const today = daily.at(-1);
  if (!alarms) return <Skeleton className="h-[74px] rounded-xl" />;
  return (
    <Panel aria-label="告警概况" className="grid grid-cols-[minmax(0,0.8fr)_minmax(0,1.4fr)_minmax(0,1.2fr)]">
      <SummaryCell
        label="平均恢复时长"
        value={mttr === null ? '—' : formatSpan(mttr)}
        caption={`近 ${days} 天已恢复 ${cleared} 条`}
      />
      <SummaryCell label={`近 ${days} 天新触发`}>
        <div className="flex items-end gap-4">
          <span className="text-lg leading-6 font-semibold tracking-[-0.02em] tabular-nums">{alarms.length}<span className="ml-1 text-xs font-medium text-subtle-foreground">条</span></span>
          <div className="flex h-7 flex-1 items-end gap-[3px]" role="img" aria-label={`近 ${days} 天每天新触发的告警`}>
            {daily.map((row) => (
              <span key={row.day} className="flex flex-1 flex-col-reverse overflow-hidden rounded-[2px]" style={{ height: `${Math.max(8, (total(row) / peak) * 100)}%` }} title={`${row.label} · ${total(row)} 条`}>
                {total(row) === 0 ? <span className="h-full bg-chart-grid" /> : SEVERITY_ORDER.filter((severity) => row[severity] > 0).map((severity) => (
                  <span key={severity} style={{ height: `${(row[severity] / total(row)) * 100}%`, background: SEVERITY_COLORS[severity] }} />
                ))}
              </span>
            ))}
          </div>
          <span className="shrink-0 text-xs text-muted-foreground">今日 {today ? total(today) : 0} 条</span>
        </div>
      </SummaryCell>
      <SummaryCell label="告警最多的设备" caption={devices.length === 0 ? (alarms.length > 0 ? '都是站点级告警' : `近 ${days} 天没有设备告警`) : undefined}>
        {devices.length > 0 ? (
          <div className="flex flex-wrap gap-1.5">
            {devices.map(({ deviceId, count }) => (
              <button
                key={deviceId}
                type="button"
                onClick={() => onDevice(deviceId)}
                className="inline-flex h-7 items-center gap-1.5 rounded-md border bg-card px-2 text-xs font-medium outline-none hover:border-input-hover hover:bg-accent focus-visible:ring-3 focus-visible:ring-ring-soft"
              >
                {deviceNames.get(deviceId) ?? '未登记设备'}
                <span className="font-mono text-subtle-foreground">{count}</span>
              </button>
            ))}
          </div>
        ) : null}
      </SummaryCell>
    </Panel>
  );
}

export function AlarmsWorkspace() {
  const { site, principal } = siteRoute.useRouteContext();
  const search = pageRoute.useSearch();
  const navigate = useNavigate({ from: '/operations/alarms' });
  const view = search.view ?? 'active';
  const severities = useMemo(() => search.severity ?? [], [search.severity]);
  const deviceNames = useDeviceNames(site.id);
  const myId = principal.principalId;
  const now = useNow(30_000);
  const { acknowledge } = useAlarmActions();
  const [searchDraft, setSearchDraft] = useState(search.q ?? '');
  const [creatingWorkOrder, setCreatingWorkOrder] = useState(false);

  const active = useQuery(activeAlarmsQuery(site.id));
  const history = useQuery(alarmHistoryQuery(site.id));
  const filter = VIEWS.find((candidate) => candidate.value === view)!.filter;
  const list = useInfiniteQuery({
    queryKey: alarmKeys.list(site.id, filter),
    queryFn: ({ pageParam, signal }) => listAlarms(site.id, filter, pageParam, signal),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (page) => (page.hasMore ? page.nextCursor ?? undefined : undefined),
    refetchInterval: REFRESH_MS,
  });

  const setSearch = (patch: Partial<Omit<typeof search, 'site'>>) =>
    void navigate({ search: (previous) => ({ ...previous, ...patch }), replace: true });
  const commitQuery = useDebouncedCallback((q: string) => setSearch({ q: q || undefined }), 250);

  const loaded = useMemo(() => sortAlarmsForOperators(list.data?.pages.flatMap((page) => page.items) ?? []), [list.data]);
  const query = (search.q ?? '').trim().toLowerCase();
  const alarms = useMemo(() => loaded.filter((alarm) =>
    (severities.length === 0 || severities.includes(alarm.currentSeverity))
    && (!search.device || alarm.deviceId === search.device)
    && (!query || [alarm.title, alarm.summary, alarm.deviceId ? deviceNames.get(alarm.deviceId) : '站点']
      .some((text) => text?.toLowerCase().includes(query)))), [loaded, severities, search.device, query, deviceNames]);
  const groups = useMemo(() => groupAlarms(alarms), [alarms]);
  const ordered = useMemo(() => groups.flatMap((group) => group.alarms), [groups]);

  const activeItems = active.data?.items ?? [];
  const unacknowledged = activeItems.filter((alarm) => !alarm.acknowledgement);
  const days = history.data?.days ?? 14;
  const windowed = useMemo(() => (history.data ? alarmsInWindow(history.data.alarms, site.timezone, days, now) : undefined), [history.data, site.timezone, days, now]);
  const filtered = severities.length > 0 || Boolean(search.device) || Boolean(query);
  const selectedId = search.inspect ?? ordered[0]?.alarmId;
  const selected = ordered.find((alarm) => alarm.alarmId === selectedId);

  // Severity, device and text filters run over loaded rows, so a filtered view reads every page first.
  useEffect(() => {
    if (filtered && list.hasNextPage && !list.isFetchingNextPage) void list.fetchNextPage();
  }, [filtered, list]);

  // J / K move through the list, A acknowledges, C raises a work order; typing in a field is left alone.
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      const target = event.target as HTMLElement | null;
      if (event.metaKey || event.ctrlKey || event.altKey || target?.closest('input, textarea, [contenteditable], [role="dialog"]')) return;
      const index = ordered.findIndex((alarm) => alarm.alarmId === selectedId);
      const key = event.key.toLowerCase();
      if ((key === 'j' || key === 'k') && ordered.length > 0) {
        event.preventDefault();
        const next = ordered[Math.min(ordered.length - 1, Math.max(0, index + (key === 'j' ? 1 : -1)))];
        setSearch({ inspect: next.alarmId });
        document.getElementById(`alarm-${next.alarmId}`)?.scrollIntoView({ block: 'nearest' });
      } else if (key === 'a' && selected && !selected.acknowledgement && !acknowledge.isPending) {
        event.preventDefault();
        acknowledge.mutate({ alarm: selected, comment: '' });
      } else if (key === 'c' && selected) {
        event.preventDefault();
        setCreatingWorkOrder(true);
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  });

  const readingMore = filtered && list.hasNextPage;
  const counts: Partial<Record<AlarmView, number>> = active.data ? { active: activeItems.length, unack: unacknowledged.length } : {};

  return (
    <Main fixed>
      <PageHeader
        title="告警"
        description={site.displayName}
        actions={(
          <Button variant="outline" onClick={() => { void active.refetch(); void list.refetch(); void history.refetch(); }}>
            <RefreshCw data-icon="inline-start" />
            刷新
          </Button>
        )}
      />

      <AlarmSummary
        alarms={windowed}
        days={days}
        timeZone={site.timezone}
        now={now}
        deviceNames={deviceNames}
        onDevice={(device) => setSearch({ device, view: 'all', inspect: undefined })}
      />

      <Panel aria-label="告警分诊" className="grid h-[calc(100svh-300px)] min-h-[520px] grid-cols-[420px_minmax(0,1fr)]">
        <div className="flex min-h-0 flex-col border-r">
          <ToggleGroup
            type="single"
            value={view}
            onValueChange={(value) => { if (value) setSearch({ view: value as AlarmView, inspect: undefined }); }}
            aria-label="告警范围"
            className="h-11 w-full justify-start gap-5 rounded-none border-b border-divider px-4"
          >
            {VIEWS.map((option) => (
              <ToggleGroupItem
                key={option.value}
                value={option.value}
                className="h-full flex-none gap-1.5 rounded-none border-0 px-0 text-[13px] font-medium text-muted-foreground shadow-[inset_0_-2px_0_transparent] hover:bg-transparent hover:text-foreground data-[state=on]:bg-transparent data-[state=on]:text-foreground data-[state=on]:shadow-[inset_0_-2px_0_var(--primary)]"
              >
                {option.label}
                {counts[option.value] !== undefined ? <span className="font-mono text-[11px] text-subtle-foreground">{counts[option.value]}</span> : null}
              </ToggleGroupItem>
            ))}
          </ToggleGroup>

          <div role="toolbar" aria-label="筛选告警" className="flex flex-wrap items-center gap-2 border-b border-divider px-4 py-2.5">
            <InputGroup className="h-8 min-w-0 flex-1">
              <InputGroupAddon><Search /></InputGroupAddon>
              <InputGroupInput
                aria-label="搜索告警"
                placeholder="告警、说明或设备"
                value={searchDraft}
                onChange={(event) => {
                  setSearchDraft(event.target.value);
                  commitQuery(event.target.value.trim());
                }}
              />
            </InputGroup>
            <Faceted
              multiple
              value={severities}
              onValueChange={(value) => setSearch({ severity: value && value.length > 0 ? value as AlarmSeverity[] : undefined })}
            >
              <FacetedTrigger asChild>
                <Button variant="outline" className="border-dashed font-normal">
                  <FacetedBadgeList options={SEVERITY_ORDER.map((severity) => ({ label: SEVERITY_LABELS[severity], value: severity }))} placeholder="严重度" />
                </Button>
              </FacetedTrigger>
              <FacetedContent className="w-44">
                <FacetedList>
                  <FacetedEmpty>没有选项</FacetedEmpty>
                  <FacetedGroup>
                    {SEVERITY_ORDER.map((severity) => (
                      <FacetedItem key={severity} value={severity}>
                        <SeverityMark level={SEVERITY_LEVEL[severity]} label="" />
                        <span>{SEVERITY_LABELS[severity]}</span>
                      </FacetedItem>
                    ))}
                  </FacetedGroup>
                </FacetedList>
              </FacetedContent>
            </Faceted>
            {search.device ? (
              <Button variant="outline" size="sm" aria-label={`清除设备筛选：${deviceNames.get(search.device) ?? '未登记设备'}`} onClick={() => setSearch({ device: undefined })}>
                设备：{deviceNames.get(search.device) ?? '未登记设备'}
                <X data-icon="inline-end" />
              </Button>
            ) : null}
            {filtered ? (
              <Button variant="ghost" size="sm" onClick={() => { setSearchDraft(''); setSearch({ severity: undefined, device: undefined, q: undefined }); }}>
                清除筛选
              </Button>
            ) : null}
          </div>

          <div className="min-h-0 flex-1 overflow-y-auto">
            {list.isError ? (
              <p className="px-4 py-6 text-sm text-destructive-text">告警暂时读不到：{list.error.message}</p>
            ) : list.isPending || (readingMore && alarms.length === 0) ? (
              <div className="flex flex-col gap-2 p-4"><Skeleton className="h-14" /><Skeleton className="h-14" /><Skeleton className="h-14" /></div>
            ) : alarms.length === 0 ? (
              <div className="flex flex-col items-center gap-2 px-6 py-12 text-center">
                <span className="flex size-9 items-center justify-center rounded-xl bg-success-soft text-success-text"><CircleCheck className="size-[18px]" aria-hidden="true" /></span>
                <p className="text-sm font-semibold">{filtered ? '没有符合条件的告警' : view === 'active' ? '当前没有活动告警' : '没有告警'}</p>
                <p className="text-xs text-muted-foreground">
                  {filtered ? '调整筛选条件后再看' : windowed && windowed.length > 0 ? `近 ${days} 天触发 ${windowed.length} 条，已全部处理` : `近 ${days} 天没有触发告警`}
                </p>
              </div>
            ) : (
              <ul aria-label="告警记录">
                {groups.map((group) => (
                  <li key={group.label}>
                    <div className="flex h-[34px] items-center bg-muted px-4 text-xs font-medium text-muted-foreground">
                      {group.label}
                      <span className="ml-auto font-mono text-subtle-foreground">{group.alarms.length}</span>
                    </div>
                    <ul>
                      {group.alarms.map((alarm) => {
                        const isSelected = alarm.alarmId === selectedId;
                        return (
                          <li key={alarm.alarmId} id={`alarm-${alarm.alarmId}`}>
                            <button
                              type="button"
                              aria-current={isSelected ? 'true' : undefined}
                              onClick={() => setSearch({ inspect: alarm.alarmId })}
                              className={cn(
                                'relative grid w-full grid-cols-[12px_minmax(0,1fr)_auto] gap-x-2.5 gap-y-[3px] border-b border-divider px-4 pt-[11px] pb-3 text-left outline-none hover:bg-accent focus-visible:bg-accent',
                                isSelected && 'bg-primary-soft hover:bg-primary-soft before:absolute before:inset-y-0 before:left-0 before:w-0.5 before:bg-primary',
                              )}
                            >
                              <SeverityMark level={SEVERITY_LEVEL[alarm.currentSeverity]} label={SEVERITY_LABELS[alarm.currentSeverity]} className="row-span-2 mt-[5px]" />
                              <span className="truncate text-sm font-semibold">{alarm.title}</span>
                              <span className="text-xs whitespace-nowrap text-subtle-foreground" title={formatTime(alarm.firstOccurredAt, site.timezone)}>
                                {formatRelative(alarm.firstOccurredAt, now)}
                              </span>
                              <span className="col-span-2 col-start-2 truncate text-xs text-muted-foreground">
                                {alarm.deviceId ? deviceNames.get(alarm.deviceId) ?? '—' : '站点'}
                                {' · '}持续 {formatDuration(alarm.firstOccurredAt, alarm.clearedAt, now)}
                                {alarm.occurrenceCount > 1 ? ` · 发生 ${alarm.occurrenceCount} 次` : ''}
                                {' · '}{alarm.assigneeId ? personLabel(alarm.assigneeId, myId) : '未分配'}
                                {' · '}{alarm.condition === 'CLEARED' ? '已恢复' : alarm.acknowledgement ? '已确认' : '未确认'}
                              </span>
                            </button>
                          </li>
                        );
                      })}
                    </ul>
                  </li>
                ))}
              </ul>
            )}
            {readingMore && alarms.length > 0 ? <p className="px-4 py-3 text-xs text-muted-foreground">正在读取全部记录以完成筛选…</p> : null}
            {list.hasNextPage && !readingMore ? (
              <div className="flex justify-center p-3">
                <Button variant="outline" size="sm" disabled={list.isFetchingNextPage} onClick={() => void list.fetchNextPage()}>
                  {list.isFetchingNextPage ? '正在读取…' : '加载更早的告警'}
                </Button>
              </div>
            ) : null}
          </div>

          <div className="flex items-center gap-1.5 border-t border-divider px-4 py-2 text-[11px] text-subtle-foreground">
            <Kbd>J</Kbd><Kbd>K</Kbd> 上下切换
            <Kbd className="ml-2">A</Kbd> 确认
            <Kbd className="ml-2">C</Kbd> 创建工单
          </div>
        </div>

        <AlarmDetailPane
          alarmId={selected?.alarmId ?? search.inspect}
          empty={list.isSuccess && alarms.length === 0}
          deviceNames={deviceNames}
          creatingWorkOrder={creatingWorkOrder}
          onCreatingWorkOrderChange={setCreatingWorkOrder}
        />
      </Panel>
    </Main>
  );
}
