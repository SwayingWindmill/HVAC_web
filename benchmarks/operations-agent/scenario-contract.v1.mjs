import { z } from 'zod';

export const OPERATIONS_AGENT_SCENARIO_CONTRACT_VERSION = 'operations-agent-scenario/v1';
export const OPERATIONS_AGENT_TOOL_CATALOG_VERSION = 'operations-agent-tool-catalog/v1';

export const OPERATIONS_AGENT_TOOL_CATALOG = Object.freeze({
  'authorization.checkScope': 'iam-service',
  'registry.getSite': 'platform-core-service',
  'registry.getEquipment': 'platform-core-service',
  'registry.listSiteEquipment': 'platform-core-service',
  'registry.getEquipmentEnergyBindings': 'platform-core-service',
  'telemetry.current.getEquipmentState': 'telemetry-runtime-service',
  'telemetry.current.getDeviceObservationSnapshot': 'telemetry-runtime-service',
  'analytics.energy.getSiteSeries': 'telemetry-query-service',
  'analytics.energy.compareSitePeriods': 'telemetry-query-service',
  'analytics.energy.getEquipmentSeries': 'telemetry-query-service',
  'commands.createIntent': 'command-service',
  'commands.getIntent': 'command-service',
  'commands.approveIntent': 'command-service',
  'audit.getRecord': 'audit-ledger-service',
});

const identifier = z.string().min(1).regex(/^[A-Za-z0-9][A-Za-z0-9._:-]*$/);
const nonEmptyText = z.string().trim().min(1);
const isoDateTime = z.string().datetime({ offset: true });
const logicalTool = z.string().min(1);

const canonicalScopeSchema = z.object({
  organizationId: identifier,
  siteIds: z.array(identifier).min(1),
  equipmentIds: z.array(identifier).default([]),
  deviceIds: z.array(identifier).default([]),
  timeRange: z.object({
    from: isoDateTime,
    to: isoDateTime,
  }).strict().optional(),
}).strict();

const factSchema = z.object({
  id: identifier,
  kind: identifier,
  ownerTool: logicalTool,
  scopeBasis: z.enum(['AUTHORIZED', 'REQUESTED']).default('AUTHORIZED'),
  scope: canonicalScopeSchema,
  metadata: z.object({
    businessRevision: z.union([z.string().min(1), z.number().int().nonnegative()]).optional(),
    datasetRevision: z.string().min(1).optional(),
    watermark: isoDateTime.optional(),
    partial: z.boolean().optional(),
    freshness: z.enum(['FRESH', 'STALE', 'MISSING']).optional(),
    quality: z.enum(['GOOD', 'SUSPECT', 'REJECTED', 'UNKNOWN']),
    evaluatedAt: isoDateTime.optional(),
    capturedAt: isoDateTime,
    payloadDigest: z.string().min(1).optional(),
  }).strict(),
  payload: z.record(z.unknown()),
}).strict();

const outcomeSchema = z.object({
  id: identifier,
  classification: z.enum([
    'FACT',
    'ALGORITHM_RESULT',
    'INFERENCE',
    'HYPOTHESIS',
    'UNABLE_TO_CONCLUDE',
  ]),
  statement: nonEmptyText,
  evidenceRequirementIds: z.array(identifier),
  required: z.boolean(),
}).strict();

const evidenceRequirementSchema = z.object({
  id: identifier,
  kind: identifier,
  ownerTool: logicalTool,
  status: z.enum(['AVAILABLE', 'REQUIRED_NEXT']).default('AVAILABLE'),
  scopeBasis: z.enum(['AUTHORIZED', 'REQUESTED']).default('AUTHORIZED'),
  scope: canonicalScopeSchema,
  factIds: z.array(identifier),
  requiredMetadata: z.array(z.enum([
    'BUSINESS_REVISION',
    'DATASET_REVISION',
    'WATERMARK',
    'PARTIAL',
    'FRESHNESS',
    'QUALITY',
    'EVALUATED_AT',
    'CAPTURED_AT',
    'PAYLOAD_DIGEST',
  ])),
}).strict();

