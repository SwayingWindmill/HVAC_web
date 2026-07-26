import { z } from 'zod';
import { API_MODE } from './config';
import { createPlatformGatewayClient } from './generated/platformGateway.gen';

export const COMMAND_PUBLIC_ROUTES_ENABLED = false as const;

const uuidV7Schema = z.string().uuid().regex(/^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);

export const commandStatusSchema = z.enum([
  'SUBMITTED', 'VALIDATING', 'AWAITING_APPROVAL', 'APPROVED', 'QUEUED', 'DISPATCHING',
  'SUCCEEDED', 'FAILED', 'REJECTED', 'CANCELLED', 'EXPIRED', 'OUTCOME_UNKNOWN',
]);
export const commandRiskSchema = z.enum(['LOW', 'MEDIUM', 'HIGH']);
export const commandApprovalPolicySchema = z.enum(['NONE', 'SINGLE_APPROVER', 'TWO_PERSON']);

export const commandTransitionSchema = z.object({
  fromStatus: commandStatusSchema.optional(),
  toStatus: commandStatusSchema,
  reason: z.string().min(1).max(256),
  actorType: z.enum(['PRINCIPAL', 'WORKLOAD']),
  occurredAt: z.string().datetime({ offset: true }),
  version: z.number().int().positive(),
}).strict();

export const commandSchema = z.object({
  schemaVersion: z.literal(1),
  commandId: uuidV7Schema,
  deviceId: uuidV7Schema,
  capability: z.literal('SET_TEMPERATURE_SETPOINT'),
  capabilityRevision: z.literal('capability:set-temperature-setpoint:v1'),
  status: commandStatusSchema,
  risk: commandRiskSchema,
  approvalPolicy: commandApprovalPolicySchema,
  approvalCount: z.number().int().nonnegative(),
  requiredApprovalCount: z.number().int().min(0).max(2),
  setpointC: z.number().min(16).max(30),
  deviceCommandSequence: z.number().int().positive(),
  version: z.number().int().positive(),
  snapshotRevision: z.number().int().positive(),
  transitions: z.array(commandTransitionSchema).min(1).max(256),
  createdAt: z.string().datetime({ offset: true }),
  updatedAt: z.string().datetime({ offset: true }),
}).strict().superRefine((command, context) => {
  const required = command.approvalPolicy === 'NONE' ? 0 : command.approvalPolicy === 'SINGLE_APPROVER' ? 1 : 2;
  if (command.requiredApprovalCount !== required || command.approvalCount > required) {
    context.addIssue({ code: z.ZodIssueCode.custom, message: 'Command approval projection is inconsistent' });
  }
  const latest = command.transitions.at(-1);
  if (!latest || latest.toStatus !== command.status || latest.version !== command.version) {
    context.addIssue({ code: z.ZodIssueCode.custom, message: 'Command timeline does not converge' });
  }
});

export type CommandStatus = z.infer<typeof commandStatusSchema>;
export type CommandRisk = z.infer<typeof commandRiskSchema>;
export type CommandTransition = z.infer<typeof commandTransitionSchema>;
export type Command = z.infer<typeof commandSchema>;

export interface CreateCommandInput {
  deviceId: string;
  setpointC: number;
}

export class CommandApiError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    message: string,
    readonly retryable = false,
  ) {
    super(message);
    this.name = 'CommandApiError';
  }
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

async function commandRequest(path: string, init: RequestInit): Promise<Command> {
  const response = await fetch(path, {
    ...init,
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
  return commandSchema.parse(payload);
}

export async function getCommand(commandId: string, signal?: AbortSignal): Promise<Command> {
  if (!uuidV7Schema.safeParse(commandId).success) {
    throw new CommandApiError(404, 'RESOURCE_NOT_FOUND', 'Command ID 格式无效。');
  }
  if (API_MODE === 'mock') return structuredClone(ensureMockCommand(commandId));
  if (!COMMAND_PUBLIC_ROUTES_ENABLED) {
    throw new CommandApiError(503, 'COMMAND_ROUTE_DISABLED', 'Command 控制路由已登记，但尚未启用生产流量。');
  }
  return commandRequest(`/api/v1/commands/${encodeURIComponent(commandId)}`, { method: 'GET', signal });
}

export async function createCommand(input: CreateCommandInput): Promise<Command> {
  const deviceId = uuidV7Schema.parse(input.deviceId);
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
  if (!COMMAND_PUBLIC_ROUTES_ENABLED) {
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
  if (!COMMAND_PUBLIC_ROUTES_ENABLED) {
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
