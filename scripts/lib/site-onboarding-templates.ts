import { existsSync, readdirSync, readFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

export interface TemplateStandardPointSpec {
  readonly pointCode: string;
  readonly sourceKey?: string;
  readonly displayName: string;
  readonly pointType: string;
  readonly valueType: string;
  readonly unit?: string;
  readonly writable?: boolean;
  readonly sampleIntervalMs: number;
  readonly publishIntervalMs: number;
  readonly staleAfterMs: number;
  readonly counterDecreaseMode?: string;
  readonly counterRolloverModulus?: number;
  readonly sourceMetadata?: Record<string, unknown>;
  readonly minimumNumber?: number;
  readonly maximumNumber?: number;
  readonly qualityChecks?: Record<string, unknown>;
}

export interface TemplateOnboardingHint {
  readonly semanticRole?: string;
  readonly critical?: boolean;
  readonly qualityProfile?: string;
}

export interface DeviceTemplateCatalogEntry {
  readonly templateKey: string;
  readonly templateKind: 'DEVICE';
  readonly defaultSampleSeconds?: number;
  readonly description?: string;
  readonly payload: {
    readonly deviceType: string;
    readonly standardPoints?: readonly TemplateStandardPointSpec[];
    readonly capabilityProfiles?: readonly Record<string, unknown>[];
  };
  readonly onboardingHints?: Readonly<Record<string, TemplateOnboardingHint>>;
  readonly releaseReferences: Readonly<Record<string, string>>;
}

export interface TemplateAwareDevice {
  readonly id?: string;
  readonly code: string;
  readonly name?: string;
  readonly sourceKey?: string;
  readonly type?: string;
  readonly sampleSeconds?: number;
  readonly asset?: string;
  readonly template?: string;
  readonly pointOverrides?: Readonly<Record<string, Readonly<Record<string, unknown>> & { omit?: boolean }>>;
  readonly points?: 'meter' | ReadonlyArray<Record<string, unknown>>;
  readonly morePoints?: ReadonlyArray<Record<string, unknown>>;
}

const DEFAULT_CATALOG_DIR = resolve(dirname(fileURLToPath(import.meta.url)), '../../deploy/templates/devices');
const catalogCache = new Map<string, ReadonlyMap<string, DeviceTemplateCatalogEntry>>();

/** Load all standard DEVICE templates from deploy/templates/devices/*.json (cached per directory). */
export function loadDeviceTemplateCatalog(dir = DEFAULT_CATALOG_DIR): ReadonlyMap<string, DeviceTemplateCatalogEntry> {
  const key = resolve(dir);
  const cached = catalogCache.get(key);
  if (cached) return cached;
  const entries = new Map<string, DeviceTemplateCatalogEntry>();
  if (existsSync(key)) {
    const files = readdirSync(key).filter((name) => name.endsWith('.json')).sort();
    for (const file of files) {
      const parsed = JSON.parse(readFileSync(join(key, file), 'utf8')) as DeviceTemplateCatalogEntry;
      if (parsed.templateKey) entries.set(parsed.templateKey, parsed);
    }
  }
  catalogCache.set(key, entries);
  return entries;
}

/** Fill omitted device.type and device.sampleSeconds from the referenced standard template. */
export function hydrateTemplateDevice<T extends TemplateAwareDevice>(
  device: T,
  catalog: ReadonlyMap<string, DeviceTemplateCatalogEntry> = loadDeviceTemplateCatalog(),
): T {
  if (!device.template) return device;
  const entry = catalog.get(device.template);
  if (!entry) return device;
  return {
    ...device,
    type: device.type ?? entry.payload.deviceType,
    sampleSeconds: device.sampleSeconds ?? entry.defaultSampleSeconds ?? 60,
  };
}

/** Expand points from 'meter', an inline points array, or a referenced standard device template. */
export function expandDevicePoints<T extends Record<string, unknown>>(
  device: TemplateAwareDevice,
  meterPoints?: readonly T[],
  catalog: ReadonlyMap<string, DeviceTemplateCatalogEntry> = loadDeviceTemplateCatalog(),
): T[] {
  let base: Record<string, unknown>[];
  if (device.points === 'meter') {
    base = [...(meterPoints ?? [])];
  } else if (Array.isArray(device.points)) {
    base = [...device.points];
  } else if (device.template) {
    const entry = catalog.get(device.template);
    if (!entry) throw new Error(`device ${device.code} names unknown template ${device.template}`);
    const overrides = device.pointOverrides ?? {};
    const hints = entry.onboardingHints ?? {};
    base = [];
    for (const sp of entry.payload.standardPoints ?? []) {
      const override = overrides[sp.pointCode];
      if (override?.omit === true) continue;
      const hint = hints[sp.pointCode];
      const merged: Record<string, unknown> = {
        code: sp.pointCode,
        name: sp.displayName,
        pointType: sp.pointType,
        valueType: sp.valueType,
        ...(sp.unit ? { unit: sp.unit } : {}),
        ...(sp.sourceKey ? { sourceKey: sp.sourceKey } : {}),
        ...(sp.writable != null ? { writable: sp.writable } : {}),
        ...(sp.sourceMetadata ? { sourceMetadata: structuredClone(sp.sourceMetadata) } : {}),
        ...(hint?.semanticRole ? { semanticRole: hint.semanticRole } : {}),
        ...(hint?.critical !== undefined ? { critical: hint.critical } : {}),
        ...(hint?.qualityProfile ? { qualityProfile: hint.qualityProfile } : {}),
        ...(override ?? {}),
      };
      delete merged.omit;
      base.push(merged);
    }
  } else {
    throw new Error(`device ${device.code} must declare points or template`);
  }
  return [...base, ...(device.morePoints ?? [])].map((point) => ({ ...point })) as T[];
}

/** Validate device template references, declared deviceType parity, and pointOverrides keys. */
export function validateDeviceTemplates(
  profile: { devices?: readonly TemplateAwareDevice[] },
  problems: string[],
  catalog: ReadonlyMap<string, DeviceTemplateCatalogEntry> = loadDeviceTemplateCatalog(),
): void {
  for (const device of profile.devices ?? []) {
    if (!device.template) {
      if (device.points !== 'meter' && !Array.isArray(device.points)) {
        problems.push(`device ${device.code}: must declare points or template`);
      }
      continue;
    }
    const entry = catalog.get(device.template);
    if (!entry) {
      problems.push(`device ${device.code}: unknown template ${device.template}`);
      continue;
    }
    if (device.type && device.type !== entry.payload.deviceType) {
      problems.push(`device ${device.code}: type ${device.type} contradicts template ${device.template} (${entry.payload.deviceType})`);
    }
    const knownPoints = new Set((entry.payload.standardPoints ?? []).map((p) => p.pointCode));
    for (const overrideCode of Object.keys(device.pointOverrides ?? {})) {
      if (!knownPoints.has(overrideCode)) {
        problems.push(`device ${device.code}: pointOverrides.${overrideCode} is not in template ${device.template}`);
      }
    }
  }
}

export const tpl = {
  hydrate: hydrateTemplateDevice,
  expand: expandDevicePoints,
  validate: validateDeviceTemplates,
};
