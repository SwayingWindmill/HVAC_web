import { queryOptions } from '@tanstack/react-query';
import {
  createPlatformGatewayClient,
  PlatformApiError,
  type AlarmRule,
  type AlarmRuleCondition,
  type AlarmRuleReading,
  type AlarmRuleSimulationRequest,
  type AlarmRuleSimulationResult,
  type AlarmRuleSimulationStep,
} from '@/api/generated/platformGateway.gen';
import { formatTelemetryDisplayValue, formatTelemetryUnit } from '@/domain/centralPlantTelemetry';

const client = createPlatformGatewayClient();

export type {
  AlarmRule,
  AlarmRuleCondition,
  AlarmRuleReading,
  AlarmRuleSimulationRequest,
  AlarmRuleSimulationResult,
  AlarmRuleSimulationStep,
};

/** The rules the Site evaluates now. They change only when someone publishes the rules file. */
export function siteAlarmRulesQuery(siteId: string) {
  return queryOptions({
    queryKey: ['alarms', siteId, 'rules'] as const,
    queryFn: async ({ signal }) => (await client.listSiteAlarmRules(siteId, { signal })).data.data,
    staleTime: 5 * 60_000,
  });
}

/** How a rule names a reading: its device and point by name, and the point's unit. */
export interface ReadingNames {
  device(deviceId: string): string;
  point(reading: AlarmRuleReading): { readonly name: string; readonly unit?: string | null };
}

// The rules file's "any reading" clear condition: a number within ±1e12 only says one arrived.
const ANY_READING = 1e12;
// Past this many readings a group is counted, not listed.
const LISTED = 3;

/** A span of seconds in the one unit that says it exactly: 6 小时, 15 分钟, 90 秒. */
export function formatSeconds(seconds: number): string {
  if (seconds % 3600 === 0) return `${seconds / 3600} 小时`;
  if (seconds % 60 === 0) return `${seconds / 60} 分钟`;
  return `${seconds} 秒`;
}

function formatValue(value: number | string | boolean | undefined, unit: string | null | undefined): string {
  if (typeof value === 'boolean') return value ? '是' : '否';
  const text = formatTelemetryDisplayValue(value);
  const shown = formatTelemetryUnit(unit);
  return typeof value === 'number' && shown ? `${text} ${shown}` : text;
}

/** What a condition says about its one reading, without naming the reading: 高于 10 °C, 30 分钟没有新读数. */
function predicate(condition: AlarmRuleCondition, unit: string | null | undefined): string {
  const value = (number: number | undefined) => formatValue(number, unit);
  switch (condition.kind) {
    case 'COMPARE': {
      if (condition.operator === 'NE' && condition.value === '') return '有新读数';
      const phrases = { EQ: '为', NE: '不为', GT: '高于', GTE: '不低于', LT: '低于', LTE: '不高于' } as const;
      // A number stands apart from the words around it; a state reads as one word: 高于 10 °C, 为运行.
      return `${phrases[condition.operator!]}${typeof condition.value === 'number' ? ' ' : ''}${formatValue(condition.value, unit)}`;
    }
    case 'RANGE':
      if (condition.minimum! <= -ANY_READING && condition.maximum! >= ANY_READING) return '有新读数';
      return `在 ${value(condition.minimum)} 到 ${value(condition.maximum)} 之间`;
    case 'HYSTERESIS':
      return condition.direction === 'ABOVE'
        ? `达到 ${value(condition.trigger)} 及以上（降到 ${value(condition.reset)} 及以下才算回落）`
        : `降到 ${value(condition.trigger)} 及以下（升到 ${value(condition.reset)} 及以上才算回升）`;
    case 'NO_DATA':
      return `超过 ${formatSeconds(condition.ageSeconds!)}没有新读数`;
    case 'STALE':
      return `有读数，但已超过 ${formatSeconds(condition.ageSeconds!)}没有更新`;
    case 'UNCHANGED':
      return `连续 ${formatSeconds(condition.ageSeconds!)}完全没变`;
    case 'CHANGED':
      return `在 ${formatSeconds(condition.ageSeconds!)}内有变化`;
    case 'REASONS': {
      const names: Record<string, string> = {
        OUT_OF_RANGE: '超出量程', SPIKE: '跳变', SENTINEL: '通讯异常值', FLATLINE: '卡住', DERIVED_INPUT: '计算输入不可信',
        SOURCE_UNTRUSTED: '采集源不可信',
        PHYSICS_WET_BULB_DRY: '湿球高于干球', PHYSICS_WET_BULB_CONDENSER: '湿球不像湿球', PHYSICS_POWER: '功率超过铭牌',
        PHYSICS_COP: '能效超过卡诺上限', PHYSICS_CHW_DIRECTION: '冷冻水供回水接反', PHYSICS_CW_DIRECTION: '冷却水进出水接反',
        PHYSICS_HEAT_BALANCE: '热平衡对不上', LOW_CONFIDENCE: '温差不确定度过大', PROBE_OFFSET: '供回水零点偏差',
      };
      const list = (condition.reasons ?? []).map((code) => names[code] ?? code).join('、');
      return condition.match === 'NONE' ? `没有这些数据质量问题（${list}）` : `数据质量问题：${list}`;
    }
    case 'SLOPE': {
      const span = formatSeconds(condition.windowSeconds!);
      const delta = condition.delta ?? 0;
      const op = condition.operator ?? 'GTE';
      if (delta >= 0 && (op === 'GT' || op === 'GTE')) {
        return `在 ${span}内升高${op === 'GTE' ? '达到' : '超过'} ${formatValue(delta, unit)}`;
      }
      if (delta < 0 && (op === 'LT' || op === 'LTE')) {
        return `在 ${span}内降低${op === 'LTE' ? '达到' : '超过'} ${formatValue(Math.abs(delta), unit)}`;
      }
      const phrases = { EQ: '等于', NE: '不等于', GT: '大于', GTE: '不低于', LT: '低于', LTE: '不高于' } as const;
      return `在 ${span}内变化${phrases[op]} ${formatValue(delta, unit)}`;
    }
    default:
      return '';
  }
}

