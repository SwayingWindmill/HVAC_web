import type { TemplateKind } from '../../../api/generated/platformGateway.gen.ts';
import { isOneOf, isPlainRecord } from '../../../lib/typed.ts';

const POINT_CODE_PATTERN = /^[a-z][a-z0-9_]{0,127}$/;
const SOURCE_KEY_PATTERN = /^[A-Za-z][A-Za-z0-9_.:-]{0,127}$/;

export const STANDARD_POINT_TYPES = ['TELEMETRY', 'COUNTER', 'STATE', 'SETTING', 'COMMAND'] as const;
export type StandardPointType = (typeof STANDARD_POINT_TYPES)[number];

export const STANDARD_VALUE_TYPES = ['NUMBER', 'BOOLEAN', 'STRING', 'JSON'] as const;
export type StandardValueType = (typeof STANDARD_VALUE_TYPES)[number];

export const TEMPLATE_COUNTER_DECREASE_MODES = ['RESET_TO_ZERO', 'ROLLOVER', 'INVALID'] as const;
export type TemplateCounterDecreaseMode = (typeof TEMPLATE_COUNTER_DECREASE_MODES)[number];

export const CAPABILITY_STATUSES = ['DRAFT', 'VERIFIED'] as const;
export type CapabilityProfileStatus = (typeof CAPABILITY_STATUSES)[number];

export const CAPABILITY_RISK_LEVELS = ['LOW', 'MEDIUM', 'HIGH', 'CRITICAL'] as const;
export type CapabilityRiskLevel = (typeof CAPABILITY_RISK_LEVELS)[number];

export const CAPABILITY_APPROVAL_POLICIES = ['NONE', 'SINGLE_APPROVER', 'TWO_PERSON'] as const;
export type CapabilityApprovalPolicy = (typeof CAPABILITY_APPROVAL_POLICIES)[number];

export interface TemplateReferenceEntry {
  readonly key: string;
  readonly value: string;
}

export interface StandardPointDraft {
  readonly pointCode: string;
  readonly sourceKey: string;
  readonly displayName: string;
  readonly pointType: StandardPointType;
  readonly valueType: StandardValueType;
  readonly unit: string;
  readonly writable: boolean;
  readonly sampleIntervalMs: string;
  readonly publishIntervalMs: string;
  readonly staleAfterMs: string;
  readonly counterDecreaseMode: '' | TemplateCounterDecreaseMode;
  readonly counterRolloverModulus: string;
  readonly minimumNumber: string;
  readonly maximumNumber: string;
  readonly flatlineSeconds: string;
}

export interface CapabilityProfileDraft {
  readonly capabilityName: string;
  readonly capabilityRevision: string;
  readonly status: CapabilityProfileStatus;
  readonly canonicalUnit: string;
  readonly minimumValue: string;
  readonly maximumValue: string;
  readonly maximumDelta: string;
  readonly riskLevel: CapabilityRiskLevel;
  readonly approvalPolicy: CapabilityApprovalPolicy;
  readonly connectorKind: string;
}

export interface StructuredDeviceTemplateDraft {
  readonly deviceType: string;
  readonly standardPoints: readonly StandardPointDraft[];
  readonly capabilityProfiles: readonly CapabilityProfileDraft[];
}

export interface TemplatePayloadSummary {
  readonly deviceType: string | null;
  readonly standardPointCount: number;
  readonly capabilityCount: number;
  readonly hasQualityChecks: boolean;
  readonly topKeys: readonly string[];
  readonly kind: TemplateKind;
}

export function createDefaultReferenceEntries(): TemplateReferenceEntry[] {
  return [{ key: 'registrySchema', value: 'v1' }];
}

export function releaseReferencesToEntries(
  references: Readonly<Record<string, string>>,
): TemplateReferenceEntry[] {
  const entries = Object.entries(references)
    .sort(([left], [right]) => left.localeCompare(right))
    .map(([key, value]) => ({ key, value }));
  return entries.length > 0 ? entries : createDefaultReferenceEntries();
}

