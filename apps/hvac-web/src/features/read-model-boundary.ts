export type ReadModelStatus = 'NOT_INTEGRATED' | 'INTEGRATED';

export interface ReadModelBoundary {
  readonly domain: string;
  readonly label: string;
  readonly status: ReadModelStatus;
  readonly authority: string;
  readonly fallback: string;
  readonly requiredFields: readonly string[];
}

export function boundaryMeta(boundary: ReadModelBoundary): string {
  return `${boundary.status} · ${boundary.authority}`;
}
