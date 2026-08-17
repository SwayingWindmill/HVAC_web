import type { RealReadModelBoundary } from '../real-read-model-boundary';

export const FORECAST_READ_MODEL_BOUNDARY = {
  domain: 'forecast',
  label: 'Forecast Read Model',
  status: 'NOT_INTEGRATED',
  authority: 'backend-contract-pending',
  fallback: 'none',
  requiredFields: ['target', 'origin', 'asOf', 'forecastFor', 'horizon', 'granularity', 'modelVersion', 'featureSetVersion', 'quality'],
} as const satisfies RealReadModelBoundary;
