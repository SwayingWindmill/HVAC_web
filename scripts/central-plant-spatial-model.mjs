import { centralPlantDevices } from './central-plant-local-contract.mjs';

export const centralPlantAreas = Object.freeze([
  { id: 'building-main', parentId: null, code: 'main-building', name: '商业办公楼', type: 'BUILDING' },
  { id: 'plant-room', parentId: 'building-main', code: 'central-plant-room', name: '中央机房', type: 'PLANT_ROOM' },
  { id: 'rooftop', parentId: 'building-main', code: 'rooftop', name: '屋面', type: 'ROOFTOP' },
  { id: 'outdoor', parentId: 'building-main', code: 'outdoor', name: '室外环境', type: 'OUTDOOR' },
]);

const areaByDeviceType = Object.freeze({
  COOLING_TOWER: 'rooftop',
  WEATHER_STATION: 'outdoor',
});

const deviceEndpointType = Object.freeze({
  CHILLER: 'CHILLER_CONTROLLER',
  CHILLED_WATER_PUMP: 'PUMP_CONTROLLER',
  COOLING_WATER_PUMP: 'PUMP_CONTROLLER',
  COOLING_TOWER: 'COOLING_TOWER_CONTROLLER',
  HVAC_POWER_METER: 'POWER_METER',
  BTU_METER: 'BTU_METER',
  WEATHER_STATION: 'WEATHER_STATION',
});

export const centralPlantEquipment = Object.freeze(centralPlantDevices.map((device) => Object.freeze({
  id: `equipment-${device.slug}`,
  code: device.slug,
  areaId: areaByDeviceType[device.type] ?? 'plant-room',
  name: device.name,
  type: device.type,
  platformDeviceId: device.platformDeviceId,
})));

export const centralPlantDeviceEndpoints = Object.freeze(centralPlantDevices.map((device) => Object.freeze({
  id: device.name,
  platformDeviceId: device.platformDeviceId,
  areaId: areaByDeviceType[device.type] ?? 'plant-room',
  name: `${device.name} 通信端点`,
  type: deviceEndpointType[device.type] ?? device.type,
  equipmentIds: Object.freeze([`equipment-${device.slug}`]),
})));

const equipmentSubject = (slug) => ({ subjectType: 'EQUIPMENT', subjectId: `equipment-${slug}` });
const siteSubject = () => ({ subjectType: 'SITE', subjectId: '' });

