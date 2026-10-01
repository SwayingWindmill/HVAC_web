import { AlertTriangle, Wifi, WifiOff } from 'lucide-react';
import type { AssetsAttentionReason, AssetsDeviceRow } from '@/features/assets/model';

export const ATTENTION_LABELS: Readonly<Record<AssetsAttentionReason, string>> = Object.freeze({
  CURRENT_STATE_UNAVAILABLE: '当前状态不可用',
  CURRENT_STATE_NOT_VISIBLE: '当前状态不可见',
  POINT_CATALOG_CONTRACT_DRIFT: '点位配置需核查',
  PRESENCE_OFFLINE: '通信离线',
  TELEMETRY_STALE: '数据延迟',
  TELEMETRY_QUALITY_DEGRADED: '数据质量需核查',
  TELEMETRY_MISSING: '数据缺失',
  TELEMETRY_DEGRADED: '数据状态降级',
  TELEMETRY_INCOMPLETE: '数据不完整',
});

export function deviceLocation(row: AssetsDeviceRow): string {
  return row.space.state === 'bound' ? row.space.space.displayName : '位置未登记';
}

export function connectedAsset(row: AssetsDeviceRow): string | null {
  if (row.binding.state === 'bound') return row.binding.asset.displayName;
  if (row.binding.state === 'multi-bound') return `${row.binding.bindings.length} 个关联资产`;
  return null;
}

export function connectionPresentation(row: AssetsDeviceRow) {
  const state = row.operational.connection.state;
  if (state === 'ONLINE') return { label: '在线', icon: Wifi, tone: 'text-success' };
  if (state === 'OFFLINE') return { label: '离线', icon: WifiOff, tone: 'text-destructive' };
  if (state === 'NOT_APPLICABLE') return { label: '不适用', icon: Wifi, tone: 'text-muted-foreground' };
  if (state === 'UNAVAILABLE') return { label: '不可用', icon: AlertTriangle, tone: 'text-warning' };
  return { label: '未知', icon: AlertTriangle, tone: 'text-warning' };
}

export function runningPresentation(row: AssetsDeviceRow) {
  const point = row.representativePoints.find((candidate) =>
    candidate.state === 'PRESENT'
    && ['RUNNING', 'STOPPED', 'STANDBY'].includes(candidate.displayValue),
  );
  if (point?.displayValue === 'RUNNING') return { label: '运行中', tone: 'text-success' };
  if (point?.displayValue === 'STOPPED') return { label: '已停止', tone: 'text-muted-foreground' };
  if (point?.displayValue === 'STANDBY') return { label: '待机', tone: 'text-information' };
  return { label: '未知', tone: 'text-muted-foreground' };
}

export function freshnessLabel(value: string): string {
  if (value === 'FRESH') return '新鲜';
  if (value === 'STALE') return '延迟';
  if (value === 'MISSING' || value === 'NO_DATA') return '无数据';
  if (value === 'NOT_APPLICABLE') return '不适用';
  return '不可用';
}

export function qualityLabel(value: string): string {
  if (value === 'GOOD') return '良好';
  if (value === 'DEGRADED') return '需核查';
  if (value === 'PARTIAL') return '部分有效';
  if (value === 'ESTIMATED') return '估算';
  if (value === 'MANUAL') return '人工';
  if (value === 'STALE') return '延迟';
  if (value === 'INVALID') return '无效';
  if (value === 'NO_DATA' || value === 'MISSING') return '无数据';
  if (value === 'NOT_APPLICABLE') return '不适用';
  return '不可用';
}

export function latestTimestamp(row: AssetsDeviceRow, timezone: string): string {
  const timestamps = row.representativePoints
    .flatMap((point) => point.sampledAt ? [Date.parse(point.sampledAt)] : [])
    .filter(Number.isFinite);
  if (timestamps.length === 0) return '暂无更新时间';
  return new Intl.DateTimeFormat('zh-CN', {
    timeZone: timezone,
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  }).format(new Date(Math.max(...timestamps)));
}

export function metricPoints(row: AssetsDeviceRow, limit = 2) {
  return row.representativePoints
    .filter((point) => !['RUNNING', 'STOPPED', 'STANDBY'].includes(point.displayValue))
    .slice(0, limit);
}
