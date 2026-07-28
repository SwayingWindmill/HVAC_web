export type CentralPlantProfileKind =
  | 'CHILLER'
  | 'CHILLED_WATER_PUMP'
  | 'COOLING_WATER_PUMP'
  | 'COOLING_TOWER'
  | 'HVAC_POWER_METER'
  | 'BTU_METER'
  | 'GENERIC';

export interface TelemetryPointDefinition {
  key: string;
  label: string;
  defaultUnit?: string;
  precision?: number;
}

export interface DeviceTelemetryProfile {
  kind: CentralPlantProfileKind;
  title: string;
  keys: readonly string[];
  highlightKeys: readonly string[];
}

interface PresentTelemetryState {
  key: string;
  state: 'PRESENT';
  value: unknown;
  unit: string | null;
  freshness: string;
  quality: string;
}

interface MissingTelemetryState {
  key: string;
  state: 'MISSING';
  freshness: 'MISSING';
}

export interface TelemetrySnapshotLike {
  values: ReadonlyArray<PresentTelemetryState | MissingTelemetryState>;
}

export interface DeviceTelemetryHighlight {
  key: string;
  label: string;
  displayValue: string;
  unit: string | null;
  state: 'PRESENT' | 'MISSING';
  freshness: string;
  quality: string | null;
}

const point = (
  key: string,
  label: string,
  defaultUnit?: string,
  precision = 3,
): TelemetryPointDefinition => ({ key, label, defaultUnit, precision });

const POINTS: Readonly<Record<string, TelemetryPointDefinition>> = Object.freeze(Object.fromEntries([
  point('temperature', '温度', '°C'),
  point('humidity', '湿度', '%RH'),
  point('setpoint', '设定值', '°C'),
  point('power', '功率', 'kW'),

  point('chiller.run_state', '运行状态'),
  point('chiller.power', '主机功率', 'kW'),
  point('chiller.cop', '主机 COP', undefined, 2),
  point('chiller.cooling_capacity', '制冷量', 'kW'),
  point('chiller.compressor_load', '压缩机负载', '%', 1),
  point('chiller.leaving_chilled_water_temperature', '冷冻水出水温度', 'Cel'),
  point('chiller.entering_chilled_water_temperature', '冷冻水回水温度', 'Cel'),
  point('chiller.chilled_water_temperature_setpoint', '冷冻水设定温度', 'Cel'),
  point('chiller.entering_cooling_water_temperature', '冷却水进水温度', 'Cel'),
  point('chiller.business_revision', '业务版本', undefined, 0),
  point('chiller.fault_code', '故障代码'),

  point('chwp.run_state', '运行状态'),
  point('chwp.frequency', '运行频率', 'Hz', 1),
  point('chwp.speed', '转速', '%', 1),
  point('chwp.flow_rate', '冷冻水流量', 'm3/h'),
  point('chwp.power', '水泵功率', 'kW'),
  point('chwp.business_revision', '业务版本', undefined, 0),
  point('chwp.fault_code', '故障代码'),

  point('cwp.run_state', '运行状态'),
  point('cwp.frequency', '运行频率', 'Hz', 1),
  point('cwp.speed', '转速', '%', 1),
  point('cwp.flow_rate', '冷却水流量', 'm3/h'),
  point('cwp.power', '水泵功率', 'kW'),
  point('cwp.business_revision', '业务版本', undefined, 0),
  point('cwp.fault_code', '故障代码'),

  point('cooling_tower.run_state', '运行状态'),
  point('cooling_tower.fan_speed', '风机转速', '%', 1),
  point('cooling_tower.entering_water_temperature', '进塔水温', 'Cel'),
  point('cooling_tower.leaving_water_temperature', '出塔水温', 'Cel'),
  point('cooling_tower.ambient_wet_bulb_temperature', '室外湿球温度', 'Cel'),
  point('cooling_tower.approach_temperature', '冷却塔逼近温度', 'Cel'),
  point('cooling_tower.power', '冷却塔功率', 'kW'),
  point('cooling_tower.business_revision', '业务版本', undefined, 0),
  point('cooling_tower.fault_code', '故障代码'),

  point('hvac_meter.active_power', '中央空调总功率', 'kW'),
  point('hvac_meter.energy', '累计电量', 'kWh'),
  point('hvac_meter.power_factor', '功率因数', undefined, 3),
  point('hvac_meter.frequency', '电网频率', 'Hz', 2),

  point('btu_meter.supply_water_temperature', '供水温度', 'Cel'),
  point('btu_meter.return_water_temperature', '回水温度', 'Cel'),
  point('btu_meter.temperature_difference', '供回水温差', 'Cel'),
  point('btu_meter.flow_rate', '冷冻水流量', 'm3/h'),
  point('btu_meter.instant_cooling_capacity', '瞬时制冷量', 'kW'),
  point('btu_meter.accumulated_cooling_energy', '累计冷量', 'kWh'),
].map((definition) => [definition.key, definition])));

