import type {
  Asset,
  AssetRelationship,
  Device,
  SiteAssetModel,
  Space,
  TelemetryPoint,
} from '@/api/generated/platformGateway.gen';
import type { DeviceObservationSnapshot, TelemetryKeyState } from '@/api/generated/s2Telemetry.gen';
import type { AssetsCurrentStateData, AssetsRegistryData } from '@/features/assets/data';
import type { AssetsSnapshotResult } from '@/features/assets/model';

const REVIEW_CREATED_AT = '2026-09-01T00:00:00.000Z';

interface ReviewPointDefinition {
  readonly sourceKey: string;
  readonly label: string;
  readonly pointType: TelemetryPoint['pointType'];
  readonly valueType: TelemetryPoint['valueType'];
  readonly unit: string | null;
}

interface ReviewDeviceGroup {
  readonly count: number;
  readonly deviceType: string;
  readonly assetType: string;
  readonly codePrefix: string;
  readonly label: string;
  readonly points: readonly ReviewPointDefinition[];
}

const REVIEW_GROUPS: readonly ReviewDeviceGroup[] = [
  {
    count: 8,
    deviceType: 'WATER_COOLED_CHILLER',
    assetType: 'CHILLER',
    codePrefix: 'CH',
    label: '冷水机组',
    points: [
      { sourceKey: 'chiller.run_state', label: '运行状态', pointType: 'STATE', valueType: 'STRING', unit: null },
      { sourceKey: 'chiller.power', label: '主机功率', pointType: 'TELEMETRY', valueType: 'NUMBER', unit: 'kW' },
      { sourceKey: 'chiller.cop', label: '主机 COP', pointType: 'TELEMETRY', valueType: 'NUMBER', unit: null },
      { sourceKey: 'chiller.cooling_capacity', label: '制冷量', pointType: 'TELEMETRY', valueType: 'NUMBER', unit: 'kW' },
    ],
  },
  {
    count: 10,
    deviceType: 'CHILLED_WATER_PUMP',
    assetType: 'CHILLED_WATER_PUMP',
    codePrefix: 'CHWP',
    label: '冷冻水泵',
    points: [
      { sourceKey: 'chwp.run_state', label: '运行状态', pointType: 'STATE', valueType: 'STRING', unit: null },
      { sourceKey: 'chwp.frequency', label: '运行频率', pointType: 'TELEMETRY', valueType: 'NUMBER', unit: 'Hz' },
      { sourceKey: 'chwp.flow_rate', label: '冷冻水流量', pointType: 'TELEMETRY', valueType: 'NUMBER', unit: 'm3/h' },
      { sourceKey: 'chwp.power', label: '水泵功率', pointType: 'TELEMETRY', valueType: 'NUMBER', unit: 'kW' },
    ],
  },
  {
    count: 8,
    deviceType: 'COOLING_WATER_PUMP',
    assetType: 'COOLING_WATER_PUMP',
    codePrefix: 'CWP',
    label: '冷却水泵',
    points: [
      { sourceKey: 'cwp.run_state', label: '运行状态', pointType: 'STATE', valueType: 'STRING', unit: null },
      { sourceKey: 'cwp.frequency', label: '运行频率', pointType: 'TELEMETRY', valueType: 'NUMBER', unit: 'Hz' },
      { sourceKey: 'cwp.flow_rate', label: '冷却水流量', pointType: 'TELEMETRY', valueType: 'NUMBER', unit: 'm3/h' },
      { sourceKey: 'cwp.power', label: '水泵功率', pointType: 'TELEMETRY', valueType: 'NUMBER', unit: 'kW' },
    ],
  },
  {
    count: 8,
    deviceType: 'COOLING_TOWER',
    assetType: 'COOLING_TOWER',
    codePrefix: 'CT',
    label: '冷却塔',
    points: [
      { sourceKey: 'cooling_tower.run_state', label: '运行状态', pointType: 'STATE', valueType: 'STRING', unit: null },
      { sourceKey: 'cooling_tower.fan_speed', label: '风机转速', pointType: 'TELEMETRY', valueType: 'NUMBER', unit: '%' },
      { sourceKey: 'cooling_tower.approach_temperature', label: '逼近温度', pointType: 'TELEMETRY', valueType: 'NUMBER', unit: 'Cel' },
      { sourceKey: 'cooling_tower.power', label: '冷却塔功率', pointType: 'TELEMETRY', valueType: 'NUMBER', unit: 'kW' },
    ],
  },
  {
    count: 6,
    deviceType: 'HVAC_POWER_METER',
    assetType: 'HVAC_POWER_METER',
    codePrefix: 'EM',
    label: '中央空调电表',
    points: [
      { sourceKey: 'hvac_meter.active_power', label: '中央空调总功率', pointType: 'TELEMETRY', valueType: 'NUMBER', unit: 'kW' },
      { sourceKey: 'hvac_meter.energy', label: '累计电量', pointType: 'COUNTER', valueType: 'NUMBER', unit: 'kWh' },
      { sourceKey: 'hvac_meter.power_factor', label: '功率因数', pointType: 'TELEMETRY', valueType: 'NUMBER', unit: null },
      { sourceKey: 'hvac_meter.frequency', label: '电网频率', pointType: 'TELEMETRY', valueType: 'NUMBER', unit: 'Hz' },
    ],
  },
  {
    count: 6,
    deviceType: 'BTU_METER',
    assetType: 'BTU_METER',
    codePrefix: 'BTU',
    label: '冷量表',
    points: [
      { sourceKey: 'btu_meter.instant_cooling_capacity', label: '瞬时制冷量', pointType: 'TELEMETRY', valueType: 'NUMBER', unit: 'kW' },
      { sourceKey: 'btu_meter.temperature_difference', label: '供回水温差', pointType: 'TELEMETRY', valueType: 'NUMBER', unit: 'Cel' },
      { sourceKey: 'btu_meter.flow_rate', label: '冷冻水流量', pointType: 'TELEMETRY', valueType: 'NUMBER', unit: 'm3/h' },
      { sourceKey: 'btu_meter.accumulated_cooling_energy', label: '累计冷量', pointType: 'COUNTER', valueType: 'NUMBER', unit: 'kWh' },
    ],
  },
];

