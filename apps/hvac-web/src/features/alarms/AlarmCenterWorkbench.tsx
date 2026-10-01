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
import { DataTableBlock } from '@/blocks/data-table';
import { FactStrip } from '@/blocks/fact-strip';
import { listSiteFDDFindings, type FDDFinding } from '@/api/intelligence';
import {
  getIssueInvestigation,
  getIssuePerformance,
  getIssueQueue,
  type IssuePerformancePeriod,
  type IssueQueueItem,
} from '@/api/issues';
import {
  createPlatformGatewayClient,
  type CurrentPrincipalResponse,
  type Site,
} from '@/api/generated/platformGateway.gen';
import { createFrontendReviewAssetsRegistry } from '@/app/frontend-review-assets-data';
import type { ProtectedScopeDraft, ProtectedScopeResource } from '@/app/protected-scope';
import { DataTable, DataTableAdvancedToolbar, type DataTableFeatures } from '@/components/data-table';
import { Main } from '@/components/layout/Main';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
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
  SelectGroup,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group';
import {
  Timeline,
  TimelineConnector,
  TimelineContent,
  TimelineDescription,
  TimelineHeader,
  TimelineItem,
  TimelineMarker,
  TimelineRail,
  TimelineTime,
  TimelineTitle,
} from '@/components/ui/timeline';
import { Textarea } from '@/components/ui/textarea';
import { useDataTable } from '@/hooks/use-data-table';
import { cn } from '@/lib/utils';
import { OperationalDetailSheet } from '@/shared/ui';
import { alarmOperationLabel } from './alarm-projection';
import { AlarmDiagnosisPanel } from './AlarmDiagnosisPanel';
import { AlarmPerformanceView } from './AlarmPerformanceView';
import { AlarmFilterPopover, type IssueImpactFilter } from './IssueFilters';
import { ProblemQueueView } from './ProblemQueueView';
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
  readonly alarmView?: AlarmCenterView;
  readonly q?: string;
  readonly severity?: AlarmSeverity;
  readonly ack?: Exclude<AlarmAcknowledgementFilter, 'all'>;
  readonly owner?: Exclude<AlarmOwnershipFilter, 'all'>;
  readonly sourceType?: AlarmSourceType;
  readonly selected?: string;
  readonly source?: string;
  readonly device?: string;
  readonly deviceId?: string;
  readonly performancePeriod?: IssuePerformancePeriod;
  readonly queueMode?: 'problems' | 'alarms';
  readonly selectedIssue?: string;
  readonly issueState?: IssueQueueItem['state'];
  readonly diagnosis?: IssueQueueItem['diagnosisState'];
  readonly impact?: IssueImpactFilter;
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

function DiagnosisBadge({ finding }: { finding?: FDDFinding }) {
  if (!finding) {
    return (
      <Badge variant="outline" className="gap-1 font-normal text-xs text-muted-foreground">
        <Search className="size-3" aria-hidden="true" />
        待诊断
      </Badge>
    );
  }
  if (finding.qualityBlocker) {
    return (
      <Badge variant="outline" className="gap-1 border-amber-500/40 bg-amber-500/5 font-normal text-xs text-amber-700 dark:text-amber-400">
        <ShieldAlert className="size-3" aria-hidden="true" />
        证据待核实
      </Badge>
    );
  }
  return (
    <Badge variant="outline" className="gap-1 border-emerald-500/30 bg-emerald-500/5 font-normal text-xs text-emerald-700 dark:text-emerald-400">
      <CheckCircle2 className="size-3" aria-hidden="true" />
      已有诊断
    </Badge>
  );
}

