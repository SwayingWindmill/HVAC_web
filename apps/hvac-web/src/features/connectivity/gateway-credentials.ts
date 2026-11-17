import {
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