function reviewId(namespace: number, index: number): string {
  return `01940000-${namespace.toString(16).padStart(4, '0')}-7${index.toString(16).padStart(3, '0')}-8000-${index.toString(16).padStart(12, '0')}`;
}

function pointCode(sourceKey: string): string {
  return sourceKey.split('.').join('_');
}

function pointValue(sourceKey: string, index: number, offline: boolean): string | number {
  if (sourceKey.endsWith('run_state')) {
    if (offline) return 'STOPPED';
    return index % 5 === 0 ? 'STANDBY' : 'RUNNING';
  }
  if (sourceKey.endsWith('cop')) return Number((4.55 + (index % 7) * 0.08).toFixed(2));
  if (sourceKey.includes('cooling_capacity')) return 480 + index * 7;
  if (sourceKey.endsWith('frequency')) return Number((42 + (index % 9) * 0.9).toFixed(1));
  if (sourceKey.includes('flow_rate')) return 260 + index * 4;
  if (sourceKey.includes('fan_speed')) return 58 + (index % 8) * 4;
  if (sourceKey.includes('approach_temperature')) return Number((3.1 + (index % 5) * 0.2).toFixed(1));
  if (sourceKey.endsWith('power_factor')) return Number((0.94 + (index % 4) * 0.01).toFixed(2));
  if (sourceKey.endsWith('active_power')) return 420 + index * 9;
  if (sourceKey.endsWith('energy')) return 128_000 + index * 2_450;
  if (sourceKey.includes('temperature_difference')) return Number((5.2 + (index % 6) * 0.2).toFixed(1));
  if (sourceKey.includes('accumulated_cooling_energy')) return 246_000 + index * 4_200;
  return 18 + index * 1.7;
}

