import { z } from 'zod';

export const commandUUIDSchema = z.string().uuid();
export const commandUUIDv7Schema = commandUUIDSchema.regex(/^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);

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
  commandId: commandUUIDSchema,
  organizationId: commandUUIDv7Schema,
  siteId: commandUUIDv7Schema,
  deviceId: commandUUIDv7Schema,
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

export class CommandApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly retryable: boolean;

  constructor(status: number, code: string, message: string, retryable = false) {
    super(message);
    this.name = 'CommandApiError';
    this.status = status;
    this.code = code;
    this.retryable = retryable;
  }
}

export function validateCommandScope(
  command: Command,
  scope: { readonly trustedOrganizationId: string; readonly trustedSiteId: string },
): Command {
  const organizationId = commandUUIDv7Schema.parse(scope.trustedOrganizationId);
  const siteId = commandUUIDv7Schema.parse(scope.trustedSiteId);
  if (command.organizationId !== organizationId || command.siteId !== siteId) {
    throw new CommandApiError(404, 'RESOURCE_NOT_FOUND', '未找到该 Command。');
  }
  return command;
}
