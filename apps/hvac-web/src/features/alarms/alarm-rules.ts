import { queryOptions } from '@tanstack/react-query';
import {
  createPlatformGatewayClient,
  type AlarmRule,
  type AlarmRuleCondition,
  type AlarmRuleReading,
} from '@/api/generated/platformGateway.gen';
import { formatTelemetryDisplayValue, formatTelemetryUnit } from '@/domain/centralPlantTelemetry';

const client = createPlatformGatewayClient();

export type { AlarmRule };

/** The rules the Site evaluates now (#693). They change only when someone publishes the rules file. */
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

function describeNormalized(condition: AlarmRuleCondition, names: ReadingNames): string {
  if (condition.reading) {
    return describeLeaves([{ reading: condition.reading, predicate: predicate(condition, names.point(condition.reading).unit) }], true, names);
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

/** Every device a rule reads, by name in the order it first reads them; past LISTED they are counted. */
export function describeDevices(rule: AlarmRule, names: ReadingNames): string {
  const devices = new Set<string>();
  const visit = (condition: AlarmRuleCondition) => {
    if (condition.reading) devices.add(condition.reading.deviceId);
    condition.children?.forEach(visit);
  };
  visit(rule.raise);
  visit(rule.clear);
  const named = [...devices].map((id) => plainName(names.device(id)));
  return named.length > LISTED ? `${named.slice(0, LISTED).join('、')} 等 ${named.length} 台` : named.join('、');
}