const profile = (
  kind: CentralPlantProfileKind,
  title: string,
  keys: readonly string[],
  highlightKeys: readonly string[],
): DeviceTelemetryProfile => Object.freeze({ kind, title, keys: Object.freeze([...keys]), highlightKeys: Object.freeze([...highlightKeys]) });

const PROFILES: Readonly<Record<CentralPlantProfileKind, DeviceTelemetryProfile>> = Object.freeze({
  CHILLER: profile('CHILLER', '冷水机组', [
    'chiller.run_state',
    'chiller.power',
    'chiller.cop',
    'chiller.cooling_capacity',
    'chiller.compressor_load',
    'chiller.leaving_chilled_water_temperature',
    'chiller.entering_chilled_water_temperature',
    'chiller.chilled_water_temperature_setpoint',
    'chiller.entering_cooling_water_temperature',
    'chiller.business_revision',
    'chiller.fault_code',
  ], [
    'chiller.run_state',
    'chiller.power',
    'chiller.cop',
    'chiller.cooling_capacity',
  ]),
  CHILLED_WATER_PUMP: profile('CHILLED_WATER_PUMP', '冷冻水泵', [
    'chwp.run_state',
    'chwp.frequency',
    'chwp.speed',
    'chwp.flow_rate',
    'chwp.power',
    'chwp.business_revision',
    'chwp.fault_code',
  ], [
    'chwp.run_state',
    'chwp.frequency',
    'chwp.flow_rate',
    'chwp.power',
  ]),
  COOLING_WATER_PUMP: profile('COOLING_WATER_PUMP', '冷却水泵', [
    'cwp.run_state',
    'cwp.frequency',
    'cwp.speed',
    'cwp.flow_rate',
    'cwp.power',
    'cwp.business_revision',
    'cwp.fault_code',
  ], [
    'cwp.run_state',
    'cwp.frequency',
    'cwp.flow_rate',
    'cwp.power',
  ]),
  COOLING_TOWER: profile('COOLING_TOWER', '冷却塔', [
    'cooling_tower.run_state',
    'cooling_tower.fan_speed',
    'cooling_tower.entering_water_temperature',
    'cooling_tower.leaving_water_temperature',
    'cooling_tower.ambient_wet_bulb_temperature',
    'cooling_tower.approach_temperature',
    'cooling_tower.power',
    'cooling_tower.business_revision',
    'cooling_tower.fault_code',
  ], [
    'cooling_tower.run_state',
    'cooling_tower.fan_speed',
    'cooling_tower.approach_temperature',
    'cooling_tower.power',
  ]),
  HVAC_POWER_METER: profile('HVAC_POWER_METER', '中央空调电表', [
    'hvac_meter.active_power',
    'hvac_meter.energy',
    'hvac_meter.power_factor',
    'hvac_meter.frequency',
  ], [
    'hvac_meter.active_power',
    'hvac_meter.energy',
    'hvac_meter.power_factor',
    'hvac_meter.frequency',
  ]),
  BTU_METER: profile('BTU_METER', '冷量表', [
    'btu_meter.supply_water_temperature',
    'btu_meter.return_water_temperature',
    'btu_meter.temperature_difference',
    'btu_meter.flow_rate',
    'btu_meter.instant_cooling_capacity',
    'btu_meter.accumulated_cooling_energy',
  ], [
    'btu_meter.instant_cooling_capacity',
    'btu_meter.temperature_difference',
    'btu_meter.flow_rate',
    'btu_meter.accumulated_cooling_energy',
  ]),
  GENERIC: profile('GENERIC', '设备遥测', [
    'temperature',
    'humidity',
    'setpoint',
    'power',
  ], [
    'temperature',
    'humidity',
    'setpoint',
    'power',
  ]),
});

