// Formatting shared by operator workspaces.

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
