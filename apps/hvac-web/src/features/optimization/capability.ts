import type { RealReadModelBoundary } from '../real-read-model-boundary';

export const OPTIMIZATION_READ_MODEL_BOUNDARY = {
  domain: 'optimization',
  label: 'Optimization Read Model',
  status: 'NOT_INTEGRATED',
  authority: 'backend-contract-pending',
  fallback: 'none',
  requiredFields: ['runId', 'status', 'objective', 'constraints', 'baseline', 'candidate', 'risk', 'approval', 'dispatchPlan'],
} as const satisfies RealReadModelBoundary;