const TYPE_ALIASES: Readonly<Record<string, CentralPlantProfileKind>> = Object.freeze({
  CHILLER: 'CHILLER',
  WATER_COOLED_CHILLER: 'CHILLER',
  CHILLED_WATER_PUMP: 'CHILLED_WATER_PUMP',
  CHWP: 'CHILLED_WATER_PUMP',
  COOLING_WATER_PUMP: 'COOLING_WATER_PUMP',
  CWP: 'COOLING_WATER_PUMP',
  COOLING_TOWER: 'COOLING_TOWER',
  CT: 'COOLING_TOWER',
  HVAC_POWER_METER: 'HVAC_POWER_METER',
  POWER_METER: 'HVAC_POWER_METER',
  ELECTRIC_METER: 'HVAC_POWER_METER',
  BTU_METER: 'BTU_METER',
  THERMAL_ENERGY_METER: 'BTU_METER',
  COOLING_METER: 'BTU_METER',
});

function normalizedDeviceType(deviceType: string | null | undefined): string {
  return (deviceType ?? '').trim().toUpperCase().replace(/[^A-Z0-9]+/g, '_').replace(/^_+|_+$/g, '');
}

export function getDeviceTelemetryProfile(deviceType: string | null | undefined): DeviceTelemetryProfile {
  const kind = TYPE_ALIASES[normalizedDeviceType(deviceType)] ?? 'GENERIC';
  return PROFILES[kind];
}

export function telemetryPointDefinition(key: string): TelemetryPointDefinition {
  return POINTS[key] ?? { key, label: key, precision: 3 };
}

function formatNumber(value: number, precision: number): string {
  if (!Number.isFinite(value)) return String(value);
  if (Number.isInteger(value)) return String(value);
  return value.toFixed(precision).replace(/0+$/, '').replace(/\.$/, '');
}

export function formatTelemetryDisplayValue(value: unknown, precision = 3): string {
  if (typeof value === 'number') return formatNumber(value, precision);
  if (typeof value === 'string' || typeof value === 'boolean') return String(value);
  return JSON.stringify(value) ?? '—';
}

export function formatTelemetryUnit(unit: string | null | undefined): string | null {
  if (!unit) return null;
  if (unit === 'Cel') return '°C';
  if (unit === 'm3/h') return 'm³/h';
  return unit;
}

export function buildDeviceTelemetryHighlights(
  deviceType: string | null | undefined,
  snapshot: TelemetrySnapshotLike,
): DeviceTelemetryHighlight[] {
  const profileDefinition = getDeviceTelemetryProfile(deviceType);
  const values = new Map(snapshot.values.map((state) => [state.key, state]));
  return profileDefinition.highlightKeys.map((key) => {
    const definition = telemetryPointDefinition(key);
    const state = values.get(key);
    if (!state || state.state === 'MISSING') {
      return {
        key,
        label: definition.label,
        displayValue: 'MISSING',
        unit: null,
        state: 'MISSING',
        freshness: 'MISSING',
        quality: null,
      };
    }
    return {
      key,
      label: definition.label,
      displayValue: formatTelemetryDisplayValue(state.value, definition.precision ?? 3),
      unit: formatTelemetryUnit(state.unit ?? definition.defaultUnit),
      state: 'PRESENT',
      freshness: state.freshness,
      quality: state.quality,
    };
  });
}
