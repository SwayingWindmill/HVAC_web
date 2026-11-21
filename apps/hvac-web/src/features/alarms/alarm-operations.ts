import type { AlarmRule, SiteAlarmRoutingSummary } from '../../api/generated/platformGateway.gen.ts';

export type AlarmRuleDependencyGate = NonNullable<AlarmRule['dependencyGate']>;
export type AlarmRuleAction = NonNullable<AlarmRule['actions']>[number];
export type AlarmRuleActionKind = AlarmRuleAction['kind'];
export type AlarmRuleActionTrigger = NonNullable<AlarmRuleAction['triggerOn']>;

const DEPENDENCY_GATE_LABEL: Readonly<Record<AlarmRuleDependencyGate, string>> = {
  NONE: '无前置条件',
  ALL_ACTIVE: '需全部上游告警同时触发',
  ANY_ACTIVE: '任一上游告警触发即可',
};

const ACTION_KIND_LABEL: Readonly<Record<AlarmRuleActionKind, string>> = {
  WEBHOOK: '回调外部接口',
  WORK_ORDER: '创建运维工单',
  PROTECTION_COMMAND: '下发保护指令',
};

const ACTION_TRIGGER_LABEL: Readonly<Record<AlarmRuleActionTrigger, string>> = {
  RAISE: '触发时',
  CLEAR: '恢复时',
  BOTH: '触发与恢复时',
};

const ROUTING_CHANNEL_LABEL: Readonly<Record<string, string>> = {
  smtp: '邮件',
  wecom_app: '企业微信应用',
  wecom_bot: '企业微信群机器人',
};

const GROUP_BY_LABEL: Readonly<Record<string, string>> = {
  severity: '按严重级别合并',
  alarmType: '按告警类型合并',
  rule: '按规则合并',
  device: '按设备合并',
  site: '按站点合并',
};

const DEAD_LETTER_ERROR_LABEL: Readonly<Record<string, string>> = {
  HTTP_5XX: '外部接收接口服务异常',
  HTTP_4XX: '外部接收接口拒绝请求',
  HTTP_408: '外部接口请求超时',
  HTTP_401: '外部接口签名或认证未通过',
  HTTP_403: '外部接口拒绝访问权限',
  HTTP_429: '外部接口触发限流',
  TIMEOUT: '网络请求超时',
  NETWORK_ERROR: '网络请求超时或传输中断',
  REDIRECT_BLOCKED: '外部回调禁止跳转重定向',
  SSRF_BLOCKED: '回调地址违反内网安全限制',
  SECRET_UNAVAILABLE: '回调签名密钥不可用或未配置',
  INVALID_WEBHOOK_URL: '回调地址格式无效',
  INVALID_PAYLOAD: '动作参数配置不合法',
  HANDLER_UNCONFIGURED: '动作执行处理器未启用',
  WORK_ORDER_ERROR: '自动创建运维工单失败',
  COMMAND_ERROR: '下发设备保护指令被拒',
  PERMANENT_FAILURE: '动作配置校验未通过',
  AUTHORIZATION: '目标权限校验失败',
  RATE_LIMITED: '触发限流保护',
  TRANSPORT: '网络请求超时或传输中断',
  DEPENDENCY: '关联服务不可用',
  SCOPE: '站点或设备作用域不匹配',
  CAPACITY: '执行队列容量受限',
  WEBHOOK_NON_2XX: '外部接口返回异常状态码',
  UNKNOWN: '未知执行错误',
};

const RELOAD_ERROR_LABEL: Readonly<Record<string, string>> = {
  RECIPIENTS_READ_FAILED: '读取接收人名录文件失败，已保留上一版有效配置',
  RECIPIENTS_INVALID: '接收人名录格式校验未通过，已保留上一版有效配置',
  ROUTING_READ_FAILED: '读取站点通知路由文件失败，已保留上一版有效配置',
  ROUTING_INVALID: '通知路由校验未通过，已保留上一版有效配置',
  ROUTING_RECIPIENT_MISMATCH: '路由引用了未登记的接收人，已保留上一版有效配置',
  INVALID_JSON: '配置文件 JSON 格式有误，已保留上一版有效配置',
  INVALID_ROUTING: '通知路由校验未通过，已保留上一版有效配置',
  MISSING_RECIPIENT: '路由引用了未登记的接收人，已保留上一版有效配置',
  READ_FAILED: '读取本地路由或接收人文件失败，已保留上一版有效配置',
};

