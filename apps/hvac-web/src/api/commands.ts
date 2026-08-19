import { z } from 'zod';
import {
  CommandApiError,
  commandCapabilityProfiles,
  commandSchema,
  commandUUIDSchema,
  commandUUIDv7Schema,
  validateCommandParameters,
  validateCommandScope,
  type Command,
  type CommandCapability,
  type CommandRisk,
  type CommandStatus,
  type CommandTransition,
} from './command-contract';
import { API_MODE } from './config';
import { createPlatformGatewayClient } from './generated/platformGateway.gen';

export {
  CommandApiError,
  commandApprovalPolicySchema,
  commandRiskSchema,
  commandSchema,
  commandStatusSchema,
  commandTransitionSchema,
  validateCommandScope,
} from './command-contract';
export type { Command, CommandCapability, CommandRisk, CommandStatus, CommandTransition } from './command-contract';

export const COMMAND_PUBLIC_ROUTES_ENABLED = API_MODE === 'real';
export const COMMAND_LOCAL_ROUTES_ENABLED = API_MODE === 'real'
  && import.meta.env.DEV
  && (import.meta.env.VITE_S3_LOCAL_COMMANDS as string | undefined) === 'true';
export const COMMAND_ROUTES_AVAILABLE = COMMAND_PUBLIC_ROUTES_ENABLED || COMMAND_LOCAL_ROUTES_ENABLED;

export interface CreateCommandInput {
  assetId: string;
  commandPointId: string;
  parameters: Record<string, number>;
}

export interface ScopedCommandRequestOptions {
  trustedTenantId: string;
  trustedSiteId: string;
  csrfToken?: string;
  signal?: AbortSignal;
  fetchImplementation?: typeof fetch;
  baseUrl?: string;
  idempotencyKey?: string;
}

const platformClient = createPlatformGatewayClient();
const problemSchema = z.object({
  title: z.string().optional(),
  detail: z.string().optional(),
  code: z.string().optional(),
  retryable: z.boolean().optional(),
}).passthrough();

const mockCommands = new Map<string, Command>();
let mockSequence = 2;
const mockPendingCommandId = ['018f3e00', '4000', '7000', '8000', '000000000001'].join('-');
const mockDeviceId = ['018f3e00', '3000', '7000', '8000', '000000000001'].join('-');
const mockAssetId = ['018f3e00', '3100', '7000', '8000', '000000000001'].join('-');
const mockCommandPointId = ['018f3e00', '3200', '7000', '8000', '000000000001'].join('-');
export const MOCK_PENDING_COMMAND_ID = mockPendingCommandId;
export const MOCK_COMMAND_ASSET_ID = mockAssetId;
export const MOCK_COMMAND_POINT_ID = mockCommandPointId;

function iso(offsetSeconds = 0): string {
  return new Date(Date.now() + offsetSeconds * 1000).toISOString();
}

function buildPendingMockCommand(): Command {
  const createdAt = iso(-180);
  const validatingAt = iso(-179);
  const waitingAt = iso(-178);
  return commandSchema.parse({
    schemaVersion: 1,
    commandId: mockPendingCommandId,
    tenantId: ['018f3e00', '1000', '7000', '8000', '000000000001'].join('-'),
    siteId: ['018f3e00', '2000', '7000', '8000', '000000000001'].join('-'),
    deviceId: mockDeviceId,
    pointId: mockCommandPointId,
    capability: 'SET_TEMPERATURE_SETPOINT',
    capabilityRevision: 'capability:set-temperature-setpoint:v1',
    status: 'AWAITING_APPROVAL',
    risk: 'MEDIUM',
    approvalPolicy: 'SINGLE_APPROVER',
    approvalCount: 0,
    requiredApprovalCount: 1,
    parameters: { setpointC: 27 },
    deviceCommandSequence: 1,
    version: 3,
    snapshotRevision: 17,
    transitions: [
      { toStatus: 'SUBMITTED', reason: 'COMMAND_SUBMITTED', actorType: 'PRINCIPAL', occurredAt: createdAt, version: 1 },
      { fromStatus: 'SUBMITTED', toStatus: 'VALIDATING', reason: 'COMMAND_VALIDATING', actorType: 'WORKLOAD', occurredAt: validatingAt, version: 2 },
      { fromStatus: 'VALIDATING', toStatus: 'AWAITING_APPROVAL', reason: 'APPROVAL_REQUIRED', actorType: 'WORKLOAD', occurredAt: waitingAt, version: 3 },
    ],
    createdAt,
    updatedAt: waitingAt,
  });
}

mockCommands.set(mockPendingCommandId, buildPendingMockCommand());

function mockUuidV7(): string {
  const timestamp = Date.now().toString(16).padStart(12, '0').slice(-12);
  const random = Array.from(crypto.getRandomValues(new Uint8Array(10)), (value) => value.toString(16).padStart(2, '0')).join('');
  return `${timestamp.slice(0, 8)}-${timestamp.slice(8)}-7${random.slice(0, 3)}-8${random.slice(3, 6)}-${random.slice(6, 18)}`;
}

