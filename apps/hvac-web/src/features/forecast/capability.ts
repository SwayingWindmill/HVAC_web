import type { ReadModelBoundary } from '../read-model-boundary';

export const FORECAST_READ_MODEL_BOUNDARY = {
  domain: 'forecast',
  label: 'Forecast Read Model',
  status: 'INTEGRATED',
  authority: 'forecast-service',
  fallback: 'explicit-quality-only',
  requiredFields: ['target', 'origin', 'asOf', 'forecastFor', 'horizon', 'granularity', 'modelVersion', 'featureSetVersion', 'quality'],
} as const satisfies ReadModelBoundary;
