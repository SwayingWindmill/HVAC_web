import {
  OperationsInvestigation,
  OperationsInvestigationError,
  createIdempotencyKey,
  createStepIdentity,
  type CommittedEffectKind,
  type CommittedEffectView,
  type InvestigationRevision,
  type InvestigationScope,
  type OperationsInvestigationErrorCode,
  type OperationsInvestigationView,
} from '../../domain/index.js';
import {
  InvestigationRepositoryConflictError,
  type ApplicationEvent,
  type AuditRecord,
  type InvestigationAuthorizationAction,
  type InvestigationCoordinatorPorts,
  type OwnerReadResult,
  type ParallelReadRequest,
  type RuntimePlanningResult,
  type RuntimeReadPlan,
} from './ports.js';

export type InvestigationCoordinatorErrorCode =
  | 'INVESTIGATION_NOT_FOUND'
  | 'AUTHORIZATION_DENIED'
  | 'LEASE_CONFLICT'
  | 'REVISION_CONFLICT'
  | 'DUPLICATE_EFFECT'
  | 'BUDGET_EXHAUSTED'
  | 'UNABLE_TO_CONCLUDE'
  | 'INVALID_INVESTIGATION_STATE';

export class InvestigationCoordinatorError extends Error {
  readonly code: InvestigationCoordinatorErrorCode;

  constructor(code: InvestigationCoordinatorErrorCode, message: string, options?: ErrorOptions) {
    super(message, options);
    this.name = 'InvestigationCoordinatorError';
    this.code = code;
  }
}

export interface CreateInvestigationCommand {
  readonly scope: InvestigationScope;
}

export interface StartInvestigationCommand {
  readonly investigationId: string;
  readonly runtimeRevision: string;
  readonly expectedRevision: InvestigationRevision;
}

export type ReopenInvestigationCommand = StartInvestigationCommand;

export interface GetInvestigationQuery {
  readonly investigationId: string;
}

export interface AdvanceInvestigationCommand {
  readonly investigationId: string;
  readonly runId: string;
  readonly leaseId: string;
  readonly expectedRevision: InvestigationRevision;
}

export interface AdvanceInvestigationResult {
  readonly investigation: OperationsInvestigationView;
  readonly plan: RuntimeReadPlan;
  readonly results: readonly OwnerReadResult[];
  readonly checkpointId: string;
}

export interface RunLeaseMutationCommand {
  readonly investigationId: string;
  readonly runId: string;
  readonly leaseId: string;
  readonly expectedRevision: InvestigationRevision;
}

export interface ResumeInvestigationCommand {
  readonly investigationId: string;
  readonly runId: string;
  readonly expectedRevision: InvestigationRevision;
}

export interface CancelInvestigationCommand {
  readonly investigationId: string;
  readonly expectedRevision: InvestigationRevision;
}

export interface CommitInvestigationEffectCommand extends RunLeaseMutationCommand {
  readonly stepId: string;
  readonly idempotencyKey: string;
  readonly kind: CommittedEffectKind;
  readonly recordId: string;
}

export interface CommitInvestigationEffectResult {
  readonly outcome: 'COMMITTED' | 'DUPLICATE';
  readonly investigation: OperationsInvestigationView;
  readonly effect: CommittedEffectView;
}

export interface InvestigationCoordinator {
  create(command: CreateInvestigationCommand): Promise<OperationsInvestigationView>;
  start(command: StartInvestigationCommand): Promise<OperationsInvestigationView>;
  reopen(command: ReopenInvestigationCommand): Promise<OperationsInvestigationView>;
  advance(command: AdvanceInvestigationCommand): Promise<AdvanceInvestigationResult>;
  commitEffect(command: CommitInvestigationEffectCommand): Promise<CommitInvestigationEffectResult>;
  pause(command: RunLeaseMutationCommand): Promise<OperationsInvestigationView>;
  resume(command: ResumeInvestigationCommand): Promise<OperationsInvestigationView>;
  cancel(command: CancelInvestigationCommand): Promise<OperationsInvestigationView>;
  complete(command: RunLeaseMutationCommand): Promise<OperationsInvestigationView>;
  fail(command: RunLeaseMutationCommand): Promise<OperationsInvestigationView>;
  get(query: GetInvestigationQuery): Promise<OperationsInvestigationView>;
}

