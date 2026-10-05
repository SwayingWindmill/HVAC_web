import { createPlatformGatewayClient } from '@/api/generated/platformGateway.gen';

const client = createPlatformGatewayClient();
export const gatewayCredentialKey = (gatewayId: string) => ['gateway-credential', gatewayId] as const;
export const gatewayCredentialApi = {
  register: async (siteId: string, values: { code: string; displayName: string; reason: string }) => (await client.createSiteDevice(siteId, {
    code: values.code, displayName: values.displayName, deviceType: 'GATEWAY', status: 'ACTIVE',
    meta: { expectedRevision: 0, reason: values.reason, idempotencyKey: crypto.randomUUID() },
  })).data,
  status: async (gatewayId: string, signal: AbortSignal) => (await client.getGatewayCredential(gatewayId, { signal })).data,
  generateCode: async (gatewayId: string, csrfToken: string) => (await client.generateGatewayEnrollmentCode(gatewayId, csrfToken)).data,
  revoke: async (gatewayId: string, csrfToken: string) => (await client.revokeGatewayCredential(gatewayId, csrfToken)).data,
};
