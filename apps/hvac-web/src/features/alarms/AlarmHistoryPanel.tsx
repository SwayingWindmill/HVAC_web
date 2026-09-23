import { useMemo, useState } from 'react';
import type { ColumnDef } from '@tanstack/react-table';
import { parseAsStringEnum, useQueryState } from 'nuqs';
import { Download, Link2, LoaderCircle, Search } from 'lucide-react';
import { DataTableBlock } from '@/blocks/data-table';
import type { AlarmSeverity } from '@/api/alarms';
import type { Site } from '@/api/generated/platformGateway.gen';
import { siteRoute } from '@/app/router-paths';
import { Checkbox } from '@/components/ui/checkbox';
import {
  DataTable,
  DataTableAdvancedToolbar,
  DataTableFilterList,
  DataTablePagination,
  DataTableSortList,
  type DataTableFeatures,
} from '@/components/data-table';
import { StatusBadge } from '@/components/status-badge';
import { Button } from '@/components/ui/button';
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card';
import { InputGroup, InputGroupAddon, InputGroupInput, InputGroupText } from '@/components/ui/input-group';
import { useDataTable } from '@/hooks/use-data-table';
import type { ExtendedColumnFilter, JoinOperator } from '@/lib/data-table-types';
import { getFiltersStateParser } from '@/lib/parsers';
import {
  durationMs,
  formatDuration,
  formatDurationMs,
  formatInstant,
  linkedWorkOrderIds,
  localDateKey,
  recoveryReason,
  severityLabel,
  type AlarmRow,
} from './alarm-center-model';

const ALARM_SEVERITY_OPTIONS = [
  { label: '紧急', value: 'CRITICAL' },
  { label: '重要', value: 'MAJOR' },
  { label: '一般', value: 'MINOR' },
  { label: '警告', value: 'WARNING' },
  { label: '提示', value: 'INFO' },
] as const;

const ALARM_HISTORY_FILTER_COLUMN_IDS = ['alarm', 'severityLabel'] as const;
const ALARM_HISTORY_FILTERS_QUERY_KEY = 'alarmHistoryFilters';
const ALARM_HISTORY_JOIN_OPERATOR_QUERY_KEY = 'alarmHistoryJoinOperator';

interface AlarmHistoryPanelProps {
  rows: readonly AlarmRow[];
  site: Readonly<Site>;
  onOpenAlarm: (alarmId: string) => void;
  onExport: (rows: readonly AlarmRow[]) => void;
  loading: boolean;
  hasNextPage: boolean;
  fetchingNextPage: boolean;
  onLoadMore: () => void;
}

function severityTone(severity: AlarmSeverity): 'destructive' | 'warning' | 'info' | 'neutral' {
  if (severity === 'CRITICAL' || severity === 'MAJOR') return 'destructive';
  if (severity === 'MINOR' || severity === 'WARNING') return 'warning';
  return 'info';
}

function matchesAlarmHistoryAdvancedFilter(
  row: AlarmRow,
  filter: ExtendedColumnFilter<AlarmRow>,
) {
  if (filter.id !== 'alarm' && filter.id !== 'severityLabel') return true;
  const values = Array.isArray(filter.value) ? filter.value : [filter.value];
  const actual = filter.id === 'severityLabel' ? row.alarm.currentSeverity : row.alarm.alarmType;

  switch (filter.operator) {
    case 'eq':
    case 'inArray':
      return values.includes(actual);
    case 'ne':
    case 'notInArray':
      return !values.includes(actual);
    case 'isEmpty':
      return actual.length === 0;
    case 'isNotEmpty':
      return actual.length > 0;
    default:
      return false;
  }
}

function SeverityBadge({ severity }: { severity: AlarmSeverity }) {
  return (
    <StatusBadge tone={severityTone(severity)}>
      {severityLabel[severity]}
    </StatusBadge>
  );
}