const leaseErrorCodes = new Set<OperationsInvestigationErrorCode>([
  'LEASE_INVALID',
  'LEASE_ID_REUSED',
  'LEASE_MISMATCH',
  'LEASE_EXPIRED',
  'RUN_NOT_ACTIVE',
]);

const duplicateErrorCodes = new Set<OperationsInvestigationErrorCode>([
  'IDEMPOTENCY_KEY_REUSED',
  'EFFECT_RECORD_ALREADY_COMMITTED',
]);

const mapApplicationError = (error: unknown): never => {
  if (error instanceof InvestigationCoordinatorError) throw error;
  if (error instanceof InvestigationRepositoryConflictError) {
    const code = error.code === 'LEASE_CONFLICT'
      ? 'LEASE_CONFLICT'
      : error.code === 'DUPLICATE_EFFECT'
        ? 'DUPLICATE_EFFECT'
        : error.code === 'IDENTITY_CONFLICT'
          ? 'INVALID_INVESTIGATION_STATE'
          : 'REVISION_CONFLICT';
    throw new InvestigationCoordinatorError(code, error.message, { cause: error });
  }
  if (error instanceof OperationsInvestigationError) {
    if (error.code === 'REVISION_STALE') {
      throw new InvestigationCoordinatorError('REVISION_CONFLICT', error.message, { cause: error });
    }
    if (leaseErrorCodes.has(error.code)) {
      throw new InvestigationCoordinatorError('LEASE_CONFLICT', error.message, { cause: error });
    }
    if (duplicateErrorCodes.has(error.code)) {
      throw new InvestigationCoordinatorError('DUPLICATE_EFFECT', error.message, { cause: error });
    }
    throw new InvestigationCoordinatorError(
      'INVALID_INVESTIGATION_STATE',
      error.message,
      { cause: error },
    );
  }
  throw error;
};

const requirePositiveLeaseDuration = (value: number): number => {
  if (!Number.isSafeInteger(value) || value <= 0) {
    throw new Error('leaseDurationMs must be a positive safe integer.');
  }
  return value;
};

const supportedReadTools = new Set<ParallelReadRequest['tool']>([
  'registry.getEquipment',
  'telemetry.getCurrentSnapshot',
  'analytics.getEnergySeries',
  'commands.getCapabilities',
]);

const supportedQualities = new Set<OwnerReadResult['quality']>([
  'GOOD',
  'UNCERTAIN',
  'BAD',
  'STALE',
]);

const scopeIsWithin = (
  authorized: InvestigationScope,
  returned: InvestigationScope,
): boolean => (
  authorized.organizationId === returned.organizationId
  && (authorized.siteId === null || authorized.siteId === returned.siteId)
  && (authorized.equipmentId === null || authorized.equipmentId === returned.equipmentId)
  && (authorized.deviceId === null || authorized.deviceId === returned.deviceId)
);

const expectedOwner = (tool: ParallelReadRequest['tool']): OwnerReadResult['owner'] => {
  if (tool === 'registry.getEquipment') return 'registry';
  if (tool === 'telemetry.getCurrentSnapshot') return 'telemetry-query-service';
  if (tool === 'analytics.getEnergySeries') return 'analytics-service';
  return 'command-service';
};

const validateOwnerResult = (
  request: ParallelReadRequest,
  result: OwnerReadResult,
  authorizedScope: InvestigationScope,
): OwnerReadResult => {
  if (result.requestId !== request.requestId
    || result.owner !== expectedOwner(request.tool)
    || !scopeIsWithin(authorizedScope, result.scope)
    || result.revision.trim().length === 0
    || result.provenance.trim().length === 0
    || !supportedQualities.has(result.quality)) {
    throw new InvestigationCoordinatorError(
      'INVALID_INVESTIGATION_STATE',
      `Owner result for READ request ${request.requestId} failed metadata validation.`,
    );
  }
  return result;
};

