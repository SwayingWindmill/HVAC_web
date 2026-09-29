import { z } from 'zod';
import {
  CommandApiError,
  commandSchema,
  commandUUIDSchema,
  commandUUIDv7Schema,
  validateCommandScope,
  type Command,
} from './command-contract';
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

export const COMMAND_PUBLIC_ROUTES_ENABLED = true;
export const COMMAND_LOCAL_ROUTES_ENABLED = import.meta.env.DEV
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
  return commandRequest(`/api/v1/commands/${encodeURIComponent(commandId)}/approve`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': options.csrfToken },
    body: JSON.stringify({}),
  }, options);
}

export async function getCommand(commandId: string, signal?: AbortSignal): Promise<Command> {
  if (!commandUUIDSchema.safeParse(commandId).success) {
    throw new CommandApiError(404, 'RESOURCE_NOT_FOUND', 'Command ID 格式无效。');
  }
  if (!COMMAND_ROUTES_AVAILABLE) {
    throw new CommandApiError(503, 'COMMAND_ROUTE_DISABLED', 'Command 控制路由已登记，但尚未启用生产流量。');
  }
  return commandRequest(`/api/v1/commands/${encodeURIComponent(commandId)}`, { method: 'GET', signal });
}

export async function createCommand(input: CreateCommandInput): Promise<Command> {
  const assetId = commandUUIDv7Schema.parse(input.assetId);
  const commandPointId = commandUUIDv7Schema.parse(input.commandPointId);
  const parameters = z.record(z.string(), z.number().finite()).parse(input.parameters);
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
  if (!COMMAND_ROUTES_AVAILABLE) {
    throw new CommandApiError(503, 'COMMAND_ROUTE_DISABLED', 'Command 控制路由已登记，但尚未启用生产流量。');
  }
  const csrf = await csrfCapability();
  return commandRequest(`/api/v1/commands/${encodeURIComponent(commandId)}/approve`, {
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