export function AlarmHistoryPanel({
  rows,
  site,
  onOpenAlarm,
  onExport,
  loading,
  hasNextPage,
  fetchingNextPage,
  onLoadMore,
}: AlarmHistoryPanelProps) {
  const [search, setSearch] = useState('');
  const [advancedFilters, setAdvancedFilters] = useQueryState(
    ALARM_HISTORY_FILTERS_QUERY_KEY,
    getFiltersStateParser<AlarmRow>([...ALARM_HISTORY_FILTER_COLUMN_IDS]).withDefault([]),
  );
  const [joinOperator, setJoinOperator] = useQueryState(
    ALARM_HISTORY_JOIN_OPERATOR_QUERY_KEY,
    parseAsStringEnum<JoinOperator>(['and', 'or']).withDefault('and'),
  );

  const typeOptions = useMemo(() => [...new Map(rows.map((row) => [row.alarm.alarmType, row.alarm.title] as const)).entries()]
    .sort(([, left], [, right]) => left.localeCompare(right, 'zh-CN')), [rows]);

  const filteredRows = useMemo(() => {
    const normalized = search.trim().toLocaleLowerCase('zh-CN');
    return rows.filter((row) => {
      const matchesSearch = !normalized || [row.alarm.title, row.alarm.summary, row.alarm.alarmType, row.deviceLabel, row.locationLabel]
        .some((value) => value.toLocaleLowerCase('zh-CN').includes(normalized));
      const filterMatches = advancedFilters.map((filter) => matchesAlarmHistoryAdvancedFilter(row, filter));
      const matchesAdvancedFilters = advancedFilters.length === 0 || (joinOperator === 'or'
        ? filterMatches.some(Boolean)
        : filterMatches.every(Boolean));
      return matchesSearch && matchesAdvancedFilters;
    });
  }, [advancedFilters, joinOperator, rows, search]);

  const today = localDateKey(Date.now(), site.timezone);
  const todayRecovered = filteredRows.filter((row) => row.alarm.clearedAt
    && localDateKey(row.alarm.clearedAt, site.timezone) === today).length;
  const withWorkOrder = filteredRows.filter((row) => linkedWorkOrderIds(row.alarm).length > 0).length;
  const averageDuration = filteredRows.length > 0
    ? formatDurationMs(filteredRows.reduce((sum, row) => sum + durationMs(row.alarm.firstOccurredAt, row.alarm.clearedAt), 0) / filteredRows.length)
    : '—';

  const trend = useMemo(() => {
    const counts = new Map<string, number>();
    for (const row of filteredRows) {
      if (!row.alarm.clearedAt) continue;
      const key = localDateKey(row.alarm.clearedAt, site.timezone);
      counts.set(key, (counts.get(key) ?? 0) + 1);
    }
    return [...counts.entries()].sort(([left], [right]) => left.localeCompare(right)).slice(-14);
  }, [filteredRows, site.timezone]);
  const largestTrend = Math.max(1, ...trend.map(([, count]) => count));

  const recoveryReasons = useMemo(() => {
    const counts = new Map<string, number>();
    for (const row of filteredRows) {
      const reason = recoveryReason(row.alarm);
      counts.set(reason, (counts.get(reason) ?? 0) + 1);
    }
    return [...counts.entries()].sort(([, left], [, right]) => right - left).slice(0, 6);
  }, [filteredRows]);
  const largestReasonCount = recoveryReasons[0]?.[1] ?? 1;

  const columns = useMemo<Array<ColumnDef<DataTableFeatures, AlarmRow>>>(() => [
    {
      id: 'select',
      header: ({ table }) => (
        <Checkbox
          checked={
            table.getIsAllPageRowsSelected()
              ? true
              : table.getIsSomePageRowsSelected()
                ? 'indeterminate'
                : false
          }
          onCheckedChange={(checked) => table.toggleAllPageRowsSelected(Boolean(checked))}
          aria-label="全选"
        />
      ),
      cell: ({ row }) => (
        <Checkbox
          checked={row.getIsSelected()}
          onCheckedChange={(checked) => row.toggleSelected(Boolean(checked))}
          aria-label="选择行"
          onClick={(event) => event.stopPropagation()}
        />
      ),
      enableSorting: false,
      enableHiding: false,
    },
    { id: 'severityLabel', accessorFn: (row) => row.severityLabel, enableColumnFilter: true, meta: { label: '等级', variant: 'select', options: [...ALARM_SEVERITY_OPTIONS] }, header: '等级', cell: ({ row }) => <SeverityBadge severity={row.original.alarm.currentSeverity} /> },
    {
      id: 'alarm',
      accessorFn: (row) => row.alarm.title,
      enableColumnFilter: true,
      meta: {
        label: '告警类型',
        variant: 'select',
        options: typeOptions.map(([value, label]) => ({ value, label })),
      },
      header: '告警',
      cell: ({ row }) => (
        <button type="button" className="block max-w-[360px] text-left" onClick={() => onOpenAlarm(row.original.alarm.alarmId)}>
          <strong className="block truncate text-xs font-medium">{row.original.alarm.title}</strong>
          <span className="mt-0.5 block truncate text-[11px] text-muted-foreground">{row.original.alarm.summary}</span>
        </button>
      ),
    },
    { id: 'device', accessorFn: (row) => row.deviceLabel, meta: { label: '设备 / 位置' }, header: '设备 / 位置', cell: ({ row }) => <div className="max-w-48"><span className="block truncate text-xs">{row.original.deviceLabel}</span><span className="block truncate text-[11px] text-muted-foreground">{row.original.locationLabel}</span></div> },
    { id: 'first', accessorFn: (row) => row.alarm.firstOccurredAt, meta: { label: '首次发生' }, header: '首次发生', cell: ({ row }) => <span className="whitespace-nowrap font-mono text-[11px] text-muted-foreground tabular-nums">{formatInstant(row.original.alarm.firstOccurredAt, site.timezone)}</span> },
    { id: 'cleared', accessorFn: (row) => row.alarm.clearedAt ?? '', meta: { label: '恢复时间' }, header: '恢复时间', cell: ({ row }) => <span className="whitespace-nowrap font-mono text-[11px] text-muted-foreground tabular-nums">{formatInstant(row.original.alarm.clearedAt, site.timezone)}</span> },
    { id: 'duration', accessorFn: (row) => durationMs(row.alarm.firstOccurredAt, row.alarm.clearedAt), meta: { label: '持续时长' }, header: '持续时长', cell: ({ row }) => <span className="whitespace-nowrap font-mono text-xs tabular-nums">{formatDuration(row.original.alarm.firstOccurredAt, row.original.alarm.clearedAt)}</span> },
    { id: 'repeat', accessorFn: (row) => row.alarm.occurrenceCount, meta: { label: '重复次数' }, header: '重复', cell: ({ row }) => <span className="font-mono text-xs tabular-nums">{row.original.alarm.occurrenceCount}</span> },
    {
      id: 'work-order',
      header: '工单',
      cell: ({ row }) => {
        const workOrders = linkedWorkOrderIds(row.original.alarm);
        if (workOrders.length === 0) return <span className="text-xs text-muted-foreground">—</span>;
        return <Button variant="ghost" size="xs" asChild><a href={`${siteRoute(site, 'work-orders')}?workOrder=${encodeURIComponent(workOrders[0])}`}><Link2 />查看{workOrders.length > 1 ? ` +${workOrders.length - 1}` : ''}</a></Button>;
      },
    },
    {
      id: 'action',
      header: '',
      cell: ({ row }) => (
        <Button variant="ghost" size="sm" onClick={() => onOpenAlarm(row.original.alarm.alarmId)}>
          查看
        </Button>
      ),
      enableSorting: false,
      enableHiding: false,
    },
  ], [onOpenAlarm, site, typeOptions]);

  const table = useDataTable({
    key: 'alarm-history-table',
    data: filteredRows,
    columns,
    pageSize: 10,
    getRowId: (row) => row.alarm.alarmId,
    meta: {
      queryKeys: {
        page: 'alarmHistoryPage',
        perPage: 'alarmHistoryPerPage',
        sort: 'alarmHistorySort',
        filters: ALARM_HISTORY_FILTERS_QUERY_KEY,
        joinOperator: ALARM_HISTORY_JOIN_OPERATOR_QUERY_KEY,
      },
    },
  });


  return (
    <div className="alarm-center__history-workbench space-y-4">
      <div className="grid gap-3 md:grid-cols-3">
        <Card size="sm" className="shadow-none"><CardHeader className="gap-0"><CardTitle className="text-xs font-medium text-muted-foreground">已恢复记录</CardTitle></CardHeader><CardContent><strong className="text-2xl font-semibold tabular-nums">{filteredRows.length}</strong><p className="mt-1 text-[11px] text-muted-foreground">当前已加载恢复记录</p></CardContent></Card>
        <Card size="sm" className="shadow-none"><CardHeader className="gap-0"><CardTitle className="text-xs font-medium text-muted-foreground">今日恢复</CardTitle></CardHeader><CardContent><strong className="text-2xl font-semibold tabular-nums">{todayRecovered}</strong><p className="mt-1 text-[11px] text-muted-foreground">由恢复条件自动判定</p></CardContent></Card>
        <Card size="sm" className="shadow-none"><CardHeader className="gap-0"><CardTitle className="text-xs font-medium text-muted-foreground">平均持续时长</CardTitle></CardHeader><CardContent><strong className="text-xl font-semibold tabular-nums">{averageDuration}</strong><p className="mt-1 text-[11px] text-muted-foreground">{withWorkOrder} 条已关联工单</p></CardContent></Card>
      </div>

      <div className="rounded-md border bg-muted/20 p-3 text-xs leading-5 text-muted-foreground">
        <strong className="text-foreground">历史页展示的是物理恢复事实。</strong> 恢复来自规则对实时条件的判定；确认、指派和抑制属于处置事实，不改变恢复时间与恢复原因。
      </div>

      <div className="grid gap-4 xl:grid-cols-2">
        <Card className="shadow-none">
          <CardHeader><CardTitle>最近恢复趋势</CardTitle><CardDescription>最近 14 个有记录的日期</CardDescription></CardHeader>
          <CardContent>
            {trend.length > 0 ? (
              <div className="flex h-44 items-end gap-2" role="img" aria-label="历史告警恢复数量趋势">
                {trend.map(([key, count]) => (
                  <div key={key} className="flex min-w-0 flex-1 flex-col items-center justify-end gap-1">
                    <span className="text-[10px] font-medium tabular-nums">{count}</span>
                    <div className="w-full rounded-sm bg-primary/80" style={{ height: `${Math.max(8, (count / largestTrend) * 112)}px` }} />
                    <span className="truncate text-[9px] text-muted-foreground">{key.slice(5)}</span>
                  </div>
                ))}
              </div>
            ) : <div className="grid h-44 place-items-center text-xs text-muted-foreground">当前筛选条件下没有恢复趋势数据</div>}
          </CardContent>
        </Card>

        <Card className="shadow-none">
          <CardHeader><CardTitle>恢复原因</CardTitle></CardHeader>
          <CardContent className="space-y-3">
            {recoveryReasons.length > 0 ? recoveryReasons.map(([reason, count]) => (
              <div key={reason}>
                <div className="flex items-center justify-between gap-3 text-xs"><span className="min-w-0 truncate text-muted-foreground" title={reason}>{reason}</span><strong className="tabular-nums">{count}</strong></div>
                <div className="mt-1 h-1.5 overflow-hidden rounded-full bg-muted"><div className="h-full rounded-full bg-primary/70" style={{ width: `${Math.max(8, (count / largestReasonCount) * 100)}%` }} /></div>
              </div>
            )) : <div className="grid h-36 place-items-center text-xs text-muted-foreground">暂无恢复原因</div>}
          </CardContent>
        </Card>
      </div>

      <DataTableBlock
        className="alarm-center__history-ledger"
        title="历史告警"
      >
        {loading ? (
          <div className="flex min-h-64 items-center justify-center gap-2 rounded-md border text-sm text-muted-foreground"><LoaderCircle className="size-4 animate-spin" />正在读取历史告警</div>
        ) : (
          <DataTable
            table={table}
            tableAriaLabel="历史告警"
            empty="当前筛选条件下没有恢复记录"
            getRowProps={(row) => ({
              className: 'cursor-pointer',
              onClick: () => onOpenAlarm(row.original.alarm.alarmId),
            })}
            getCellProps={() => ({ className: 'py-2.5' })}
            footer={<DataTablePagination table={table} totalRows={filteredRows.length} />}
          >
            <DataTableAdvancedToolbar table={table}>
              <InputGroup className="min-w-64 flex-1 lg:max-w-sm">
                <InputGroupInput aria-label="搜索历史告警" placeholder="搜索告警 / 设备 / 类型" value={search} onChange={(event) => { setSearch(event.currentTarget.value); table.setPageIndex(0); }} />
                <InputGroupAddon align="inline-start"><Search className="size-3.5" /></InputGroupAddon>
                <InputGroupAddon align="inline-end"><InputGroupText>{filteredRows.length} 条</InputGroupText></InputGroupAddon>
              </InputGroup>
              <DataTableSortList table={table} />
              <DataTableFilterList table={table} />
              <Button variant="ghost" size="sm" onClick={() => { setSearch(''); void setAdvancedFilters(null); void setJoinOperator('and'); table.setPageIndex(0); }}>重置</Button>
              <Button variant="outline" size="sm" disabled={filteredRows.length === 0} onClick={() => onExport(filteredRows)}><Download className="mr-1.5 size-3.5" />导出</Button>
            </DataTableAdvancedToolbar>
          </DataTable>
        )}
        {hasNextPage ? <div className="flex justify-center"><Button variant="outline" size="sm" disabled={fetchingNextPage} onClick={onLoadMore}>{fetchingNextPage ? <LoaderCircle className="animate-spin" /> : null}加载更多历史记录</Button></div> : null}
      </DataTableBlock>
    </div>
  );
}