const countValidatedReads = (plan: RuntimeReadPlan): number => {
  if (plan.batches.length === 0) {
    throw new InvestigationCoordinatorError(
      'INVALID_INVESTIGATION_STATE',
      'Runtime READ plan must contain at least one batch.',
    );
  }
  const batchIds = new Set<string>();
  const requestIds = new Set<string>();
  let count = 0;
  for (const batch of plan.batches) {
    if (batch.batchId.trim().length === 0 || batchIds.has(batch.batchId)) {
      throw new InvestigationCoordinatorError(
        'INVALID_INVESTIGATION_STATE',
        `Runtime READ batch identity ${batch.batchId} is invalid or duplicated.`,
      );
    }
    batchIds.add(batch.batchId);
    if (batch.requests.length === 0) {
      throw new InvestigationCoordinatorError(
        'INVALID_INVESTIGATION_STATE',
        `Runtime READ batch ${batch.batchId} must contain at least one request.`,
      );
    }
    for (const request of batch.requests) {
      if (request.requestId.trim().length === 0 || requestIds.has(request.requestId)) {
        throw new InvestigationCoordinatorError(
          'INVALID_INVESTIGATION_STATE',
          `Runtime READ request identity ${request.requestId} is invalid or duplicated.`,
        );
      }
      if (!supportedReadTools.has(request.tool)) {
        throw new InvestigationCoordinatorError(
          'INVALID_INVESTIGATION_STATE',
          `Runtime requested unsupported READ tool ${String(request.tool)}.`,
        );
      }
      requestIds.add(request.requestId);
      count += 1;
    }
  }
  return count;
};

