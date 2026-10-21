import { readFileSync } from 'node:fs';

// The plant recorded from the local platform for the web's browser tests (apps/hvac-web/e2e/fixtures):
// one chiller, its two pumps and cooling tower running, at 23:30 on 1 October at the Site (UTC+8).
const fixture = (name: string) => JSON.parse(readFileSync(new URL(`../../hvac-web/e2e/fixtures/${name}`, import.meta.url), 'utf8'));

export const assetModel = fixture('asset-model.json');
export const snapshots = fixture('snapshots.json');
export const recordedAt = new Date('2026-10-01T15:30:00Z');
