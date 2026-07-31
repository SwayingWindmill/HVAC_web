import { createInvestigationCoordinator } from '../../dist/index.js';
import { InvestigationRepositoryConflictError } from '../../dist/application/index.js';

const wait = (milliseconds) => new Promise((resolve) => setTimeout(resolve, milliseconds));

class FakeBusinessStore {
  records = new Map();
  businessRecords = new Map();
  outboxEvents = [];
  auditRecords = [];
  saveCalls = [];
  nextConflict = null;

  repository = {
    get: async (investigationId) => this.records.get(investigationId) ?? null,
  };

  businessRecordRepository = {
    get: async (investigationId, recordId) => (
      this.businessRecords.get(`${investigationId}:${recordId}`) ?? null
    ),
  };

  transaction = {
    create: async ({ investigation, event, audit }) => {
      const view = investigation.view();
      if (this.records.has(view.id)) {
        throw new InvestigationRepositoryConflictError(
          'IDENTITY_CONFLICT',
          `Investigation ${view.id} already exists.`,
        );
      }
      this.records.set(view.id, investigation);
      this.outboxEvents.push(event);
      this.auditRecords.push(audit);
    },
    save: async ({
      investigation,
      expectedRevision,
      expectedAuthority,
      effect,
      record,
      event,
      audit,
    }) => {
      if (this.nextConflict !== null) {
        const conflict = this.nextConflict;
        this.nextConflict = null;
        throw new InvestigationRepositoryConflictError(conflict, `Forced ${conflict}.`);
      }

      const view = investigation.view();
      const current = this.records.get(view.id);
      if (current === undefined || current.view().revision !== expectedRevision) {
        throw new InvestigationRepositoryConflictError(
          'REVISION_CONFLICT',
          `Expected Investigation Revision ${expectedRevision}.`,
        );
      }
      if (expectedAuthority !== undefined) {
        const currentView = current.view();
        const run = currentView.runs.find(({ id }) => id === expectedAuthority.runId);
        if (run?.lease?.id !== expectedAuthority.leaseId
          || expectedAuthority.at >= run.lease.expiresAt) {
          throw new InvestigationRepositoryConflictError(
            'LEASE_CONFLICT',
            'The Fake Repository rejected a stale Agent Run Lease.',
          );
        }
      }

      if (record !== undefined) {
        this.businessRecords.set(`${view.id}:${record.id}`, record);
      }
      this.saveCalls.push({
        investigationId: view.id,
        expectedRevision,
        nextRevision: view.revision,
        effect: effect ?? null,
      });
      this.records.set(view.id, investigation);
      this.outboxEvents.push(event);
      this.auditRecords.push(audit);
    },
  };

  forceConflict(code) {
    this.nextConflict = code;
  }
}

class FakeCheckpointStore {
  records = new Map();

  key(investigationId, runId) {
    return `${investigationId}:${runId}`;
  }

  repository = {
    save: async (checkpoint) => {
      this.records.set(this.key(checkpoint.investigationId, checkpoint.runId), checkpoint);
    },
    load: async (investigationId, runId) => (
      this.records.get(this.key(investigationId, runId)) ?? null
    ),
    delete: async (investigationId, runId) => {
      this.records.delete(this.key(investigationId, runId));
    },
  };
}

class ScriptedFakeRuntime {
  constructor(steps) {
    this.steps = [...steps];
    this.calls = [];
    this.attemptedBusinessMutation = false;
  }

  async planReads(input) {
    const step = this.steps.shift();
    if (step === undefined) {
      throw new Error('Fake Runtime has no planned Investigation Step remaining.');
    }
    if (step.expectedCheckpointPosition !== undefined
      && input.checkpoint?.position !== step.expectedCheckpointPosition) {
      throw new Error(
        `Fake Runtime expected Checkpoint ${step.expectedCheckpointPosition}, got ${input.checkpoint?.position ?? 'none'}.`,
      );
    }

    this.calls.push({
      stepId: step.stepId,
      runId: input.runId,
      checkpointPosition: input.checkpoint?.position ?? null,
    });

    if (step.attemptBusinessMutation === true) {
      input.investigation.status = 'FAILED';
      input.investigation.evidenceIds.push('runtime-forged-evidence');
      this.attemptedBusinessMutation = true;
    }

    return {
      status: 'PLANNED',
      plan: step.plan,
      checkpoint: {
        position: step.checkpointPosition,
        opaqueState: JSON.stringify({ stepId: step.stepId }),
      },
    };
  }
}

