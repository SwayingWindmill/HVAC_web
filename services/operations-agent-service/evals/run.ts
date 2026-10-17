// Runs the evaluation suite against the deployed assistant (#587). It executes in the signed-in
// Web app's page, so the operator's own session authorizes every call and no cookie, password or
// model key passes through whoever runs it. Each trial is a new Agent Session asked through the
// public Session API, exactly as the panel asks it. The Gateway validates every snapshot against
// the Session contract before it answers, so the runner reads them as the Agent's own type.

import type { AgentSessionSnapshot } from '../src/agent/index.ts';
import { gradeTrial, seriesKey, type CheckName, type SeriesByKey, type TrialGrade } from './grade.ts';
import { questions as suite, SONGYAXUAN_SITE_ID, type EvalQuestion } from './questions.ts';

export interface TrialResult extends TrialGrade {
  readonly questionId: string;
  readonly trial: number;
  /** The whole Session as graded, to read the transcript; absent when the trial could not run. */
  readonly snapshot?: AgentSessionSnapshot;
}

export interface QuestionReport {
  readonly id: string;
  readonly kind: EvalQuestion['kind'];
  readonly trials: number;
  /** pass^k: every trial passed every check. */
  readonly passedEveryTrial: boolean;
  /** Per check, the share of the trials it ran in that passed. */
  readonly checkPassRates: Partial<Record<CheckName, number>>;
  readonly meanDurationMs: number;
  readonly meanTokens: number;
  readonly meanToolCalls: number;
}

export interface SuiteReport {
  readonly siteId: string;
  readonly startedAt: string;
  readonly finishedAt: string;
  readonly questions: readonly QuestionReport[];
  readonly results: readonly TrialResult[];
}

export interface RunOptions {
  readonly trials?: number;
  /** Question ids to run again; all when omitted. */
  readonly only?: readonly string[];
  readonly onProgress?: (line: string) => void;
}

const POLL_MS = 2_000;
const RUN_TIMEOUT_MS = 180_000;

const request = async (path: string, init: RequestInit = {}): Promise<unknown> => {
  const response = await fetch(path, { credentials: 'include', ...init, headers: { Accept: 'application/json', ...init.headers } });
  if (!response.ok) throw new Error(`${init.method ?? 'GET'} ${path} answered ${response.status}`);
  return response.json();
};

const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));
const mean = (values: readonly number[]) => (values.length === 0 ? 0 : values.reduce((sum, value) => sum + value, 0) / values.length);

export const runSuite = async (options: RunOptions = {}): Promise<SuiteReport> => {
  const siteId = SONGYAXUAN_SITE_ID;
  const trials = options.trials ?? 3;
  const chosen = suite.filter((question) => options.only === undefined || options.only.includes(question.id));
  const progress = options.onProgress ?? (() => undefined);
  const startedAt = new Date().toISOString();

  const principal = await request('/api/v1/principal') as { session: { csrfToken: string } };
  const site = await request(`/api/v1/sites/${siteId}`) as { tenantId: string; timezone: string };
  const post = (path: string, body: unknown) => request(path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': principal.session.csrfToken },
    body: JSON.stringify(body),
  });

  // The reference figures, read once per period from the same Energy Series the pages use.
  const series: Record<string, SeriesByKey[string]> = {};
  for (const figure of chosen.flatMap((question) => question.figures)) {
    const key = seriesKey(figure);
    if (series[key] !== undefined) continue;
    const read = async (energyType: 'electricity' | 'cooling') => (await post('/api/v1/analytics/energy-series', {
      tenantId: site.tenantId, siteId, energyType, granularity: figure.granularity, timezone: site.timezone,
      from: figure.from, to: figure.to, qualityPolicy: 'VALID_ONLY',
    }) as { points: SeriesByKey[string]['electricity'] }).points;
    series[key] = { timezone: site.timezone, electricity: await read('electricity'), cooling: await read('cooling') };
  }

  const sessions = `/api/v1/sites/${siteId}/operations/agent-sessions`;
  const ask = async (question: EvalQuestion): Promise<AgentSessionSnapshot> => {
    let snapshot = await post(sessions, { message: question.text }) as AgentSessionSnapshot;
    const deadline = Date.now() + RUN_TIMEOUT_MS;
    while (snapshot.session.status === 'ACTIVE' && Date.now() < deadline) {
      await sleep(POLL_MS);
      snapshot = await request(`${sessions}/${snapshot.session.id}`) as AgentSessionSnapshot;
    }
    if (snapshot.session.status === 'ACTIVE') {
      // Graded as it stands (the Run did not complete); stop it so it does not run on unseen.
      await post(`${sessions}/${snapshot.session.id}:cancel`, { expectedRevision: snapshot.session.revision });
    }
    return snapshot;
  };

  const results: TrialResult[] = [];
  for (const question of chosen) {
    for (let trial = 1; trial <= trials; trial += 1) {
      try {
        const snapshot = await ask(question);
        const grade = gradeTrial(question, snapshot, series);
        results.push({ ...grade, questionId: question.id, trial, snapshot });
        const failed = grade.checks.filter((each) => !each.passed).map((each) => `${each.name}: ${each.reason}`);
        progress(`${question.id} #${trial} ${grade.passed ? 'PASS' : 'FAIL'} ${(grade.durationMs / 1000).toFixed(1)} s ${failed.join(' | ')}`);
      } catch (error) {
        // One broken trial is a failed trial, not a lost run.
        const reason = `the trial could not run: ${String(error)}`;
        results.push({
          passed: false, checks: [{ name: 'completed', passed: false, reason }],
          durationMs: Number.POSITIVE_INFINITY, tokens: 0, toolCalls: 0, questionId: question.id, trial,
        });
        progress(`${question.id} #${trial} FAIL ${reason}`);
      }
    }
  }

  const questions = chosen.map((question): QuestionReport => {
    const own = results.filter((result) => result.questionId === question.id);
    const names = [...new Set(own.flatMap((result) => result.checks.map((each) => each.name)))];
    const rate = (name: CheckName) => {
      const ran = own.flatMap((result) => result.checks.filter((each) => each.name === name));
      return ran.filter((each) => each.passed).length / ran.length;
    };
    const finished = own.filter((result) => Number.isFinite(result.durationMs));
    return {
      id: question.id,
      kind: question.kind,
      trials: own.length,
      passedEveryTrial: own.length > 0 && own.every((result) => result.passed),
      checkPassRates: Object.fromEntries(names.map((name) => [name, rate(name)])),
      meanDurationMs: Math.round(mean(finished.map((result) => result.durationMs))),
      meanTokens: Math.round(mean(finished.map((result) => result.tokens))),
      meanToolCalls: mean(finished.map((result) => result.toolCalls)),
    };
  });
  return { siteId, startedAt, finishedAt: new Date().toISOString(), questions, results };
};
