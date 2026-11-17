import {
  PlatformApiError,
  createPlatformGatewayClient,
  type ConcentratorEidUpdateRequest,
  type ConcentratorParamsUpdateRequest,
} from '@/api/generated/platformGateway.gen';

const client = createPlatformGatewayClient();
export const gatewayCredentialKey = (gatewayId: string) => ['gateway-credential', gatewayId] as const;
export const gatewayCredentialListKey = (gatewayId: string) => ['gateway-credential-list', gatewayId] as const;
export const gatewayCredentialApi = {
  register: async (siteId: string, values: { code: string; displayName: string; reason: string }) => (await client.createSiteDevice(siteId, {
    code: values.code, displayName: values.displayName, deviceType: 'GATEWAY', status: 'ACTIVE',
    meta: { expectedRevision: 0, reason: values.reason, idempotencyKey: crypto.randomUUID() },
  })).data,
  status: async (gatewayId: string, signal: AbortSignal) => (await client.getGatewayCredential(gatewayId, { signal })).data,
  listCredentials: async (gatewayId: string, signal?: AbortSignal) => (await client.listGatewayCredentials(gatewayId, { signal })).data,
  generateCode: async (gatewayId: string, csrfToken: string) => (await client.generateGatewayEnrollmentCode(gatewayId, csrfToken)).data,
  rotate: async (gatewayId: string, csrfToken: string) => (await client.rotateGatewayEnrollment(gatewayId, csrfToken)).data,
  revoke: async (gatewayId: string, csrfToken: string) => (await client.revokeGatewayCredential(gatewayId, csrfToken)).data,
  revokeCertificate: async (
    gatewayId: string,
    credentialId: string,
    reason: string | undefined,
    csrfToken: string,
  ) => {
    const trimmed = reason?.trim();
    return (await client.revokeGatewayCertificate(
      gatewayId,
      credentialId,
      trimmed ? { reason: trimmed } : {},
      csrfToken,
    )).data;
  },
};

export const gatewayConcentratorStatusKey = (gatewayId: string, unitId: number) =>
  ['gateway-concentrator-status', gatewayId, unitId] as const;

export const gatewayConcentratorEidsKey = (gatewayId: string, unitId: number) =>
  ['gateway-concentrator-eids', gatewayId, unitId] as const;

export const gatewayConcentratorApi = {
  status: async (gatewayId: string, unitId: number, signal?: AbortSignal) =>
    (await client.getGatewayConcentratorStatus(gatewayId, unitId, { signal })).data,
  updateParams: async (
    gatewayId: string,
    unitId: number,
    body: ConcentratorParamsUpdateRequest,
    csrfToken: string,
  ) => (await client.updateGatewayConcentratorParams(gatewayId, unitId, body, csrfToken)).data,
  eids: async (gatewayId: string, unitId: number, signal?: AbortSignal) =>
    (await client.getGatewayConcentratorEids(gatewayId, unitId, { signal })).data,
  updateEids: async (
    gatewayId: string,
    unitId: number,
    body: ConcentratorEidUpdateRequest,
    csrfToken: string,
  ) => (await client.updateGatewayConcentratorEids(gatewayId, unitId, body, csrfToken)).data,
};

export const gatewayQuarantineKey = (gatewayId: string) =>
  ['gateway-quarantine', gatewayId] as const;

export const gatewayPacketCaptureKey = (gatewayId: string) =>
  ['gateway-packet-capture', gatewayId] as const;

const DIAGNOSTICS_MESSAGES: Readonly<Record<string, string>> = {
  CREDENTIAL_FORBIDDEN: '当前账号缺少设备读取或抓包操作权限。',
  GATEWAY_NOT_FOUND: '未找到所选现场网关，请确认网关登记状态。',
  GATEWAY_REVOKED: '该现场网关已停用，请先重新启用后再执行抓包。',
  PACKET_CAPTURE_UNAVAILABLE: '抓包诊断通道暂未就绪，请稍后重试。',
  PACKET_CAPTURE_FORBIDDEN: '当前账号没有执行通信抓包的权限。',
  QUARANTINE_UNAVAILABLE: '接收隔离记录暂时读不到，请稍后重试。',
};

export function describeGatewayDiagnosticsError(error: unknown, fallback: string): string {
  if (error instanceof PlatformApiError) {
    const matched = DIAGNOSTICS_MESSAGES[error.problem.code];
    if (matched) return matched;
    if (error.problem.status === 403) return '当前账号缺少设备读取或抓包操作权限。';
    if (error.problem.status === 404) return '未找到所选现场网关，请确认网关登记状态。';
  }
  return fallback;
}

export const gatewayDiagnosticsApi = {
  listQuarantine: async (gatewayId: string, signal?: AbortSignal) =>
    (await client.listGatewayQuarantine(gatewayId, { limit: 20 }, { signal })).data,
  getPacketCapture: async (gatewayId: string, signal?: AbortSignal) =>
    (await client.getGatewayPacketCapture(gatewayId, { signal })).data,
  startPacketCapture: async (gatewayId: string, durationSeconds: number, csrfToken: string) =>
    (await client.startGatewayPacketCapture(gatewayId, { durationSeconds }, csrfToken)).data,
  stopPacketCapture: async (gatewayId: string, csrfToken: string) =>
    (await client.stopGatewayPacketCapture(gatewayId, csrfToken)).data,
};
