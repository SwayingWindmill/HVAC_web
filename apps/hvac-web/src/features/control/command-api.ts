import { queryOptions } from '@tanstack/react-query';
import { failureStatus, gatewayPath, gatewayRequest, ROUTE_MISSING_MESSAGE, type GatewayRequest } from '@/api/gateway-client';
import { commandPaths } from '@/api/generated/platformGateway.gen';

// Command governance through the Platform Gateway: an operator asks, the owner assesses the risk,
// approvals are collected when the policy needs them, and an unknown outcome is reconciled by a person.

export type CommandStatus =
  | 'SUBMITTED' | 'VALIDATING' | 'AWAITING_APPROVAL' | 'APPROVED' | 'QUEUED' | 'DISPATCHING'
  | 'SUCCEEDED' | 'FAILED' | 'REJECTED' | 'CANCELLED' | 'EXPIRED' | 'OUTCOME_UNKNOWN';
export type CommandRisk = 'LOW' | 'MEDIUM' | 'HIGH';
export type CommandApprovalPolicy = 'NONE' | 'SINGLE_APPROVER' | 'TWO_PERSON';
export type ReconciledOutcome = 'APPLIED' | 'NOT_APPLIED';

export interface CommandTransition {
  readonly fromStatus?: CommandStatus;
  readonly toStatus: CommandStatus;
  readonly reason: string;
  readonly actorType: 'PRINCIPAL' | 'WORKLOAD';
  readonly occurredAt: string;
  readonly version: number;
}

export interface Command {
  readonly commandId: string;
  readonly siteId: string;
  readonly deviceId: string;
  readonly pointId: string;
  readonly capability: string;
  readonly status: CommandStatus;
  readonly risk: CommandRisk;
  readonly approvalPolicy: CommandApprovalPolicy;
  readonly approvalCount: number;
  readonly requiredApprovalCount: number;
  readonly parameters: Readonly<Record<string, number>>;
  readonly transitions: readonly CommandTransition[];
  readonly createdAt: string;
  readonly updatedAt: string;
}

export class CommandRequestError extends Error {
  constructor(readonly status: number, readonly code: string | undefined, message: string) {
    super(message);
  }
}

async function send<T>(path: string, request: GatewayRequest<T> = {}): Promise<T> {
  const result = await gatewayRequest<T>(path, request);
  if (result.kind === 'ok') return result.value;
  if (result.kind === 'routeMissing') throw new CommandRequestError(result.status, undefined, ROUTE_MISSING_MESSAGE);
  const code = result.kind === 'unavailable' ? undefined : result.problem.code;
  const message = result.kind === 'unavailable'
    ? '指令服务暂时不可用'
    : (result.problem.detail ?? result.problem.title ?? '指令服务暂时不可用');
  throw new CommandRequestError(failureStatus(result), code, message);
}

export async function listCommands(siteId: string, signal?: AbortSignal): Promise<Command[]> {
  const query = new URLSearchParams({ siteId, limit: '50' });
  const body = await send<{ data: Command[] }>(`${commandPaths.collection}?${query.toString()}`, { signal });
  return body.data;
}

export function createCommand(
  input: { readonly assetId: string; readonly commandPointId: string; readonly parameters: Readonly<Record<string, number>> },
  csrfToken: string,
): Promise<Command> {
  return send<Command>(commandPaths.collection, {
    method: 'POST',
    csrfToken,
    idempotencyKey: `command-${crypto.randomUUID()}`,
    body: input,
  });
}

export function approveCommand(commandId: string, csrfToken: string): Promise<Command> {
  return send<Command>(gatewayPath(commandPaths.approve, { commandId }), {
    method: 'POST',
    csrfToken,
    idempotencyKey: `command-${crypto.randomUUID()}`,
    body: {},
  });
}

export function reconcileCommand(commandId: string, outcome: ReconciledOutcome, csrfToken: string): Promise<Command> {
  return send<Command>(gatewayPath(commandPaths.reconcile, { commandId }), {
    method: 'POST',
    csrfToken,
    idempotencyKey: `command-${crypto.randomUUID()}`,
    body: { outcome },
  });
}

export const commandKeys = {
  site: (siteId: string) => ['commands', siteId] as const,
};

/** A Site's newest Commands; followed on a short interval while any is still moving. */
export function siteCommandsQuery(siteId: string) {
  return queryOptions({
    queryKey: commandKeys.site(siteId),
    queryFn: ({ signal }) => listCommands(siteId, signal),
    refetchInterval: (query) => (query.state.data?.some((command) => !SETTLED.has(command.status)) ? 3_000 : 30_000),
  });
}

export const SETTLED: ReadonlySet<CommandStatus> = new Set(['SUCCEEDED', 'FAILED', 'REJECTED', 'CANCELLED', 'EXPIRED']);