const ROUTING_ROUTE_NAME_LABEL: Readonly<Record<string, string>> = {
  critical: '紧急直通通道',
  major: '重要分级通道',
  minor: '次要聚合通道',
  warning: '预警汇总通道',
  info: '常规提示通道',
  default: '默认通知通道',
};

const HANZI_PATTERN = /[\u4e00-\u9fff]/;

export function describeDependencyGate(gate: AlarmRule['dependencyGate']): string {
  if (!gate) return DEPENDENCY_GATE_LABEL.NONE;
  return DEPENDENCY_GATE_LABEL[gate] ?? DEPENDENCY_GATE_LABEL.NONE;
}

export function describeActionKind(kind: AlarmRuleActionKind): string {
  return ACTION_KIND_LABEL[kind] ?? '自动响应动作';
}

export function describeActionTrigger(triggerOn: AlarmRuleAction['triggerOn']): string {
  if (!triggerOn) return ACTION_TRIGGER_LABEL.RAISE;
  return ACTION_TRIGGER_LABEL[triggerOn] ?? ACTION_TRIGGER_LABEL.RAISE;
}

export function describeRuleAction(action: AlarmRuleAction): string {
  const triggerLabel = describeActionTrigger(action.triggerOn);
  const kindLabel = describeActionKind(action.kind);
  if (action.kind === 'WORK_ORDER' && action.workOrderTitle) {
    return `${triggerLabel}${kindLabel}（${action.workOrderTitle}）`;
  }
  if (action.kind === 'PROTECTION_COMMAND' && action.capability) {
    return `${triggerLabel}${kindLabel}`;
  }
  return `${triggerLabel}${kindLabel}`;
}

export function summarizeRuleActions(actions: AlarmRule['actions']): string | null {
  if (!actions || actions.length === 0) return null;
  const kinds = [...new Set(actions.map((action) => describeActionKind(action.kind)))];
  return `联动 ${actions.length} 项动作：${kinds.join('、')}`;
}

export function describeRoutingChannel(channel: string): string {
  return ROUTING_CHANNEL_LABEL[channel] ?? '站内通知通道';
}

export function describeGroupBy(groupBy: string | undefined): string {
  if (!groupBy) return '逐条发送';
  return GROUP_BY_LABEL[groupBy] ?? '按规则合并';
}

export function describeRoutingRouteName(name: string): string {
  const trimmed = name.trim();
  const normalized = trimmed.toLowerCase();
  if (normalized in ROUTING_ROUTE_NAME_LABEL) return ROUTING_ROUTE_NAME_LABEL[normalized];
  if (HANZI_PATTERN.test(trimmed)) return trimmed;
  return '分级通知通道';
}

export function describePlannedActionSummary(action: {
  readonly kind: AlarmRuleActionKind;
  readonly target: string;
  readonly summary: string;
}): string {
  if (action.kind === 'WEBHOOK') {
    try {
      const host = new URL(action.target.trim()).host;
      if (host) return `外部回调（${host}）`;
    } catch {
      // Ignore unparseable target host and fall back to plain Chinese label.
    }
    return '外部接口回调';
  }
  if (action.kind === 'WORK_ORDER') {
    const stripped = action.summary.replace(/^work order\s*(?:\[[^\]]+\]\s*)?:\s*/i, '').trim();
    if (HANZI_PATTERN.test(stripped)) return stripped;
    return '自动运维工单';
  }
  return '保护设定值与联锁指令';
}

export function describeDeadLetterError(code: string | undefined): string {
  if (!code) return '自动重试次数已耗尽';
  const upper = code.toUpperCase();
  if (upper in DEAD_LETTER_ERROR_LABEL) return DEAD_LETTER_ERROR_LABEL[upper];
  for (const [key, label] of Object.entries(DEAD_LETTER_ERROR_LABEL)) {
    if (upper.includes(key)) return label;
  }
  return '动作投递受阻，请人工核查后重试';
}

export function describeReloadError(code: string | undefined): string | null {
  if (!code) return null;
  const upper = code.toUpperCase();
  if (upper in RELOAD_ERROR_LABEL) return RELOAD_ERROR_LABEL[upper];
  return '最近一次路由热重载校验失败，系统继续沿用上一版生效配置';
}

export function routingDigestPrefix(digest: SiteAlarmRoutingSummary['configDigest']): string {
  const trimmed = digest.trim();
  return trimmed.length > 12 ? trimmed.slice(0, 12) : trimmed;
}
