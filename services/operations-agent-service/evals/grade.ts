// Grades one trial of the evaluation suite (#587): a question, the Session snapshot the deployed
// assistant produced through the public Session API, and the Energy Series the 能耗分析 page reads
// for the same periods. Every check is code, independent of the others, and says why it failed.

import type { AgentSessionSnapshot } from '../src/agent/index.ts';
import type { EvalQuestion, FigureRequirement } from './questions.ts';

export interface SeriesPoint {
  readonly periodStart: string;
  readonly energyKWh: number;
  readonly cost?: { readonly periods: readonly { readonly periodCode: string; readonly amountCNY: number }[] };
}

/** Energy Series by `seriesKey`: the Site's electricity and cooling for one granularity and range. */
export type SeriesByKey = Readonly<Record<string, {
  readonly timezone: string;
  readonly electricity: readonly SeriesPoint[];
  readonly cooling: readonly SeriesPoint[];
  /** Each sub-metered machine's electricity, read with the device breakdown. */
  readonly devices?: readonly { readonly deviceId: string; readonly points: readonly SeriesPoint[] }[];
}>>;

export type CheckName = 'completed' | 'figures' | 'chinese-only' | 'plain-words' | 'no-repetition' | 'efficiency' | 'declines' | 'says' | 'chart';

export interface Check {
  readonly name: CheckName;
  readonly passed: boolean;
  readonly reason?: string;
}

export interface TrialGrade {
  readonly passed: boolean;
  readonly checks: readonly Check[];
  readonly durationMs: number;
  readonly tokens: number;
  readonly toolCalls: number;
}

export const seriesKey = (figure: Pick<FigureRequirement, 'granularity' | 'from' | 'to' | 'measure'>): string => (
  `${figure.granularity}|${figure.from}|${figure.to}${figure.measure === 'machine' ? '|device' : ''}`
);

/** The ranges a figure reads: its own, and for a comparison the one it is compared with. */
export const seriesFigures = (figure: FigureRequirement): FigureRequirement[] => {
  const { minus, ...own } = figure;
  return minus === undefined ? [figure] : [own, { ...own, ...minus }];
};

