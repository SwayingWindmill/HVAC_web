import { useMemo, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { getRouteApi, Link, useNavigate } from '@tanstack/react-router';
import { AlertCircle, CalendarClock, LineChart, RefreshCw, ShieldAlert, X } from 'lucide-react';
import { toast } from 'sonner';
import { PageHeader, PageMeta } from '@/blocks/page-header';
import { Panel } from '@/blocks/panel';
import { StateCard } from '@/blocks/state/state-card';
import { Main } from '@/components/layout/Main';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { Skeleton } from '@/components/ui/skeleton';
import { Switch } from '@/components/ui/switch';
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group';
import { AlarmRequestError } from '@/features/alarms/alarm-api';
import { PRIORITY_BY_SEVERITY } from '@/features/alarms/components/AlarmDetailPane';
import { CommandRequestError } from '@/features/control/command-api';
import { siteMidnight, wallTime } from '@/features/energy-analysis/consumption/model';
import { CreateWorkOrderDialog } from '@/features/work-orders/CreateWorkOrderDialog';
import { workOrderKeys } from '@/features/work-orders/work-order-queries';
import { useNow } from '@/hooks/use-now';
import { formatCompactTime } from '@/lib/operator-format';
import { cn } from '@/lib/utils';
import {
  TrendHistoryPermissionError,
  siteAssetModelQuery,
  trendAggregateWindowQuery,
  trendCommandsQuery,
  trendOverlappingAlarmsQuery,
} from '../query';
import type { TrendGranularity } from '../timeline';
import {
  MAX_TREND_POINTS,
  buildTrendCatalog,
  buildTrendViewModel,
  deriveBuiltInPresets,
  resolveTrendWindow,
  type TrendPointItem,
  type TrendWindowPreset,
} from '../trends-model';
import {
  buildTrendOverlays,
  formatTrendOverlays,
  parseTrendOverlays,
  type TrendOverlayAlarmInput,
} from '../trends-overlays';
import { PointPickerPopover } from './PointPickerPopover';
import { TrendsChartStack } from './TrendsChartStack';
import { TrendsEventRail } from './TrendsEventRail';
import { TrendsStateLane } from './TrendsStateLane';
import { TrendsSummaryTable } from './TrendsSummaryTable';

const siteRoute = getRouteApi('/_app/_site');
const pageRoute = getRouteApi('/_app/_site/operations/trends');

const WINDOW_ITEMS: readonly { readonly value: Exclude<TrendWindowPreset, 'custom'>; readonly label: string }[] = [
  { value: '1h', label: '1 小时' },
  { value: '6h', label: '6 小时' },
  { value: 'today', label: '今日' },
  { value: '7d', label: '7 天' },
];

const GRANULARITY_ITEMS: readonly { readonly value: TrendGranularity; readonly label: string }[] = [
  { value: 'MINUTE', label: '1 分钟' },
  { value: 'FIVE_MINUTE', label: '5 分钟' },
  { value: 'QUARTER_HOUR', label: '15 分钟' },
  { value: 'HOUR', label: '1 小时' },
  { value: 'DAY', label: '按日' },
];

const GRANULARITY_LABELS: Readonly<Record<TrendGranularity, string>> = {
  MINUTE: '每 1 分钟',
  FIVE_MINUTE: '每 5 分钟',
  QUARTER_HOUR: '每 15 分钟',
  HOUR: '每 1 小时',
  DAY: '按日',
};

function pad2(n: number): string {
  return String(n).padStart(2, '0');
}

function formatSiteLocalInput(ms: number, timezone: string): string {
  const w = wallTime(new Date(ms), timezone);
  return `${w.year}-${pad2(w.month)}-${pad2(w.day)}T${pad2(w.hour)}:${pad2(w.minute)}`;
}

function parseSiteLocalInput(raw: string, timezone: string): number | null {
  const match = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})$/.exec(raw.trim());
  if (!match) return null;
  const year = Number(match[1]);
  const month = Number(match[2]);
  const day = Number(match[3]);
  const hour = Number(match[4]);
  const minute = Number(match[5]);
  const baseMs = siteMidnight(year, month, day, timezone).getTime() + (hour * 60 + minute) * 60_000;
  return Number.isFinite(baseMs) ? baseMs : null;
}

