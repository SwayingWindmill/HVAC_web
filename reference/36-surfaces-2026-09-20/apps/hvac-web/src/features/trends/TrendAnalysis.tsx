import { useEffect, useMemo, useState } from 'react';
import { useQueries, useQuery, useQueryClient } from '@tanstack/react-query';
import type { ColumnDef } from '@tanstack/react-table';
import { Link } from '@tanstack/react-router';
import {
  Activity,
  AlarmClock,
  ChevronLeft,
  ChevronRight,
  CircleAlert,
  ListFilter,
  RefreshCw,
} from 'lucide-react';
import {
  createPlatformGatewayClient,
  type CurrentPrincipalResponse,
  type Device,
  type Site,
  type TelemetryPoint,
} from '@/api/generated/platformGateway.gen';
import {
  createS2TelemetryClient,
  type DeviceHistoryObservation,
  type DeviceHistoryResponse,
} from '@/api/generated/s2Telemetry.gen';
import { listScopedAlarms, type Alarm, type AlarmOperation } from '@/api/alarms';
import { Main } from '@/components/layout/Main';
import { PageIntro } from '@/components/layout/PageIntro';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Input } from '@/components/ui/input';
import {
  Sheet,
  SheetBody,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
  SheetTrigger,
} from '@/components/ui/sheet';
import { Skeleton } from '@/components/ui/skeleton';
import { DataTable, StatusPillBadge, type DataTableFeatures } from '@/components/data-table';
import { useDataTable } from '@/hooks/use-data-table';
import {
  TimeSeriesChart,
  type OperationalChartSeries,
  type OperationalChartTone,
} from '@/shared/charts/TimeSeriesChart';
import {
  buildTrendData,
  buildTrendHistoryRequests,
  createTrendWindow,
  formatTrendInstant,
  formatTrendValue,
  loadTrendHistory,
  observationsForPoint,
  parseTrendSeries,
  pointDisplayUnit,
  qualityLabel,
  resolveTrendWindow,
  serializeTrendSeries,
  trendInterpolation,
  unitGroupKey,
  type TrendRangePreset,
} from './model';

export interface TrendAnalysisSearchState {
  readonly timeStart?: string;
  readonly timeEnd?: string;
  readonly series?: string;
  readonly eventTypes?: 'alarm';
}

interface TrendAnalysisProps {
  readonly site: Readonly<Site>;
  readonly principal: CurrentPrincipalResponse;
  readonly searchState: TrendAnalysisSearchState;
  readonly onSearchChange: (patch: Partial<TrendAnalysisSearchState>) => void;
}

interface PointEvidence {
  readonly point: TelemetryPoint;
  readonly device: Device | undefined;
  readonly observations: readonly DeviceHistoryObservation[];
  readonly latest: DeviceHistoryObservation | undefined;
}

interface TrendEvent {
  readonly id: string;
  readonly at: string;
  readonly label: string;
  readonly title: string;
  readonly alarmId: string;
  readonly severity: Alarm['currentSeverity'];
}

interface TrendEvidenceRow {
  readonly item: PointEvidence;
  readonly observation: DeviceHistoryObservation;
}

const SERIES_TONES: readonly OperationalChartTone[] = ['primary', 'info', 'secondary', 'warning'];

function operationLabel(operation: AlarmOperation): string | null {
  switch (operation) {
    case 'PUBLISH': return '告警发生';
    case 'ACKNOWLEDGE': return '告警确认（非恢复）';
    case 'CLEAR': return '告警恢复';
    case 'SUPPRESS': return '告警抑制';
    case 'UNSUPPRESS': return '解除抑制';
    case 'ASSIGN': return '告警指派';
    case 'UNASSIGN': return '取消指派';
  }
}