export const createInvestigationCoordinator = (
  ports: InvestigationCoordinatorPorts,
): InvestigationCoordinator => {
  const leaseDurationMs = requirePositiveLeaseDuration(ports.leaseDurationMs);

  const load = async (investigationId: string): Promise<OperationsInvestigation> => {
    const investigation = await ports.investigationRepository.get(investigationId);
    if (investigation === null) {
      throw new InvestigationCoordinatorError(
        'INVESTIGATION_NOT_FOUND',
        `Operations Investigation ${investigationId} was not found.`,
      );
    }
    return investigation;
  };

  const authorize = async (
    scope: InvestigationScope,
    action: InvestigationAuthorizationAction,
  ): Promise<void> => {
    const authorization = await ports.authorizationDecisionReader.authorizeScope({ scope, action });
    if (authorization.decision === 'DENY') {
      throw new InvestigationCoordinatorError(
        'AUTHORIZATION_DENIED',
        authorization.reason ?? 'The requested Investigation Scope is not authorized.',
      );
    }
  };

  const loadAuthorized = async (
    investigationId: string,
    action: InvestigationAuthorizationAction,
  ): Promise<OperationsInvestigation> => {
    const investigation = await load(investigationId);
    await authorize(investigation.view().scope, action);
    return investigation;
  };

  const recordMutation = async (
    event: ApplicationEvent,
    audit: AuditRecord,
  ): Promise<void> => {
    await ports.applicationOutbox.append(event);
    await ports.auditRecorder.record(audit);
  };

  const persistMutation = async (input: {
    readonly investigation: OperationsInvestigation;
    readonly expectedRevision: InvestigationRevision;
    readonly expectedAuthority?: {
      readonly runId: string;
      readonly leaseId: string;
      readonly at: number;
    };
    readonly effect?: CommittedEffectView;
    readonly occurredAt: number;
    readonly eventType: ApplicationEvent['type'];
    readonly auditAction: AuditRecord['action'];
    readonly runId: string | null;
  }): Promise<OperationsInvestigationView> => {
    const view = input.investigation.view();
    await ports.investigationTransaction.save({
      investigation: input.investigation,
      expectedRevision: input.expectedRevision,
      ...(input.expectedAuthority === undefined
        ? {}
        : { expectedAuthority: input.expectedAuthority }),
      ...(input.effect === undefined ? {} : { effect: input.effect }),
      event: {
        type: input.eventType,
        investigationId: view.id,
        revision: view.revision,
        occurredAt: input.occurredAt,
      },
      audit: {
        action: input.auditAction,
        investigationId: view.id,
        runId: input.runId,
        revision: view.revision,
        occurredAt: input.occurredAt,
      },
    });
    return view;
  };

  const executeRead = async (
    request: ParallelReadRequest,
    authorizedScope: InvestigationScope,
  ): Promise<OwnerReadResult> => {
    const result = request.tool === 'registry.getEquipment'
      ? await ports.ownerReaders.registry.read(request)
      : request.tool === 'telemetry.getCurrentSnapshot'
        ? await ports.ownerReaders.currentTelemetry.read(request)
        : request.tool === 'analytics.getEnergySeries'
          ? await ports.ownerReaders.energyAnalytics.read(request)
          : await ports.ownerReaders.commandCapabilities.read(request);
    return validateOwnerResult(request, result, authorizedScope);
  };

  return {
    async create(command) {
      try {
        await authorize(command.scope, 'CREATE_INVESTIGATION');

        const now = ports.clock.now();
        const investigation = OperationsInvestigation.create({
          id: ports.idGenerator.next('investigation'),
          scope: command.scope,
          createdAt: now,
        });
        const view = investigation.view();
        await ports.investigationTransaction.create({
          investigation,
          event: {
            type: 'INVESTIGATION_CREATED',
            investigationId: view.id,
            revision: view.revision,
            occurredAt: now,
          },
          audit: {
            action: 'CREATE_INVESTIGATION',
            investigationId: view.id,
            runId: null,
            revision: view.revision,
            occurredAt: now,
          },
        });
        return view;
      } catch (error) {
        return mapApplicationError(error);
      }
    },

    async start(command) {
      try {
        const investigation = await loadAuthorized(command.investigationId, 'START_AGENT_RUN');
        const now = ports.clock.now();
        const next = investigation.startRun({
          runId: ports.idGenerator.next('run'),
          runtimeRevision: command.runtimeRevision,
          leaseId: ports.idGenerator.next('lease'),
          leaseAcquiredAt: now,
          leaseExpiresAt: now + leaseDurationMs,
          expectedRevision: command.expectedRevision,
        });
        return await persistMutation({
          investigation: next,
          expectedRevision: command.expectedRevision,
          occurredAt: now,
          eventType: 'AGENT_RUN_STARTED',
          auditAction: 'START_AGENT_RUN',
          runId: next.view().activeRunId,
        });
      } catch (error) {
        return mapApplicationError(error);
      }
    },

    async reopen(command) {
      try {
        const investigation = await loadAuthorized(command.investigationId, 'REOPEN_INVESTIGATION');
        const now = ports.clock.now();
        const next = investigation.reopenCompleted({
          runId: ports.idGenerator.next('run'),
          runtimeRevision: command.runtimeRevision,
          leaseId: ports.idGenerator.next('lease'),
          leaseAcquiredAt: now,
          leaseExpiresAt: now + leaseDurationMs,
          expectedRevision: command.expectedRevision,
        });
        return await persistMutation({
          investigation: next,
          expectedRevision: command.expectedRevision,
          occurredAt: now,
          eventType: 'AGENT_RUN_STARTED',
          auditAction: 'START_AGENT_RUN',
          runId: next.view().activeRunId,
        });
      } catch (error) {
        return mapApplicationError(error);
      }
    },

    async advance(command) {
      try {
        const investigation = await loadAuthorized(command.investigationId, 'ADVANCE_AGENT_RUN');
        const now = ports.clock.now();
        const run = investigation.assertRunAuthority({
          runId: command.runId,
          leaseId: command.leaseId,
          at: now,
          expectedRevision: command.expectedRevision,
        });
        const investigationView = investigation.view();
        const checkpoint = await ports.checkpointRepository.load(command.investigationId, run.id);
        if (checkpoint !== null && checkpoint.runtimeRevision !== run.runtimeRevision) {
          throw new InvestigationCoordinatorError(
            'INVALID_INVESTIGATION_STATE',
            'Runtime Checkpoint revision does not match the active Agent Run.',
          );
        }
        let planning: RuntimePlanningResult;
        try {
          planning = await ports.agentExecutionRuntime.planReads({
            investigation: investigation.view(),
            runId: run.id,
            checkpoint,
          });
        } catch (cause) {
          throw new InvestigationCoordinatorError(
            'INVALID_INVESTIGATION_STATE',
            'The Agent execution Runtime rejected the active Run or Checkpoint.',
            { cause },
          );
        }
        if (planning.status === 'UNABLE_TO_CONCLUDE') {
          throw new InvestigationCoordinatorError('UNABLE_TO_CONCLUDE', planning.reason);
        }

        const plannedReadCount = countValidatedReads(planning.plan);
        const budget = await ports.budgetGuard.check({
          investigationId: command.investigationId,
          runId: run.id,
          plannedReadCount,
        });
        if (budget.decision === 'DENY') {
          throw new InvestigationCoordinatorError(
            'BUDGET_EXHAUSTED',
            budget.reason ?? 'The Agent Run budget is exhausted.',
          );
        }

        const results: OwnerReadResult[] = [];
        for (const batch of planning.plan.batches) {
          results.push(...await Promise.all(batch.requests.map((request) => (
            executeRead(request, investigationView.scope)
          ))));
        }

        const checkpointId = ports.idGenerator.next('checkpoint');
        const checkpointSavedAt = checkpoint === null
          ? now
          : Math.max(now, checkpoint.savedAt + 1);
        if (!Number.isSafeInteger(checkpointSavedAt)) {
          throw new InvestigationCoordinatorError(
            'INVALID_INVESTIGATION_STATE',
            'Runtime Checkpoint time is outside the safe integer range.',
          );
        }
        await ports.checkpointRepository.save({
          id: checkpointId,
          investigationId: command.investigationId,
          runId: run.id,
          runtimeRevision: run.runtimeRevision,
          position: planning.checkpoint.position,
          opaqueState: planning.checkpoint.opaqueState,
          savedAt: checkpointSavedAt,
        });

        const view = investigation.view();
        await recordMutation(
          {
            type: 'READ_PLAN_COMPLETED',
            investigationId: view.id,
            revision: view.revision,
            occurredAt: now,
          },
          {
            action: 'PLAN_READS',
            investigationId: view.id,
            runId: run.id,
            revision: view.revision,
            occurredAt: now,
          },
        );
        return {
          investigation: view,
          plan: planning.plan,
          results,
          checkpointId,
        };
      } catch (error) {
        return mapApplicationError(error);
      }
    },

    async commitEffect(command) {
      try {
        const investigation = await loadAuthorized(command.investigationId, 'COMMIT_EFFECT');
        const now = ports.clock.now();
        const result = investigation.commitEffect({
          runId: command.runId,
          leaseId: command.leaseId,
          at: now,
          expectedRevision: command.expectedRevision,
          stepId: createStepIdentity(command.stepId),
          idempotencyKey: createIdempotencyKey(command.idempotencyKey),
          kind: command.kind,
          recordId: command.recordId,
        });
        const view = result.outcome === 'COMMITTED'
          ? await persistMutation({
            investigation: result.investigation,
            expectedRevision: command.expectedRevision,
            expectedAuthority: {
              runId: command.runId,
              leaseId: command.leaseId,
              at: now,
            },
            effect: result.effect,
            occurredAt: now,
            eventType: 'INVESTIGATION_EFFECT_COMMITTED',
            auditAction: 'COMMIT_EFFECT',
            runId: command.runId,
          })
          : result.investigation.view();
        return {
          outcome: result.outcome,
          investigation: view,
          effect: result.effect,
        };
      } catch (error) {
        return mapApplicationError(error);
      }
    },

    async pause(command) {
      try {
        const investigation = await loadAuthorized(command.investigationId, 'PAUSE_AGENT_RUN');
        const now = ports.clock.now();
        const next = investigation.pauseRun({
          runId: command.runId,
          leaseId: command.leaseId,
          at: now,
          expectedRevision: command.expectedRevision,
        });
        return await persistMutation({
          investigation: next,
          expectedRevision: command.expectedRevision,
          expectedAuthority: {
            runId: command.runId,
            leaseId: command.leaseId,
            at: now,
          },
          occurredAt: now,
          eventType: 'AGENT_RUN_PAUSED',
          auditAction: 'PAUSE_AGENT_RUN',
          runId: command.runId,
        });
      } catch (error) {
        return mapApplicationError(error);
      }
    },

    async resume(command) {
      try {
        const investigation = await loadAuthorized(command.investigationId, 'RESUME_AGENT_RUN');
        const now = ports.clock.now();
        const next = investigation.resumeRun({
          runId: command.runId,
          leaseId: ports.idGenerator.next('lease'),
          leaseAcquiredAt: now,
          leaseExpiresAt: now + leaseDurationMs,
          expectedRevision: command.expectedRevision,
        });
        return await persistMutation({
          investigation: next,
          expectedRevision: command.expectedRevision,
          occurredAt: now,
          eventType: 'AGENT_RUN_RESUMED',
          auditAction: 'RESUME_AGENT_RUN',
          runId: command.runId,
        });
      } catch (error) {
        return mapApplicationError(error);
      }
    },

    async cancel(command) {
      try {
        const investigation = await loadAuthorized(command.investigationId, 'CANCEL_INVESTIGATION');
        const activeRunId = investigation.view().activeRunId;
        const now = ports.clock.now();
        const next = investigation.cancel({
          at: now,
          expectedRevision: command.expectedRevision,
        });
        return await persistMutation({
          investigation: next,
          expectedRevision: command.expectedRevision,
          occurredAt: now,
          eventType: 'INVESTIGATION_CANCELLED',
          auditAction: 'CANCEL_INVESTIGATION',
          runId: activeRunId,
        });
      } catch (error) {
        return mapApplicationError(error);
      }
    },

    async complete(command) {
      try {
        const investigation = await loadAuthorized(command.investigationId, 'COMPLETE_AGENT_RUN');
        const now = ports.clock.now();
        const next = investigation.completeRun({
          runId: command.runId,
          leaseId: command.leaseId,
          at: now,
          expectedRevision: command.expectedRevision,
        });
        return await persistMutation({
          investigation: next,
          expectedRevision: command.expectedRevision,
          expectedAuthority: {
            runId: command.runId,
            leaseId: command.leaseId,
            at: now,
          },
          occurredAt: now,
          eventType: 'AGENT_RUN_COMPLETED',
          auditAction: 'COMPLETE_AGENT_RUN',
          runId: command.runId,
        });
      } catch (error) {
        return mapApplicationError(error);
      }
    },

    async fail(command) {
      try {
        const investigation = await loadAuthorized(command.investigationId, 'FAIL_AGENT_RUN');
        const now = ports.clock.now();
        const next = investigation.failRun({
          runId: command.runId,
          leaseId: command.leaseId,
          at: now,
          expectedRevision: command.expectedRevision,
        });
        return await persistMutation({
          investigation: next,
          expectedRevision: command.expectedRevision,
          expectedAuthority: {
            runId: command.runId,
            leaseId: command.leaseId,
            at: now,
          },
          occurredAt: now,
          eventType: 'AGENT_RUN_FAILED',
          auditAction: 'FAIL_AGENT_RUN',
          runId: command.runId,
        });
      } catch (error) {
        return mapApplicationError(error);
      }
    },

    async get(query) {
      try {
        return (await loadAuthorized(query.investigationId, 'READ_INVESTIGATION')).view();
      } catch (error) {
        return mapApplicationError(error);
      }
    },
  };
};
