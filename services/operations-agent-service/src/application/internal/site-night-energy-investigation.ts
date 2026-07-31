import {
  businessRecordsEqual,
  createInvestigationBusinessRecord,
  type AnalysisReferenceRecord,
  type EvidenceQualityClassification,
  type EvidenceRecord,
  type EvidenceSourceReference,
  type FindingRecord,
  type InvestigationBusinessRecord,
  type InvestigationScope,
  type InvestigationStatus,
  type OperationsInvestigationView,
  type ToolExecutionReceiptRecord,
} from '../../domain/index.js';
import {
  InvestigationCoordinatorError,
  createInvestigationCoordinator,
  type InvestigationCoordinator,
} from './investigation-coordinator.js';
import {
  analyzeSiteNightEnergy,
  planSiteNightEnergyPeriods,
  type NightEnergyQualityPolicy,
  type NightEnergySeries,
  type SiteNightEnergyWindow,
} from './night-energy-analysis.js';
import type {
  AgentExecutionRuntime,
  AuthorizationDecision,
  InvestigationCoordinatorPorts,
  OwnerReadContext,
  OwnerReadResult,
  ParallelReadRequest,
} from './ports.js';
import { sha256Hex } from './sha256.js';

export interface SiteNightEnergyInvestigationPolicy {
  readonly runtimeRevision: string;
  readonly window: SiteNightEnergyWindow;
  readonly baselineOffsetDays: number;
  readonly increaseThresholdPercent: number;
  readonly qualityPolicy: NightEnergyQualityPolicy;
}

export type SiteNightEnergyInvestigationCoordinatorPorts = Omit<
  InvestigationCoordinatorPorts,
  'agentExecutionRuntime'
> & {
  readonly createAgentExecutionRuntime: (scope: InvestigationScope) => AgentExecutionRuntime;
};

export interface StartSiteNightEnergyInvestigationCommand {
  readonly organizationId: string;
  readonly siteId: string;
}

export interface SiteNightEnergyInvestigationQuery {
  readonly investigationId: string;
}

export interface SiteNightEnergyActiveRunView {
  readonly id: string;
  readonly status: 'ACTIVE' | 'PAUSED';
  readonly startedAt: number;
}

export interface SiteNightEnergyInvestigationView {
  readonly schemaVersion: 1;
  readonly id: string;
  readonly scope: InvestigationScope;
  readonly status: InvestigationStatus;
  readonly revision: number;
  readonly createdAt: number;
  readonly activeRun: SiteNightEnergyActiveRunView | null;
  readonly outcome: 'SUPPORTED_SITE_FINDING' | 'UNABLE_TO_CONCLUDE' | null;
  readonly evidence: readonly EvidenceRecord[];
  readonly analysisReferences: readonly AnalysisReferenceRecord[];
  readonly findings: readonly FindingRecord[];
  readonly toolReceipts: readonly ToolExecutionReceiptRecord[];
}

export interface SiteNightEnergyInvestigationCoordinator {
  start(command: StartSiteNightEnergyInvestigationCommand): Promise<SiteNightEnergyInvestigationView>;
  get(query: SiteNightEnergyInvestigationQuery): Promise<SiteNightEnergyInvestigationView>;
  advance(query: SiteNightEnergyInvestigationQuery): Promise<SiteNightEnergyInvestigationView>;
  cancel(query: SiteNightEnergyInvestigationQuery): Promise<SiteNightEnergyInvestigationView>;
}

interface RegistrySitePayload {
  readonly kind: 'SITE';
  readonly site: {
    readonly id: string;
    readonly owningOrganizationId: string;
    readonly timezone: string;
  };
}

interface RegistryEquipmentPayload {
  readonly kind: 'SITE_EQUIPMENT';
  readonly siteId: string;
  readonly equipment: readonly { readonly id: string }[];
}

