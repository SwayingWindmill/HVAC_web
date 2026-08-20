export type RealReadModelStatus = 'NOT_INTEGRATED' | 'INTEGRATED';

export interface RealReadModelBoundary {
  readonly domain: string;
  readonly label: string;
  readonly status: RealReadModelStatus;
  readonly authority: string;
  readonly fallback: string;
  readonly requiredFields: readonly string[];
}

export function boundaryMeta(boundary: RealReadModelBoundary): string {
  return `${boundary.status} · ${boundary.authority}`;
}
