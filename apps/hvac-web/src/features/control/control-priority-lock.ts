import type { DevicePriorityLockMutation, DevicePriorityLockState, SiteControlState } from '../../api/generated/platformGateway.gen.ts';

export type DevicePriorityTier = DevicePriorityLockState['lockedPriority'];
export type MutableDevicePriorityTier = DevicePriorityLockMutation['lockedPriority'];

export const DEVICE_PRIORITY_TIER_LABEL: Readonly<Record<DevicePriorityTier, string>> = {
  SAFETY_INTERLOCK: '安全联锁',
  PROTECTION_RULE: '保护规则',
  MANUAL_OVERRIDE: '人工接管',
  AI_OPTIMIZATION: 'AI 优化',
  SCHEDULE: '日常日程',
};

export const MUTABLE_PRIORITY_TIERS: readonly {
  readonly value: MutableDevicePriorityTier;
  readonly label: string;
  readonly hint: string;
}[] = [
  {
    value: 'MANUAL_OVERRIDE',
    label: '人工接管',
    hint: '在锁定时段内拦截 AI 优化与定时日程指令，仅允许安全联锁或保护规则抢占。',
  },
  {
    value: 'PROTECTION_RULE',
    label: '保护规则',
    hint: '针对传感器越限或运行条件保护加锁，低于此等级的操控和调优指令一律拒收。',
  },
  {
    value: 'SAFETY_INTERLOCK',
    label: '安全联锁',
    hint: '最高运行优先等级，在解锁或到期前拦截一切自动及常规手动指令。',
  },
];

export const CONTROL_ALARM_EVENT_LABEL: Readonly<Record<string, string>> = {
  OUTCOME_UNKNOWN: '执行结果未知',
  PULSE_DELIVERED_POWER_UNCONFIRMED: '脉冲发出后功率未确认',
  EMERGENCY_DO_RELEASE: '紧急输出释放',
  VERIFY_TIMEOUT: '读回校验超时',
  DISPATCH_REJECTED: '下发前安全拒收',
  CONTROL_EXIT: '控制模式切换',
  SAFETY_SHUTDOWN: '安全停机',
  ASSET_LOCK: '设备锁定',
  PRIORITY_LOCK: '优先级锁定',
  DEVICE_PRIORITY_LOCK: '设备优先级锁定',
};

const CONTROL_ALARM_REASON_LABEL: Readonly<Record<string, string>> = {
  CONTROL_ALARM_HTTP_FAILED: '下游告警服务通信失败',
  CONTROL_ALARM_HTTP_REJECTED: '下游告警服务拒绝接收',
  CONTROL_ALARM_PAYLOAD_INVALID: '控制告警内容编码无效',
  CONTROL_ALARM_REQUEST_INVALID: '控制告警请求构造失败',
  CONTROL_ALARM_SENDER_PANIC: '控制告警发送异常中断',
  CONTROL_ALARM_PUBLISH_FAILED: '控制告警写入存储失败',
  CONTROL_ALARM_TARGET_UNAVAILABLE: '控制告警目标地址未配置',
  CONTROL_ALARM_DELIVERY_TIMEOUT: '下游告警服务超时未响应',
  CONTROL_ALARM_DELIVERY_CANCELED: '控制告警投递已取消',
  CONTROL_ALARM_LEASE_EXHAUSTED: '控制告警重试次数已耗尽',
  CONTROL_ALARM_DELIVERY_FAILED: '控制事件同步失败',
  VERIFY_TIMEOUT: '设备状态读回超时未确认',
  DISPATCH_REJECTED: '安全校验未通过导致下发拒收',
  TIMEOUT: '下游告警服务超时未响应',
  UNAVAILABLE: '告警服务暂时不可达',
  TRANSPORT: '内网通信异常',
  CONNECTION_REFUSED: '告警服务连接被拒绝',
  DEPENDENCY: '告警服务依赖失败',
  AUTHORIZATION: '内部授权凭证被拒',
  RATE_LIMITED: '触发通知速率限制',
  CAPACITY: '告警投递队列容量不足',
  SCOPE: '站点或设备范围校验未通过',
  DELIVERY_FAILED: '控制事件同步失败',
  VALIDATION_FAILED: '控制告警内容校验未通过',
};

/** Only non-expired device priority locks restrict lower-priority commands. */
export function activePriorityLocks(
  state: Pick<SiteControlState, 'devicePriorityLocks'> | undefined,
  nowMs: number,
): ReadonlyMap<string, DevicePriorityLockState> {
  const entries = (state?.devicePriorityLocks ?? [])
    .filter((lock) => {
      const expiresAt = Date.parse(lock.lockedUntil);
      return Number.isFinite(expiresAt) && expiresAt > nowMs;
    })
    .map((lock) => [lock.deviceId, lock] as const);
  return new Map(entries);
}

/** Remaining lock time in plain Chinese, never negative. */
export function formatLockRemaining(lockedUntil: string, nowMs: number): string {
  const remainingMs = Date.parse(lockedUntil) - nowMs;
  if (!Number.isFinite(remainingMs) || remainingMs <= 0) return '即将到期';
  const totalMinutes = Math.ceil(remainingMs / 60_000);
  if (totalMinutes <= 1) return '剩余不足 1 分钟';
  if (totalMinutes < 60) return `剩余 ${totalMinutes} 分钟`;
  const hours = Math.floor(totalMinutes / 60);
  const minutes = totalMinutes % 60;
  return minutes === 0 ? `剩余 ${hours} 小时` : `剩余 ${hours} 小时 ${minutes} 分钟`;
}

export function describeControlAlarmEvent(eventKind: string): string {
  return CONTROL_ALARM_EVENT_LABEL[eventKind] ?? '控制事件同步';
}

export function describeControlAlarmError(code: string | undefined): string {
  if (!code) return '下游确认异常';
  const upper = code.toUpperCase();
  if (upper in CONTROL_ALARM_REASON_LABEL) return CONTROL_ALARM_REASON_LABEL[upper];
  for (const [key, label] of Object.entries(CONTROL_ALARM_REASON_LABEL)) {
    if (upper.includes(key)) return label;
  }
  return '告警同步受阻，请重试或检查服务状态';
}