function ensureMockCommand(commandId: string): Command {
  const command = mockCommands.get(commandId);
  if (!command) throw new CommandApiError(404, 'RESOURCE_NOT_FOUND', '未找到该 Command。');
  return command;
}

async function csrfCapability(): Promise<string> {
  const principal = await platformClient.getCurrentPrincipal();
  const capability = principal.data.session.csrfToken;
  if (!capability) throw new CommandApiError(401, 'CSRF_REQUIRED', '认证会话没有提供 CSRF 能力。');
  return capability;
}

async function commandRequest(
  path: string,
  init: RequestInit,
  options?: Partial<ScopedCommandRequestOptions>,
): Promise<Command> {
  const fetchImplementation = options?.fetchImplementation ?? globalThis.fetch.bind(globalThis);
  const response = await fetchImplementation(`${options?.baseUrl ?? ''}${path}`, {
    ...init,
    signal: options?.signal ?? init.signal,
    credentials: 'same-origin',
    headers: {
      Accept: 'application/json, application/problem+json',
      ...Object.fromEntries(new Headers(init.headers)),
    },
  });
  const payload: unknown = await response.json().catch(() => ({}));
  if (!response.ok) {
    const problem = problemSchema.parse(payload);
    throw new CommandApiError(
      response.status,
      problem.code ?? 'COMMAND_UNAVAILABLE',
      problem.detail ?? problem.title ?? 'Command 服务暂时不可用。',
      problem.retryable ?? false,
    );
  }
  const command = commandSchema.parse(payload);
  return options?.trustedTenantId && options.trustedSiteId
    ? validateCommandScope(command, options as ScopedCommandRequestOptions)
    : command;
}

export async function getScopedCommand(
  commandId: string,
  options: ScopedCommandRequestOptions,
): Promise<Command> {
  if (!commandUUIDSchema.safeParse(commandId).success) {
    throw new CommandApiError(404, 'RESOURCE_NOT_FOUND', 'Command ID 格式无效。');
  }
  if (!COMMAND_ROUTES_AVAILABLE) {
    throw new CommandApiError(503, 'COMMAND_ROUTE_DISABLED', 'Command 控制路由已登记，但尚未启用生产流量。');
  }
  return commandRequest(`/api/v1/commands/${encodeURIComponent(commandId)}`, { method: 'GET' }, options);
}

export async function createScopedCommand(
  input: CreateCommandInput,
  options: ScopedCommandRequestOptions,
): Promise<Command> {
  const assetId = commandUUIDv7Schema.parse(input.assetId);
  const commandPointId = commandUUIDv7Schema.parse(input.commandPointId);
  const parameters = z.record(z.string(), z.number().finite()).parse(input.parameters);
  if (!COMMAND_ROUTES_AVAILABLE) {
    throw new CommandApiError(503, 'COMMAND_ROUTE_DISABLED', 'Command 控制路由已登记，但尚未启用生产流量。');
  }
  if (!options.csrfToken) {
    throw new CommandApiError(401, 'CSRF_REQUIRED', '认证会话没有提供 CSRF 能力。');
  }
  const idempotencyKey = options.idempotencyKey ?? `hvac-web-${crypto.randomUUID()}`;
  return commandRequest('/api/v1/commands', {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      'X-CSRF-Token': options.csrfToken,
      'Idempotency-Key': idempotencyKey,
    },
    body: JSON.stringify({ assetId, commandPointId, parameters }),
  }, options);
}

export async function approveScopedCommand(
  commandId: string,
  options: ScopedCommandRequestOptions,
): Promise<Command> {
  if (!commandUUIDSchema.safeParse(commandId).success) {
    throw new CommandApiError(404, 'RESOURCE_NOT_FOUND', 'Command ID 格式无效。');
  }
  if (!COMMAND_ROUTES_AVAILABLE) {
    throw new CommandApiError(503, 'COMMAND_ROUTE_DISABLED', 'Command 控制路由已登记，但尚未启用生产流量。');
  }
  if (!options.csrfToken) {
    throw new CommandApiError(401, 'CSRF_REQUIRED', '认证会话没有提供 CSRF 能力。');
  }
  return commandRequest(`/api/v1/commands/${encodeURIComponent(commandId)}:approve`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': options.csrfToken },
    body: JSON.stringify({}),
  }, options);
}

export async function getCommand(commandId: string, signal?: AbortSignal): Promise<Command> {
  if (!commandUUIDSchema.safeParse(commandId).success) {
    throw new CommandApiError(404, 'RESOURCE_NOT_FOUND', 'Command ID 格式无效。');
  }
  if (API_MODE === 'mock') return structuredClone(ensureMockCommand(commandId));
  if (!COMMAND_ROUTES_AVAILABLE) {
    throw new CommandApiError(503, 'COMMAND_ROUTE_DISABLED', 'Command 控制路由已登记，但尚未启用生产流量。');
  }
  return commandRequest(`/api/v1/commands/${encodeURIComponent(commandId)}`, { method: 'GET', signal });
}