export const centralPlantSensors = Object.freeze([
  { id: 'sensor-ch01-chws-temp', deviceId: 'CHILLER-01', mountedAreaId: 'plant-room', ...equipmentSubject('chiller-01'), name: 'CH-01 冷冻水出水温度传感器', type: 'TEMPERATURE', mode: 'WIRED' },
  { id: 'sensor-ch01-chwr-temp', deviceId: 'CHILLER-01', mountedAreaId: 'plant-room', ...equipmentSubject('chiller-01'), name: 'CH-01 冷冻水回水温度传感器', type: 'TEMPERATURE', mode: 'WIRED' },
  { id: 'sensor-ch01-cw-entering-temp', deviceId: 'CHILLER-01', mountedAreaId: 'plant-room', ...equipmentSubject('chiller-01'), name: 'CH-01 冷却水进水温度传感器', type: 'TEMPERATURE', mode: 'WIRED' },
  { id: 'sensor-ch01-power', deviceId: 'CHILLER-01', mountedAreaId: 'plant-room', ...equipmentSubject('chiller-01'), name: 'CH-01 功率测量', type: 'POWER', mode: 'EMBEDDED' },
  { id: 'sensor-chwp01-flow', deviceId: 'CHWP-01', mountedAreaId: 'plant-room', ...equipmentSubject('chwp-01'), name: 'CHWP-01 流量传感器', type: 'FLOW', mode: 'WIRED' },
  { id: 'sensor-chwp01-power', deviceId: 'CHWP-01', mountedAreaId: 'plant-room', ...equipmentSubject('chwp-01'), name: 'CHWP-01 功率测量', type: 'POWER', mode: 'EMBEDDED' },
  { id: 'sensor-cwp01-flow', deviceId: 'CWP-01', mountedAreaId: 'plant-room', ...equipmentSubject('cwp-01'), name: 'CWP-01 流量传感器', type: 'FLOW', mode: 'WIRED' },
  { id: 'sensor-cwp01-power', deviceId: 'CWP-01', mountedAreaId: 'plant-room', ...equipmentSubject('cwp-01'), name: 'CWP-01 功率测量', type: 'POWER', mode: 'EMBEDDED' },
  { id: 'sensor-ct01-entering-temp', deviceId: 'CT-01', mountedAreaId: 'rooftop', ...equipmentSubject('ct-01'), name: 'CT-01 进水温度传感器', type: 'TEMPERATURE', mode: 'WIRED' },
  { id: 'sensor-ct01-leaving-temp', deviceId: 'CT-01', mountedAreaId: 'rooftop', ...equipmentSubject('ct-01'), name: 'CT-01 出水温度传感器', type: 'TEMPERATURE', mode: 'WIRED' },
  { id: 'sensor-ct01-wet-bulb', deviceId: 'CT-01', mountedAreaId: 'rooftop', ...siteSubject(), name: 'CT-01 本地湿球温度传感器', type: 'TEMPERATURE', mode: 'WIRED' },
  { id: 'sensor-ct01-power', deviceId: 'CT-01', mountedAreaId: 'rooftop', ...equipmentSubject('ct-01'), name: 'CT-01 功率测量', type: 'POWER', mode: 'EMBEDDED' },
  { id: 'sensor-hvac-meter', deviceId: 'METER-HVAC-TOTAL', mountedAreaId: 'plant-room', ...siteSubject(), name: '中央空调总电表测量单元', type: 'ELECTRICAL_METER', mode: 'INDEPENDENT_DEVICE' },
  { id: 'sensor-btu-meter', deviceId: 'BTU-METER-01', mountedAreaId: 'plant-room', ...siteSubject(), name: '中央空调总冷量表测量单元', type: 'THERMAL_METER', mode: 'INDEPENDENT_DEVICE' },
  { id: 'sensor-weather-station', deviceId: 'WEATHER-STATION-01', mountedAreaId: 'outdoor', ...siteSubject(), name: '室外气象站', type: 'WEATHER', mode: 'INDEPENDENT_DEVICE' },
].map(Object.freeze));

const sensorByDeviceSource = new Map([
  ['CHILLER-01/leavingChilledWaterTemperatureC', 'sensor-ch01-chws-temp'],
  ['CHILLER-01/enteringChilledWaterTemperatureC', 'sensor-ch01-chwr-temp'],
  ['CHILLER-01/enteringCoolingWaterTemperatureC', 'sensor-ch01-cw-entering-temp'],
  ['CHILLER-01/powerKw', 'sensor-ch01-power'],
  ['CHWP-01/flowRateM3h', 'sensor-chwp01-flow'],
  ['CHWP-01/powerKw', 'sensor-chwp01-power'],
  ['CWP-01/flowRateM3h', 'sensor-cwp01-flow'],
  ['CWP-01/powerKw', 'sensor-cwp01-power'],
  ['CT-01/enteringWaterTemperatureC', 'sensor-ct01-entering-temp'],
  ['CT-01/leavingWaterTemperatureC', 'sensor-ct01-leaving-temp'],
  ['CT-01/ambientWetBulbTemperatureC', 'sensor-ct01-wet-bulb'],
  ['CT-01/powerKw', 'sensor-ct01-power'],
  ['METER-HVAC-TOTAL/activePowerKw', 'sensor-hvac-meter'],
  ['METER-HVAC-TOTAL/energyKwh', 'sensor-hvac-meter'],
  ['METER-HVAC-TOTAL/powerFactor', 'sensor-hvac-meter'],
  ['METER-HVAC-TOTAL/frequencyHz', 'sensor-hvac-meter'],
  ['BTU-METER-01/supplyWaterTemperatureC', 'sensor-btu-meter'],
  ['BTU-METER-01/returnWaterTemperatureC', 'sensor-btu-meter'],
  ['BTU-METER-01/flowRateM3h', 'sensor-btu-meter'],
  ['BTU-METER-01/accumulatedCoolingEnergyKwh', 'sensor-btu-meter'],
  ['WEATHER-STATION-01/ambientDryBulbTemperatureC', 'sensor-weather-station'],
  ['WEATHER-STATION-01/ambientWetBulbTemperatureC', 'sensor-weather-station'],
  ['WEATHER-STATION-01/relativeHumidityPct', 'sensor-weather-station'],
]);

