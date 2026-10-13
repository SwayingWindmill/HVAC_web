import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { getRouteApi, Link } from '@tanstack/react-router';
import { Check, ChevronRight, Inbox, Link2, RefreshCw } from 'lucide-react';
import { useMemo, useState } from 'react';
import { toast } from 'sonner';
import { markNotificationRead, notificationErrorMessage, type NotificationInboxItem } from '@/api/notifications';
import type { ShellSnapshot } from '@/app/shell-runtime';
import { PageHeader, PageMeta } from '@/blocks/page-header';
import { Panel } from '@/blocks/panel';
import { InfoTip } from '@/components/info-tip';
import { shellNotificationsQuery } from '@/components/layout/use-shell-notifications';
import { Main } from '@/components/layout/Main';
import { SeverityMark } from '@/components/severity-mark';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Skeleton } from '@/components/ui/skeleton';
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group';
import { alarmDetailQuery } from '@/features/alarms/alarm-api';
import { alarmStatusLabel, formatDuration, SEVERITY_LABELS, SEVERITY_LEVEL } from '@/features/alarms/alarm-presentation';
import { formatCompactTime, formatTime, personLabel } from '@/lib/operator-format';
import { cn } from '@/lib/utils';

const pageRoute = getRouteApi('/_app/notifications');

type View = 'unread' | 'all';

const BROWSER_TIME_ZONE = Intl.DateTimeFormat().resolvedOptions().timeZone;

const SOURCE_ACTION: Readonly<Record<NotificationInboxItem['sourceAction'], string>> = {
  CREATED: '告警触发',
  SEVERITY_CHANGED: '告警级别变化',
  ACKNOWLEDGED: '告警已确认',
  CLEARED: '告警已恢复',
};

const STATUS_LABEL: Readonly<Record<NotificationInboxItem['status'], string>> = { UNREAD: '未读', READ: '已读', ACKED: '已确认' };

/** The alarm a notification came from, as it stands now: the notification is a snapshot, the alarm moves on. */
function SourceAlarm({ item, myId }: { readonly item: NotificationInboxItem; readonly myId: string }) {
  const alarm = useQuery(alarmDetailQuery(item.siteId, item.alarmId));
  return (
    <section className="flex flex-col gap-2.5 px-6 py-4">
      <h3 className="text-sm font-semibold">来源告警当前状态</h3>
      {alarm.isPending ? <Skeleton className="h-16 rounded-lg" /> : alarm.isError ? (
        <p className="text-sm text-muted-foreground">来源告警暂时读不到</p>
      ) : (
        <Link
          to="/operations/alarms"
          search={{ site: item.siteId, view: 'all', inspect: item.alarmId }}
          className="grid grid-cols-[auto_minmax(0,1fr)_auto_auto] items-center gap-3 rounded-lg border px-4 py-3 outline-none hover:bg-accent focus-visible:ring-3 focus-visible:ring-ring-soft"
        >
          <span className="inline-flex items-center gap-1.5 text-xs font-medium">
            <SeverityMark level={SEVERITY_LEVEL[alarm.data.currentSeverity]} label="" />
            {SEVERITY_LABELS[alarm.data.currentSeverity]}
          </span>
          <span className="flex min-w-0 flex-col gap-0.5">
            <span className="truncate text-sm font-semibold">{alarm.data.title}</span>
            <span className="truncate text-xs text-muted-foreground">
              持续 {formatDuration(alarm.data.firstOccurredAt, alarm.data.clearedAt, Date.now())}
              {' · '}{alarm.data.assigneeId ? `${personLabel(alarm.data.assigneeId, myId)}负责` : '未分配'}
            </span>
          </span>
          <span className="inline-flex items-center gap-1.5 text-xs whitespace-nowrap">
            <span className={cn('size-2 rounded-full', alarm.data.condition === 'ACTIVE' ? 'bg-destructive' : 'bg-success')} aria-hidden="true" />
            {alarmStatusLabel(alarm.data)}
          </span>
          <ChevronRight className="size-4 text-muted-foreground" aria-hidden="true" />
        </Link>
      )}
    </section>
  );
}