const requiredMetadataFields = Object.freeze({
  BUSINESS_REVISION: 'businessRevision',
  DATASET_REVISION: 'datasetRevision',
  WATERMARK: 'watermark',
  PARTIAL: 'partial',
  FRESHNESS: 'freshness',
  QUALITY: 'quality',
  EVALUATED_AT: 'evaluatedAt',
  CAPTURED_AT: 'capturedAt',
  PAYLOAD_DIGEST: 'payloadDigest',
});

const dataQualityConditionSchema = z.object({
  id: identifier,
  appliesToFactId: identifier,
  condition: z.enum([
    'PARTIAL_DATASET',
    'INSUFFICIENT_COVERAGE',
    'EQUIPMENT_NOT_OPERATING',
    'STALE_CURRENT_STATE',
    'SUSPECT_QUALITY',
    'MISSING_REQUIRED_SENSOR',
    'TIME_RANGE_TOO_SHORT',
    'BASELINE_NOT_COMPARABLE',
  ]),
  required: z.boolean(),
  failureOutcomeId: identifier,
}).strict();

const planningNodeSchema = z.object({
  id: identifier,
  label: nonEmptyText,
  dependsOn: z.array(identifier),
}).strict();

const executionNodeSchema = z.object({
  id: identifier,
  kind: z.enum(['TOOL_CALL', 'VALIDATION', 'DECISION', 'FINAL_OUTPUT']),
  label: nonEmptyText,
  dependsOn: z.array(identifier),
  tool: logicalTool.optional(),
}).strict();

const criterionDimension = z.enum([
  'SCOPE_ACCURACY',
  'AUTHORIZATION_COMPLIANCE',
  'DATA_RETRIEVAL_ACCURACY',
  'EVIDENCE_COMPLETENESS',
  'DATA_QUALITY_AWARENESS',
  'DIAGNOSTIC_CORRECTNESS',
  'SAFETY_COMPLIANCE',
  'OPERATIONAL_USEFULNESS',
]);

const blockerCriterionSchema = z.object({
  id: identifier,
  dimension: criterionDimension,
  description: nonEmptyText,
}).strict();

const scoredCriterionSchema = blockerCriterionSchema.extend({
  weight: z.number().positive(),
}).strict();

const forbiddenPathSchema = z.enum([
  'DIRECT_CLICKHOUSE_SQL',
  'ARBITRARY_CUBE_QUERY',
  'THINGSBOARD_READ_THROUGH',
  'LEGACY_AGENT_MOCK',
  'PHYSICAL_COMMAND_EXECUTION',
  'HISTORICAL_AS_CURRENT_STATE',
  'UNAUTHORIZED_RESOURCE_DISCLOSURE',
]);

const actionLifecycleSchema = z.object({
  proposedAction: z.enum(['EXPECTED', 'ALLOWED', 'MUST_NOT_CREATE']),
  formalApproval: z.enum(['NOT_PRESENT', 'ALLOWED', 'REQUIRED']),
  commandIntent: z.enum(['NOT_PRESENT', 'ALLOWED', 'MUST_NOT_CREATE']),
  physicalExecutionResult: z.enum(['NOT_PRESENT', 'ALLOWED', 'MUST_NOT_CLAIM']),
}).strict();

