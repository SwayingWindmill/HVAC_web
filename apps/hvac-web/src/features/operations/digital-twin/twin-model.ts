import type { Asset } from '@/api/generated/platformGateway.gen';
import { isRunning, plantReading, type PlantDevice, type PlantReading, type PlantView } from '../realtime/plant-model.ts';

// The cold side of the digital twin, built from what the Registry has: every chiller, cooling tower and
// pump asset is drawn, live when a device is bound to it and「未接入」when none is. Nothing is placed
// from site-specific code (docs/specs/digital-twin.md).

export type TwinKind = 'chiller' | 'tower' | 'chilledPump' | 'coolingPump' | 'ahu' | 'fcu' | 'vav';
export type TwinState = 'running' | 'stopped' | 'fault' | 'offline' | 'unknown' | 'unconnected';

export interface TwinReading {
  readonly label: string;
  readonly display: string;
  readonly unit: string | null;
}

export interface TwinEquipment {
  readonly assetId: string;
  readonly name: string;
  readonly kind: TwinKind;
  readonly state: TwinState;
  /** The bound device, whose side sheet opens on selection. */
  readonly device: PlantDevice | null;
  readonly readings: readonly TwinReading[];
  /** The same readings by source key, for labels placed on the part they measure. */
  readonly values: Readonly<Record<string, TwinReading>>;
}

export interface TwinPlant {
  readonly chillers: readonly TwinEquipment[];
  readonly towers: readonly TwinEquipment[];
  readonly chilledPumps: readonly TwinEquipment[];
  readonly coolingPumps: readonly TwinEquipment[];
  /** Chilled water moves while a chilled-water pump reports running. */
  readonly chilledFlow: boolean;
  /** Cooling water moves while a cooling-water pump reports running, or any machine runs (it cannot without it). */
  readonly coolingFlow: boolean;
  readonly supply: PlantReading | null;
  readonly return: PlantReading | null;
  readonly flow: PlantReading | null;
}

const KIND_BY_ASSET_TYPE: Readonly<Record<string, TwinKind>> = {
  CHILLER: 'chiller',
  COOLING_TOWER: 'tower',
  CHILLED_WATER_PUMP: 'chilledPump',
  COOLING_WATER_PUMP: 'coolingPump',
};

const READING_KEYS: Readonly<Record<TwinKind, readonly string[]>> = {
  chiller: [
    'chiller.power',
    'chiller.leaving_chilled_water_temperature',
    'chiller.entering_chilled_water_temperature',
    'chiller.entering_cooling_water_temperature',
    'chiller.leaving_cooling_water_temperature',
  ],
  tower: ['cooling_tower.power', 'cooling_tower.leaving_water_temperature', 'cooling_tower.fan_speed'],
  chilledPump: ['chwp.power', 'chwp.frequency', 'chwp.flow_rate'],
  coolingPump: ['cwp.power', 'cwp.frequency', 'cwp.flow_rate'],
  ahu: [],
  fcu: [],
  vav: [],
};

function stateOf(device: PlantDevice | null): TwinState {
  if (!device) return 'unconnected';
  if (device.connection === 'OFFLINE') return 'offline';
  if (device.runState === 'RUNNING') return 'running';
  if (device.runState === 'STOPPED') return 'stopped';
  if (device.runState === 'FAULT') return 'fault';
  return 'unknown';
}

function valuesOf(device: PlantDevice | null, kind: TwinKind): Record<string, TwinReading> {
  if (!device) return {};
  return Object.fromEntries(READING_KEYS[kind].flatMap((sourceKey) => {
    const reading = device.reading(sourceKey);
    return reading && reading.state === 'PRESENT' ? [[sourceKey, { label: reading.label, display: reading.display, unit: reading.unit }]] : [];
  }));
}

export function buildTwinPlant(assets: readonly Asset[], plant: PlantView): TwinPlant {
  const deviceFor = (assetId: string) => plant.devices.find((device) =>
    device.row.binding.state === 'bound' && device.row.binding.asset.id === assetId) ?? null;
  const equipment = assets
    .filter((asset) => asset.status === 'ACTIVE' && KIND_BY_ASSET_TYPE[asset.assetType])
    .sort((left, right) => left.code.localeCompare(right.code))
    .map((asset): TwinEquipment => {
      const kind = KIND_BY_ASSET_TYPE[asset.assetType];
      const device = deviceFor(asset.id);
      const values = valuesOf(device, kind);
      return { assetId: asset.id, name: asset.displayName, kind, state: stateOf(device), device, readings: Object.values(values), values };
    });
  const ofKind = (kind: TwinKind) => equipment.filter((item) => item.kind === kind);
  const runningIn = (category: PlantDevice['category']) => plant.devices.some((device) => device.category === category && isRunning(device));
  return {
    chillers: ofKind('chiller'),
    towers: ofKind('tower'),
    chilledPumps: ofKind('chilledPump'),
    coolingPumps: ofKind('coolingPump'),
    chilledFlow: runningIn('CHILLED_WATER_PUMP'),
    coolingFlow: runningIn('COOLING_WATER_PUMP') || runningIn('CHILLER'),
    supply: plantReading(plant, 'btu_meter.supply_water_temperature'),
    return: plantReading(plant, 'btu_meter.return_water_temperature'),
    flow: plantReading(plant, 'btu_meter.flow_rate'),
  };
}

export const TWIN_STATE_LABELS: Readonly<Record<TwinState, string>> = {
  running: '运行',
  stopped: '停止',
  fault: '故障',
  offline: '离线',
  unknown: '状态未知',
  unconnected: '未接入',
};