type Leaf = { readonly reading: AlarmRuleReading; readonly predicate: string };

const sameReading = (left: AlarmRuleReading, right: AlarmRuleReading) =>
  left.deviceId === right.deviceId && left.pointCode === right.pointCode;

/** A rule's children as the reader takes them: nested AND in AND (OR in OR) is one list, and a
 * boolean reading that is either true or false just has a reading. */
function normalize(condition: AlarmRuleCondition): AlarmRuleCondition {
  const children = condition.children?.map(normalize);
  if (!children) return condition;
  const [first, second] = children;
  if (condition.kind === 'OR' && children.length === 2 && first.reading && second.reading && sameReading(first.reading, second.reading)
    && first.operator === 'EQ' && second.operator === 'EQ' && typeof first.value === 'boolean' && typeof second.value === 'boolean' && first.value !== second.value) {
    return { kind: 'COMPARE', reading: first.reading, operator: 'NE', value: '' };
  }
  return { ...condition, children: children.flatMap((child) => (child.kind === condition.kind ? child.children! : [child])) };
}

/** Words joined into a sentence, with a space where Chinese meets a letter or digit: 「温度通道 3」+「连续」→「温度通道 3 连续」. */
function spaced(...parts: readonly string[]): string {
  return parts.reduce((sentence, part) => {
    const before = sentence.at(-1) ?? '';
    const after = part[0] ?? '';
    const latin = /[A-Za-z0-9]/;
    const chinese = /[一-鿿]/;
    return (latin.test(before) && chinese.test(after)) || (chinese.test(before) && latin.test(after)) ? `${sentence} ${part}` : sentence + part;
  }, '');
}

/** A device's reading, without the bracketed detail a name carries: 「冷却水进水温度（四台共用）」→「冷却水进水温度」. */
const plainName = (name: string) => name.split('（')[0].trim();

function describeLeaves(leaves: readonly Leaf[], all: boolean, names: ReadingNames): string {
  const devices = [...new Set(leaves.map((leaf) => leaf.reading.deviceId))].map((id) => plainName(names.device(id)));
  const points = [...new Set(leaves.map((leaf) => plainName(names.point(leaf.reading).name)))];
  const phrase = leaves[0].predicate;
  const each = all ? '都' : '中任一个';
  if (leaves.length === 1) return spaced(devices[0], '的', points[0], phrase);
  if (devices.length === 1) {
    const listed = points.length > LISTED ? `${points.length} 个读数` : points.join('、');
    return spaced(devices[0], '的', listed, each, phrase);
  }
  if (points.length === 1 && devices.length <= LISTED) return spaced(devices.join('、'), '的', points[0], all ? '都' : '中任一台', phrase);
  if (points.length === 1) return spaced(`${devices.length} 台设备`, '的', points[0], all ? '都' : '中任一台', phrase);
  return spaced(`${devices.length} 台设备`, '的', `${leaves.length} 个读数`, each, phrase);
}