export function buildReleaseReferencesFromEntries(
  entries: readonly TemplateReferenceEntry[],
): { readonly ok: true; readonly releaseReferences: Record<string, string> } | { readonly ok: false; readonly error: string } {
  const result: Record<string, string> = {};
  const seenKeys = new Set<string>();
  for (const row of entries) {
    const key = row.key.trim();
    const value = row.value.trim();
    if (!key && !value) continue;
    if (!key) {
      return { ok: false, error: '发布引用存在空键名，请补充键名或删除该行。' };
    }
    if (!value || value.length > 256) {
      return { ok: false, error: `发布引用「${key}」的值不能为空且不超过 256 个字符。` };
    }
    if (seenKeys.has(key)) {
      return { ok: false, error: `发布引用键名「${key}」重复。` };
    }
    seenKeys.add(key);
    result[key] = value;
  }
  if (Object.keys(result).length === 0) {
    return { ok: false, error: '发布引用至少需要保留一项有效键值对（例如 registrySchema = v1）。' };
  }
  return { ok: true, releaseReferences: result };
}

export function createEmptyStandardPointDraft(): StandardPointDraft {
  return {
    pointCode: '',
    sourceKey: '',
    displayName: '',
    pointType: 'TELEMETRY',
    valueType: 'NUMBER',
    unit: '',
    writable: false,
    sampleIntervalMs: '5000',
    publishIntervalMs: '5000',
    staleAfterMs: '15000',
    counterDecreaseMode: '',
    counterRolloverModulus: '',
    minimumNumber: '',
    maximumNumber: '',
    flatlineSeconds: '',
  };
}

export function createEmptyCapabilityProfileDraft(): CapabilityProfileDraft {
  return {
    capabilityName: '',
    capabilityRevision: 'v1',
    status: 'VERIFIED',
    canonicalUnit: 'degC',
    minimumValue: '0',
    maximumValue: '100',
    maximumDelta: '5',
    riskLevel: 'MEDIUM',
    approvalPolicy: 'NONE',
    connectorKind: 'MODBUS_TCP',
  };
}

export function createEmptyStructuredDeviceDraft(): StructuredDeviceTemplateDraft {
  return {
    deviceType: '',
    standardPoints: [],
    capabilityProfiles: [],
  };
}

function hasControlChars(value: string): boolean {
  for (let index = 0; index < value.length; index += 1) {
    const code = value.charCodeAt(index);
    if (code < 0x20 || code === 0x7f) return true;
  }
  return false;
}

function isValidIdentifierToken(value: string, maxBytes: number): boolean {
  if (!value || value.trim() !== value || value.length > maxBytes) return false;
  for (let index = 0; index < value.length; index += 1) {
    const code = value.charCodeAt(index);
    if (code <= 0x20 || code === 0x7f) return false;
  }
  return true;
}

function parseOptionalFiniteNumber(raw: string, label: string): { readonly ok: true; readonly value: number | undefined } | { readonly ok: false; readonly error: string } {
  const trimmed = raw.trim();
  if (!trimmed) return { ok: true, value: undefined };
  const parsed = Number(trimmed);
  if (!Number.isFinite(parsed)) {
    return { ok: false, error: `${label}必须是有效数字` };
  }
  return { ok: true, value: parsed };
}

