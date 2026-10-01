import { useMemo } from 'react';
import { useInfiniteQuery, useQuery } from '@tanstack/react-query';
import { getRouteApi, useNavigate } from '@tanstack/react-router';
import { ChevronRight, RefreshCw } from 'lucide-react';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Card, CardContent } from '@/components/ui/card';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Skeleton } from '@/components/ui/skeleton';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { MetricStrip, WorkspaceHeader } from '@/components/analysis/workspace-parts';
import { useDeviceNames } from '@/features/assets/use-device-names';
import { cn } from '@/lib/utils';
import { alarmKeys, listAlarms, type Alarm, type AlarmListFilter, type AlarmSeverity } from '../alarm-api';
import {
  alarmStatusLabel,
  formatDuration,
  SEVERITY_CLASSES,
  SEVERITY_LABELS,
  SEVERITY_ORDER,
  severityRank,
} from '../alarm-presentation';
import { formatTime, personLabel } from '@/lib/operator-format';
import { AlarmInspector } from './AlarmInspector';

export type AlarmView = 'active' | 'unack' | 'cleared' | 'all';

const VIEW_LABELS: Readonly<Record<AlarmView, string>> = {
  active: '活动',
  unack: '未确认',
  cleared: '已恢复',
  all: '全部',
};

const VIEW_FILTERS: Readonly<Record<AlarmView, AlarmListFilter>> = {
  active: { condition: 'ACTIVE' },
  unack: { acknowledged: false },
  cleared: { condition: 'CLEARED' },
  all: {},
};

// The Alarm owner has no stream yet; lists follow it on a short interval.
const REFRESH_MS = 15_000;

const siteRoute = getRouteApi('/_app/_site');
const pageRoute = getRouteApi('/_app/_site/operations/alarms');

export function SeverityBadge({ severity }: { readonly severity: AlarmSeverity }) {
  return (
    <span className={cn('inline-flex items-center rounded-md border px-1.5 py-0.5 text-xs font-medium', SEVERITY_CLASSES[severity])}>
      {SEVERITY_LABELS[severity]}
    </span>
  );
}