function describeExpression(condition: AlarmRuleCondition, names: ReadingNames): string {
  const expr = condition.expression ?? '';
  const inputs = condition.inputs ?? {};
  const parts: string[] = [];
  let index = 0;
  while (index < expr.length) {
    const ch = expr[index];
    if (ch === ' ' || ch === '\t' || ch === '\n') {
      index += 1;
      continue;
    }
    if (ch === '"' || ch === "'") {
      const quote = ch;
      let end = index + 1;
      while (end < expr.length && expr[end] !== quote) {
        if (expr[end] === '\\' && end + 1 < expr.length) end += 2;
        else end += 1;
      }
      const raw = expr.slice(index + 1, end);
      parts.push(`「${formatTelemetryDisplayValue(raw)}」`);
      index = Math.min(end + 1, expr.length);
      continue;
    }
    const twoChar = expr.slice(index, index + 2);
    if (twoChar === '&&') { parts.push(' 并且 '); index += 2; continue; }
    if (twoChar === '||') { parts.push(' 或者 '); index += 2; continue; }
    if (twoChar === '==') { parts.push(' 为 '); index += 2; continue; }
    if (twoChar === '!=') { parts.push(' 不为 '); index += 2; continue; }
    if (twoChar === '>=') { parts.push(' 不低于 '); index += 2; continue; }
    if (twoChar === '<=') { parts.push(' 不高于 '); index += 2; continue; }
    if (ch === '>') { parts.push(' 高于 '); index += 1; continue; }
    if (ch === '<') { parts.push(' 低于 '); index += 1; continue; }
    if (ch === '!') { parts.push('非 '); index += 1; continue; }
    if (/[+\-*/(),]/.test(ch)) {
      parts.push(ch === ',' ? '、' : ch === '(' || ch === ')' ? ch : ` ${ch} `);
      index += 1;
      continue;
    }
    if (/[A-Za-z_]/.test(ch)) {
      let end = index + 1;
      while (end < expr.length && /[A-Za-z0-9_]/.test(expr[end])) end += 1;
      const token = expr.slice(index, end);
      const reading = inputs[token];
      if (reading) {
        const dev = plainName(names.device(reading.deviceId));
        const pt = plainName(names.point(reading).name);
        parts.push(spaced(dev, '的', pt));
      } else if (token === 'true') {
        parts.push('是');
      } else if (token === 'false') {
        parts.push('否');
      } else if (token === 'abs') {
        parts.push('绝对值');
      } else if (token === 'min') {
        parts.push('最小值');
      } else if (token === 'max') {
        parts.push('最大值');
      } else {
        parts.push(token);
      }
      index = end;
      continue;
    }
    if (/[0-9.]/.test(ch)) {
      let end = index + 1;
      while (end < expr.length && /[0-9.]/.test(expr[end])) end += 1;
      parts.push(expr.slice(index, end));
      index = end;
      continue;
    }
    parts.push(ch);
    index += 1;
  }
  return parts.join('').replace(/\s+/g, ' ').trim();
}

function describeNormalized(condition: AlarmRuleCondition, names: ReadingNames): string {
  if (condition.reading) {
    return describeLeaves([{ reading: condition.reading, predicate: predicate(condition, names.point(condition.reading).unit) }], true, names);
  }
  if (condition.kind === 'EXPRESSION') {
    return describeExpression(condition, names);
  }
  const all = condition.kind === 'AND';
  const leaves: Leaf[] = [];
  const parts: string[] = [];
  for (const child of condition.children!) {
    if (child.reading) leaves.push({ reading: child.reading, predicate: predicate(child, names.point(child.reading).unit) });
    else parts.push(describeNormalized(child, names));
  }
  // Readings that say the same thing are said once.
  const byPredicate = new Map<string, Leaf[]>();
  for (const leaf of leaves) byPredicate.set(leaf.predicate, [...(byPredicate.get(leaf.predicate) ?? []), leaf]);
  const said = [...byPredicate.values()].map((group) => describeLeaves(group, all, names));
  return spaced(...[...said, ...parts].flatMap((part, index) => (index === 0 ? [part] : [all ? '，并且' : '，或者', part])));
}

/** A condition in the operator's words, naming each reading by device and point. */
export function describeCondition(condition: AlarmRuleCondition, names: ReadingNames): string {
  return describeNormalized(normalize(condition), names);
}

/** When the rule raises its alarm: the raise condition, and how long it has to hold. */
export function describeRaise(rule: AlarmRule, names: ReadingNames): string {
  const condition = describeCondition(rule.raise, names);
  if (rule.triggerMode === 'DURATION') return `${condition}，持续 ${formatSeconds(rule.durationSeconds!)}`;
  if (rule.triggerMode === 'REPEATING') return `${condition}，连续 ${rule.repeatCount} 次读数都这样`;
  return condition;
}

/** Deduplicated readings referenced by a rule (raise first, then clear) in traversal order. */
export function collectRuleReadings(rule: AlarmRule): AlarmRuleReading[] {
  const seen = new Set<string>();
  const readings: AlarmRuleReading[] = [];
  const addReading = (reading: AlarmRuleReading | undefined) => {
    if (!reading) return;
    const key = `${reading.deviceId}/${reading.pointCode}`;
    if (seen.has(key)) return;
    seen.add(key);
    readings.push(reading);
  };
  const visit = (condition: AlarmRuleCondition) => {
    addReading(condition.reading);
    if (condition.inputs) {
      for (const reading of Object.values(condition.inputs)) {
        addReading(reading);
      }
    }
    condition.children?.forEach(visit);
  };
  visit(rule.raise);
  visit(rule.clear);
  return readings;
}

