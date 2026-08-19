import { useEffect, useMemo, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import type { CurrentPrincipalResponse, Site } from '../../api/generated/platformGateway.gen.ts';
import type { DeviceHistoryObservation, S2TelemetryClient } from '../../api/generated/s2Telemetry.gen.ts';
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
  numericHistoryObservations,
  realAssetsHistoryQueryKey,
  realAssetsHistoryRevisionKey,
  type RealAssetsHistoryQuery,
  type RealAssetsHistoryRange,
} from './history.ts';
import type { RealAssetsDeviceRow } from './model.ts';
import { runRealAssetsProtectedRequest } from './protected-request.ts';
import { TimeSeriesChart } from '@/shared/charts/TimeSeriesChart';

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
  observations: readonly DeviceHistoryObservation[],
  query: RealAssetsHistoryQuery,
  timezone: string,
) {
  const unit = historySeriesUnit(observations, definition.defaultUnit);
  const data = buildRealAssetsTrendData(observations, query.range, query.pageSize);
  const description = `${definition.label}，${REAL_ASSETS_HISTORY_RANGES[query.range].label}，Site 时区 ${timezone}。断点表示缺失或 Point/Sensor 历史身份切换，零值表示真实零，非 GOOD 质量点保持可见。`;
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
            : `${definition.label}: ${value} ${unit}${quality && quality !== 'GOOD' ? ` · ${quality}` : ''}`);
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
            symbol: item.quality && item.quality !== 'GOOD' ? 'diamond' : 'circle',
            symbolSize: item.quality && item.quality !== 'GOOD' ? 10 : 7,
          }),
      emphasis: { focus: 'series' },
    }],
  };
}

