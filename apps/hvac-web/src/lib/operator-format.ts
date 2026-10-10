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

/** How long ago an instant was, in the words an operator scans: 刚刚, 12 分钟前, 3 小时前, 2 天前. */
export function formatRelative(value: string, now: number): string {
  const minutes = Math.floor((now - Date.parse(value)) / 60_000);
  if (minutes < 1) return '刚刚';
  if (minutes < 60) return `${minutes} 分钟前`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours} 小时前`;
  return `${Math.floor(hours / 24)} 天前`;
}
