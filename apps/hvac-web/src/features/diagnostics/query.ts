import { queryOptions } from '@tanstack/react-query';
import { gatewayPath, gatewayRequest, valueOf } from '@/api/gateway-client';
import { intelligencePaths } from '@/api/generated/platformGateway.gen';
import type { Finding, RuleRevision } from './diagnostics-model';

export const diagnosticsKeys = {
  findings: (siteId: string) => ['fdd', siteId, 'findings'] as const,
  rule: (siteId: string) => ['fdd', siteId, 'rules', 'low-delta-t'] as const,
};

const UNAVAILABLE = '诊断服务暂时不可用';

/** The Site's most recent findings, newest evaluation first. */
export function siteFindingsQuery(siteId: string) {
  return queryOptions({
    queryKey: diagnosticsKeys.findings(siteId),
    queryFn: async ({ signal }): Promise<Finding[]> => valueOf(
      await gatewayRequest(`${gatewayPath(intelligencePaths.siteFddFindings, { siteId })}?limit=100`, {
        signal, parse: (payload) => (payload as { items: Finding[] }).items,
      }),
      { unavailable: UNAVAILABLE },
    ),
    retry: false,
  });
}

/** The low-delta-T rule revision FDD evaluates now, as its owner holds it. */
export function lowDeltaTRuleQuery(siteId: string) {
  return queryOptions({
    queryKey: diagnosticsKeys.rule(siteId),
    queryFn: async ({ signal }): Promise<RuleRevision> => valueOf(
      await gatewayRequest<RuleRevision>(gatewayPath(intelligencePaths.siteFddLowDeltaTRule, { siteId }), { signal }),
      { unavailable: '诊断规则暂不可用', refused: () => '诊断规则暂不可用' },
    ),
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

type EvaluationPayload = { status: EvaluationOutcome['status']; deltaTC: number; rule: RuleRevision; window: EvaluationWindow; finding?: Finding };

/** Diagnoses the loop now; FDD reads its last window under the rule revision in effect. */
export async function evaluateRule(
  siteId: string,
  csrfToken: string,
  target: { readonly deviceId: string; readonly assetId: string },
): Promise<EvaluationOutcome> {
  const payload = valueOf(
    await gatewayRequest<EvaluationPayload>(gatewayPath(intelligencePaths.siteFddLowDeltaT, { siteId }), {
      method: 'POST', csrfToken, body: { assetId: target.assetId, deviceId: target.deviceId },
    }),
    {
      unavailable: UNAVAILABLE,
      // The owner declines, among other reasons, when the window holds no good readings.
      refused: (problem) => problem.status === 502
        ? '诊断未完成：诊断窗口内缺少可用的供回水温度，或诊断规则暂不可用'
        : problem.detail ?? UNAVAILABLE,
    },
  );
  return payload.status === 'CLEAR' || payload.status === 'NOT_OPERATING'
    ? { status: payload.status, observedValue: payload.deltaTC, rule: payload.rule, window: payload.window }
    : { status: payload.status, observedValue: payload.deltaTC, rule: payload.rule, window: payload.window, finding: payload.finding as Finding };
}