const operationsAgentScenarioSchemaV1 = z.object({
  contractVersion: z.literal(OPERATIONS_AGENT_SCENARIO_CONTRACT_VERSION),
  toolCatalogVersion: z.literal(OPERATIONS_AGENT_TOOL_CATALOG_VERSION),
  scenarioId: identifier,
  scenarioVersion: z.string().regex(/^\d+\.\d+\.\d+$/),
  title: nonEmptyText,
  userUtterance: nonEmptyText,
  deterministic: z.boolean(),
  purpose: z.enum(['RETROSPECTIVE', 'PREDICTIVE', 'PRESCRIPTIVE']),
  taskCategories: z.array(z.enum([
    'KNOWLEDGE_QUERY',
    'DATA_QUERY',
    'DIAGNOSTIC_ANALYSIS',
    'ROOT_CAUSE_ANALYSIS',
    'DECISION_SUPPORT',
    'ACTION_PROPOSAL',
  ])).min(1),
  scope: canonicalScopeSchema,
  requestedScope: canonicalScopeSchema.optional(),
  inputFacts: z.array(factSchema).min(1),
  groundTruth: z.object({ outcomes: z.array(outcomeSchema).min(1) }).strict(),
  evidenceRequirements: z.array(evidenceRequirementSchema).min(1),
  dataQuality: z.object({ conditions: z.array(dataQualityConditionSchema) }).strict(),
  planningDag: z.object({ nodes: z.array(planningNodeSchema).min(1) }).strict(),
  executionDag: z.object({ nodes: z.array(executionNodeSchema).min(1) }).strict(),
  tools: z.object({
    allowed: z.array(logicalTool),
    forbidden: z.array(logicalTool),
    forbiddenPaths: z.array(forbiddenPathSchema).default([]),
  }).strict(),
  actionLifecycle: actionLifecycleSchema.optional(),
  acceptance: z.object({
    blockers: z.array(blockerCriterionSchema).min(1),
    scored: z.array(scoredCriterionSchema),
  }).strict(),
}).strict();

const error = (code, path, message) => ({ code, path, message });
const jsonPath = (segments) => segments.reduce(
  (path, segment) => typeof segment === 'number' ? `${path}[${segment}]` : `${path}.${segment}`,
  '$',
);

const pushDuplicateErrors = (items, basePath, errors) => {
  const firstIndex = new Map();
  items.forEach((item, index) => {
    if (firstIndex.has(item.id)) {
      errors.push(error(
        'DUPLICATE_ID',
        `${basePath}[${index}].id`,
        `ID ${item.id} duplicates ${basePath}[${firstIndex.get(item.id)}].id.`,
      ));
      return;
    }
    firstIndex.set(item.id, index);
  });
};

const pushDuplicateValues = (values, basePath, errors) => {
  const firstIndex = new Map();
  values.forEach((value, index) => {
    if (firstIndex.has(value)) {
      errors.push(error(
        'DUPLICATE_VALUE',
        `${basePath}[${index}]`,
        `Value ${value} duplicates ${basePath}[${firstIndex.get(value)}].`,
      ));
      return;
    }
    firstIndex.set(value, index);
  });
};

const validateDag = (nodes, basePath, errors) => {
  pushDuplicateErrors(nodes, `${basePath}.nodes`, errors);
  const ids = new Set(nodes.map((node) => node.id));

  nodes.forEach((node, nodeIndex) => {
    pushDuplicateValues(node.dependsOn, `${basePath}.nodes[${nodeIndex}].dependsOn`, errors);
    node.dependsOn.forEach((dependency, dependencyIndex) => {
      if (!ids.has(dependency)) {
        errors.push(error(
          'DANGLING_REFERENCE',
          `${basePath}.nodes[${nodeIndex}].dependsOn[${dependencyIndex}]`,
          `Dependency ${dependency} does not identify a node in ${basePath}.`,
        ));
      }
    });
  });

  const byId = new Map(nodes.map((node) => [node.id, node]));
  const visiting = new Set();
  const visited = new Set();
  const visit = (nodeId, trail) => {
    if (visited.has(nodeId) || !byId.has(nodeId)) return;
    if (visiting.has(nodeId)) {
      const start = trail.indexOf(nodeId);
      const cycle = [...trail.slice(start), nodeId];
      errors.push(error('DAG_CYCLE', basePath, `Dependency cycle detected: ${cycle.join(' -> ')}.`));
      return;
    }

    visiting.add(nodeId);
    const nextTrail = [...trail, nodeId];
    for (const dependency of byId.get(nodeId).dependsOn) visit(dependency, nextTrail);
    visiting.delete(nodeId);
    visited.add(nodeId);
  };

  for (const node of nodes) visit(node.id, []);
};

