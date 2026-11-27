import { queryOptions } from '@tanstack/react-query';
import { failureStatus, gatewayPath, gatewayRequest, ROUTE_MISSING_MESSAGE } from '@/api/gateway-client';
import {
  assetControlLockMutationResponseSchema,
  controlAlarmDeadLetterListEnvelopeSchema,
  controlAlarmDeadLetterReplayResponseSchema,
  controlExitPaths,
  devicePriorityLockReleaseResponseSchema,
  devicePriorityLockStateSchema,
  siteControlModeMutationResponseSchema,
  siteControlShutdownResponseSchema,
  siteControlStateSchema,
  type ActiveMaintenanceWindow,
  type AssetControlLockMode,
  type AssetControlLockMutationResponse,
  type AssetControlLockState,
  type ControlAlarmDeadLetter,
  type ControlAlarmDeadLetterReplayResponse,
  type DevicePriorityLockMutation,
  type DevicePriorityLockReleaseResponse,
  type DevicePriorityLockState,
  type SiteControlMode,
  type SiteControlModeMutationResponse,
  type SiteControlShutdownRequest,
  type SiteControlShutdownResponse,
  type SiteControlState,
} from '@/api/generated/platformGateway.gen';
import { CommandRequestError } from './command-api';
import {
  controlActorLabel,
  LOCK_PRESENTATION,
  MAINTENANCE_BLOCKED,
  MAINTENANCE_PERMITTED,
  MODE_ORDER,
  MODE_PRESENTATION,
  REASON_LIMIT,
} from './control-constants';
import { controlErrorMessage } from './control-errors';

// Who may command the plant right now: the Site's control mode, and each asset's own lock.
// Leaving remote auto control, or locking an asset, withdraws the commands not yet dispatched.

export type {
  ActiveMaintenanceWindow,
  AssetControlLockMode,
  AssetControlLockState,
  ControlAlarmDeadLetter,
  ControlAlarmDeadLetterReplayResponse,
  DevicePriorityLockMutation,
  DevicePriorityLockReleaseResponse,
  DevicePriorityLockState,
  SiteControlMode,
  SiteControlState,
};

export {
  controlActorLabel,
  LOCK_PRESENTATION,
  MAINTENANCE_BLOCKED,
  MAINTENANCE_PERMITTED,
  MODE_ORDER,
  MODE_PRESENTATION,
  REASON_LIMIT,
};

export const controlStateKeys = {
  site: (siteId: string) => ['control-state', siteId] as const,
};

async function send<T>(path: string, request: Parameters<typeof gatewayRequest<T>>[1]): Promise<T> {
  const result = await gatewayRequest<T>(path, request);
  if (result.kind === 'ok') return result.value;
  if (result.kind === 'routeMissing') throw new CommandRequestError(result.status, undefined, ROUTE_MISSING_MESSAGE);
  const code = result.kind === 'unavailable' ? undefined : result.problem.code;
  const reason = result.kind === 'unavailable' ? undefined : result.problem.reason;
  throw new CommandRequestError(failureStatus(result), code, controlErrorMessage(code, result.kind === 'unavailable' ? '控制状态暂时读不到，请稍后再试' : '没有保存成功，请刷新后再试'), reason);
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

export {
  activePriorityLocks,
  CONTROL_ALARM_EVENT_LABEL,
  describeControlAlarmError,
  describeControlAlarmEvent,
  DEVICE_PRIORITY_TIER_LABEL,
  formatLockRemaining,
  MUTABLE_PRIORITY_TIERS,
  type DevicePriorityTier,
  type MutableDevicePriorityTier,
} from './control-priority-lock';

export const controlAlarmDeadLetterKeys = {
  site: (siteId: string, limit = 20) => ['control-alarm-dead-letters', siteId, limit] as const,
};

export function controlAlarmDeadLettersQuery(siteId: string, limit = 20) {
  return queryOptions({
    queryKey: controlAlarmDeadLetterKeys.site(siteId, limit),
    queryFn: async ({ signal }) => {
      const basePath = gatewayPath(controlExitPaths.controlAlarmDeadLetters, { siteId });
      const query = new URLSearchParams({ limit: String(limit) });
      const body = await send(
        `${basePath}?${query.toString()}`,
        {
          signal,
          parse: (payload) => controlAlarmDeadLetterListEnvelopeSchema.parse(payload),
        },
      );
      return body.data;
    },
    refetchInterval: 30_000,
  });
}

export function putDevicePriorityLock(
  siteId: string,
  deviceId: string,
  input: DevicePriorityLockMutation,
  csrfToken: string,
): Promise<DevicePriorityLockState> {
  return send(gatewayPath(controlExitPaths.devicePriorityLock, { siteId, deviceId }), {
    method: 'PUT',
    csrfToken,
    body: input,
    parse: (payload) => devicePriorityLockStateSchema.parse(payload),
  });
}

export function deleteDevicePriorityLock(
  siteId: string,
  deviceId: string,
  reason: string,
  csrfToken: string,
): Promise<DevicePriorityLockReleaseResponse> {
  return send(gatewayPath(controlExitPaths.devicePriorityLock, { siteId, deviceId }), {
    method: 'DELETE',
    csrfToken,
    body: { reason: reason.trim() },
    parse: (payload) => devicePriorityLockReleaseResponseSchema.parse(payload),
  });
}

export function replayControlAlarmDeadLetter(
  siteId: string,
  outboxId: string,
  csrfToken: string,
): Promise<ControlAlarmDeadLetterReplayResponse> {
  return send(
    gatewayPath(controlExitPaths.controlAlarmDeadLetterReplay, { siteId, outboxId }),
    {
      method: 'POST',
      csrfToken,
      body: {},
      parse: (payload) => controlAlarmDeadLetterReplayResponseSchema.parse(payload),
    },
  );
}