const RULE_KIND_LABELS: Readonly<Record<NonNullable<AlarmRule['ruleKind']>, string>> = {
  THRESHOLD: '阈值规则',
  SLOPE: '斜率趋势',
  EXPRESSION: '多点表达式',
};

const OVERRIDDEN_FIELD_LABELS: Readonly<Record<string, string>> = {
  ruleKind: '规则类型',
  title: '名称',
  summary: '说明',
  severity: '严重程度',
  qualityPolicy: '质量门控',
  freshnessSeconds: '有效时长',
  triggerMode: '触发方式',
  durationSeconds: '持续时长',
  repeatCount: '连续次数',
  raise: '报警条件',
  'raise.delta': '变化幅度',
  'raise.windowSeconds': '斜率窗口',
  'raise.value': '报警阈值',
  'raise.trigger': '触发阈值',
  clear: '恢复条件',
  'clear.value': '恢复阈值',
  'clear.reset': '恢复回差',
};

function formatOverriddenField(field: string): string {
  if (OVERRIDDEN_FIELD_LABELS[field]) return OVERRIDDEN_FIELD_LABELS[field];
  if (field.startsWith('raise.')) return '报警参数';
  if (field.startsWith('clear.')) return '恢复参数';
  return '自定义参数';
}

export function describeRuleOrigin(rule: AlarmRule): string {
  const origin = rule.templateKey
    ? rule.overriddenFields && rule.overriddenFields.length > 0
      ? `模板派生（覆盖${[...new Set(rule.overriddenFields.map(formatOverriddenField))].join('、')}）`
      : '模板派生'
    : '场站规则';
  const kind = rule.ruleKind ? RULE_KIND_LABELS[rule.ruleKind] : null;
  return kind ? `${origin} · ${kind}` : origin;
}

export function describeRuleLinkage(rule: AlarmRule, titleByRuleId: ReadonlyMap<string, string>): string {
  const deps = rule.dependsOnRuleIds ?? [];
  if (deps.length === 0) return '独立评估';
  const hop = rule.hopDepth ?? 1;
  const titles = deps.map((id) => titleByRuleId.get(id) ?? '前置规则').map((title) => `「${title}」`);
  return `第 ${hop} 跳 · 依赖${titles.join('、')}`;
}

const SIMULATION_ERROR_MESSAGES: Readonly<Record<string, string>> = {
  ORIGIN_NOT_ALLOWED: '页面来源校验未通过，请刷新页面后重试',
  CSRF_TOKEN_INVALID: '会话安全凭证失效，请刷新页面后重试',
  CSRF_REQUIRED: '缺少会话安全凭证，请刷新页面后重试',
  CSRF_INVALID: '会话安全凭证无效，请刷新页面后重试',
  ALARM_ACCESS_DENIED: '你没有执行规则模拟试算的权限',
  FORBIDDEN: '你没有执行规则模拟试算的权限',
  RESOURCE_NOT_FOUND: '未找到对应的站内规则，请刷新规则列表',
  RULE_GRAPH_INVALID: '规则因果依赖图校验未通过，无法试算',
};

export async function simulateSiteAlarmRule(
  siteId: string,
  body: AlarmRuleSimulationRequest,
  csrfToken: string,
): Promise<AlarmRuleSimulationResult> {
  try {
    const response = await client.simulateSiteAlarmRule(siteId, body, csrfToken);
    return response.data.data;
  } catch (error) {
    if (error instanceof PlatformApiError) {
      const code = error.problem.code ?? '';
      if (code in SIMULATION_ERROR_MESSAGES) {
        throw new Error(SIMULATION_ERROR_MESSAGES[code]);
      }
      if (/[\u4e00-\u9fff]/.test(error.problem.detail)) {
        throw new Error(error.problem.detail);
      }
      throw new Error('模拟试算未能完成，请检查输入值后重试');
    }
    throw error instanceof Error ? error : new Error('模拟试算未能完成，请稍后重试');
  }
}

/** Every device a rule reads, by name in the order it first reads them; past LISTED they are counted. */
export function describeDevices(rule: AlarmRule, names: ReadingNames): string {
  const devices = new Set(collectRuleReadings(rule).map((reading) => reading.deviceId));
  const named = [...devices].map((id) => plainName(names.device(id)));
  return named.length > LISTED ? `${named.slice(0, LISTED).join('、')} 等 ${named.length} 台` : named.join('、');
}
