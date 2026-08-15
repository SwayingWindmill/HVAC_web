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
  point('plant_temperature', '温度', 'Cel'),
  point('plant_delta_t', '温差', 'Cel'),

  point('chiller_run_state', '运行状态'),
  point('chiller_power', '主机功率', 'kW'),
  point('chiller_cop', '主机 COP', undefined, 2),
  point('chiller_cooling_capacity', '制冷量', 'kW'),
  point('chiller_compressor_load', '压缩机负载', '%', 1),
  point('chiller_load_limit', '负荷上限', '%', 1),
  point('chiller_leaving_chilled_water_temperature', '冷冻水出水温度', 'Cel'),
  point('chiller_entering_chilled_water_temperature', '冷冻水回水温度', 'Cel'),
  point('chiller_chilled_water_temperature_setpoint', '冷冻水设定温度', 'Cel'),
  point('chiller_entering_cooling_water_temperature', '冷却水进水温度', 'Cel'),
  point('chiller_business_revision', '业务版本', undefined, 0),
  point('chiller_fault_code', '故障代码'),

  point('chwp_run_state', '运行状态'),
  point('chwp_frequency', '运行频率', 'Hz', 1),
  point('chwp_speed', '转速', '%', 1),
  point('chwp_flow_rate', '冷冻水流量', 'm3/h'),
  point('chwp_power', '水泵功率', 'kW'),
  point('chwp_business_revision', '业务版本', undefined, 0),
  point('chwp_fault_code', '故障代码'),

  point('cwp_run_state', '运行状态'),
  point('cwp_frequency', '运行频率', 'Hz', 1),
  point('cwp_speed', '转速', '%', 1),
  point('cwp_flow_rate', '冷却水流量', 'm3/h'),
  point('cwp_power', '水泵功率', 'kW'),
  point('cwp_business_revision', '业务版本', undefined, 0),
  point('cwp_fault_code', '故障代码'),

  point('cooling_tower_run_state', '运行状态'),
  point('cooling_tower_fan_speed', '风机转速', '%', 1),
  point('cooling_tower_entering_water_temperature', '进塔水温', 'Cel'),
  point('cooling_tower_leaving_water_temperature', '出塔水温', 'Cel'),
  point('cooling_tower_ambient_wet_bulb_temperature', '室外湿球温度', 'Cel'),
  point('cooling_tower_approach_temperature', '冷却塔逼近温度', 'Cel'),
  point('cooling_tower_power', '冷却塔功率', 'kW'),
  point('cooling_tower_business_revision', '业务版本', undefined, 0),
  point('cooling_tower_fault_code', '故障代码'),

  point('hvac_meter_active_power', '中央空调总功率', 'kW'),
  point('hvac_meter_energy', '累计电量', 'kWh'),
  point('hvac_meter_power_factor', '功率因数', undefined, 3),
  point('hvac_meter_frequency', '电网频率', 'Hz', 2),

  point('btu_meter_supply_water_temperature', '供水温度', 'Cel'),
  point('btu_meter_return_water_temperature', '回水温度', 'Cel'),
  point('btu_meter_temperature_difference', '供回水温差', 'Cel'),
  point('btu_meter_flow_rate', '冷冻水流量', 'm3/h'),
  point('btu_meter_instant_cooling_capacity', '瞬时制冷量', 'kW'),
  point('btu_meter_accumulated_cooling_energy', '累计冷量', 'kWh'),

  point('weather_ambient_dry_bulb_temperature', '室外干球温度', 'Cel'),
  point('weather_ambient_wet_bulb_temperature', '室外湿球温度', 'Cel'),
  point('weather_relative_humidity', '室外相对湿度', '%RH'),
].map((definition) => [definition.key, definition])));

const profile = (
  kind: CentralPlantProfileKind,
  title: string,
  keys: readonly string[],
  highlightKeys: readonly string[],
): DeviceTelemetryProfile => Object.freeze({ kind, title, keys: Object.freeze([...keys]), highlightKeys: Object.freeze([...highlightKeys]) });

const PROFILES: Readonly<Record<CentralPlantProfileKind, DeviceTelemetryProfile>> = Object.freeze({
  CHILLER: profile('CHILLER', '冷水机组', [
    'chiller_run_state',
    'chiller_power',
    'chiller_cop',
    'chiller_cooling_capacity',
    'chiller_compressor_load',
    'chiller_load_limit',
    'chiller_leaving_chilled_water_temperature',
    'chiller_entering_chilled_water_temperature',
    'chiller_chilled_water_temperature_setpoint',
    'chiller_entering_cooling_water_temperature',
    'chiller_business_revision',
    'chiller_fault_code',
  ], [
    'chiller_run_state',
    'chiller_power',
    'chiller_cop',
    'chiller_cooling_capacity',
  ]),
  CHILLED_WATER_PUMP: profile('CHILLED_WATER_PUMP', '冷冻水泵', [
    'chwp_run_state',
    'chwp_frequency',
    'chwp_speed',
    'chwp_flow_rate',
    'chwp_power',
    'chwp_business_revision',
    'chwp_fault_code',
  ], [
    'chwp_run_state',
    'chwp_frequency',
    'chwp_flow_rate',
    'chwp_power',
  ]),
  COOLING_WATER_PUMP: profile('COOLING_WATER_PUMP', '冷却水泵', [
    'cwp_run_state',
    'cwp_frequency',
    'cwp_speed',
    'cwp_flow_rate',
    'cwp_power',
    'cwp_business_revision',
    'cwp_fault_code',
  ], [
    'cwp_run_state',
    'cwp_frequency',
    'cwp_flow_rate',
    'cwp_power',
  ]),
  COOLING_TOWER: profile('COOLING_TOWER', '冷却塔', [
    'cooling_tower_run_state',
    'cooling_tower_fan_speed',
    'cooling_tower_entering_water_temperature',
    'cooling_tower_leaving_water_temperature',
    'cooling_tower_ambient_wet_bulb_temperature',
    'cooling_tower_approach_temperature',
    'cooling_tower_power',
    'cooling_tower_business_revision',
    'cooling_tower_fault_code',
  ], [
    'cooling_tower_run_state',
    'cooling_tower_fan_speed',
    'cooling_tower_approach_temperature',
    'cooling_tower_power',
  ]),
  HVAC_POWER_METER: profile('HVAC_POWER_METER', '中央空调电表', [
    'hvac_meter_active_power',
    'hvac_meter_energy',
    'hvac_meter_power_factor',
    'hvac_meter_frequency',
  ], [
    'hvac_meter_active_power',
    'hvac_meter_energy',
    'hvac_meter_power_factor',
    'hvac_meter_frequency',
  ]),
  BTU_METER: profile('BTU_METER', '冷量表', [
    'btu_meter_supply_water_temperature',
    'btu_meter_return_water_temperature',
    'btu_meter_temperature_difference',
    'btu_meter_flow_rate',
    'btu_meter_instant_cooling_capacity',
    'btu_meter_accumulated_cooling_energy',
  ], [
    'btu_meter_instant_cooling_capacity',
    'btu_meter_temperature_difference',
    'btu_meter_flow_rate',
    'btu_meter_accumulated_cooling_energy',
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
  GENERIC: 'GENERIC',
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