const validateKnownTool = (tool, path, errors) => {
  if (!Object.hasOwn(OPERATIONS_AGENT_TOOL_CATALOG, tool)) {
    errors.push(error('UNKNOWN_TOOL', path, `Logical tool ${tool} is not in the v1 tool catalog.`));
  }
};

const validateReferences = (scenario, errors) => {
  const factById = new Map(scenario.inputFacts.map((fact) => [fact.id, fact]));
  const factIds = new Set(factById.keys());
  const evidenceIds = new Set(scenario.evidenceRequirements.map(({ id }) => id));
  const outcomeIds = new Set(scenario.groundTruth.outcomes.map(({ id }) => id));

  scenario.groundTruth.outcomes.forEach((outcome, outcomeIndex) => {
    outcome.evidenceRequirementIds.forEach((evidenceId, evidenceIndex) => {
      if (!evidenceIds.has(evidenceId)) {
        errors.push(error(
          'DANGLING_REFERENCE',
          `$.groundTruth.outcomes[${outcomeIndex}].evidenceRequirementIds[${evidenceIndex}]`,
          `Evidence requirement ${evidenceId} does not exist.`,
        ));
      }
    });
  });

  scenario.evidenceRequirements.forEach((requirement, requirementIndex) => {
    if (requirement.status === 'AVAILABLE' && requirement.factIds.length === 0) {
      errors.push(error(
        'EVIDENCE_STATUS_CONFLICT',
        `$.evidenceRequirements[${requirementIndex}].factIds`,
        'AVAILABLE Evidence must identify at least one input fact.',
      ));
    }
    if (requirement.status === 'REQUIRED_NEXT' && requirement.factIds.length > 0) {
      errors.push(error(
        'EVIDENCE_STATUS_CONFLICT',
        `$.evidenceRequirements[${requirementIndex}].factIds`,
        'REQUIRED_NEXT Evidence must not claim an available input fact.',
      ));
    }

    requirement.factIds.forEach((factId, factIndex) => {
      if (!factIds.has(factId)) {
        errors.push(error(
          'DANGLING_REFERENCE',
          `$.evidenceRequirements[${requirementIndex}].factIds[${factIndex}]`,
          `Input fact ${factId} does not exist.`,
        ));
        return;
      }

      if (requirement.status !== 'AVAILABLE') return;

      const fact = factById.get(factId);
      if (fact.scopeBasis !== requirement.scopeBasis) {
        errors.push(error(
          'EVIDENCE_SCOPE_BASIS_MISMATCH',
          `$.evidenceRequirements[${requirementIndex}].scopeBasis`,
          `Evidence ${requirement.id} and input fact ${factId} must use the same Scope basis.`,
        ));
      }
      requirement.requiredMetadata.forEach((metadataName, metadataIndex) => {
        const field = requiredMetadataFields[metadataName];
        if (fact.metadata[field] === undefined) {
          errors.push(error(
            'MISSING_REQUIRED_METADATA',
            `$.evidenceRequirements[${requirementIndex}].requiredMetadata[${metadataIndex}]`,
            `Input fact ${factId} does not provide required metadata ${metadataName}.`,
          ));
        }
      });
    });
  });

  scenario.dataQuality.conditions.forEach((condition, conditionIndex) => {
    if (!factIds.has(condition.appliesToFactId)) {
      errors.push(error(
        'DANGLING_REFERENCE',
        `$.dataQuality.conditions[${conditionIndex}].appliesToFactId`,
        `Input fact ${condition.appliesToFactId} does not exist.`,
      ));
    }
    if (!outcomeIds.has(condition.failureOutcomeId)) {
      errors.push(error(
        'DANGLING_REFERENCE',
        `$.dataQuality.conditions[${conditionIndex}].failureOutcomeId`,
        `Ground Truth outcome ${condition.failureOutcomeId} does not exist.`,
      ));
    }
  });
};

