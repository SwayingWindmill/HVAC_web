import { useMemo } from 'react';
import type { ColumnDef } from '@tanstack/react-table';
import { AlertCircle, Search, UsersRound } from 'lucide-react';

import type { AlarmSeverity } from '@/api/alarms';
import type { IssueQueue, IssueQueueItem } from '@/api/issues';
import { DataTableBlock } from '@/blocks/data-table';
import { DataTable, DataTableAdvancedToolbar, type DataTableFeatures } from '@/components/data-table';
import { Badge } from '@/components/ui/badge';
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from '@/components/ui/empty';
import { InputGroup, InputGroupAddon, InputGroupInput, InputGroupText } from '@/components/ui/input-group';
import { useDataTable } from '@/hooks/use-data-table';
import { cn } from '@/lib/utils';
import type { AlarmOwnershipFilter } from './alarm-center-model';
import { formatInstant, severityLabel } from './alarm-center-model';
import {
  ProblemQueueFilterPopover,
  issueImpactMatches,
  type IssueImpactFilter,
} from './IssueFilters';

interface ProblemQueueViewProps {
  readonly data: IssueQueue | null | undefined;
  readonly loading: boolean;
  readonly error: boolean;
  readonly query: string;
  readonly severity?: AlarmSeverity;
  readonly issueState?: IssueQueueItem['state'];
  readonly diagnosis?: IssueQueueItem['diagnosisState'];
  readonly owner?: Exclude<AlarmOwnershipFilter, 'all'>;
  readonly impact?: IssueImpactFilter;
  readonly onQueryChange: (value: string) => void;
  readonly onFilterChange: (patch: {
    severity?: AlarmSeverity;
    issueState?: IssueQueueItem['state'];
    diagnosis?: IssueQueueItem['diagnosisState'];
    owner?: Exclude<AlarmOwnershipFilter, 'all'>;
    impact?: IssueImpactFilter;
  }) => void;
  readonly onOpenProblem: (item: IssueQueueItem) => void;
  readonly timeZone: string;
}

function stateLabel(state: IssueQueueItem['state']): string {
  if (state === 'OPEN') return '待处理';
  if (state === 'INVESTIGATING') return '排查中';
  if (state === 'ACTION_PENDING') return '待执行';
  if (state === 'VERIFYING') return '验证中';
  return '已解决';
}

function diagnosisLabel(state: IssueQueueItem['diagnosisState']): string {
  if (state === 'ROOT_CAUSE_CONFIRMED') return '原因已确认';
  if (state === 'EVIDENCE_LIMITED') return '证据待核实';
  if (state === 'PUBLISHED') return '已有诊断';
  return '待诊断';
}

function riskLabel(value: IssueQueueItem['impact']['reliabilityRisk']): string {
  if (value === 'CRITICAL') return '严重';
  if (value === 'HIGH') return '高';
  if (value === 'MEDIUM') return '中';
  if (value === 'LOW') return '低';
  return '—';
}

function formatMoney(value: number | null, currency: string | null): string | null {
  if (value === null) return null;
  return new Intl.NumberFormat('zh-CN', {
    style: 'currency',
    currency: currency ?? 'CNY',
    maximumFractionDigits: 0,
  }).format(value);
}

