import { queryOptions } from '@tanstack/react-query';
import { failureStatus, gatewayPath, gatewayRequest, ROUTE_MISSING_MESSAGE, type GatewayRequest, type StepUpFailureReason } from '@/api/gateway-client';
import { commandPaths } from '@/api/generated/platformGateway.gen';
import { controlErrorMessage } from './control-errors';

// Command governance through the Platform Gateway: an operator asks, the owner assesses the risk,
// approvals are collected when the policy needs them, and an unknown outcome is reconciled by a person.

export type CommandStatus =
  | 'SUBMITTED' | 'VALIDATING' | 'AWAITING_APPROVAL' | 'APPROVED' | 'QUEUED' | 'DISPATCHING'
  | 'SUCCEEDED' | 'FAILED' | 'REJECTED' | 'CANCELLED' | 'EXPIRED' | 'OUTCOME_UNKNOWN';
export type CommandRisk = 'LOW' | 'MEDIUM' | 'HIGH' | 'CRITICAL';
export type CommandApprovalPolicy = 'NONE' | 'SINGLE_APPROVER' | 'TWO_PERSON';
export type CommandPriorityTier = 'SAFETY_INTERLOCK' | 'MANUAL_OVERRIDE' | 'PROTECTION_RULE' | 'AI_OPTIMIZATION' | 'SCHEDULE';
export type CommandSource = 'SAFETY_INTERLOCK' | 'OPERATOR' | 'PROTECTION_RULE' | 'OPTIMIZATION' | 'SCHEDULE';
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
  readonly priorityTier?: CommandPriorityTier;
  readonly commandSource?: CommandSource;
  readonly approvalPolicy: CommandApprovalPolicy;
  readonly approvalCount: number;
  readonly requiredApprovalCount: number;
  readonly parameters: Readonly<Record<string, number>>;
  readonly transitions: readonly CommandTransition[];
  readonly createdAt: string;
  readonly updatedAt: string;
}

export class CommandRequestError extends Error {
  constructor(
    readonly status: number,
    readonly code: string | undefined,
    message: string,
    readonly reason?: StepUpFailureReason,
  ) {
    super(message);
  }
}

async function send<T>(path: string, request: GatewayRequest<T> = {}): Promise<T> {
  const result = await gatewayRequest<T>(path, request);
  if (result.kind === 'ok') return result.value;
  if (result.kind === 'routeMissing') throw new CommandRequestError(result.status, undefined, ROUTE_MISSING_MESSAGE);
  const code = result.kind === 'unavailable' ? undefined : result.problem.code;
  const reason = result.kind === 'unavailable' ? undefined : result.problem.reason;
  throw new CommandRequestError(failureStatus(result), code, controlErrorMessage(code, result.kind === 'unavailable' ? '指令服务暂时不可用，请稍后再试' : '指令没有被接受，请刷新后再试'), reason);
}

const idempotencyKey = () => `command-${crypto.randomUUID()}`;

export async function listCommands(siteId: string, signal?: AbortSignal): Promise<Command[]> {
  const query = new URLSearchParams({ siteId, limit: '50' });
  const body = await send<{ data: Command[] }>(`${commandPaths.collection}?${query.toString()}`, { signal });
  return body.data;
}

export interface CommandInput {
  readonly assetId: string;
  readonly commandPointId: string;
  readonly parameters: Readonly<Record<string, number>>;
  /** The authenticator's 6-digit code; the Gateway requires it for high and critical risk. */
  readonly verificationCode?: string;
}

export function createCommand(input: CommandInput, csrfToken: string): Promise<Command> {
  return send<Command>(commandPaths.collection, { method: 'POST', csrfToken, idempotencyKey: idempotencyKey(), body: input });
}

export function approveCommand(commandId: string, csrfToken: string): Promise<Command> {
  return send<Command>(gatewayPath(commandPaths.approve, { commandId }), { method: 'POST', csrfToken, idempotencyKey: idempotencyKey(), body: {} });
}

/** Withdraws a command that is still waiting for approval or queued. */
export function cancelCommand(commandId: string, reason: string | undefined, csrfToken: string): Promise<Command> {
  return send<Command>(gatewayPath(commandPaths.cancel, { commandId }), {
    method: 'POST',
    csrfToken,
    idempotencyKey: idempotencyKey(),
    body: reason ? { reason: reason.slice(0, 256) } : {},
  });
}

export function reconcileCommand(commandId: string, outcome: ReconciledOutcome, csrfToken: string): Promise<Command> {
  return send<Command>(gatewayPath(commandPaths.reconcile, { commandId }), { method: 'POST', csrfToken, idempotencyKey: idempotencyKey(), body: { outcome } });
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

/** Still before dispatch, so a person can withdraw it. */
export const WITHDRAWABLE: ReadonlySet<CommandStatus> = new Set(['AWAITING_APPROVAL', 'QUEUED']);
