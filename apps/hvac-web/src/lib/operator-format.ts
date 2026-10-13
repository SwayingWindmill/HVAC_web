// Formatting shared by operator workspaces.

/** A measured value with a fixed number of decimals, grouped the Chinese way. */
export function formatDecimal(value: number, digits: number): string {
  return value.toLocaleString('zh-CN', { minimumFractionDigits: digits, maximumFractionDigits: digits });
}

/** Platform principals are shown relative to the viewer until IAM offers a user directory. */
export function personLabel(principalId: string | undefined, myId: string): string {
  if (!principalId) return '—';
  return principalId === myId ? '我' : '其他人员';
}

export function formatTime(value: string | undefined, timezone: string): string {
  if (!value) return '—';
  return new Intl.DateTimeFormat('zh-CN', {
    timeZone: timezone,
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  }).format(new Date(value));
}

/** A time as an operator scans a list: 09:42 today, 10/12 22:15 on an earlier day. */
export function formatCompactTime(value: string | undefined, timezone: string, now: number = Date.now()): string {
  if (!value) return '—';
  const day = new Intl.DateTimeFormat('zh-CN', { timeZone: timezone, year: 'numeric', month: '2-digit', day: '2-digit' });
  const clock = new Intl.DateTimeFormat('zh-CN', { timeZone: timezone, hour: '2-digit', minute: '2-digit', hour12: false }).format(new Date(value));
  if (day.format(new Date(value)) === day.format(new Date(now))) return clock;
  const date = new Intl.DateTimeFormat('zh-CN', { timeZone: timezone, month: '2-digit', day: '2-digit' }).format(new Date(value));
  return `${date} ${clock}`;
}

/** How long ago an instant was, in the words an operator scans: 刚刚, 12 分钟前, 3 小时前, 2 天前. */
export function formatRelative(value: string, now: number): string {
  const minutes = Math.floor((now - Date.parse(value)) / 60_000);
  if (minutes < 1) return '刚刚';
  if (minutes < 60) return `${minutes} 分钟前`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours} 小时前`;
  return `${Math.floor(hours / 24)} 天前`;
}