const defaultPolicy: SiteNightEnergyInvestigationPolicy = Object.freeze({
  runtimeRevision: 'site-night-energy-investigation/v1',
  window: Object.freeze({ startLocalTime: '22:00', endLocalTime: '06:00' }),
  baselineOffsetDays: 7,
  increaseThresholdPercent: 10,
  qualityPolicy: 'VALID_AND_SUSPECT',
});

const isRecord = (value: unknown): value is Record<string, unknown> => (
  typeof value === 'object' && value !== null && !Array.isArray(value)
);

const canonicalize = (value: unknown): unknown => {
  if (Array.isArray(value)) return value.map(canonicalize);
  if (!isRecord(value)) return value;
  return Object.fromEntries(
    Object.entries(value)
      .sort(([left], [right]) => left.localeCompare(right))
      .map(([key, item]) => [key, canonicalize(item)]),
  );
};

const digest = (value: unknown): string => `sha256:${sha256Hex(JSON.stringify(canonicalize(value)))}`;

const requireIdentity = (value: string, label: string): string => {
  if (value.trim().length === 0 || value.length > 256) {
    throw new InvestigationCoordinatorError(
      'INVALID_INVESTIGATION_STATE',
      `${label} must contain 1 to 256 characters.`,
    );
  }
  return value;
};

const requireSiteScope = (scope: InvestigationScope): InvestigationScope => {
  if (scope.organizationId.trim().length === 0
    || scope.siteId === null
    || scope.siteId.trim().length === 0
    || scope.equipmentId !== null
    || scope.deviceId !== null) {
    throw new InvestigationCoordinatorError(
      'INVALID_INVESTIGATION_STATE',
      'Night-energy Investigations require an exact Site Scope.',
    );
  }
  return scope;
};

const localDateAt = (epochMs: number, timezone: string): string => {
  const parts = Object.fromEntries(
    new Intl.DateTimeFormat('en-US-u-ca-iso8601', {
      timeZone: timezone,
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
    }).formatToParts(epochMs).map(({ type, value }) => [type, value]),
  );
  return `${parts.year}-${parts.month}-${parts.day}`;
};

const shiftLocalDate = (value: string, days: number): string => {
  const [year, month, day] = value.split('-').map(Number);
  return new Date(Date.UTC(year ?? 0, (month ?? 1) - 1, (day ?? 1) + days))
    .toISOString()
    .slice(0, 10);
};

const ownerForTool = (tool: ParallelReadRequest['tool']): ToolExecutionReceiptRecord['owner'] => {
  if (tool === 'registry.getSite' || tool === 'registry.listSiteEquipment') return 'registry';
  if (tool === 'commands.getCapabilities') return 'command-service';
  return 'telemetry-query-service';
};

const qualityClassification = (quality: OwnerReadResult['quality']): EvidenceQualityClassification => quality;

const requireAllowed = (decision: AuthorizationDecision): AuthorizationDecision => {
  if (decision.decision !== 'ALLOW' || decision.decisionId.trim().length === 0) {
    throw new InvestigationCoordinatorError(
      'AUTHORIZATION_DENIED',
      'The requested Site Investigation is not authorized.',
    );
  }
  return decision;
};

const decodeSite = (result: OwnerReadResult, scope: InvestigationScope): RegistrySitePayload => {
  const payload = result.payload;
  if (!isRecord(payload)
    || payload.kind !== 'SITE'
    || !isRecord(payload.site)
    || payload.site.id !== scope.siteId
    || payload.site.owningOrganizationId !== scope.organizationId
    || typeof payload.site.timezone !== 'string'
    || payload.site.timezone.trim().length === 0) {
    throw new InvestigationCoordinatorError(
      'OWNER_RESPONSE_INVALID',
      'Registry returned Site data outside the Investigation Scope.',
    );
  }
  return payload as unknown as RegistrySitePayload;
};

