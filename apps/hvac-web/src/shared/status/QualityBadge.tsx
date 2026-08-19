import { Tag } from 'antd';

export type TelemetryQuality = 'GOOD' | 'PARTIAL' | 'ESTIMATED' | 'MANUAL' | 'STALE' | 'INVALID';

const QUALITY_META: Record<TelemetryQuality, { label: string; color: string }> = {
  GOOD: { label: 'GOOD · 良好', color: 'green' },
  PARTIAL: { label: 'PARTIAL · 部分', color: 'gold' },
  ESTIMATED: { label: 'ESTIMATED · 估算', color: 'blue' },
  MANUAL: { label: 'MANUAL · 手工', color: 'purple' },
  STALE: { label: 'STALE · 陈旧', color: 'orange' },
  INVALID: { label: 'INVALID · 无效', color: 'red' },
};

export function QualityBadge({
  quality,
  showLabel = true,
}: {
  quality: TelemetryQuality | null | undefined;
  showLabel?: boolean;
}) {
  if (!quality) return <Tag>QUALITY · 未提供</Tag>;
  const meta = QUALITY_META[quality];
  return <Tag color={meta.color}>{showLabel ? meta.label : quality}</Tag>;
}
