export const REAL_ASSETS_CATALOG_SCHEMA_VERSION = 1;
export const REAL_ASSETS_CATALOG_REVISION = 'real-assets-critical-points:v1';

export interface RealAssetsPointDefinition {
  readonly key: string;
  readonly label: string;
  readonly order: number;
  readonly critical: boolean;
  readonly showInList: boolean;
  readonly showInDetail: boolean;
  readonly trendEligible: boolean;
  readonly defaultUnit?: string;
  readonly precision?: number;
}

export interface RealAssetsDeviceProfile {
  readonly kind: string;
  readonly title: string;
  readonly aliases: readonly string[];
  readonly points: readonly RealAssetsPointDefinition[];
}

export type RealAssetsProfileResolution =
  | { readonly state: 'configured'; readonly profile: RealAssetsDeviceProfile }
  | { readonly state: 'unconfigured'; readonly normalizedDeviceType: string };

const point = (
  key: string,
  label: string,
  order: number,
  options: Partial<Omit<RealAssetsPointDefinition, 'key' | 'label' | 'order'>> = {},
): RealAssetsPointDefinition => Object.freeze({
  key,
  label,
  order,
  critical: options.critical ?? true,
  showInList: options.showInList ?? true,
  showInDetail: options.showInDetail ?? true,
  trendEligible: options.trendEligible ?? false,
  defaultUnit: options.defaultUnit,
  precision: options.precision,
});

const profile = (
  kind: string,
  title: string,
  aliases: readonly string[],
  points: readonly RealAssetsPointDefinition[],
): RealAssetsDeviceProfile => Object.freeze({
  kind,
  title,
  aliases: Object.freeze([...aliases]),
  points: Object.freeze([...points].sort((left, right) => left.order - right.order)),
});

export const REAL_ASSETS_DEVICE_PROFILES = Object.freeze([
  profile('CHILLER', '冷水机组', ['CHILLER', 'WATER_COOLED_CHILLER'], [
    point('chiller_run_state', '运行状态', 10, { trendEligible: false }),
    point('chiller_power', '主机功率', 20, { defaultUnit: 'kW', precision: 1, trendEligible: true }),
    point('chiller_cop', '主机 COP', 30, { precision: 2, trendEligible: true }),
    point('chiller_cooling_capacity', '制冷量', 40, { defaultUnit: 'kW', precision: 1, trendEligible: true }),
  ]),
  profile('CHILLED_WATER_PUMP', '冷冻水泵', ['CHILLED_WATER_PUMP', 'CHWP'], [
    point('chwp_run_state', '运行状态', 10),
    point('chwp_frequency', '运行频率', 20, { defaultUnit: 'Hz', precision: 1, trendEligible: true }),
    point('chwp_flow_rate', '冷冻水流量', 30, { defaultUnit: 'm3/h', precision: 1, trendEligible: true }),
    point('chwp_power', '水泵功率', 40, { defaultUnit: 'kW', precision: 1, trendEligible: true }),
  ]),
  profile('COOLING_WATER_PUMP', '冷却水泵', ['COOLING_WATER_PUMP', 'CWP'], [
    point('cwp_run_state', '运行状态', 10),
    point('cwp_frequency', '运行频率', 20, { defaultUnit: 'Hz', precision: 1, trendEligible: true }),
    point('cwp_flow_rate', '冷却水流量', 30, { defaultUnit: 'm3/h', precision: 1, trendEligible: true }),
    point('cwp_power', '水泵功率', 40, { defaultUnit: 'kW', precision: 1, trendEligible: true }),
  ]),
  profile('COOLING_TOWER', '冷却塔', ['COOLING_TOWER', 'CT'], [
    point('cooling_tower_run_state', '运行状态', 10),
    point('cooling_tower_fan_speed', '风机转速', 20, { defaultUnit: '%', precision: 1, trendEligible: true }),
    point('cooling_tower_approach_temperature', '逼近温度', 30, { defaultUnit: 'Cel', precision: 1, trendEligible: true }),
    point('cooling_tower_power', '冷却塔功率', 40, { defaultUnit: 'kW', precision: 1, trendEligible: true }),
  ]),
  profile('HVAC_POWER_METER', '中央空调电表', ['HVAC_POWER_METER', 'POWER_METER', 'ELECTRIC_METER'], [
    point('hvac_meter_active_power', '中央空调总功率', 10, { defaultUnit: 'kW', precision: 1, trendEligible: true }),
    point('hvac_meter_energy', '累计电量', 20, { defaultUnit: 'kWh', precision: 1, trendEligible: true }),
    point('hvac_meter_power_factor', '功率因数', 30, { precision: 3, trendEligible: true }),
    point('hvac_meter_frequency', '电网频率', 40, { defaultUnit: 'Hz', precision: 2, trendEligible: true }),
  ]),
  profile('BTU_METER', '冷量表', ['BTU_METER', 'THERMAL_ENERGY_METER', 'COOLING_METER'], [
    point('btu_meter_instant_cooling_capacity', '瞬时制冷量', 10, { defaultUnit: 'kW', precision: 1, trendEligible: true }),
    point('btu_meter_temperature_difference', '供回水温差', 20, { defaultUnit: 'Cel', precision: 1, trendEligible: true }),
    point('btu_meter_flow_rate', '冷冻水流量', 30, { defaultUnit: 'm3/h', precision: 1, trendEligible: true }),
    point('btu_meter_accumulated_cooling_energy', '累计冷量', 40, { defaultUnit: 'kWh', precision: 1, trendEligible: true }),
  ]),
  profile('GENERIC', '通用设备', ['GENERIC'], [
    point('temperature', '温度', 10, { defaultUnit: 'Cel', precision: 1, trendEligible: true }),
    point('humidity', '湿度', 20, { defaultUnit: '%RH', precision: 1, trendEligible: true }),
    point('setpoint', '设定值', 30, { defaultUnit: 'Cel', precision: 1, trendEligible: true }),
    point('power', '功率', 40, { defaultUnit: 'kW', precision: 1, trendEligible: true }),
  ]),
] satisfies readonly RealAssetsDeviceProfile[]);

const PROFILE_BY_ALIAS = new Map<string, RealAssetsDeviceProfile>(
  REAL_ASSETS_DEVICE_PROFILES.flatMap((definition) => definition.aliases.map((alias) => [alias, definition] as const)),
);

export function normalizeRealAssetsDeviceType(deviceType: string | null | undefined): string {
  return (deviceType ?? '').trim().toUpperCase().replace(/[^A-Z0-9]+/g, '_').replace(/^_+|_+$/g, '');
}

export function resolveRealAssetsProfile(deviceType: string | null | undefined): RealAssetsProfileResolution {
  const normalizedDeviceType = normalizeRealAssetsDeviceType(deviceType);
  const profileDefinition = PROFILE_BY_ALIAS.get(normalizedDeviceType);
  return profileDefinition
    ? { state: 'configured', profile: profileDefinition }
    : { state: 'unconfigured', normalizedDeviceType };
}

export function listPointDefinitions(resolution: RealAssetsProfileResolution): readonly RealAssetsPointDefinition[] {
  return resolution.state === 'configured'
    ? resolution.profile.points.filter((definition) => definition.showInList)
    : [];
}

export function listTelemetryKeys(resolution: RealAssetsProfileResolution): readonly string[] {
  return listPointDefinitions(resolution).map((definition) => definition.key);
}