const decodeEquipment = (
  result: OwnerReadResult,
  scope: InvestigationScope,
): RegistryEquipmentPayload => {
  const payload = result.payload;
  if (!isRecord(payload)
    || payload.kind !== 'SITE_EQUIPMENT'
    || payload.siteId !== scope.siteId
    || !Array.isArray(payload.equipment)
    || payload.equipment.length > 2_000
    || payload.equipment.some((item) => !isRecord(item) || typeof item.id !== 'string')) {
    throw new InvestigationCoordinatorError(
      'OWNER_RESPONSE_INVALID',
      'Registry returned Equipment data outside the Investigation Scope.',
    );
  }
  return payload as unknown as RegistryEquipmentPayload;
};

const decodeEnergy = (result: OwnerReadResult): NightEnergySeries => {
  if (!isRecord(result.payload)) {
    throw new InvestigationCoordinatorError(
      'OWNER_RESPONSE_INVALID',
      'Telemetry Query Service returned an invalid Energy Series.',
    );
  }
  return result.payload as unknown as NightEnergySeries;
};

const sourceFor = (
  result: OwnerReadResult,
  recordedAt: number,
): EvidenceSourceReference => {
  if (result.owner === 'registry') {
    return {
      owner: 'registry',
      scope: { ...result.scope },
      requestId: result.requestId,
      registryRevision: result.revision,
      datasetRevision: null,
      watermark: { data: null, aggregate: null },
      partial: false,
      quality: {
        classification: qualityClassification(result.quality),
        valid: 1,
        suspect: result.quality === 'UNCERTAIN' ? 1 : 0,
        invalid: result.quality === 'BAD' ? 1 : 0,
      },
      capturedAt: recordedAt,
      evaluatedAt: recordedAt,
      provenanceDigest: digest({
        requestId: result.requestId,
        revision: result.revision,
        provenance: result.provenance,
      }),
    };
  }
  const series = decodeEnergy(result);
  return {
    owner: 'telemetry-query-service',
    scope: { ...result.scope },
    requestId: result.requestId,
    registryRevision: null,
    datasetRevision: series.metadata.datasetRevision,
    watermark: {
      data: series.metadata.dataWatermark ?? null,
      aggregate: series.metadata.aggregateWatermark ?? null,
    },
    partial: series.metadata.partial,
    quality: {
      classification: qualityClassification(result.quality),
      valid: series.metadata.qualitySummary.valid,
      suspect: series.metadata.qualitySummary.suspect,
      invalid: series.metadata.qualitySummary.invalid,
    },
    capturedAt: recordedAt,
    evaluatedAt: recordedAt,
    provenanceDigest: digest({
      requestId: result.requestId,
      revision: result.revision,
      provenance: result.provenance,
      metadata: series.metadata,
    }),
  };
};

const recordId = (investigationId: string, suffix: string): string => (
  `${investigationId}:${suffix}`
);

const requestId = (investigationId: string, suffix: string): string => (
  `${investigationId}:${suffix}`
);

const activeAuthority = (view: OperationsInvestigationView): {
  readonly runId: string;
  readonly leaseId: string;
} => {
  const run = view.runs.find(({ id }) => id === view.activeRunId);
  if (run === undefined || run.status !== 'ACTIVE' || run.lease === null) {
    throw new InvestigationCoordinatorError(
      'LEASE_CONFLICT',
      'The Night-energy Investigation has no active writable Agent Run.',
    );
  }
  return { runId: run.id, leaseId: run.lease.id };
};