const ownerForTool = (tool) => {
  if (tool === 'registry.getSite') return 'registry';
  if (tool === 'telemetry.getCurrentSnapshot') return 'telemetry-query-service';
  if (tool === 'analytics.getEnergySeries') return 'telemetry-query-service';
  return 'command-service';
};

const revisionForTool = (tool) => {
  if (tool === 'registry.getSite') return 'registry-revision-17';
  if (tool === 'telemetry.getCurrentSnapshot') return 'telemetry-revision-73';
  if (tool === 'analytics.getEnergySeries') return 'dataset-revision-29';
  return 'capability-revision-5';
};

class FakeOwnerReaders {
  constructor(scope, delayMs) {
    this.scope = scope;
    this.delayMs = delayMs;
    this.activeReads = 0;
    this.maxConcurrentReads = 0;
    this.calls = [];
  }

  async read({ request }) {
    this.activeReads += 1;
    this.maxConcurrentReads = Math.max(this.maxConcurrentReads, this.activeReads);
    this.calls.push({ requestId: request.requestId, tool: request.tool });
    try {
      await wait(this.delayMs);
      const equipmentId = 'equipmentId' in request.input ? request.input.equipmentId : null;
      return {
        requestId: request.requestId,
        owner: ownerForTool(request.tool),
        scope: {
          ...this.scope,
          equipmentId,
          deviceId: null,
        },
        revision: revisionForTool(request.tool),
        quality: 'GOOD',
        provenance: `fake-owner://${ownerForTool(request.tool)}/${request.requestId}`,
        payload: {
          tool: request.tool,
          input: request.input,
        },
      };
    } finally {
      this.activeReads -= 1;
    }
  }

  ports = {
    registry: { read: (request) => this.read(request) },
    currentTelemetry: { read: (request) => this.read(request) },
    energyAnalytics: { read: (request) => this.read(request) },
    commandCapabilities: { read: (request) => this.read(request) },
  };
}

const createIdentityGenerator = () => {
  const counters = new Map();
  return {
    next(kind) {
      const next = (counters.get(kind) ?? 0) + 1;
      counters.set(kind, next);
      return `${kind}-${String(next).padStart(3, '0')}`;
    },
  };
};

export const createFakeOperationsAgentEnvironment = ({
  scope,
  runtimeSteps,
  initialTime = 10_000,
  leaseDurationMs = 10_000,
  ownerDelayMs = 15,
}) => {
  let currentTime = initialTime;
  const businessStore = new FakeBusinessStore();
  const checkpointStore = new FakeCheckpointStore();
  const runtime = new ScriptedFakeRuntime(runtimeSteps);
  const owners = new FakeOwnerReaders(scope, ownerDelayMs);

  const applicationOutbox = {
    append: async (event) => businessStore.outboxEvents.push(event),
  };
  const auditRecorder = {
    record: async (record) => businessStore.auditRecords.push(record),
  };

  const coordinator = createInvestigationCoordinator({
    investigationRepository: businessStore.repository,
    businessRecordRepository: businessStore.businessRecordRepository,
    investigationTransaction: businessStore.transaction,
    authorizationDecisionReader: {
      async authorizeScope() {
        return {
          decision: 'ALLOW',
          decisionId: 'fake-authorization-allow',
          delegationGrant: 'fake-delegation-grant',
          policyRevision: 'fake-policy-revision',
        };
      },
    },
    agentExecutionRuntime: runtime,
    checkpointRepository: checkpointStore.repository,
    applicationOutbox,
    auditRecorder,
    budgetGuard: {
      async check() {
        return { decision: 'ALLOW' };
      },
    },
    ownerReaders: owners.ports,
    clock: { now: () => currentTime },
    idGenerator: createIdentityGenerator(),
    leaseDurationMs,
  });

  return Object.freeze({
    coordinator,
    businessStore,
    checkpointStore,
    runtime,
    owners,
    setTime(value) {
      currentTime = value;
    },
  });
};
