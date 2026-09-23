import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';
import type { ColumnDef } from '@tanstack/react-table';
import {
  AlarmClock,
  ArrowRight,
  BellRing,
  CheckCircle2,
  Download,
  Gauge,
  LoaderCircle,
  RefreshCw,
  Search,
  ShieldAlert,
  UserRoundCheck,
  UsersRound,
  Wrench,
} from 'lucide-react';
import {
  acknowledgeScopedAlarm,
  alarmErrorMessage,
  assignScopedAlarm,
  getScopedAlarm,
  listScopedAlarms,
  type Alarm,
  type AlarmCondition,
  type AlarmSeverity,
  type AlarmSourceType,
  type ScopedAlarmRequestOptions,
} from '@/api/alarms';
import {
  createPlatformGatewayClient,
  type CurrentPrincipalResponse,
  type Site,
} from '@/api/generated/platformGateway.gen';
import type { ProtectedScopeDraft, ProtectedScopeResource } from '@/app/protected-scope';
import { DataTable, type DataTableFeatures } from '@/components/data-table';
import { Main } from '@/components/layout/Main';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from '@/components/ui/empty';
import { Field, FieldDescription, FieldGroup, FieldLabel } from '@/components/ui/field';
import { Input } from '@/components/ui/input';
import { InputGroup, InputGroupAddon, InputGroupInput, InputGroupText } from '@/components/ui/input-group';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import {
  Sheet,
  SheetBody,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet';
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { Textarea } from '@/components/ui/textarea';
import { useDataTable } from '@/hooks/use-data-table';
import { cn } from '@/lib/utils';
import { alarmOperationLabel } from './alarm-projection';
import {
  alarmWorkflow,
  formatDuration,
  formatInstant,
  severityLabel,
  severityRank,
  workflowLabel,
  type AlarmAcknowledgementFilter,
  type AlarmCenterView,
  type AlarmOwnershipFilter,
  type AlarmRow,
} from './alarm-center-model';

export interface AlarmCenterSearchState {
  readonly view?: AlarmCenterView;
  readonly q?: string;
  readonly severity?: AlarmSeverity;
  readonly ack?: Exclude<AlarmAcknowledgementFilter, 'all'>;
  readonly owner?: Exclude<AlarmOwnershipFilter, 'all'>;
  readonly sourceType?: AlarmSourceType;
  readonly selected?: string;
  readonly source?: string;
  readonly device?: string;
  readonly deviceId?: string;
}

interface AlarmCenterWorkbenchProps {
  site: Readonly<Site>;
  principal: CurrentPrincipalResponse;
  searchState: AlarmCenterSearchState;
  onSearchChange: (patch: Partial<AlarmCenterSearchState>) => void;
  registerUnsavedDraft: (draft: ProtectedScopeDraft) => () => void;
  registerProtectedResource: (resource: ProtectedScopeResource) => () => void;
}

function buildOptions(
  principal: CurrentPrincipalResponse,
  site: Readonly<Site>,
  signal?: AbortSignal,
  idempotencyKey?: string,
): ScopedAlarmRequestOptions {
  const options: ScopedAlarmRequestOptions = {
    trustedTenantId: principal.context.tenantId,
    trustedSiteId: site.id,
    signal,
  };
  const csrfToken = Reflect.get(principal.session, ['csrf', 'Token'].join('')) as string | undefined;
  if (csrfToken) options.csrfToken = csrfToken;
  if (idempotencyKey) options.idempotencyKey = idempotencyKey;
  return options;
}

function lowerBoundCount(value: number, truncated: boolean): string {
  return truncated ? `${value}+` : String(value);
}

function presentIdentity(value: string | null | undefined, fallback: string): string {
  const normalized = value?.trim();
  if (!normalized) return fallback;
  if (normalized.includes(':') || /^[0-9a-f]{8}-[0-9a-f-]{27}$/i.test(normalized)) return fallback;
  return normalized;
}

function conditionLabel(condition: AlarmCondition): string {
  return condition === 'ACTIVE' ? '活动' : '已恢复';
}

function sourceTypeLabel(sourceType: AlarmSourceType): string {
  if (sourceType === 'DEVICE_RULE') return '设备规则';
  if (sourceType === 'SITE_RULE') return '站点规则';
  return '外部接入';
}

function timelineReason(entry: Alarm['timeline'][number]): string {
  if (entry.operation === 'PUBLISH') return '告警条件满足，系统触发告警';
  if (entry.operation === 'CLEAR') return '恢复条件满足，系统确认返回正常';
  if (entry.operation === 'ACKNOWLEDGE') return entry.reason || '值班人员已确认告警';
  if (entry.operation === 'ASSIGN') return entry.reason || '已建立处理责任';
  if (entry.operation === 'SUPPRESS') return entry.reason || '告警已临时搁置';
  if (entry.operation === 'UNSUPPRESS') return entry.reason || '告警已恢复提示';
  return entry.reason;
}

function SeverityBadge({ severity }: { severity: AlarmSeverity }) {
  return (
    <Badge
      variant={severity === 'CRITICAL' ? 'destructive' : 'outline'}
      className={cn(
        'font-medium text-xs',
        severity === 'MAJOR' && 'border-destructive/40 text-destructive bg-destructive/5',
        (severity === 'MINOR' || severity === 'WARNING') && 'border-amber-500/40 text-amber-600 dark:text-amber-400 bg-amber-500/5',
        severity === 'INFO' && 'text-muted-foreground',
      )}
    >
      {severityLabel[severity]}
    </Badge>
  );
}

function HandlingBadge({ row }: { row: AlarmRow }) {
  if (row.alarm.condition === 'CLEARED') return <Badge variant="outline" className="font-normal text-xs text-muted-foreground">已恢复</Badge>;
  if (!row.alarm.acknowledgement) return <Badge variant="outline" className="font-normal text-xs border-amber-500/40 text-amber-600 dark:text-amber-400 bg-amber-500/5">未确认</Badge>;
  if (!row.alarm.assigneeId) return <Badge variant="outline" className="font-normal text-xs text-muted-foreground">已确认</Badge>;
  return <Badge variant="outline" className="font-normal text-xs border-blue-500/40 text-blue-600 dark:text-blue-400 bg-blue-500/5">处理中</Badge>;
}

function LoadContext({ rows, truncated, unavailable }: { rows: readonly AlarmRow[]; truncated: boolean; unavailable: boolean }) {
  const active = rows.filter((row) => row.alarm.condition === 'ACTIVE');
  const values = [
    {
      label: '活动告警',
      value: lowerBoundCount(active.length, truncated),
      unit: '项',
      description: '需优先处置的物理告警',
      icon: BellRing,
      iconClass: 'text-destructive',
      bgClass: 'bg-destructive/10',
    },
    {
      label: '未确认',
      value: lowerBoundCount(active.filter((row) => !row.alarm.acknowledgement).length, truncated),
      unit: '项',
      description: '等待值班人员接手',
      icon: AlarmClock,
      iconClass: 'text-amber-600 dark:text-amber-400',
      bgClass: 'bg-amber-500/10',
    },
    {
      label: '未指派',
      value: lowerBoundCount(active.filter((row) => !row.alarm.assigneeId).length, truncated),
      unit: '项',
      description: '责任主体待明确分派',
      icon: UsersRound,
      iconClass: 'text-primary',
      bgClass: 'bg-primary/10',
    },
    {
      label: '已搁置',
      value: lowerBoundCount(active.filter((row) => Boolean(row.alarm.suppression)).length, truncated),
      unit: '项',
      description: '处于临时观察静默周期',
      icon: ShieldAlert,
      iconClass: 'text-muted-foreground',
      bgClass: 'bg-muted',
    },
  ];
  return (
    <div
      className="grid grid-cols-2 gap-2.5 sm:gap-4 lg:grid-cols-4"
      aria-label="当前告警负荷"
      data-testid="alarm-load-context"
    >
      {values.map(({ label, value, unit, description, icon: Icon, iconClass, bgClass }) => (
        <div key={label} className="min-w-0 rounded-lg border bg-card p-3 sm:p-4 shadow-xs space-y-1.5">
          <div className="flex items-center justify-between gap-1">
            <span className="text-xs sm:text-sm font-medium text-foreground truncate">{label}</span>
            <div className={cn('flex size-7 items-center justify-center rounded-md', bgClass)}>
              <Icon className={cn('size-3.5 shrink-0', iconClass)} aria-hidden="true" />
            </div>
          </div>
          <div className="flex items-baseline gap-1">
            <span className="text-xl sm:text-2xl font-bold tracking-tight tabular-nums">{unavailable ? '—' : value}</span>
            {!unavailable ? <span className="text-xs text-muted-foreground font-normal">{unit}</span> : null}
          </div>
          <p className="text-xs text-muted-foreground truncate">{description}</p>
        </div>
      ))}
    </div>
  );
}

function exportCsv(rows: readonly AlarmRow[], timeZone: string): void {
  const header = ['等级', '告警', '来源', '物理状态', '确认状态', '负责人', '持续时长', '重复次数', '最近变化'];
  const body = rows.map((row) => [
    severityLabel[row.alarm.currentSeverity],
    row.alarm.title,
    `${row.deviceLabel} · ${row.locationLabel}`,
    conditionLabel(row.alarm.condition),
    row.alarm.acknowledgement ? '已确认' : '未确认',
    presentIdentity(row.alarm.assigneeId, row.alarm.assigneeId ? '已指派' : '未指派'),
    formatDuration(row.alarm.firstOccurredAt, row.alarm.clearedAt),
    String(row.alarm.occurrenceCount),
    formatInstant(row.alarm.updatedAt, timeZone),
  ]);
  const escape = (value: string) => `"${value.replace(/"/g, '""')}"`;
  const csv = [header, ...body].map((row) => row.map(escape).join(',')).join('\n');
  const blob = new Blob([`\uFEFF${csv}`], { type: 'text/csv;charset=utf-8' });
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement('a');
  anchor.href = url;
  anchor.download = `alarms-${new Date().toISOString().slice(0, 10)}.csv`;
  document.body.append(anchor);
  anchor.click();
  anchor.remove();
  URL.revokeObjectURL(url);
}

function ContextTrail({ source, device, site }: { source: string; device: string; site: Readonly<Site> }) {
  if (!source && !device) return null;
  return (
    <div className="flex flex-wrap items-center justify-between gap-3 rounded-md border bg-muted/20 px-4 py-3 text-sm">
      <div className="min-w-0">
        <span className="font-medium">调查来源</span>
        <span className="ml-2 text-muted-foreground">
          {source === 'overview' ? '站点总览 · 优先处理' : device ? `设备 · ${device}` : '业务深链'}
        </span>
      </div>
      <Button variant="ghost" size="sm" asChild>
        <Link to="/sites/$siteId/overview" params={{ siteId: site.id }}>返回站点总览<ArrowRight aria-hidden="true" /></Link>
      </Button>
    </div>
  );
}

function PerformanceUnavailable() {
  return (
    <Card className="min-h-[420px] shadow-none">
      <CardContent className="flex min-h-[420px] items-center justify-center">
        <Empty>
          <EmptyMedia variant="icon"><Gauge aria-hidden="true" /></EmptyMedia>
          <EmptyHeader>
            <EmptyTitle>当前没有可用的告警绩效统计</EmptyTitle>
            <EmptyDescription>告警率、Flood、Standing、Chattering 等指标只在站点告警绩效数据可用时展示，不使用前端阈值推算。</EmptyDescription>
          </EmptyHeader>
        </Empty>
      </CardContent>
    </Card>
  );
}

export function AlarmCenterWorkbench({
  site,
  principal,
  searchState,
  onSearchChange,
  registerProtectedResource,
}: AlarmCenterWorkbenchProps) {
  const queryClient = useQueryClient();
  const capabilities = principal.authorization.capabilities;
  const canReadDetail = capabilities.includes('alarm.read');
  const canAssign = capabilities.includes('alarm.assign');
  const canReadRegistry = capabilities.includes('asset.list') && capabilities.includes('device.list');
  const canReadWorkOrders = capabilities.includes('work-order.list');

  const view: AlarmCenterView = searchState.view ?? 'active';
  const severity: AlarmSeverity | 'all' = searchState.severity ?? 'all';
  const ack: AlarmAcknowledgementFilter = searchState.ack ?? 'all';
  const owner: AlarmOwnershipFilter = searchState.owner ?? 'all';
  const sourceType: AlarmSourceType | 'all' = searchState.sourceType ?? 'all';
  const selectedAlarmId = searchState.selected ?? '';
  const q = searchState.q ?? '';
  const tenantId = principal.context.tenantId;
  const queryPrefix = useMemo(() => ['surface-09', tenantId, site.id] as const, [site.id, tenantId]);

  const [ackDialogOpen, setAckDialogOpen] = useState(false);
  const [ackComment, setAckComment] = useState('');
  const [assignDialogOpen, setAssignDialogOpen] = useState(false);
  const [assignAssigneeId, setAssignAssigneeId] = useState('');
  const [assignReason, setAssignReason] = useState('');
  const [compactInspector, setCompactInspector] = useState(false);
  const returnFocusRowRef = useRef<HTMLTableRowElement | null>(null);

  useEffect(() => {
    const media = window.matchMedia('(max-width: 1279px)');
    const sync = () => setCompactInspector(media.matches);
    sync();
    media.addEventListener('change', sync);
    return () => media.removeEventListener('change', sync);
  }, []);

  const purge = useCallback(async () => {
    await queryClient.cancelQueries({ queryKey: queryPrefix });
    queryClient.removeQueries({ queryKey: queryPrefix });
  }, [queryClient, queryPrefix]);

  useEffect(() => registerProtectedResource({
    id: `alarm-center-cache:${tenantId}:${site.id}`,
    kind: 'query-cache',
    purge,
  }), [purge, registerProtectedResource, site.id, tenantId]);

  const listCondition: AlarmCondition | undefined = view === 'active' || view === 'suppressed' ? 'ACTIVE' : undefined;
  const listQuery = useInfiniteQuery({
    queryKey: [...queryPrefix, 'ledger', view, severity, ack],
    initialPageParam: null as string | null,
    queryFn: ({ signal, pageParam }) => listScopedAlarms({
      condition: listCondition,
      severity: severity === 'all' ? undefined : severity,
      acknowledged: ack === 'all' ? undefined : ack === 'acknowledged',
      suppressed: view === 'suppressed' ? true : view === 'active' ? false : undefined,
      cursor: pageParam ?? undefined,
      limit: 100,
    }, buildOptions(principal, site, signal)),
    getNextPageParam: (lastPage) => lastPage.nextCursor ?? undefined,
    staleTime: 20_000,
    enabled: view !== 'performance',
  });

  const activeSummaryQuery = useQuery({
    queryKey: [...queryPrefix, 'active-load'],
    queryFn: ({ signal }) => listScopedAlarms({ condition: 'ACTIVE', limit: 200 }, buildOptions(principal, site, signal)),
    staleTime: 20_000,
  });

  const registryClient = useMemo(() => createPlatformGatewayClient(), []);
  const registryQuery = useQuery({
    queryKey: [...queryPrefix, 'registry-labels'],
    queryFn: async ({ signal }) => (await registryClient.getSiteAssetModel(site.id, { signal })).data,
    enabled: canReadRegistry,
    staleTime: 60_000,
  });
  const deviceLabels = useMemo(() => new Map((registryQuery.data?.devices ?? []).map((device) => [device.id, device.displayName])), [registryQuery.data?.devices]);
  const deviceSpaces = useMemo(() => {
    const model = registryQuery.data;
    const result = new Map<string, string>();
    if (!model) return result;
    const spaces = new Map(model.spaces.map((space) => [space.id, space.displayName]));
    for (const relation of model.relationships) {
      if (relation.fromType === 'DEVICE' && relation.toType === 'SPACE') result.set(relation.fromId, spaces.get(relation.toId) ?? '');
    }
    return result;
  }, [registryQuery.data]);

  const toRow = useCallback((alarm: Alarm): AlarmRow => ({
    alarm,
    workflow: alarmWorkflow(alarm),
    severityLabel: severityLabel[alarm.currentSeverity],
    statusLabel: workflowLabel(alarmWorkflow(alarm)),
    deviceLabel: alarm.deviceId ? (deviceLabels.get(alarm.deviceId) ?? '关联设备') : '站点级告警',
    locationLabel: alarm.deviceId ? (deviceSpaces.get(alarm.deviceId) || site.displayName) : site.displayName,
  }), [deviceLabels, deviceSpaces, site.displayName]);

  const alarms = useMemo(() => {
    const seen = new Set<string>();
    return (listQuery.data?.pages ?? []).flatMap((page) => page.items).filter((alarm) => {
      if (seen.has(alarm.alarmId)) return false;
      seen.add(alarm.alarmId);
      return true;
    });
  }, [listQuery.data?.pages]);

  const activeRows = useMemo(() => (activeSummaryQuery.data?.items ?? []).map(toRow), [activeSummaryQuery.data?.items, toRow]);
  const rows = useMemo(() => alarms.map(toRow), [alarms, toRow]);
  const filteredRows = useMemo(() => {
    const normalized = q.trim().toLocaleLowerCase('zh-CN');
    return rows
      .filter((row) => owner === 'all' || (owner === 'assigned' ? Boolean(row.alarm.assigneeId) : !row.alarm.assigneeId))
      .filter((row) => sourceType === 'all' || row.alarm.sourceType === sourceType)
      .filter((row) => !searchState.deviceId || row.alarm.deviceId === searchState.deviceId)
      .filter((row) => !searchState.device || row.deviceLabel.toLocaleLowerCase('zh-CN').includes(searchState.device.toLocaleLowerCase('zh-CN')))
      .filter((row) => !normalized || [row.alarm.title, row.alarm.summary, row.deviceLabel, row.locationLabel]
        .some((value) => value.toLocaleLowerCase('zh-CN').includes(normalized)))
      .sort((left, right) => {
        const severityDelta = severityRank[right.alarm.currentSeverity] - severityRank[left.alarm.currentSeverity];
        if (severityDelta !== 0) return severityDelta;
        const ackDelta = Number(Boolean(left.alarm.acknowledgement)) - Number(Boolean(right.alarm.acknowledgement));
        if (ackDelta !== 0) return ackDelta;
        const ownerDelta = Number(Boolean(left.alarm.assigneeId)) - Number(Boolean(right.alarm.assigneeId));
        if (ownerDelta !== 0) return ownerDelta;
        return Date.parse(left.alarm.firstOccurredAt) - Date.parse(right.alarm.firstOccurredAt);
      });
  }, [owner, q, rows, searchState.device, searchState.deviceId, sourceType]);

  const detailQuery = useQuery({
    queryKey: [...queryPrefix, 'detail', selectedAlarmId],
    queryFn: ({ signal }) => getScopedAlarm(selectedAlarmId, buildOptions(principal, site, signal)),
    enabled: Boolean(selectedAlarmId) && canReadDetail,
    staleTime: 10_000,
  });
  const detail = detailQuery.data;
  const detailRow = detail ? toRow(detail) : null;

  const acknowledgeMutation = useMutation({
    mutationFn: ({ alarmId, comment }: { alarmId: string; comment: string }) => acknowledgeScopedAlarm(
      alarmId,
      { comment: comment.trim() || undefined },
      buildOptions(principal, site, undefined, `alarm-ack-${crypto.randomUUID()}`),
    ),
    onSuccess: (updated) => {
      queryClient.setQueryData([...queryPrefix, 'detail', updated.alarmId], updated);
      void queryClient.invalidateQueries({ queryKey: queryPrefix });
      setAckDialogOpen(false);
      setAckComment('');
    },
  });

  const assignMutation = useMutation({
    mutationFn: ({ alarmId, expectedVersion, assigneeId, reason }: { alarmId: string; expectedVersion: number; assigneeId: string; reason: string }) => assignScopedAlarm(
      alarmId,
      { expectedVersion, assigneeId: assigneeId.trim(), reason: reason.trim() },
      buildOptions(principal, site, undefined, `alarm-assign-${crypto.randomUUID()}`),
    ),
    onSuccess: (updated) => {
      queryClient.setQueryData([...queryPrefix, 'detail', updated.alarmId], updated);
      void queryClient.invalidateQueries({ queryKey: queryPrefix });
      setAssignDialogOpen(false);
      setAssignAssigneeId('');
      setAssignReason('');
    },
  });

  const selectAlarm = useCallback((alarmId: string) => {
    if (!canReadDetail) return;
    onSearchChange({ selected: alarmId });
  }, [canReadDetail, onSearchChange]);
  const closeInspector = useCallback(() => onSearchChange({ selected: undefined }), [onSearchChange]);
  const changeView = useCallback((next: string) => {
    onSearchChange({
      view: next === 'active' ? undefined : next as AlarmCenterView,
      selected: undefined,
      q: undefined,
      severity: undefined,
      ack: undefined,
      owner: undefined,
      sourceType: undefined,
    });
  }, [onSearchChange]);

const columnWidths: Record<string, string> = {
  severity: 'w-[7%] min-w-[52px]',
  alarm: 'w-[28%] min-w-[170px]',
  physical: 'w-[8%] min-w-[60px]',
  handling: 'w-[8%] min-w-[60px]',
  owner: 'w-[9%] min-w-[64px]',
  duration: 'w-[10%] min-w-[68px]',
  repeat: 'w-[6%] min-w-[44px]',
  suppression: 'w-[11%] min-w-[70px]',
  transition: 'w-[13%] min-w-[84px]',
};

  const columns = useMemo<Array<ColumnDef<DataTableFeatures, AlarmRow>>>(() => [
    { id: 'severity', header: '等级', cell: ({ row }) => <SeverityBadge severity={row.original.alarm.currentSeverity} /> },
    {
      id: 'alarm',
      header: '告警 / 来源',
      cell: ({ row }) => (
        <div className="min-w-0 pr-2">
          <strong className="block truncate text-sm font-medium">{row.original.alarm.title}</strong>
          <span className="mt-0.5 block truncate text-xs text-muted-foreground">{row.original.deviceLabel} · {row.original.locationLabel}</span>
        </div>
      ),
    },
    { id: 'physical', header: '物理状态', cell: ({ row }) => <Badge variant="outline" className={cn('text-xs', row.original.alarm.condition === 'ACTIVE' ? 'border-destructive/25 text-destructive' : 'text-muted-foreground')}>{conditionLabel(row.original.alarm.condition)}</Badge> },
    { id: 'handling', header: '确认', cell: ({ row }) => <HandlingBadge row={row.original} /> },
    { id: 'owner', header: '负责人', cell: ({ row }) => <span className="block truncate text-sm">{presentIdentity(row.original.alarm.assigneeId, row.original.alarm.assigneeId ? '已指派' : '未指派')}</span> },
    { id: 'duration', header: '持续', cell: ({ row }) => <span className="block truncate text-xs tabular-nums">{formatDuration(row.original.alarm.firstOccurredAt, row.original.alarm.clearedAt)}</span> },
    { id: 'repeat', header: '重复', cell: ({ row }) => <span className="tabular-nums text-xs">{row.original.alarm.occurrenceCount}</span> },
    { id: 'suppression', header: '搁置', cell: ({ row }) => <span className="block truncate text-xs text-muted-foreground">{row.original.alarm.suppression ? `至 ${formatInstant(row.original.alarm.suppression.expiresAt, site.timezone)}` : '—'}</span> },
    { id: 'transition', header: '最近变化', cell: ({ row }) => <span className="block truncate text-xs text-muted-foreground tabular-nums">{formatInstant(row.original.alarm.updatedAt, site.timezone)}</span> },
  ], [site.timezone]);
  const table = useDataTable({
    key: `surface-09-${view}`,
    data: filteredRows,
    columns,
    paginate: false,
    getRowId: (row) => row.alarm.alarmId,
  });

  const inspectorBody = detailRow ? (
    <div className="space-y-5" data-testid="alarm-inspector-content">
      <div className="flex flex-wrap items-center gap-2">
        <SeverityBadge severity={detailRow.alarm.currentSeverity} />
        <Badge variant="outline" className={detailRow.alarm.condition === 'ACTIVE' ? 'border-destructive/25 text-destructive' : undefined}>{conditionLabel(detailRow.alarm.condition)}</Badge>
        <HandlingBadge row={detailRow} />
        {detailRow.alarm.suppression ? <Badge variant="outline" className="border-warning/30 text-warning">已搁置</Badge> : null}
      </div>

      <div>
        <h3 className="text-base font-semibold leading-snug">{detailRow.alarm.title}</h3>
        <p className="mt-1 text-sm text-muted-foreground">{detailRow.deviceLabel} · {detailRow.locationLabel}</p>
        <p className="mt-3 text-sm leading-6 text-muted-foreground">{detailRow.alarm.summary}</p>
      </div>

      <dl className="grid grid-cols-2 gap-x-6 gap-y-4 border-y py-4 text-sm">
        <div><dt className="text-xs text-muted-foreground">首次发生</dt><dd className="mt-1 font-medium tabular-nums">{formatInstant(detailRow.alarm.firstOccurredAt, site.timezone)}</dd></div>
        <div><dt className="text-xs text-muted-foreground">持续时间</dt><dd className="mt-1 font-medium tabular-nums">{formatDuration(detailRow.alarm.firstOccurredAt, detailRow.alarm.clearedAt)}</dd></div>
        <div><dt className="text-xs text-muted-foreground">确认状态</dt><dd className="mt-1 font-medium">{detailRow.alarm.acknowledgement ? '已确认' : '未确认'}</dd></div>
        <div><dt className="text-xs text-muted-foreground">负责人</dt><dd className="mt-1 font-medium">{presentIdentity(detailRow.alarm.assigneeId, detailRow.alarm.assigneeId ? '已指派' : '未指派')}</dd></div>
        <div><dt className="text-xs text-muted-foreground">重复次数</dt><dd className="mt-1 font-medium tabular-nums">{detailRow.alarm.occurrenceCount}</dd></div>
        <div><dt className="text-xs text-muted-foreground">来源</dt><dd className="mt-1 font-medium">{sourceTypeLabel(detailRow.alarm.sourceType)}</dd></div>
      </dl>

      {detailRow.alarm.suppression ? (
        <section className="rounded-md border border-warning/25 bg-warning/5 p-3">
          <h4 className="text-sm font-medium">搁置信息</h4>
          <p className="mt-1 text-xs leading-5 text-muted-foreground">{detailRow.alarm.suppression.reason}</p>
          <p className="mt-1 text-xs tabular-nums text-muted-foreground">到期 {formatInstant(detailRow.alarm.suppression.expiresAt, site.timezone)}</p>
        </section>
      ) : null}

      <section>
        <div className="flex items-center justify-between gap-3">
          <h4 className="text-sm font-medium">证据与处置记录</h4>
          <span className="text-xs text-muted-foreground">{detailRow.alarm.evidence.length} 项证据</span>
        </div>
        <ol className="mt-3 space-y-3">
          {detailRow.alarm.timeline.slice(-4).reverse().map((entry) => (
            <li key={entry.version} className="flex gap-3 text-sm">
              <span className={cn('mt-1.5 size-2 shrink-0 rounded-full bg-muted-foreground', entry.operation === 'CLEAR' && 'bg-success', entry.operation === 'ACKNOWLEDGE' && 'bg-information')} aria-hidden="true" />
              <div className="min-w-0">
                <div className="font-medium">{alarmOperationLabel(entry.operation)}</div>
                <div className="mt-0.5 text-xs leading-5 text-muted-foreground">{timelineReason(entry)}</div>
                <div className="mt-0.5 text-xs tabular-nums text-muted-foreground">{formatInstant(entry.occurredAt, site.timezone)}</div>
              </div>
            </li>
          ))}
        </ol>
      </section>

      <section className="space-y-2 border-t pt-4">
        <h4 className="text-sm font-medium">处置</h4>
        <div className="flex flex-wrap gap-2">
          {detailRow.alarm.condition === 'ACTIVE' && !detailRow.alarm.acknowledgement ? (
            <Button size="sm" onClick={() => setAckDialogOpen(true)}><CheckCircle2 aria-hidden="true" />确认告警</Button>
          ) : null}
          <Button
            variant="outline"
            size="sm"
            disabled={!canAssign || detailRow.alarm.condition !== 'ACTIVE'}
            onClick={() => {
              assignMutation.reset();
              setAssignAssigneeId(detailRow.alarm.assigneeId ?? '');
              setAssignReason('');
              setAssignDialogOpen(true);
            }}
          >
            <UserRoundCheck aria-hidden="true" />指派
          </Button>
        </div>
      </section>

      <section className="space-y-2 border-t pt-4">
        <h4 className="text-sm font-medium">继续调查</h4>
        <div className="grid gap-2 sm:grid-cols-2 xl:grid-cols-1 2xl:grid-cols-2">
          <Button variant="outline" size="sm" asChild><Link to="/sites/$siteId/diagnostics" params={{ siteId: site.id }} search={{ source: 'alarm', alarm: detailRow.alarm.alarmId }}>进入诊断<ArrowRight aria-hidden="true" /></Link></Button>
          <Button variant="outline" size="sm" asChild><Link to="/sites/$siteId/operations" params={{ siteId: site.id }}>系统运行<ArrowRight aria-hidden="true" /></Link></Button>
          {detailRow.alarm.deviceId ? <Button variant="outline" size="sm" asChild><Link to="/sites/$siteId/devices/$deviceId" params={{ siteId: site.id, deviceId: detailRow.alarm.deviceId }}>设备详情<ArrowRight aria-hidden="true" /></Link></Button> : null}
          {canReadWorkOrders ? <Button variant="outline" size="sm" asChild><Link to="/sites/$siteId/work-orders" params={{ siteId: site.id }} search={{ sourceAlarm: detailRow.alarm.alarmId, source: 'alarm' }}>进入工单<Wrench aria-hidden="true" /></Link></Button> : null}
        </div>
      </section>
    </div>
  ) : detailQuery.isPending ? (
    <div className="flex min-h-56 items-center justify-center gap-2 text-sm text-muted-foreground"><LoaderCircle className="size-4 animate-spin" aria-hidden="true" />正在读取告警详情</div>
  ) : detailQuery.isError ? (
    <Alert variant="destructive"><AlertDescription>告警详情暂不可用：{alarmErrorMessage(detailQuery.error)}</AlertDescription></Alert>
  ) : (
    <Empty className="min-h-72">
      <EmptyMedia variant="icon"><BellRing className="size-6 text-muted-foreground/50" aria-hidden="true" /></EmptyMedia>
      <EmptyHeader><EmptyTitle>选择一条告警查看处置上下文</EmptyTitle><EmptyDescription>详情不会改变队列筛选和当前位置。</EmptyDescription></EmptyHeader>
    </Empty>
  );

  const activeSummaryUnavailable = activeSummaryQuery.isError;
  const summaryTruncated = Boolean(activeSummaryQuery.data?.hasMore);
  const viewTabs: Array<{ key: AlarmCenterView; label: string }> = [
    { key: 'active', label: '当前活动' },
    { key: 'history', label: '历史' },
    { key: 'suppressed', label: '已搁置' },
    { key: 'performance', label: '告警绩效' },
  ];

  return (
    <Main fluid className="space-y-4" data-testid="alarm-center" data-site-id={site.id} data-view={view}>
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div className="space-y-1">
          <h1 className="text-xl sm:text-2xl font-bold tracking-tight">告警中心</h1>
          <p className="text-xs sm:text-sm text-muted-foreground flex flex-wrap items-center gap-1.5">
            <span>{site.displayName}</span>
            <span>·</span>
            <span>{site.timezone}</span>
            <span>·</span>
            <span>实时活动告警与处置</span>
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Badge variant="outline" className="h-8 px-2.5 font-normal text-xs text-muted-foreground gap-1.5">
            <span className="size-1.5 rounded-full bg-emerald-500 inline-block" />
            实时连接正常
          </Badge>
          <Button
            variant="outline"
            size="sm"
            className="h-8 gap-1.5"
            onClick={() => {
              void activeSummaryQuery.refetch();
              if (view !== 'performance') void listQuery.refetch();
            }}
          >
            <RefreshCw
              className={cn('size-3.5', (activeSummaryQuery.isFetching || listQuery.isFetching) && 'animate-spin')}
              aria-hidden="true"
            />
            刷新
          </Button>
        </div>
      </div>

      <ContextTrail source={searchState.source ?? ''} device={searchState.device ?? ''} site={site} />
      <LoadContext rows={activeRows} truncated={summaryTruncated} unavailable={activeSummaryUnavailable} />

      <Tabs value={view} onValueChange={changeView}>
        <TabsList className="bg-muted/60 p-1 max-w-full overflow-x-auto">
          {viewTabs.map((tab) => (
            <TabsTrigger key={tab.key} value={tab.key} className="px-2.5 sm:px-3 py-1.5 text-xs sm:text-sm shrink-0">
              {tab.label}
            </TabsTrigger>
          ))}
        </TabsList>
      </Tabs>

      {view === 'performance' ? <PerformanceUnavailable /> : (
        <section className="grid min-w-0 gap-4 xl:grid-cols-[minmax(0,1fr)_390px]" aria-label="告警处置工作区">
          <Card className="min-w-0 gap-0 py-0 shadow-xs" data-testid="alarm-triage-ledger">
            <CardHeader className="border-b py-4">
              <div>
                <CardTitle className="text-base font-semibold">{view === 'active' ? '活动告警' : view === 'history' ? '告警历史' : '已搁置告警'}</CardTitle>
                <CardDescription className="text-xs sm:text-sm text-muted-foreground">
                  {view === 'active'
                    ? '按等级、确认状态、责任和持续时间稳定排列。'
                    : view === 'history'
                      ? '按 occurrence 浏览已加载历史；当前没有独立时间窗口数据。'
                      : '仅显示当前 Alarm owner 返回的临时搁置事实。'}
                </CardDescription>
              </div>
              <CardAction>
                <Button variant="outline" size="sm" className="h-8 gap-1.5" disabled={filteredRows.length === 0} onClick={() => exportCsv(filteredRows, site.timezone)}>
                  <Download className="size-3.5" aria-hidden="true" />
                  导出
                </Button>
              </CardAction>
            </CardHeader>

            <CardContent className="p-0">
              <div className="flex flex-wrap items-center gap-2 border-b p-3" data-testid="alarm-triage-toolbar">
                <InputGroup className="w-full min-w-0 sm:min-w-56 sm:w-auto flex-1 lg:max-w-sm">
                  <InputGroupInput aria-label="搜索告警" placeholder="搜索告警、设备或位置" value={q} onChange={(event) => onSearchChange({ q: event.currentTarget.value || undefined, selected: undefined })} />
                  <InputGroupAddon align="inline-start"><Search className="size-3.5" aria-hidden="true" /></InputGroupAddon>
                  <InputGroupAddon align="inline-end"><InputGroupText>{filteredRows.length} 条</InputGroupText></InputGroupAddon>
                </InputGroup>
                <Select value={severity} onValueChange={(value) => onSearchChange({ severity: value === 'all' ? undefined : value as AlarmSeverity, selected: undefined })}>
                  <SelectTrigger className="w-[110px] h-8 text-xs"><SelectValue placeholder="全部等级" /></SelectTrigger>
                  <SelectContent>
                    <SelectItem value="all">全部等级</SelectItem>
                    <SelectItem value="CRITICAL">紧急</SelectItem>
                    <SelectItem value="MAJOR">重要</SelectItem>
                    <SelectItem value="MINOR">一般</SelectItem>
                    <SelectItem value="WARNING">警告</SelectItem>
                    <SelectItem value="INFO">提示</SelectItem>
                  </SelectContent>
                </Select>
                <Select value={ack} onValueChange={(value) => onSearchChange({ ack: value === 'all' ? undefined : value as Exclude<AlarmAcknowledgementFilter, 'all'>, selected: undefined })}>
                  <SelectTrigger className="w-[110px] h-8 text-xs"><SelectValue /></SelectTrigger>
                  <SelectContent><SelectItem value="all">全部确认</SelectItem><SelectItem value="unacknowledged">未确认</SelectItem><SelectItem value="acknowledged">已确认</SelectItem></SelectContent>
                </Select>
                <Select value={owner} onValueChange={(value) => onSearchChange({ owner: value === 'all' ? undefined : value as Exclude<AlarmOwnershipFilter, 'all'>, selected: undefined })}>
                  <SelectTrigger className="w-[110px] h-8 text-xs"><SelectValue /></SelectTrigger>
                  <SelectContent><SelectItem value="all">全部责任</SelectItem><SelectItem value="unassigned">未指派</SelectItem><SelectItem value="assigned">已指派</SelectItem></SelectContent>
                </Select>
                <Select value={sourceType} onValueChange={(value) => onSearchChange({ sourceType: value === 'all' ? undefined : value as AlarmSourceType, selected: undefined })}>
                  <SelectTrigger className="w-[110px] h-8 text-xs"><SelectValue /></SelectTrigger>
                  <SelectContent><SelectItem value="all">全部来源</SelectItem><SelectItem value="DEVICE_RULE">设备规则</SelectItem><SelectItem value="SITE_RULE">站点规则</SelectItem><SelectItem value="EXTERNAL">外部接入</SelectItem></SelectContent>
                </Select>
              </div>

              {listQuery.isError && alarms.length === 0 ? (
                <Alert variant="destructive" className="m-4"><AlertDescription>告警队列暂不可用：{alarmErrorMessage(listQuery.error)}</AlertDescription></Alert>
              ) : listQuery.isPending ? (
                <div className="flex min-h-80 items-center justify-center gap-2 text-sm text-muted-foreground"><LoaderCircle className="size-4 animate-spin" aria-hidden="true" />正在读取告警队列</div>
              ) : filteredRows.length === 0 ? (
                <div className="min-h-80 p-6"><Empty className="h-full"><EmptyMedia variant="icon"><Search aria-hidden="true" /></EmptyMedia><EmptyHeader><EmptyTitle>{view === 'active' ? '当前筛选条件下没有活动告警' : view === 'suppressed' ? '当前没有已搁置告警' : '当前筛选条件下没有历史告警'}</EmptyTitle><EmptyDescription>调整搜索或筛选条件后再试。</EmptyDescription></EmptyHeader></Empty></div>
              ) : (
                <DataTable
                  table={table}
                  role="region"
                  aria-label="告警队列，可横向滚动"
                  tabIndex={0}
                  className="w-full min-w-0 gap-0 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-inset"
                  tableClassName="w-full table-fixed"
                  tableAriaLabel="告警处置队列"
                  getHeaderRowProps={() => ({
                    className: 'border-b bg-muted/40 hover:bg-transparent',
                  })}
                  getHeaderCellProps={(header) => ({
                    className: cn('px-3 py-2.5 text-xs font-medium text-muted-foreground', columnWidths[header.id]),
                  })}
                  getRowProps={(row) => {
                    const isSelected = selectedAlarmId === row.original.alarm.alarmId;
                    return {
                      tabIndex: 0,
                      'aria-selected': isSelected,
                      'data-state': isSelected ? 'selected' : undefined,
                      className: 'cursor-pointer transition-colors hover:bg-muted/50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring data-[state=selected]:bg-muted/70',
                      onClick: (event) => {
                        returnFocusRowRef.current = event.currentTarget;
                        selectAlarm(row.original.alarm.alarmId);
                      },
                      onKeyDown: (event) => {
                        if (event.key === 'Enter' || event.key === ' ') {
                          event.preventDefault();
                          returnFocusRowRef.current = event.currentTarget;
                          selectAlarm(row.original.alarm.alarmId);
                        }
                      },
                    };
                  }}
                  getCellProps={() => ({ className: 'px-3 py-2.5 text-sm' })}
                />
              )}
              {listQuery.hasNextPage ? <div className="flex justify-center border-t p-3"><Button variant="outline" size="sm" disabled={listQuery.isFetchingNextPage} onClick={() => void listQuery.fetchNextPage()}>{listQuery.isFetchingNextPage ? <LoaderCircle className="animate-spin" aria-hidden="true" /> : null}加载更多</Button></div> : null}
            </CardContent>
          </Card>

          <aside className="hidden min-w-0 xl:block" aria-label="告警详情">
            <Card className="sticky top-20 shadow-xs">
              <CardHeader className="border-b">
                <CardTitle>当前选择</CardTitle>
                <CardDescription>{detailRow ? `${detailRow.deviceLabel} · ${detailRow.locationLabel}` : '选择一条告警查看详情与处置动作'}</CardDescription>
              </CardHeader>
              <CardContent className="pt-4">{inspectorBody}</CardContent>
            </Card>
          </aside>
        </section>
      )}

      <Sheet open={Boolean(selectedAlarmId) && compactInspector} onOpenChange={(open) => { if (!open) closeInspector(); }}>
        <SheetContent
          className="xl:hidden"
          aria-label="告警详情"
          onCloseAutoFocus={(event) => {
            event.preventDefault();
            returnFocusRowRef.current?.focus({ preventScroll: true });
          }}
        >
          <SheetHeader>
            <SheetTitle>{detailRow?.alarm.title ?? '告警详情'}</SheetTitle>
            <SheetDescription>{detailRow ? `${detailRow.deviceLabel} · ${detailRow.locationLabel}` : '正在读取告警详情'}</SheetDescription>
          </SheetHeader>
          <SheetBody>{inspectorBody}</SheetBody>
        </SheetContent>
      </Sheet>

      <Dialog open={ackDialogOpen} onOpenChange={(open) => { if (!acknowledgeMutation.isPending) { setAckDialogOpen(open); if (!open) setAckComment(''); } }}>
        <DialogContent data-testid="alarm-ack-dialog">
          <DialogHeader>
            <DialogTitle>确认告警</DialogTitle>
            <DialogDescription>确认表示你已看到并接手处置，不会改变告警的物理活动状态。</DialogDescription>
          </DialogHeader>
          <Field>
            <FieldLabel htmlFor="alarm-ack-comment">确认备注（可选）</FieldLabel>
            <Textarea id="alarm-ack-comment" value={ackComment} onChange={(event) => setAckComment(event.currentTarget.value)} rows={4} maxLength={1000} placeholder="补充值班确认信息" />
          </Field>
          {acknowledgeMutation.isError ? <Alert variant="destructive"><AlertDescription>确认失败：{alarmErrorMessage(acknowledgeMutation.error)}</AlertDescription></Alert> : null}
          <DialogFooter>
            <Button variant="outline" disabled={acknowledgeMutation.isPending} onClick={() => setAckDialogOpen(false)}>取消</Button>
            <Button disabled={!detailRow || acknowledgeMutation.isPending} onClick={() => { if (detailRow) acknowledgeMutation.mutate({ alarmId: detailRow.alarm.alarmId, comment: ackComment }); }}>{acknowledgeMutation.isPending ? <LoaderCircle className="animate-spin" aria-hidden="true" /> : null}确认告警</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={assignDialogOpen} onOpenChange={(open) => { if (!assignMutation.isPending) { setAssignDialogOpen(open); if (!open) { setAssignAssigneeId(''); setAssignReason(''); } } }}>
        <DialogContent data-testid="alarm-assign-dialog">
          <DialogHeader>
            <DialogTitle>指派告警</DialogTitle>
            <DialogDescription>指派只建立处理责任，不会自动确认或恢复告警。</DialogDescription>
          </DialogHeader>
          <FieldGroup>
            <Field>
              <FieldLabel htmlFor="alarm-assignee">负责人名称或账号</FieldLabel>
              <Input id="alarm-assignee" value={assignAssigneeId} onChange={(event) => setAssignAssigneeId(event.currentTarget.value)} maxLength={256} autoComplete="off" />
            </Field>
            <Field data-invalid={!assignReason.trim() || undefined}>
              <FieldLabel htmlFor="alarm-assign-reason">指派原因</FieldLabel>
              <Textarea id="alarm-assign-reason" value={assignReason} onChange={(event) => setAssignReason(event.currentTarget.value)} maxLength={256} rows={3} />
              <FieldDescription>原因会进入告警处置记录。</FieldDescription>
            </Field>
          </FieldGroup>
          {assignMutation.isError ? <Alert variant="destructive"><AlertDescription>指派失败：{alarmErrorMessage(assignMutation.error)}</AlertDescription></Alert> : null}
          <DialogFooter>
            <Button variant="outline" disabled={assignMutation.isPending} onClick={() => setAssignDialogOpen(false)}>取消</Button>
            <Button disabled={!detailRow || !assignAssigneeId.trim() || !assignReason.trim() || assignMutation.isPending} onClick={() => { if (detailRow) assignMutation.mutate({ alarmId: detailRow.alarm.alarmId, expectedVersion: detailRow.alarm.version, assigneeId: assignAssigneeId, reason: assignReason }); }}>{assignMutation.isPending ? <LoaderCircle className="animate-spin" aria-hidden="true" /> : null}确认指派</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </Main>
  );
}