const stateSources = new Set(['runState', 'businessRevision', 'faultCode']);
const feedbackSources = new Set(['chilledWaterTemperatureSetpointC', 'compressorLoadPct', 'loadLimitPct', 'frequencyHz', 'fanSpeedPct']);
const calculatedPointMeta = new Map([
  ['CHILLER-01/chiller.cooling_capacity', { formulaRevision: 'chiller-cooling-balance:v1', inputPointRefs: ['CHWP-01/chwp.flow_rate', 'CHILLER-01/chiller.entering_chilled_water_temperature', 'CHILLER-01/chiller.leaving_chilled_water_temperature'] }],
  ['CHILLER-01/chiller.cop', { formulaRevision: 'chiller-cop:v1', inputPointRefs: ['CHILLER-01/chiller.cooling_capacity', 'CHILLER-01/chiller.power'] }],
  ['CHWP-01/chwp.speed', { formulaRevision: 'pump-speed-from-frequency:v1', inputPointRefs: ['CHWP-01/chwp.frequency'] }],
  ['CWP-01/cwp.speed', { formulaRevision: 'pump-speed-from-frequency:v1', inputPointRefs: ['CWP-01/cwp.frequency'] }],
  ['CT-01/cooling_tower.approach_temperature', { formulaRevision: 'cooling-tower-approach:v1', inputPointRefs: ['CT-01/cooling_tower.leaving_water_temperature', 'CT-01/cooling_tower.ambient_wet_bulb_temperature'] }],
  ['BTU-METER-01/btu_meter.temperature_difference', { formulaRevision: 'water-temperature-difference:v1', inputPointRefs: ['BTU-METER-01/btu_meter.return_water_temperature', 'BTU-METER-01/btu_meter.supply_water_temperature'] }],
  ['BTU-METER-01/btu_meter.instant_cooling_capacity', { formulaRevision: 'water-cooling-capacity:v1', inputPointRefs: ['BTU-METER-01/btu_meter.flow_rate', 'BTU-METER-01/btu_meter.temperature_difference'] }],
]);

export const centralPlantCalculatedPointCount = calculatedPointMeta.size;

const commandPointBase = (deviceId, equipmentSlug, sourceKey, telemetryKey, name, valueType, metadata, unit = '') => ({
  deviceId,
  sensorId: '',
  ...equipmentSubject(equipmentSlug),
  sourceKey,
  telemetryKey,
  name,
  kind: 'COMMAND',
  valueType,
  ...(unit ? { unit } : {}),
  writable: true,
  sampleInterval: '1s',
  publishInterval: '2s',
  staleAfter: '10s',
  sourceMetadata: metadata,
});

const actionCommandPoint = (deviceId, equipmentSlug, sourceKey, telemetryKey, name, capability, revision, feedbackPointKey) => commandPointBase(
  deviceId,
  equipmentSlug,
  sourceKey,
  telemetryKey,
  name,
  'STRING',
  { controlKind: 'ACTION', capability, capabilityRevision: revision, feedbackPointKey },
);

const numericCommandPoint = (deviceId, equipmentSlug, sourceKey, telemetryKey, name, capability, revision, parameterKey, minimum, maximum, step, feedbackPointKey, unit) => commandPointBase(
  deviceId,
  equipmentSlug,
  sourceKey,
  telemetryKey,
  name,
  'NUMBER',
  { controlKind: 'NUMBER', capability, capabilityRevision: revision, parameterKey, minimum, maximum, step, feedbackPointKey },
  unit,
);