export function ProblemQueueView({
  data,
  loading,
  error,
  query,
  severity,
  issueState,
  diagnosis,
  owner,
  impact,
  onQueryChange,
  onFilterChange,
  onOpenProblem,
  timeZone,
}: ProblemQueueViewProps) {
  const visibleItems = useMemo(() => {
    const normalized = query.trim().toLocaleLowerCase('zh-CN');
    return (data?.items ?? [])
      .filter((item) => !severity || item.highestSeverity === severity)
      .filter((item) => !issueState || item.state === issueState)
      .filter((item) => !diagnosis || item.diagnosisState === diagnosis)
      .filter((item) => !owner || (owner === 'assigned' ? Boolean(item.assignee) : !item.assignee))
      .filter((item) => issueImpactMatches(item, impact))
      .filter((item) => !normalized || [
        item.title,
        item.object.label,
        item.object.locationLabel ?? '',
        item.assignee ?? '',
        item.nextAction ?? '',
        ...item.signals.map((signal) => signal.title),
      ].some((value) => value.toLocaleLowerCase('zh-CN').includes(normalized)));
  }, [data?.items, diagnosis, impact, issueState, owner, query, severity]);

  const columns = useMemo<ColumnDef<DataTableFeatures, IssueQueueItem>[]>(() => [
    {
      id: 'problem',
      accessorFn: (row) => row.title,
      header: '问题',
      meta: { label: '问题' },
      cell: ({ row }) => (
        <div className="min-w-[250px] max-w-[390px]">
          <div className="flex flex-wrap items-center gap-1.5">
            <Badge variant="outline" className="font-normal">{severityLabel[row.original.highestSeverity]}</Badge>
            <span className="font-medium leading-5">{row.original.title}</span>
          </div>
          <div className="mt-1 flex min-w-0 flex-wrap items-center gap-x-2 gap-y-0.5 text-xs text-muted-foreground">
            <span className="truncate">{row.original.object.label}</span>
            {row.original.object.locationLabel ? <span>· {row.original.object.locationLabel}</span> : null}
          </div>
        </div>
      ),
    },
    {
      id: 'signals',
      accessorFn: (row) => row.alarmIds.length,
      header: '关联告警',
      meta: { label: '关联告警' },
      cell: ({ row }) => (
        <div className="min-w-[92px] text-sm">
          <span className="font-medium tabular-nums">{row.original.alarmIds.length} 条</span>
          <span className="mt-0.5 block text-xs text-muted-foreground">
            {row.original.activeAlarmCount} 活动
            {row.original.unacknowledgedAlarmCount ? ` · ${row.original.unacknowledgedAlarmCount} 未确认` : ''}
          </span>
        </div>
      ),
    },
    {
      id: 'progress',
      accessorFn: (row) => row.state,
      header: '处理进度',
      meta: { label: '处理进度' },
      cell: ({ row }) => (
        <div className="min-w-[100px]">
          <span className="text-sm font-medium">{stateLabel(row.original.state)}</span>
          <span className={cn(
            'mt-0.5 block text-xs',
            row.original.diagnosisState === 'EVIDENCE_LIMITED' ? 'text-amber-700 dark:text-amber-400' : 'text-muted-foreground',
          )}>
            {diagnosisLabel(row.original.diagnosisState)}
          </span>
        </div>
      ),
    },
    {
      id: 'impact',
      accessorFn: (row) => row.impact.avoidableCost ?? row.impact.avoidableEnergyKwh ?? 0,
      header: '影响',
      meta: { label: '影响' },
      cell: ({ row }) => {
        const cost = formatMoney(row.original.impact.avoidableCost, row.original.impact.currency);
        const energy = row.original.impact.avoidableEnergyKwh;
        return (
          <div className="min-w-[120px] text-sm">
            <span className="font-medium tabular-nums">
              {cost ?? (energy === null ? '待评估' : `${new Intl.NumberFormat('zh-CN').format(energy)} kWh`)}
            </span>
            <span className="mt-0.5 block text-xs text-muted-foreground">
              {cost && energy !== null ? `${new Intl.NumberFormat('zh-CN').format(energy)} kWh · ` : ''}
              可靠性 {riskLabel(row.original.impact.reliabilityRisk)}
            </span>
          </div>
        );
      },
    },
    {
      id: 'owner',
      accessorFn: (row) => row.assignee ?? '',
      header: '负责人',
      meta: { label: '负责人' },
      cell: ({ row }) => (
        <span className={cn('whitespace-nowrap text-sm', !row.original.assignee && 'text-muted-foreground')}>
          {row.original.assignee ?? '未指派'}
        </span>
      ),
    },
    {
      id: 'nextAction',
      accessorFn: (row) => row.nextAction ?? '',
      header: '下一步',
      meta: { label: '下一步' },
      cell: ({ row }) => (
        <p className="min-w-[220px] max-w-[340px] text-xs leading-5 text-muted-foreground">
          {row.original.nextAction ?? '等待下一步处理'}
        </p>
      ),
    },
    {
      id: 'updatedAt',
      accessorFn: (row) => row.updatedAt,
      header: '最近变化',
      meta: { label: '最近变化' },
      cell: ({ row }) => (
        <span className="whitespace-nowrap text-xs tabular-nums text-muted-foreground">
          {formatInstant(row.original.updatedAt, timeZone)}
        </span>
      ),
    },
  ], [timeZone]);

  const table = useDataTable({
    key: 'issues-problem-queue',
    data: visibleItems,
    columns,
    paginate: false,
    getRowId: (row) => row.issueId,
  });

  if (loading) {
    return <div className="min-h-[360px] animate-pulse rounded-lg border bg-muted/20" aria-label="正在加载问题列表" />;
  }

  if (error) {
    return (
      <Empty className="min-h-[360px] rounded-lg border">
        <EmptyMedia variant="icon"><AlertCircle aria-hidden="true" /></EmptyMedia>
        <EmptyHeader>
          <EmptyTitle>问题列表暂时无法读取</EmptyTitle>
          <EmptyDescription>可以切换到“原始告警”继续处理，不影响现有告警操作。</EmptyDescription>
        </EmptyHeader>
      </Empty>
    );
  }

  if (!data) {
    return (
      <Empty className="min-h-[360px] rounded-lg border">
        <EmptyMedia variant="icon"><UsersRound aria-hidden="true" /></EmptyMedia>
        <EmptyHeader>
          <EmptyTitle>问题归并尚未接入</EmptyTitle>
          <EmptyDescription>当前仍可使用原始告警。后端接入后，这里会把同一设备或同一系统的相关告警放在一起处理。</EmptyDescription>
        </EmptyHeader>
      </Empty>
    );
  }

  return (
    <DataTableBlock data-testid="problem-queue">
      <DataTable table={table} empty={query ? '没有符合当前搜索的问题' : '当前没有需要处理的问题'}
        tableAriaLabel="问题列表"
        getRowProps={(row) => ({
          className: 'cursor-pointer',
          tabIndex: 0,
          'aria-label': `查看问题：${row.original.title}`,
          onClick: () => onOpenProblem(row.original),
          onKeyDown: (event) => {
            if (event.key === 'Enter' || event.key === ' ') {
              event.preventDefault();
              onOpenProblem(row.original);
            }
          },
        })}
      >
        <DataTableAdvancedToolbar table={table} className="p-0">
          <InputGroup className="w-full min-w-0 flex-1 sm:w-auto sm:min-w-72 lg:max-w-md">
            <InputGroupAddon>
              <InputGroupText><Search className="size-4" aria-hidden="true" /></InputGroupText>
            </InputGroupAddon>
            <InputGroupInput
              value={query}
              onChange={(event) => onQueryChange(event.currentTarget.value)}
              placeholder="搜索问题、设备或下一步"
              aria-label="搜索问题"
            />
            <InputGroupAddon align="inline-end">
              <InputGroupText>{visibleItems.length} 项</InputGroupText>
            </InputGroupAddon>
          </InputGroup>
          <ProblemQueueFilterPopover
            severity={severity}
            state={issueState}
            diagnosis={diagnosis}
            owner={owner}
            impact={impact}
            onChange={onFilterChange}
          />
        </DataTableAdvancedToolbar>
      </DataTable>
    </DataTableBlock>
  );
}
