// Operator-facing vocabulary for central-plant telemetry, keyed by the Registry point source key.
export interface TelemetryPointDefinition {
  key: string;
  label: string;
  defaultUnit?: string;
  precision?: number;
}

// Display resolution follows what an operator reads from the quantity, not sensor noise.
const PRECISION_BY_UNIT: Readonly<Record<string, number>> = {
  Cel: 1,
  '°C': 1,
  kW: 1,
  kWh: 0,
  '%': 1,
  '%RH': 0,
  'm3/h': 0,
  Hz: 1,
};

const point = (
  key: string,
  label: string,
  defaultUnit?: string,
  precision = defaultUnit ? PRECISION_BY_UNIT[defaultUnit] ?? 2 : 2,
): TelemetryPointDefinition => ({ key, label, defaultUnit, precision });

const POINTS: Readonly<Record<string, TelemetryPointDefinition>> = Object.freeze(Object.fromEntries([
  point('temperature', '温度', '°C'),
  point('humidity', '湿度', '%RH'),
  point('setpoint', '设定值', '°C'),
  point('power', '功率', 'kW'),
  point('plant_temperature', '温度', 'Cel'),
  point('plant_delta_t', '温差', 'Cel'),

  point('chiller.run_state', '运行状态'),
  point('chiller.power', '主机功率', 'kW'),
  point('chiller.cop', '主机 COP', undefined, 2),
  point('chiller.cooling_capacity', '制冷量', 'kW'),
  point('chiller.compressor_load', '压缩机负载', '%', 1),
  point('chiller.load_limit', '负荷上限', '%', 1),
  point('chiller.leaving_chilled_water_temperature', '冷冻水出水温度', 'Cel'),
  point('chiller.entering_chilled_water_temperature', '冷冻水回水温度', 'Cel'),
  point('chiller.chilled_water_temperature_setpoint', '冷冻水设定温度', 'Cel'),
  point('chiller.entering_cooling_water_temperature', '冷却水进水温度', 'Cel'),
  point('chiller.fault_code', '故障代码'),

  point('chwp.run_state', '运行状态'),
  point('chwp.frequency', '运行频率', 'Hz', 1),
  point('chwp.speed', '转速', '%', 1),
  point('chwp.flow_rate', '冷冻水流量', 'm3/h'),
  point('chwp.power', '水泵功率', 'kW'),
  point('chwp.fault_code', '故障代码'),

  point('cwp.run_state', '运行状态'),
  point('cwp.frequency', '运行频率', 'Hz', 1),
  point('cwp.speed', '转速', '%', 1),
  point('cwp.flow_rate', '冷却水流量', 'm3/h'),
  point('cwp.power', '水泵功率', 'kW'),
  point('cwp.fault_code', '故障代码'),

  point('cooling_tower.run_state', '运行状态'),
  point('cooling_tower.fan_speed', '风机转速', '%', 1),
  point('cooling_tower.entering_water_temperature', '进塔水温', 'Cel'),
  point('cooling_tower.leaving_water_temperature', '出塔水温', 'Cel'),
  point('cooling_tower.ambient_wet_bulb_temperature', '室外湿球温度', 'Cel'),
  point('cooling_tower.approach_temperature', '冷却塔逼近温度', 'Cel'),
  point('cooling_tower.power', '冷却塔功率', 'kW'),
  point('cooling_tower.fault_code', '故障代码'),

  point('hvac_meter.active_power', '中央空调总功率', 'kW'),
  point('hvac_meter.energy', '累计电量', 'kWh'),
  point('hvac_meter.power_factor', '功率因数', undefined, 2),
  point('hvac_meter.frequency', '电网频率', 'Hz', 2),

  point('btu_meter.supply_water_temperature', '供水温度', 'Cel'),
  point('btu_meter.return_water_temperature', '回水温度', 'Cel'),
  point('btu_meter.temperature_difference', '供回水温差', 'Cel'),
  point('btu_meter.flow_rate', '冷冻水流量', 'm3/h'),
  point('btu_meter.instant_cooling_capacity', '瞬时制冷量', 'kW'),
  point('btu_meter.accumulated_cooling_energy', '累计冷量', 'kWh'),

  point('weather.ambient_dry_bulb_temperature', '室外干球温度', 'Cel'),
  point('weather.ambient_wet_bulb_temperature', '室外湿球温度', 'Cel'),
  point('weather.relative_humidity', '室外相对湿度', '%RH'),
].map((definition) => [definition.key, definition])));

export function telemetryPointDefinition(key: string): TelemetryPointDefinition {
  return POINTS[key] ?? { key, label: key, precision: 2 };
}

function formatNumber(value: number, precision: number): string {
  if (!Number.isFinite(value)) return String(value);
  if (Number.isInteger(value)) return String(value);
  return value.toFixed(precision).replace(/0+$/, '').replace(/\.$/, '');
}

// Equipment state values reported by plant controllers.
const STATE_LABELS: Readonly<Record<string, string>> = {
  RUNNING: '运行',
  STOPPED: '停机',
  STANDBY: '待机',
  FAULT: '故障',
};

export function formatTelemetryDisplayValue(value: unknown, precision = 3): string {
  if (typeof value === 'number') return formatNumber(value, precision);
  if (typeof value === 'string') return value === '' ? '—' : STATE_LABELS[value] ?? value;
  if (typeof value === 'boolean') return String(value);
  return JSON.stringify(value) ?? '—';
}

export function formatTelemetryUnit(unit: string | null | undefined): string | null {
  if (!unit) return null;
  if (unit === 'Cel') return '°C';
  if (unit === 'm3/h') return 'm³/h';
  return unit;
}