export function buildCentralPlantControlPoints() {
  return [
    actionCommandPoint('CHILLER-01', 'chiller-01', 'start', 'chiller.command.start', '启动冷水机', 'START', 'capability:start:v1', 'chiller.run_state'),
    actionCommandPoint('CHILLER-01', 'chiller-01', 'stop', 'chiller.command.stop', '停止冷水机', 'STOP', 'capability:stop:v1', 'chiller.run_state'),
    actionCommandPoint('CHILLER-01', 'chiller-01', 'resetFault', 'chiller.command.reset_fault', '复位冷水机故障', 'RESET_FAULT', 'capability:reset-fault:v1', 'chiller.fault_code'),
    numericCommandPoint('CHILLER-01', 'chiller-01', 'setChilledWaterTemperatureSetpoint', 'chiller.command.chilled_water_temperature_setpoint', '冷冻水出水设定温度', 'SET_CHILLED_WATER_TEMPERATURE_SETPOINT', 'capability:set-chilled-water-temperature-setpoint:v1', 'setpointC', 5, 12, 0.5, 'chiller.chilled_water_temperature_setpoint', 'Cel'),
    numericCommandPoint('CHILLER-01', 'chiller-01', 'setLoadLimit', 'chiller.command.load_limit', '冷水机负荷上限', 'SET_LOAD_LIMIT', 'capability:set-load-limit:v1', 'loadLimitPct', 20, 100, 1, 'chiller.load_limit', '%'),

    actionCommandPoint('CHWP-01', 'chwp-01', 'start', 'chwp.command.start', '启动冷冻水泵', 'START', 'capability:start:v1', 'chwp.run_state'),
    actionCommandPoint('CHWP-01', 'chwp-01', 'stop', 'chwp.command.stop', '停止冷冻水泵', 'STOP', 'capability:stop:v1', 'chwp.run_state'),
    actionCommandPoint('CHWP-01', 'chwp-01', 'resetFault', 'chwp.command.reset_fault', '复位冷冻水泵故障', 'RESET_FAULT', 'capability:reset-fault:v1', 'chwp.fault_code'),
    numericCommandPoint('CHWP-01', 'chwp-01', 'setFrequency', 'chwp.command.frequency', '冷冻水泵频率', 'SET_FREQUENCY', 'capability:set-frequency:v1', 'frequencyHz', 20, 50, 0.5, 'chwp.frequency', 'Hz'),

    actionCommandPoint('CWP-01', 'cwp-01', 'start', 'cwp.command.start', '启动冷却水泵', 'START', 'capability:start:v1', 'cwp.run_state'),
    actionCommandPoint('CWP-01', 'cwp-01', 'stop', 'cwp.command.stop', '停止冷却水泵', 'STOP', 'capability:stop:v1', 'cwp.run_state'),
    actionCommandPoint('CWP-01', 'cwp-01', 'resetFault', 'cwp.command.reset_fault', '复位冷却水泵故障', 'RESET_FAULT', 'capability:reset-fault:v1', 'cwp.fault_code'),
    numericCommandPoint('CWP-01', 'cwp-01', 'setFrequency', 'cwp.command.frequency', '冷却水泵频率', 'SET_FREQUENCY', 'capability:set-frequency:v1', 'frequencyHz', 20, 50, 0.5, 'cwp.frequency', 'Hz'),

    actionCommandPoint('CT-01', 'ct-01', 'start', 'cooling_tower.command.start', '启动冷却塔', 'START', 'capability:start:v1', 'cooling_tower.run_state'),
    actionCommandPoint('CT-01', 'ct-01', 'stop', 'cooling_tower.command.stop', '停止冷却塔', 'STOP', 'capability:stop:v1', 'cooling_tower.run_state'),
    actionCommandPoint('CT-01', 'ct-01', 'resetFault', 'cooling_tower.command.reset_fault', '复位冷却塔故障', 'RESET_FAULT', 'capability:reset-fault:v1', 'cooling_tower.fault_code'),
    numericCommandPoint('CT-01', 'ct-01', 'setFanSpeed', 'cooling_tower.command.fan_speed', '冷却塔风机转速', 'SET_FAN_SPEED', 'capability:set-fan-speed:v1', 'fanSpeedPct', 20, 100, 1, 'cooling_tower.fan_speed', '%'),
  ].map(Object.freeze);
}

const humanize = (value) => value
  .replaceAll('.', ' ')
  .replaceAll('_', ' ')
  .replace(/([a-z0-9])([A-Z])/g, '$1 $2')
  .replace(/\s+/g, ' ')
  .trim();

const subjectForDevice = (deviceId, sourceKey) => {
  const sensorId = sensorByDeviceSource.get(`${deviceId}/${sourceKey}`);
  if (sensorId) {
    const sensor = centralPlantSensors.find((candidate) => candidate.id === sensorId);
    return { subjectType: sensor.subjectType, subjectId: sensor.subjectId };
  }
  if (deviceId === 'METER-HVAC-TOTAL' || deviceId === 'BTU-METER-01' || deviceId === 'WEATHER-STATION-01') return siteSubject();
  const endpoint = centralPlantDeviceEndpoints.find((candidate) => candidate.id === deviceId);
  return equipmentSubject(endpoint.equipmentIds[0].replace(/^equipment-/, ''));
};

