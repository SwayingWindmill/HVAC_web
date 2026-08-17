import type { RealReadModelBoundary } from '../real-read-model-boundary';

export const SETTLEMENT_READ_MODEL_BOUNDARY = {
  domain: 'settlement',
  label: 'Settlement Read Model',
  status: 'NOT_INTEGRATED',
  authority: 'backend-contract-pending',
  fallback: 'none',
  requiredFields: ['period', 'status', 'revision', 'sourceReadingLineage', 'tariffVersion', 'reconciliation', 'correctionHistory'],
} as const satisfies RealReadModelBoundary;