const validateTimeRange = (timeRange, path, errors) => {
  if (timeRange && Date.parse(timeRange.from) >= Date.parse(timeRange.to)) {
    errors.push(error(
      'INVALID_TIME_RANGE',
      path,
      'Scope time range must use inclusive from and exclusive to with from earlier than to.',
    ));
  }
};

const validateScopeContainment = (scenario, errors) => {
  const validateBoundary = (scope, basePath) => {
    pushDuplicateValues(scope.siteIds, `${basePath}.siteIds`, errors);
    pushDuplicateValues(scope.equipmentIds, `${basePath}.equipmentIds`, errors);
    pushDuplicateValues(scope.deviceIds, `${basePath}.deviceIds`, errors);
    validateTimeRange(scope.timeRange, `${basePath}.timeRange`, errors);
  };

  const validateScope = (scope, scopeBasis, basePath) => {
    validateBoundary(scope, basePath);
    const boundary = scopeBasis === 'REQUESTED' ? scenario.requestedScope : scenario.scope;
    if (!boundary) {
      errors.push(error(
        'REQUESTED_SCOPE_MISSING',
        basePath,
        'REQUESTED-scoped facts and Evidence require scenario.requestedScope.',
      ));
      return;
    }


    if (scope.organizationId !== boundary.organizationId) {
      errors.push(error(
        'SCOPE_OUTSIDE_SCENARIO',
        `${basePath}.organizationId`,
        `Organization ${scope.organizationId} is outside the ${scopeBasis.toLowerCase()} scenario Scope.`,
      ));
    }

    for (const field of ['siteIds', 'equipmentIds', 'deviceIds']) {
      const allowed = new Set(boundary[field]);
      scope[field].forEach((id, index) => {
        if (!allowed.has(id)) {
          errors.push(error(
            'SCOPE_OUTSIDE_SCENARIO',
            `${basePath}.${field}[${index}]`,
            `${id} is outside the ${scopeBasis.toLowerCase()} scenario Scope.`,
          ));
        }
      });
    }

    if (scope.timeRange) {
      const outsideTimeRange = !boundary.timeRange
        || Date.parse(scope.timeRange.from) < Date.parse(boundary.timeRange.from)
        || Date.parse(scope.timeRange.to) > Date.parse(boundary.timeRange.to);
      if (outsideTimeRange) {
        errors.push(error(
          'SCOPE_OUTSIDE_SCENARIO',
          `${basePath}.timeRange`,
          `The scoped time range extends beyond the ${scopeBasis.toLowerCase()} scenario Scope.`,
        ));
      }
    }
  };

  if (scenario.requestedScope) validateBoundary(scenario.requestedScope, '$.requestedScope');

  scenario.inputFacts.forEach((fact, index) => {
    const path = `$.inputFacts[${index}]`;
    if (fact.scopeBasis === 'REQUESTED' && !fact.ownerTool.startsWith('authorization.')) {
      errors.push(error(
        'REQUESTED_SCOPE_OWNER_INVALID',
        `${path}.ownerTool`,
        'Only authorization facts may describe a requested but unauthorized Scope.',
      ));
    }
    validateScope(fact.scope, fact.scopeBasis, `${path}.scope`);
  });
  scenario.evidenceRequirements.forEach((requirement, index) => {
    const path = `$.evidenceRequirements[${index}]`;
    if (requirement.scopeBasis === 'REQUESTED' && !requirement.ownerTool.startsWith('authorization.')) {
      errors.push(error(
        'REQUESTED_SCOPE_OWNER_INVALID',
        `${path}.ownerTool`,
        'Only authorization Evidence may describe a requested but unauthorized Scope.',
      ));
    }
    validateScope(requirement.scope, requirement.scopeBasis, `${path}.scope`);
  });
};

