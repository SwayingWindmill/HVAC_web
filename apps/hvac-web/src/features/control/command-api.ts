import { queryOptions } from '@tanstack/react-query';
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

async function send<T>(path: string, init: RequestInit = {}): Promise<T> {
  const response = await fetch(path, {
    ...init,
    credentials: 'same-origin',
    headers: { Accept: 'application/json, application/problem+json', ...init.headers },
  });
  const payload = await response.json().catch(() => null);
  if (!response.ok) {
    throw new CommandRequestError(response.status, payload?.code, payload?.detail ?? payload?.title ?? '指令服务暂时不可用');
  }
  return payload as T;
}

function mutationHeaders(csrfToken: string): HeadersInit {
  return { 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken, 'Idempotency-Key': `command-${crypto.randomUUID()}` };
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
  return send<Command>(commandPaths.collection, { method: 'POST', headers: mutationHeaders(csrfToken), body: JSON.stringify(input) });
}

export function approveCommand(commandId: string, csrfToken: string): Promise<Command> {
  return send<Command>(commandPaths.approve.replace('{commandId}', commandId), {
    method: 'POST', headers: mutationHeaders(csrfToken), body: '{}',
  });
}

export function reconcileCommand(commandId: string, outcome: ReconciledOutcome, csrfToken: string): Promise<Command> {
  return send<Command>(commandPaths.reconcile.replace('{commandId}', commandId), {
    method: 'POST', headers: mutationHeaders(csrfToken), body: JSON.stringify({ outcome }),
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