function SeriesPanel({
  definition,
  observations,
  query,
  timezone,
  partial,
  projectionWatermark,
}: {
  readonly definition: RealAssetsPointDefinition;
  readonly observations: readonly DeviceHistoryObservation[];
  readonly query: RealAssetsHistoryQuery;
  readonly timezone: string;
  readonly partial: boolean;
  readonly projectionWatermark: string | null;
}) {
  const option = useMemo(() => chartOption(definition, observations, query, timezone), [definition, observations, query, timezone]);
  const unit = historySeriesUnit(observations, definition.defaultUnit);
  const first = observations.at(0);
  const last = observations.at(-1);
  const degradedQualityCount = observations.filter((observation) => observation.quality !== 'GOOD').length;
  const zeroCount = observations.filter((observation) => observation.valueType === 'NUMBER' && observation.value === 0).length;
  let previousIdentity: string | null = null;
  let identitySegmentCount = 0;
  for (const observation of observations) {
    const identity = `${observation.pointId}:${observation.sensorId ?? 'no-sensor'}:${observation.pointRevision}`;
    if (identity !== previousIdentity) identitySegmentCount += 1;
    previousIdentity = identity;
  }
  const latestIdentity = last ? `Point ${last.pointId} · rev ${last.pointRevision}${last.sensorId ? ` · Sensor ${last.sensorId}` : ' · 无独立 Sensor'}` : '无历史身份';
  const businessState = observations.length === 0 ? 'EMPTY' : partial ? 'PARTIAL' : degradedQualityCount > 0 ? 'QUALITY_DEGRADED' : 'READY';

  return (
    <article className="real-assets-history__series" data-history-key={definition.key} data-business-state={businessState} data-unit={unit} data-history-identity-count={identitySegmentCount}>
      <header>
        <div><h4>{definition.label}</h4><code>{definition.key}</code></div>
        <span>{unit}</span>
      </header>
      <dl className="real-assets-history__series-facts">
        <div><dt>返回点数</dt><dd>{observations.length}</dd></div>
        <div><dt>非 GOOD 质量点</dt><dd>{degradedQualityCount}</dd></div>
        <div><dt>真实零值点</dt><dd>{zeroCount}{zeroCount > 0 ? ` · 0 ${unit}` : ''}</dd></div>
        <div><dt>历史身份段</dt><dd>{identitySegmentCount}</dd></div>
        <div><dt>最新历史身份</dt><dd><code>{latestIdentity}</code></dd></div>
        <div><dt>可用范围</dt><dd>{first && last ? `${formatRealAssetsHistoryInstant(first.sampledAt, timezone)} — ${formatRealAssetsHistoryInstant(last.sampledAt, timezone)}` : '无数值历史点'}</dd></div>
        <div><dt>投影水位</dt><dd>{formatRealAssetsHistoryInstant(projectionWatermark, timezone)}</dd></div>
      </dl>
      {partial ? <p className="real-assets-history__notice" role="status">该页存在 nextCursor；图表仅显示当前固定快照页，不把截断结果冒充完整历史。</p> : null}
      {observations.length === 0 ? (
        <div className="real-assets-history__empty" role="status">当前范围没有 NUMBER 类型历史点。STRING/BOOLEAN/JSON 历史仍保留在统一 History API 中。</div>
      ) : (
        <TimeSeriesChart
          key={`${definition.key}:${projectionWatermark ?? 'none'}:${observations.at(-1)?.observationId ?? 'empty'}`}
          option={option}
          className="real-assets-history__chart"
          style={{ height: 230, width: '100%', minWidth: 0 }}
          opts={{ renderer: 'svg' }}
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
      tenantId: row.device.tenantId,
      siteId: site.id,
      deviceId: row.device.id,
      keys: definitions.map((definition) => definition.key),
      range: selection.range,
      timezone: site.timezone,
      routePolicyRevision,
      asOf: selection.asOf,
    });
  }, [definitions, principal.session.id, protectedGeneration, routePolicyRevision, row.device.id, row.device.tenantId, selection.asOf, selection.range, selectionCurrent, site.id, site.timezone]);

  const result = useQuery({
    queryKey: query
      ? realAssetsHistoryQueryKey(query)
      : ['real-assets', protectedGeneration, row.device.tenantId, site.id, 'history', 'disabled', row.device.id],
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
      data-history-state={result.isPending ? 'LOADING' : result.isError ? 'ERROR' : result.data?.metadata.nextCursor ? 'PARTIAL' : result.data?.metadata.returnedObservations === 0 ? 'EMPTY' : 'READY'}
      data-history-range={selection.range}
      data-history-revision={result.data?.metadata.projectionWatermark ?? 'unavailable'}
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
          {result.data.metadata.nextCursor ? <p className="real-assets-history__notice real-assets-history__notice--warning" role="status">当前固定快照还有后续页；图表不会把本页冒充完整历史。</p> : null}
          <dl className="real-assets-history__metadata" aria-label="短历史数据范围与投影水位">
            <div><dt>请求范围</dt><dd>{formatRealAssetsHistoryInstant(query.from, site.timezone)} — {formatRealAssetsHistoryInstant(query.to, site.timezone)}</dd></div>
            <div><dt>投影水位</dt><dd>{formatRealAssetsHistoryInstant(result.data.metadata.projectionWatermark, site.timezone)}</dd></div>
            <div><dt>返回 Observation</dt><dd>{result.data.metadata.returnedObservations}</dd></div>
            <div><dt>分页状态</dt><dd>{result.data.metadata.nextCursor ? '还有后续页' : '当前快照已完整返回'}</dd></div>
          </dl>
          <div className="real-assets-history__series-grid">
            {definitions.map((definition) => (
              <SeriesPanel
                key={definition.key}
                definition={definition}
                observations={numericHistoryObservations(result.data, definition.key)}
                query={query}
                timezone={site.timezone}
                partial={result.data.metadata.nextCursor !== null}
                projectionWatermark={result.data.metadata.projectionWatermark}
              />
            ))}
          </div>
        </>
      ) : null}
    </div>
  );
}
