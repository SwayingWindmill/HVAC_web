import { queryOptions } from '@tanstack/react-query';
import { failureStatus, gatewayPath, gatewayRequest, ROUTE_MISSING_MESSAGE } from '@/api/gateway-client';
import {
  assetControlLockMutationResponseSchema,
  controlExitPaths,
  siteControlModeMutationResponseSchema,
  siteControlShutdownResponseSchema,
  siteControlStateSchema,
  type ActiveMaintenanceWindow,
  type AssetControlLockMode,
  type AssetControlLockMutationResponse,
  type AssetControlLockState,
  type SiteControlMode,
  type SiteControlModeMutationResponse,
  type SiteControlShutdownRequest,
  type SiteControlShutdownResponse,
  type SiteControlState,
} from '@/api/generated/platformGateway.gen';
import { CommandRequestError } from './command-api';
import { controlErrorMessage } from './control-errors';

// Who may command the plant right now: the Site's control mode, and each asset's own lock.
// Leaving remote auto control, or locking an asset, withdraws the commands not yet dispatched.

export type { ActiveMaintenanceWindow, AssetControlLockMode, AssetControlLockState, SiteControlMode, SiteControlState };

/** During a maintenance window from the operating schedule the gateway takes only these commands. */
export const MAINTENANCE_PERMITTED: ReadonlySet<string> = new Set(['STOP', 'STOP_UNIT_SEQUENCE', 'RESET_FAULT']);
export const MAINTENANCE_BLOCKED = '检修窗口内只能停机和复位';

export const MODE_PRESENTATION: Readonly<Record<SiteControlMode, {
  readonly label: string;
  readonly short: string;
  readonly hint: string;
  readonly glyph: string;
  readonly tone: 'success' | 'warning' | 'destructive';
}>> = {
  REMOTE_AUTO: { label: '远程自动控制', short: '远程自动', hint: '平台按审批规则下发指令', glyph: '●', tone: 'success' },
  MANUAL_OVERRIDE: { label: '人工接管', short: '人工接管', hint: '平台暂停下发，现场手动操作', glyph: '▲', tone: 'warning' },
  SAFE_SHUTDOWN_EXIT: { label: '安全退出', short: '安全退出', hint: '停掉一切远程控制，交回现场', glyph: '◆', tone: 'destructive' },
};

export const MODE_ORDER = ['REMOTE_AUTO', 'MANUAL_OVERRIDE', 'SAFE_SHUTDOWN_EXIT'] as const satisfies readonly SiteControlMode[];

export const LOCK_PRESENTATION: Readonly<Record<Exclude<AssetControlLockMode, 'UNLOCKED'>, { readonly label: string; readonly hint: string }>> = {
  MANUAL_LOCK: { label: '手动锁定', hint: '现场临时手动操作，平台不下发、不给停机建议' },
  MAINTENANCE_LOCK: { label: '检修锁定', hint: '设备在检修，长时间停用' },
};

export const REASON_LIMIT = 1000;

export const controlStateKeys = {
  site: (siteId: string) => ['control-state', siteId] as const,
};

async function send<T>(path: string, request: Parameters<typeof gatewayRequest<T>>[1]): Promise<T> {
  const result = await gatewayRequest<T>(path, request);
  if (result.kind === 'ok') return result.value;
  if (result.kind === 'routeMissing') throw new CommandRequestError(result.status, undefined, ROUTE_MISSING_MESSAGE);
  const code = result.kind === 'unavailable' ? undefined : result.problem.code;
  throw new CommandRequestError(failureStatus(result), code, controlErrorMessage(code, result.kind === 'unavailable' ? '控制状态暂时读不到，请稍后再试' : '没有保存成功，请刷新后再试'));
}

/** The Site's control mode and asset locks; a missing route means this deployment has no control boundary yet. */
export function siteControlStateQuery(siteId: string) {
  return queryOptions({
    queryKey: controlStateKeys.site(siteId),
    queryFn: ({ signal }) => send<SiteControlState>(gatewayPath(controlExitPaths.siteControlState, { siteId }), { signal, parse: (payload) => siteControlStateSchema.parse(payload) }),
    refetchInterval: 30_000,
    retry: false,
  });
}

export function updateSiteControlMode(siteId: string, input: { readonly controlMode: SiteControlMode; readonly reason: string; readonly expectedRevision: number }, csrfToken: string): Promise<SiteControlModeMutationResponse> {
  return send(gatewayPath(controlExitPaths.siteControlMode, { siteId }), {
    method: 'PUT',
    csrfToken,
    body: input,
    parse: (payload) => siteControlModeMutationResponseSchema.parse(payload),
  });
}

export function updateAssetControlLock(assetId: string, input: { readonly lockMode: AssetControlLockMode; readonly reason: string; readonly expectedRevision?: number }, csrfToken: string): Promise<AssetControlLockMutationResponse> {
  return send(gatewayPath(controlExitPaths.assetControlLock, { assetId }), {
    method: 'PUT',
    csrfToken,
    body: input,
    parse: (payload) => assetControlLockMutationResponseSchema.parse(payload),
  });
}

/**
 * Stops the plant from the platform: the whole plant in order (chillers, then the pumps and towers
 * they hold once each chiller is confirmed stopped), every output channel at once, or one unit's
 * running sequence. The Idempotency-Key stays the same for one confirmation, so a retry never stops twice.
 */
export function executeSiteShutdown(siteId: string, input: SiteControlShutdownRequest, idempotencyKey: string, csrfToken: string): Promise<SiteControlShutdownResponse> {
  return send(gatewayPath(controlExitPaths.siteControlShutdown, { siteId }), {
    method: 'POST',
    csrfToken,
    idempotencyKey,
    body: input,
    parse: (payload) => siteControlShutdownResponseSchema.parse(payload),
  });
}

/** The asset's lock when it is locked; unlocked rows are not locks. */
export function activeLocks(state: SiteControlState | undefined): ReadonlyMap<string, AssetControlLockState> {
  return new Map((state?.assetLocks ?? []).filter((lock) => lock.lockMode !== 'UNLOCKED').map((lock) => [lock.assetId, lock]));
}
