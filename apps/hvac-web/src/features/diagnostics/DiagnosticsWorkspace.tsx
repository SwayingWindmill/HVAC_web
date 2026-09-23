import { useCallback, useEffect, useMemo } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import type { ColumnDef } from '@tanstack/react-table';
import {
  AlertTriangle,
  ArrowRight,
  Bot,
  CheckCircle2,
  ClipboardList,
  Link2,
  LoaderCircle,
  Search,
  Sparkles,
  Wrench,
} from 'lucide-react';
import { DataTableBlock } from '@/blocks/data-table';
import { listSiteFDDFindings, type FDDFinding } from '@/api/intelligence';
import {
  createPlatformGatewayClient,
  type CurrentPrincipalResponse,
  type Site,
} from '@/api/generated/platformGateway.gen';
import type { HvacRouterContext } from '@/app/router-context';
import { siteDeviceRoute, siteRoute } from '@/app/router-paths';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card';
import {
  InputGroup,
  InputGroupAddon,
  InputGroupInput,
  InputGroupText,
} from '@/components/ui/input-group';
import { Skeleton } from '@/components/ui/skeleton';
import {
  SelectGroup,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import {
  DataTable,
  DataTablePagination,
  type DataTableFeatures,
} from '@/components/data-table';
import { StatusBadge } from '@/components/status-badge';
import { useDataTable } from '@/hooks/use-data-table';
import { cn } from '@/lib/utils';

export interface DiagnosticsSearchState {
  readonly diagnosis?: string;
  readonly q?: string;
  readonly source?: string;
  readonly alarm?: string;
}

interface DiagnosticsWorkspaceProps {
  readonly site: Readonly<Site>;
  readonly principal: CurrentPrincipalResponse;
  readonly runtime: HvacRouterContext['runtime'];
  readonly searchState: DiagnosticsSearchState;
  readonly onSearchChange: (patch: Partial<DiagnosticsSearchState>) => void;
}

interface DiagnosisRow {
  readonly finding: FDDFinding;
  readonly assetLabel: string;
  readonly locationLabel: string;
  readonly deviceLabels: readonly string[];
  readonly deviceIds: readonly string[];
}

function formatInstant(value: string, timeZone: string): string {
  return new Intl.DateTimeFormat('zh-CN', {
    timeZone,
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  }).format(new Date(value));
}

function confidence(value: number): string {
  return `${Math.round(value * 100)}%`;
}

function resultSource(finding: FDDFinding): '规则结果' | '模型结果' | '已发布结果' {
  if (finding.ruleRevisionId) return '规则结果';
  if (finding.modelDeploymentRevisionId) return '模型结果';
  return '已发布结果';
}

function sourceExplanation(finding: FDDFinding): string {
  if (finding.ruleRevisionId) {
    return '规则命中说明检测条件成立，不自动等于根因已经确认；建议结合现场工况与测点历史复核。';
  }
  if (finding.modelDeploymentRevisionId) {
    return '模型置信度表示本次诊断结果的模型评分，不等于根因概率；建议结合现场工况复核。';
  }
  return '诊断结果已生成，建议核对设备实时运行参数与关联工况。';
}

function DiagnosticsLoading() {
  return (
    <div className="mx-auto flex w-full max-w-[1800px] flex-col gap-5">
      <Skeleton className="h-16 w-80" />
      <div className="grid gap-4 xl:grid-cols-[minmax(420px,0.9fr)_minmax(0,1.5fr)]">
        <Skeleton className="h-[680px] w-full" />
        <Skeleton className="h-[680px] w-full" />
      </div>
    </div>
  );
}

export function DiagnosticsWorkspace({
  site,
  principal,
  runtime,
  searchState,
  onSearchChange,
}: DiagnosticsWorkspaceProps) {
  const queryClient = useQueryClient();
  const tenantId = principal.context.tenantId;
  const queryPrefix = useMemo(
    () => ['diagnostics', principal.session.id, principal.authorization.policyRevision, tenantId, site.id] as const,
    [principal.authorization.policyRevision, principal.session.id, site.id, tenantId],
  );
  const canReadRegistry = principal.authorization.capabilities.includes('asset.list')
    && principal.authorization.capabilities.includes('device.list');

  const purge = useCallback(async () => {
    await queryClient.cancelQueries({ queryKey: queryPrefix });
    queryClient.removeQueries({ queryKey: queryPrefix });
  }, [queryClient, queryPrefix]);

  useEffect(() => runtime.registerProtectedResource({
    id: `diagnostics-cache:${tenantId}:${site.id}`,
    kind: 'query-cache',
    purge,
  }), [purge, runtime, site.id, tenantId]);


  const findingsQuery = useQuery({
    queryKey: [...queryPrefix, 'findings'],
    queryFn: ({ signal }) => listSiteFDDFindings(site.id, signal),
    staleTime: 30_000,
  });

  const registryClient = useMemo(() => createPlatformGatewayClient(), []);
  const registryQuery = useQuery({
    queryKey: [...queryPrefix, 'registry'],
    queryFn: async ({ signal }) => (await registryClient.getSiteAssetModel(site.id, { signal })).data,
    enabled: canReadRegistry,
    staleTime: 60_000,
  });

  const rows = useMemo<DiagnosisRow[]>(() => {
    const model = registryQuery.data;
    const assetLabels = new Map((model?.assets ?? []).map((asset) => [asset.id, asset.displayName]));
    const deviceById = new Map((model?.devices ?? []).map((device) => [device.id, device]));
    const spaceById = new Map((model?.spaces ?? []).map((space) => [space.id, space.displayName]));
    const devicesByAsset = new Map<string, string[]>();
    const spaceByAsset = new Map<string, string>();

    for (const relationship of model?.relationships ?? []) {
      if (relationship.fromType === 'DEVICE' && relationship.toType === 'ASSET') {
        devicesByAsset.set(relationship.toId, [...(devicesByAsset.get(relationship.toId) ?? []), relationship.fromId]);
      }
      if (relationship.fromType === 'ASSET' && relationship.toType === 'SPACE') {
        spaceByAsset.set(relationship.fromId, relationship.toId);
      }
    }

    return [...(findingsQuery.data ?? [])]
      .sort((left, right) => Date.parse(right.createdAt) - Date.parse(left.createdAt))
      .map((finding) => {
        const deviceIds = [...new Set(devicesByAsset.get(finding.assetId) ?? [])];
        const deviceLabels = deviceIds
          .map((deviceId) => deviceById.get(deviceId)?.displayName)
          .filter((value): value is string => Boolean(value));
        const spaceId = spaceByAsset.get(finding.assetId);
        return {
          finding,
          assetLabel: assetLabels.get(finding.assetId) ?? '关联资产名称不可用',
          locationLabel: (spaceId ? spaceById.get(spaceId) : undefined) ?? site.displayName,
          deviceLabels,
          deviceIds,
        };
      });
  }, [findingsQuery.data, registryQuery.data, site.displayName]);

  const sourceFilter = searchState.source && ['规则结果', '模型结果', '已发布结果'].includes(searchState.source) ? searchState.source : 'ALL';

  const normalizedSearch = (searchState.q ?? '').trim().toLocaleLowerCase('zh-CN');
  const filteredRows = useMemo(() => rows.filter((row) => {
    if (sourceFilter !== 'ALL' && resultSource(row.finding) !== sourceFilter) return false;
    if (!normalizedSearch) return true;
    return [
      row.finding.findingType,
      row.assetLabel,
      row.locationLabel,
      ...row.deviceLabels,
    ].some((value) => value.toLocaleLowerCase('zh-CN').includes(normalizedSearch));
  }), [normalizedSearch, rows, sourceFilter]);

  const selected = useMemo(() => {
    const exact = searchState.diagnosis
      ? rows.find((row) => row.finding.id === searchState.diagnosis)
      : undefined;
    if (exact) return exact;
    const fromAlarm = searchState.alarm
      ? rows.find((row) => row.finding.alarmId === searchState.alarm)
      : undefined;
    return fromAlarm ?? filteredRows[0] ?? rows[0] ?? null;
  }, [filteredRows, rows, searchState.alarm, searchState.diagnosis]);

  useEffect(() => {
    if (!selected || searchState.diagnosis === selected.finding.id) return;
    onSearchChange({ diagnosis: selected.finding.id });
  }, [onSearchChange, searchState.diagnosis, selected]);

  const columns = useMemo<Array<ColumnDef<DataTableFeatures, DiagnosisRow>>>(() => [
    {
      id: 'finding',
      header: '诊断',
      cell: ({ row }) => (
        <div className="max-w-[260px]">
          <strong className="block truncate text-xs font-medium">{row.original.finding.findingType}</strong>
          <span className="mt-0.5 block truncate text-[11px] text-muted-foreground">{row.original.assetLabel}</span>
        </div>
      ),
    },
    {
      id: 'source',
      header: '来源',
      cell: ({ row }) => {
        const src = resultSource(row.original.finding);
        const tone = src === '已发布结果' ? 'success' : 'info';
        return <StatusBadge label={src} tone={tone} />;
      },
    },
    {
      id: 'confidence',
      header: '置信度',
      cell: ({ row }) => <span className="text-xs font-medium tabular-nums">{confidence(row.original.finding.confidence)}</span>,
    },
    {
      id: 'time',
      header: '评估结束',
      cell: ({ row }) => <span className="whitespace-nowrap text-[11px] text-muted-foreground tabular-nums">{formatInstant(row.original.finding.evaluationTo, site.timezone)}</span>,
    },
  ], [site.timezone]);

  const table = useDataTable({
    key: `diagnostics-${selected?.finding.id ?? 'none'}`,
    data: filteredRows,
    columns,
    pageSize: 10,
    getRowId: (row) => row.finding.id,
  });

  if (findingsQuery.isPending) return <DiagnosticsLoading />;

  const sourceContext = searchState.source === 'alarm' && searchState.alarm;
  const selectedFinding = selected?.finding;
  const singleDeviceId = selected?.deviceIds.length === 1 ? selected.deviceIds[0] : null;

  return (
    <main
      className="mx-auto flex w-full max-w-[1800px] flex-col gap-5"
      data-testid="diagnostics-workspace"
      data-business-state={findingsQuery.isError ? 'UNAVAILABLE' : rows.length === 0 ? 'EMPTY' : 'READY'}
      data-site-id={site.id}
    >
      <header className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <p className="text-sm text-muted-foreground">{site.displayName}</p>
        <Button variant="outline" size="sm" disabled={findingsQuery.isFetching} onClick={() => void findingsQuery.refetch()}>
          {findingsQuery.isFetching ? <LoaderCircle className="animate-spin" /> : <Sparkles />}
          刷新诊断
        </Button>
      </header>

      <div className="rounded-md border bg-muted/20 p-3 text-xs leading-5 text-muted-foreground">
        诊断结果不等于已确认根因；关键证据不足时需现场复核。
      </div>

      {sourceContext ? (
        <div className="flex flex-col gap-2 rounded-md border bg-card p-3 text-xs sm:flex-row sm:items-center sm:justify-between" data-testid="diagnostics-source-context">
          <div className="flex min-w-0 items-center gap-2"><Link2 className="size-3.5 text-muted-foreground" /><span>来自告警 · {rows.some((row) => row.finding.alarmId === searchState.alarm) ? '已找到显式关联的诊断结果' : '当前没有显式关联的已发布诊断结果'}</span></div>
          <Button variant="outline" size="sm" asChild><a href={`${siteRoute(site, 'alarms')}?alarm=${encodeURIComponent(searchState.alarm!)}&source=diagnostics`}>返回关联告警</a></Button>
        </div>
      ) : null}

      {findingsQuery.isError ? (
        <div className="rounded-md border border-destructive/30 bg-destructive/5 p-4 text-sm text-destructive">诊断结果暂不可用，请稍后重试。</div>
      ) : rows.length === 0 ? (
        <div className="grid min-h-[420px] place-items-center rounded-lg border border-dashed text-center">
          <div className="max-w-md px-6">
            <CheckCircle2 className="mx-auto size-8 text-muted-foreground" />
            <h2 className="mt-3 text-sm font-medium">当前没有故障诊断发现</h2>
            <p className="mt-1 text-xs leading-5 text-muted-foreground">当前没有已发布的故障诊断发现；这不代表系统不存在异常。</p>
          </div>
        </div>
      ) : (
        <div className="grid min-w-0 gap-4 xl:grid-cols-[minmax(420px,0.9fr)_minmax(0,1.5fr)]">
          <DataTableBlock
            className="min-w-0"
            data-testid="diagnostics-queue"
            title="诊断结果"
          >
            <DataTable
              table={table}
              tableAriaLabel="诊断结果队列"
              empty="当前搜索条件下没有诊断结果"
              getRowProps={(row) => ({
                className: cn('cursor-pointer', selected?.finding.id === row.original.finding.id && 'bg-muted/60'),
                'data-state': selected?.finding.id === row.original.finding.id ? 'selected' : undefined,
                onClick: () => onSearchChange({ diagnosis: row.original.finding.id }),
              })}
              getCellProps={() => ({ className: 'py-3' })}
              footer={<DataTablePagination table={table} totalRows={filteredRows.length} />}
            >
              <div className="flex flex-col gap-2 sm:flex-row">
                <InputGroup className="min-w-0 flex-1">
                  <InputGroupInput
                    aria-label="搜索诊断"
                    placeholder="搜索诊断 / 资产 / 设备"
                    value={searchState.q ?? ''}
                    onChange={(event) => onSearchChange({ q: event.currentTarget.value || undefined })}
                  />
                  <InputGroupAddon align="inline-start"><Search /></InputGroupAddon>
                  <InputGroupAddon align="inline-end"><InputGroupText>{filteredRows.length} 条</InputGroupText></InputGroupAddon>
                </InputGroup>
                <Select value={sourceFilter} onValueChange={(val) => onSearchChange({ source: val === 'ALL' ? undefined : val })}>
                  <SelectTrigger className="w-full sm:w-[160px]" aria-label="筛选诊断来源">
                    <SelectValue placeholder="全部来源" />
                  </SelectTrigger>
                  <SelectContent><SelectGroup>
                    <SelectItem value="ALL">全部来源 ({rows.length})</SelectItem>
                    <SelectItem value="规则结果">规则结果 ({rows.filter((row) => resultSource(row.finding) === '规则结果').length})</SelectItem>
                    <SelectItem value="模型结果">模型结果 ({rows.filter((row) => resultSource(row.finding) === '模型结果').length})</SelectItem>
                    <SelectItem value="已发布结果">已发布结果 ({rows.filter((row) => resultSource(row.finding) === '已发布结果').length})</SelectItem>
                  </SelectGroup></SelectContent>
                </Select>
              </div>
            </DataTable>
          </DataTableBlock>

          {selected && selectedFinding ? (
            <div className="min-w-0 space-y-4" data-testid="diagnostics-detail">
              <Card className="shadow-none">
                <CardHeader>
                  <div className="flex flex-wrap items-center gap-2">
                    <Badge variant="outline">{resultSource(selectedFinding)}</Badge>
                    {selectedFinding.qualityBlocker ? <Badge variant="destructive">证据受限</Badge> : <Badge variant="secondary">已发布</Badge>}
                  </div>
                  <CardTitle>{selectedFinding.findingType}</CardTitle>
                  <CardDescription>{selected.assetLabel} · {selected.locationLabel}</CardDescription>
                  <CardAction><Badge variant="outline">置信度 {confidence(selectedFinding.confidence)}</Badge></CardAction>
                </CardHeader>
                <CardContent>
                  <div className="grid overflow-hidden rounded-md border text-xs sm:grid-cols-3">
                    <div className="p-3"><span className="text-[11px] text-muted-foreground">评估窗口</span><strong className="mt-1 block font-medium">{formatInstant(selectedFinding.evaluationFrom, site.timezone)} → {formatInstant(selectedFinding.evaluationTo, site.timezone)}</strong></div>
                    <div className="border-t p-3 sm:border-l sm:border-t-0"><span className="text-[11px] text-muted-foreground">证据引用</span><strong className="mt-1 block font-medium tabular-nums">{selectedFinding.evidenceIds.length} 项</strong></div>
                    <div className="border-t p-3 sm:border-l sm:border-t-0"><span className="text-[11px] text-muted-foreground">关联设备</span><strong className="mt-1 block font-medium tabular-nums">{selected.deviceIds.length || '—'}</strong></div>
                  </div>
                </CardContent>
              </Card>

              <section className="rounded-lg border bg-card p-4" aria-labelledby="diagnostics-evidence-title">
                <div className="flex items-center gap-2"><CheckCircle2 className="size-4 text-success" /><h2 id="diagnostics-evidence-title" className="text-sm font-semibold">已验证事实</h2></div>
                <div className="mt-3 grid gap-3 sm:grid-cols-2">
                  <div className="rounded-md border p-3"><span className="text-[11px] text-muted-foreground">调查对象</span><strong className="mt-1 block text-xs font-medium">{selected.assetLabel}</strong><small className="mt-1 block text-[10px] text-muted-foreground">{selected.locationLabel}</small></div>
                  <div className="rounded-md border p-3"><span className="text-[11px] text-muted-foreground">证据范围</span><strong className="mt-1 block text-xs font-medium">{selectedFinding.evidenceIds.length} 项已绑定证据引用</strong><small className="mt-1 block text-[10px] text-muted-foreground">关联测点遥测与运行记录</small></div>
                </div>
                {selectedFinding.qualityBlocker ? <div className="mt-3 flex items-start gap-2 rounded-md border border-warning/30 bg-warning/5 p-3 text-xs text-warning"><AlertTriangle className="mt-0.5 size-4 shrink-0" />当前诊断结果存在测点数据质量异常，请先核实相关传感器通信与测点准确性。</div> : null}
              </section>

              <section className="rounded-lg border bg-card p-4" aria-labelledby="diagnostics-result-title">
                <div className="flex items-center gap-2"><Wrench className="size-4 text-information" /><h2 id="diagnostics-result-title" className="text-sm font-semibold">已发布诊断结果</h2></div>
                <div className="mt-3 rounded-md border p-4">
                  <div className="flex flex-wrap items-center gap-2"><Badge variant="outline">{resultSource(selectedFinding)}</Badge><Badge variant="outline">置信度 {confidence(selectedFinding.confidence)}</Badge></div>
                  <strong className="mt-3 block text-sm font-medium">{selectedFinding.findingType}</strong>
                  <p className="mt-1 text-xs leading-5 text-muted-foreground">{sourceExplanation(selectedFinding)}</p>
                </div>
              </section>

              <section className="rounded-lg border bg-card p-4" aria-labelledby="diagnostics-hypothesis-title">
                <div className="flex items-center gap-2"><Bot className="size-4 text-muted-foreground" /><h2 id="diagnostics-hypothesis-title" className="text-sm font-semibold">根因假设</h2></div>
                <div className="mt-3 rounded-md border border-dashed p-4">
                  <Badge variant="secondary">尚无根因结论</Badge>
                  <p className="mt-2 text-xs leading-5 text-muted-foreground">当前已确认异常现象，建议安排现场工程师排查机械及电气根因。</p>
                </div>
              </section>

              <section className="rounded-lg border bg-card p-4" aria-labelledby="diagnostics-impact-title">
                <div className="flex items-center gap-2"><Link2 className="size-4 text-muted-foreground" /><h2 id="diagnostics-impact-title" className="text-sm font-semibold">影响范围与关联对象</h2></div>
                <div className="mt-3 grid gap-3 sm:grid-cols-2">
                  <div className="rounded-md border p-3"><span className="text-[11px] text-muted-foreground">关联资产</span><strong className="mt-1 block text-xs font-medium">{selected.assetLabel}</strong><Button className="mt-2 px-0" variant="link" size="sm" asChild><a href={`${siteRoute(site, 'devices')}?q=${encodeURIComponent(selected.assetLabel)}`}>在设备中查看 <ArrowRight /></a></Button></div>
                  <div className="rounded-md border p-3"><span className="text-[11px] text-muted-foreground">关联设备</span><strong className="mt-1 block text-xs font-medium">{selected.deviceLabels.length > 0 ? selected.deviceLabels.slice(0, 3).join('、') : '暂无可展示关联设备'}</strong>{selected.deviceLabels.length > 3 ? <small className="mt-1 block text-[10px] text-muted-foreground">另有 {selected.deviceLabels.length - 3} 台</small> : null}{singleDeviceId ? <Button className="mt-2 px-0" variant="link" size="sm" asChild><a href={siteDeviceRoute(site, singleDeviceId)}>打开设备详情 <ArrowRight /></a></Button> : null}</div>
                </div>
                <div className="mt-3 flex flex-wrap gap-2">
                  {selectedFinding.alarmId ? <Button variant="outline" size="sm" asChild><a href={`${siteRoute(site, 'alarms')}?source=diagnostics&alarm=${encodeURIComponent(selectedFinding.alarmId)}`}>查看关联告警</a></Button> : <Badge variant="outline">未关联告警</Badge>}
                  {selectedFinding.workOrderId ? <Button variant="outline" size="sm" asChild><a href={`${siteRoute(site, 'work-orders')}?source=diagnostics&workOrder=${encodeURIComponent(selectedFinding.workOrderId)}`}><ClipboardList />查看关联工单</a></Button> : <Badge variant="outline">未关联工单</Badge>}
                </div>
              </section>

              <section className="rounded-lg border bg-card p-4" aria-labelledby="diagnostics-next-title">
                <div className="flex items-center gap-2"><ArrowRight className="size-4 text-information" /><h2 id="diagnostics-next-title" className="text-sm font-semibold">下一验证 / 动作</h2></div>
                <ol className="mt-3 list-decimal space-y-2 pl-5 text-xs leading-5 text-muted-foreground">
                  {selectedFinding.qualityBlocker ? <li>先核查相关测点通信与数据有效性，消除异常数据干扰。</li> : null}
                  {selectedFinding.alarmId ? <li>复核关联告警的当前实时状态，确认设备异常现象是否仍存在。</li> : <li>核对受影响设备的当前运行工况与测点读数。</li>}
                  {selected.deviceIds.length > 0 ? <li>检查关联设备实时数据与运行趋势，确认故障是否依然存在。</li> : <li>当前诊断未直接关联单台设备，可在设备中查看系统概况。</li>}
                  {selectedFinding.workOrderId ? <li>核对关联工单处理进展与责任人。</li> : <li>若需现场检修核实，可转派工单跟进。</li>}
                </ol>
                {!selectedFinding.workOrderId ? <Button className="mt-3" variant="outline" size="sm" asChild><a href={siteRoute(site, 'work-orders')}><ClipboardList />进入工单</a></Button> : null}
              </section>

              <section className="rounded-lg border bg-card p-4" aria-labelledby="diagnostics-history-title">
                <h2 id="diagnostics-history-title" className="text-sm font-semibold">调查更新</h2>
                <ol className="mt-3 space-y-3 text-xs">
                  <li className="flex gap-3"><span className="mt-1.5 size-2 shrink-0 rounded-full bg-muted-foreground" /><div><strong className="font-medium">评估窗口结束</strong><p className="mt-0.5 text-[11px] text-muted-foreground">{formatInstant(selectedFinding.evaluationTo, site.timezone)} · 系统完成本次诊断评估窗口</p></div></li>
                  <li className="flex gap-3"><span className="mt-1.5 size-2 shrink-0 rounded-full bg-primary" /><div><strong className="font-medium">诊断发布</strong><p className="mt-0.5 text-[11px] text-muted-foreground">{formatInstant(selectedFinding.createdAt, site.timezone)} · 完成诊断分析并发布结论</p></div></li>
                </ol>
              </section>
            </div>
          ) : null}
        </div>
      )}
    </main>
  );
}