export function serializeStructuredDevicePayload(
  draft: StructuredDeviceTemplateDraft,
): { readonly ok: true; readonly payload: Record<string, unknown> } | { readonly ok: false; readonly error: string } {
  const payload: Record<string, unknown> = {};
  const deviceType = draft.deviceType.trim();
  if (deviceType) {
    if (deviceType.length > 128 || hasControlChars(deviceType)) {
      return { ok: false, error: '适用设备类型不能超过 128 个字符且不能包含控制字符。' };
    }
    payload.deviceType = deviceType;
  }

  if (draft.standardPoints.length > 0) {
    const seenPointCodes = new Set<string>();
    const serializedPoints: Record<string, unknown>[] = [];
    for (let index = 0; index < draft.standardPoints.length; index += 1) {
      const row = draft.standardPoints[index]!;
      const labelPrefix = `标准点位第 ${index + 1} 行`;
      const pointCode = row.pointCode.trim();
      const sourceKey = row.sourceKey.trim();
      const displayName = row.displayName.trim();
      const unit = row.unit.trim();

      if (!POINT_CODE_PATTERN.test(pointCode)) {
        return { ok: false, error: `${labelPrefix}的 pointCode 须以小写字母开头，仅含小写字母、数字和下划线。` };
      }
      if (seenPointCodes.has(pointCode)) {
        return { ok: false, error: `标准点位编码「${pointCode}」重复。` };
      }
      seenPointCodes.add(pointCode);

      if (sourceKey && !SOURCE_KEY_PATTERN.test(sourceKey)) {
        return { ok: false, error: `${labelPrefix}的来源键「${sourceKey}」格式无效。` };
      }
      if (!displayName || displayName.length > 256 || hasControlChars(displayName)) {
        return { ok: false, error: `${labelPrefix}的显示名称不能为空且不超过 256 个字符。` };
      }
      if (unit && (unit.length > 64 || hasControlChars(unit))) {
        return { ok: false, error: `${labelPrefix}的单位不能超过 64 个字符。` };
      }

      const writable = row.pointType === 'COMMAND' ? true : row.pointType === 'SETTING' ? row.writable : false;
      const sampleIntervalMs = Number(row.sampleIntervalMs);
      const publishIntervalMs = Number(row.publishIntervalMs);
      const staleAfterMs = Number(row.staleAfterMs);
      if (
        !Number.isInteger(sampleIntervalMs) ||
        sampleIntervalMs < 100 ||
        sampleIntervalMs > 86_400_000 ||
        !Number.isInteger(publishIntervalMs) ||
        publishIntervalMs < sampleIntervalMs ||
        publishIntervalMs > 86_400_000 ||
        !Number.isInteger(staleAfterMs) ||
        staleAfterMs < publishIntervalMs ||
        staleAfterMs > 604_800_000
      ) {
        return {
          ok: false,
          error: `${labelPrefix}的采样 / 上报 / 陈旧时间窗须满足 100 <= 采样 <= 上报 <= 陈旧阈值。`,
        };
      }

      if (row.pointType === 'COUNTER') {
        if (row.valueType !== 'NUMBER' || !row.counterDecreaseMode) {
          return { ok: false, error: `${labelPrefix}为累计量时，值类型须为数值且选择下降处理方式。` };
        }
        if (row.counterDecreaseMode === 'ROLLOVER') {
          const modulus = Number(row.counterRolloverModulus);
          if (!Number.isFinite(modulus) || modulus <= 0) {
            return { ok: false, error: `${labelPrefix}选择量程回绕时，必须填写大于 0 的回绕模数。` };
          }
        }
      }

      const minRes = parseOptionalFiniteNumber(row.minimumNumber, `${labelPrefix}最小量程`);
      if (!minRes.ok) return minRes;
      const maxRes = parseOptionalFiniteNumber(row.maximumNumber, `${labelPrefix}最大量程`);
      if (!maxRes.ok) return maxRes;
      if (
        (minRes.value !== undefined || maxRes.value !== undefined) &&
        row.valueType !== 'NUMBER'
      ) {
        return { ok: false, error: `${labelPrefix}只有数值类型点位才支持上下限质量检查。` };
      }
      if (minRes.value !== undefined && maxRes.value !== undefined && maxRes.value < minRes.value) {
        return { ok: false, error: `${labelPrefix}最大量程不能小于最小量程。` };
      }

      const flatlineRaw = row.flatlineSeconds.trim();
      let flatlineSeconds: number | undefined;
      if (flatlineRaw) {
        flatlineSeconds = Number(flatlineRaw);
        if (!Number.isInteger(flatlineSeconds) || flatlineSeconds < 60) {
          return { ok: false, error: `${labelPrefix}的卡死判定秒数须为不少于 60 的整数。` };
        }
      }

      const serialized: Record<string, unknown> = {
        pointCode,
        ...(sourceKey ? { sourceKey } : {}),
        displayName,
        pointType: row.pointType,
        valueType: row.valueType,
        ...(unit ? { unit } : {}),
        writable,
        sampleIntervalMs,
        publishIntervalMs,
        staleAfterMs,
      };
      if (row.pointType === 'COUNTER' && row.counterDecreaseMode) {
        serialized.counterDecreaseMode = row.counterDecreaseMode;
        if (row.counterDecreaseMode === 'ROLLOVER') {
          serialized.counterRolloverModulus = Number(row.counterRolloverModulus);
        }
      }
      if (minRes.value !== undefined) serialized.minimumNumber = minRes.value;
      if (maxRes.value !== undefined) serialized.maximumNumber = maxRes.value;
      if (flatlineSeconds !== undefined) {
        serialized.qualityChecks = { flatlineSeconds };
      }
      serializedPoints.push(serialized);
    }
    payload.standardPoints = serializedPoints;
  }

  if (draft.capabilityProfiles.length > 0) {
    const seenPairs = new Set<string>();
    const serializedProfiles: Record<string, unknown>[] = [];
    for (let index = 0; index < draft.capabilityProfiles.length; index += 1) {
      const row = draft.capabilityProfiles[index]!;
      const labelPrefix = `控制能力第 ${index + 1} 行`;
      const capabilityName = row.capabilityName.trim();
      const capabilityRevision = row.capabilityRevision.trim();
      const canonicalUnit = row.canonicalUnit.trim();
      const connectorKind = row.connectorKind.trim();

      if (!isValidIdentifierToken(capabilityName, 128) || !isValidIdentifierToken(capabilityRevision, 128)) {
        return { ok: false, error: `${labelPrefix}的能力名称和版本不能包含空白字符且不超过 128 字节。` };
      }
      const pairKey = `${capabilityName}\0${capabilityRevision}`;
      if (seenPairs.has(pairKey)) {
        return { ok: false, error: `控制能力「${capabilityName}@${capabilityRevision}」重复。` };
      }
      seenPairs.add(pairKey);

      if (!canonicalUnit || canonicalUnit.length > 64 || hasControlChars(canonicalUnit)) {
        return { ok: false, error: `${labelPrefix}的规范单位不能为空且不超过 64 字符。` };
      }
      if (!connectorKind || connectorKind.length > 64 || hasControlChars(connectorKind)) {
        return { ok: false, error: `${labelPrefix}的连接器类型不能为空且不超过 64 字符。` };
      }

      const minimumValue = Number(row.minimumValue);
      const maximumValue = Number(row.maximumValue);
      const maximumDelta = Number(row.maximumDelta);
      if (!Number.isFinite(minimumValue) || !Number.isFinite(maximumValue) || maximumValue < minimumValue) {
        return { ok: false, error: `${labelPrefix}的最小值与最大值须为有效数字且最大值不小于最小值。` };
      }
      if (!Number.isFinite(maximumDelta) || maximumDelta < 0) {
        return { ok: false, error: `${labelPrefix}的单步最大变化量须为非负数字。` };
      }

      serializedProfiles.push({
        capabilityName,
        capabilityRevision,
        status: row.status,
        canonicalUnit,
        minimumValue,
        maximumValue,
        maximumDelta,
        riskLevel: row.riskLevel,
        approvalPolicy: row.approvalPolicy,
        retryPolicy: 'PRE_SEND_ONLY',
        connectorKind,
      });
    }
    payload.capabilityProfiles = serializedProfiles;
  }

  return { ok: true, payload };
}

