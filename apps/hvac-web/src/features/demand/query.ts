import { queryOptions, skipToken } from '@tanstack/react-query';
import type { SiteAssetModel } from '@/api/generated/platformGateway.gen';
import { createS2TelemetryClient } from '@/api/generated/s2Telemetry.gen';
import { demandWindows, quarterHours, type QuarterHour } from './demand-model';

const client = createS2TelemetryClient();

/** The plant's total active power point: the HVAC meter's reading demand is measured on. */
export function plantPowerPoint(model: Pick<SiteAssetModel, 'telemetryPoints'>): { deviceId: string; pointCode: string } | null {
  const point = model.telemetryPoints.find((candidate) => candidate.sourceKey === 'hvac_meter.active_power' && candidate.status === 'ACTIVE');
  return point ? { deviceId: point.reportingDeviceId, pointCode: point.pointCode } : null;
}

/** Quarter-hour demand from the earlier of the month's and yesterday's start until now. */
export function plantDemandQuery(input: {
  readonly siteId: string;
  readonly timezone: string;
  readonly csrfToken: string;
  /** Null until the Registry names the meter; nothing is read before then. */
  readonly point: { deviceId: string; pointCode: string } | null;
  readonly now: Date;
}) {
  const { point } = input;
  return queryOptions({
    queryKey: ['demand', input.siteId, point?.deviceId, point?.pointCode, input.now.getTime()],
    queryFn: point === null ? skipToken : async ({ signal }): Promise<QuarterHour[]> => {
      const responses = await Promise.all(demandWindows(input.now, input.timezone).map((window) =>
        client.queryDeviceHistoryAggregate({
          deviceId: point.deviceId,
          keys: [point.pointCode],
          from: window.from.toISOString(),
          to: window.to.toISOString(),
          granularity: 'QUARTER_HOUR',
          timezone: input.timezone,
          qualityPolicy: 'USABLE',
        }, { csrfToken: input.csrfToken, signal })));
      return quarterHours(responses.flatMap((response) => response.buckets), input.now);
    },
    staleTime: 60_000,
    placeholderData: (previous) => previous,
  });
}