function LoadContext({ rows, truncated, unavailable }: { rows: readonly AlarmRow[]; truncated: boolean; unavailable: boolean }) {
  const active = rows.filter((row) => row.alarm.condition === 'ACTIVE');
  return (
    <div data-testid="alarm-load-context">
      <FactStrip
        ariaLabel="当前告警负荷"
        items={[
          {
            key: 'active',
            label: '活动告警',
            value: unavailable ? '—' : lowerBoundCount(active.length, truncated),
            suffix: unavailable ? undefined : '项',
            icon: <BellRing />,
            tone: active.length > 0 ? 'critical' : 'default',
          },
          {
            key: 'unacknowledged',
            label: '未确认',
            value: unavailable ? '—' : lowerBoundCount(active.filter((row) => !row.alarm.acknowledgement).length, truncated),
            suffix: unavailable ? undefined : '项',
            icon: <AlarmClock />,
            tone: active.some((row) => !row.alarm.acknowledgement) ? 'warning' : 'default',
          },
          {
            key: 'unassigned',
            label: '未指派',
            value: unavailable ? '—' : lowerBoundCount(active.filter((row) => !row.alarm.assigneeId).length, truncated),
            suffix: unavailable ? undefined : '项',
            icon: <UsersRound />,
          },
          {
            key: 'shelved',
            label: '已搁置',
            value: unavailable ? '—' : lowerBoundCount(active.filter((row) => Boolean(row.alarm.suppression)).length, truncated),
            suffix: unavailable ? undefined : '项',
            icon: <ShieldAlert />,
          },
        ]}
      />
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
        <span className="font-medium">来自</span>
        <span className="ml-2 text-muted-foreground">
          {source === 'overview' ? '站点总览 · 优先处理' : device ? `设备 · ${device}` : '其他页面'}
        </span>
      </div>
      <Button variant="ghost" size="sm" asChild>
        <Link to="/sites/$siteId/overview" params={{ siteId: site.id }}>返回站点总览<ArrowRight aria-hidden="true" /></Link>
      </Button>
    </div>
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

  const view: AlarmCenterView = searchState.alarmView ?? 'active';
  const severity: AlarmSeverity | 'all' = searchState.severity ?? 'all';
  const ack: AlarmAcknowledgementFilter = searchState.ack ?? 'all';
  const owner: AlarmOwnershipFilter = searchState.owner ?? 'all';
  const sourceType: AlarmSourceType | 'all' = searchState.sourceType ?? 'all';
  const selectedAlarmId = searchState.selected ?? '';
  const selectedIssueId = searchState.selectedIssue ?? '';
  const queueMode = searchState.queueMode ?? 'problems';
  const issueState = searchState.issueState;
  const diagnosis = searchState.diagnosis;
  const impact = searchState.impact;
  const performancePeriod: IssuePerformancePeriod = searchState.performancePeriod ?? '30d';
  const q = searchState.q ?? '';
  const tenantId = principal.context.tenantId;
  const queryPrefix = useMemo(() => ['surface-09', tenantId, site.id] as const, [site.id, tenantId]);

  const [ackDialogOpen, setAckDialogOpen] = useState(false);
  const [ackComment, setAckComment] = useState('');
  const [assignDialogOpen, setAssignDialogOpen] = useState(false);
  const [assignAssigneeId, setAssignAssigneeId] = useState('');
  const [assignReason, setAssignReason] = useState('');
  const returnFocusRowRef = useRef<HTMLTableRowElement | null>(null);

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
    enabled: view !== 'performance' && queueMode === 'alarms',
  });

  const activeSummaryQuery = useQuery({
    queryKey: [...queryPrefix, 'active-load'],
    queryFn: ({ signal }) => listScopedAlarms({ condition: 'ACTIVE', limit: 200 }, buildOptions(principal, site, signal)),
    staleTime: 20_000,
  });

  const findingsQuery = useQuery({
    queryKey: [...queryPrefix, 'diagnostic-findings'],
    queryFn: ({ signal }) => listSiteFDDFindings(site.id, signal),
    staleTime: 30_000,
  });
  const findingsByAlarmId = useMemo(() => {
    const result = new Map<string, FDDFinding>();
    for (const finding of findingsQuery.data ?? []) {
      if (finding.alarmId) result.set(finding.alarmId, finding);
    }
    return result;
  }, [findingsQuery.data]);

  const issueScope = view === 'history' ? 'all' : view === 'suppressed' ? 'suppressed' : 'active';
  const issueQueueQuery = useQuery({
    queryKey: [...queryPrefix, 'problem-queue', issueScope],
    queryFn: ({ signal }) => getIssueQueue(site.id, { scope: issueScope }, signal),
    enabled: view !== 'performance' && queueMode === 'problems',
    staleTime: 20_000,
  });

  const investigationQuery = useQuery({
    queryKey: [...queryPrefix, 'investigation', selectedAlarmId],
    queryFn: ({ signal }) => getIssueInvestigation(site.id, selectedAlarmId, signal),
    enabled: Boolean(selectedAlarmId),
    staleTime: 30_000,
  });

  const performanceQuery = useQuery({
    queryKey: [...queryPrefix, 'performance', performancePeriod],
    queryFn: ({ signal }) => getIssuePerformance(site.id, performancePeriod, signal),
    enabled: view === 'performance',
    staleTime: 60_000,
  });

  const registryClient = useMemo(() => createPlatformGatewayClient(), []);
  const registryQuery = useQuery({
    queryKey: [...queryPrefix, 'registry-labels'],
    queryFn: async ({ signal }) => (typeof __HVAC_WEB_FRONTEND_REVIEW__ !== 'undefined' && __HVAC_WEB_FRONTEND_REVIEW__)
      ? createFrontendReviewAssetsRegistry(tenantId, site.id).assetModel
      : (await registryClient.getSiteAssetModel(site.id, { signal })).data,
    enabled: canReadRegistry,
    staleTime: 60_000,
  });
  const deviceLabels = useMemo(() => new Map((registryQuery.data?.devices ?? []).map((device) => [device.id, device.displayName])), [registryQuery.data?.devices]);
  const deviceSpaces = useMemo(() => {
    const model = registryQuery.data;
    const result = new Map<string, string>();
    if (!model) return result;
    const spaces = new Map(model.spaces.map((space) => [space.id, space.displayName]));
    const assetSpaces = new Map<string, string>();
    for (const relation of model.relationships) {
      if (relation.fromType === 'ASSET' && relation.toType === 'SPACE') {
        assetSpaces.set(relation.fromId, spaces.get(relation.toId) ?? '');
      }
      if (relation.fromType === 'DEVICE' && relation.toType === 'SPACE') {
        result.set(relation.fromId, spaces.get(relation.toId) ?? '');
      }
    }
    for (const relation of model.relationships) {
      if (relation.fromType === 'DEVICE' && relation.toType === 'ASSET') {
        const location = assetSpaces.get(relation.toId);
        if (location && !result.has(relation.fromId)) result.set(relation.fromId, location);
      }
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
    onSearchChange({ selected: alarmId, selectedIssue: undefined });
  }, [canReadDetail, onSearchChange]);
  const selectProblem = useCallback((item: IssueQueueItem) => {
    if (!canReadDetail) return;
    onSearchChange({ selected: item.alarmIds[0], selectedIssue: item.issueId });
  }, [canReadDetail, onSearchChange]);
  const closeDetailSheet = useCallback(() => onSearchChange({ selected: undefined, selectedIssue: undefined }), [onSearchChange]);
  const changeMode = useCallback((next: string) => {
    onSearchChange({
      alarmView: next === 'performance' ? 'performance' : undefined,
      selected: undefined,
      selectedIssue: undefined,
      q: undefined,
      severity: undefined,
      ack: undefined,
      owner: undefined,
      sourceType: undefined,
    });
  }, [onSearchChange]);
  const changeQueueView = useCallback((next: string) => {
    onSearchChange({
      alarmView: next === 'active' ? undefined : next as Exclude<AlarmCenterView, 'performance'>,
      selected: undefined,
      selectedIssue: undefined,
    });
  }, [onSearchChange]);

const columnWidths: Record<string, string> = {
  severity: 'w-[7%] min-w-[52px]',
  alarm: 'w-[23%] min-w-[170px]',
  physical: 'w-[8%] min-w-[60px]',
  handling: 'w-[8%] min-w-[60px]',
  diagnosis: 'w-[10%] min-w-[76px]',
  owner: 'w-[8%] min-w-[64px]',
  duration: 'w-[8%] min-w-[68px]',
  repeat: 'w-[5%] min-w-[44px]',
  suppression: 'w-[10%] min-w-[70px]',
  transition: 'w-[13%] min-w-[84px]',
};

  const columns = useMemo<Array<ColumnDef<DataTableFeatures, AlarmRow>>>(() => [
    {
      id: 'severity',
      accessorFn: (row) => row.alarm.currentSeverity,
      meta: { label: '等级' },
      header: '等级',
      cell: ({ row }) => <SeverityBadge severity={row.original.alarm.currentSeverity} />,
    },
    {
      id: 'alarm',
      accessorFn: (row) => row.alarm.title,
      meta: { label: '告警 / 来源' },
      header: '告警 / 来源',
      cell: ({ row }) => (
        <div className="min-w-0 pr-2">
          <strong className="block truncate text-sm font-medium">{row.original.alarm.title}</strong>
          <span className="mt-0.5 block truncate text-xs text-muted-foreground">{row.original.deviceLabel} · {row.original.locationLabel}</span>
        </div>
      ),
    },
    {
      id: 'physical',
      accessorFn: (row) => row.alarm.condition,
      meta: { label: '物理状态' },
      header: '物理状态',
      cell: ({ row }) => <Badge variant="outline" className={cn('text-xs', row.original.alarm.condition === 'ACTIVE' ? 'border-destructive/25 text-destructive' : 'text-muted-foreground')}>{conditionLabel(row.original.alarm.condition)}</Badge>,
    },
    {
      id: 'handling',
      accessorFn: (row) => row.alarm.acknowledgement ? 'acknowledged' : 'unacknowledged',
      meta: { label: '确认' },
      header: '确认',
      cell: ({ row }) => <HandlingBadge row={row.original} />,
    },
    {
      id: 'diagnosis',
      accessorFn: (row) => {
        const finding = findingsByAlarmId.get(row.alarm.alarmId);
        return finding ? (finding.qualityBlocker ? 'blocked' : 'published') : 'pending';
      },
      meta: { label: '诊断' },
      header: '诊断',
      cell: ({ row }) => <DiagnosisBadge finding={findingsByAlarmId.get(row.original.alarm.alarmId)} />,
    },
    {
      id: 'owner',
      accessorFn: (row) => row.alarm.assigneeId ?? '',
      meta: { label: '负责人' },
      header: '负责人',
      cell: ({ row }) => <span className="block truncate text-sm">{presentIdentity(row.original.alarm.assigneeId, row.original.alarm.assigneeId ? '已指派' : '未指派')}</span>,
    },
    {
      id: 'duration',
      accessorFn: (row) => Date.parse(row.alarm.firstOccurredAt),
      meta: { label: '持续' },
      header: '持续',
      cell: ({ row }) => <span className="block truncate text-xs tabular-nums">{formatDuration(row.original.alarm.firstOccurredAt, row.original.alarm.clearedAt)}</span>,
    },
    {
      id: 'repeat',
      accessorFn: (row) => row.alarm.occurrenceCount,
      meta: { label: '重复' },
      header: '重复',
      cell: ({ row }) => <span className="tabular-nums text-xs">{row.original.alarm.occurrenceCount}</span>,
    },
    {
      id: 'suppression',
      accessorFn: (row) => row.alarm.suppression?.expiresAt ?? '',
      meta: { label: '搁置' },
      header: '搁置',
      cell: ({ row }) => <span className="block truncate text-xs text-muted-foreground">{row.original.alarm.suppression ? `至 ${formatInstant(row.original.alarm.suppression.expiresAt, site.timezone)}` : '—'}</span>,
    },
    {
      id: 'transition',
      accessorFn: (row) => Date.parse(row.alarm.updatedAt),
      meta: { label: '最近变化' },
      header: '最近变化',
      cell: ({ row }) => <span className="block truncate text-xs text-muted-foreground tabular-nums">{formatInstant(row.original.alarm.updatedAt, site.timezone)}</span>,
    },
  ], [findingsByAlarmId, site.timezone]);
  const table = useDataTable({
    key: `surface-09-${view}`,
    data: filteredRows,
    columns,
    paginate: false,
    getRowId: (row) => row.alarm.alarmId,
  });

  const detailFinding = detailRow ? findingsByAlarmId.get(detailRow.alarm.alarmId) : undefined;
  const selectedProblem = selectedIssueId
    ? issueQueueQuery.data?.items.find((item) => item.issueId === selectedIssueId)
    : undefined;

  const detailSheetBody = detailRow ? (
    <div className="space-y-5" data-testid="alarm-detail-sheet-content">
      <div className="flex flex-wrap items-center gap-2">
        <Badge variant="outline" className={detailRow.alarm.condition === 'ACTIVE' ? 'border-destructive/25 text-destructive' : undefined}>
          {conditionLabel(detailRow.alarm.condition)}
        </Badge>
        <HandlingBadge row={detailRow} />
        <DiagnosisBadge finding={detailFinding} />
        {detailRow.alarm.suppression ? <Badge variant="outline" className="border-amber-500/30 text-amber-700 dark:text-amber-400">已搁置</Badge> : null}
      </div>

      {selectedProblem ? (
        <section className="space-y-3 rounded-lg border bg-muted/15 p-3" aria-labelledby="problem-context-heading">
          <div className="flex flex-wrap items-start justify-between gap-3">
            <div className="min-w-0">
              <h4 id="problem-context-heading" className="text-sm font-semibold">
                {selectedProblem.alarmIds.length > 1 ? `${selectedProblem.alarmIds.length} 条相关告警一起处理` : '当前处理问题'}
              </h4>
              <p className="mt-1 text-xs leading-5 text-muted-foreground">{selectedProblem.grouping.reason}</p>
            </div>
            <Badge variant="outline" className="shrink-0">
              {selectedProblem.state === 'INVESTIGATING' ? '排查中' : selectedProblem.state === 'ACTION_PENDING' ? '待执行' : selectedProblem.state === 'VERIFYING' ? '验证中' : selectedProblem.state === 'RESOLVED' ? '已解决' : '待处理'}
            </Badge>
          </div>
          {selectedProblem.signals.length > 1 ? (
            <div className="divide-y overflow-hidden rounded-md border bg-background">
              {selectedProblem.signals.map((signal) => (
                <button
                  key={signal.alarmId}
                  type="button"
                  className="flex w-full items-center justify-between gap-3 px-3 py-2 text-left hover:bg-muted/40"
                  onClick={() => onSearchChange({ selected: signal.alarmId, selectedIssue: selectedProblem.issueId })}
                >
                  <span className="min-w-0">
                    <span className="block truncate text-sm font-medium">{signal.title}</span>
                    <span className="mt-0.5 block text-xs text-muted-foreground">
                      {signal.condition === 'ACTIVE' ? '活动中' : '已恢复'} · {signal.acknowledged ? '已确认' : '未确认'}
                    </span>
                  </span>
                  <span className="shrink-0 text-xs text-muted-foreground">{severityLabel[signal.severity]}</span>
                </button>
              ))}
            </div>
          ) : null}
          {selectedProblem.nextAction ? (
            <p className="text-xs leading-5"><strong className="font-medium">下一步：</strong>{selectedProblem.nextAction}</p>
          ) : null}
        </section>
      ) : null}

      <section aria-labelledby="issue-summary-heading">
        <h4 id="issue-summary-heading" className="text-sm font-semibold">当前告警</h4>
        <p className="mt-2 text-sm leading-6">{detailRow.alarm.summary}</p>
        <p className="mt-2 text-xs text-muted-foreground">
          {sourceTypeLabel(detailRow.alarm.sourceType)} · 最近变化 {formatInstant(detailRow.alarm.updatedAt, site.timezone)}
        </p>
      </section>

      <dl className="grid overflow-hidden rounded-lg border text-sm sm:grid-cols-2">
        <div className="border-b p-3 sm:border-r">
          <dt className="text-xs text-muted-foreground">首次发生</dt>
          <dd className="mt-1 font-medium tabular-nums">{formatInstant(detailRow.alarm.firstOccurredAt, site.timezone)}</dd>
        </div>
        <div className="border-b p-3">
          <dt className="text-xs text-muted-foreground">持续时间</dt>
          <dd className="mt-1 font-medium tabular-nums">{formatDuration(detailRow.alarm.firstOccurredAt, detailRow.alarm.clearedAt)}</dd>
        </div>
        <div className="border-b p-3 sm:border-r">
          <dt className="text-xs text-muted-foreground">负责人</dt>
          <dd className="mt-1 font-medium">{presentIdentity(detailRow.alarm.assigneeId, detailRow.alarm.assigneeId ? '已指派' : '未指派')}</dd>
        </div>
        <div className="border-b p-3">
          <dt className="text-xs text-muted-foreground">重复发生</dt>
          <dd className="mt-1 font-medium tabular-nums">{detailRow.alarm.occurrenceCount} 次</dd>
        </div>
        <div className="p-3 sm:border-r">
          <dt className="text-xs text-muted-foreground">确认状态</dt>
          <dd className="mt-1 font-medium">{detailRow.alarm.acknowledgement ? '已确认' : '未确认'}</dd>
        </div>
        <div className="p-3">
          <dt className="text-xs text-muted-foreground">诊断状态</dt>
          <dd className="mt-1 font-medium">{detailFinding ? (detailFinding.qualityBlocker ? '已发布 · 证据待核实' : '已发布') : '待诊断'}</dd>
        </div>
      </dl>

      {detailRow.alarm.suppression ? (
        <section className="rounded-lg border border-amber-500/25 bg-amber-500/5 p-3">
          <h4 className="text-sm font-medium">当前已搁置</h4>
          <p className="mt-1 text-xs leading-5 text-muted-foreground">{detailRow.alarm.suppression.reason}</p>
          <p className="mt-1 text-xs tabular-nums text-muted-foreground">到期 {formatInstant(detailRow.alarm.suppression.expiresAt, site.timezone)}</p>
        </section>
      ) : null}

      <AlarmDiagnosisPanel
        alarm={detailRow.alarm}
        finding={detailFinding}
        investigation={investigationQuery.data}
        investigationLoading={investigationQuery.isPending}
        timeZone={site.timezone}
      />

      {investigationQuery.data?.relatedIssues.length ? (
        <section className="space-y-3 border-t pt-5" aria-labelledby="related-issues-heading">
          <div>
            <h4 id="related-issues-heading" className="text-sm font-semibold">关联问题</h4>
            <p className="mt-0.5 text-xs text-muted-foreground">同一事件、上下游设备或重复模式中的相关告警，帮助避免逐条孤立排查。</p>
          </div>
          <div className="divide-y overflow-hidden rounded-lg border">
            {investigationQuery.data.relatedIssues.map((related) => (
              <button
                key={related.alarmId}
                type="button"
                className="flex w-full items-center justify-between gap-3 px-3 py-2.5 text-left hover:bg-muted/40"
                onClick={() => selectAlarm(related.alarmId)}
              >
                <span className="min-w-0">
                  <span className="block truncate text-sm font-medium">{related.title}</span>
                  <span className="mt-0.5 block text-xs text-muted-foreground">
                    {related.relationship === 'SAME_INCIDENT' ? '同一事件' : related.relationship === 'UPSTREAM' ? '上游关联' : related.relationship === 'DOWNSTREAM' ? '下游关联' : related.relationship === 'SAME_EQUIPMENT' ? '同一设备' : '重复模式'}
                  </span>
                </span>
                <span className="shrink-0 text-xs tabular-nums text-muted-foreground">{formatInstant(related.occurredAt, site.timezone)}</span>
              </button>
            ))}
          </div>
        </section>
      ) : null}

      <section className="space-y-3 border-t pt-5" aria-labelledby="alarm-evidence-heading">
        <div className="flex items-start justify-between gap-3">
          <div>
            <h4 id="alarm-evidence-heading" className="text-sm font-semibold">证据</h4>
            <p className="mt-0.5 text-xs text-muted-foreground">先看事实来源，再判断诊断结论是否值得继续验证。</p>
          </div>
          <Badge variant="outline">{detailRow.alarm.evidence.length} 项</Badge>
        </div>

        {detailRow.alarm.evidence.length ? (
          <div className="divide-y overflow-hidden rounded-lg border">
            {detailRow.alarm.evidence.slice(0, 3).map((evidence, index) => (
              <div key={`${evidence.kind}-${evidence.reference}-${index}`} className="flex items-center justify-between gap-4 px-3 py-2.5">
                <div className="min-w-0">
                  <p className="truncate text-sm font-medium">{evidence.kind}</p>
                  <p className="mt-0.5 text-xs text-muted-foreground">已保留来源引用与采集时间</p>
                </div>
                <time className="shrink-0 text-xs tabular-nums text-muted-foreground">
                  {formatInstant(evidence.capturedAt, site.timezone)}
                </time>
              </div>
            ))}
            {detailRow.alarm.evidence.length > 3 ? (
              <div className="px-3 py-2 text-xs text-muted-foreground">
                另有 {detailRow.alarm.evidence.length - 3} 项证据保留在该告警记录中
              </div>
            ) : null}
          </div>
        ) : (
          <div className="rounded-lg border border-dashed px-3 py-6 text-center text-sm text-muted-foreground">
            当前告警记录没有可展示的证据引用
          </div>
        )}
      </section>

      <section className="space-y-3 border-t pt-5" aria-labelledby="alarm-timeline-heading">
        <div>
          <h4 id="alarm-timeline-heading" className="text-sm font-semibold">状态与处置时间线</h4>
          <p className="mt-0.5 text-xs text-muted-foreground">物理状态、确认和责任变化分别记录，不互相替代。</p>
        </div>
        <Timeline aria-label="告警状态与处置时间线">
          {detailRow.alarm.timeline.slice().reverse().slice(0, 6).map((entry, index) => (
            <TimelineItem key={entry.version} status={index === 0 ? 'current' : 'default'}>
              <TimelineRail>
                <TimelineMarker>
                  {entry.operation === 'CLEAR' || entry.operation === 'ACKNOWLEDGE' ? (
                    <CheckCircle2 aria-hidden="true" />
                  ) : entry.operation === 'ASSIGN' ? (
                    <UserRoundCheck aria-hidden="true" />
                  ) : entry.operation === 'SUPPRESS' ? (
                    <ShieldAlert aria-hidden="true" />
                  ) : (
                    <BellRing aria-hidden="true" />
                  )}
                </TimelineMarker>
                <TimelineConnector />
              </TimelineRail>
              <TimelineContent>
                <TimelineHeader>
                  <TimelineTitle>{alarmOperationLabel(entry.operation)}</TimelineTitle>
                  <TimelineTime dateTime={entry.occurredAt}>{formatInstant(entry.occurredAt, site.timezone)}</TimelineTime>
                </TimelineHeader>
                <TimelineDescription>{timelineReason(entry)}</TimelineDescription>
                {entry.assigneeId ? (
                  <span className="text-xs text-muted-foreground">负责人：{presentIdentity(entry.assigneeId, '已指派')}</span>
                ) : null}
              </TimelineContent>
            </TimelineItem>
          ))}
        </Timeline>
      </section>

      <section className="space-y-3 border-t pt-5">
        <div>
          <h4 className="text-sm font-semibold">继续调查</h4>
          <p className="mt-0.5 text-xs text-muted-foreground">带着当前对象和时间窗口进入更适合的专业页面。</p>
        </div>
        <div className="grid gap-2 sm:grid-cols-2">
          <Button variant="outline" size="sm" asChild>
            <Link
              to="/sites/$siteId/operations/trends"
              params={{ siteId: site.id }}
              search={{
                timeStart: detailRow.alarm.firstOccurredAt,
                timeEnd: detailRow.alarm.clearedAt ?? detailRow.alarm.updatedAt,
                eventTypes: 'alarm',
              }}
            >
              查看趋势<ArrowRight aria-hidden="true" />
            </Link>
          </Button>
          <Button variant="outline" size="sm" asChild>
            <Link to="/sites/$siteId/operations" params={{ siteId: site.id }}>系统运行<ArrowRight aria-hidden="true" /></Link>
          </Button>
          {detailRow.alarm.deviceId ? (
            <Button variant="outline" size="sm" asChild>
              <Link to="/sites/$siteId/devices/$deviceId" params={{ siteId: site.id, deviceId: detailRow.alarm.deviceId }}>设备详情<ArrowRight aria-hidden="true" /></Link>
            </Button>
          ) : null}
          {detailFinding?.qualityBlocker ? (
            <Button variant="outline" size="sm" asChild>
              <Link to="/sites/$siteId/data-quality" params={{ siteId: site.id }}>数据质量<ArrowRight aria-hidden="true" /></Link>
            </Button>
          ) : null}
          {selectedProblem ? (
            <Button variant="outline" size="sm" asChild>
              <Link
                to="/sites/$siteId/issues/$issueId"
                params={{ siteId: site.id, issueId: selectedProblem.issueId }}
                search={{ ...searchState, selected: undefined, selectedIssue: undefined }}
              >
                打开完整详情<ArrowRight aria-hidden="true" />
              </Link>
            </Button>
          ) : null}
          {canReadWorkOrders ? (
            <Button variant="outline" size="sm" asChild>
              <Link to="/sites/$siteId/work-orders" params={{ siteId: site.id }} search={{ sourceAlarm: detailRow.alarm.alarmId, source: 'alarm' }}>
                进入工单<Wrench aria-hidden="true" />
              </Link>
            </Button>
          ) : null}
        </div>
      </section>
    </div>
  ) : selectedAlarmId && detailQuery.isPending ? (
    <div className="flex min-h-56 items-center justify-center gap-2 text-sm text-muted-foreground"><LoaderCircle className="size-4 animate-spin" aria-hidden="true" />正在读取问题详情</div>
  ) : selectedAlarmId && detailQuery.isError ? (
    <Alert variant="destructive"><AlertDescription>问题详情暂不可用：{alarmErrorMessage(detailQuery.error)}</AlertDescription></Alert>
  ) : (
    <Empty className="min-h-72">
      <EmptyMedia variant="icon"><BellRing className="size-6 text-muted-foreground/50" aria-hidden="true" /></EmptyMedia>
      <EmptyHeader><EmptyTitle>选择一个问题或告警查看详情</EmptyTitle><EmptyDescription>这里会连续显示当前状态、诊断结果、证据、原因判断和下一步。</EmptyDescription></EmptyHeader>
    </Empty>
  );

  const detailSheetFooter = detailRow?.alarm.condition === 'ACTIVE' ? (
    <>
      {canAssign ? (
        <Button
          variant="outline"
          size="sm"
          onClick={() => {
            assignMutation.reset();
            setAssignAssigneeId(detailRow.alarm.assigneeId ?? '');
            setAssignReason('');
            setAssignDialogOpen(true);
          }}
        >
          <UserRoundCheck aria-hidden="true" />指派
        </Button>
      ) : null}
      {!detailRow.alarm.acknowledgement ? (
        <Button size="sm" onClick={() => setAckDialogOpen(true)}>
          <CheckCircle2 aria-hidden="true" />确认告警
        </Button>
      ) : null}
    </>
  ) : undefined;

  const activeSummaryUnavailable = activeSummaryQuery.isError;
  const summaryTruncated = Boolean(activeSummaryQuery.data?.hasMore);
  const workspaceMode = view === 'performance' ? 'performance' : 'operations';

  return (
    <Main fluid className="space-y-4" data-testid="alarm-center" data-site-id={site.id} data-view={view}>
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div className="space-y-1">
          
          <p className="text-xs sm:text-sm text-muted-foreground flex flex-wrap items-center gap-1.5">
            <span>{site.displayName}</span>
            <span>·</span>
            <span>{site.timezone}</span>
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button
            variant="outline"
            size="sm"
            className="h-8 gap-1.5"
            onClick={() => {
              if (view === 'performance') {
                void performanceQuery.refetch();
              } else if (queueMode === 'problems') {
                void issueQueueQuery.refetch();
              } else {
                void activeSummaryQuery.refetch();
                void listQuery.refetch();
              }
            }}
          >
            <RefreshCw
              className={cn(
                'size-3.5',
                (view === 'performance'
                  ? performanceQuery.isFetching
                  : queueMode === 'problems'
                    ? issueQueueQuery.isFetching
                    : activeSummaryQuery.isFetching || listQuery.isFetching) && 'animate-spin',
              )}
              aria-hidden="true"
            />
            刷新
          </Button>
        </div>
      </div>

      <ContextTrail source={searchState.source ?? ''} device={searchState.device ?? ''} site={site} />

      <Tabs value={workspaceMode} onValueChange={changeMode}>
        <TabsList className="bg-muted/60 p-1">
          <TabsTrigger value="operations" className="px-3 py-1.5 text-xs sm:text-sm">问题处置</TabsTrigger>
          <TabsTrigger value="performance" className="px-3 py-1.5 text-xs sm:text-sm">告警分析</TabsTrigger>
        </TabsList>
      </Tabs>

      {view === 'performance' ? (
        <AlarmPerformanceView
          data={performanceQuery.data}
          period={performancePeriod}
          onPeriodChange={(nextPeriod) => onSearchChange({ performancePeriod: nextPeriod })}
          loading={performanceQuery.isPending}
          error={performanceQuery.isError}
        />
      ) : (
        <>
          {queueMode === 'problems' && issueQueueQuery.data ? (
            <FactStrip
              ariaLabel="当前待处理问题"
              items={[
                {
                  key: 'problems',
                  label: '待处理问题',
                  value: issueQueueQuery.data.active,
                  suffix: '项',
                  icon: <Wrench />,
                  tone: issueQueueQuery.data.active > 0 ? 'warning' : 'default',
                },
                {
                  key: 'unassigned',
                  label: '未指派',
                  value: issueQueueQuery.data.unassigned,
                  suffix: '项',
                  icon: <UsersRound />,
                },
                {
                  key: 'high-impact',
                  label: '高影响',
                  value: issueQueueQuery.data.highImpact,
                  suffix: '项',
                  icon: <ShieldAlert />,
                  tone: issueQueueQuery.data.highImpact > 0 ? 'critical' : 'default',
                },
                {
                  key: 'signals',
                  label: '关联告警',
                  value: issueQueueQuery.data.groupedAlarmCount,
                  suffix: '条',
                  icon: <BellRing />,
                },
              ]}
            />
          ) : (
            <LoadContext rows={activeRows} truncated={summaryTruncated} unavailable={activeSummaryUnavailable} />
          )}

          <div className="flex flex-wrap items-center justify-between gap-2">
            <ToggleGroup
              type="single"
              value={queueMode}
              onValueChange={(next) => {
                if (next) onSearchChange({ queueMode: next as 'problems' | 'alarms', selected: undefined, selectedIssue: undefined });
              }}
              variant="outline"
              size="sm"
              spacing={0}
              aria-label="处置列表"
            >
              <ToggleGroupItem value="problems">待处理问题</ToggleGroupItem>
              <ToggleGroupItem value="alarms">原始告警</ToggleGroupItem>
            </ToggleGroup>
            <Select value={view} onValueChange={changeQueueView}>
              <SelectTrigger className="h-8 w-[110px] text-xs" aria-label="记录范围">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectGroup>
                  <SelectItem value="active">当前活动</SelectItem>
                  <SelectItem value="history">全部记录</SelectItem>
                  <SelectItem value="suppressed">已搁置</SelectItem>
                </SelectGroup>
              </SelectContent>
            </Select>
          </div>

          {queueMode === 'problems' ? (
            <ProblemQueueView
              data={issueQueueQuery.data}
              loading={issueQueueQuery.isPending}
              error={issueQueueQuery.isError}
              query={q}
              severity={searchState.severity}
              issueState={issueState}
              diagnosis={diagnosis}
              owner={searchState.owner}
              impact={impact}
              onQueryChange={(value) => onSearchChange({ q: value || undefined, selected: undefined, selectedIssue: undefined })}
              onFilterChange={(patch) => onSearchChange({ ...patch, selected: undefined, selectedIssue: undefined })}
              onOpenProblem={selectProblem}
              timeZone={site.timezone}
            />
          ) : (
        <section className="min-w-0" aria-label="原始告警">
          <DataTableBlock
            className="min-w-0"
            data-testid="alarm-triage-ledger"
          >

            {listQuery.isError && alarms.length === 0 ? (
              <Alert variant="destructive"><AlertDescription>告警暂不可用：{alarmErrorMessage(listQuery.error)}</AlertDescription></Alert>
            ) : listQuery.isPending ? (
              <div className="flex min-h-80 items-center justify-center gap-2 rounded-md border text-sm text-muted-foreground"><LoaderCircle className="size-4 animate-spin" aria-hidden="true" />正在加载告警</div>
            ) : (
              <DataTable
                table={table}
                role="region"
                aria-label="告警，可横向滚动"
                tabIndex={0}
                className="w-full min-w-0 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-inset"
                tableClassName="w-full table-fixed"
                tableAriaLabel="告警"
                empty={view === 'active' ? '当前筛选条件下没有活动告警' : view === 'suppressed' ? '当前没有已搁置告警' : '当前筛选条件下没有告警记录'}
                getHeaderRowProps={() => ({
                  className: 'bg-muted/40',
                })}
                getHeaderCellProps={(header) => ({
                  className: cn(
                    'px-3 py-2.5 font-medium text-muted-foreground',
                    columnWidths[header.id],
                    ['owner', 'duration', 'repeat', 'suppression', 'transition'].includes(header.id) && 'hidden 2xl:table-cell',
                  ),
                })}
                getRowProps={(row) => {
                  const isSelected = selectedAlarmId === row.original.alarm.alarmId;
                  return {
                    tabIndex: 0,
                    'aria-selected': isSelected,
                    'data-state': isSelected ? 'selected' : undefined,
                    className: 'cursor-pointer hover:bg-muted/50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring data-[state=selected]:bg-muted/70',
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
                getCellProps={(cell) => ({
                  className: cn(
                    'px-3 py-2.5',
                    ['owner', 'duration', 'repeat', 'suppression', 'transition'].includes(cell.column.id) && 'hidden 2xl:table-cell',
                  ),
                })}
              >
                <DataTableAdvancedToolbar table={table} className="p-0" data-testid="alarm-triage-toolbar">
                  <InputGroup className="w-full min-w-0 flex-1 sm:w-auto sm:min-w-56 lg:max-w-sm">
                    <InputGroupInput aria-label="搜索告警" placeholder="搜索告警、设备或位置" value={q} onChange={(event) => onSearchChange({ q: event.currentTarget.value || undefined, selected: undefined })} />
                    <InputGroupAddon align="inline-start"><Search className="size-3.5" aria-hidden="true" /></InputGroupAddon>
                    <InputGroupAddon align="inline-end"><InputGroupText>{filteredRows.length} 条</InputGroupText></InputGroupAddon>
                  </InputGroup>
                  <AlarmFilterPopover
                    severity={searchState.severity}
                    acknowledgement={searchState.ack}
                    owner={searchState.owner}
                    sourceType={searchState.sourceType}
                    onChange={(patch) => onSearchChange({ ...patch, selected: undefined })}
                  />
                  <Button variant="outline" size="sm" className="h-8 gap-1.5" disabled={filteredRows.length === 0} onClick={() => exportCsv(filteredRows, site.timezone)}>
                    <Download className="size-3.5" aria-hidden="true" />
                    导出
                  </Button>
                </DataTableAdvancedToolbar>
              </DataTable>
            )}
            {listQuery.hasNextPage ? <div className="flex justify-center"><Button variant="outline" size="sm" disabled={listQuery.isFetchingNextPage} onClick={() => void listQuery.fetchNextPage()}>{listQuery.isFetchingNextPage ? <LoaderCircle className="animate-spin" aria-hidden="true" /> : null}加载更多</Button></div> : null}
          </DataTableBlock>

        </section>
          )}
        </>
      )}

      <OperationalDetailSheet
        open={Boolean(selectedAlarmId)}
        onClose={closeDetailSheet}
        title={selectedProblem?.title ?? detailRow?.alarm.title ?? '问题详情'}
        subtitle={selectedProblem
          ? `${selectedProblem.object.label}${selectedProblem.object.locationLabel ? ` · ${selectedProblem.object.locationLabel}` : ''}`
          : detailRow
            ? `${detailRow.deviceLabel} · ${detailRow.locationLabel}`
            : '正在读取问题详情'}
        status={detailRow ? <SeverityBadge severity={detailRow.alarm.currentSeverity} /> : undefined}
        size={640}
        footer={detailSheetFooter}
        onCloseAutoFocus={(event) => {
          event.preventDefault();
          returnFocusRowRef.current?.focus({ preventScroll: true });
        }}
      >
        {detailSheetBody}
      </OperationalDetailSheet>

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