function buildReviewAssetModel(tenantId: string, siteId: string): SiteAssetModel {
  const plantRoomId = reviewId(0x1000, 1);
  const spaces: Space[] = [{
    id: plantRoomId,
    tenantId,
    siteId,
    parentSpaceId: null,
    code: 'CENTRAL-PLANT',
    displayName: '中央机房',
    spaceType: 'PLANT_ROOM',
    status: 'ACTIVE',
    revision: 1,
    createdAt: REVIEW_CREATED_AT,
    updatedAt: REVIEW_CREATED_AT,
  }];

  const officeFloorId = reviewId(0x1001, 1);
  spaces.push({
    id: officeFloorId,
    tenantId,
    siteId,
    parentSpaceId: null,
    code: 'OFFICE-F03',
    displayName: '办公区 3F',
    spaceType: 'FLOOR',
    status: 'ACTIVE',
    revision: 1,
    createdAt: REVIEW_CREATED_AT,
    updatedAt: REVIEW_CREATED_AT,
  });
  for (let zoneIndex = 1; zoneIndex <= 8; zoneIndex += 1) {
    spaces.push({
      id: reviewId(0x1002, zoneIndex),
      tenantId,
      siteId,
      parentSpaceId: officeFloorId,
      code: `F03-Z${String(zoneIndex).padStart(2, '0')}`,
      displayName: `3F · 区域 ${String(zoneIndex).padStart(2, '0')}`,
      spaceType: 'ZONE',
      status: 'ACTIVE',
      revision: 1,
      createdAt: REVIEW_CREATED_AT,
      updatedAt: REVIEW_CREATED_AT,
    });
  }

  const assets: Asset[] = [];
  const devices: Device[] = [];
  const telemetryPoints: TelemetryPoint[] = [];
  const relationships: AssetRelationship[] = [];

  let deviceIndex = 0;
  for (const group of REVIEW_GROUPS) {
    for (let localIndex = 1; localIndex <= group.count; localIndex += 1) {
      deviceIndex += 1;
      const assetId = reviewId(0x2000, deviceIndex);
      const deviceId = reviewId(0x3000, deviceIndex);
      const code = `${group.codePrefix}-${String(localIndex).padStart(2, '0')}`;
      const displayName = `${localIndex}# ${group.label}`;

      assets.push({
        id: assetId,
        tenantId,
        siteId,
        code,
        displayName,
        assetType: group.assetType,
        status: 'ACTIVE',
        revision: 1,
        createdAt: REVIEW_CREATED_AT,
        updatedAt: REVIEW_CREATED_AT,
      });
      devices.push({
        id: deviceId,
        tenantId,
        siteId,
        code: `${code}-CTRL`,
        displayName,
        deviceType: group.deviceType,
        status: 'ACTIVE',
        revision: 1,
        createdAt: REVIEW_CREATED_AT,
        updatedAt: REVIEW_CREATED_AT,
      });
      relationships.push(
        {
          id: reviewId(0x4000, deviceIndex * 2 - 1),
          tenantId,
          siteId,
          fromType: 'ASSET',
          fromId: assetId,
          toType: 'SPACE',
          toId: plantRoomId,
          role: 'INSTALLED_IN',
          status: 'ACTIVE',
          validFrom: REVIEW_CREATED_AT,
          validTo: null,
          revision: 1,
          createdAt: REVIEW_CREATED_AT,
          updatedAt: REVIEW_CREATED_AT,
        },
        {
          id: reviewId(0x4000, deviceIndex * 2),
          tenantId,
          siteId,
          fromType: 'DEVICE',
          fromId: deviceId,
          toType: 'ASSET',
          toId: assetId,
          role: 'CONTROLS',
          status: 'ACTIVE',
          validFrom: REVIEW_CREATED_AT,
          validTo: null,
          revision: 1,
          createdAt: REVIEW_CREATED_AT,
          updatedAt: REVIEW_CREATED_AT,
        },
      );

      group.points.forEach((definition, pointIndex) => {
        telemetryPoints.push({
          id: reviewId(0x5000 + pointIndex, deviceIndex),
          tenantId,
          siteId,
          reportingDeviceId: deviceId,
          sensorId: null,
          pointCode: pointCode(definition.sourceKey),
          sourceKey: definition.sourceKey,
          displayName: definition.label,
          pointType: definition.pointType,
          valueType: definition.valueType,
          unit: definition.unit,
          writable: false,
          sampleIntervalMs: 1000,
          publishIntervalMs: 1000,
          staleAfterMs: 15_000,
          counterDecreaseMode: null,
          counterRolloverModulus: null,
          sourceMetadata: {},
          status: 'ACTIVE',
          revision: 1,
          createdAt: REVIEW_CREATED_AT,
          updatedAt: REVIEW_CREATED_AT,
        });
      });
    }
  }

  return {
    schemaVersion: 2,
    tenantId,
    siteId,
    spaces,
    assets,
    devices,
    sensors: [],
    telemetryPoints,
    relationships,
    counts: {
      spaces: spaces.length,
      assets: assets.length,
      deviceEndpoints: devices.length,
      physicalSensors: 0,
      points: telemetryPoints.length,
    },
  };
}

