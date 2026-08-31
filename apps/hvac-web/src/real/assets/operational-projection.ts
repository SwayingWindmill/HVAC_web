import type { Device, TelemetryPoint } from '../../api/generated/platformGateway.gen.ts';
import type {
  DeviceObservationSnapshot,
  DevicePresenceState,
  EvaluationAvailability,
  PresenceApplicability,
  ProblemDetails,
  TelemetryFreshness,
  TelemetryKeyState,
  TelemetryQuality,
  TelemetryReadiness,
} from '../../api/generated/s2Telemetry.gen.ts';
import {
  formatTelemetryDisplayValue,
  formatTelemetryUnit,
  telemetryPointDefinition,
} from '../../domain/centralPlantTelemetry.ts';

export type RealAssetsSnapshotResult =
  | { readonly status: 'ok'; readonly snapshot: DeviceObservationSnapshot }
  | { readonly status: 'error'; readonly problem: ProblemDetails };

export type RealAssetsConnectionState = DevicePresenceState | 'NOT_APPLICABLE' | 'UNAVAILABLE';
export type RealAssetsTelemetryFreshnessState = TelemetryFreshness | 'NOT_APPLICABLE' | 'UNAVAILABLE';
export type RealAssetsTelemetryQualityState = 'GOOD' | 'DEGRADED' | 'NO_DATA' | 'NOT_APPLICABLE' | 'UNAVAILABLE';
export type RealAssetsTelemetryReadinessState = TelemetryReadiness | 'UNAVAILABLE';

export type RealAssetsAttentionReason =
  | 'CURRENT_STATE_UNAVAILABLE'
  | 'CURRENT_STATE_NOT_VISIBLE'
  | 'POINT_CATALOG_CONTRACT_DRIFT'
  | 'PRESENCE_OFFLINE'
  | 'TELEMETRY_STALE'
  | 'TELEMETRY_QUALITY_DEGRADED'
  | 'TELEMETRY_MISSING'
  | 'TELEMETRY_DEGRADED'
  | 'TELEMETRY_INCOMPLETE';

export interface RealAssetsPointView {
  readonly pointId: string;
  readonly sensorId: string | null;
  readonly key: string;
  readonly label: string;
  readonly state: 'PRESENT' | 'MISSING' | 'UNAVAILABLE';
  readonly displayValue: string;
  readonly unit: string | null;
  readonly freshness: 'FRESH' | 'STALE' | 'MISSING' | 'UNAVAILABLE';
  readonly quality: TelemetryQuality | null;
  readonly qualityReasons: readonly string[];
  readonly sampledAt: string | null;
  readonly receivedAt: string | null;
  readonly policyRevision: number | null;
  readonly missingReason?: 'NEVER_OBSERVED' | 'ONLY_REJECTED_CANDIDATES' | 'POLICY_NOT_CONFIGURED';
}

export interface RealAssetsConnectionProjection {
  readonly applicability: PresenceApplicability | 'UNAVAILABLE';
  readonly state: RealAssetsConnectionState;
  readonly lastSeenAt: string | null;
  readonly policyRevision: number | null;
}

export interface RealAssetsTelemetryProjection {
  readonly evaluationAvailability: EvaluationAvailability;
  readonly readiness: RealAssetsTelemetryReadinessState;
  readonly freshness: RealAssetsTelemetryFreshnessState;
  readonly quality: RealAssetsTelemetryQualityState;
  readonly registeredPointCount: number;
  readonly presentPointCount: number;
  readonly stalePointCount: number;
  readonly missingPointCount: number;
  readonly unavailablePointCount: number;
  readonly degradedQualityPointCount: number;
}

export interface RealAssetsDeviceOperationalProjection {
  readonly connection: RealAssetsConnectionProjection;
  readonly telemetry: RealAssetsTelemetryProjection;
  readonly registryLifecycle: Device['status'];
  readonly points: readonly RealAssetsPointView[];
  readonly attentionReasons: readonly RealAssetsAttentionReason[];
  readonly needsAttention: boolean;
}

export interface ProjectRealAssetsDeviceOperationalStateInput {
  readonly device: Device;
  readonly telemetryPoints: readonly TelemetryPoint[];
  readonly snapshotResult?: RealAssetsSnapshotResult;
}