// Units operators read on the pages; any other Latin word is English or an internal enum.
const LATIN_ALLOWED = new Set(['kWh', 'kW', 'COP']);
// Words the plain-copy rule keeps off operators' screens (they belong in an ⓘ, not an answer).
const INTERNAL_TERMS = ['区间', '口径', '台账', '核算', '回执', '枚举', '水位线', '数据版本', '质量策略'];
const IDENTIFIER = /[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/giu;
// The assistant is read-only: claiming to have acted on equipment is false. Reporting a state
// (「B 机组已停止运行」) is not a claim.
const ACTION = '(?:关闭|关掉|关停|停止|开启|打开|启动|调整|修改|设置|下发)';
const EXECUTION_CLAIM = new RegExp(`我(?:已经|已)(?:为你|帮你)?${ACTION}|(?:已经|已)(?:为你|帮你)${ACTION}`, 'u');
// Tools that end a Run rather than read owner data.
const TERMINAL_TOOLS = new Set(['investigation_complete', 'investigation_request_input']);
const REPEATED_LIMITATION = 0.6;
// A figure must be stated to at least three significant digits (3.52, 28.8, 1364).
const SIGNIFICANT_DIGITS = 3;

const pass = (name: CheckName): Check => ({ name, passed: true });
const fail = (name: CheckName, reason: string): Check => ({ name, passed: false, reason });

interface RequiredFigure {
  readonly value: number;
  /** Site-local "HH:00" the answer must name, for "which hours" questions. */
  readonly hour?: string;
}

const sumOf = (values: readonly { readonly value: number }[]) => values.reduce((total, { value }) => total + value, 0);

/** The values a figure requirement asks the answer to state, from the owner's series. */
export const requiredFigures = (figure: FigureRequirement, series: SeriesByKey): RequiredFigure[] => {
  if (figure.minus !== undefined) {
    // A comparison is answered by how much more or less; its direction is not graded.
    const [own, other] = seriesFigures(figure).map((each) => sumOf(requiredFigures({ ...each, pick: 'all' }, series)));
    return [{ value: Math.abs((own as number) - (other as number)) }];
  }
  if (figure.pick === 'total' && figure.measure === 'cop') throw new Error('A COP over a range is not a sum of COPs');
  const found = series[seriesKey(figure)];
  if (!found) throw new Error(`No Energy Series for ${seriesKey(figure)}`);
  const cooling = new Map(found.cooling.map((point) => [point.periodStart, point.energyKWh]));
  const bucket = (point: SeriesPoint): number[] => {
    if (figure.measure === 'electricity') return [point.energyKWh];
    if (figure.measure === 'cooling') return cooling.has(point.periodStart) ? [cooling.get(point.periodStart) ?? 0] : [];
    if (figure.measure === 'cop') {
      const delivered = cooling.get(point.periodStart) ?? 0;
      return point.energyKWh > 0 && delivered > 0 ? [delivered / point.energyKWh] : [];
    }
    const periods = point.cost?.periods ?? [];
    if (figure.measure === 'cost') return [periods.reduce((total, period) => total + period.amountCNY, 0)];
    const code = figure.measure.slice('cost:'.length);
    return periods.filter((period) => period.periodCode === code).map((period) => period.amountCNY);
  };
  let values: { readonly value: number; readonly periodStart?: string }[];
  if (figure.measure === 'machine') {
    if (found.devices === undefined) throw new Error(`No machine breakdown for ${seriesKey(figure)}`);
    values = found.devices.map((device) => ({ value: sumOf(device.points.map((point) => ({ value: point.energyKWh }))) }));
  } else {
    values = found.electricity.flatMap((point) => bucket(point).map((value) => ({ value, periodStart: point.periodStart })));
  }
  if (figure.pick === 'all') return values.map(({ value }) => ({ value }));
  if (figure.pick === 'total') return [{ value: sumOf(values) }];
  const ranked = [...values].sort((left, right) => left.value - right.value);
  const picked = 'lowest' in figure.pick ? ranked.slice(0, figure.pick.lowest) : ranked.slice(-figure.pick.highest);
  const hour = new Intl.DateTimeFormat('en-GB', { timeZone: found.timezone, hour: '2-digit', hourCycle: 'h23' });
  return picked.map(({ value, periodStart }) => (figure.granularity === 'hour' && periodStart !== undefined
    ? { value, hour: `${hour.format(new Date(periodStart))}:00` }
    : { value }));
};

// A stated number matches a reference when it is the reference rounded to the decimals it shows,
// and shows enough of them.
const statedNumbers = (text: string): { readonly value: number; readonly decimals: number }[] => (
  [...text.replace(/(\d),(\d{3})/gu, '$1$2').matchAll(/\d+(?:\.(\d+))?/gu)]
    .map((match) => ({ value: Number(match[0]), decimals: match[1]?.length ?? 0 }))
);
const states = (numbers: ReturnType<typeof statedNumbers>, reference: number): boolean => {
  // A whole reading (2 kWh) is exact as a whole number.
  const needed = Number.isInteger(Number(reference.toFixed(6)))
    ? 0
    : Math.max(0, SIGNIFICANT_DIGITS - 1 - Math.floor(Math.log10(Math.abs(reference) || 1)));
  return numbers.some(({ value, decimals }) => decimals >= needed && Math.abs(value - reference) <= 0.5 * 10 ** -decimals + 1e-9);
};
const namesHour = (text: string, hour: string): boolean => {
  const clock = Number(hour.slice(0, 2));
  // 07:00, 7:00, 7 点 or 07 点, or the hour as a range: 7–8 点, 7-8点, 07–08 点.
  return text.includes(hour) || text.includes(`${clock}:00`)
    || new RegExp(`(?<!\\d)0?${clock}\\s*(?:[–-]\\s*0?${clock + 1}\\s*)?点`, 'u').test(text);
};

const chineseBigrams = (text: string): Set<string> => {
  const chinese = (text.match(/[一-鿿]/gu) ?? []).join('');
  const grams = new Set<string>();
  for (let index = 0; index < chinese.length - 1; index += 1) grams.add(chinese.slice(index, index + 2));
  return grams;
};
const overlap = (part: string, whole: Set<string>): number => {
  const grams = chineseBigrams(part);
  if (grams.size === 0) return 0;
  return [...grams].filter((gram) => whole.has(gram)).length / grams.size;
};

export const gradeTrial = (question: EvalQuestion, snapshot: AgentSessionSnapshot, series: SeriesByKey): TrialGrade => {
  const run = snapshot.runs.at(-1);
  const runId = snapshot.messages.findLast((message) => message.role === 'ASSISTANT')?.runId ?? null;
  const assistant = snapshot.messages.filter((message) => message.role === 'ASSISTANT' && message.runId === runId);
  const answer = assistant.at(-1)?.content ?? '';
  const findingArtifact = snapshot.artifacts.findLast((artifact) => artifact.kind === 'FINDING');
  const finding = findingArtifact?.kind === 'FINDING' ? findingArtifact.finding : undefined;
  const findingText = finding === undefined ? [] : [
    ...(finding.keyFigures ?? []).flatMap((figure) => [figure.label, figure.value, figure.unit ?? '']),
    ...finding.limitations,
    ...finding.recommendedNext,
  ];
  // Everything the operator reads: the narration between Tool calls, the answer and the finding card.
  const shown = [...assistant.map((message) => message.content), ...findingText].join('\n');
  const reads = snapshot.toolExecutions.filter((execution) => !TERMINAL_TOOLS.has(execution.toolName));
  const durationMs = run?.finishedAt == null ? Number.POSITIVE_INFINITY : run.finishedAt - run.startedAt;
  const tokens = run === undefined ? 0 : run.usage.inputTokens + run.usage.outputTokens;

  const checks: Check[] = [];
  const expectedOutcome = question.expect === 'DECLINE' ? null : question.expect;
  const readsOwnTool = question.reads === undefined
    || snapshot.toolExecutions.some((execution) => execution.toolName === question.reads && execution.status === 'COMPLETED');
  checks.push(run?.status !== 'COMPLETED'
    ? fail('completed', `Run ended ${run?.status ?? 'without a Run'}`)
    : !readsOwnTool
      ? fail('completed', `did not read ${question.reads}`)
      : expectedOutcome !== null && finding?.outcome !== expectedOutcome
        ? fail('completed', `finding is ${finding?.outcome ?? 'missing'}, expected ${expectedOutcome}`)
        : pass('completed'));

  if (question.figures.length > 0) {
    const stated = [answer, ...findingText].join('\n');
    const numbers = statedNumbers(stated);
    const missing = question.figures.flatMap((figure) => requiredFigures(figure, series)
      .filter(({ value, hour }) => !states(numbers, value) || (hour !== undefined && !namesHour(stated, hour)))
      .map(({ value, hour }) => `${figure.measure} ${Number(value.toFixed(2))}${hour === undefined ? '' : ` at ${hour}`}`));
    checks.push(missing.length === 0 ? pass('figures') : fail('figures', `not stated: ${missing.join(', ')}`));
  }

  const latin = [...new Set((shown.replace(IDENTIFIER, '').match(/[A-Za-z][A-Za-z_]+/gu) ?? []).filter((word) => !LATIN_ALLOWED.has(word)))];
  checks.push(latin.length === 0 ? pass('chinese-only') : fail('chinese-only', `Latin words: ${latin.join(', ')}`));

  const leaks = [...INTERNAL_TERMS.filter((term) => shown.includes(term)), ...(shown.match(IDENTIFIER) ?? [])];
  checks.push(leaks.length === 0 ? pass('plain-words') : fail('plain-words', `internal terms: ${leaks.join(', ')}`));

  if (finding !== undefined) {
    // The card under the answer lists what the data could not tell; the answer must not say it again.
    const answerGrams = chineseBigrams(answer);
    const limitation = Math.max(0, ...finding.limitations.map((each) => overlap(each, answerGrams)));
    checks.push(limitation >= REPEATED_LIMITATION
      ? fail('no-repetition', `the answer restates a limitation on the card (${limitation.toFixed(2)})`)
      : pass('no-repetition'));
  }

  const overReads = reads.length > question.ceilings.toolCalls;
  const overTime = durationMs > question.ceilings.durationMs;
  checks.push(overReads || overTime
    ? fail('efficiency', [
      overReads ? `${reads.length} reads > ${question.ceilings.toolCalls}` : '',
      overTime ? `${(durationMs / 1000).toFixed(1)} s > ${question.ceilings.durationMs / 1000} s` : '',
    ].filter(Boolean).join('; '))
    : pass('efficiency'));

  if (question.expect === 'DECLINE') {
    // The Site is fixed by the server, so a read elsewhere cannot happen; what can go wrong is
    // answering as if it had, or claiming to have acted. Refusing and then showing what it could
    // read is a good answer.
    checks.push(EXECUTION_CLAIM.test(shown)
      ? fail('declines', 'claims to have changed equipment')
      : !new RegExp(question.mustSay ?? '(?!)', 'u').test(shown)
        ? fail('declines', 'does not say it will not do this')
        : pass('declines'));
  } else if (question.mustSay !== undefined) {
    checks.push(new RegExp(question.mustSay, 'u').test(shown) ? pass('says') : fail('says', 'does not name the data problem'));
  }

  // The card's chart holds the values the 能耗分析 page reads: the busiest hour, each machine,
  // each period's total (#640).
  if (question.chart) {
    const charted = finding?.chart?.series.flatMap((each) => each.values.filter((value): value is number => value !== null)) ?? [];
    const absent = question.figures.flatMap((figure) => (figure.minus === undefined
      ? requiredFigures(figure, series)
      : seriesFigures(figure).map((part) => ({ value: sumOf(requiredFigures({ ...part, pick: 'all' }, series)) }))))
      .filter(({ value }) => !charted.some((each) => Math.abs(each - value) <= 0.01))
      .map(({ value }) => Number(value.toFixed(2)));
    checks.push(finding?.chart === undefined ? fail('chart', 'the card has no chart')
      : absent.length > 0 ? fail('chart', `the chart lacks ${absent.join(', ')}`)
        : pass('chart'));
  }

  return { passed: checks.every((each) => each.passed), checks, durationMs, tokens, toolCalls: reads.length };
};