export function AlarmsWorkspace() {
  const { site, principal } = siteRoute.useRouteContext();
  const search = pageRoute.useSearch();
  const navigate = useNavigate({ from: '/operations/alarms' });
  const view = search.view ?? 'active';
  const filter = { ...VIEW_FILTERS[view], severity: search.severity };
  const deviceNames = useDeviceNames(site.id);
  const myId = principal.principalId;
  const now = Date.now();

  const active = useQuery({
    queryKey: alarmKeys.activeSummary(site.id),
    queryFn: ({ signal }) => listAlarms(site.id, VIEW_FILTERS.active, undefined, signal),
    refetchInterval: REFRESH_MS,
  });
  const list = useInfiniteQuery({
    queryKey: alarmKeys.list(site.id, filter),
    queryFn: ({ pageParam, signal }) => listAlarms(site.id, filter, pageParam, signal),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (page) => (page.hasMore ? page.nextCursor ?? undefined : undefined),
    refetchInterval: REFRESH_MS,
  });

  const alarms = useMemo(() => {
    const items = list.data?.pages.flatMap((page) => page.items) ?? [];
    // Most severe first, then the most recent.
    return [...items].sort((left, right) => severityRank(left.currentSeverity) - severityRank(right.currentSeverity)
      || Date.parse(right.lastOccurredAt) - Date.parse(left.lastOccurredAt));
  }, [list.data]);

  const activeItems = active.data?.items ?? [];
  const setSearch = (patch: { view?: AlarmView; severity?: AlarmSeverity; inspect?: string }) =>
    void navigate({ search: (previous) => ({ ...previous, ...patch }) });

  return (
    <main className="mx-auto max-w-[1600px] space-y-5 p-6">
      <WorkspaceHeader
        title="异常与告警"
        actions={(
          <Button variant="outline" size="sm" onClick={() => { void active.refetch(); void list.refetch(); }}>
            <RefreshCw aria-hidden="true" data-icon="inline-start" />刷新
          </Button>
        )}
      />

      <MetricStrip
        items={[
          { label: '活动告警', value: active.data ? activeItems.length : '—', unit: '条' },
          { label: '未确认', value: active.data ? activeItems.filter((alarm) => !alarm.acknowledgement).length : '—', unit: '条' },
          {
            label: '紧急与重要',
            value: active.data ? activeItems.filter((alarm) => alarm.currentSeverity === 'CRITICAL' || alarm.currentSeverity === 'MAJOR').length : '—',
            unit: '条',
          },
          { label: '无人认领', value: active.data ? activeItems.filter((alarm) => !alarm.assigneeId).length : '—', unit: '条' },
        ]}
      />

      <div className="flex flex-wrap items-center gap-3">
        <Tabs value={view} onValueChange={(value) => setSearch({ view: value as AlarmView })}>
          <TabsList>
            {(Object.keys(VIEW_LABELS) as AlarmView[]).map((key) => (
              <TabsTrigger key={key} value={key}>{VIEW_LABELS[key]}</TabsTrigger>
            ))}
          </TabsList>
        </Tabs>
        <Select
          value={search.severity ?? 'ALL'}
          onValueChange={(value) => setSearch({ severity: value === 'ALL' ? undefined : value as AlarmSeverity })}
        >
          <SelectTrigger className="w-32" aria-label="严重度"><SelectValue /></SelectTrigger>
          <SelectContent>
            <SelectItem value="ALL">全部严重度</SelectItem>
            {SEVERITY_ORDER.map((severity) => <SelectItem key={severity} value={severity}>{SEVERITY_LABELS[severity]}</SelectItem>)}
          </SelectContent>
        </Select>
      </div>

      {list.isError ? (
        <Alert variant="destructive">
          <AlertTitle>告警暂不可读</AlertTitle>
          <AlertDescription>{list.error.message}</AlertDescription>
        </Alert>
      ) : null}

      <Card>
        <CardContent className="p-0">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="w-20 pl-4">严重度</TableHead>
                <TableHead>告警</TableHead>
                <TableHead className="w-36">设备</TableHead>
                <TableHead className="w-32">状态</TableHead>
                <TableHead className="w-28">开始</TableHead>
                <TableHead className="w-28">持续</TableHead>
                <TableHead className="w-16 text-right">次数</TableHead>
                <TableHead className="w-20">负责人</TableHead>
                <TableHead className="w-8" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {list.isPending ? (
                <TableRow><TableCell colSpan={9}><Skeleton className="h-24" /></TableCell></TableRow>
              ) : alarms.length === 0 ? (
                <TableRow>
                  <TableCell colSpan={9} className="py-12 text-center text-sm text-muted-foreground">
                    {view === 'active' ? '当前没有活动告警' : '没有符合条件的告警'}
                  </TableCell>
                </TableRow>
              ) : alarms.map((alarm: Alarm) => (
                <TableRow
                  key={alarm.alarmId}
                  className="cursor-pointer"
                  data-state={search.inspect === alarm.alarmId ? 'selected' : undefined}
                  onClick={() => setSearch({ inspect: alarm.alarmId })}
                >
                  <TableCell className="pl-4"><SeverityBadge severity={alarm.currentSeverity} /></TableCell>
                  <TableCell>
                    <p className="font-medium">{alarm.title}</p>
                    <p className="line-clamp-1 text-xs text-muted-foreground">{alarm.summary}</p>
                  </TableCell>
                  <TableCell>{alarm.deviceId ? deviceNames.get(alarm.deviceId) ?? '—' : '站点'}</TableCell>
                  <TableCell className={cn(alarm.condition === 'ACTIVE' && !alarm.acknowledgement && 'font-medium text-foreground', alarm.condition === 'CLEARED' && 'text-muted-foreground')}>
                    {alarmStatusLabel(alarm)}
                  </TableCell>
                  <TableCell className="tabular-nums">{formatTime(alarm.firstOccurredAt, site.timezone)}</TableCell>
                  <TableCell className="tabular-nums">{formatDuration(alarm.firstOccurredAt, alarm.clearedAt, now)}</TableCell>
                  <TableCell className="text-right tabular-nums">{alarm.occurrenceCount}</TableCell>
                  <TableCell>{personLabel(alarm.assigneeId, myId)}</TableCell>
                  <TableCell><ChevronRight className="size-4 text-muted-foreground" aria-hidden="true" /></TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </CardContent>
      </Card>

      {list.hasNextPage ? (
        <div className="flex justify-center">
          <Button variant="outline" size="sm" disabled={list.isFetchingNextPage} onClick={() => void list.fetchNextPage()}>
            加载更多
          </Button>
        </div>
      ) : null}

      <AlarmInspector
        alarmId={search.inspect}
        deviceNames={deviceNames}
        onClose={() => setSearch({ inspect: undefined })}
      />
    </main>
  );
}