export function TrendsWorkspace() {
  const { site, principal } = siteRoute.useRouteContext();
  const search = pageRoute.useSearch();
  const navigate = useNavigate({ from: '/operations/trends' });
  const queryClient = useQueryClient();
  const nowMs = useNow(60_000);
  const [hoverAt, setHoverAt] = useState<number | null>(null);
  const [workOrderAlarm, setWorkOrderAlarm] = useState<TrendOverlayAlarmInput | null>(null);
  const [customOpen, setCustomOpen] = useState(false);
  const [customFromDraft, setCustomFromDraft] = useState('');
  const [customToDraft, setCustomToDraft] = useState('');

  const capabilities = principal.authorization.capabilities;
  const canListAlarms = capabilities.includes('alarm.list');
  const canReadCommands = capabilities.includes('site.read');
  const canCreateWorkOrder = capabilities.includes('work-order.create');

  const assetModel = useQuery(siteAssetModelQuery(site.id));
  const catalog = useMemo(
    () => (assetModel.data ? buildTrendCatalog(assetModel.data) : null),
    [assetModel.data],
  );
  const presets = useMemo(
    () => (catalog ? deriveBuiltInPresets(catalog) : []),
    [catalog],
  );

  const activePreset = useMemo(() => {
    if (search.points) return null;
    if (search.preset) {
      return presets.find((candidate) => candidate.id === search.preset) ?? presets[0] ?? null;
    }
    return presets[0] ?? null;
  }, [presets, search.points, search.preset]);

  const selectedRefs = useMemo<readonly string[]>(() => {
    if (!catalog) return [];
    if (search.points) {
      const parsed = search.points
        .split(',')
        .map((item) => item.trim())
        .filter((ref) => catalog.byRef.has(ref));
      if (parsed.length > 0) return parsed.slice(0, MAX_TREND_POINTS);
    }
    if (activePreset) return activePreset.pointRefs;
    return catalog.points.slice(0, 4).map((item) => item.ref);
  }, [activePreset, catalog, search.points]);

  const selectedPoints = useMemo<readonly TrendPointItem[]>(() => {
    if (!catalog) return [];
    return selectedRefs
      .map((ref) => catalog.byRef.get(ref))
      .filter((item): item is TrendPointItem => Boolean(item));
  }, [catalog, selectedRefs]);

  const windowState = useMemo(
    () =>
      resolveTrendWindow({
        window: search.window ?? '6h',
        from: search.from,
        to: search.to,
        granularity: search.granularity ?? 'auto',
        now: new Date(nowMs),
        timezone: site.timezone,
      }),
    [nowMs, search.from, search.granularity, search.to, search.window, site.timezone],
  );

  const showCompare = search.compare === 'prev';
  const viewMode = search.view === 'table' ? 'table' : 'chart';
  const overlayFlags = useMemo(() => parseTrendOverlays(search.overlays), [search.overlays]);

  const alarmsOverlayQuery = useQuery(
    trendOverlappingAlarmsQuery({
      siteId: site.id,
      fromMs: windowState.fromMs,
      toMs: windowState.toMs,
      enabled: overlayFlags.alarms && canListAlarms,
    }),
  );

  const commandsOverlayQuery = useQuery(
    trendCommandsQuery({
      siteId: site.id,
      enabled: overlayFlags.commands && canReadCommands,
    }),
  );

  const alarmsForbidden =
    !canListAlarms ||
    (alarmsOverlayQuery.error instanceof AlarmRequestError &&
      (alarmsOverlayQuery.error.status === 401 || alarmsOverlayQuery.error.status === 403));

  const commandsForbidden =
    !canReadCommands ||
    (commandsOverlayQuery.error instanceof CommandRequestError &&
      (commandsOverlayQuery.error.status === 401 || commandsOverlayQuery.error.status === 403));

  const showAlarmsActive = overlayFlags.alarms && !alarmsForbidden;
  const showCommandsActive = overlayFlags.commands && !commandsForbidden;

  const overlaysModel = useMemo(
    () =>
      buildTrendOverlays({
        window: windowState,
        selectedPoints,
        catalog,
        showAlarms: showAlarmsActive,
        showCommands: showCommandsActive,
        alarms: alarmsOverlayQuery.data,
        commands: commandsOverlayQuery.data,
      }),
    [
      alarmsOverlayQuery.data,
      catalog,
      commandsOverlayQuery.data,
      selectedPoints,
      showAlarmsActive,
      showCommandsActive,
      windowState,
    ],
  );

  const currentQuery = useQuery(
    trendAggregateWindowQuery({
      siteId: site.id,
      timezone: site.timezone,
      csrfToken: principal.session.csrfToken,
      points: selectedPoints,
      fromMs: windowState.fromMs,
      toMs: windowState.toMs,
      granularity: windowState.granularity,
    }),
  );

  const compareQuery = useQuery(
    trendAggregateWindowQuery({
      siteId: site.id,
      timezone: site.timezone,
      csrfToken: principal.session.csrfToken,
      points: selectedPoints.filter((p) => p.kind === 'gauge'),
      fromMs: windowState.compareFromMs,
      toMs: windowState.compareToMs,
      granularity: windowState.granularity,
      enabled: showCompare,
    }),
  );

  const viewModel = useMemo(() => {
    if (!currentQuery.data) return null;
    return buildTrendViewModel({
      selectedPoints,
      window: windowState,
      currentBucketsByDevice: currentQuery.data,
      compareBucketsByDevice: showCompare ? compareQuery.data : undefined,
    });
  }, [compareQuery.data, currentQuery.data, selectedPoints, showCompare, windowState]);

  const handleApplyRefs = (nextRefs: readonly string[]) => {
    const capped = nextRefs.slice(0, MAX_TREND_POINTS);
    void navigate({
      search: (prev) => ({
        ...prev,
        preset: undefined,
        points: capped.length > 0 ? capped.join(',') : undefined,
      }),
    });
  };

  const rangeText = `${formatCompactTime(new Date(windowState.fromMs).toISOString(), site.timezone)} – ${formatCompactTime(new Date(windowState.toMs).toISOString(), site.timezone)}`;

  const parsedCustomFromMs = parseSiteLocalInput(customFromDraft, site.timezone);
  const parsedCustomToMs = parseSiteLocalInput(customToDraft, site.timezone);
  const customRangeValid =
    parsedCustomFromMs !== null &&
    parsedCustomToMs !== null &&
    parsedCustomToMs > parsedCustomFromMs &&
    parsedCustomToMs - parsedCustomFromMs <= 366 * 86_400_000;

  return (
    <Main>
      <PageHeader
        title="历史趋势"
        meta={
          <PageMeta>
            {rangeText} · {GRANULARITY_LABELS[windowState.granularity]}聚合 · {windowState.slots.length} 个时段
          </PageMeta>
        }
        actions={
          <div className="flex items-center gap-2">
            <ToggleGroup
              type="single"
              variant="outline"
              size="sm"
              spacing={0}
              value={viewMode}
              onValueChange={(val) => {
                if (!val) return;
                void navigate({
                  search: (prev) => ({
                    ...prev,
                    view: val === 'table' ? 'table' : undefined,
                  }),
                });
              }}
              aria-label="展示模式"
            >
              <ToggleGroupItem value="chart" className="h-8 px-3 text-xs">
                曲线
              </ToggleGroupItem>
              <ToggleGroupItem value="table" className="h-8 px-3 text-xs">
                统计表
              </ToggleGroupItem>
            </ToggleGroup>
            <Button
              variant="outline"
              size="sm"
              onClick={() => {
                void queryClient.invalidateQueries({ queryKey: ['trends-aggregate', site.id] });
              }}
            >
              <RefreshCw data-icon="inline-start" />
              刷新
            </Button>
          </div>
        }
      />

      {assetModel.isPending ? (
        <Skeleton className="h-[520px] rounded-xl" />
      ) : assetModel.isError || !catalog ? (
        <Panel aria-label="趋势异常">
          <StateCard
            icon={AlertCircle}
            tone="danger"
            title={<h2 className="text-sm font-semibold">站点设备点位目录暂时无法读取</h2>}
            description="请检查网络或稍后重新加载站点设备目录。"
          >
            <Button variant="outline" size="sm" onClick={() => void assetModel.refetch()}>
              重试
            </Button>
          </StateCard>
        </Panel>
      ) : catalog.points.length === 0 ? (
        <Panel aria-label="暂无点位">
          <StateCard
            icon={LineChart}
            title={<h2 className="text-sm font-semibold">当前站点尚未接入可趋势化的遥测点位</h2>}
            description="完成网关与设备点位启用后即可在此对比多点位历史曲线。"
          />
        </Panel>
      ) : (
        <Panel aria-label="历史趋势工作台">
          {/* L1 Control Strip: Presets + Point Picker on left, Window + Granularity + Overlays on right */}
          <div className="flex flex-wrap items-center justify-between gap-3 border-b border-divider px-5 py-3">
            <div className="flex flex-wrap items-center gap-2">
              {presets.length > 0 ? (
                <ToggleGroup
                  type="single"
                  variant="outline"
                  size="sm"
                  value={activePreset?.id ?? ''}
                  onValueChange={(val) => {
                    const matched = presets.find((preset) => preset.id === val);
                    if (!matched) return;
                    void navigate({
                      search: (prev) => ({
                        ...prev,
                        preset: matched.id,
                        points: undefined,
                      }),
                    });
                  }}
                  aria-label="排障预设"
                >
                  {presets.map((preset) => (
                    <ToggleGroupItem key={preset.id} value={preset.id} className="h-7 px-2.5 text-xs">
                      {preset.title}
                    </ToggleGroupItem>
                  ))}
                </ToggleGroup>
              ) : null}

              <PointPickerPopover
                catalog={catalog}
                selectedRefs={selectedRefs}
                onApplyRefs={handleApplyRefs}
              />
            </div>

            <div className="flex flex-wrap items-center gap-3">
              <div className="flex items-center gap-1.5">
                <ToggleGroup
                  type="single"
                  variant="outline"
                  size="sm"
                  spacing={0}
                  value={windowState.preset === 'custom' ? '' : windowState.preset}
                  onValueChange={(val) => {
                    const matched = WINDOW_ITEMS.find((item) => item.value === val);
                    if (!matched) return;
                    void navigate({
                      search: (prev) => ({
                        ...prev,
                        window: matched.value,
                        from: undefined,
                        to: undefined,
                        granularity: undefined,
                      }),
                    });
                  }}
                  aria-label="时间范围"
                >
                  {WINDOW_ITEMS.map((item) => (
                    <ToggleGroupItem key={item.value} value={item.value} className="h-7 px-2.5 text-xs">
                      {item.label}
                    </ToggleGroupItem>
                  ))}
                </ToggleGroup>

                <Popover
                  open={customOpen}
                  onOpenChange={(nextOpen) => {
                    if (nextOpen) {
                      setCustomFromDraft(formatSiteLocalInput(windowState.fromMs, site.timezone));
                      setCustomToDraft(formatSiteLocalInput(windowState.toMs, site.timezone));
                    }
                    setCustomOpen(nextOpen);
                  }}
                >
                  <PopoverTrigger asChild>
                    <Button
                      variant="outline"
                      size="sm"
                      aria-pressed={windowState.preset === 'custom'}
                      className={cn(
                        'h-7 gap-1 px-2.5 text-xs',
                        windowState.preset === 'custom' && 'border-input-hover bg-accent text-foreground',
                      )}
                    >
                      <CalendarClock className="size-3.5" aria-hidden="true" />
                      自定义
                    </Button>
                  </PopoverTrigger>
                  <PopoverContent align="end" className="flex w-72 flex-col gap-3 p-3">
                    <p className="text-xs font-medium">自定义时段（站点时间）</p>
                    <label className="flex flex-col gap-1 text-xs text-muted-foreground">
                      <span>开始时间</span>
                      <Input
                        type="datetime-local"
                        aria-label="开始时间"
                        value={customFromDraft}
                        onChange={(ev) => setCustomFromDraft(ev.target.value)}
                        className="h-8 font-mono text-xs tabular-nums"
                      />
                    </label>
                    <label className="flex flex-col gap-1 text-xs text-muted-foreground">
                      <span>结束时间</span>
                      <Input
                        type="datetime-local"
                        aria-label="结束时间"
                        value={customToDraft}
                        onChange={(ev) => setCustomToDraft(ev.target.value)}
                        className="h-8 font-mono text-xs tabular-nums"
                      />
                    </label>
                    <div className="flex items-center justify-end gap-2 pt-1">
                      <Button
                        type="button"
                        variant="ghost"
                        size="sm"
                        className="h-7 px-2.5 text-xs"
                        onClick={() => setCustomOpen(false)}
                      >
                        取消
                      </Button>
                      <Button
                        type="button"
                        size="sm"
                        className="h-7 px-3 text-xs"
                        disabled={!customRangeValid}
                        onClick={() => {
                          if (!customRangeValid) return;
                          void navigate({
                            search: (prev) => ({
                              ...prev,
                              window: 'custom',
                              from: new Date(parsedCustomFromMs).toISOString(),
                              to: new Date(parsedCustomToMs).toISOString(),
                              granularity: undefined,
                            }),
                          });
                          setCustomOpen(false);
                        }}
                      >
                        应用
                      </Button>
                    </div>
                  </PopoverContent>
                </Popover>
              </div>

              <ToggleGroup
                type="single"
                variant="outline"
                size="sm"
                spacing={0}
                value={windowState.granularity}
                onValueChange={(val) => {
                  const matched = GRANULARITY_ITEMS.find((item) => item.value === val);
                  if (!matched) return;
                  void navigate({
                    search: (prev) => ({
                      ...prev,
                      granularity: matched.value,
                    }),
                  });
                }}
                aria-label="聚合粒度"
              >
                {GRANULARITY_ITEMS.map((item) => {
                  const allowed = windowState.allowedGranularities.includes(item.value);
                  return (
                    <ToggleGroupItem
                      key={item.value}
                      value={item.value}
                      disabled={!allowed}
                      title={!allowed ? '当前时间跨度下该粒度超过 720 桶上限' : undefined}
                      className="h-7 px-2 text-xs"
                    >
                      {item.label}
                    </ToggleGroupItem>
                  );
                })}
              </ToggleGroup>

              <label className="inline-flex cursor-pointer items-center gap-1.5 text-xs text-muted-foreground select-none">
                <Switch
                  size="sm"
                  aria-label={windowState.compareLabel}
                  checked={showCompare}
                  onCheckedChange={(checked) => {
                    void navigate({
                      search: (prev) => ({
                        ...prev,
                        compare: checked ? 'prev' : undefined,
                      }),
                    });
                  }}
                />
                <span>{windowState.compareLabel}</span>
              </label>

              <label className="inline-flex items-center gap-1.5 text-xs text-muted-foreground select-none">
                <Switch
                  size="sm"
                  aria-label="叠加告警"
                  disabled={alarmsForbidden}
                  checked={showAlarmsActive}
                  onCheckedChange={(checked) => {
                    void navigate({
                      search: (prev) => ({
                        ...prev,
                        overlays: formatTrendOverlays({
                          alarms: checked,
                          commands: overlayFlags.commands,
                        }),
                      }),
                    });
                  }}
                />
                <span>叠加告警</span>
                {alarmsForbidden ? (
                  <span className="text-[11px] text-subtle-foreground">无告警查看权限</span>
                ) : null}
              </label>

              <label className="inline-flex items-center gap-1.5 text-xs text-muted-foreground select-none">
                <Switch
                  size="sm"
                  aria-label="叠加控制指令"
                  disabled={commandsForbidden}
                  checked={showCommandsActive}
                  onCheckedChange={(checked) => {
                    void navigate({
                      search: (prev) => ({
                        ...prev,
                        overlays: formatTrendOverlays({
                          alarms: overlayFlags.alarms,
                          commands: checked,
                        }),
                      }),
                    });
                  }}
                />
                <span>叠加控制指令</span>
                {commandsForbidden ? (
                  <span className="text-[11px] text-subtle-foreground">无指令查看权限</span>
                ) : null}
              </label>
            </div>
          </div>

          {search.source && search.sourceTitle ? (
            <div
              role="status"
              className="flex flex-wrap items-center justify-between gap-2 border-b border-divider bg-primary-soft/55 px-5 py-2 text-xs"
            >
              <div className="flex flex-wrap items-center gap-2">
                <span className="font-medium text-foreground">
                  {search.source === 'alarm'
                    ? `来自告警：${search.sourceTitle}`
                    : `来自诊断：${search.sourceTitle}`}
                </span>
                {search.source === 'alarm' ? (
                  <Link
                    to="/operations/alarms"
                    search={{ site: site.id, inspect: search.sourceId, view: 'all' }}
                    className="font-medium text-primary hover:underline"
                  >
                    返回告警 ›
                  </Link>
                ) : (
                  <Link
                    to="/operations/diagnostics"
                    search={{ site: site.id, inspect: search.sourceId, range: 'all' }}
                    className="font-medium text-primary hover:underline"
                  >
                    返回诊断 ›
                  </Link>
                )}
              </div>
              <button
                type="button"
                aria-label="关闭来源提示"
                onClick={() => {
                  void navigate({
                    search: (prev) => ({
                      ...prev,
                      source: undefined,
                      sourceId: undefined,
                      sourceTitle: undefined,
                    }),
                  });
                }}
                className="rounded p-0.5 text-muted-foreground hover:bg-accent hover:text-foreground"
              >
                <X className="size-3.5" aria-hidden="true" />
              </button>
            </div>
          ) : null}

          {/* Selected Point Chips + Unconnected Preset Notice */}
          <div className="flex flex-wrap items-center gap-1.5 border-b border-divider bg-muted/20 px-5 py-2">
            {selectedPoints.map((point) => (
              <span
                key={point.ref}
                className="inline-flex items-center gap-1.5 rounded-md border bg-background px-2 py-0.5 text-xs"
              >
                <span className="truncate">{point.seriesLabel}</span>
                {point.kind === 'state' ? (
                  <span className="text-[11px] text-muted-foreground">启停</span>
                ) : point.unit ? (
                  <span className="text-[11px] text-muted-foreground">{point.unit}</span>
                ) : null}
                {selectedPoints.length > 1 ? (
                  <button
                    type="button"
                    aria-label={`移除 ${point.seriesLabel}`}
                    onClick={() => handleApplyRefs(selectedRefs.filter((item) => item !== point.ref))}
                    className="rounded-sm p-0.5 text-muted-foreground hover:bg-accent hover:text-foreground"
                  >
                    <X className="size-3" aria-hidden="true" />
                  </button>
                ) : null}
              </span>
            ))}

            {activePreset && activePreset.unconnectedLabels.length > 0 ? (
              <span className="ml-auto text-xs text-muted-foreground">
                未接入：{activePreset.unconnectedLabels.join('、')}
              </span>
            ) : null}
          </div>

          {/* Body: Loading / Permission / Error / Empty / Chart or Summary Table */}
          {currentQuery.isPending && !viewModel ? (
            <div className="p-5">
              <Skeleton className="h-[380px] rounded-lg" />
            </div>
          ) : currentQuery.error instanceof TrendHistoryPermissionError ? (
            <StateCard
              icon={ShieldAlert}
              tone="warning"
              title={<h2 className="text-sm font-semibold">当前账号没有该设备的历史读数查看权限</h2>}
              description="如需查看历史趋势，请联系管理员开通遥测历史读取权限。"
            />
          ) : currentQuery.isError || !viewModel ? (
            <StateCard
              icon={AlertCircle}
              tone="danger"
              title={<h2 className="text-sm font-semibold">历史趋势数据暂时无法读取</h2>}
              description="请稍后重试，或切换较短的时间范围重新查询。"
            >
              <Button variant="outline" size="sm" onClick={() => void currentQuery.refetch()}>
                重试
              </Button>
            </StateCard>
          ) : viewModel.summaryRows.every((row) => row.validBuckets === 0) && viewModel.statePoints.length === 0 ? (
            <StateCard
              icon={LineChart}
              title={<h2 className="text-sm font-semibold">所选时段内暂无有效历史读数</h2>}
              description="可切换至更宽的时间范围（如 7 天）或检查网关采集状态。"
            />
          ) : viewMode === 'table' ? (
            <TrendsSummaryTable rows={viewModel.summaryRows} timezone={site.timezone} />
          ) : (
            <>
              {showAlarmsActive || showCommandsActive ? (
                <TrendsEventRail
                  siteId={site.id}
                  timezone={site.timezone}
                  overlays={overlaysModel}
                  canCreateWorkOrder={canCreateWorkOrder}
                  onCreateWorkOrderForAlarm={(alarm) => setWorkOrderAlarm(alarm)}
                />
              ) : null}
              <TrendsChartStack
                lanes={viewModel.gaugeLanes}
                fromMs={windowState.fromMs}
                toMs={windowState.toMs}
                granularity={windowState.granularity}
                timezone={site.timezone}
                showCompare={showCompare}
                compareLabel={windowState.compareLabel}
                alarmSpans={overlaysModel.alarmSpans.map((span) => ({
                  id: span.id,
                  fromMs: span.clampedStartMs,
                  toMs: Math.max(
                    span.clampedEndMs,
                    Math.min(
                      windowState.toMs,
                      span.clampedStartMs +
                        Math.max(15_000, Math.round((windowState.toMs - windowState.fromMs) * 0.004)),
                    ),
                  ),
                  severity: span.alarm.currentSeverity,
                  label: span.alarm.title,
                }))}
                commandMarks={overlaysModel.commandMarks.map((mark) => ({
                  id: mark.id,
                  atMs: mark.atMs,
                  label: mark.summaryLabel,
                }))}
                hoverAt={hoverAt}
                onHoverAt={setHoverAt}
              />
              <TrendsStateLane
                tracks={viewModel.stateTracks}
                fromMs={windowState.fromMs}
                toMs={windowState.toMs}
                timezone={site.timezone}
                hoverAt={hoverAt}
                onHoverAt={setHoverAt}
              />
            </>
          )}
        </Panel>
      )}

      {workOrderAlarm ? (
        <CreateWorkOrderDialog
          open={Boolean(workOrderAlarm)}
          onOpenChange={(open) => {
            if (!open) setWorkOrderAlarm(null);
          }}
          title="由告警创建工单"
          initial={{
            title: workOrderAlarm.title,
            priority: PRIORITY_BY_SEVERITY[workOrderAlarm.currentSeverity],
            description: [
              workOrderAlarm.summary,
              `趋势时段：${rangeText}`,
              selectedPoints.length > 0
                ? `排查点位：${selectedPoints.map((p) => p.seriesLabel).join('、')}`
                : '',
            ]
              .filter(Boolean)
              .join('\n'),
          }}
          sourceReferences={[
            {
              domain: 'ALARM',
              resourceId: workOrderAlarm.alarmId,
              relationship: 'ORIGIN',
            },
          ]}
          onCreated={() => {
            void queryClient.invalidateQueries({ queryKey: workOrderKeys.all(site.id) });
            toast.success('工单已创建');
          }}
        />
      ) : null}
    </Main>
  );
}
