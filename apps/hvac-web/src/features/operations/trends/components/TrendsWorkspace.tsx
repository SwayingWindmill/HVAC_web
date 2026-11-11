import { useMemo, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { getRouteApi, useNavigate } from '@tanstack/react-router';
import { LineChart, RefreshCw, ShieldAlert, X } from 'lucide-react';
import { PageHeader, PageMeta } from '@/blocks/page-header';
import { Panel } from '@/blocks/panel';
import { Main } from '@/components/layout/Main';
import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { Switch } from '@/components/ui/switch';
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group';
import { useNow } from '@/hooks/use-now';
import { formatCompactTime } from '@/lib/operator-format';
import {
  TrendHistoryPermissionError,
  siteAssetModelQuery,
  trendAggregateWindowQuery,
} from '../query';
import type { TrendGranularity } from '../timeline';
import {
  MAX_TREND_POINTS,
  buildTrendCatalog,
  buildTrendViewModel,
  deriveBuiltInPresets,
  resolveTrendWindow,
  type TrendPointItem,
  type TrendPresetId,
  type TrendWindowPreset,
} from '../trends-model';
import { PointPickerPopover } from './PointPickerPopover';
import { TrendsChartStack } from './TrendsChartStack';
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

export function TrendsWorkspace() {
  const { site, principal } = siteRoute.useRouteContext();
  const search = pageRoute.useSearch();
  const navigate = useNavigate({ from: '/operations/trends' });
  const queryClient = useQueryClient();
  const nowMs = useNow(60_000);
  const [hoverAt, setHoverAt] = useState<number | null>(null);

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

  const handleToggleRef = (ref: string) => {
    const exists = selectedRefs.includes(ref);
    const next = exists
      ? selectedRefs.filter((item) => item !== ref)
      : selectedRefs.length < MAX_TREND_POINTS
        ? [...selectedRefs, ref]
        : selectedRefs;
    void navigate({
      search: (prev) => ({
        ...prev,
        preset: undefined,
        points: next.length > 0 ? next.join(',') : undefined,
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
          <div className="flex flex-col items-start gap-2 px-5 py-10">
            <p className="text-sm font-semibold">站点设备点位目录暂时无法读取</p>
            <Button variant="outline" size="sm" onClick={() => void assetModel.refetch()}>
              重试
            </Button>
          </div>
        </Panel>
      ) : catalog.points.length === 0 ? (
        <Panel aria-label="暂无点位">
          <div className="flex flex-col items-center gap-2 px-6 py-14 text-center">
            <span className="flex size-9 items-center justify-center rounded-xl bg-muted text-muted-foreground">
              <LineChart className="size-4" aria-hidden="true" />
            </span>
            <p className="text-sm font-semibold">当前站点尚未接入可趋势化的遥测点位</p>
          </div>
        </Panel>
      ) : (
        <Panel aria-label="历史趋势工作台">
          {/* L1 Control Strip: Presets + Point Picker on left, Window + Granularity + Compare on right */}
          <div className="flex flex-wrap items-center justify-between gap-3 border-b border-divider px-5 py-3">
            <div className="flex flex-wrap items-center gap-2">
              {presets.length > 0 ? (
                <ToggleGroup
                  type="single"
                  variant="outline"
                  size="sm"
                  value={activePreset?.id ?? ''}
                  onValueChange={(val) => {
                    if (!val) return;
                    void navigate({
                      search: (prev) => ({
                        ...prev,
                        preset: val as TrendPresetId,
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
                onToggleRef={handleToggleRef}
              />
            </div>

            <div className="flex flex-wrap items-center gap-3">
              <ToggleGroup
                type="single"
                variant="outline"
                size="sm"
                spacing={0}
                value={windowState.preset === 'custom' ? '' : windowState.preset}
                onValueChange={(val) => {
                  if (!val) return;
                  void navigate({
                    search: (prev) => ({
                      ...prev,
                      window: val as Exclude<TrendWindowPreset, 'custom'>,
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

              <ToggleGroup
                type="single"
                variant="outline"
                size="sm"
                spacing={0}
                value={windowState.granularity}
                onValueChange={(val) => {
                  if (!val) return;
                  void navigate({
                    search: (prev) => ({
                      ...prev,
                      granularity: val as TrendGranularity,
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
            </div>
          </div>

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
                    onClick={() => handleToggleRef(point.ref)}
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
            <div className="flex flex-col items-center gap-2 px-6 py-14 text-center">
              <span className="flex size-9 items-center justify-center rounded-xl bg-muted text-muted-foreground">
                <ShieldAlert className="size-4" aria-hidden="true" />
              </span>
              <p className="text-sm font-semibold">当前账号没有该设备的历史读数查看权限</p>
              <p className="text-xs text-muted-foreground">如需查看历史趋势，请联系管理员开通遥测历史读取权限</p>
            </div>
          ) : currentQuery.isError || !viewModel ? (
            <div className="flex flex-col items-start gap-2 px-5 py-10">
              <p className="text-sm font-semibold">历史趋势数据暂时无法读取</p>
              <Button variant="outline" size="sm" onClick={() => void currentQuery.refetch()}>
                重试
              </Button>
            </div>
          ) : viewModel.summaryRows.every((row) => row.validBuckets === 0) && viewModel.statePoints.length === 0 ? (
            <div className="flex flex-col items-center gap-2 px-6 py-14 text-center">
              <p className="text-sm font-semibold">所选时段内暂无有效历史读数</p>
              <p className="text-xs text-muted-foreground">可切换至更宽的时间范围（如 7 天）或检查网关采集状态</p>
            </div>
          ) : viewMode === 'table' ? (
            <TrendsSummaryTable rows={viewModel.summaryRows} timezone={site.timezone} />
          ) : (
            <>
              <TrendsChartStack
                lanes={viewModel.gaugeLanes}
                fromMs={windowState.fromMs}
                toMs={windowState.toMs}
                granularity={windowState.granularity}
                timezone={site.timezone}
                showCompare={showCompare}
                compareLabel={windowState.compareLabel}
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
    </Main>
  );
}