export const createSiteNightEnergyInvestigationCoordinator = (
  ports: SiteNightEnergyInvestigationCoordinatorPorts,
  configuredPolicy: Partial<SiteNightEnergyInvestigationPolicy> = {},
): SiteNightEnergyInvestigationCoordinator => {
  const policy: SiteNightEnergyInvestigationPolicy = Object.freeze({
    ...defaultPolicy,
    ...configuredPolicy,
    window: Object.freeze({
      ...defaultPolicy.window,
      ...configuredPolicy.window,
    }),
  });
  if (!Number.isSafeInteger(policy.baselineOffsetDays) || policy.baselineOffsetDays <= 0) {
    throw new Error('baselineOffsetDays must be a positive safe integer.');
  }

  const genericFor = (
    scope: InvestigationScope,
    fixedNow?: number,
  ): InvestigationCoordinator => (
    createInvestigationCoordinator({
      ...ports,
      ...(fixedNow === undefined ? {} : { clock: { now: () => fixedNow } }),
      agentExecutionRuntime: ports.createAgentExecutionRuntime(scope),
    })
  );

  const loadAuthorizedView = async (
    investigationId: string,
  ): Promise<OperationsInvestigationView> => {
    const persisted = await ports.investigationRepository.get(investigationId);
    if (persisted === null) {
      throw new InvestigationCoordinatorError(
        'INVESTIGATION_NOT_FOUND',
        'The requested Investigation was not found.',
      );
    }
    return genericFor(requireSiteScope(persisted.view().scope)).get({ investigationId });
  };

  const ensureRecord = async <TRecord extends InvestigationBusinessRecord>(
    investigationId: string,
    identity: string,
    create: (recordedAt: number) => TRecord,
    newRecordedAt = ports.clock.now(),
  ): Promise<TRecord> => {
    const existing = await ports.businessRecordRepository.get(investigationId, identity);
    const recordedAt = existing?.recordedAt ?? newRecordedAt;
    const candidate = createInvestigationBusinessRecord(create(recordedAt)) as TRecord;
    if (existing !== null && !businessRecordsEqual(existing, candidate)) {
      throw new InvestigationCoordinatorError(
        'DUPLICATE_RECORD',
        `Business record ${identity} already exists with different authoritative content.`,
      );
    }
    return (existing ?? candidate) as TRecord;
  };

  const commitRecord = async (
    coordinator: InvestigationCoordinator,
    view: OperationsInvestigationView,
    stepId: string,
    record: InvestigationBusinessRecord,
  ): Promise<OperationsInvestigationView> => {
    const authority = activeAuthority(view);
    const result = await coordinator.commitEffect({
      investigationId: view.id,
      runId: authority.runId,
      leaseId: authority.leaseId,
      expectedRevision: view.revision,
      stepId,
      idempotencyKey: `${record.id}:commit`,
      kind: record.recordType,
      recordId: record.id,
      record,
    });
    return result.investigation;
  };

  const receiptFor = async (
    investigationId: string,
    runId: string,
    stepId: string,
    request: ParallelReadRequest,
    result: OwnerReadResult,
    operationTime: number,
  ): Promise<ToolExecutionReceiptRecord> => {
    const identity = recordId(investigationId, `receipt:${request.requestId}`);
    return ensureRecord(investigationId, identity, (recordedAt) => ({
      schemaVersion: 1,
      recordType: 'TOOL_EXECUTION_RECEIPT',
      id: identity,
      investigationId,
      recordedAt,
      logicalTool: request.tool,
      owner: ownerForTool(request.tool),
      requestId: request.requestId,
      attemptId: `${request.requestId}:attempt:1`,
      runId,
      stepId: stepId as ToolExecutionReceiptRecord['stepId'],
      startedAt: recordedAt,
      completedAt: recordedAt,
      resultCategory: 'SUCCEEDED',
      metadata: {
        revision: result.revision,
        quality: result.quality,
        provenanceDigest: digest(result.provenance),
      },
    }), operationTime);
  };

  const readDirect = async (
    request: ParallelReadRequest,
    context: OwnerReadContext,
  ): Promise<OwnerReadResult> => {
    const toolGrant = ports.toolAuthorizationReader === undefined
      ? undefined
      : await ports.toolAuthorizationReader.authorize({ request, context });
    const authorizedContext: OwnerReadContext = toolGrant === undefined
      ? context
      : {
        ...context,
        authorization: {
          ...context.authorization,
          delegationGrant: toolGrant.delegationGrant,
          toolDelegationGrants: {
            ...context.authorization.toolDelegationGrants,
            [request.tool]: toolGrant.delegationGrant,
          },
          ...(toolGrant.policyRevision === undefined
            ? {}
            : { policyRevision: toolGrant.policyRevision }),
        },
      };
    if (request.tool === 'registry.getSite' || request.tool === 'registry.listSiteEquipment') {
      return ports.ownerReaders.registry.read({ request, context: authorizedContext });
    }
    if (request.tool === 'analytics.getEnergySeries') {
      return ports.ownerReaders.energyAnalytics.read({ request, context: authorizedContext });
    }
    throw new InvestigationCoordinatorError(
      'OWNER_REQUEST_INVALID',
      'The Night-energy Investigation requested an unsupported logical Tool.',
    );
  };

  const snapshot = async (
    view: OperationsInvestigationView,
  ): Promise<SiteNightEnergyInvestigationView> => {
    const persisted = await ports.investigationRepository.get(view.id);
    if (persisted === null) {
      throw new InvestigationCoordinatorError(
        'INVESTIGATION_NOT_FOUND',
        'The requested Investigation was not found.',
      );
    }
    const records = await Promise.all([
      ...view.evidenceIds,
      ...view.analysisReferenceIds,
      ...view.findingIds,
      ...view.toolReceiptIds,
    ].map((identity) => ports.businessRecordRepository.get(view.id, identity)));
    if (records.some((record) => record === null)) {
      throw new InvestigationCoordinatorError(
        'INVALID_INVESTIGATION_STATE',
        'The Investigation references a missing typed business record.',
      );
    }
    const typed = records as InvestigationBusinessRecord[];
    const evidence = typed.filter((record): record is EvidenceRecord => record.recordType === 'EVIDENCE');
    const analysisReferences = typed.filter(
      (record): record is AnalysisReferenceRecord => record.recordType === 'ANALYSIS_REFERENCE',
    );
    const findings = typed.filter((record): record is FindingRecord => record.recordType === 'FINDING');
    const toolReceipts = typed.filter(
      (record): record is ToolExecutionReceiptRecord => record.recordType === 'TOOL_EXECUTION_RECEIPT',
    );
    const active = view.runs.find(({ id }) => id === view.activeRunId);
    const lastFinding = findings.at(-1);
    return {
      schemaVersion: 1,
      id: view.id,
      scope: { ...view.scope },
      status: view.status,
      revision: view.revision,
      createdAt: persisted.snapshot().createdAt,
      activeRun: active === undefined || (active.status !== 'ACTIVE' && active.status !== 'PAUSED')
        ? null
        : { id: active.id, status: active.status, startedAt: active.startedAt },
      outcome: lastFinding === undefined
        ? null
        : lastFinding.conclusion.status === 'SUPPORTED'
          ? 'SUPPORTED_SITE_FINDING'
          : 'UNABLE_TO_CONCLUDE',
      evidence,
      analysisReferences,
      findings,
      toolReceipts,
    };
  };

  const registryRequests = (investigationId: string, siteId: string): readonly [
    ParallelReadRequest,
    ParallelReadRequest,
  ] => [{
    requestId: requestId(investigationId, 'registry-site'),
    tool: 'registry.getSite',
    input: { siteId },
  }, {
    requestId: requestId(investigationId, 'registry-equipment'),
    tool: 'registry.listSiteEquipment',
    input: { siteId },
  }];

  return Object.freeze({
    async start(command: StartSiteNightEnergyInvestigationCommand) {
      const scope = requireSiteScope({
        organizationId: requireIdentity(command.organizationId, 'Organization identity'),
        siteId: requireIdentity(command.siteId, 'Site identity'),
        equipmentId: null,
        deviceId: null,
      });
      const coordinator = genericFor(scope);
      const created = await coordinator.create({ scope });
      const started = await coordinator.start({
        investigationId: created.id,
        runtimeRevision: policy.runtimeRevision,
        expectedRevision: created.revision,
      });
      return snapshot(started);
    },

    async get(query: SiteNightEnergyInvestigationQuery) {
      return snapshot(await loadAuthorizedView(requireIdentity(
        query.investigationId,
        'Investigation identity',
      )));
    },

    async cancel(query: SiteNightEnergyInvestigationQuery) {
      const view = await loadAuthorizedView(requireIdentity(
        query.investigationId,
        'Investigation identity',
      ));
      if (view.status === 'CANCELLED') return snapshot(view);
      const cancelled = await genericFor(view.scope).cancel({
        investigationId: view.id,
        expectedRevision: view.revision,
      });
      return snapshot(cancelled);
    },

    async advance(query: SiteNightEnergyInvestigationQuery) {
      let view = await loadAuthorizedView(requireIdentity(
        query.investigationId,
        'Investigation identity',
      ));
      if (view.status === 'COMPLETED') return snapshot(view);
      if (view.status !== 'RUNNING') {
        throw new InvestigationCoordinatorError(
          'INVALID_INVESTIGATION_STATE',
          'Only a running Night-energy Investigation can advance.',
        );
      }
      const scope = requireSiteScope(view.scope);
      const siteId = scope.siteId;
      if (siteId === null) {
        throw new InvestigationCoordinatorError(
          'INVALID_INVESTIGATION_STATE',
          'Night-energy Investigations require a Site identity.',
        );
      }
      const operationTime = ports.clock.now();
      const coordinator = genericFor(scope, operationTime);
      const authority = activeAuthority(view);
      const authorization = requireAllowed(await ports.authorizationDecisionReader.authorizeScope({
        scope,
        action: 'ADVANCE_AGENT_RUN',
      }));
      const context: OwnerReadContext = {
        investigationId: view.id,
        runId: authority.runId,
        scope,
        authorization,
        correlationId: `${view.id}:${authority.runId}`,
      };
      const registry = registryRequests(view.id, siteId);
      const registryReceiptIdentities = registry.map((request) => (
        recordId(view.id, `receipt:${request.requestId}`)
      ));
      let registryResults: readonly OwnerReadResult[];
      const existingCheckpoint = await ports.checkpointRepository.load(view.id, authority.runId);
      if (existingCheckpoint !== null
        || registryReceiptIdentities.every((identity) => view.toolReceiptIds.includes(identity))) {
        registryResults = await Promise.all(registry.map((request) => readDirect(request, context)));
      } else {
        const advanced = await coordinator.advance({
          investigationId: view.id,
          runId: authority.runId,
          leaseId: authority.leaseId,
          expectedRevision: view.revision,
        });
        registryResults = advanced.results;
        if (advanced.plan.batches.length !== 1
          || registryResults.length !== 2
          || !registry.every((request) => (
            registryResults.some((result) => result.requestId === request.requestId)
          ))) {
          throw new InvestigationCoordinatorError(
            'INVALID_INVESTIGATION_STATE',
            'The Runtime READ plan is outside the Site night-energy program.',
          );
        }
        view = advanced.investigation;
      }
      const siteResult = registryResults.find(({ requestId: identity }) => identity === registry[0].requestId);
      const equipmentResult = registryResults.find(
        ({ requestId: identity }) => identity === registry[1].requestId,
      );
      if (siteResult === undefined || equipmentResult === undefined) {
        throw new InvestigationCoordinatorError(
          'OWNER_RESPONSE_INVALID',
          'The Runtime did not return complete Registry Site context.',
        );
      }
      const site = decodeSite(siteResult, scope);
      const equipment = decodeEquipment(equipmentResult, scope);
      for (const request of registry) {
        const result = registryResults.find(({ requestId: identity }) => identity === request.requestId);
        if (result === undefined) continue;
        const receipt = await receiptFor(
          view.id,
          authority.runId,
          'collect-registry-context',
          request,
          result,
          operationTime,
        );
        view = await commitRecord(coordinator, view, 'collect-registry-context', receipt);
      }

      const targetLocalDate = shiftLocalDate(localDateAt(
        view.runs.find(({ id }) => id === authority.runId)?.startedAt ?? operationTime,
        site.site.timezone,
      ), -1);
      const baselineLocalDate = shiftLocalDate(targetLocalDate, -policy.baselineOffsetDays);
      const periods = planSiteNightEnergyPeriods({
        timezone: site.site.timezone,
        window: policy.window,
        targetLocalDate,
        baselineLocalDate,
      });
      const energyRequests: readonly [ParallelReadRequest, ParallelReadRequest] = [{
        requestId: requestId(view.id, 'energy-target'),
        tool: 'analytics.getEnergySeries',
        input: {
          organizationId: scope.organizationId,
          siteId,
          energyType: 'electricity',
          granularity: 'hour',
          timezone: site.site.timezone,
          from: periods.targetPeriod.from,
          to: periods.targetPeriod.to,
          qualityPolicy: policy.qualityPolicy,
        },
      }, {
        requestId: requestId(view.id, 'energy-baseline'),
        tool: 'analytics.getEnergySeries',
        input: {
          organizationId: scope.organizationId,
          siteId,
          energyType: 'electricity',
          granularity: 'hour',
          timezone: site.site.timezone,
          from: periods.baselinePeriod.from,
          to: periods.baselinePeriod.to,
          qualityPolicy: policy.qualityPolicy,
        },
      }];
      const energyResults = await Promise.all(energyRequests.map((request) => readDirect(request, context)));
      for (const [index, request] of energyRequests.entries()) {
        const receipt = await receiptFor(
          view.id,
          authority.runId,
          'collect-energy-periods',
          request,
          energyResults[index] as OwnerReadResult,
          operationTime,
        );
        view = await commitRecord(coordinator, view, 'collect-energy-periods', receipt);
      }

      const analysis = analyzeSiteNightEnergy({
        site: {
          organizationId: scope.organizationId,
          siteId,
          timezone: site.site.timezone,
          equipmentIds: equipment.equipment.map(({ id }) => id),
        },
        window: policy.window,
        targetLocalDate,
        baselineLocalDate,
        increaseThresholdPercent: policy.increaseThresholdPercent,
        qualityPolicy: policy.qualityPolicy,
        targetSeries: decodeEnergy(energyResults[0] as OwnerReadResult),
        baselineSeries: decodeEnergy(energyResults[1] as OwnerReadResult),
      });
      const allResults = [siteResult, equipmentResult, ...energyResults];
      const readinessIdentity = recordId(view.id, 'evidence:energy-readiness');
      const readiness = await ensureRecord(view.id, readinessIdentity, (recordedAt) => ({
        schemaVersion: 1,
        recordType: 'EVIDENCE',
        id: readinessIdentity,
        investigationId: view.id,
        recordedAt,
        evidenceKind: analysis.status === 'SUPPORTED_SITE_FINDING'
          ? 'SITE_ENERGY_SERIES_READY'
          : 'SITE_ENERGY_SERIES_READINESS_ASSESSED',
        classification: 'FACT',
        statement: analysis.status === 'SUPPORTED_SITE_FINDING'
          ? analysis.evidence[0].statement
          : `Energy Series readiness failed: ${analysis.blockers.map(({ code }) => code).join(', ')}.`,
        analysisReferenceDigest: analysis.analysisReference.digest,
        sources: allResults.map((result) => sourceFor(result, recordedAt)),
      }), operationTime);
      view = await commitRecord(coordinator, view, 'evaluate-energy-readiness', readiness);

      const analysisIdentity = recordId(view.id, 'analysis:night-energy-comparison');
      const analysisRecord = await ensureRecord(view.id, analysisIdentity, (recordedAt) => ({
        schemaVersion: 1,
        recordType: 'ANALYSIS_REFERENCE',
        id: analysisIdentity,
        investigationId: view.id,
        recordedAt,
        analysisKind: 'SITE_NIGHT_ENERGY_COMPARISON',
        authority: 'DETERMINISTIC_ALGORITHM',
        algorithmVersion: analysis.analysisReference.algorithmVersion,
        policyVersion: 'site-night-energy-policy/v1',
        inputEvidenceIds: [readiness.id],
        parameterDigest: digest({
          window: policy.window,
          targetLocalDate,
          baselineLocalDate,
          threshold: policy.increaseThresholdPercent,
          qualityPolicy: policy.qualityPolicy,
        }),
        resultDigest: analysis.analysisReference.digest,
        executedAt: recordedAt,
        outcome: analysis.status,
      }), operationTime);
      view = await commitRecord(coordinator, view, 'analyze-night-energy', analysisRecord);

      const evidenceIds: string[] = [readiness.id];
      if (analysis.status === 'SUPPORTED_SITE_FINDING') {
        const comparisonIdentity = recordId(view.id, 'evidence:energy-comparison');
        const comparison = await ensureRecord(view.id, comparisonIdentity, (recordedAt) => ({
          schemaVersion: 1,
          recordType: 'EVIDENCE',
          id: comparisonIdentity,
          investigationId: view.id,
          recordedAt,
          evidenceKind: 'SITE_ENERGY_PERIOD_COMPARISON',
          classification: 'ALGORITHM_RESULT',
          statement: analysis.evidence[1].statement,
          analysisReferenceDigest: analysis.analysisReference.digest,
          sources: energyResults.map((result) => sourceFor(result, recordedAt)),
        }), operationTime);
        view = await commitRecord(coordinator, view, 'record-energy-comparison', comparison);
        evidenceIds.push(comparison.id);
      }

      const findingIdentity = recordId(view.id, 'finding:night-energy');
      const finding = await ensureRecord(view.id, findingIdentity, (recordedAt): FindingRecord => (
        analysis.status === 'SUPPORTED_SITE_FINDING'
          ? {
            schemaVersion: 1,
            recordType: 'FINDING',
            id: findingIdentity,
            investigationId: view.id,
            recordedAt,
            findingKind: analysis.siteFinding.kind,
            classification: 'INFERENCE',
            statement: analysis.siteFinding.statement,
            evidenceIds,
            analysisReferenceIds: [analysisRecord.id],
            conclusion: {
              status: 'SUPPORTED',
              scope: 'SITE',
              organizationId: scope.organizationId,
              siteId,
            },
          }
          : {
            schemaVersion: 1,
            recordType: 'FINDING',
            id: findingIdentity,
            investigationId: view.id,
            recordedAt,
            findingKind: 'UNABLE_TO_CONCLUDE',
            classification: 'INFERENCE',
            statement: 'The Site night-energy Investigation cannot produce a supported conclusion.',
            evidenceIds,
            analysisReferenceIds: [analysisRecord.id],
            conclusion: {
              status: 'UNABLE_TO_CONCLUDE',
              scope: 'SITE',
              reasonCode: analysis.blockers[0]?.code ?? 'ENERGY_READINESS_FAILED',
              detail: analysis.blockers.map(({ detail }) => detail).join(' '),
            },
          }
      ), operationTime);
      view = await commitRecord(coordinator, view, 'record-night-energy-finding', finding);

      const latestAuthority = activeAuthority(view);
      view = await coordinator.complete({
        investigationId: view.id,
        runId: latestAuthority.runId,
        leaseId: latestAuthority.leaseId,
        expectedRevision: view.revision,
      });
      return snapshot(view);
    },
  });
};