function alarmEvents(alarms: readonly Alarm[], from: string, to: string): readonly TrendEvent[] {
  const start = Date.parse(from);
  const end = Date.parse(to);
  return alarms.flatMap((alarm) => alarm.timeline.flatMap((entry) => {
    const timestamp = Date.parse(entry.occurredAt);
    const label = operationLabel(entry.operation);
    if (!label || timestamp < start || timestamp >= end) return [];
    return [{
      id: `${alarm.alarmId}:${entry.version}`,
      at: entry.occurredAt,
      label,
      title: alarm.title,
      alarmId: alarm.alarmId,
      severity: entry.currentSeverity,
    } satisfies TrendEvent];
  })).sort((left, right) => Date.parse(left.at) - Date.parse(right.at));
}

function pointTypeLabel(point: TelemetryPoint): string {
  switch (point.pointType) {
    case 'TELEMETRY': return '测量';
    case 'COUNTER': return '累计';
    case 'STATE': return '状态';
    case 'SETTING': return '设定';
    case 'COMMAND': return '命令';
  }
}

function formatAxisTime(value: string | number, timezone: string): string {
  return new Intl.DateTimeFormat('zh-CN', {
    timeZone: timezone,
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  }).format(new Date(Number(value)));
}

function nearestObservation(observations: readonly DeviceHistoryObservation[], timestamp: number): DeviceHistoryObservation | undefined {
  let best: DeviceHistoryObservation | undefined;
  let distance = Number.POSITIVE_INFINITY;
  for (const observation of observations) {
    const nextDistance = Math.abs(Date.parse(observation.sampledAt) - timestamp);
    if (nextDistance < distance) {
      distance = nextDistance;
      best = observation;
    }
  }
  return best;
}

function SeriesPicker({
  points,
  devices,
  selectedIds,
  onChange,
}: {
  readonly points: readonly TelemetryPoint[];
  readonly devices: readonly Device[];
  readonly selectedIds: readonly string[];
  readonly onChange: (ids: readonly string[]) => void;
}) {
  const [query, setQuery] = useState('');
  const normalized = query.trim().toLocaleLowerCase('zh-CN');
  const deviceById = useMemo(() => new Map(devices.map((device) => [device.id, device])), [devices]);
  const filtered = points.filter((point) => {
    if (point.status !== 'ACTIVE') return false;
    if (!normalized) return true;
    const device = deviceById.get(point.reportingDeviceId);
    return `${point.displayName} ${device?.displayName ?? ''} ${point.unit ?? ''}`.toLocaleLowerCase('zh-CN').includes(normalized);
  });
  const groups = [...new Set(filtered.map((point) => point.reportingDeviceId))];

  return (
    <Sheet>
      <SheetTrigger asChild>
        <Button variant="outline" size="sm" data-testid="trend-series-trigger">
          <ListFilter />变量 {selectedIds.length}/4
        </Button>
      </SheetTrigger>
      <SheetContent side="right">
        <SheetHeader>
          <SheetTitle>选择趋势变量</SheetTitle>
          <SheetDescription>最多选择 4 个已登记变量。不同单位会自动分面，不使用双 Y 轴。</SheetDescription>
        </SheetHeader>
        <SheetBody>
          <Input value={query} onChange={(event) => setQuery(event.currentTarget.value)} placeholder="搜索变量或设备" aria-label="搜索趋势变量" />
          <div className="mt-5 space-y-5">
            {groups.map((deviceId) => {
              const device = deviceById.get(deviceId);
              const devicePoints = filtered.filter((point) => point.reportingDeviceId === deviceId);
              return (
                <section key={deviceId} aria-label={device?.displayName ?? '设备变量'}>
                  <div className="mb-2 text-xs font-medium text-muted-foreground">{device?.displayName ?? '未命名设备'}</div>
                  <div className="divide-y rounded-md border">
                    {devicePoints.map((point) => {
                      const checked = selectedIds.includes(point.id);
                      const disabled = !checked && selectedIds.length >= 4;
                      return (
                        <label key={point.id} className="flex cursor-pointer items-start gap-3 px-3 py-3 has-[:disabled]:cursor-not-allowed has-[:disabled]:opacity-50">
                          <Checkbox
                            checked={checked}
                            disabled={disabled}
                            onCheckedChange={(next) => {
                              if (next === true) onChange([...selectedIds, point.id].slice(0, 4));
                              else onChange(selectedIds.filter((id) => id !== point.id));
                            }}
                            aria-label={`选择 ${point.displayName}`}
                          />
                          <span className="min-w-0 flex-1">
                            <strong className="block text-sm font-medium">{point.displayName}</strong>
                            <span className="mt-1 block text-xs text-muted-foreground">{pointTypeLabel(point)} · {pointDisplayUnit(point)} · 采样 {Math.max(1, Math.round(point.sampleIntervalMs / 1000))} 秒</span>
                          </span>
                        </label>
                      );
                    })}
                  </div>
                </section>
              );
            })}
            {filtered.length === 0 ? <p className="text-sm text-muted-foreground">没有匹配的可见变量。</p> : null}
          </div>
        </SheetBody>
      </SheetContent>
    </Sheet>
  );
}

