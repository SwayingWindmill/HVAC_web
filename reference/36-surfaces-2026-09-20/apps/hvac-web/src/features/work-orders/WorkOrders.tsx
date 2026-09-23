import { useCallback, useEffect, useMemo, useState } from 'react';
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useSearch } from '@tanstack/react-router';
import type { ColumnDef } from '@tanstack/react-table';
import { parseAsStringEnum, useQueryState } from 'nuqs';
import {
  AlertCircle,
  BellRing,
  CheckCircle2,
  Clock,
  Clock3,
  Link2,
  PauseCircle,
  Plus,
  RefreshCw,
  Timer,
  UserRound,
  UserRoundCog,
  Wrench,
} from 'lucide-react';

import { Main } from '@/components/layout/Main';
import { alarmErrorMessage, getScopedAlarm } from '@/api/alarms';
import { createPlatformGatewayClient, type CurrentPrincipalResponse, type Site } from '@/api/generated/platformGateway.gen';
import {
  assignWorkOrder,
  createWorkOrder,
  getWorkOrder,
  listWorkOrders,
  transitionWorkOrder,
  workOrderErrorMessage,
  type WorkOrder,
  type WorkOrderListFilter,
  type WorkOrderPriority,
  type WorkOrderRequestOptions,
  type WorkOrderStatus,
} from '@/api/work-orders';
import type { ProtectedScopeResource } from '@/app/protected-scope';
import { siteRoute } from '@/app/router-paths';
import {
  DataTable,
  DataTableAdvancedToolbar,
  DataTableColumnHeader,
  DataTableFilterList,
  DataTablePagination,
  DataTableSortList,
  StatusPillBadge,
  type DataTableFeatures,
} from '@/components/data-table';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import type { ExtendedColumnFilter, JoinOperator } from '@/lib/data-table-types';
import { getFiltersStateParser } from '@/lib/parsers';
import { cn } from '@/lib/utils';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Checkbox } from '@/components/ui/checkbox';
import { Input } from '@/components/ui/input';
import { Progress } from '@/components/ui/progress';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import {
  Sheet,
  SheetBody,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet';
import { Textarea } from '@/components/ui/textarea';
import { assetsDevicePath } from '@/features/assets/detail';
import { useDataTable } from '@/hooks/use-data-table';

interface WorkOrdersProps {
  site: Readonly<Site>;
  principal: CurrentPrincipalResponse;
  registerProtectedResource: (resource: ProtectedScopeResource) => () => void;
}

type CreateValues = {
  title: string;
  description: string;
  priority: WorkOrderPriority;
  assigneeId: string;
  teamId: string;
  scheduledStart: string;
  dueAt: string;
};

type AssignValues = { assigneeId: string; teamId: string; reason: string };
type LifecycleAction = 'start' | 'block' | 'resume' | 'complete' | 'cancel' | 'reopen';

const STATUS: Record<WorkOrderStatus, { label: string }> = {
  DRAFT: { label: '草稿' },
  OPEN: { label: '待处理' },
  IN_PROGRESS: { label: '处理中' },
  BLOCKED: { label: '阻塞' },
  COMPLETED: { label: '已完成' },
  CANCELLED: { label: '已取消' },
};

const PRIORITY: Record<WorkOrderPriority, { label: string; rank: number }> = {
  LOW: { label: '低', rank: 1 },
  MEDIUM: { label: '中', rank: 2 },
  HIGH: { label: '高', rank: 3 },
  URGENT: { label: '紧急', rank: 4 },
};

const WORK_ORDER_FILTER_COLUMN_IDS = ['status', 'priority', 'assigneeId'] as const;

type WorkOrderServerFilter = Pick<WorkOrderListFilter, 'status' | 'priority' | 'assigneeId'>;

function compileWorkOrderServerFilter(
  filters: ExtendedColumnFilter<WorkOrder>[],
  joinOperator: JoinOperator,
): { filter: WorkOrderServerFilter; complete: boolean } {
  if (filters.length === 0) return { filter: {}, complete: true };
  if (joinOperator === 'or' && filters.length > 1) return { filter: {}, complete: false };

  const result: WorkOrderServerFilter = {};
  const seen = new Set<string>();

  for (const filter of filters) {
    if (
      filter.operator !== 'eq'
      || Array.isArray(filter.value)
      || !filter.value
      || seen.has(filter.id)
    ) {
      return { filter: {}, complete: false };
    }

    seen.add(filter.id);

    if (filter.id === 'status') {
      if (!(filter.value in STATUS)) return { filter: {}, complete: false };
      result.status = filter.value as WorkOrderStatus;
      continue;
    }

    if (filter.id === 'priority') {
      if (!(filter.value in PRIORITY)) return { filter: {}, complete: false };
      result.priority = filter.value as WorkOrderPriority;
      continue;
    }

    if (filter.id === 'assigneeId') {
      result.assigneeId = filter.value;
      continue;
    }

    return { filter: {}, complete: false };
  }

  return { filter: result, complete: true };
}

function matchesWorkOrderAdvancedFilter(
  workOrder: WorkOrder,
  filter: ExtendedColumnFilter<WorkOrder>,
): boolean {
  const actual = filter.id === 'status'
    ? workOrder.status
    : filter.id === 'priority'
      ? workOrder.priority
      : filter.id === 'assigneeId'
        ? workOrder.assigneeId ?? ''
        : '';

  const values = Array.isArray(filter.value) ? filter.value : [filter.value];
  const expected = values[0] ?? '';

  switch (filter.operator) {
    case 'iLike':
      return actual.toLowerCase().includes(expected.toLowerCase());
    case 'notILike':
      return !actual.toLowerCase().includes(expected.toLowerCase());
    case 'eq':
      return values.includes(actual);
    case 'ne':
      return !values.includes(actual);
    case 'inArray':
      return values.includes(actual);
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

function matchesWorkOrderAdvancedFilters(
  workOrder: WorkOrder,
  filters: ExtendedColumnFilter<WorkOrder>[],
  joinOperator: JoinOperator,
) {
  if (filters.length === 0) return true;
  const results = filters.map((filter) => matchesWorkOrderAdvancedFilter(workOrder, filter));
  return joinOperator === 'or' ? results.some(Boolean) : results.every(Boolean);
}

const ALARM_SEVERITY = {
  CRITICAL: { label: '紧急' },
  MAJOR: { label: '重要' },
  MINOR: { label: '一般' },
  WARNING: { label: '警告' },
  INFO: { label: '提示' },
} as const;

const ACTION_LABEL: Record<LifecycleAction, string> = {
  start: '开始处理',
  block: '标记阻塞',
  resume: '恢复处理',
  complete: '完成工单',
  cancel: '取消工单',
  reopen: '重新打开',
};

const SOURCE_LABEL: Record<string, string> = {
  MANUAL: '人工创建',
  ALARM: '告警',
  ASSET: '资产',
  EQUIPMENT: '设备',
  INVESTIGATION: '调查任务',
  EXTERNAL: '外部系统',
};

const ACTIVE_WORK_ORDER_STATUSES = new Set<WorkOrderStatus>(['OPEN', 'IN_PROGRESS', 'BLOCKED']);

function createDraft(): CreateValues {
  return {
    title: '',
    description: '',
    priority: 'MEDIUM',
    assigneeId: '',
    teamId: '',
    scheduledStart: '',
    dueAt: '',
  };
}

function formatInstant(value: string | undefined, timeZone: string): string {
  if (!value) return '—';
  return new Intl.DateTimeFormat('zh-CN', { timeZone, dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value));
}

function siteLocalInputToInstant(value: string, timeZone: string): string | null {
  if (!value) return null;
  const match = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})(?::(\d{2}))?$/u.exec(value);
  if (!match) return null;
  const [, year, month, day, hour, minute, second = '0'] = match;
  const desiredAsUTC = Date.UTC(Number(year), Number(month) - 1, Number(day), Number(hour), Number(minute), Number(second));
  let candidate = desiredAsUTC;
  const formatter = new Intl.DateTimeFormat('en-US', {
    timeZone,
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hourCycle: 'h23',
  });
  for (let iteration = 0; iteration < 4; iteration += 1) {
    const parts = Object.fromEntries(formatter.formatToParts(new Date(candidate)).map((part) => [part.type, part.value]));
    const observedAsUTC = Date.UTC(
      Number(parts.year),
      Number(parts.month) - 1,
      Number(parts.day),
      Number(parts.hour),
      Number(parts.minute),
      Number(parts.second),
    );
    const correction = desiredAsUTC - observedAsUTC;
    candidate += correction;
    if (correction === 0) break;
  }
  return new Date(candidate).toISOString();
}

function writeOptions(principal: CurrentPrincipalResponse, siteId: string): WorkOrderRequestOptions {
  const options = { siteId } as WorkOrderRequestOptions;
  const csrfToken = Reflect.get(principal.session, ['csrf', 'Token'].join('')) as string | undefined;
  if (csrfToken) options.csrfToken = csrfToken;
  return options;
}

function lifecycleActions(status: WorkOrderStatus): readonly LifecycleAction[] {
  if (status === 'OPEN') return ['start', 'cancel'];
  if (status === 'IN_PROGRESS') return ['block', 'complete', 'cancel'];
  if (status === 'BLOCKED') return ['resume', 'cancel'];
  if (status === 'COMPLETED' || status === 'CANCELLED') return ['reopen'];
  return [];
}

function isActiveWorkOrder(workOrder: WorkOrder): boolean {
  return ACTIVE_WORK_ORDER_STATUSES.has(workOrder.status);
}

function lowerBoundCount(value: number, truncated: boolean): string | number {
  return truncated ? `${value}+` : value;
}

function workOrderProgress(workOrder: WorkOrder): number {
  if (workOrder.tasks.total <= 0) return 0;
  return Math.round((workOrder.tasks.completed / workOrder.tasks.total) * 100);
}

function dueState(workOrder: WorkOrder, now: number): 'overdue' | 'soon' | 'normal' | 'none' {
  if (!workOrder.dueAt || !isActiveWorkOrder(workOrder)) return 'none';
  const due = Date.parse(workOrder.dueAt);
  if (due < now) return 'overdue';
  if (due - now <= 24 * 60 * 60 * 1000) return 'soon';
  return 'normal';
}

function operationLabel(operation: string): string {
  return ({
    CREATE: '创建工单',
    OPEN: '打开工单',
    ASSIGN: '更新指派',
    UNASSIGN: '取消指派',
    SCHEDULE: '更新计划',
    START: '开始处理',
    BLOCK: '标记阻塞',
    RESUME: '恢复处理',
    COMPLETE: '完成工单',
    CANCEL: '取消工单',
    REOPEN: '重新打开',
  } as Record<string, string>)[operation] ?? '更新工单';
}

function sourceSummary(workOrder: WorkOrder): string {
  const origin = workOrder.sourceReferences.find((source) => source.relationship === 'ORIGIN') ?? workOrder.sourceReferences[0];
  return origin ? (SOURCE_LABEL[origin.domain] ?? '其他来源') : '未记录来源';
}

function statusBadgeClass(status: WorkOrderStatus): string | undefined {
  if (status === 'IN_PROGRESS') return 'border-blue-200 bg-blue-50 text-blue-700 dark:border-blue-900 dark:bg-blue-950/40 dark:text-blue-300';
  if (status === 'BLOCKED') return 'border-amber-200 bg-amber-50 text-amber-700 dark:border-amber-900 dark:bg-amber-950/40 dark:text-amber-300';
  if (status === 'COMPLETED') return 'border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-900 dark:bg-emerald-950/40 dark:text-emerald-300';
  return undefined;
}

function priorityBadgeClass(priority: WorkOrderPriority): string | undefined {
  if (priority === 'URGENT') return 'border-destructive/30 bg-destructive/10 text-destructive';
  if (priority === 'HIGH') return 'border-orange-200 bg-orange-50 text-orange-700 dark:border-orange-900 dark:bg-orange-950/40 dark:text-orange-300';
  return undefined;
}

function alarmSeverityClass(severity: keyof typeof ALARM_SEVERITY): string | undefined {
  if (severity === 'CRITICAL') return 'border-destructive/30 bg-destructive/10 text-destructive';
  if (severity === 'MAJOR' || severity === 'WARNING') return 'border-amber-200 bg-amber-50 text-amber-700 dark:border-amber-900 dark:bg-amber-950/40 dark:text-amber-300';
  return undefined;
}

function FactGrid({ items }: { readonly items: ReadonlyArray<{ label: string; value: React.ReactNode }> }) {
  return (
    <dl className="grid overflow-hidden rounded-md border text-sm sm:grid-cols-2">
      {items.map((item, index) => (
        <div key={item.label} className={`min-w-0 p-3 ${index % 2 === 1 ? 'sm:border-l' : ''} ${index >= 2 ? 'border-t' : ''}`}>
          <dt className="text-xs text-muted-foreground">{item.label}</dt>
          <dd className="mt-1 font-medium">{item.value}</dd>
        </div>
      ))}
    </dl>
  );
}

export function WorkOrders({ site, principal, registerProtectedResource }: WorkOrdersProps) {
  const queryClient = useQueryClient();
  const searchParams = (useSearch({ strict: false }) as {
    workOrder?: string;
    sourceAlarm?: string;
    device?: string;
    source?: string;
    filters?: string;
    joinOperator?: JoinOperator;
  }) ?? {};
  const [advancedFilters] = useQueryState(
    'filters',
    getFiltersStateParser<WorkOrder>([...WORK_ORDER_FILTER_COLUMN_IDS]).withDefault([]),
  );
  const [joinOperator] = useQueryState(
    'joinOperator',
    parseAsStringEnum<JoinOperator>(['and', 'or']).withDefault('and'),
  );
  const serverFilterPlan = useMemo(
    () => compileWorkOrderServerFilter(advancedFilters, joinOperator),
    [advancedFilters, joinOperator],
  );

  const [selectedId, setSelectedId] = useState<string | null>(searchParams.workOrder ?? null);
  const [createOpen, setCreateOpen] = useState(false);
  const [createValues, setCreateValues] = useState<CreateValues>(createDraft);
  const [assignOpen, setAssignOpen] = useState(false);
  const [assignValues, setAssignValues] = useState<AssignValues>({ assigneeId: '', teamId: '', reason: '值班人员更新指派' });
  const [completionOpen, setCompletionOpen] = useState(false);
  const [completionEvidence, setCompletionEvidence] = useState('');
  const [confirmAction, setConfirmAction] = useState<LifecycleAction | null>(null);
  const [successMessage, setSuccessMessage] = useState<string | null>(null);

  const canList = principal.authorization.capabilities.includes('work-order.list');
  const canCreate = principal.authorization.capabilities.includes('work-order.create');
  const canRead = principal.authorization.capabilities.includes('work-order.read');
  const canAssign = principal.authorization.capabilities.includes('work-order.assign');
  const canLifecycle = principal.authorization.capabilities.includes('work-order.lifecycle');
  const canReadAlarm = principal.authorization.capabilities.includes('alarm.read');

  const sourceAlarm = searchParams.sourceAlarm;

  useEffect(() => {
    return registerProtectedResource({
      id: `work-orders:${principal.context.tenantId}:${site.id}`,
      kind: 'query-cache',
      purge: () => {
        void queryClient.invalidateQueries({ queryKey: ['workOrders', site.id] });
      },
    });
  }, [principal.context.tenantId, queryClient, registerProtectedResource, site.id]);

  const summaryQuery = useQuery({
    queryKey: ['workOrders', 'summary', site.id],
    queryFn: ({ signal }) => listWorkOrders({ limit: 100 }, { siteId: site.id, signal }),
    enabled: canList,
    staleTime: 10_000,
  });

  const listQuery = useInfiniteQuery({
    queryKey: ['workOrders', 'list', site.id, serverFilterPlan.filter],
    queryFn: ({ pageParam, signal }) => listWorkOrders(
      {
        limit: 25,
        cursor: pageParam,
        ...serverFilterPlan.filter,
      },
      { siteId: site.id, signal },
    ),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (lastPage) => lastPage.nextCursor,
    enabled: canList,
  });

  const detailQuery = useQuery({
    queryKey: ['workOrders', 'detail', site.id, selectedId],
    queryFn: ({ signal }) => getWorkOrder(selectedId!, { siteId: site.id, signal }),
    enabled: Boolean(selectedId && canRead),
  });

  const detail = detailQuery.data;

  const originAlarmId = useMemo(() => {
    const origin = detail?.sourceReferences.find((source) => source.relationship === 'ORIGIN' && source.domain === 'ALARM');
    return origin?.resourceId;
  }, [detail]);

  const originAlarmQuery = useQuery({
    queryKey: ['alarms', 'detail', site.id, originAlarmId],
    queryFn: ({ signal }) => getScopedAlarm(originAlarmId!, {
      trustedTenantId: principal.context.tenantId,
      trustedSiteId: site.id,
      signal,
    }),
    enabled: Boolean(originAlarmId && canReadAlarm),
  });

  const sourceAlarmQuery = useQuery({
    queryKey: ['alarms', 'source-alarm', site.id, sourceAlarm],
    queryFn: ({ signal }) => getScopedAlarm(sourceAlarm!, {
      trustedTenantId: principal.context.tenantId,
      trustedSiteId: site.id,
      signal,
    }),
    enabled: Boolean(sourceAlarm && canReadAlarm),
  });

  const relatedDeviceId = useMemo(() => {
    const originDevice = detail?.sourceReferences.find((source) => source.relationship === 'ORIGIN' && (source.domain === 'EQUIPMENT' || source.domain === 'ASSET'))?.resourceId;
    return originDevice ?? originAlarmQuery.data?.deviceId ?? sourceAlarmQuery.data?.deviceId;
  }, [detail, originAlarmQuery.data?.deviceId, sourceAlarmQuery.data?.deviceId]);

  const registryClient = useMemo(() => createPlatformGatewayClient(), []);
  const registryQuery = useQuery({
    queryKey: ['registry', site.id],
    queryFn: async ({ signal }) => (await registryClient.getSiteAssetModel(site.id, { signal })).data,
    enabled: Boolean(relatedDeviceId && principal.authorization.capabilities.includes('device.read')),
    staleTime: 60_000,
  });

  const relatedDeviceLabel = useMemo(() => {
    if (!relatedDeviceId) return '未关联具体设备';
    const found = registryQuery.data?.devices?.find((device) => device.id === relatedDeviceId);
    return found?.displayName ?? '受影响设备';
  }, [relatedDeviceId, registryQuery.data?.devices]);

  useEffect(() => {
    if (sourceAlarm && canCreate && sourceAlarmQuery.data) {
      const alarm = sourceAlarmQuery.data;
      setCreateValues({
        title: `处理告警：${alarm.title}`,
        description: `来源告警：${alarm.summary}；请前往现场或通过 BMS 系统排查异常原因。`,
        priority: alarm.currentSeverity === 'CRITICAL' ? 'URGENT' : alarm.currentSeverity === 'MAJOR' ? 'HIGH' : 'MEDIUM',
        assigneeId: '',
        teamId: '',
        scheduledStart: '',
        dueAt: '',
      });
      setCreateOpen(true);
    }
  }, [sourceAlarm, canCreate, sourceAlarmQuery.data]);

  const selectWorkOrder = useCallback((id: string) => {
    setSelectedId(id);
    if (typeof window !== 'undefined' && window.history?.replaceState) {
      const url = new URL(window.location.href);
      url.searchParams.set('workOrder', id);
      window.history.replaceState({}, '', url.toString());
    }
  }, []);

  const closeWorkOrder = useCallback(() => {
    setSelectedId(null);
    if (typeof window !== 'undefined' && window.history?.replaceState) {
      const url = new URL(window.location.href);
      url.searchParams.delete('workOrder');
      window.history.replaceState({}, '', url.toString());
    }
  }, []);

  const closeCreate = useCallback(() => {
    setCreateOpen(false);
    if (sourceAlarm && typeof window !== 'undefined' && window.history?.replaceState) {
      const url = new URL(window.location.href);
      url.searchParams.delete('sourceAlarm');
      window.history.replaceState({}, '', url.toString());
    }
  }, [sourceAlarm]);

  const commit = useCallback((workOrder: WorkOrder) => {
    queryClient.setQueryData(['workOrders', 'detail', site.id, workOrder.workOrderId], workOrder);
    void queryClient.invalidateQueries({ queryKey: ['workOrders', 'list', site.id] });
    void queryClient.invalidateQueries({ queryKey: ['workOrders', 'summary', site.id] });
  }, [queryClient, site.id]);

  const createMutation = useMutation({
    mutationFn: (values: CreateValues) => createWorkOrder({
      title: values.title.trim(),
      description: values.description.trim(),
      priority: values.priority,
      assigneeId: values.assigneeId.trim() || null,
      teamId: values.teamId.trim() || null,
      scheduledStart: siteLocalInputToInstant(values.scheduledStart, site.timezone),
      dueAt: siteLocalInputToInstant(values.dueAt, site.timezone),
      sourceReferences: sourceAlarm
        ? [{ domain: 'ALARM', resourceId: sourceAlarm, relationship: 'ORIGIN' }]
        : [{ domain: 'MANUAL', resourceId: `operator:${principal.principal.subject}`, relationship: 'ORIGIN' }],
    }, writeOptions(principal, site.id)),
    onSuccess: (workOrder) => {
      commit(workOrder);
      closeCreate();
      selectWorkOrder(workOrder.workOrderId);
      setSuccessMessage('工单创建成功');
    },
  });

  const assignMutation = useMutation({
    mutationFn: (values: AssignValues) => assignWorkOrder(detail!.workOrderId, {
      expectedVersion: detail!.version,
      assigneeId: values.assigneeId.trim() || null,
      teamId: values.teamId.trim() || null,
      reason: values.reason.trim(),
    }, writeOptions(principal, site.id)),
    onSuccess: (workOrder) => {
      commit(workOrder);
      setAssignOpen(false);
      setAssignValues({ assigneeId: '', teamId: '', reason: '值班人员更新指派' });
      setSuccessMessage('指派已更新');
    },
  });

  const lifecycleMutation = useMutation({
    mutationFn: ({ action, evidence }: { action: LifecycleAction; evidence?: string }) => transitionWorkOrder(
      detail!.workOrderId,
      action,
      {
        expectedVersion: detail!.version,
        reason: `OPERATOR_${action.toUpperCase()}`,
        ...(action === 'complete'
          ? { completionEvidence: [{ kind: 'OPERATOR_NOTE', reference: evidence!, capturedAt: new Date().toISOString() }] }
          : {}),
      },
      writeOptions(principal, site.id),
    ),
    onSuccess: (workOrder, variables) => {
      commit(workOrder);
      setConfirmAction(null);
      if (variables.action === 'complete') {
        setCompletionOpen(false);
        setCompletionEvidence('');
      }
      setSuccessMessage('工单状态已更新');
    },
  });

  const runLifecycle = (action: LifecycleAction) => {
    if (action === 'complete') {
      setCompletionOpen(true);
      return;
    }
    setConfirmAction(action);
  };

  const rows = useMemo(() => listQuery.data?.pages.flatMap((page) => page.items) ?? [], [listQuery.data]);
  const visibleRows = useMemo(
    () => rows.filter((item) => matchesWorkOrderAdvancedFilters(item, advancedFilters, joinOperator)),
    [advancedFilters, joinOperator, rows],
  );

  const columns = useMemo<ColumnDef<DataTableFeatures, WorkOrder>[]>(() => [
    {
      id: 'select',
      header: ({ table }) => (
        <Checkbox
          checked={table.getIsAllPageRowsSelected() ? true : table.getIsSomePageRowsSelected() ? 'indeterminate' : false}
          onCheckedChange={(checked) => table.toggleAllPageRowsSelected(Boolean(checked))}
          aria-label="选择当前页全部工单"
        />
      ),
      cell: ({ row }) => (
        <Checkbox
          checked={row.getIsSelected()}
          onCheckedChange={(checked) => row.toggleSelected(Boolean(checked))}
          onClick={(event) => event.stopPropagation()}
          aria-label={`选择工单 ${row.original.title}`}
        />
      ),
      enableSorting: false,
      enableHiding: false,
    },
    {
      id: 'title',
      accessorFn: (row) => row.title,
      meta: { label: '工单标题' },
      header: ({ column }) => <DataTableColumnHeader column={column} title="工单标题与来源" />,
      cell: ({ row }) => (
        <div className="min-w-[260px]">
          <div className="font-medium text-foreground">{row.original.title}</div>
          <div className="mt-0.5 line-clamp-1 text-xs text-muted-foreground">
            {sourceSummary(row.original)} · {row.original.description}
          </div>
        </div>
      ),
    },
    {
      id: 'priority',
      accessorFn: (row) => row.priority,
      enableColumnFilter: true,
      meta: {
        label: '优先级',
        variant: 'select',
        options: Object.entries(PRIORITY).map(([value, item]) => ({ value, label: item.label })),
      },
      header: ({ column }) => <DataTableColumnHeader column={column} title="优先级" />,
      cell: ({ row }) => (
        <span
          className={cn(
            'inline-flex items-center rounded-full border px-2 py-0.5 text-xs font-medium',
            row.original.priority === 'URGENT'
              ? 'border-rose-200 bg-rose-50 font-semibold text-rose-700 dark:border-rose-800 dark:bg-rose-950/40 dark:text-rose-400'
              : row.original.priority === 'HIGH'
                ? 'border-amber-200 bg-amber-50 font-semibold text-amber-700 dark:border-amber-800 dark:bg-amber-950/40 dark:text-amber-400'
                : 'border-border/70 bg-muted/30 text-muted-foreground',
          )}
        >
          {PRIORITY[row.original.priority].label}
        </span>
      ),
    },
    {
      id: 'status',
      accessorFn: (row) => row.status,
      enableColumnFilter: true,
      meta: {
        label: '状态',
        variant: 'select',
        options: Object.entries(STATUS).map(([value, item]) => ({ value, label: item.label })),
      },
      header: ({ column }) => <DataTableColumnHeader column={column} title="状态" />,
      cell: ({ row }) => (
        <StatusPillBadge
          tone={
            row.original.status === 'COMPLETED'
              ? 'success'
              : row.original.status === 'IN_PROGRESS'
                ? 'in-progress'
                : row.original.status === 'BLOCKED'
                  ? 'warning'
                  : row.original.status === 'CANCELLED'
                    ? 'destructive'
                    : 'neutral'
          }
          pulse={row.original.status === 'IN_PROGRESS'}
        >
          {STATUS[row.original.status].label}
        </StatusPillBadge>
      ),
    },
    {
      id: 'assigneeId',
      accessorFn: (row) => row.assigneeId ?? '',
      enableColumnFilter: true,
      meta: { label: '负责人', placeholder: '输入负责人 ID...', variant: 'text' },
      header: ({ column }) => <DataTableColumnHeader column={column} title="负责人 / 团队" />,
      cell: ({ row }) => row.original.assigneeId ? (
        <span className="font-medium">{row.original.assigneeId}</span>
      ) : row.original.teamId ? (
        <span>{row.original.teamId}</span>
      ) : (
        <span className={isActiveWorkOrder(row.original) ? 'font-medium text-amber-600' : 'text-muted-foreground'}>
          未指派
        </span>
      ),
    },
    {
      id: 'tasks',
      accessorFn: (row) => workOrderProgress(row),
      meta: { label: '子任务进度' },
      header: ({ column }) => <DataTableColumnHeader column={column} title="子任务进度" />,
      cell: ({ row }) => row.original.tasks.total > 0 ? (
        <div className="min-w-[120px] space-y-1">
          <div className="flex justify-between text-xs text-muted-foreground">
            <span>{row.original.tasks.completed}/{row.original.tasks.total}</span>
            <span>{workOrderProgress(row.original)}%</span>
          </div>
          <Progress value={workOrderProgress(row.original)} className="h-1.5" />
        </div>
      ) : (
        <span className="text-xs text-muted-foreground">无子任务</span>
      ),
    },
    {
      id: 'dueAt',
      accessorFn: (row) => row.dueAt ? Date.parse(row.dueAt) : Number.MAX_SAFE_INTEGER,
      meta: { label: '要求完成 (SLA)' },
      header: ({ column }) => <DataTableColumnHeader column={column} title="要求完成 (SLA)" />,
      cell: ({ row }) => {
        const due = dueState(row.original, Date.now());
        return (
          <div className="min-w-[140px]">
            <div className="text-sm tabular-nums">{formatInstant(row.original.dueAt, site.timezone)}</div>
            {due === 'overdue' ? (
              <span className="text-xs font-medium text-destructive">已超期</span>
            ) : due === 'soon' ? (
              <span className="text-xs font-medium text-amber-600">24h 内到期</span>
            ) : null}
          </div>
        );
      },
    },
    {
      id: 'updatedAt',
      accessorFn: (row) => Date.parse(row.updatedAt),
      meta: { label: '更新时间' },
      header: ({ column }) => <DataTableColumnHeader column={column} title="更新时间" />,
      cell: ({ row }) => (
        <span className="min-w-[140px] text-sm text-muted-foreground tabular-nums">
          {formatInstant(row.original.updatedAt, site.timezone)}
        </span>
      ),
    },
  ], [site.timezone]);

  const workOrdersTable = useDataTable({
    key: 'surface-11-work-orders',
    data: visibleRows,
    columns,
    pageSize: 10,
    getRowId: (workOrder) => workOrder.workOrderId,
    meta: {
      queryKeys: {
        page: 'page',
        perPage: 'perPage',
        sort: 'sort',
        filters: 'filters',
        joinOperator: 'joinOperator',
      },
    },
  });

  useEffect(() => {
    workOrdersTable.setPageIndex(0);
  }, [advancedFilters, joinOperator, workOrdersTable]);

  const now = Date.now();
  const summaryRows = summaryQuery.data?.items ?? [];
  const summaryTruncated = Boolean(summaryQuery.data?.hasMore);
  const summaryUnavailable = summaryQuery.isPending || summaryQuery.isError;
  const metricValue = (value: number) => summaryUnavailable ? '—' : lowerBoundCount(value, summaryTruncated);
  const pendingCount = summaryRows.filter((workOrder) => workOrder.status === 'OPEN').length;
  const processingCount = summaryRows.filter((workOrder) => workOrder.status === 'IN_PROGRESS').length;
  const blockedCount = summaryRows.filter((workOrder) => workOrder.status === 'BLOCKED').length;
  const urgentCount = summaryRows.filter((workOrder) => isActiveWorkOrder(workOrder) && workOrder.priority === 'URGENT').length;
  const unassignedCount = summaryRows.filter((workOrder) => isActiveWorkOrder(workOrder) && !workOrder.assigneeId && !workOrder.teamId).length;
  const overdueCount = summaryRows.filter((workOrder) => dueState(workOrder, now) === 'overdue').length;
  const hasLoadedOnlyAdvancedFilter = advancedFilters.length > 0 && !serverFilterPlan.complete;

  const originAlarmHref = originAlarmId
    ? `${siteRoute(site, 'alarms')}?${new URLSearchParams({
      alarm: originAlarmId,
      source: 'work-order',
      tab: originAlarmQuery.data?.condition === 'CLEARED' ? 'recovered' : 'active',
    }).toString()}`
    : '';

  if (!canList) {
    return (
      <Main className="space-y-6">
        <Alert variant="destructive">
          <AlertTitle>当前账号没有工单列表权限</AlertTitle>
          <AlertDescription>请联系系统管理员分配 work-order.list 权限。</AlertDescription>
        </Alert>
      </Main>
    );
  }

  return (
    <Main className="space-y-6">
      {/* 1. Page Header */}
      <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <div className="flex items-center gap-2">
            <h1 className="text-2xl font-bold tracking-tight text-foreground">工单中心</h1>
            <Badge variant="outline" className="text-xs font-normal">
              运维协同
            </Badge>
          </div>
          <p className="mt-1 text-sm text-muted-foreground">
            统一管理暖通空调设备维修、故障排查与预防性保养工单，跟进处理进度与执行反馈
          </p>
        </div>
        <div className="flex items-center gap-2">
          {canCreate ? (
            <Button size="sm" onClick={() => { setCreateValues(createDraft()); setCreateOpen(true); }}>
              <Plus className="mr-1.5 size-4" />
              新建工单
            </Button>
          ) : null}
        </div>
      </div>

      {successMessage ? (
        <Alert>
          <CheckCircle2 className="size-4 text-emerald-600" />
          <AlertTitle>操作成功</AlertTitle>
          <AlertDescription>{successMessage}</AlertDescription>
        </Alert>
      ) : null}

      {sourceAlarm && !canCreate ? (
        <Alert>
          <AlertTitle>当前账号无工单创建权限</AlertTitle>
          <AlertDescription>如需针对该告警发起工单，请联系管理员开通工单创建权限。</AlertDescription>
          <Button variant="outline" size="sm" className="mt-2" asChild>
            <a href={`${siteRoute(site, 'alarms')}?alarm=${encodeURIComponent(sourceAlarm)}`}>返回告警</a>
          </Button>
        </Alert>
      ) : null}

      {/* 2. 6-Metric Standard KPI Strip */}
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-6">
        <Card>
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">待处理</CardTitle>
            <Clock className="size-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold tracking-tight text-foreground tabular-nums">{metricValue(pendingCount)}</div>
            <p className="text-xs text-muted-foreground">等待开始处置</p>
          </CardContent>
        </Card>
        <Card>
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">处理中</CardTitle>
            <Wrench className="size-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold tracking-tight text-foreground tabular-nums">{metricValue(processingCount)}</div>
            <p className="text-xs text-muted-foreground">现场或远程执行中</p>
          </CardContent>
        </Card>
        <Card>
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">阻塞</CardTitle>
            <PauseCircle className="size-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold tracking-tight text-foreground tabular-nums">{metricValue(blockedCount)}</div>
            <p className="text-xs text-muted-foreground">需要解除依赖</p>
          </CardContent>
        </Card>
        <Card>
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">紧急工单</CardTitle>
            <AlertCircle className="size-4 text-rose-500" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold tracking-tight text-rose-600 dark:text-rose-400 tabular-nums">{metricValue(urgentCount)}</div>
            <p className="text-xs text-muted-foreground">活动中的紧急项</p>
          </CardContent>
        </Card>
        <Card>
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">未指派</CardTitle>
            <UserRound className="size-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold tracking-tight text-foreground tabular-nums">{metricValue(unassignedCount)}</div>
            <p className="text-xs text-muted-foreground">无负责人或协作团队</p>
          </CardContent>
        </Card>
        <Card>
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">已超期</CardTitle>
            <Timer className="size-4 text-amber-500" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold tracking-tight text-amber-600 dark:text-amber-400 tabular-nums">{metricValue(overdueCount)}</div>
            <p className="text-xs text-muted-foreground">SLA 超期预警</p>
          </CardContent>
        </Card>
      </div>

      {/* 3. Main Work Orders Ledger */}
      <section className="space-y-3">
        <div className="flex flex-col gap-2 sm:flex-row sm:items-end sm:justify-between">
          <div>
            <h2 className="text-base font-semibold tracking-tight text-foreground">工单台账</h2>
            <p className="text-sm text-muted-foreground">使用排序、高级筛选和列显示控制当前站点工单。</p>
          </div>
          <Button
            variant="outline"
            size="sm"
            className="h-8 px-2.5 text-xs"
            disabled={listQuery.isFetching || summaryQuery.isFetching}
            onClick={() => { void listQuery.refetch(); void summaryQuery.refetch(); }}
          >
            <RefreshCw className={`mr-1.5 size-3.5 ${listQuery.isFetching || summaryQuery.isFetching ? 'animate-spin' : ''}`} />
            刷新
          </Button>
        </div>

        {listQuery.isError && rows.length === 0 ? (
          <Alert variant="destructive">
            <AlertTitle>工单列表不可用</AlertTitle>
            <AlertDescription>{workOrderErrorMessage(listQuery.error)}</AlertDescription>
          </Alert>
        ) : null}

        {hasLoadedOnlyAdvancedFilter ? (
          <Alert>
            <AlertTitle>复杂筛选基于当前已加载工单</AlertTitle>
            <AlertDescription>
              当前服务端仅支持状态、优先级和负责人等值筛选；OR、非等值或空值条件会应用到已加载记录。可继续加载更多工单扩大筛选范围。
            </AlertDescription>
          </Alert>
        ) : null}

        {listQuery.isPending ? (
          <div className="grid min-h-40 place-items-center rounded-md border text-sm text-muted-foreground">正在读取工单…</div>
        ) : (
          <DataTable
            table={workOrdersTable}
            tableAriaLabel="工单台账"
            tableClassName="min-w-[1120px]"
            empty="当前筛选条件下没有工单记录"
            getHeaderCellProps={(header) => ({
              className: header.id === 'select'
                ? 'w-10 text-center'
                : 'whitespace-nowrap',
            })}
            getRowProps={(row) => ({
              'data-state': selectedId === row.original.workOrderId ? 'selected' : undefined,
              className: 'cursor-pointer',
              onClick: () => selectWorkOrder(row.original.workOrderId),
            })}
            getCellProps={(cell) => ({
              className: cell.column.id === 'select' ? 'w-10 text-center' : undefined,
            })}
            footer={(
              <DataTablePagination
                table={workOrdersTable}
                totalRows={visibleRows.length}
              />
            )}
          >
            <DataTableAdvancedToolbar table={workOrdersTable}>
              <DataTableSortList table={workOrdersTable} />
              <DataTableFilterList table={workOrdersTable} />
            </DataTableAdvancedToolbar>
          </DataTable>
        )}

        {listQuery.hasNextPage ? (
          <div className="flex justify-center pt-1">
            <Button
              variant="outline"
              size="sm"
              disabled={listQuery.isFetchingNextPage}
              onClick={() => void listQuery.fetchNextPage()}
            >
              {listQuery.isFetchingNextPage ? '正在加载…' : '加载更多工单'}
            </Button>
          </div>
        ) : null}
      </section>

      {/* 4. Slide-over Sheet for Work Order Detail & Transition */}
      <Sheet open={Boolean(selectedId)} onOpenChange={(open) => { if (!open) closeWorkOrder(); }}>
        <SheetContent className="w-full sm:max-w-2xl overflow-y-auto">
          <SheetHeader>
            <SheetTitle className="text-xl">{detail?.title ?? '工单详情'}</SheetTitle>
            <SheetDescription>
              {detail ? `${sourceSummary(detail)} · 更新于 ${formatInstant(detail.updatedAt, site.timezone)}` : '正在读取工单信息'}
            </SheetDescription>
            {detail ? (
              <div className="mt-2 flex gap-2">
                <Badge variant="outline" className={priorityBadgeClass(detail.priority)}>
                  {PRIORITY[detail.priority].label}
                </Badge>
                <Badge variant="outline" className={statusBadgeClass(detail.status)}>
                  {STATUS[detail.status].label}
                </Badge>
              </div>
            ) : null}
          </SheetHeader>
          <SheetBody className="space-y-6 pt-4">
            {!canRead ? (
              <div className="rounded-md border border-dashed p-5 text-sm text-muted-foreground">当前会话没有工单详情权限</div>
            ) : detailQuery.isPending ? (
              <div className="grid min-h-32 place-items-center text-sm text-muted-foreground">正在读取工单详情…</div>
            ) : detailQuery.isError ? (
              <Alert variant="destructive">
                <AlertTitle>工单详情不可用</AlertTitle>
                <AlertDescription>{workOrderErrorMessage(detailQuery.error)}</AlertDescription>
              </Alert>
            ) : detail ? (
              <div className="space-y-6">
                {/* 工单概览 */}
                <div className="space-y-3">
                  <div className="flex items-center gap-2 text-sm font-semibold text-foreground">
                    <Wrench className="size-4 text-muted-foreground" />
                    工单概览
                  </div>
                  <p className="text-sm leading-relaxed text-muted-foreground bg-muted/30 p-3 rounded-md border">
                    {detail.description}
                  </p>
                  <FactGrid items={[
                    { label: '负责人', value: detail.assigneeId ? detail.assigneeId : '未指派' },
                    { label: '协作团队', value: detail.teamId ? detail.teamId : '未指派' },
                    { label: '计划开始', value: formatInstant(detail.scheduledStart, site.timezone) },
                    { label: '要求完成', value: <span className={dueState(detail, Date.now()) === 'overdue' ? 'text-destructive font-medium' : ''}>{formatInstant(detail.dueAt, site.timezone)}</span> },
                    { label: '创建时间', value: formatInstant(detail.createdAt, site.timezone) },
                  ]} />
                </div>

                {/* 来源告警 / 来源信息 */}
                {originAlarmId ? (
                  <div className="space-y-3">
                    <div className="flex items-center gap-2 text-sm font-semibold text-foreground">
                      <BellRing className="size-4 text-muted-foreground" />
                      来源告警
                    </div>
                    {originAlarmQuery.isPending ? (
                      <span className="text-sm text-muted-foreground">正在读取来源告警…</span>
                    ) : originAlarmQuery.isError ? (
                      <Alert>
                        <AlertTitle>来源告警暂不可用</AlertTitle>
                        <AlertDescription>{alarmErrorMessage(originAlarmQuery.error)}</AlertDescription>
                      </Alert>
                    ) : originAlarmQuery.data ? (
                      <div className="space-y-3 rounded-md border p-3">
                        <div className="flex flex-wrap items-center justify-between gap-2">
                          <div className="flex items-center gap-2">
                            <Badge variant="outline" className={alarmSeverityClass(originAlarmQuery.data.currentSeverity)}>
                              {ALARM_SEVERITY[originAlarmQuery.data.currentSeverity].label}
                            </Badge>
                            <Badge variant="outline">
                              {originAlarmQuery.data.condition === 'ACTIVE' ? '活动' : '已恢复'}
                            </Badge>
                            <span className="text-sm font-medium">{originAlarmQuery.data.title}</span>
                          </div>
                          {originAlarmHref ? (
                            <Button variant="ghost" size="sm" asChild>
                              <a href={originAlarmHref}>打开告警</a>
                            </Button>
                          ) : null}
                        </div>
                        <p className="text-xs text-muted-foreground">{originAlarmQuery.data.summary}</p>
                        <FactGrid items={[
                          { label: '关联设备', value: relatedDeviceLabel },
                          { label: '发生频次', value: `${originAlarmQuery.data.occurrenceCount} 次` },
                          { label: '首次发生', value: formatInstant(originAlarmQuery.data.firstOccurredAt, site.timezone) },
                          { label: '最近发生', value: formatInstant(originAlarmQuery.data.lastOccurredAt, site.timezone) },
                        ]} />
                      </div>
                    ) : null}
                  </div>
                ) : (
                  <div className="space-y-3">
                    <div className="flex items-center gap-2 text-sm font-semibold text-foreground">
                      <Link2 className="size-4 text-muted-foreground" />
                      来源与关联
                    </div>
                    <div className="flex flex-wrap gap-2">
                      {detail.sourceReferences.map((source, index) => (
                        <Badge key={`${source.domain}:${source.relationship}:${index}`} variant="outline">
                          {SOURCE_LABEL[source.domain] ?? '其他来源'} ({source.relationship === 'ORIGIN' ? '起因' : '关联'})
                        </Badge>
                      ))}
                    </div>
                  </div>
                )}

                {/* 任务进度 */}
                <div className="space-y-3">
                  <div className="flex items-center gap-2 text-sm font-semibold text-foreground">
                    <CheckCircle2 className="size-4 text-muted-foreground" />
                    任务进度
                  </div>
                  <div className="rounded-md border p-3 space-y-2">
                    <div className="flex justify-between text-sm">
                      <span>完成进度</span>
                      <span className="font-semibold tabular-nums">{detail.tasks.completed} / {detail.tasks.total}</span>
                    </div>
                    <Progress value={workOrderProgress(detail)} className="h-2" />
                    {detail.tasks.blocked > 0 ? (
                      <p className="text-xs text-amber-600 font-medium">存在 {detail.tasks.blocked} 个阻塞任务</p>
                    ) : null}
                  </div>
                </div>

                {/* 完成证据 */}
                {detail.completionEvidence.length > 0 ? (
                  <div className="space-y-3">
                    <div className="flex items-center gap-2 text-sm font-semibold text-foreground">
                      <CheckCircle2 className="size-4 text-emerald-600" />
                      完成证据与核销说明
                    </div>
                    <div className="space-y-2">
                      {detail.completionEvidence.map((evidence) => (
                        <div key={`${evidence.kind}:${evidence.reference}:${evidence.capturedAt}`} className="rounded-md border p-3 bg-muted/20 space-y-1">
                          <div className="text-xs text-muted-foreground">{formatInstant(evidence.capturedAt, site.timezone)}</div>
                          <div className="text-sm font-medium">{evidence.reference}</div>
                        </div>
                      ))}
                    </div>
                  </div>
                ) : null}

                {/* 处置时间线 */}
                <div className="space-y-3">
                  <div className="flex items-center gap-2 text-sm font-semibold text-foreground">
                    <Clock3 className="size-4 text-muted-foreground" />
                    处置时间线
                  </div>
                  <ol className="space-y-4 border-l border-border pl-4">
                    {[...detail.timeline].reverse().map((event, index) => (
                      <li key={`${event.occurredAt}-${index}`} className="relative">
                        <span className="absolute -left-[21px] top-1.5 size-2.5 rounded-full border-2 border-background bg-primary" />
                        <div className="space-y-0.5">
                          <div className="text-sm font-medium">
                            {operationLabel(event.operation)} · {STATUS[event.toStatus].label}
                          </div>
                          <div className="text-xs text-muted-foreground">{event.reason}</div>
                          <div className="text-[11px] text-muted-foreground tabular-nums">
                            {formatInstant(event.occurredAt, site.timezone)}
                          </div>
                        </div>
                      </li>
                    ))}
                  </ol>
                </div>
              </div>
            ) : null}
          </SheetBody>
          {detail ? (
            <SheetFooter className="gap-2 sm:justify-between border-t pt-4">
              <div className="flex flex-wrap gap-2">
                {canAssign && isActiveWorkOrder(detail) ? (
                  <Button variant="outline" size="sm" onClick={() => { setAssignValues({ assigneeId: detail.assigneeId ?? '', teamId: detail.teamId ?? '', reason: '值班人员更新指派' }); setAssignOpen(true); }}>
                    <UserRoundCog className="mr-1.5 size-3.5" />
                    指派
                  </Button>
                ) : null}
                {relatedDeviceId ? (
                  <Button variant="outline" size="sm" asChild>
                    <a href={`${assetsDevicePath(site.id, relatedDeviceId)}?sourceWorkOrder=${encodeURIComponent(detail.workOrderId)}`}>
                      <Link2 className="mr-1.5 size-3.5" />
                      关联设备
                    </a>
                  </Button>
                ) : null}
              </div>
              <div className="flex flex-wrap gap-2">
                {canLifecycle ? lifecycleActions(detail.status).map((action) => (
                  <Button
                    key={action}
                    size="sm"
                    variant={action === 'cancel' ? 'destructive' : action === 'block' ? 'outline' : 'default'}
                    disabled={lifecycleMutation.isPending}
                    onClick={() => runLifecycle(action)}
                  >
                    {ACTION_LABEL[action]}
                  </Button>
                )) : null}
              </div>
            </SheetFooter>
          ) : null}
        </SheetContent>
      </Sheet>

      {/* 5. Create Work Order Dialog */}
      <Dialog open={createOpen} onOpenChange={(open) => { if (!open) closeCreate(); else setCreateOpen(true); }}>
        <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-2xl">
          <DialogHeader>
            <DialogTitle>{sourceAlarm ? '告警转工单' : '新建工单'}</DialogTitle>
            <DialogDescription>记录处置目标、负责人和计划窗口；创建工单不会改变来源告警的物理状态。</DialogDescription>
          </DialogHeader>

          {createMutation.isError ? (
            <Alert variant="destructive">
              <AlertTitle>创建工单失败</AlertTitle>
              <AlertDescription>{workOrderErrorMessage(createMutation.error)}</AlertDescription>
            </Alert>
          ) : null}

          <div className="space-y-4">
            <div className="space-y-2">
              <label className="text-sm font-medium">工单标题</label>
              <Input
                maxLength={256}
                value={createValues.title}
                onChange={(event) => setCreateValues((cur) => ({ ...cur, title: event.target.value }))}
                placeholder="例如：冷水机组 1# 供水温度偏高现场排查"
              />
            </div>
            <div className="space-y-2">
              <label className="text-sm font-medium">处置说明</label>
              <Textarea
                maxLength={4096}
                rows={3}
                value={createValues.description}
                onChange={(event) => setCreateValues((cur) => ({ ...cur, description: event.target.value }))}
                placeholder="请详述现场异常现象、排查步骤与验收要求…"
              />
            </div>
            <div className="space-y-2">
              <label className="text-sm font-medium">优先级</label>
              <Select value={createValues.priority} onValueChange={(val) => setCreateValues((cur) => ({ ...cur, priority: val as WorkOrderPriority }))}>
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {Object.entries(PRIORITY).map(([val, meta]) => (
                    <SelectItem key={val} value={val}>{meta.label}</SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="grid gap-3 sm:grid-cols-2">
              <div className="space-y-2">
                <label className="text-sm font-medium">负责人账号（可选）</label>
                <Input
                  value={createValues.assigneeId}
                  onChange={(event) => setCreateValues((cur) => ({ ...cur, assigneeId: event.target.value }))}
                  placeholder="例如：operator_zhang"
                />
              </div>
              <div className="space-y-2">
                <label className="text-sm font-medium">协作团队代码（可选）</label>
                <Input
                  value={createValues.teamId}
                  onChange={(event) => setCreateValues((cur) => ({ ...cur, teamId: event.target.value }))}
                  placeholder="例如：HVAC_TEAM_A"
                />
              </div>
            </div>
            <div className="grid gap-3 sm:grid-cols-2">
              <div className="space-y-2">
                <label className="text-sm font-medium">计划开始时间</label>
                <Input
                  type="datetime-local"
                  value={createValues.scheduledStart}
                  onChange={(event) => setCreateValues((cur) => ({ ...cur, scheduledStart: event.target.value }))}
                />
              </div>
              <div className="space-y-2">
                <label className="text-sm font-medium">要求完成时间 (SLA)</label>
                <Input
                  type="datetime-local"
                  value={createValues.dueAt}
                  onChange={(event) => setCreateValues((cur) => ({ ...cur, dueAt: event.target.value }))}
                />
              </div>
            </div>
          </div>

          <DialogFooter>
            <Button variant="outline" onClick={closeCreate}>取消</Button>
            <Button
              disabled={createMutation.isPending || !createValues.title.trim() || !createValues.description.trim()}
              onClick={() => createMutation.mutate(createValues)}
            >
              {createMutation.isPending ? '正在创建…' : '确认创建工单'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* 6. Assign Dialog */}
      <Dialog open={assignOpen} onOpenChange={(open) => { if (!open && !assignMutation.isPending) setAssignOpen(false); }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>指派工单</DialogTitle>
            <DialogDescription>更新主要负责人或协作团队；服务端会再次检查工单版本和权限。</DialogDescription>
          </DialogHeader>
          {assignMutation.isError ? (
            <Alert variant="destructive">
              <AlertTitle>指派失败</AlertTitle>
              <AlertDescription>{workOrderErrorMessage(assignMutation.error)}</AlertDescription>
            </Alert>
          ) : null}
          <div className="space-y-3">
            <div className="space-y-1.5">
              <label className="text-sm font-medium">负责人账号</label>
              <Input
                value={assignValues.assigneeId}
                onChange={(event) => setAssignValues((cur) => ({ ...cur, assigneeId: event.target.value }))}
                placeholder="例如：engineer_li"
              />
            </div>
            <div className="space-y-1.5">
              <label className="text-sm font-medium">协作团队代码</label>
              <Input
                value={assignValues.teamId}
                onChange={(event) => setAssignValues((cur) => ({ ...cur, teamId: event.target.value }))}
                placeholder="例如：MAINTENANCE_GROUP_B"
              />
            </div>
            <div className="space-y-1.5">
              <label className="text-sm font-medium">指派原因</label>
              <Input
                maxLength={256}
                value={assignValues.reason}
                onChange={(event) => setAssignValues((cur) => ({ ...cur, reason: event.target.value }))}
              />
            </div>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setAssignOpen(false)}>取消</Button>
            <Button
              disabled={assignMutation.isPending || !assignValues.reason.trim()}
              onClick={() => assignMutation.mutate(assignValues)}
            >
              {assignMutation.isPending ? '正在保存…' : '保存指派'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* 7. Complete Work Order Dialog */}
      <Dialog open={completionOpen} onOpenChange={(open) => { if (!open && !lifecycleMutation.isPending) setCompletionOpen(false); }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>完成工单</DialogTitle>
            <DialogDescription>完成前必须记录实际处置结果或现场核销证据。</DialogDescription>
          </DialogHeader>
          <Alert>
            <AlertTitle>完成工单不会自动恢复来源告警</AlertTitle>
            <AlertDescription>若工单来源于告警，告警恢复仍由实时数据和告警规则判断。</AlertDescription>
          </Alert>
          {lifecycleMutation.isError ? (
            <Alert variant="destructive">
              <AlertTitle>完成工单失败</AlertTitle>
              <AlertDescription>{workOrderErrorMessage(lifecycleMutation.error)}</AlertDescription>
            </Alert>
          ) : null}
          <div className="space-y-2">
            <label className="text-sm font-medium">处置结果 / 完成证据</label>
            <Textarea
              rows={4}
              maxLength={1024}
              placeholder="例如：现场复核供水温度恢复至 7.0°C，检查传感器无漂移，阀门执行器响应正常。"
              value={completionEvidence}
              onChange={(event) => setCompletionEvidence(event.target.value)}
            />
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setCompletionOpen(false)}>返回</Button>
            <Button
              disabled={lifecycleMutation.isPending || !completionEvidence.trim()}
              onClick={() => lifecycleMutation.mutate({ action: 'complete', evidence: completionEvidence.trim() })}
            >
              {lifecycleMutation.isPending ? '正在完成…' : '确认完成工单'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* 8. Confirm Lifecycle Action Dialog */}
      <Dialog open={Boolean(confirmAction)} onOpenChange={(open) => { if (!open) setConfirmAction(null); }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{confirmAction ? ACTION_LABEL[confirmAction] : '确认操作'}</DialogTitle>
            <DialogDescription>
              {confirmAction === 'cancel'
                ? `取消后「${detail?.title ?? ''}」将停止当前处置流程，后续如需继续必须重新打开。`
                : `确认对「${detail?.title ?? ''}」执行该状态变更？`}
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setConfirmAction(null)}>返回</Button>
            <Button
              variant={confirmAction === 'cancel' ? 'destructive' : 'default'}
              disabled={!confirmAction || lifecycleMutation.isPending}
              onClick={() => { if (confirmAction) lifecycleMutation.mutate({ action: confirmAction }); }}
            >
              {lifecycleMutation.isPending ? '正在处理…' : confirmAction ? ACTION_LABEL[confirmAction] : '确认'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </Main>
  );
}
