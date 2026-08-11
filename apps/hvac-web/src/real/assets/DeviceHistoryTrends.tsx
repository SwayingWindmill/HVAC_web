import { useEffect, useMemo, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import ReactECharts from 'echarts-for-react';
import type { CurrentPrincipalResponse, Site } from '../../api/generated/platformGateway.gen.ts';
import type { DeviceHistorySeries, S2TelemetryClient } from '../../api/generated/s2Telemetry.gen.ts';
import type { ProtectedScopeRequestToken } from '../protected-scope.ts';
import type { RealAssetsPointDefinition } from './catalog.ts';
import {
  REAL_ASSETS_HISTORY_RANGES,
  buildRealAssetsTrendData,
  classifyRealAssetsHistoryFailure,
  createRealAssetsHistoryQuery,
  formatRealAssetsHistoryInstant,
  historySeriesUnit,
  listRealAssetsTrendDefinitions,
  loadRealAssetsHistory,
  realAssetsHistoryQueryKey,
  realAssetsHistoryRevisionKey,
  type RealAssetsHistoryQuery,
  type RealAssetsHistoryRange,
} from './history.ts';
import type { RealAssetsDeviceRow } from './model.ts';
import { runRealAssetsProtectedRequest } from './protected-request.ts';

interface DeviceHistoryTrendsProps {
  readonly site: Readonly<Site>;
  readonly row: RealAssetsDeviceRow;
  readonly principal: CurrentPrincipalResponse;
  readonly client: S2TelemetryClient;
  readonly protectedGeneration: number;
  readonly protectedRequestToken: () => ProtectedScopeRequestToken;
  readonly routePolicyRevision: string | null;
  readonly historyAllowed: boolean;
  readonly currentUnavailable: boolean;
  readonly sessionCapability: string;
}

interface HistorySelection {
  readonly deviceId: string;
  readonly protectedGeneration: number;
  readonly range: RealAssetsHistoryRange;
  readonly asOf: number;
}

function chartOption(
  definition: RealAssetsPointDefinition,
  series: DeviceHistorySeries,
  query: RealAssetsHistoryQuery,
  timezone: string,
) {
  const unit = historySeriesUnit(series.points, definition.defaultUnit);
  const data = buildRealAssetsTrendData(series.points, query.range, query.maxPointsPerKey);
  const description = `${definition.label}，${REAL_ASSETS_HISTORY_RANGES[query.range].label}，Site 时区 ${timezone}。断点表示缺失或 Point/Sensor 历史身份切换，零值表示真实零，可疑点保持可见。`;
  return {
    animation: false,
    aria: { enabled: true, description },
    grid: { left: 58, right: 20, top: 24, bottom: 50 },
    tooltip: {
      trigger: 'axis',
      renderMode: 'richText',
      formatter: (parameters: unknown) => {
        const values = Array.isArray(parameters) ? parameters : [parameters];
        const first = values[0] as { value?: [number, number | null] } | undefined;
        const timestamp = first?.value?.[0];
        const lines = [timestamp === undefined ? '时间不可用' : formatRealAssetsHistoryInstant(timestamp, timezone)];
        for (const parameter of values as Array<{ value?: [number, number | null]; data?: { quality?: string; pointId?: string | null; sensorId?: string | null } }>) {
          const value = parameter.value?.[1];
          const quality = parameter.data?.quality;
          const pointId = parameter.data?.pointId;
          const sensorId = parameter.data?.sensorId;
          lines.push(value === null || value === undefined
            ? `${definition.label}: 数据缺口或历史身份切换`
            : `${definition.label}: ${value} ${unit}${quality === 'SUSPECT' ? ' · 可疑' : ''}`);
          if (value !== null && value !== undefined && pointId) {
            lines.push(`Point ${pointId}${sensorId ? ` · Sensor ${sensorId}` : ' · 无独立 Sensor'}`);
          }
        }
        return lines.join('\n');
      },
    },
    xAxis: {
      type: 'time',
      name: timezone,
      nameLocation: 'middle',
      nameGap: 34,
      axisLabel: {
        hideOverlap: true,
        formatter: (value: number) => formatRealAssetsHistoryInstant(value, timezone),
      },
    },
    yAxis: { type: 'value', name: unit },
    series: [{
      name: definition.label,
      type: 'line',
      connectNulls: false,
      showSymbol: data.length < 90,
      symbolSize: 7,
      data: data.map((item) => item.value === null
        ? { value: [item.timestamp, null], quality: null, pointId: null, sensorId: null }
        : {
            value: [item.timestamp, item.value],
            quality: item.quality,
            pointId: item.pointId,
            sensorId: item.sensorId,
            symbol: item.quality === 'SUSPECT' ? 'diamond' : 'circle',
            symbolSize: item.quality === 'SUSPECT' ? 10 : 7,
          }),
      emphasis: { focus: 'series' },
    }],
  };
}

function SeriesPanel({
  definition,
  series,
  query,
  timezone,
  partial,
  truncated,
  datasetRevision,
  dataWatermark,
}: {
  readonly definition: RealAssetsPointDefinition;
  readonly series: DeviceHistorySeries;
  readonly query: RealAssetsHistoryQuery;
  readonly timezone: string;
  readonly partial: boolean;
  readonly truncated: boolean;
  readonly datasetRevision: string;
  readonly dataWatermark: string | null;
}) {
  const option = useMemo(() => chartOption(definition, series, query, timezone), [definition, query, series, timezone]);
  const unit = historySeriesUnit(series.points, definition.defaultUnit);
  const first = series.points.at(0);
  const last = series.points.at(-1);
  const suspectCount = series.points.filter((point) => point.quality === 'SUSPECT').length;
  const zeroCount = series.points.filter((point) => point.value === 0).length;
  let previousIdentity: string | null = null;
  let identitySegmentCount = 0;
  for (const point of series.points) {
    const identity = `${point.pointId}:${point.sensorId ?? 'no-sensor'}`;
    if (identity !== previousIdentity) identitySegmentCount += 1;
    previousIdentity = identity;
  }
  const latestIdentity = last ? `Point ${last.pointId}${last.sensorId ? ` · Sensor ${last.sensorId}` : ' · 无独立 Sensor'}` : '无历史身份';
  const businessState = series.points.length === 0 ? 'EMPTY' : truncated || partial ? 'PARTIAL' : suspectCount > 0 ? 'SUSPECT' : 'READY';

  return (
    <article className="real-assets-history__series" data-history-key={definition.key} data-business-state={businessState} data-unit={unit} data-history-identity-count={identitySegmentCount}>
      <header>
        <div><h4>{definition.label}</h4><code>{definition.key}</code></div>
        <span>{unit}</span>
      </header>
      <dl className="real-assets-history__series-facts">
        <div><dt>返回点数</dt><dd>{series.points.length}</dd></div>
        <div><dt>可疑点</dt><dd>{suspectCount}</dd></div>
        <div><dt>真实零值点</dt><dd>{zeroCount}{zeroCount > 0 ? ` · 0 ${unit}` : ''}</dd></div>
        <div><dt>历史身份段</dt><dd>{identitySegmentCount}</dd></div>
        <div><dt>最新历史身份</dt><dd><code>{latestIdentity}</code></dd></div>
        <div><dt>可用范围</dt><dd>{first && last ? `${formatRealAssetsHistoryInstant(first.sampledAt, timezone)} — ${formatRealAssetsHistoryInstant(last.sampledAt, timezone)}` : '无已接受历史点'}</dd></div>
        <div><dt>数据水位</dt><dd>{formatRealAssetsHistoryInstant(dataWatermark, timezone)}</dd></div>
      </dl>
      {truncated ? <p className="real-assets-history__notice" role="status">该点位达到返回上限，结果为部分历史。</p> : null}
      {series.points.length === 0 ? (
        <div className="real-assets-history__empty" role="status">当前范围没有已接受历史点。此状态不代表零，也不会回填当前 Snapshot。</div>
      ) : (
        <ReactECharts
          key={`${definition.key}:${datasetRevision}:${dataWatermark ?? 'none'}`}
          option={option}
          className="real-assets-history__chart"
          style={{ height: 230, width: '100%', minWidth: 0 }}
          opts={{ renderer: 'svg' }}
          notMerge
          lazyUpdate
        />
      )}
    </article>
  );
}

export function DeviceHistoryTrends({
  site,
  row,
  principal,
  client,
  protectedGeneration,
  protectedRequestToken,
  routePolicyRevision,
  historyAllowed,
  currentUnavailable,
  sessionCapability,
}: DeviceHistoryTrendsProps) {
  const queryClient = useQueryClient();
  const definitions = useMemo(() => listRealAssetsTrendDefinitions(row.profile), [row.profile]);
  const [selection, setSelection] = useState<HistorySelection>(() => ({
    deviceId: row.device.id,
    protectedGeneration,
    range: '1h',
    asOf: Date.now(),
  }));

  useEffect(() => {
    setSelection({ deviceId: row.device.id, protectedGeneration, range: '1h', asOf: Date.now() });
  }, [protectedGeneration, row.device.id]);

  const selectionCurrent = selection.deviceId === row.device.id && selection.protectedGeneration === protectedGeneration;
  const query = useMemo(() => {
    if (!selectionCurrent || definitions.length === 0) return null;
    return createRealAssetsHistoryQuery({
      protectedGeneration,
      sessionId: principal.session.id,
      actingOrganizationId: principal.context.actingOrganizationId,
      owningOrganizationId: row.device.owningOrganizationId,
      siteId: site.id,
      deviceId: row.device.id,
      keys: definitions.map((definition) => definition.key),
      range: selection.range,
      timezone: site.timezone,
      routePolicyRevision,
      asOf: selection.asOf,
    });
  }, [definitions, principal.context.actingOrganizationId, principal.session.id, protectedGeneration, routePolicyRevision, row.device.id, row.device.owningOrganizationId, selection.asOf, selection.range, selectionCurrent, site.id, site.timezone]);

  const result = useQuery({
    queryKey: query
      ? realAssetsHistoryQueryKey(query)
      : ['real-assets', protectedGeneration, principal.context.actingOrganizationId, site.id, 'history', 'disabled', row.device.id],
    queryFn: ({ signal }) => {
      if (!query) throw new Error('Device history query is not ready.');
      const scopeGuard = protectedRequestToken();
      if (scopeGuard.siteId !== site.id || scopeGuard.generation !== protectedGeneration) {
        throw new DOMException('Protected Site scope is not current.', 'AbortError');
      }
      return runRealAssetsProtectedRequest(scopeGuard, signal, (protectedSignal) => loadRealAssetsHistory({
        client,
        query,
        sessionCapability,
        signal: protectedSignal,
      }));
    },
    enabled: historyAllowed && query !== null,
    staleTime: 30_000,
    retry: (failureCount, error) => failureCount < 1 && classifyRealAssetsHistoryFailure(error).retryable,
  });

  useEffect(() => {
    if (!query || !result.data) return;
    queryClient.setQueryData(realAssetsHistoryRevisionKey(query, result.data), result.data);
  }, [query, queryClient, result.data]);

  if (row.profile.state === 'unconfigured') {
    return <div className="real-assets-history__notice" role="status">当前 Device 类型尚无版本化关键点位 profile，因此不会请求任意 telemetry key 历史。</div>;
  }
  if (definitions.length === 0) {
    return <div className="real-assets-history__notice" role="status">当前 profile 没有可用于运行判断的短趋势点位。</div>;
  }
  if (!historyAllowed) {
    return <div className="real-assets-history__notice real-assets-history__notice--warning" role="status">当前 Principal 没有 Telemetry history read 能力投影；页面不会尝试历史请求。权威当前状态不受影响。</div>;
  }

  const range = REAL_ASSETS_HISTORY_RANGES[selection.range];
  return (
    <div
      className="real-assets-history"
      data-testid="real-assets-device-history"
      data-history-state={result.isPending ? 'LOADING' : result.isError ? 'ERROR' : result.data?.metadata.partial ? 'PARTIAL' : result.data?.metadata.returnedPoints === 0 ? 'EMPTY' : 'READY'}
      data-history-range={selection.range}
      data-history-revision={result.data?.metadata.datasetRevision ?? 'unavailable'}
    >
      <form className="real-assets-history__controls" aria-label="设备关键点位短趋势范围" onSubmit={(event) => event.preventDefault()}>
        <fieldset>
          <legend>短趋势范围</legend>
          {(Object.entries(REAL_ASSETS_HISTORY_RANGES) as Array<[RealAssetsHistoryRange, (typeof REAL_ASSETS_HISTORY_RANGES)[RealAssetsHistoryRange]]>).map(([value, definition]) => (
            <label key={value}>
              <input
                type="radio"
                name={`real-assets-history-range-${row.device.id}`}
                value={value}
                data-testid={`real-assets-history-range-${value}`}
                checked={selection.range === value}
                onChange={() => setSelection({ deviceId: row.device.id, protectedGeneration, range: value, asOf: Date.now() })}
              />
              {definition.label}
            </label>
          ))}
        </fieldset>
        <button type="button" data-testid="real-assets-history-refresh" disabled={result.isFetching} onClick={() => setSelection((current) => ({ ...current, asOf: Date.now() }))}>
          {result.isFetching ? '刷新中…' : '刷新短趋势'}
        </button>
      </form>

      <p className="real-assets-history__scope">{range.label} · 固定 RAW 查询 · Site 时区 {site.timezone} · 只查询 profile 中 {definitions.length} 个 trendEligible 点位。</p>
      {currentUnavailable ? <p className="real-assets-history__notice" role="status">当前 Snapshot 不可用，但短历史仍独立查询；历史最后一点不会被标记为当前状态。</p> : null}
      {result.isPending ? <div className="real-assets-history__loading" role="status" aria-live="polite">正在读取 {definitions.map((definition) => definition.label).join('、')} 的有界短历史…</div> : null}
      {result.isError ? (() => {
        const failure = classifyRealAssetsHistoryFailure(result.error);
        return (
          <div className="real-assets-history__error" role="alert" data-history-error={failure.kind} data-retryable={String(failure.retryable)}>
            <strong>{failure.title}</strong><span>{failure.detail}</span>
            {failure.traceId ? <code>traceId {failure.traceId}</code> : null}
            {failure.retryable ? <button type="button" data-testid="real-assets-history-retry" onClick={() => { void result.refetch(); }}>仅重试短历史</button> : null}
          </div>
        );
      })() : null}
      {result.data && query ? (
        <>
          {result.data.metadata.partial ? <p className="real-assets-history__notice real-assets-history__notice--warning" role="status">历史结果为部分数据；缺失时间保持断点，不插值、不补零。</p> : null}
          <dl className="real-assets-history__metadata" aria-label="短历史数据范围与修订">
            <div><dt>请求范围</dt><dd>{formatRealAssetsHistoryInstant(query.from, site.timezone)} — {formatRealAssetsHistoryInstant(query.to, site.timezone)}</dd></div>
            <div><dt>数据水位</dt><dd>{formatRealAssetsHistoryInstant(result.data.metadata.dataWatermark, site.timezone)}</dd></div>
            <div><dt>数据集修订</dt><dd><code>{result.data.metadata.datasetRevision}</code></dd></div>
            <div><dt>返回点数</dt><dd>{result.data.metadata.returnedPoints}</dd></div>
          </dl>
          <div className="real-assets-history__series-grid">
            {definitions.map((definition, index) => (
              <SeriesPanel
                key={definition.key}
                definition={definition}
                series={result.data.series[index]}
                query={query}
                timezone={site.timezone}
                partial={result.data.metadata.partial}
                truncated={result.data.metadata.truncatedKeys.includes(definition.key)}
                datasetRevision={result.data.metadata.datasetRevision}
                dataWatermark={result.data.metadata.dataWatermark}
              />
            ))}
          </div>
        </>
      ) : null}
    </div>
  );
}
