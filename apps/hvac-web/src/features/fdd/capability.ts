import type { RealReadModelBoundary } from '../real-read-model-boundary';

export const FDD_READ_MODEL_BOUNDARY = {
  domain: 'fdd',
  label: 'FDD Finding Read Model',
  status: 'INTEGRATED',
  authority: 'fdd-service',
  fallback: 'none',
  requiredFields: ['findingType', 'assetId', 'evaluationWindow', 'evidenceIds', 'ruleRevisionId', 'modelDeploymentRevisionId', 'confidence', 'alarmId', 'workOrderId'],
} as const satisfies RealReadModelBoundary;