function LoadingTrend() {
  return (
    <Main className="space-y-4">
      <Skeleton className="h-20 w-full max-w-xl" />
      <Skeleton className="h-14 w-full" />
      <Skeleton className="h-[480px] w-full" />
    </Main>
  );
}

export function TrendAnalysis({ site, principal, searchState, onSearchChange }: TrendAnalysisProps) {
  const queryClient = useQueryClient();
  const platformClient = useMemo(() => createPlatformGatewayClient(), []);
  const telemetryClient = useMemo(() => createS2TelemetryClient(), []);
  const selectedIds = useMemo(() => parseTrendSeries(searchState.series), [searchState.series]);
  const window = useMemo(() => resolveTrendWindow(searchState.timeStart, searchState.timeEnd), [searchState.timeEnd, searchState.timeStart]);
  const capabilities = useMemo(() => new Set(principal.authorization.capabilities), [principal.authorization.capabilities]);
  const canReadAlarms = capabilities.has('alarm.list');
  const eventsEnabled = canReadAlarms && searchState.eventTypes === 'alarm';
  const protectedPrefix = useMemo(
    () => ['trend-analysis', principal.session.id, principal.context.tenantId, site.id] as const,
    [principal.context.tenantId, principal.session.id, site.id],
  );

  useEffect(() => () => {
    void queryClient.cancelQueries({ queryKey: protectedPrefix });
    queryClient.removeQueries({ queryKey: protectedPrefix });
  }, [protectedPrefix, queryClient]);

  const registryQuery = useQuery({
    queryKey: [...protectedPrefix, 'asset-model'],
    queryFn: ({ signal }) => platformClient.getSiteAssetModel(site.id, { signal }).then((result) => result.data),
    staleTime: 60_000,
    refetchOnWindowFocus: false,
  });

  const visiblePoints = useMemo(
    () => (registryQuery.data?.telemetryPoints ?? []).filter((point) => point.status === 'ACTIVE'),
    [registryQuery.data?.telemetryPoints],
  );
  const pointById = useMemo(() => new Map(visiblePoints.map((point) => [point.id, point])), [visiblePoints]);
  const selectedPoints = useMemo(
    () => selectedIds.map((id) => pointById.get(id)).filter((point): point is TelemetryPoint => Boolean(point)),
    [pointById, selectedIds],
  );
  const historyRequests = useMemo(() => buildTrendHistoryRequests(selectedPoints, window), [selectedPoints, window]);
  const historyQueries = useQueries({
    queries: historyRequests.map((request) => ({
      queryKey: [...protectedPrefix, 'history', request.deviceId, [...request.keys], request.from, request.to],
      queryFn: ({ signal }: { signal: AbortSignal }) => loadTrendHistory(
        telemetryClient,
        request,
        principal.session.csrfToken,
        { tenantId: principal.context.tenantId, siteId: site.id },
        signal,
      ),
      staleTime: 30_000,
      retry: false,
      refetchOnWindowFocus: false,
    })),
  });
  const historyResponses = historyQueries.map((query) => query.data).filter((response): response is DeviceHistoryResponse => Boolean(response));
  const historyPending = historyQueries.some((query) => query.isPending);
  const historyError = historyQueries.some((query) => query.isError);

  const alarmQuery = useQuery({
    queryKey: [...protectedPrefix, 'alarm-events', window.from, window.to],
    queryFn: ({ signal }) => listScopedAlarms({ limit: 100 }, {
      trustedTenantId: principal.context.tenantId,
      trustedSiteId: site.id,
      signal,
    }),
    enabled: eventsEnabled,
    staleTime: 30_000,
    retry: false,
    refetchOnWindowFocus: false,
  });
  const events = useMemo(() => alarmEvents(alarmQuery.data?.items ?? [], window.from, window.to), [alarmQuery.data?.items, window.from, window.to]);

  const devices = registryQuery.data?.devices ?? [];
  const deviceById = useMemo(() => new Map(devices.map((device) => [device.id, device])), [devices]);
  const evidence = useMemo<readonly PointEvidence[]>(() => selectedPoints.map((point) => {
    const observations = observationsForPoint(historyResponses, point);
    return {
      point,
      device: deviceById.get(point.reportingDeviceId),
      observations,
      latest: observations.at(-1),
    };
  }), [deviceById, historyResponses, selectedPoints]);

  const plottable = evidence.filter((item) => item.point.valueType === 'NUMBER' || item.point.valueType === 'BOOLEAN');
  const nonNumeric = evidence.filter((item) => item.point.valueType !== 'NUMBER' && item.point.valueType !== 'BOOLEAN');
  const unitGroups = [...new Set(plottable.map((item) => unitGroupKey(item.point)))];
  const allTimestamps = useMemo(
    () => [...new Set(evidence.flatMap((item) => item.observations.map((observation) => Date.parse(observation.sampledAt))).filter(Number.isFinite))].sort((a, b) => a - b),
    [evidence],
  );
  const [selectedTimestamp, setSelectedTimestamp] = useState<number | undefined>();
  useEffect(() => {
    if (allTimestamps.length === 0) setSelectedTimestamp(undefined);
    else if (selectedTimestamp === undefined || !allTimestamps.includes(selectedTimestamp)) setSelectedTimestamp(allTimestamps.at(-1));
  }, [allTimestamps, selectedTimestamp]);

  const evidenceRows = useMemo<TrendEvidenceRow[]>(
    () => evidence
      .flatMap((item) => item.observations.map((observation) => ({ item, observation })))
      .sort((left, right) => Date.parse(right.observation.sampledAt) - Date.parse(left.observation.sampledAt))
      .slice(0, 120),
    [evidence],
  );

  const evidenceColumns = useMemo<Array<ColumnDef<DataTableFeatures, TrendEvidenceRow>>>(() => [
    {
      id: 'time',
      header: '时间',
      cell: ({ row }) => <span className="font-mono text-muted-foreground tabular-nums">{formatTrendInstant(row.original.observation.sampledAt, site.timezone)}</span>,
    },
    {
      id: 'variable',
      header: '变量',
      cell: ({ row }) => <span className="font-medium text-foreground">{row.original.item.point.displayName}</span>,
    },
    {
      id: 'value',
      header: '值',
      cell: ({ row }) => <span className="font-mono font-semibold tabular-nums">{formatTrendValue(row.original.observation, row.original.item.point)}</span>,
    },
    {
      id: 'type',
      header: '类型',
      cell: ({ row }) => <span className="text-muted-foreground">{pointTypeLabel(row.original.item.point)}</span>,
    },
    {
      id: 'quality',
      header: '质量',
      cell: ({ row }) => (
        <StatusPillBadge
          tone={row.original.observation.quality === 'GOOD' ? 'success' : 'warning'}
          label={qualityLabel(row.original.observation.quality)}
        />
      ),
    },
  ], [site.timezone]);

  const evidenceTable = useDataTable({
    key: `trend-evidence-${site.id}-${window.from}-${window.to}`,
    data: evidenceRows,
    columns: evidenceColumns,
    paginate: false,
    getRowId: (row) => row.observation.observationId,
  });

  if (registryQuery.isPending) return <LoadingTrend />;

  const applyPreset = (preset: TrendRangePreset) => {
    const next = createTrendWindow(preset);
    onSearchChange({ timeStart: next.from, timeEnd: next.to });
  };
  const updateSeries = (ids: readonly string[]) => onSearchChange({ series: serializeTrendSeries(ids) });
  const inspectorIndex = selectedTimestamp === undefined ? -1 : allTimestamps.indexOf(selectedTimestamp);
  const selectedAt = selectedTimestamp ?? allTimestamps.at(-1);
  const sourceSummary = selectedPoints.length === 0 ? '尚未选择变量' : `${selectedPoints.length} 个变量 · 原始历史样本`;
  const businessState = registryQuery.isError ? 'UNAVAILABLE' : selectedPoints.length === 0 ? 'EMPTY' : historyError ? 'PARTIAL' : historyPending ? 'LOADING' : 'READY';

  return (
    <Main className="space-y-4" data-testid="trend-analysis" data-business-state={businessState}>
      <PageIntro
        context={`${site.displayName} · ${site.timezone}`}
        title="趋势分析"
        description="在明确时间窗口内对齐少量关键变量与权威事件，判断什么时候发生了变化；时间上的同时发生不自动等于因果关系。"
        meta={<span>{window.label} · {sourceSummary}</span>}
        actions={(
          <Button
            variant="outline"
            size="sm"
            disabled={registryQuery.isFetching || historyQueries.some((query) => query.isFetching)}
            onClick={() => {
              void registryQuery.refetch();
              historyQueries.forEach((query) => { void query.refetch(); });
              if (eventsEnabled) void alarmQuery.refetch();
            }}
          >
            <RefreshCw />刷新证据
          </Button>
        )}
      />

      <section className="flex flex-wrap items-center gap-2 border-y py-3" data-testid="trend-toolbar" aria-label="趋势分析条件">
        <div className="flex items-center gap-1 rounded-md border bg-background p-1" aria-label="时间范围">
          {(['1h', '6h', '24h'] as const).map((preset) => (
            <Button key={preset} variant={window.preset === preset ? 'secondary' : 'ghost'} size="sm" onClick={() => applyPreset(preset)}>
              {preset === '1h' ? '1 小时' : preset === '6h' ? '6 小时' : '24 小时'}
            </Button>
          ))}
        </div>
        <SeriesPicker points={visiblePoints} devices={devices} selectedIds={selectedIds} onChange={updateSeries} />
        {canReadAlarms ? (
          <Button
            variant={eventsEnabled ? 'secondary' : 'outline'}
            size="sm"
            onClick={() => onSearchChange({ eventTypes: eventsEnabled ? undefined : 'alarm' })}
          >
            <AlarmClock />告警事件{eventsEnabled ? ' 已显示' : ''}
          </Button>
        ) : null}
        <div className="ml-auto text-xs text-muted-foreground">
          {formatTrendInstant(window.from, site.timezone)} — {formatTrendInstant(window.to, site.timezone)}
        </div>
      </section>

      {registryQuery.isError ? (
        <Alert variant="destructive">
          <CircleAlert />
          <AlertTitle>趋势变量暂不可用</AlertTitle>
          <AlertDescription>无法读取当前站点的变量登记信息。本页不会根据历史字段名猜测变量身份。</AlertDescription>
        </Alert>
      ) : selectedPoints.length === 0 ? (
        <section className="grid min-h-[460px] place-items-center rounded-lg border border-dashed" data-testid="trend-empty">
          <div className="max-w-lg px-6 text-center">
            <Activity className="mx-auto size-8 text-muted-foreground" />
            <h2 className="mt-3 text-base font-semibold">选择 1–4 个关键变量开始分析</h2>
            <p className="mt-2 text-sm leading-6 text-muted-foreground">系统不会自动把设备全部点位加入图表。请选择与当前调查问题直接相关的变量。</p>
            <div className="mt-4"><SeriesPicker points={visiblePoints} devices={devices} selectedIds={selectedIds} onChange={updateSeries} /></div>
          </div>
        </section>
      ) : (
        <>
          <section className="border-y" data-testid="trend-series-summary" aria-label="已选变量状态">
            <div className="grid md:grid-cols-2 xl:grid-cols-4 xl:divide-x">
              {evidence.map((item, index) => (
                <div key={item.point.id} className="min-w-0 border-t px-0 py-3 first:border-t-0 md:px-4 md:first:pl-0 xl:border-t-0 xl:first:pl-0">
                  <div className="flex items-center gap-2">
                    <span className="size-2 rounded-full bg-[var(--chart-1)]" aria-hidden="true" />
                    <strong className="truncate text-sm font-medium">{item.point.displayName}</strong>
                  </div>
                  <div className="mt-1 text-lg font-semibold tabular-nums">{formatTrendValue(item.latest, item.point)}</div>
                  <div className="mt-1 text-xs text-muted-foreground">
                    {item.device?.displayName ?? '设备未命名'} · {pointTypeLabel(item.point)}
                    {item.latest ? ` · 质量${qualityLabel(item.latest.quality)}` : ' · 当前窗口无记录'}
                  </div>
                  <span className="sr-only">系列 {index + 1}</span>
                </div>
              ))}
            </div>
          </section>

          {historyError ? (
            <Alert>
              <CircleAlert />
              <AlertTitle>部分历史证据暂不可用</AlertTitle>
              <AlertDescription>已成功返回的变量继续显示；失败变量不会被静默删除，也不会用当前值替代历史。</AlertDescription>
            </Alert>
          ) : null}

          <section className="overflow-hidden rounded-lg border bg-card" data-testid="trend-workspace" aria-labelledby="trend-workspace-title">
            <div className="flex flex-wrap items-start justify-between gap-3 border-b px-5 py-4">
              <div>
                <h2 id="trend-workspace-title" className="text-base font-semibold">时间关系</h2>
                <p className="mt-1 text-xs leading-5 text-muted-foreground">原始历史样本 · 缺测区间断线 · 不同单位自动分面 · 离散状态使用阶梯线。</p>
              </div>
              <Badge variant="outline">{unitGroups.length <= 1 ? '同单位叠加' : `${unitGroups.length} 个单位分面`}</Badge>
            </div>

            <div className="divide-y">
              {unitGroups.map((unit) => {
                const groupItems = plottable.filter((item) => unitGroupKey(item.point) === unit);
                const series: OperationalChartSeries<DeviceHistoryObservation | null>[] = groupItems.map((item) => ({
                  key: `series_${selectedIds.indexOf(item.point.id)}`,
                  label: item.point.displayName,
                  tone: SERIES_TONES[selectedIds.indexOf(item.point.id) % SERIES_TONES.length],
                  interpolation: trendInterpolation(item.point),
                  points: buildTrendData(item.point, item.observations).map((datum) => ({ x: datum.x, value: datum.value, meta: datum.observation })),
                }));
                return (
                  <div key={unit} className="px-4 py-4 sm:px-5" data-testid="trend-unit-panel">
                    <div className="mb-2 flex flex-wrap items-center justify-between gap-2">
                      <strong className="text-sm font-medium">{unit}</strong>
                      <span className="text-xs text-muted-foreground">{groupItems.map((item) => item.point.displayName).join(' · ')}</span>
                    </div>
                    <TimeSeriesChart
                      series={series}
                      unit={unit === '状态 0/1' ? undefined : unit === '无单位' ? undefined : unit}
                      xLabelFormatter={(value) => formatAxisTime(value, site.timezone)}
                      selectedX={selectedAt}
                      onSelectX={(value) => setSelectedTimestamp(Number(value))}
                      tooltipValueFormatter={(point, sourceSeries) => {
                        const meta = point.meta;
                        const sourcePoint = groupItems.find((item) => item.point.displayName === sourceSeries.label)?.point;
                        return sourcePoint && meta ? formatTrendValue(meta, sourcePoint) : '—';
                      }}
                      style={{ height: unitGroups.length === 1 ? 320 : 175 }}
                      ariaLabel={`${unit} 趋势，${groupItems.map((item) => item.point.displayName).join('、')}`}
                    />
                  </div>
                );
              })}

              {nonNumeric.length > 0 ? (
                <div className="px-5 py-4" data-testid="trend-discrete-lane">
                  <h3 className="text-sm font-medium">离散文本状态</h3>
                  <p className="mt-1 text-xs text-muted-foreground">文本或结构化状态不被强制转换为数值曲线。</p>
                  <div className="mt-3 space-y-2">
                    {nonNumeric.map((item) => (
                      <div key={item.point.id} className="flex flex-wrap items-center justify-between gap-2 border-t pt-2 first:border-t-0 first:pt-0">
                        <span className="text-sm">{item.point.displayName}</span>
                        <span className="text-sm font-medium">{formatTrendValue(item.latest, item.point)}</span>
                      </div>
                    ))}
                  </div>
                </div>
              ) : null}
            </div>

            <section className="border-t bg-muted/10 px-5 py-4" data-testid="trend-event-lane" aria-label="权威事件轨道">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <div>
                  <h3 className="text-sm font-medium">事件轨道</h3>
                  <p className="mt-1 text-xs text-muted-foreground">只显示已接入事实 owner 的事件；不会根据曲线峰值推断告警。</p>
                </div>
                {eventsEnabled ? <Badge variant="outline">告警 {events.length}</Badge> : <Badge variant="secondary">未开启事件</Badge>}
              </div>
              {eventsEnabled && alarmQuery.isError ? (
                <p className="mt-3 text-sm text-muted-foreground">告警事件暂不可用，趋势历史仍可继续分析。</p>
              ) : eventsEnabled && events.length > 0 ? (
                <ol className="mt-3 flex gap-3 overflow-x-auto pb-1">
                  {events.map((event) => (
                    <li key={event.id} className="min-w-56 rounded-md border bg-background px-3 py-2">
                      <div className="text-xs text-muted-foreground">{formatTrendInstant(event.at, site.timezone)}</div>
                      <div className="mt-1 text-sm font-medium">{event.label}</div>
                      <div className="mt-1 truncate text-xs text-muted-foreground">{event.title}</div>
                    </li>
                  ))}
                </ol>
              ) : eventsEnabled ? <p className="mt-3 text-sm text-muted-foreground">当前证据窗口内没有告警生命周期事件。</p> : null}
            </section>
          </section>

          <section className="grid min-w-0 gap-4 xl:grid-cols-[minmax(0,0.72fr)_minmax(0,1.28fr)]" data-testid="trend-evidence">
            <div className="min-w-0 rounded-lg border bg-card p-5" data-testid="trend-inspector">
              <div className="flex items-start justify-between gap-3">
                <div>
                  <h2 className="text-sm font-semibold">证据检查点</h2>
                  <p className="mt-1 text-xs text-muted-foreground">图表点击或键盘按钮选择时间；精确值不依赖悬停。</p>
                </div>
                <div className="flex gap-1">
                  <Button variant="ghost" size="icon-sm" aria-label="上一个证据时间" disabled={inspectorIndex <= 0} onClick={() => setSelectedTimestamp(allTimestamps[inspectorIndex - 1])}><ChevronLeft /></Button>
                  <Button variant="ghost" size="icon-sm" aria-label="下一个证据时间" disabled={inspectorIndex < 0 || inspectorIndex >= allTimestamps.length - 1} onClick={() => setSelectedTimestamp(allTimestamps[inspectorIndex + 1])}><ChevronRight /></Button>
                </div>
              </div>
              <div className="mt-4 text-lg font-semibold tabular-nums">{selectedAt !== undefined ? formatTrendInstant(selectedAt, site.timezone) : '暂无样本'}</div>
              <dl className="mt-4 divide-y">
                {evidence.map((item) => {
                  const observation = selectedAt === undefined ? undefined : nearestObservation(item.observations, selectedAt);
                  return (
                    <div key={item.point.id} className="grid grid-cols-[minmax(0,1fr)_auto] gap-3 py-3 first:pt-0 last:pb-0">
                      <div className="min-w-0">
                        <dt className="truncate text-sm font-medium">{item.point.displayName}</dt>
                        <dd className="mt-1 text-xs text-muted-foreground">{observation ? `${formatTrendInstant(observation.sampledAt, site.timezone)} · 质量${qualityLabel(observation.quality)}` : '附近没有可用样本'}</dd>
                      </div>
                      <dd className="text-sm font-semibold tabular-nums">{formatTrendValue(observation, item.point)}</dd>
                    </div>
                  );
                })}
              </dl>
            </div>

            <details className="min-w-0 rounded-lg border bg-card p-5" data-testid="trend-evidence-table">
              <summary className="cursor-pointer text-sm font-semibold">查看结构化证据表</summary>
              <p className="mt-1 text-xs text-muted-foreground">保留样本时间、变量、值、单位和质量；缺失不会显示为 0。</p>
              <DataTable
                table={evidenceTable}
                className="mt-4 gap-0"
                tableClassName="min-w-[760px]"
                tableAriaLabel="趋势历史证据"
                getHeaderRowProps={() => ({ className: 'bg-muted/20 hover:bg-transparent' })}
                getHeaderCellProps={(header) => ({
                  className:
                    header.id === 'value' ? 'text-right text-xs font-semibold' :
                    header.id === 'quality' ? 'w-28 text-center text-xs font-semibold' :
                    'text-xs font-semibold',
                })}
                getRowProps={() => ({ className: 'text-xs hover:bg-muted/40' })}
                getCellProps={(cell) => ({
                  className:
                    cell.column.id === 'value' ? 'text-right' :
                    cell.column.id === 'quality' ? 'text-center' :
                    undefined,
                })}
              />
            </details>
          </section>

          <section className="flex flex-wrap items-center justify-between gap-3 border-t pt-4" aria-label="继续调查">
            <p className="text-xs leading-5 text-muted-foreground">趋势可以支持调查假设，但不能仅凭曲线同时变化宣称因果关系。</p>
            <div className="flex flex-wrap gap-2">
              <Button variant="outline" size="sm" asChild><Link to="/sites/$siteId/alarms" params={{ siteId: site.id }} search={{}}>打开告警</Link></Button>
              <Button variant="outline" size="sm" asChild><Link to="/sites/$siteId/diagnostics" params={{ siteId: site.id }} search={{}}>进入诊断</Link></Button>
              <Button variant="outline" size="sm" asChild><Link to="/sites/$siteId/energy" params={{ siteId: site.id }} search={{}}>查看能源</Link></Button>
            </div>
          </section>
        </>
      )}
    </Main>
  );
}
