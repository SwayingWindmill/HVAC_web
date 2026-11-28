import { useMemo, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { getRouteApi, Link, useNavigate } from '@tanstack/react-router';
import { AlertCircle, LineChart, RefreshCw, ShieldAlert, X } from 'lucide-react';
import { PageHeader, PageMeta } from '@/blocks/page-header';
import { Panel } from '@/blocks/panel';
import { StateCard } from '@/blocks/state/state-card';
import { Main } from '@/components/layout/Main';
import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group';
import { AlarmRequestError } from '@/features/alarms/alarm-api';
import { PRIORITY_BY_SEVERITY } from '@/features/alarms/components/AlarmDetailPane';
import { CommandRequestError } from '@/features/control/command-api';
import { CreateWorkOrderDialog } from '@/features/work-orders/CreateWorkOrderDialog';
import { workOrderKeys } from '@/features/work-orders/work-order-queries';
import { useNow } from '@/hooks/use-now';
import { formatCompactTime } from '@/lib/operator-format';
import { siteAssetModelQuery } from '@/api/site-asset-model';
import {
  TrendHistoryPermissionError,
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
} from '../trends-model';
import {
  buildTrendOverlays,
  formatTrendOverlays,
  parseTrendOverlays,
  type TrendOverlayAlarmInput,
} from '../trends-overlays';
import { createTrendsHoverStore } from '../trends-hover-store';
import { TrendsChartStack } from './TrendsChartStack';
import { TrendsControlStrip } from './TrendsControlStrip';
import { TrendsEventRail } from './TrendsEventRail';
import { TrendsStateLane } from './TrendsStateLane';
import { TrendsSummaryTable } from './TrendsSummaryTable';

const siteRoute = getRouteApi('/_app/_site');
const pageRoute = getRouteApi('/_app/_site/operations/trends');

const GRANULARITY_LABELS: Readonly<Record<TrendGranularity, string>> = {
  MINUTE: '每 1 分钟',
  FIVE_MINUTE: '每 5 分钟',
  QUARTER_HOUR: '每 15 分钟',
  HOUR: '每 1 小时',
  DAY: '按日',
};

export function TrendsWorkspace() {
  const { site, principal } = siteRoute.useRouteContext();
  const search = pageRoute.useSearch();
  const navigate = useNavigate({ from: '/operations/trends' });
  const queryClient = useQueryClient();
  const nowMs = useNow(60_000);
  const hoverStore = useMemo(() => createTrendsHoverStore(), []);
  const [workOrderAlarm, setWorkOrderAlarm] = useState<TrendOverlayAlarmInput | null>(null);

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

  const chartAlarmSpans = useMemo(
    () =>
      overlaysModel.alarmSpans.map((span) => ({
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
      })),
    [overlaysModel.alarmSpans, windowState.fromMs, windowState.toMs],
  );

  const chartCommandMarks = useMemo(
    () =>
      overlaysModel.commandMarks.map((mark) => ({
        id: mark.id,
        atMs: mark.atMs,
        label: mark.summaryLabel,
      })),
    [overlaysModel.commandMarks],
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
          <TrendsControlStrip
            presets={presets}
            activePreset={activePreset}
            catalog={catalog}
            selectedRefs={selectedRefs}
            windowState={windowState}
            timezone={site.timezone}
            showCompare={showCompare}
            alarmsForbidden={alarmsForbidden}
            commandsForbidden={commandsForbidden}
            showAlarmsActive={showAlarmsActive}
            showCommandsActive={showCommandsActive}
            onApplyRefs={handleApplyRefs}
            onSelectPreset={(presetId) => {
              void navigate({
                search: (prev) => ({
                  ...prev,
                  preset: presetId,
                  points: undefined,
                }),
              });
            }}
            onSelectWindow={(windowPreset) => {
              void navigate({
                search: (prev) => ({
                  ...prev,
                  window: windowPreset,
                  from: undefined,
                  to: undefined,
                  granularity: undefined,
                }),
              });
            }}
            onApplyCustomRange={(fromIso, toIso) => {
              void navigate({
                search: (prev) => ({
                  ...prev,
                  window: 'custom',
                  from: fromIso,
                  to: toIso,
                  granularity: undefined,
                }),
              });
            }}
            onSelectGranularity={(granularity) => {
              void navigate({
                search: (prev) => ({
                  ...prev,
                  granularity,
                }),
              });
            }}
            onToggleCompare={(checked) => {
              void navigate({
                search: (prev) => ({
                  ...prev,
                  compare: checked ? 'prev' : undefined,
                }),
              });
            }}
            onToggleAlarms={(checked) => {
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
            onToggleCommands={(checked) => {
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
                  isPending={(showAlarmsActive && alarmsOverlayQuery.isPending) || (showCommandsActive && commandsOverlayQuery.isPending)}
                  isError={(showAlarmsActive && alarmsOverlayQuery.isError) || (showCommandsActive && commandsOverlayQuery.isError)}
                  onRetry={() => { if (showAlarmsActive && alarmsOverlayQuery.isError) void alarmsOverlayQuery.refetch(); if (showCommandsActive && commandsOverlayQuery.isError) void commandsOverlayQuery.refetch(); }}
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
                alarmSpans={chartAlarmSpans}
                commandMarks={chartCommandMarks}
                hoverStore={hoverStore}
              />
              <TrendsStateLane
                tracks={viewModel.stateTracks}
                fromMs={windowState.fromMs}
                toMs={windowState.toMs}
                timezone={site.timezone}
                hoverStore={hoverStore}
              />
            </>
          )}
        </Panel>
      )}

      {workOrderAlarm ? (
        <CreateWorkOrderDialog
          open={Boolean(workOrderAlarm)}
          onOpenChange={(open) => { if (!open) setWorkOrderAlarm(null); }}
          title="由告警创建工单"
          initial={{
            title: workOrderAlarm.title,
            priority: PRIORITY_BY_SEVERITY[workOrderAlarm.currentSeverity],
            description: [workOrderAlarm.summary, `趋势时段：${rangeText}`, selectedPoints.length > 0 ? `排查点位：${selectedPoints.map((p) => p.seriesLabel).join('、')}` : ''].filter(Boolean).join('\n'),
          }}
          sourceReferences={[{ domain: 'ALARM', resourceId: workOrderAlarm.alarmId, relationship: 'ORIGIN' }]}
          onCreated={() => void queryClient.invalidateQueries({ queryKey: workOrderKeys.all(site.id) })}
        />
      ) : null}
    </Main>
  );
}
