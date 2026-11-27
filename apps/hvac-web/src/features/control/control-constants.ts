import type { AssetControlLockMode, SiteControlMode } from '../../api/generated/platformGateway.gen.ts';

export type CommandStatus =
  | 'SUBMITTED'
  | 'VALIDATING'
  | 'AWAITING_APPROVAL'
  | 'APPROVED'
  | 'QUEUED'
  | 'DISPATCHING'
  | 'SUCCEEDED'
  | 'FAILED'
  | 'REJECTED'
  | 'CANCELLED'
  | 'EXPIRED'
  | 'OUTCOME_UNKNOWN';

export const SETTLED: ReadonlySet<CommandStatus> = new Set([
  'SUCCEEDED',
  'FAILED',
  'REJECTED',
  'CANCELLED',
  'EXPIRED',
]);

/** Still before dispatch, so a person can withdraw it. */
export const WITHDRAWABLE: ReadonlySet<CommandStatus> = new Set(['AWAITING_APPROVAL', 'QUEUED']);

/** During a maintenance window from the operating schedule the gateway takes only these commands. */
export const MAINTENANCE_PERMITTED: ReadonlySet<string> = new Set([
  'STOP',
  'STOP_UNIT_SEQUENCE',
  'RESET_FAULT',
]);

export const MAINTENANCE_BLOCKED = '检修窗口内只能停机和复位';

export const MODE_PRESENTATION: Readonly<Record<SiteControlMode, {
  readonly label: string;
  readonly short: string;
  readonly hint: string;
  readonly glyph: string;
  readonly tone: 'success' | 'warning' | 'destructive';
}>> = {
  REMOTE_AUTO: { label: '远程自动控制', short: '远程自动', hint: '平台按审批规则下发指令', glyph: '●', tone: 'success' },
  MANUAL_OVERRIDE: { label: '人工接管', short: '人工接管', hint: '平台暂停下发，现场手动操作', glyph: '▲', tone: 'warning' },
  SAFE_SHUTDOWN_EXIT: { label: '安全退出', short: '安全退出', hint: '停掉一切远程控制，交回现场', glyph: '◆', tone: 'destructive' },
};

export const MODE_ORDER = ['REMOTE_AUTO', 'MANUAL_OVERRIDE', 'SAFE_SHUTDOWN_EXIT'] as const satisfies readonly SiteControlMode[];

export const LOCK_PRESENTATION: Readonly<Record<Exclude<AssetControlLockMode, 'UNLOCKED'>, {
  readonly label: string;
  readonly hint: string;
}>> = {
  MANUAL_LOCK: { label: '手动锁定', hint: '现场临时手动操作，平台不下发、不给停机建议' },
  MAINTENANCE_LOCK: { label: '检修锁定', hint: '设备在检修，长时间停用' },
};

export const REASON_LIMIT = 1000;

/** Plain actor name on control mode and asset lock records without the legacy generic fallback. */
export function controlActorLabel(
  actor: { readonly updatedBy?: string | null; readonly updatedByName?: string | null },
  myId?: string,
): string {
  const name = actor.updatedByName?.trim();
  if (name) return name;
  if (myId && actor.updatedBy === myId) return '我';
  return '系统';
}
