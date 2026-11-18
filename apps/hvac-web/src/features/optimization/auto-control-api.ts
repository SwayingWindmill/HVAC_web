import { queryOptions } from '@tanstack/react-query';
import {
  failureStatus,
  gatewayPath,
  gatewayRequest,
  ROUTE_MISSING_MESSAGE,
  type GatewayRequest,
  type StepUpFailureReason,
} from '@/api/gateway-client';
import {
  armAutoControlLeaseRequestSchema,
  intelligencePaths,
  optimizationExecutionMonitorListSchema,
  revokeAutoControlLeaseRequestSchema,
  siteAutoControlLeaseStateSchema,
  type ArmAutoControlLeaseRequest,
  type AutoControlLease,
  type OptimizationExecutionMonitor,
  type OptimizationExecutionMonitorList,
  type RevokeAutoControlLeaseRequest,
  type SiteAutoControlLeaseState,
} from '@/api/generated/platformGateway.gen';

export type {
  ArmAutoControlLeaseRequest,
  AutoControlLease,
  OptimizationExecutionMonitor,
  OptimizationExecutionMonitorList,
  RevokeAutoControlLeaseRequest,
  SiteAutoControlLeaseState,
};

const AUTO_CONTROL_ERROR_MESSAGE: Readonly<Record<string, string>> = {
  STEP_UP_REQUIRED: '开启限时自动控制租约需要先完成两步验证。',
  CONTROL_EXIT_FORBIDDEN: '你没有开启或撤回自动控制租约的权限，请联系管理员。',
  AUTO_CONTROL_LEASE_REQUEST_INVALID: '填写的租约边界参数或原因不完整，请核对后重试。',
  REGISTRY_MUTATION_INVALID: '填写的租约边界参数超出允许范围，请核对后重试。',
  REGISTRY_REVISION_CONFLICT: '刚才有人修改过自动控制租约，已刷新最新状态，请确认后再试。',
  REGISTRY_IDEMPOTENCY_CONFLICT: '同一条租约请求重复提交了，请刷新查看最新状态。',
  RESOURCE_NOT_FOUND: '当前站点没有生效中的自动控制租约。',
  REGISTRY_UNAVAILABLE: '自动控制租约服务暂时不可用，请稍后再试。',
  REGISTRY_TIMEOUT: '自动控制租约服务响应超时，请稍后再试。',
  COMMAND_UNAVAILABLE: '指令服务暂时不可用，请稍后再试。',
};

export class AutoControlRequestError extends Error {
  constructor(
    readonly status: number,
    readonly code: string | undefined,
    message: string,
    readonly reason?: StepUpFailureReason,
  ) {
    super(message);
    this.name = 'AutoControlRequestError';
  }
}

function errorMessageForCode(code: string | undefined, fallback: string): string {
  if (code !== undefined && Object.prototype.hasOwnProperty.call(AUTO_CONTROL_ERROR_MESSAGE, code)) {
    return AUTO_CONTROL_ERROR_MESSAGE[code] ?? fallback;
  }
  return fallback;
}

async function send<T>(path: string, request: GatewayRequest<T>): Promise<T> {
  const result = await gatewayRequest<T>(path, request);
  if (result.kind === 'ok') return result.value;
  if (result.kind === 'routeMissing') {
    throw new AutoControlRequestError(result.status, undefined, ROUTE_MISSING_MESSAGE);
  }
  const code = result.kind === 'unavailable' ? undefined : result.problem.code;
  const reason = result.kind === 'unavailable' ? undefined : result.problem.reason;
  const fallback = result.kind === 'unavailable'
    ? '自动控制租约服务暂时不可用，请稍后再试'
    : '自动控制租约操作未能完成，请刷新后重试';
  throw new AutoControlRequestError(
    failureStatus(result),
    code,
    errorMessageForCode(code, fallback),
    reason,
  );
}

export const autoControlKeys = {
  lease: (siteId: string) => ['optimization', siteId, 'auto-control-lease'] as const,
  monitors: (siteId: string) => ['optimization', siteId, 'execution-monitors'] as const,
};

export function siteAutoControlLeaseQuery(siteId: string) {
  return queryOptions({
    queryKey: autoControlKeys.lease(siteId),
    queryFn: ({ signal }): Promise<SiteAutoControlLeaseState> => send(
      gatewayPath(intelligencePaths.autoControlLease, { siteId }),
      {
        signal,
        parse: (payload) => siteAutoControlLeaseStateSchema.parse(payload),
      },
    ),
    refetchInterval: 30_000,
    retry: false,
  });
}

export function siteExecutionMonitorsQuery(siteId: string) {
  return queryOptions({
    queryKey: autoControlKeys.monitors(siteId),
    queryFn: ({ signal }): Promise<OptimizationExecutionMonitorList> => send(
      gatewayPath(intelligencePaths.siteOptimizationExecutionMonitors, { siteId }),
      {
        signal,
        parse: (payload) => optimizationExecutionMonitorListSchema.parse(payload),
      },
    ),
    refetchInterval: (query) => (query.state.data?.items.some((item) => item.status === 'OBSERVING') ? 10_000 : 30_000),
    retry: false,
  });
}

export function armSiteAutoControlLease(
  siteId: string,
  input: ArmAutoControlLeaseRequest,
  csrfToken: string,
): Promise<SiteAutoControlLeaseState> {
  const validated = armAutoControlLeaseRequestSchema.parse(input);
  return send(gatewayPath(intelligencePaths.autoControlLease, { siteId }), {
    method: 'PUT',
    csrfToken,
    body: validated,
    parse: (payload) => siteAutoControlLeaseStateSchema.parse(payload),
  });
}

export function revokeSiteAutoControlLease(
  siteId: string,
  input: RevokeAutoControlLeaseRequest,
  csrfToken: string,
): Promise<SiteAutoControlLeaseState> {
  const validated = revokeAutoControlLeaseRequestSchema.parse(input);
  return send(gatewayPath(intelligencePaths.autoControlLeaseRevoke, { siteId }), {
    method: 'POST',
    csrfToken,
    body: validated,
    parse: (payload) => siteAutoControlLeaseStateSchema.parse(payload),
  });
}