function ReadingPane({ item, siteName, timeZone, marking, myId, onMarkRead }: {
  readonly item: NotificationInboxItem;
  readonly siteName: string;
  readonly timeZone: string;
  readonly marking: boolean;
  readonly myId: string;
  readonly onMarkRead: () => void;
}) {
  const copyLink = () => {
    void navigator.clipboard.writeText(window.location.href).then(() => toast.success('链接已复制'), () => toast.error('无法复制链接'));
  };
  return (
    <section role="region" aria-label="通知内容" className="flex min-h-0 min-w-0 flex-col overflow-y-auto">
      <header className="flex flex-col gap-2 border-b border-divider px-6 pt-[18px] pb-4">
        <div className="flex items-center gap-2.5 text-xs">
          <span className="inline-flex items-center gap-1.5 font-medium">
            <SeverityMark level={SEVERITY_LEVEL[item.severity]} label="" />
            {SEVERITY_LABELS[item.severity]}
          </span>
          <span className={cn('rounded-md px-1.5 leading-5', item.status === 'UNREAD' ? 'bg-primary-soft text-primary' : 'bg-secondary text-secondary-foreground')}>{STATUS_LABEL[item.status]}</span>
          <span className="truncate text-muted-foreground">· {SOURCE_ACTION[item.sourceAction]} · {siteName} · <span className="font-mono">{formatTime(item.createdAt, timeZone)}</span></span>
          <Button variant="ghost" size="icon-sm" className="ml-auto text-muted-foreground" aria-label="复制链接" onClick={copyLink}><Link2 className="size-3.5" /></Button>
        </div>
        <h2 className="text-xl leading-7 font-semibold tracking-[-0.015em]">{item.subject}</h2>
        {item.body ? <p className="text-sm leading-[22px] text-secondary-foreground">{item.body}</p> : null}
        <div className="mt-1 flex flex-wrap items-center gap-2">
          {item.status === 'UNREAD' ? (
            <Button size="sm" disabled={marking} onClick={onMarkRead}><Check data-icon="inline-start" />标记已读</Button>
          ) : null}
          <Button size="sm" variant="outline" asChild>
            <Link to="/operations/alarms" search={{ site: item.siteId, view: 'all', inspect: item.alarmId }}>查看这条告警 ›</Link>
          </Button>
          <InfoTip>标记已读不会改变告警或工单的状态</InfoTip>
        </div>
      </header>
      <SourceAlarm item={item} myId={myId} />
    </section>
  );
}

