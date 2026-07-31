import { z } from 'zod';
import {
  CommandApiError,
  commandSchema,
  commandUUIDSchema,
  commandUUIDv7Schema,
  validateCommandScope,
  type Command,
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
export type { Command, CommandRisk, CommandStatus, CommandTransition } from './command-contract';

export const COMMAND_PUBLIC_ROUTES_ENABLED = false as const;
export const COMMAND_LOCAL_ROUTES_ENABLED = API_MODE === 'real'
  && import.meta.env.DEV
  && (import.meta.env.VITE_S3_LOCAL_COMMANDS as string | undefined) === 'true';
export const COMMAND_ROUTES_AVAILABLE = COMMAND_PUBLIC_ROUTES_ENABLED || COMMAND_LOCAL_ROUTES_ENABLED;

export interface CreateCommandInput {
  deviceId: string;
  setpointC: number;
}

const localCommandDeviceSchema = z.object({
  organizationId: commandUUIDv7Schema,
  siteId: commandUUIDv7Schema,
  deviceId: commandUUIDv7Schema,
  name: z.string().min(1).max(128),
  type: z.string().min(1).max(64),
}).strict();

const localCommandDeviceCatalogSchema = z.object({
  schemaVersion: z.literal(1),
  devices: z.array(localCommandDeviceSchema).min(1).max(16),
}).strict();

export type LocalCommandDevice = z.infer<typeof localCommandDeviceSchema>;

export interface ScopedCommandRequestOptions {
  trustedOrganizationId: string;
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
export const MOCK_PENDING_COMMAND_ID = mockPendingCommandId;
export const MOCK_COMMAND_DEVICE_ID = mockDeviceId;

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
    organizationId: ['018f3e00', '1000', '7000', '8000', '000000000001'].join('-'),
    siteId: ['018f3e00', '2000', '7000', '8000', '000000000001'].join('-'),
    deviceId: mockDeviceId,
    capability: 'SET_TEMPERATURE_SETPOINT',
    capabilityRevision: 'capability:set-temperature-setpoint:v1',
    status: 'AWAITING_APPROVAL',
    risk: 'MEDIUM',
    approvalPolicy: 'SINGLE_APPROVER',
    approvalCount: 0,
    requiredApprovalCount: 1,
    setpointC: 27,
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
  return options?.trustedOrganizationId && options.trustedSiteId
    ? validateCommandScope(command, options as ScopedCommandRequestOptions)
    : command;
}

export async function listLocalCommandDevices(signal?: AbortSignal): Promise<LocalCommandDevice[]> {
  if (API_MODE === 'mock') {
    return [{
      organizationId: ['018f3e00', '1000', '7000', '8000', '000000000001'].join('-'),
      siteId: ['018f3e00', '2000', '7000', '8000', '000000000001'].join('-'),
      deviceId: mockDeviceId,
      name: 'Mock HVAC Device',
      type: 'HVAC',
    }];
  }
  if (!COMMAND_LOCAL_ROUTES_ENABLED) return [];
  const response = await fetch('/api/v1/local/devices', {
    method: 'GET',
    credentials: 'same-origin',
    signal,
    headers: { Accept: 'application/json, application/problem+json' },
  });
  const payload: unknown = await response.json().catch(() => ({}));
  if (!response.ok) {
    const problem = problemSchema.parse(payload);
    throw new CommandApiError(
      response.status,
      problem.code ?? 'COMMAND_DEVICE_CATALOG_UNAVAILABLE',
      problem.detail ?? problem.title ?? '本地设备目录暂时不可用。',
      problem.retryable ?? false,
    );
  }
  return localCommandDeviceCatalogSchema.parse(payload).devices;
}

export async function listScopedLocalCommandDevices(
  options: ScopedCommandRequestOptions,
): Promise<LocalCommandDevice[]> {
  if (!COMMAND_LOCAL_ROUTES_ENABLED) return [];
  const organizationId = commandUUIDv7Schema.parse(options.trustedOrganizationId);
  const siteId = commandUUIDv7Schema.parse(options.trustedSiteId);
  const fetchImplementation = options.fetchImplementation ?? globalThis.fetch.bind(globalThis);
  const response = await fetchImplementation(`${options.baseUrl ?? ''}/api/v1/local/devices`, {
    method: 'GET',
    credentials: 'same-origin',
    signal: options.signal,
    headers: { Accept: 'application/json, application/problem+json' },
  });
  const payload: unknown = await response.json().catch(() => ({}));
  if (!response.ok) {
    const problem = problemSchema.parse(payload);
    throw new CommandApiError(
      response.status,
      problem.code ?? 'COMMAND_DEVICE_CATALOG_UNAVAILABLE',
      problem.detail ?? problem.title ?? '本地设备目录暂时不可用。',
      problem.retryable ?? false,
    );
  }
  const devices = localCommandDeviceCatalogSchema.parse(payload).devices;
  if (devices.some((device) => device.organizationId !== organizationId || device.siteId !== siteId)) {
    throw new CommandApiError(503, 'COMMAND_DEVICE_CATALOG_INVALID', '本地设备目录超出当前 Site 范围。');
  }
  return devices;
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
  const deviceId = commandUUIDv7Schema.parse(input.deviceId);
  const setpointC = z.number().min(16).max(30).parse(input.setpointC);
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
    body: JSON.stringify({
      deviceId,
      capability: 'SET_TEMPERATURE_SETPOINT',
      parameters: { setpointC },
    }),
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
  const deviceId = commandUUIDv7Schema.parse(input.deviceId);
  const setpointC = z.number().min(16).max(30).parse(input.setpointC);
  if (API_MODE === 'mock') {
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
      organizationId: ['018f3e00', '1000', '7000', '8000', '000000000001'].join('-'),
      siteId: ['018f3e00', '2000', '7000', '8000', '000000000001'].join('-'),
      deviceId,
      capability: 'SET_TEMPERATURE_SETPOINT',
      capabilityRevision: 'capability:set-temperature-setpoint:v1',
      status,
      risk,
      approvalPolicy,
      approvalCount: 0,
      requiredApprovalCount: risk === 'LOW' ? 0 : 1,
      setpointC,
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
    body: JSON.stringify({
      deviceId,
      capability: 'SET_TEMPERATURE_SETPOINT',
      parameters: { setpointC },
    }),
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