interface PointDisplayDefinition {
  readonly point: TelemetryPoint;
  readonly label: string;
  readonly defaultUnit?: string;
  readonly precision?: number;
}

function presentationDefinition(point: TelemetryPoint): PointDisplayDefinition {
  const domainDefinition = telemetryPointDefinition(point.pointCode);
  return {
    point,
    label: domainDefinition.label === point.pointCode ? point.displayName : domainDefinition.label,
    defaultUnit: point.unit ?? domainDefinition.defaultUnit,
    precision: domainDefinition.precision,
  };
}

function pointView(definition: PointDisplayDefinition, state: TelemetryKeyState | undefined): RealAssetsPointView {
  if (!state || state.state === 'MISSING') {
    return {
      pointId: definition.point.id,
      sensorId: definition.point.sensorId ?? null,
      key: definition.point.pointCode,
      label: definition.label,
      state: 'MISSING',
      displayValue: state?.missingReason === 'ONLY_REJECTED_CANDIDATES' ? '当前值不可用' : '未观测',
      unit: null,
      freshness: 'MISSING',
      quality: null,
      qualityReasons: [],
      sampledAt: null,
      receivedAt: null,
      policyRevision: state?.policyRevision ?? null,
      missingReason: state?.missingReason,
    };
  }
  return {
    pointId: definition.point.id,
    sensorId: definition.point.sensorId ?? null,
    key: definition.point.pointCode,
    label: definition.label,
    state: 'PRESENT',
    displayValue: formatTelemetryDisplayValue(state.value, definition.precision ?? 3),
    unit: formatTelemetryUnit(state.unit ?? definition.defaultUnit),
    freshness: state.freshness,
    quality: state.quality,
    qualityReasons: [...state.qualityReasons],
    sampledAt: state.sampledAt,
    receivedAt: state.receivedAt,
    policyRevision: state.policyRevision,
  };
}

function unavailablePointView(definition: PointDisplayDefinition): RealAssetsPointView {
  return {
    pointId: definition.point.id,
    sensorId: definition.point.sensorId ?? null,
    key: definition.point.pointCode,
    label: definition.label,
    state: 'UNAVAILABLE',
    displayValue: '当前值不可用',
    unit: null,
    freshness: 'UNAVAILABLE',
    quality: null,
    qualityReasons: [],
    sampledAt: null,
    receivedAt: null,
    policyRevision: null,
  };
}

function connectionFromSnapshot(snapshot: DeviceObservationSnapshot): RealAssetsConnectionProjection {
  if (snapshot.evaluationAvailability === 'UNAVAILABLE') {
    return {
      applicability: 'UNAVAILABLE',
      state: 'UNAVAILABLE',
      lastSeenAt: null,
      policyRevision: null,
    };
  }
  if (snapshot.presence.applicability === 'NOT_APPLICABLE') {
    return {
      applicability: 'NOT_APPLICABLE',
      state: 'NOT_APPLICABLE',
      lastSeenAt: null,
      policyRevision: snapshot.presence.policyRevision,
    };
  }
  return {
    applicability: 'APPLICABLE',
    state: snapshot.presence.currentState ?? 'UNKNOWN',
    lastSeenAt: snapshot.presence.lastSeenAt,
    policyRevision: snapshot.presence.policyRevision,
  };
}

function aggregateFreshness(points: readonly RealAssetsPointView[]): TelemetryFreshness {
  if (points.some((point) => point.state === 'MISSING')) return 'MISSING';
  if (points.some((point) => point.freshness === 'STALE')) return 'STALE';
  return points.length > 0 ? 'FRESH' : 'MISSING';
}

function aggregateQuality(points: readonly RealAssetsPointView[]): 'GOOD' | 'DEGRADED' | 'NO_DATA' {
  const present = points.filter((point) => point.state === 'PRESENT');
  if (present.length === 0) return 'NO_DATA';
  return present.some((point) => point.quality !== 'GOOD') ? 'DEGRADED' : 'GOOD';
}

function ownerFailureReason(problem: ProblemDetails): RealAssetsAttentionReason {
  if (problem.code === 'RESOURCE_NOT_FOUND') return 'CURRENT_STATE_NOT_VISIBLE';
  if (problem.code === 'TELEMETRY_KEY_INVALID') return 'POINT_CATALOG_CONTRACT_DRIFT';
  return 'CURRENT_STATE_UNAVAILABLE';
}

