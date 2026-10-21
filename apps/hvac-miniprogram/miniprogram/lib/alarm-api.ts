// The Alarm owner behind the Gateway, as the web calls it (apps/hvac-web/src/features/alarms/alarm-api.ts).
// Pages pass the platform Gateway.

import type { Alarm } from './alarms';
import type { Gateway } from './gateway';

interface AlarmPage {
  readonly data: Alarm[];
  readonly meta: { readonly nextCursor: string | null; readonly hasMore: boolean };
}

const alarmPage = (gateway: Gateway, siteId: string, condition: Alarm['condition'], cursor: string | null) =>
  gateway.get<AlarmPage>(`/api/v1/alarms?siteId=${encodeURIComponent(siteId)}&limit=100&condition=${condition}${cursor === null ? '' : `&cursor=${encodeURIComponent(cursor)}`}`);

// As the web reads them: every active alarm up to ten pages; `complete` says whether that covered them all.
const PAGE_LIMIT = 10;

export async function activeAlarms(gateway: Gateway, siteId: string): Promise<{ alarms: Alarm[]; complete: boolean }> {
  const alarms: Alarm[] = [];
  let cursor: string | null = null;
  for (let page = 0; page < PAGE_LIMIT; page += 1) {
    const result: AlarmPage = await alarmPage(gateway, siteId, 'ACTIVE', cursor);
    alarms.push(...result.data);
    if (!result.meta.hasMore) return { alarms, complete: true };
    cursor = result.meta.nextCursor;
  }
  return { alarms, complete: false };
}

/** The latest hundred cleared alarms; `complete` is false when there are more. */
export async function clearedAlarms(gateway: Gateway, siteId: string): Promise<{ alarms: Alarm[]; complete: boolean }> {
  const result = await alarmPage(gateway, siteId, 'CLEARED', null);
  return { alarms: result.data, complete: !result.meta.hasMore };
}

export const getAlarm = (gateway: Gateway, alarmId: string) =>
  gateway.get<{ data: Alarm }>(`/api/v1/alarms/${encodeURIComponent(alarmId)}`).then((response) => response.data);

/** Acknowledges an alarm with an optional note; the key makes a repeated tap one acknowledgement. */
export const acknowledgeAlarm = (gateway: Gateway, alarmId: string, comment: string, idempotencyKey: string) =>
  gateway.post<{ data: Alarm }>(
    `/api/v1/alarms/${encodeURIComponent(alarmId)}/ack`,
    comment.trim() === '' ? {} : { comment: comment.trim() },
    { 'Idempotency-Key': idempotencyKey },
  ).then((response) => response.data);
