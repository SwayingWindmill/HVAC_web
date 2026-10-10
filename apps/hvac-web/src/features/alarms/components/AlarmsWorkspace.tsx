import { useMemo } from 'react';
import { useInfiniteQuery, useQuery } from '@tanstack/react-query';
import { getRouteApi, useNavigate } from '@tanstack/react-router';
import type { ColumnDef } from '@tanstack/react-table';
import { CircleCheck, Ellipsis, RefreshCw, Search, X } from 'lucide-react';
import { Bar, BarChart, CartesianGrid, XAxis, YAxis } from 'recharts';
import { DataTableBlock } from '@/blocks/data-table';
import { MetricCard, MetricGrid, MetricValue } from '@/blocks/metric-card';
import { PageHeader } from '@/blocks/page-header';
import { DataTable } from '@/components/data-table/data-table';
import { DataTableAdvancedToolbar } from '@/components/data-table/data-table-advanced-toolbar';
import { DataTableColumnHeader } from '@/components/data-table/data-table-column-header';
import type { DataTableFeatures } from '@/components/data-table/data-table-features';
import { Main } from '@/components/layout/Main';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { BarList } from '@/components/ui/bar-list';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { ChartContainer, ChartLegend, ChartLegendContent, ChartTooltip, ChartTooltipContent } from '@/components/ui/chart';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from '@/components/ui/empty';
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
import { Skeleton } from '@/components/ui/skeleton';
import { Status, StatusIndicator, StatusLabel } from '@/components/ui/status';
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { useDeviceNames } from '@/features/assets/use-device-names';
import { useDataTable } from '@/hooks/use-data-table';
import { formatRelative, formatTime, personLabel } from '@/lib/operator-format';
import {
  activeAlarmsQuery,
  alarmHistoryQuery,
  alarmKeys,
  listAlarms,
  type Alarm,
  type AlarmListFilter,
  type AlarmSeverity,
} from '../alarm-api';
import { alarmDailyCounts, alarmsByDevice, meanTimeToClear } from '../alarm-history';
import {
  alarmStatusLabel,
  formatDuration,
  formatSpan,
  SEVERITY_COLORS,
  SEVERITY_LABELS,
  SEVERITY_ORDER,
  SeverityBadge,
  severityRank,
  sortAlarmsForOperators,
} from '../alarm-presentation';
import { useAlarmActions } from '../use-alarm-actions';
import { AlarmInspector } from './AlarmInspector';

const siteRoute = getRouteApi('/_app/_site');
const pageRoute = getRouteApi('/_app/_site/operations/alarms');

export type AlarmView = 'active' | 'unack' | 'cleared' | 'all';

const VIEWS: readonly { readonly value: AlarmView; readonly label: string; readonly filter: AlarmListFilter }[] = [
  { value: 'active', label: '活动', filter: { condition: 'ACTIVE' } },
  { value: 'unack', label: '未确认', filter: { acknowledged: false } },
  { value: 'cleared', label: '已恢复', filter: { condition: 'CLEARED' } },
  { value: 'all', label: '全部', filter: {} },
];

// The Alarm owner has no stream yet; lists follow it on a short interval.
const REFRESH_MS = 15_000;

function AlarmStatus({ alarm }: { readonly alarm: Alarm }) {
  const variant = alarm.condition === 'CLEARED' ? 'success' : alarm.acknowledgement ? 'warning' : 'error';
  return (
    <Status variant={variant}>
      <StatusIndicator className={alarm.condition === 'CLEARED' ? 'before:hidden' : undefined} />
      <StatusLabel>{alarmStatusLabel(alarm)}</StatusLabel>
    </Status>
  );
}