export async function createCommand(input: CreateCommandInput): Promise<Command> {
  const assetId = commandUUIDv7Schema.parse(input.assetId);
  const commandPointId = commandUUIDv7Schema.parse(input.commandPointId);
  const parameters = z.record(z.string(), z.number().finite()).parse(input.parameters);
  if (API_MODE === 'mock') {
    if (assetId !== mockAssetId || commandPointId !== mockCommandPointId || !validateCommandParameters('SET_TEMPERATURE_SETPOINT', parameters)) {
      throw new CommandApiError(404, 'RESOURCE_NOT_FOUND', 'Mock Command Point 不存在。');
    }
    const capability: CommandCapability = 'SET_TEMPERATURE_SETPOINT';
    const setpointC = parameters.setpointC;
    const commandId = mockUuidV7();
    const createdAt = iso();
    const risk: CommandRisk = setpointC >= 26 ? 'MEDIUM' : 'LOW';
    const approvalPolicy = risk === 'LOW' ? 'NONE' : 'SINGLE_APPROVER';
    const status: CommandStatus = risk === 'LOW' ? 'QUEUED' : 'AWAITING_APPROVAL';
    const transitions: CommandTransition[] = [
      { toStatus: 'SUBMITTED', reason: 'COMMAND_SUBMITTED', actorType: 'PRINCIPAL', occurredAt: createdAt, version: 1 },
      { fromStatus: 'SUBMITTED', toStatus: 'VALIDATING', reason: 'COMMAND_VALIDATING', actorType: 'WORKLOAD', occurredAt: createdAt, version: 2 },
      { fromStatus: 'VALIDATING', toStatus: status, reason: risk === 'LOW' ? 'COMMAND_QUEUED' : 'APPROVAL_REQUIRED', actorType: 'WORKLOAD', occurredAt: createdAt, version: 3 },
    ];
    const command = commandSchema.parse({
      schemaVersion: 1,
      commandId,
      tenantId: ['018f3e00', '1000', '7000', '8000', '000000000001'].join('-'),
      siteId: ['018f3e00', '2000', '7000', '8000', '000000000001'].join('-'),
      deviceId: mockDeviceId,
      pointId: mockCommandPointId,
      capability,
      capabilityRevision: commandCapabilityProfiles[capability].revision,
      status,
      risk,
      approvalPolicy,
      approvalCount: 0,
      requiredApprovalCount: risk === 'LOW' ? 0 : 1,
      parameters,
      deviceCommandSequence: mockSequence++,
      version: 3,
      snapshotRevision: 18,
      transitions,
      createdAt,
      updatedAt: createdAt,
    });
    mockCommands.set(commandId, command);
    return structuredClone(command);
  }
  if (!COMMAND_ROUTES_AVAILABLE) {
    throw new CommandApiError(503, 'COMMAND_ROUTE_DISABLED', 'Command 控制路由已登记，但尚未启用生产流量。');
  }
  const csrf = await csrfCapability();
  return commandRequest('/api/v1/commands', {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      'X-CSRF-Token': csrf,
      'Idempotency-Key': `hvac-web-${crypto.randomUUID()}`,
    },
    body: JSON.stringify({ assetId, commandPointId, parameters }),
  });
}

export async function approveCommand(commandId: string): Promise<Command> {
  if (API_MODE === 'mock') {
    const current = ensureMockCommand(commandId);
    if (current.status !== 'AWAITING_APPROVAL' || current.approvalCount >= current.requiredApprovalCount) {
      throw new CommandApiError(409, 'COMMAND_APPROVAL_INVALID', '该 Command 当前不需要新的审批。');
    }
    const approvedAt = iso();
    const approvedVersion = current.version + 1;
    const queuedVersion = approvedVersion + 1;
    const approved = commandSchema.parse({
      ...current,
      status: 'QUEUED',
      approvalCount: current.approvalCount + 1,
      version: queuedVersion,
      transitions: [
        ...current.transitions,
        { fromStatus: 'AWAITING_APPROVAL', toStatus: 'APPROVED', reason: 'APPROVAL_THRESHOLD_MET', actorType: 'PRINCIPAL', occurredAt: approvedAt, version: approvedVersion },
        { fromStatus: 'APPROVED', toStatus: 'QUEUED', reason: 'COMMAND_QUEUED', actorType: 'WORKLOAD', occurredAt: approvedAt, version: queuedVersion },
      ],
      updatedAt: approvedAt,
    });
    mockCommands.set(commandId, approved);
    return structuredClone(approved);
  }
  if (!COMMAND_ROUTES_AVAILABLE) {
    throw new CommandApiError(503, 'COMMAND_ROUTE_DISABLED', 'Command 控制路由已登记，但尚未启用生产流量。');
  }
  const csrf = await csrfCapability();
  return commandRequest(`/api/v1/commands/${encodeURIComponent(commandId)}:approve`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf },
    body: JSON.stringify({}),
  });
}

export function commandErrorMessage(error: unknown): string {
  if (error instanceof CommandApiError) return error.message;
  if (error instanceof z.ZodError) return 'Command 契约校验失败，已拒绝显示不可信数据。';
  return error instanceof Error ? error.message : 'Command 操作失败。';
}
