import { queryOptions } from '@tanstack/react-query';
import { intelligencePaths } from '@/api/generated/platformGateway.gen';
import type { DiagnosticRule, Finding } from './diagnostics-model';

export class DiagnosticsRequestError extends Error {
  constructor(readonly status: number, message: string) {
    super(message);
  }
}

export const diagnosticsKeys = {
  findings: (siteId: string) => ['fdd', siteId, 'findings'] as const,
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

export type EvaluationOutcome =
  | { readonly status: 'CLEAR'; readonly observedValue: number }
  // The condition already has a finding from the last day; it is still there, not new.
  | { readonly status: 'ONGOING'; readonly observedValue: number; readonly finding: Finding }
  | { readonly status: 'FINDING'; readonly observedValue: number; readonly finding: Finding };

/** Runs a rule now over the last window of the target's authoritative history. */
export async function evaluateRule(
  siteId: string,
  csrfToken: string,
  rule: DiagnosticRule,
  target: { readonly deviceId: string; readonly assetId: string },
  at: Date,
): Promise<EvaluationOutcome> {
  const response = await fetch(intelligencePaths.siteFddLowDeltaT.replace('{siteId}', siteId), {
    method: 'POST',
    credentials: 'same-origin',
    headers: { ...accept, 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken },
    body: JSON.stringify({
      assetId: target.assetId,
      deviceId: target.deviceId,
      evaluationFrom: new Date(at.getTime() - rule.windowMinutes * 60_000).toISOString(),
      evaluationTo: at.toISOString(),
      ruleRevisionId: rule.revisionId,
      minimumDeltaTC: rule.threshold,
    }),
  });
  const payload = await response.json().catch(() => null);
  if (!response.ok) {
    // The owner declines, among other reasons, when the window holds no good readings.
    throw new DiagnosticsRequestError(response.status, response.status === 502
      ? `诊断未完成：最近 ${rule.windowMinutes} 分钟缺少可用的供回水温度，或诊断服务拒绝了请求`
      : payload?.detail ?? '诊断服务暂时不可用');
  }
  return payload.status === 'CLEAR'
    ? { status: 'CLEAR', observedValue: payload.deltaTC }
    : { status: payload.status, observedValue: payload.deltaTC, finding: payload.finding as Finding };
}
