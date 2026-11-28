export interface WallTime {
  readonly year: number;
  readonly month: number;
  readonly day: number;
  readonly hour: number;
  readonly minute: number;
}

const wallTimeFormatters = new Map<string, Intl.DateTimeFormat>();

function wallTimeFormatter(timeZone: string): Intl.DateTimeFormat {
  let formatter = wallTimeFormatters.get(timeZone);
  if (!formatter) {
    formatter = new Intl.DateTimeFormat('en-US', {
      timeZone,
      year: 'numeric',
      month: 'numeric',
      day: 'numeric',
      hour: 'numeric',
      minute: 'numeric',
      hourCycle: 'h23',
    });
    wallTimeFormatters.set(timeZone, formatter);
  }
  return formatter;
}

export function wallTime(instant: Date, timeZone: string): WallTime {
  let year = 0;
  let month = 0;
  let day = 0;
  let hour = 0;
  let minute = 0;
  for (const part of wallTimeFormatter(timeZone).formatToParts(instant)) {
    if (part.type === 'year') year = Number(part.value);
    else if (part.type === 'month') month = Number(part.value);
    else if (part.type === 'day') day = Number(part.value);
    else if (part.type === 'hour') hour = Number(part.value);
    else if (part.type === 'minute') minute = Number(part.value);
  }
  return { year, month, day, hour, minute };
}

/** The instant at which the Site's wall clock shows the given local midnight. */
export function siteMidnight(year: number, month: number, day: number, timeZone: string): Date {
  const guess = Date.UTC(year, month - 1, day);
  const shown = wallTime(new Date(guess), timeZone);
  const offset = Date.UTC(shown.year, shown.month - 1, shown.day, shown.hour, shown.minute) - guess;
  return new Date(guess - offset);
}

/** Snap `now` to 30s boundaries while staying past Site midnight so `to > from` always holds. */
export function energyQueryNow(timeZone: string, nowMs = Date.now(), bucketMs = 30_000): Date {
  const snapped = Math.floor(nowMs / bucketMs) * bucketMs;
  const { year, month, day } = wallTime(new Date(nowMs), timeZone);
  const midnight = siteMidnight(year, month, day, timeZone).getTime();
  return new Date(snapped > midnight ? snapped : Math.max(nowMs, midnight + 1000));
}