function unavailableProjection(
  device: Device,
  telemetryPoints: readonly TelemetryPoint[],
  reason: RealAssetsAttentionReason,
): RealAssetsDeviceOperationalProjection {
  const points = telemetryPoints.map((point) => unavailablePointView(presentationDefinition(point)));
  return {
    connection: {
      applicability: 'UNAVAILABLE',
      state: 'UNAVAILABLE',
      lastSeenAt: null,
      policyRevision: null,
    },
    telemetry: {
      evaluationAvailability: 'UNAVAILABLE',
      readiness: 'UNAVAILABLE',
      freshness: 'UNAVAILABLE',
      quality: 'UNAVAILABLE',
      registeredPointCount: telemetryPoints.length,
      presentPointCount: 0,
      stalePointCount: 0,
      missingPointCount: 0,
      unavailablePointCount: telemetryPoints.length,
      degradedQualityPointCount: 0,
    },
    registryLifecycle: device.status,
    points,
    attentionReasons: [reason],
    needsAttention: true,
  };
}

export function projectRealAssetsDeviceOperationalState(
  input: ProjectRealAssetsDeviceOperationalStateInput,
): RealAssetsDeviceOperationalProjection {
  const { device, telemetryPoints, snapshotResult } = input;
  if (!snapshotResult) {
    return unavailableProjection(device, telemetryPoints, 'CURRENT_STATE_UNAVAILABLE');
  }
  if (snapshotResult.status === 'error') {
    return unavailableProjection(device, telemetryPoints, ownerFailureReason(snapshotResult.problem));
  }

  const snapshot = snapshotResult.snapshot;
  const valueByKey = new Map(snapshot.values.map((value) => [value.key, value]));
  const points = telemetryPoints.map((point) => pointView(
    presentationDefinition(point),
    valueByKey.get(point.pointCode),
  ));

  if (snapshot.evaluationAvailability === 'UNAVAILABLE') {
    return unavailableProjection(device, telemetryPoints, 'CURRENT_STATE_UNAVAILABLE');
  }

  const connection = connectionFromSnapshot(snapshot);
  const freshness = snapshot.telemetryReadiness === 'NOT_APPLICABLE'
    ? 'NOT_APPLICABLE'
    : aggregateFreshness(points);
  const quality = snapshot.telemetryReadiness === 'NOT_APPLICABLE'
    ? 'NOT_APPLICABLE'
    : aggregateQuality(points);
  const stalePointCount = points.filter((point) => point.freshness === 'STALE').length;
  const missingPointCount = points.filter((point) => point.state === 'MISSING').length;
  const degradedQualityPointCount = points.filter((point) => point.state === 'PRESENT' && point.quality !== 'GOOD').length;
  const attentionReasons = new Set<RealAssetsAttentionReason>();

  if (connection.state === 'OFFLINE') attentionReasons.add('PRESENCE_OFFLINE');
  if (snapshot.telemetryReadiness !== 'NOT_APPLICABLE') {
    if (snapshot.telemetryReadiness === 'DEGRADED') attentionReasons.add('TELEMETRY_DEGRADED');
    if (snapshot.telemetryReadiness === 'INCOMPLETE') attentionReasons.add('TELEMETRY_INCOMPLETE');
    if (stalePointCount > 0) attentionReasons.add('TELEMETRY_STALE');
    if (missingPointCount > 0) attentionReasons.add('TELEMETRY_MISSING');
    if (degradedQualityPointCount > 0) attentionReasons.add('TELEMETRY_QUALITY_DEGRADED');
  }

  return {
    connection,
    telemetry: {
      evaluationAvailability: snapshot.evaluationAvailability,
      readiness: snapshot.telemetryReadiness,
      freshness,
      quality,
      registeredPointCount: telemetryPoints.length,
      presentPointCount: points.length - missingPointCount,
      stalePointCount,
      missingPointCount,
      unavailablePointCount: 0,
      degradedQualityPointCount,
    },
    registryLifecycle: device.status,
    points,
    attentionReasons: [...attentionReasons],
    needsAttention: attentionReasons.size > 0,
  };
}