export function parseStructuredDevicePayload(payload: Record<string, unknown>): {
  readonly supported: boolean;
  readonly draft: StructuredDeviceTemplateDraft;
} {
  const allowedTopKeys = new Set(['deviceType', 'standardPoints', 'qualityChecks', 'capabilityProfiles']);
  for (const key of Object.keys(payload)) {
    if (!allowedTopKeys.has(key)) {
      return { supported: false, draft: createEmptyStructuredDeviceDraft() };
    }
  }
  const deviceType = typeof payload.deviceType === 'string' ? payload.deviceType : '';

  const points: StandardPointDraft[] = [];
  if (payload.standardPoints !== undefined) {
    if (!Array.isArray(payload.standardPoints)) {
      return { supported: false, draft: createEmptyStructuredDeviceDraft() };
    }
    for (const entry of payload.standardPoints) {
      if (!isPlainRecord(entry)) {
        return { supported: false, draft: createEmptyStructuredDeviceDraft() };
      }
      const pointType = typeof entry.pointType === 'string' && isOneOf(STANDARD_POINT_TYPES, entry.pointType)
        ? entry.pointType
        : 'TELEMETRY';
      const valueType = typeof entry.valueType === 'string' && isOneOf(STANDARD_VALUE_TYPES, entry.valueType)
        ? entry.valueType
        : 'NUMBER';
      const decreaseMode = typeof entry.counterDecreaseMode === 'string' && isOneOf(TEMPLATE_COUNTER_DECREASE_MODES, entry.counterDecreaseMode)
        ? entry.counterDecreaseMode
        : '';
      const inlineQuality = isPlainRecord(entry.qualityChecks) ? entry.qualityChecks : undefined;
      const minVal = typeof entry.minimumNumber === 'number'
        ? String(entry.minimumNumber)
        : inlineQuality && typeof inlineQuality.minimumNumber === 'number'
          ? String(inlineQuality.minimumNumber)
          : '';
      const maxVal = typeof entry.maximumNumber === 'number'
        ? String(entry.maximumNumber)
        : inlineQuality && typeof inlineQuality.maximumNumber === 'number'
          ? String(inlineQuality.maximumNumber)
          : '';
      const flatlineVal = inlineQuality && typeof inlineQuality.flatlineSeconds === 'number'
        ? String(inlineQuality.flatlineSeconds)
        : '';

      points.push({
        pointCode: typeof entry.pointCode === 'string' ? entry.pointCode : '',
        sourceKey: typeof entry.sourceKey === 'string' ? entry.sourceKey : '',
        displayName: typeof entry.displayName === 'string' ? entry.displayName : '',
        pointType,
        valueType,
        unit: typeof entry.unit === 'string' ? entry.unit : '',
        writable: Boolean(entry.writable),
        sampleIntervalMs: typeof entry.sampleIntervalMs === 'number' ? String(entry.sampleIntervalMs) : '5000',
        publishIntervalMs: typeof entry.publishIntervalMs === 'number' ? String(entry.publishIntervalMs) : '5000',
        staleAfterMs: typeof entry.staleAfterMs === 'number' ? String(entry.staleAfterMs) : '15000',
        counterDecreaseMode: decreaseMode,
        counterRolloverModulus: typeof entry.counterRolloverModulus === 'number' ? String(entry.counterRolloverModulus) : '',
        minimumNumber: minVal,
        maximumNumber: maxVal,
        flatlineSeconds: flatlineVal,
      });
    }
  }

  const profiles: CapabilityProfileDraft[] = [];
  if (payload.capabilityProfiles !== undefined) {
    if (!Array.isArray(payload.capabilityProfiles)) {
      return { supported: false, draft: createEmptyStructuredDeviceDraft() };
    }
    for (const entry of payload.capabilityProfiles) {
      if (!isPlainRecord(entry)) {
        return { supported: false, draft: createEmptyStructuredDeviceDraft() };
      }
      profiles.push({
        capabilityName: typeof entry.capabilityName === 'string' ? entry.capabilityName : '',
        capabilityRevision: typeof entry.capabilityRevision === 'string' ? entry.capabilityRevision : 'v1',
        status: typeof entry.status === 'string' && isOneOf(CAPABILITY_STATUSES, entry.status) ? entry.status : 'VERIFIED',
        canonicalUnit: typeof entry.canonicalUnit === 'string' ? entry.canonicalUnit : 'degC',
        minimumValue: typeof entry.minimumValue === 'number' ? String(entry.minimumValue) : '0',
        maximumValue: typeof entry.maximumValue === 'number' ? String(entry.maximumValue) : '100',
        maximumDelta: typeof entry.maximumDelta === 'number' ? String(entry.maximumDelta) : '5',
        riskLevel: typeof entry.riskLevel === 'string' && isOneOf(CAPABILITY_RISK_LEVELS, entry.riskLevel) ? entry.riskLevel : 'MEDIUM',
        approvalPolicy: typeof entry.approvalPolicy === 'string' && isOneOf(CAPABILITY_APPROVAL_POLICIES, entry.approvalPolicy) ? entry.approvalPolicy : 'NONE',
        connectorKind: typeof entry.connectorKind === 'string' ? entry.connectorKind : 'MODBUS_TCP',
      });
    }
  }

  return {
    supported: payload.qualityChecks === undefined,
    draft: {
      deviceType,
      standardPoints: points,
      capabilityProfiles: profiles,
    },
  };
}

export function summarizeTemplatePayload(
  kind: TemplateKind,
  payload: Record<string, unknown>,
): TemplatePayloadSummary {
  const standardPoints = Array.isArray(payload.standardPoints) ? payload.standardPoints.length : 0;
  const capabilityProfiles = Array.isArray(payload.capabilityProfiles) ? payload.capabilityProfiles.length : 0;
  return {
    kind,
    deviceType: typeof payload.deviceType === 'string' && payload.deviceType ? payload.deviceType : null,
    standardPointCount: standardPoints,
    capabilityCount: capabilityProfiles,
    hasQualityChecks: payload.qualityChecks !== undefined,
    topKeys: Object.keys(payload).sort(),
  };
}