export function buildCentralPlantSimulatorPoints(adapterTemplate) {
  if (!Array.isArray(adapterTemplate?.devices) || adapterTemplate.devices.length !== centralPlantDeviceEndpoints.length) {
    throw new Error('adapter template and central plant Device Endpoint contract differ');
  }
  return adapterTemplate.devices.flatMap((device, deviceIndex) => {
    const endpoint = centralPlantDeviceEndpoints[deviceIndex];
    return device.points.map((point) => {
      const identity = `${endpoint.id}/${point.telemetryKey}`;
      const calculated = calculatedPointMeta.get(identity);
      const sensorId = sensorByDeviceSource.get(`${endpoint.id}/${point.sourceKey}`) ?? '';
      const subject = subjectForDevice(endpoint.id, point.sourceKey);
      const kind = calculated ? 'CALCULATED' : stateSources.has(point.sourceKey) ? 'STATE' : feedbackSources.has(point.sourceKey) ? 'FEEDBACK' : 'MEASURED';
      const sampleInterval = kind === 'STATE' ? '2s' : '1s';
      const publishInterval = kind === 'STATE' ? '5s' : '2s';
      return Object.freeze({
        deviceId: endpoint.id,
        sensorId,
        ...subject,
        sourceKey: point.sourceKey,
        telemetryKey: point.telemetryKey,
        name: humanize(point.telemetryKey),
        kind,
        valueType: point.valueType,
        ...(point.unit ? { unit: point.unit } : {}),
        writable: false,
        sampleInterval,
        publishInterval,
        staleAfter: kind === 'STATE' ? '15s' : '10s',
        ...(calculated ? calculated : { sourceProtocol: 'SIMULATED', sourceAddress: `${endpoint.id}:${point.sourceKey}` }),
      });
    });
  });
}

export function buildCentralPlantSimulatorConfig(adapterTemplate, overrides = {}) {
  return {
    schemaVersion: 2,
    gatewayId: 'EG8200-COMMERCIAL-001',
    thingsBoardBaseUrl: overrides.thingsBoardBaseUrl ?? 'http://localhost:8080',
    publishInterval: overrides.publishInterval ?? '5s',
    plant: {
      ambientDryBulbC: 34,
      ambientWetBulbC: 27,
      loadFraction: 0.72,
      initialEnergyKwh: overrides.initialEnergyKwh ?? 0,
      chiller: { id: 'CHILLER-01', ratedCoolingCapacityKw: 1200, baseCop: 5.6, initialSetpointC: 7, initialLoadLimitPct: 100, initiallyRunning: true },
      chilledWaterPump: { id: 'CHWP-01', ratedPowerKw: 45, ratedFlowM3h: 220, initialFrequencyHz: 50, initiallyRunning: true },
      coolingWaterPump: { id: 'CWP-01', ratedPowerKw: 37, ratedFlowM3h: 260, initialFrequencyHz: 50, initiallyRunning: true },
      coolingTower: { id: 'CT-01', ratedFanPowerKw: 18.5, initialFanSpeedPct: 80, initiallyRunning: true },
      powerMeterId: 'METER-HVAC-TOTAL',
      btuMeterId: 'BTU-METER-01',
      weatherStationId: 'WEATHER-STATION-01',
    },
    areas: centralPlantAreas.map(({ id, parentId, name, type }) => ({ id, ...(parentId ? { parentId } : {}), name, type })),
    equipment: centralPlantEquipment.map(({ id, areaId, name, type }) => ({ id, areaId, name, type })),
    devices: centralPlantDeviceEndpoints.map(({ id, areaId, name, type, equipmentIds }) => ({ id, areaId, name, type, equipmentIds: [...equipmentIds] })),
    sensors: centralPlantSensors.map((sensor) => ({ ...sensor })),
    points: buildCentralPlantSimulatorPoints(adapterTemplate).map((point) => ({ ...point })),
    credentialEnvByDeviceId: Object.fromEntries(centralPlantDeviceEndpoints.map((endpoint) => [endpoint.id, `TB_TOKEN_${endpoint.id.replace(/[^A-Za-z0-9]+/g, '_')}`])),
  };
}