function RowActions({ alarm, onInspect }: { readonly alarm: Alarm; readonly onInspect: () => void }) {
  const { acknowledge, claim, canClaim, myId } = useAlarmActions();
  return (
    <DropdownMenu modal={false}>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" size="icon" className="size-7" aria-label={`${alarm.title} 的操作`} onClick={(event) => event.stopPropagation()}>
          <Ellipsis />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-40" onClick={(event) => event.stopPropagation()}>
        <DropdownMenuGroup>
          <DropdownMenuItem onSelect={onInspect}>查看详情</DropdownMenuItem>
        </DropdownMenuGroup>
        <DropdownMenuSeparator />
        <DropdownMenuGroup>
          <DropdownMenuItem disabled={Boolean(alarm.acknowledgement) || acknowledge.isPending} onSelect={() => acknowledge.mutate({ alarm, comment: '' })}>
            确认告警
          </DropdownMenuItem>
          <DropdownMenuItem disabled={!canClaim || alarm.assigneeId === myId || claim.isPending} onSelect={() => claim.mutate(alarm)}>
            由我处理
          </DropdownMenuItem>
        </DropdownMenuGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

const trendConfig = Object.fromEntries(SEVERITY_ORDER.map((severity) => [severity, { label: SEVERITY_LABELS[severity], color: SEVERITY_COLORS[severity] }]));

function AlarmTrend({ alarms, complete, days, timeZone }: {
  readonly alarms: readonly Alarm[] | undefined;
  readonly complete: boolean;
  readonly days: number;
  readonly timeZone: string;
}) {
  const rows = useMemo(() => (alarms ? alarmDailyCounts(alarms, timeZone, days, Date.now()) : []), [alarms, timeZone, days]);
  return (
    <Card className="min-w-0">
      <CardHeader>
        <CardTitle>近 {days} 天告警</CardTitle>
        <CardDescription>每天新触发的告警，按触发时的严重度{complete ? '' : '；只统计了最近 1000 条'}</CardDescription>
      </CardHeader>
      <CardContent>
        {!alarms ? <Skeleton className="h-[220px]" /> : alarms.length === 0 ? (
          <Empty className="h-[220px]">
            <EmptyHeader>
              <EmptyMedia variant="icon"><CircleCheck /></EmptyMedia>
              <EmptyTitle>近 {days} 天没有告警</EmptyTitle>
              <EmptyDescription>规则触发的告警会按天出现在这里。</EmptyDescription>
            </EmptyHeader>
          </Empty>
        ) : (
          <ChartContainer config={trendConfig} className="aspect-auto h-[220px] w-full">
            <BarChart data={rows} margin={{ left: 0, right: 4 }}>
              <CartesianGrid vertical={false} />
              <XAxis dataKey="label" tickLine={false} axisLine={false} tickMargin={8} minTickGap={16} />
              <YAxis allowDecimals={false} tickLine={false} axisLine={false} width={28} />
              <ChartTooltip cursor={false} content={<ChartTooltipContent indicator="dot" />} />
              {SEVERITY_ORDER.map((severity, index, list) => (
                <Bar
                  key={severity}
                  dataKey={severity}
                  stackId="severity"
                  fill={`var(--color-${severity})`}
                  radius={index === list.length - 1 ? [3, 3, 0, 0] : 0}
                  maxBarSize={28}
                  isAnimationActive={false}
                />
              ))}
              <ChartLegend content={<ChartLegendContent />} itemSorter={(item) => SEVERITY_ORDER.indexOf(item.dataKey as AlarmSeverity)} />
            </BarChart>
          </ChartContainer>
        )}
      </CardContent>
    </Card>
  );
}

function NoisyDevices({ alarms, deviceNames, days, onSelect }: {
  readonly alarms: readonly Alarm[] | undefined;
  readonly deviceNames: ReadonlyMap<string, string>;
  readonly days: number;
  readonly onSelect: (deviceId: string) => void;
}) {
  const devices = useMemo(() => (alarms ? alarmsByDevice(alarms).slice(0, 6) : []), [alarms]);
  return (
    <Card className="min-w-0">
      <CardHeader>
        <CardTitle>告警最多的设备</CardTitle>
        <CardDescription>近 {days} 天，点击查看该设备的告警</CardDescription>
      </CardHeader>
      <CardContent>
        {!alarms ? <Skeleton className="h-[220px]" /> : devices.length === 0 ? (
          <p className="grid h-[220px] place-items-center text-sm text-muted-foreground">
            {alarms.length > 0 ? `近 ${days} 天的告警都是站点级，不针对具体设备` : `近 ${days} 天没有设备告警`}
          </p>
        ) : (
          <BarList
            data={devices.map(({ deviceId, count }) => ({ key: deviceId, name: deviceNames.get(deviceId) ?? '未登记设备', value: count }))}
            valueFormatter={(value) => `${value} 条`}
            onItemClick={(item) => onSelect(item.key!)}
          />
        )}
      </CardContent>
    </Card>
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
  const now = Date.now();

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

  const setSearch = (patch: { view?: AlarmView; severity?: AlarmSeverity[]; device?: string; q?: string; inspect?: string }) =>
    void navigate({ search: (previous) => ({ ...previous, ...patch }), replace: true });

  const loaded = useMemo(() => sortAlarmsForOperators(list.data?.pages.flatMap((page) => page.items) ?? []), [list.data]);
  const query = (search.q ?? '').trim().toLowerCase();
  const alarms = useMemo(() => loaded.filter((alarm) =>
    (severities.length === 0 || severities.includes(alarm.currentSeverity))
    && (!search.device || alarm.deviceId === search.device)
    && (!query || [alarm.title, alarm.summary, alarm.deviceId ? deviceNames.get(alarm.deviceId) : '站点']
      .some((text) => text?.toLowerCase().includes(query)))), [loaded, severities, search.device, query, deviceNames]);

  const activeItems = active.data?.items ?? [];
  const unacknowledged = activeItems.filter((alarm) => !alarm.acknowledgement);
  const oldestUnacknowledged = unacknowledged.reduce<Alarm | null>((oldest, alarm) =>
    !oldest || Date.parse(alarm.firstOccurredAt) < Date.parse(oldest.firstOccurredAt) ? alarm : oldest, null);
  const highest = sortAlarmsForOperators(activeItems)[0];
  const mttr = history.data ? meanTimeToClear(history.data.alarms) : null;
  const cleared = history.data?.alarms.filter((alarm) => alarm.condition === 'CLEARED').length ?? 0;
  const days = history.data?.days ?? 14;

  const columns = useMemo<ColumnDef<DataTableFeatures, Alarm>[]>(() => [
    {
      id: 'severity',
      accessorFn: (alarm) => severityRank(alarm.currentSeverity),
      header: ({ column }) => <DataTableColumnHeader column={column} title="严重度" />,
      cell: ({ row }) => <SeverityBadge severity={row.original.currentSeverity} />,
    },
    {
      id: 'alarm',
      header: '告警',
      enableSorting: false,
      cell: ({ row }) => (
        <div className="min-w-0 max-w-[28rem]">
          <p className="truncate font-medium">{row.original.title}</p>
          <p className="truncate text-xs text-muted-foreground">{row.original.summary}</p>
        </div>
      ),
    },
    {
      id: 'device',
      accessorFn: (alarm) => (alarm.deviceId ? deviceNames.get(alarm.deviceId) ?? '' : '站点'),
      header: ({ column }) => <DataTableColumnHeader column={column} title="设备" />,
      cell: ({ getValue }) => <span className="whitespace-nowrap">{(getValue() as string) || '—'}</span>,
    },
    { id: 'status', header: '状态', enableSorting: false, cell: ({ row }) => <AlarmStatus alarm={row.original} /> },
    {
      id: 'started',
      accessorFn: (alarm) => Date.parse(alarm.firstOccurredAt),
      header: ({ column }) => <DataTableColumnHeader column={column} title="开始" />,
      cell: ({ row }) => (
        <Tooltip>
          <TooltipTrigger asChild>
            <span className="whitespace-nowrap tabular-nums">{formatRelative(row.original.firstOccurredAt, now)}</span>
          </TooltipTrigger>
          <TooltipContent>{formatTime(row.original.firstOccurredAt, site.timezone)}</TooltipContent>
        </Tooltip>
      ),
    },
    {
      id: 'duration',
      header: '持续',
      enableSorting: false,
      cell: ({ row }) => <span className="whitespace-nowrap tabular-nums">{formatDuration(row.original.firstOccurredAt, row.original.clearedAt, now)}</span>,
    },
    {
      id: 'count',
      accessorFn: (alarm) => alarm.occurrenceCount,
      header: ({ column }) => <DataTableColumnHeader column={column} title="次数" />,
      cell: ({ getValue }) => <span className="tabular-nums">{getValue() as number}</span>,
    },
    {
      id: 'assignee',
      header: '负责人',
      enableSorting: false,
      cell: ({ row }) => (row.original.assigneeId
        ? <span className="whitespace-nowrap">{personLabel(row.original.assigneeId, myId)}</span>
        : <span className="whitespace-nowrap text-muted-foreground">未认领</span>),
    },
    {
      id: 'actions',
      header: () => <span className="sr-only">操作</span>,
      enableSorting: false,
      enableHiding: false,
      cell: ({ row }) => <RowActions alarm={row.original} onInspect={() => setSearch({ inspect: row.original.alarmId })} />,
    },
  // eslint-disable-next-line react-hooks/exhaustive-deps
  ], [deviceNames, myId, site.timezone, now]);

  const table = useDataTable({ key: 'alarm-ledger', data: alarms, columns, paginate: false, getRowId: (alarm) => alarm.alarmId });
  const filtered = severities.length > 0 || Boolean(search.device) || Boolean(query);

  return (
    <Main className="@container/main flex flex-col gap-4 md:gap-6">
      <PageHeader
        title="异常与告警"
        description={`${site.displayName} · 严重度优先，其次最近发生`}
        actions={(
          <Button variant="outline" size="sm" onClick={() => { void active.refetch(); void list.refetch(); void history.refetch(); }}>
            <RefreshCw data-icon="inline-start" />
            刷新
          </Button>
        )}
      />

      <MetricGrid ariaLabel="告警概况">
        <MetricCard
          label="活动告警"
          value={active.data ? <MetricValue value={String(activeItems.length)} unit="条" /> : <Skeleton className="h-8 w-16" />}
          badge={highest ? <SeverityBadge severity={highest.currentSeverity} /> : undefined}
          lead={highest ? `最高：${highest.title}` : '没有活动告警'}
          detail="工况恢复正常后自动恢复"
        />
        <MetricCard
          label="未确认"
          value={active.data ? <MetricValue value={String(unacknowledged.length)} unit="条" /> : <Skeleton className="h-8 w-16" />}
          lead={oldestUnacknowledged ? `最久一条已持续 ${formatDuration(oldestUnacknowledged.firstOccurredAt, undefined, now)}` : '全部已有人跟进'}
          detail="确认表示已有人跟进"
        />
        <MetricCard
          label="无人认领"
          value={active.data ? <MetricValue value={String(activeItems.filter((alarm) => !alarm.assigneeId).length)} unit="条" /> : <Skeleton className="h-8 w-16" />}
          lead={`其中 ${activeItems.filter((alarm) => !alarm.assigneeId && !alarm.acknowledgement).length} 条未确认`}
          detail="认领后由负责人跟进到恢复"
        />
        <MetricCard
          label="平均恢复时长"
          value={history.data ? <MetricValue value={mttr === null ? null : formatSpan(mttr)} /> : <Skeleton className="h-8 w-24" />}
          lead={history.data ? `近 ${days} 天触发 ${history.data.alarms.length} 条，已恢复 ${cleared} 条` : '正在读取告警历史'}
          detail="从触发到工况恢复"
        />
      </MetricGrid>

      <div className="grid gap-4 md:gap-6 @5xl/main:grid-cols-[minmax(0,2fr)_minmax(0,1fr)]">
        <AlarmTrend alarms={history.data?.alarms} complete={history.data?.complete ?? true} days={days} timeZone={site.timezone} />
        <NoisyDevices alarms={history.data?.alarms} deviceNames={deviceNames} days={days} onSelect={(device) => setSearch({ device, view: 'all' })} />
      </div>

      {list.isError ? (
        <Alert variant="destructive">
          <AlertTitle>告警暂不可读</AlertTitle>
          <AlertDescription>{list.error.message}</AlertDescription>
        </Alert>
      ) : null}

      <DataTableBlock title="告警记录" description={list.data ? `显示 ${alarms.length} 条${list.hasNextPage ? '，还有更早的记录' : ''}` : '正在读取告警'}>
        <DataTable
          table={table}
          tableAriaLabel="告警记录"
          empty={list.isPending ? <Skeleton className="h-24" /> : (
            <Empty className="py-10">
              <EmptyHeader>
                <EmptyMedia variant="icon"><CircleCheck /></EmptyMedia>
                <EmptyTitle>{filtered ? '没有符合条件的告警' : view === 'active' ? '当前没有活动告警' : '没有告警'}</EmptyTitle>
                <EmptyDescription>{filtered ? '调整筛选条件后再看。' : '规则触发的告警会出现在这里。'}</EmptyDescription>
              </EmptyHeader>
            </Empty>
          )}
          getRowProps={(row) => ({
            className: 'cursor-pointer',
            'data-state': search.inspect === row.original.alarmId ? 'selected' : undefined,
            onClick: () => setSearch({ inspect: row.original.alarmId }),
          })}
        >
          <DataTableAdvancedToolbar table={table}>
            <ToggleGroup
              type="single"
              variant="outline"
              value={view}
              onValueChange={(value) => { if (value) setSearch({ view: value as AlarmView }); }}
              aria-label="告警范围"
            >
              {VIEWS.map((option) => (
                <ToggleGroupItem key={option.value} value={option.value} className="gap-1.5 px-3">
                  {option.label}
                  {option.value === 'active' && active.data ? <Badge variant="secondary" className="h-5 px-1.5 tabular-nums">{activeItems.length}</Badge> : null}
                </ToggleGroupItem>
              ))}
            </ToggleGroup>
            <InputGroup className="w-56">
              <InputGroupAddon><Search /></InputGroupAddon>
              <InputGroupInput
                aria-label="搜索告警"
                placeholder="告警、说明或设备"
                value={search.q ?? ''}
                onChange={(event) => setSearch({ q: event.target.value || undefined })}
              />
            </InputGroup>
            <Faceted
              multiple
              value={severities}
              onValueChange={(value) => setSearch({ severity: value && value.length > 0 ? value as AlarmSeverity[] : undefined })}
            >
              <FacetedTrigger asChild>
                <Button variant="outline" size="sm" className="h-8 border-dashed font-normal" aria-label="按严重度筛选">
                  <FacetedBadgeList options={SEVERITY_ORDER.map((severity) => ({ label: SEVERITY_LABELS[severity], value: severity }))} placeholder="严重度" />
                </Button>
              </FacetedTrigger>
              <FacetedContent className="w-44">
                <FacetedList>
                  <FacetedEmpty>没有选项</FacetedEmpty>
                  <FacetedGroup>
                    {SEVERITY_ORDER.map((severity) => (
                      <FacetedItem key={severity} value={severity}>
                        <span className="size-2 rounded-full" style={{ background: SEVERITY_COLORS[severity] }} aria-hidden="true" />
                        <span>{SEVERITY_LABELS[severity]}</span>
                      </FacetedItem>
                    ))}
                  </FacetedGroup>
                </FacetedList>
              </FacetedContent>
            </Faceted>
            {search.device ? (
              <Badge variant="secondary" className="h-8 gap-1 pr-1 font-normal">
                设备：{deviceNames.get(search.device) ?? '未登记设备'}
                <Button variant="ghost" size="icon" className="size-6" aria-label="清除设备筛选" onClick={() => setSearch({ device: undefined })}>
                  <X />
                </Button>
              </Badge>
            ) : null}
            {filtered ? (
              <Button variant="ghost" size="sm" className="h-8" onClick={() => setSearch({ severity: undefined, device: undefined, q: undefined })}>
                清除筛选
              </Button>
            ) : null}
          </DataTableAdvancedToolbar>
        </DataTable>
        {list.hasNextPage ? (
          <Button variant="outline" size="sm" className="self-center" disabled={list.isFetchingNextPage} onClick={() => void list.fetchNextPage()}>
            {list.isFetchingNextPage ? '正在读取…' : '加载更早的告警'}
          </Button>
        ) : null}
      </DataTableBlock>

      <AlarmInspector alarmId={search.inspect} deviceNames={deviceNames} onClose={() => setSearch({ inspect: undefined })} />
    </Main>
  );
}
