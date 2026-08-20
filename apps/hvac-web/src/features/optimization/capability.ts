import type { RealReadModelBoundary } from '../real-read-model-boundary';

export const OPTIMIZATION_READ_MODEL_BOUNDARY = {
  domain: 'optimization',
  label: 'Optimization Read Model',
  status: 'INTEGRATED',
  authority: 'optimization-service',
  fallback: 'none',
  requiredFields: ['runId', 'status', 'objective', 'constraints', 'baseline', 'candidate', 'expectedImpact', 'uncertainty', 'risk', 'approval', 'rollbackPlan', 'verificationPlan'],
} as const satisfies RealReadModelBoundary;
