import { z } from 'zod';

export const commandUUIDSchema = z.string().uuid();
export const commandUUIDv7Schema = commandUUIDSchema.regex(/^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);

export const commandStatusSchema = z.enum([
  'SUBMITTED', 'VALIDATING', 'AWAITING_APPROVAL', 'APPROVED', 'QUEUED', 'DISPATCHING',
  'SUCCEEDED', 'FAILED', 'REJECTED', 'CANCELLED', 'EXPIRED', 'OUTCOME_UNKNOWN',
]);
export const commandRiskSchema = z.enum(['LOW', 'MEDIUM', 'HIGH']);
export const commandApprovalPolicySchema = z.enum(['NONE', 'SINGLE_APPROVER', 'TWO_PERSON']);
export const commandCapabilitySchema = z.enum([
  'START',
  'STOP',
  'RESET_FAULT',
  'SET_TEMPERATURE_SETPOINT',
  'SET_CHILLED_WATER_TEMPERATURE_SETPOINT',
  'SET_FREQUENCY',
  'SET_FAN_SPEED',
  'SET_LOAD_LIMIT',
  'SET_OPENING',
]);
export const commandCapabilityRevisionSchema = z.enum([
  'capability:start:v1',
  'capability:stop:v1',
  'capability:reset-fault:v1',
  'capability:set-temperature-setpoint:v1',
  'capability:set-chilled-water-temperature-setpoint:v1',
  'capability:set-frequency:v1',
  'capability:set-fan-speed:v1',
  'capability:set-load-limit:v1',
  'capability:set-opening:v1',
]);

export type CommandCapability = z.infer<typeof commandCapabilitySchema>;

export const commandCapabilityProfiles: Readonly<Record<CommandCapability, Readonly<{
  revision: z.infer<typeof commandCapabilityRevisionSchema>;
  parameterKey?: string;
  minimum?: number;
  maximum?: number;
  step?: number;
}>>> = Object.freeze({
  START: Object.freeze({ revision: 'capability:start:v1' }),
  STOP: Object.freeze({ revision: 'capability:stop:v1' }),
  RESET_FAULT: Object.freeze({ revision: 'capability:reset-fault:v1' }),
  SET_TEMPERATURE_SETPOINT: Object.freeze({ revision: 'capability:set-temperature-setpoint:v1', parameterKey: 'setpointC', minimum: 16, maximum: 30, step: 0.5 }),
  SET_CHILLED_WATER_TEMPERATURE_SETPOINT: Object.freeze({ revision: 'capability:set-chilled-water-temperature-setpoint:v1', parameterKey: 'setpointC', minimum: 5, maximum: 12, step: 0.5 }),
  SET_FREQUENCY: Object.freeze({ revision: 'capability:set-frequency:v1', parameterKey: 'frequencyHz', minimum: 20, maximum: 50, step: 0.5 }),
  SET_FAN_SPEED: Object.freeze({ revision: 'capability:set-fan-speed:v1', parameterKey: 'fanSpeedPct', minimum: 20, maximum: 100, step: 1 }),
  SET_LOAD_LIMIT: Object.freeze({ revision: 'capability:set-load-limit:v1', parameterKey: 'loadLimitPct', minimum: 20, maximum: 100, step: 1 }),
  SET_OPENING: Object.freeze({ revision: 'capability:set-opening:v1', parameterKey: 'openingPct', minimum: 0, maximum: 100, step: 1 }),
});

export function validateCommandParameters(capability: CommandCapability, parameters: Readonly<Record<string, number>>): boolean {
  const profile = commandCapabilityProfiles[capability];
  const entries = Object.entries(parameters);
  if (!profile.parameterKey) return entries.length === 0;
  if (entries.length !== 1 || entries[0]?.[0] !== profile.parameterKey) return false;
  const value = entries[0][1];
  return Number.isFinite(value) && value >= profile.minimum! && value <= profile.maximum!;
}

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
  tenantId: commandUUIDv7Schema,
  siteId: commandUUIDv7Schema,
  deviceId: commandUUIDv7Schema,
  pointId: commandUUIDv7Schema,
  capability: commandCapabilitySchema,
  capabilityRevision: commandCapabilityRevisionSchema,
  status: commandStatusSchema,
  risk: commandRiskSchema,
  approvalPolicy: commandApprovalPolicySchema,
  approvalCount: z.number().int().nonnegative(),
  requiredApprovalCount: z.number().int().min(0).max(2),
  parameters: z.record(z.string(), z.number().finite()),
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
  const profile = commandCapabilityProfiles[command.capability];
  if (command.capabilityRevision !== profile.revision) {
    context.addIssue({ code: z.ZodIssueCode.custom, message: 'Command capability revision is inconsistent' });
  }
  if (!validateCommandParameters(command.capability, command.parameters)) {
    context.addIssue({ code: z.ZodIssueCode.custom, message: 'Command parameters are inconsistent with the capability profile' });
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
  scope: { readonly trustedTenantId: string; readonly trustedSiteId: string },
): Command {
  const tenantId = commandUUIDv7Schema.parse(scope.trustedTenantId);
  const siteId = commandUUIDv7Schema.parse(scope.trustedSiteId);
  if (command.tenantId !== tenantId || command.siteId !== siteId) {
    throw new CommandApiError(404, 'RESOURCE_NOT_FOUND', '未找到该 Command。');
  }
  return command;
}
