import type { SiteDataGap } from '../../api/generated/platformGateway.gen.ts';

export type DataGapBannerContext = 'overview' | 'general';

export interface DataGapSummary {
  readonly message: string;
}

export function formatDataGapSince(iso: string, timeZone: string): string {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) {
    return iso;
  }
  const parts = Object.fromEntries(
    new Intl.DateTimeFormat('en-US', {
      timeZone,
      month: 'numeric',
      day: 'numeric',
      hour: '2-digit',
      minute: '2-digit',
      hourCycle: 'h23',
    })
      .formatToParts(date)
      .map((part) => [part.type, part.value]),
  );
  return `${parts.month}/${parts.day} ${parts.hour}:${parts.minute}`;
}

export function summarizeDataGaps(
  items: readonly SiteDataGap[],
  timeZone: string,
  context: DataGapBannerContext = 'general',
): DataGapSummary | null {
  if (items.length === 0) {
    return null;
  }

  const groups: {
    deviceId: string;
    deviceName: string;
    points: SiteDataGap[];
  }[] = [];
  const byDevice = new Map<string, (typeof groups)[number]>();

  for (const item of items) {
    let group = byDevice.get(item.deviceId);
    if (!group) {
      group = {
        deviceId: item.deviceId,
        deviceName: item.deviceName,
        points: [],
      };
      byDevice.set(item.deviceId, group);
      groups.push(group);
    }
    group.points.push(item);
  }

  const clauses = groups.slice(0, 2).map((group) => {
    const earliest = group.points.reduce((best, candidate) => {
      const bestMs = Date.parse(best.since);
      const candidateMs = Date.parse(candidate.since);
      if (Number.isNaN(bestMs)) return candidate;
      if (Number.isNaN(candidateMs)) return best;
      return candidateMs < bestMs ? candidate : best;
    }, group.points[0]);
    const sinceText = formatDataGapSince(earliest.since, timeZone);
    const excludesFromTotal = group.points.some((point) => point.excludesDeviceFromTotal);

    if (excludesFromTotal) {
      const totalLabel = context === 'overview' ? '今日空调用电' : '空调用电';
      return `${group.deviceName} ${sinceText} 起没有读数，${totalLabel}不含这台`;
    }

    const figures: string[] = [];
    for (const point of group.points) {
      for (const figure of point.affectedFigures) {
        if (!figures.includes(figure)) {
          figures.push(figure);
        }
      }
    }
    const impactSuffix = figures.length > 0 ? `，影响${figures.slice(0, 2).join('、')}` : '';
    if (group.points.length === 1) {
      return `${group.deviceName}（${group.points[0].pointName}）${sinceText} 起没有读数${impactSuffix}`;
    }
    return `${group.deviceName} ${sinceText} 起没有读数${impactSuffix}`;
  });

  const restCount = groups.length - clauses.length;
  const message =
    restCount > 0
      ? `${clauses.join('；')}（另有 ${restCount} 台设备读数中断）`
      : clauses.join('；');

  return { message };
}
