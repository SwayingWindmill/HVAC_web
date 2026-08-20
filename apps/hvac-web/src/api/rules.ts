import { useQuery } from '@tanstack/react-query';
import { ZodError } from 'zod';
import { API_MODE } from './config';
import {
  PlatformApiError,
  createPlatformGatewayClient,
  type RuleAssignmentRequest,
  type RuleBinding,
  type RuleDraft,
  type RuleRetirementRequest,
  type RuleRevision,
  type CurrentPrincipalResponse,
  type RuleSimulationRequest,
  type RuleSimulationResult,
  type RuleValidationResult,
} from './generated/platformGateway.gen';

const client = createPlatformGatewayClient();
type PrincipalSession = CurrentPrincipalResponse['session'];

export type RuleErrorKind = 'forbidden' | 'not-found' | 'invalid' | 'conflict' | 'unavailable' | 'unknown';

export interface RuleErrorPresentation {
  kind: RuleErrorKind;
  title: string;
  description: string;
  retryable: boolean;
  traceId?: string;
}

const enabledInRealMode = (enabled: boolean) => API_MODE === 'real' && enabled;
const authorizationScope = (tenantId: string, sessionId: string, policyRevision: string) => `${tenantId}:${sessionId}:${policyRevision}`;
const mutationInit = (session: PrincipalSession): RequestInit => ({ headers: { 'X-CSRF-Token': session.csrfToken } });

const retryRuleQuery = (failureCount: number, error: Error) => {
  if (failureCount >= 1 || error instanceof ZodError) return false;
  if (!(error instanceof PlatformApiError)) return true;
  return error.problem.retryable;
};

export function presentRuleError(error: unknown): RuleErrorPresentation {
  if (error instanceof ZodError) {
    return { kind: 'invalid', title: '规则请求无效', description: '规则草稿或资源标识没有通过生成契约校验。', retryable: false };
  }
  if (error instanceof PlatformApiError) {
    const { code, detail, retryable, traceId } = error.problem;
    switch (code) {
      case 'RULE_MANAGEMENT_FORBIDDEN':
        return { kind: 'forbidden', title: '没有规则管理权限', description: detail, retryable: false, traceId };
      case 'RESOURCE_NOT_FOUND':
        return { kind: 'not-found', title: '规则资源不可见或不存在', description: detail, retryable: false, traceId };
      case 'RULE_REQUEST_INVALID':
      case 'RULE_VALIDATION_FAILED':
        return { kind: 'invalid', title: '规则校验失败', description: detail, retryable: false, traceId };
      case 'RULE_REVISION_CONFLICT':
        return { kind: 'conflict', title: '规则生命周期已变化', description: detail, retryable: false, traceId };
      case 'RULE_RUNTIME_UNAVAILABLE':
        return { kind: 'unavailable', title: 'Rule Runtime 暂不可用', description: detail, retryable, traceId };
      default:
        return { kind: 'unknown', title: '规则请求失败', description: detail, retryable, traceId };
    }
  }
  return { kind: 'unavailable', title: 'Rule Runtime 连接失败', description: '真实模式不会回退到浏览器规则或 Demo 规则。', retryable: true };
}

export function useRuleCatalog(tenantId: string, sessionId: string, policyRevision: string, enabled = true) {
  const scope = authorizationScope(tenantId, sessionId, policyRevision);
  return useQuery({
    queryKey: ['rule-management', 'catalog', scope],
    queryFn: async ({ signal }) => (await client.getRuleCatalog({ signal })).data,
    enabled: enabledInRealMode(enabled),
    retry: retryRuleQuery,
  });
}

export function useRuleRevisions(tenantId: string, sessionId: string, policyRevision: string, ruleId?: string, enabled = true) {
  const scope = authorizationScope(tenantId, sessionId, policyRevision);
  return useQuery({
    queryKey: ['rule-management', 'revisions', scope, ruleId ?? 'all'],
    queryFn: async ({ signal }) => (await client.listRuleRevisions(ruleId, { signal })).data.items,
    enabled: enabledInRealMode(enabled),
    retry: retryRuleQuery,
  });
}

export function useRuleBindings(tenantId: string, sessionId: string, policyRevision: string, siteId: string | null, enabled = true) {
  const scope = authorizationScope(tenantId, sessionId, policyRevision);
  return useQuery({
    queryKey: ['rule-management', 'bindings', scope, siteId],
    queryFn: async ({ signal }) => (await client.listRuleBindings(siteId!, { signal })).data.items,
    enabled: enabledInRealMode(enabled && Boolean(siteId)),
    retry: retryRuleQuery,
  });
}

export function useRuleExecutionEvidence(tenantId: string, sessionId: string, policyRevision: string, siteId: string | null, enabled = true) {
  const scope = authorizationScope(tenantId, sessionId, policyRevision);
  return useQuery({
    queryKey: ['rule-management', 'evidence', scope, siteId],
    queryFn: async ({ signal }) => (await client.listRuleExecutionEvidence({ siteId: siteId!, limit: 50 }, { signal })).data.items,
    enabled: enabledInRealMode(enabled && Boolean(siteId)),
    retry: retryRuleQuery,
  });
}

export const ruleManagementApi = {
  validate: async (draft: RuleDraft, session: PrincipalSession): Promise<RuleValidationResult> =>
    (await client.validateRuleDraft(draft, mutationInit(session))).data,
  simulate: async (request: RuleSimulationRequest, session: PrincipalSession): Promise<RuleSimulationResult> =>
    (await client.simulateRuleDraft(request, mutationInit(session))).data,
  release: async (draft: RuleDraft, session: PrincipalSession): Promise<RuleRevision> =>
    (await client.releaseRuleRevision(draft, mutationInit(session))).data,
  assign: async (request: RuleAssignmentRequest, session: PrincipalSession): Promise<RuleBinding> =>
    (await client.assignRuleRevision(request, mutationInit(session))).data,
  retire: async (bindingId: string, request: RuleRetirementRequest, session: PrincipalSession): Promise<RuleBinding> =>
    (await client.retireRuleBinding(bindingId, request, mutationInit(session))).data,
};
