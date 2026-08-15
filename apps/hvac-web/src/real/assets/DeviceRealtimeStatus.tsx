import type { Site } from '../../api/generated/platformGateway.gen.ts';
import {
  describeRealAssetsRealtimeState,
  type RealAssetsRealtimeProjection,
} from './realtime.ts';
import type { RealAssetsRealtimeResult } from './useDeviceRealtime.ts';

function formatInstant(value: string, timeZone: string): string {
  const parsed = new Date(value);
  if (Number.isNaN(parsed.getTime())) return '不可用';
  return new Intl.DateTimeFormat('zh-CN', {
    timeZone,
    year: 'numeric', month: '2-digit', day: '2-digit',
    hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false,
  }).format(parsed);
}

export function DeviceRealtimeStatus({
  realtime,
  projection,
  site,
}: {
  realtime: RealAssetsRealtimeResult;
  projection: RealAssetsRealtimeProjection | null;
  site: Readonly<Site>;
}) {
  const state = realtime.state;
  const status = state?.status ?? realtime.phase;
  const retryable = realtime.phase === 'error'
    || state?.status === 'snapshot'
    || (state?.status === 'unavailable' && state.retryable);
  let label = '实时详情未打开';
  let detail = '只有当前可见 Device 详情会建立精确订阅；列表不会订阅全部业务点位。';
  let degraded = false;
  if (realtime.phase === 'not-authorized') {
    label = '实时订阅未授权';
    detail = '当前 Principal 缺少 telemetry.subscribe；权威 Current Snapshot 仍可独立展示。';
    degraded = true;
  } else if (realtime.phase === 'not-configured') {
    label = '实时点位目录未配置';
    detail = '此 Device type 没有版本化关键点位 profile，因此不会猜测 keys 或建立宽泛订阅。';
    degraded = true;
  } else if (realtime.phase === 'opening') {
    label = '正在建立精确实时订阅';
    detail = '先完成同一 Device 与 exact keys 的权威 Snapshot bootstrap，再接受连续 delta。';
  } else if (realtime.phase === 'purged') {
    label = '实时受保护状态已清除';
    detail = 'Site、Session、Principal 或 policy scope 已变化；旧订阅和晚到事件不会继续写入。';
    degraded = true;
  } else if (realtime.phase === 'error') {
    label = '实时连接暂不可用';
    detail = realtime.error?.message ?? '精确实时订阅无法建立；不会回退到 Legacy、Provider 直读或 Socket.IO。';
    degraded = true;
  } else if (state) {
    const description = describeRealAssetsRealtimeState(state);
    label = description.label;
    detail = description.detail;
    degraded = description.degraded;
  }
  return (
    <section
      className={`real-assets-realtime${degraded ? ' real-assets-realtime--degraded' : ''}`}
      aria-labelledby="real-assets-detail-realtime"
      data-testid="real-assets-device-realtime"
      data-realtime-state={status}
      data-realtime-source={projection?.source ?? 'none'}
      data-realtime-revision={String(projection?.realtimeRevision ?? '')}
      data-baseline-revision={String(projection?.baselineRevision ?? '')}
    >
      <header>
        <div>
          <h3 id="real-assets-detail-realtime">实时传输</h3>
          <strong>{label}</strong>
        </div>
        {retryable ? <button type="button" data-testid="real-assets-realtime-refresh" onClick={realtime.refresh}>重新读取实时基线</button> : null}
      </header>
      <p>{detail}</p>
      {state?.snapshot ? (
        <small>最后权威状态：revision {state.snapshot.businessRevision} · {formatInstant(state.snapshot.evaluatedAt, site.timezone)}</small>
      ) : null}
      {projection?.realtimeOlderThanBaseline ? (
        <div className="real-assets-detail__notice real-assets-detail__notice--warning" role="status">
          实时基线 revision {projection.realtimeRevision} 落后于已显示的 Current Snapshot revision {projection.baselineRevision}；旧 Snapshot 不会覆盖更新状态。
        </div>
      ) : null}
    </section>
  );
}