export function NotificationCenter({ snapshot }: { readonly snapshot: ShellSnapshot }) {
  const principal = snapshot.principal!;
  const search = pageRoute.useSearch();
  const navigate = pageRoute.useNavigate();
  const queryClient = useQueryClient();
  const inbox = useQuery(shellNotificationsQuery(principal.principal.subject));
  const [checked, setChecked] = useState<ReadonlySet<string>>(new Set());

  const siteById = useMemo(() => new Map((snapshot.sites?.items ?? []).map((site) => [site.id, site])), [snapshot.sites?.items]);
  const items = useMemo(() => [...(inbox.data ?? [])].sort((left, right) => Date.parse(right.createdAt) - Date.parse(left.createdAt)), [inbox.data]);
  // Each notification reads on its Site's clock, as the Site's own pages do.
  const timeZoneOf = (item: NotificationInboxItem) => siteById.get(item.siteId)?.timezone ?? BROWSER_TIME_ZONE;
  const unread = items.filter((item) => item.status === 'UNREAD');
  const view: View = search.view ?? 'unread';
  const groups = view === 'unread'
    ? [{ label: '未读', items: unread }]
    : [{ label: '未读', items: unread }, { label: '更早 · 已读', items: items.filter((item) => item.status !== 'UNREAD') }].filter((group) => group.items.length > 0);
  const shown = groups.flatMap((group) => group.items);
  const selected = items.find((item) => item.inboxItemId === search.inspect) ?? shown[0];
  const setSearch = (next: { view?: View; inspect?: string }) => void navigate({ search: (previous) => ({ ...previous, ...next }), replace: true });

  const markRead = useMutation({
    mutationFn: (ids: readonly string[]) => Promise.all(ids.map((id) => markNotificationRead(id, principal.session.csrfToken))),
    onSuccess: (updated) => {
      queryClient.setQueryData<NotificationInboxItem[]>(shellNotificationsQuery(principal.principal.subject).queryKey, (current) =>
        current?.map((item) => updated.find((candidate) => candidate.inboxItemId === item.inboxItemId) ?? item));
      setChecked(new Set());
      toast.success(updated.length > 1 ? `已将 ${updated.length} 条标记为已读` : '已标记为已读');
    },
    onError: (error) => {
      toast.error(notificationErrorMessage(error));
      void inbox.refetch();
    },
  });

  const checkable = shown.filter((item) => item.status === 'UNREAD');
  const allChecked = checkable.length > 0 && checkable.every((item) => checked.has(item.inboxItemId));
  const toggle = (id: string, on: boolean) => setChecked((current) => {
    const next = new Set(current);
    if (on) next.add(id); else next.delete(id);
    return next;
  });

  return (
    <Main fixed>
      <PageHeader
        title="通知中心"
        description="全部授权站点"
        meta={inbox.isSuccess ? <PageMeta dot={unread.length > 0 ? 'warning' : 'idle'}>{unread.length} 条未读</PageMeta> : undefined}
        actions={(
          <Button variant="outline" size="sm" onClick={() => void inbox.refetch()} disabled={inbox.isFetching}>
            <RefreshCw data-icon="inline-start" className={inbox.isFetching ? 'animate-spin' : undefined} />
            刷新
          </Button>
        )}
      />

      <Panel aria-label="通知" className="grid h-[calc(100svh-180px)] min-h-[520px] grid-cols-[420px_minmax(0,1fr)]">
        <div className="flex min-h-0 flex-col border-r">
          <ToggleGroup
            type="single"
            value={view}
            onValueChange={(value) => { if (value) { setChecked(new Set()); setSearch({ view: value === 'unread' ? undefined : value as View, inspect: undefined }); } }}
            aria-label="通知范围"
            className="h-11 w-full shrink-0 justify-start gap-5 rounded-none border-b border-divider px-4"
          >
            {([['unread', '未读', unread.length], ['all', '全部', items.length]] as const).map(([value, label, count]) => (
              <ToggleGroupItem
                key={value}
                value={value}
                className="h-full flex-none gap-1.5 rounded-none border-0 px-0 text-[13px] font-medium text-muted-foreground shadow-[inset_0_-2px_0_transparent] hover:bg-transparent hover:text-foreground data-[state=on]:bg-transparent data-[state=on]:text-foreground data-[state=on]:shadow-[inset_0_-2px_0_var(--primary)]"
              >
                {label}
                {inbox.isSuccess ? <span className="font-mono text-[11px] text-subtle-foreground">{count}</span> : null}
              </ToggleGroupItem>
            ))}
          </ToggleGroup>

          {checkable.length > 0 ? (
            <div role="toolbar" aria-label="批量操作" className="flex h-11 shrink-0 items-center gap-3 border-b border-divider px-4">
              <Checkbox
                checked={allChecked ? true : checked.size > 0 ? 'indeterminate' : false}
                onCheckedChange={(on) => setChecked(on ? new Set(checkable.map((item) => item.inboxItemId)) : new Set())}
                aria-label="全选未读通知"
              />
              <span className="text-xs text-muted-foreground">{checked.size > 0 ? `已选 ${checked.size} 条` : '全选'}</span>
              {checked.size > 0 ? (
                <Button size="sm" variant="outline" className="ml-auto" disabled={markRead.isPending} onClick={() => markRead.mutate([...checked])}>
                  <Check data-icon="inline-start" />
                  标记已读
                </Button>
              ) : null}
            </div>
          ) : null}

          <div className="min-h-0 flex-1 overflow-y-auto">
            {inbox.isPending ? (
              <div className="flex flex-col gap-2 p-4"><Skeleton className="h-14" /><Skeleton className="h-14" /><Skeleton className="h-14" /></div>
            ) : inbox.isError ? (
              <div className="flex flex-col items-start gap-2 px-4 py-6">
                <p className="text-sm text-destructive-text">通知暂时读不到：{notificationErrorMessage(inbox.error)}</p>
                <Button variant="outline" size="sm" onClick={() => void inbox.refetch()}>重试</Button>
              </div>
            ) : shown.length === 0 ? (
              <div className="flex flex-col items-center gap-2 px-6 py-12 text-center">
                <span className="flex size-9 items-center justify-center rounded-xl bg-success-soft text-success-text"><Inbox className="size-[18px]" aria-hidden="true" /></span>
                <p className="text-sm font-semibold">{view === 'unread' ? '没有未读通知' : '还没有通知'}</p>
                <p className="text-xs text-muted-foreground">{view === 'unread' && items.length > 0 ? `已读的 ${items.length} 条在「全部」里` : '告警触发或变化时会通知你'}</p>
              </div>
            ) : (
              <ul aria-label="通知列表">
                {groups.map((group) => (
                  <li key={group.label}>
                    <div className="flex h-[34px] items-center bg-muted px-4 text-xs font-medium text-muted-foreground">
                      {group.label}
                      <span className="ml-auto font-mono text-subtle-foreground">{group.items.length}</span>
                    </div>
                    <ul>
                      {group.items.map((item) => {
                        const isSelected = item.inboxItemId === selected?.inboxItemId;
                        const isUnread = item.status === 'UNREAD';
                        return (
                          <li
                            key={item.inboxItemId}
                            className={cn(
                              'relative grid grid-cols-[16px_12px_minmax(0,1fr)_auto] items-start gap-x-2.5 gap-y-[3px] border-b border-divider px-4 pt-[11px] pb-3 hover:bg-accent',
                              isSelected && 'bg-primary-soft hover:bg-primary-soft before:absolute before:inset-y-0 before:left-0 before:w-0.5 before:bg-primary',
                            )}
                          >
                            {isUnread ? (
                              <Checkbox
                                className="relative z-10 row-span-2 mt-0.5"
                                checked={checked.has(item.inboxItemId)}
                                onCheckedChange={(on) => toggle(item.inboxItemId, Boolean(on))}
                                aria-label={`选择 ${item.subject}`}
                              />
                            ) : <span className="row-span-2" />}
                            <span className="row-span-2 mt-[5px]"><SeverityMark level={SEVERITY_LEVEL[item.severity]} label={SEVERITY_LABELS[item.severity]} /></span>
                            <button
                              type="button"
                              aria-current={isSelected ? 'true' : undefined}
                              onClick={() => setSearch({ inspect: item.inboxItemId })}
                              className={cn('truncate text-left text-sm outline-none after:absolute after:inset-0 after:content-[\'\'] focus-visible:underline', isUnread ? 'font-semibold' : 'font-medium text-secondary-foreground')}
                            >
                              {item.subject}
                            </button>
                            <span className="font-mono text-xs whitespace-nowrap text-subtle-foreground">{formatCompactTime(item.createdAt, timeZoneOf(item))}</span>
                            <span className="col-span-2 col-start-3 truncate text-xs text-muted-foreground">
                              {isUnread ? <span className="mr-1.5 inline-block size-1.5 rounded-full bg-primary align-middle" aria-label="未读" /> : null}
                              {SEVERITY_LABELS[item.severity]} · {siteById.get(item.siteId)?.displayName ?? '站点信息不可用'}
                              {item.body ? ` · ${item.body}` : ''}
                            </span>
                          </li>
                        );
                      })}
                    </ul>
                  </li>
                ))}
              </ul>
            )}
          </div>
        </div>

        {selected ? (
          <ReadingPane
            key={selected.inboxItemId}
            item={selected}
            siteName={siteById.get(selected.siteId)?.displayName ?? '站点信息不可用'}
            timeZone={timeZoneOf(selected)}
            marking={markRead.isPending}
            myId={principal.principalId}
            // Keep the notice open once read, even where the unread list no longer holds it.
            onMarkRead={() => { setSearch({ inspect: selected.inboxItemId }); markRead.mutate([selected.inboxItemId]); }}
          />
        ) : (
          <div className="flex items-center justify-center p-10 text-sm text-muted-foreground">
            {inbox.isPending ? null : items.length === 0 ? '没有可查看的通知' : '从左侧选择一条通知'}
          </div>
        )}
      </Panel>
    </Main>
  );
}