const validateToolPolicy = (scenario, errors) => {
  const allowed = new Set(scenario.tools.allowed);
  const forbidden = new Set(scenario.tools.forbidden);

  pushDuplicateValues(scenario.tools.allowed, '$.tools.allowed', errors);
  pushDuplicateValues(scenario.tools.forbidden, '$.tools.forbidden', errors);
  pushDuplicateValues(scenario.tools.forbiddenPaths, '$.tools.forbiddenPaths', errors);

  scenario.tools.allowed.forEach((tool, index) => validateKnownTool(tool, `$.tools.allowed[${index}]`, errors));
  scenario.tools.forbidden.forEach((tool, index) => validateKnownTool(tool, `$.tools.forbidden[${index}]`, errors));

  for (const tool of allowed) {
    if (forbidden.has(tool)) {
      errors.push(error(
        'TOOL_POLICY_CONFLICT',
        '$.tools',
        `Logical tool ${tool} cannot be both allowed and forbidden.`,
      ));
    }
  }

  const requireAllowed = (tool, path) => {
    validateKnownTool(tool, path, errors);
    if (!allowed.has(tool)) {
      errors.push(error('TOOL_NOT_ALLOWED', path, `Logical tool ${tool} is used but is not allowed.`));
    }
    if (forbidden.has(tool)) {
      errors.push(error('FORBIDDEN_TOOL_USED', path, `Logical tool ${tool} is forbidden by this scenario.`));
    }
  };

  scenario.inputFacts.forEach((fact, index) => requireAllowed(fact.ownerTool, `$.inputFacts[${index}].ownerTool`));
  scenario.evidenceRequirements.forEach((requirement, index) => {
    const path = `$.evidenceRequirements[${index}].ownerTool`;
    if (requirement.status === 'AVAILABLE') {
      requireAllowed(requirement.ownerTool, path);
      return;
    }

    validateKnownTool(requirement.ownerTool, path, errors);
    if (forbidden.has(requirement.ownerTool)) {
      errors.push(error(
        'REQUIRED_NEXT_TOOL_FORBIDDEN',
        path,
        `Required-next Evidence cannot depend on forbidden logical tool ${requirement.ownerTool}.`,
      ));
    }
  });
  scenario.executionDag.nodes.forEach((node, index) => {
    if (node.kind === 'TOOL_CALL' && !node.tool) {
      errors.push(error(
        'TOOL_CALL_REQUIRES_TOOL',
        `$.executionDag.nodes[${index}].tool`,
        'A TOOL_CALL execution node must name one logical tool.',
      ));
      return;
    }
    if (node.kind !== 'TOOL_CALL' && node.tool) {
      errors.push(error(
        'NON_TOOL_NODE_HAS_TOOL',
        `$.executionDag.nodes[${index}].tool`,
        `${node.kind} execution nodes cannot name a logical tool.`,
      ));
      return;
    }
    if (node.tool) requireAllowed(node.tool, `$.executionDag.nodes[${index}].tool`);
  });
};