function buildReviewSnapshot(device: Device, points: readonly TelemetryPoint[], index: number): DeviceObservationSnapshot {
  const now = Date.now();
  const offline = index % 9 === 0;
  const stale = !offline && index % 11 === 0;
  const suspect = !offline && index % 13 === 0;
  const missing = !offline && index % 17 === 0;
  const sampledAt = new Date(now - (stale ? 90_000 : 4_000 + (index % 4) * 1_000)).toISOString();
  const receivedAt = new Date(now - (stale ? 89_000 : 3_000 + (index % 4) * 1_000)).toISOString();
  const lastSeenAt = new Date(now - (offline ? 18 * 60_000 : 3_000 + (index % 5) * 1_000)).toISOString();

  const values: TelemetryKeyState[] = points.map((point) => {
    if (missing) {
      return {
        key: point.pointCode,
        state: 'MISSING',
        freshness: 'MISSING',
        missingReason: 'NEVER_OBSERVED',
        policyRevision: 14,
      };
    }
    return {
      key: point.pointCode,
      state: 'PRESENT',
      value: pointValue(point.sourceKey, index, offline),
      valueType: point.valueType,
      unit: point.unit,
      sampledAt,
      receivedAt,
      freshness: stale ? 'STALE' : 'FRESH',
      quality: suspect ? 'PARTIAL' : 'GOOD',
      qualityReasons: suspect ? ['SOURCE_LAG_EXCEEDED'] : [],
      policyRevision: 14,
    };
  });

  return {
    schemaVersion: 1,
    deviceId: device.id,
    tenantId: device.tenantId,
    siteId: device.siteId,
    businessRevision: 1000 + index,
    evaluatedAt: new Date(now).toISOString(),
    evaluationAvailability: 'AVAILABLE',
    availabilityReasons: [],
    presence: {
      applicability: 'APPLICABLE',
      currentState: offline ? 'OFFLINE' : 'ONLINE',
      lastSeenAt,
      policyRevision: 14,
      lastKnown: null,
    },
    telemetryReadiness: missing ? 'INCOMPLETE' : stale || suspect ? 'DEGRADED' : 'CURRENT',
    displayState: offline ? 'OFFLINE' : missing ? 'UNKNOWN' : stale ? 'STALE' : 'ONLINE',
    values,
  };
}

export function createFrontendReviewAssetsRegistry(tenantId: string, siteId: string): AssetsRegistryData {
  return {
    assetModel: buildReviewAssetModel(tenantId, siteId),
    routePolicyRevision: 'frontend-review-assets:1',
  };
}

export function createFrontendReviewAssetsCurrentState(
  devices: readonly Device[],
  telemetryPoints: readonly TelemetryPoint[],
): AssetsCurrentStateData {
  const byDeviceId = new Map<string, AssetsSnapshotResult>();
  devices.forEach((device, deviceIndex) => {
    const points = telemetryPoints.filter((point) => point.reportingDeviceId === device.id);
    byDeviceId.set(device.id, {
      status: 'ok',
      snapshot: buildReviewSnapshot(device, points, deviceIndex + 1),
    });
  });
  return {
    byDeviceId,
    partial: false,
    requestCount: Math.ceil(devices.length / 100),
    routePolicyRevision: 'frontend-review-assets:1',
  };
}
