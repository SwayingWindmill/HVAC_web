import { queryOptions } from '@tanstack/react-query';
import { intelligencePaths } from '@/api/generated/platformGateway.gen';
import type { Finding, RuleRevision } from './diagnostics-model';

export class DiagnosticsRequestError extends Error {
  constructor(readonly status: number, message: string) {
    super(message);
  }
}

export const diagnosticsKeys = {
  findings: (siteId: string) => ['fdd', siteId, 'findings'] as const,
  rule: (siteId: string) => ['fdd', siteId, 'rules', 'low-delta-t'] as const,
};

const accept = { Accept: 'application/json, application/problem+json' };

/** The Site's most recent findings, newest evaluation first. */
export function siteFindingsQuery(siteId: string) {
  return queryOptions({
    queryKey: diagnosticsKeys.findings(siteId),
    queryFn: async ({ signal }): Promise<Finding[]> => {
      const response = await fetch(`${intelligencePaths.siteFddFindings.replace('{siteId}', siteId)}?limit=100`, {
        credentials: 'same-origin', signal, headers: accept,
      });
      const payload = await response.json().catch(() => null);
      if (!response.ok) throw new DiagnosticsRequestError(response.status, payload?.detail ?? '诊断服务暂时不可用');
      return payload.items as Finding[];
    },
    retry: false,
  });
}

/** The low-delta-T rule revision FDD evaluates now, as its owner holds it. */
export function lowDeltaTRuleQuery(siteId: string) {
  return queryOptions({
    queryKey: diagnosticsKeys.rule(siteId),
    queryFn: async ({ signal }): Promise<RuleRevision> => {
      const response = await fetch(intelligencePaths.siteFddLowDeltaTRule.replace('{siteId}', siteId), { credentials: 'same-origin', signal, headers: accept });
      const payload = await response.json().catch(() => null);
      if (!response.ok) throw new DiagnosticsRequestError(response.status, '诊断规则暂不可用');
      return payload as RuleRevision;
    },
    retry: false,
  });
}

/** How much of the window the plant operated and how much of that was low. */
export interface EvaluationWindow {
  readonly minutes: number;
  readonly operatingMinutes: number;
  readonly lowMinutes: number;
  readonly operatingShare: number;
  readonly lowShare: number;
}

type Outcome<Status extends string> = { readonly status: Status; readonly observedValue: number; readonly rule: RuleRevision; readonly window: EvaluationWindow };

export type EvaluationOutcome =
  | Outcome<'CLEAR'>
  // The plant did not operate for enough of the window; FDD does not judge its delta-T.
  | Outcome<'NOT_OPERATING'>
  // The condition already has a finding from the last day; it is still there, not new.
  | (Outcome<'ONGOING'> & { readonly finding: Finding })
  | (Outcome<'FINDING'> & { readonly finding: Finding });

/** Diagnoses the loop now; FDD reads its last window under the rule revision in effect. */
export async function evaluateRule(
  siteId: string,
  csrfToken: string,
  target: { readonly deviceId: string; readonly assetId: string },
): Promise<EvaluationOutcome> {
  const response = await fetch(intelligencePaths.siteFddLowDeltaT.replace('{siteId}', siteId), {
    method: 'POST',
    credentials: 'same-origin',
    headers: { ...accept, 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken },
    body: JSON.stringify({ assetId: target.assetId, deviceId: target.deviceId }),
  });
  const payload = await response.json().catch(() => null);
  if (!response.ok) {
    // The owner declines, among other reasons, when the window holds no good readings.
    throw new DiagnosticsRequestError(response.status, response.status === 502
      ? '诊断未完成：诊断窗口内缺少可用的供回水温度，或诊断规则暂不可用'
      : payload?.detail ?? '诊断服务暂时不可用');
  }
  const rule = payload.rule as RuleRevision;
  const window = payload.window as EvaluationWindow;
  return payload.status === 'CLEAR' || payload.status === 'NOT_OPERATING'
    ? { status: payload.status, observedValue: payload.deltaTC, rule, window }
    : { status: payload.status, observedValue: payload.deltaTC, rule, window, finding: payload.finding as Finding };
}