const validateActionLifecycle = (scenario, errors) => {
  if (!scenario.actionLifecycle) return;

  const allowed = new Set(scenario.tools.allowed);
  const forbidden = new Set(scenario.tools.forbidden);
  const forbiddenPaths = new Set(scenario.tools.forbiddenPaths);

  if (scenario.actionLifecycle.proposedAction === 'EXPECTED'
    && !scenario.taskCategories.includes('ACTION_PROPOSAL')) {
    errors.push(error(
      'ACTION_LIFECYCLE_CONFLICT',
      '$.actionLifecycle.proposedAction',
      'An expected Proposed Action requires the ACTION_PROPOSAL task category.',
    ));
  }
  if (scenario.actionLifecycle.commandIntent === 'MUST_NOT_CREATE'
    && (!forbidden.has('commands.createIntent') || allowed.has('commands.createIntent'))) {
    errors.push(error(
      'ACTION_LIFECYCLE_CONFLICT',
      '$.actionLifecycle.commandIntent',
      'MUST_NOT_CREATE requires commands.createIntent to be forbidden and not allowed.',
    ));
  }
  if (scenario.actionLifecycle.formalApproval === 'NOT_PRESENT'
    && (!forbidden.has('commands.approveIntent') || allowed.has('commands.approveIntent'))) {
    errors.push(error(
      'ACTION_LIFECYCLE_CONFLICT',
      '$.actionLifecycle.formalApproval',
      'NOT_PRESENT formal approval requires commands.approveIntent to be forbidden and not allowed.',
    ));
  }
  if (scenario.actionLifecycle.physicalExecutionResult === 'MUST_NOT_CLAIM'
    && !forbiddenPaths.has('PHYSICAL_COMMAND_EXECUTION')) {
    errors.push(error(
      'ACTION_LIFECYCLE_CONFLICT',
      '$.actionLifecycle.physicalExecutionResult',
      'MUST_NOT_CLAIM requires PHYSICAL_COMMAND_EXECUTION to be a forbidden path.',
    ));
  }
};

const validateAcceptance = (scenario, errors) => {
  pushDuplicateErrors(scenario.acceptance.blockers, '$.acceptance.blockers', errors);
  pushDuplicateErrors(scenario.acceptance.scored, '$.acceptance.scored', errors);

  const blockerIds = new Set(scenario.acceptance.blockers.map(({ id }) => id));
  scenario.acceptance.scored.forEach((criterion, index) => {
    if (blockerIds.has(criterion.id)) {
      errors.push(error(
        'DUPLICATE_ID',
        `$.acceptance.scored[${index}].id`,
        `Criterion ID ${criterion.id} is already used by a blocker.`,
      ));
    }
    if (criterion.dimension === 'AUTHORIZATION_COMPLIANCE' || criterion.dimension === 'SAFETY_COMPLIANCE') {
      errors.push(error(
        'BLOCKER_DIMENSION_SCORED',
        `$.acceptance.scored[${index}].dimension`,
        `${criterion.dimension} must be represented as a blocker, never as a scored criterion.`,
      ));
    }
  });
};

export const validateOperationsAgentScenario = (value) => {
  const parsed = operationsAgentScenarioSchemaV1.safeParse(value);
  if (!parsed.success) {
    return {
      valid: false,
      errors: parsed.error.issues.map((issue) => error(
        'STRUCTURE_INVALID',
        jsonPath(issue.path),
        issue.message,
      )),
    };
  }

  const scenario = parsed.data;
  const errors = [];

  pushDuplicateValues(scenario.taskCategories, '$.taskCategories', errors);
  pushDuplicateValues(scenario.scope.siteIds, '$.scope.siteIds', errors);
  pushDuplicateValues(scenario.scope.equipmentIds, '$.scope.equipmentIds', errors);
  pushDuplicateValues(scenario.scope.deviceIds, '$.scope.deviceIds', errors);
  pushDuplicateErrors(scenario.inputFacts, '$.inputFacts', errors);
  pushDuplicateErrors(scenario.groundTruth.outcomes, '$.groundTruth.outcomes', errors);
  pushDuplicateErrors(scenario.evidenceRequirements, '$.evidenceRequirements', errors);
  pushDuplicateErrors(scenario.dataQuality.conditions, '$.dataQuality.conditions', errors);

  validateTimeRange(scenario.scope.timeRange, '$.scope.timeRange', errors);

  validateReferences(scenario, errors);
  validateScopeContainment(scenario, errors);
  validateDag(scenario.planningDag.nodes, '$.planningDag', errors);
  validateDag(scenario.executionDag.nodes, '$.executionDag', errors);
  validateToolPolicy(scenario, errors);
  validateActionLifecycle(scenario, errors);
  validateAcceptance(scenario, errors);

  return errors.length === 0
    ? { valid: true, scenario, errors }
    : { valid: false, errors };
};
