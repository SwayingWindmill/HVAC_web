export type RealReadModelStatus = 'NOT_INTEGRATED';

export interface RealReadModelBoundary {
  readonly domain: string;
  readonly label: string;
  readonly status: RealReadModelStatus;
  readonly authority: 'backend-contract-pending';
  readonly fallback: 'none';
  readonly requiredFields: readonly string[];
}

export function boundaryMeta(boundary: RealReadModelBoundary): string {
  return `${boundary.status} · ${boundary.authority}`;
}
