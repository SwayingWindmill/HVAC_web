import { centralPlantDevices, localUUID } from './central-plant-local-contract.mjs';

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

// Sensor is optional physical traceability only. Device-native measurement
// channels, meters and weather stations are modeled directly as Device -> Point.
export const centralPlantSensors = Object.freeze([
  { id: 'sensor-ch01-chws-temp', deviceId: 'CHILLER-01', mountedAreaId: 'plant-room', name: 'CH-01 冷冻水出水温度探头', type: 'TEMPERATURE', serialNumber: 'SIM-PT1000-CH01-CHWS', calibrationDueAt: '2027-08-01T00:00:00Z' },
  { id: 'sensor-ch01-chwr-temp', deviceId: 'CHILLER-01', mountedAreaId: 'plant-room', name: 'CH-01 冷冻水回水温度探头', type: 'TEMPERATURE', serialNumber: 'SIM-PT1000-CH01-CHWR', calibrationDueAt: '2027-08-01T00:00:00Z' },
  { id: 'sensor-ch01-cw-entering-temp', deviceId: 'CHILLER-01', mountedAreaId: 'plant-room', name: 'CH-01 冷却水进水温度探头', type: 'TEMPERATURE', serialNumber: 'SIM-PT1000-CH01-CWE', calibrationDueAt: '2027-08-01T00:00:00Z' },
  { id: 'sensor-chwp01-flow', deviceId: 'CHWP-01', mountedAreaId: 'plant-room', name: 'CHWP-01 流量探头', type: 'FLOW', serialNumber: 'SIM-FLOW-CHWP01', calibrationDueAt: '2027-08-01T00:00:00Z' },
  { id: 'sensor-cwp01-flow', deviceId: 'CWP-01', mountedAreaId: 'plant-room', name: 'CWP-01 流量探头', type: 'FLOW', serialNumber: 'SIM-FLOW-CWP01', calibrationDueAt: '2027-08-01T00:00:00Z' },
  { id: 'sensor-ct01-entering-temp', deviceId: 'CT-01', mountedAreaId: 'rooftop', name: 'CT-01 进水温度探头', type: 'TEMPERATURE', serialNumber: 'SIM-PT1000-CT01-E', calibrationDueAt: '2027-08-01T00:00:00Z' },
  { id: 'sensor-ct01-leaving-temp', deviceId: 'CT-01', mountedAreaId: 'rooftop', name: 'CT-01 出水温度探头', type: 'TEMPERATURE', serialNumber: 'SIM-PT1000-CT01-L', calibrationDueAt: '2027-08-01T00:00:00Z' },
  { id: 'sensor-ct01-wet-bulb', deviceId: 'CT-01', mountedAreaId: 'rooftop', name: 'CT-01 本地湿球温度探头', type: 'TEMPERATURE', serialNumber: 'SIM-WB-CT01', calibrationDueAt: '2027-08-01T00:00:00Z' },
].map(Object.freeze));

const sensorByDeviceSource = new Map([
  ['CHILLER-01/leavingChilledWaterTemperatureC', 'sensor-ch01-chws-temp'],
  ['CHILLER-01/enteringChilledWaterTemperatureC', 'sensor-ch01-chwr-temp'],
  ['CHILLER-01/enteringCoolingWaterTemperatureC', 'sensor-ch01-cw-entering-temp'],
  ['CHWP-01/flowRateM3h', 'sensor-chwp01-flow'],
  ['CWP-01/flowRateM3h', 'sensor-cwp01-flow'],
  ['CT-01/enteringWaterTemperatureC', 'sensor-ct01-entering-temp'],
  ['CT-01/leavingWaterTemperatureC', 'sensor-ct01-leaving-temp'],
  ['CT-01/ambientWetBulbTemperatureC', 'sensor-ct01-wet-bulb'],
]);

const stateSources = new Set(['runState', 'faultCode']);
const settingSources = new Set(['chilledWaterTemperatureSetpointC', 'compressorLoadPct', 'loadLimitPct', 'frequencyHz', 'fanSpeedPct']);
const counterSources = new Set(['energyKwh', 'accumulatedCoolingEnergyKwh']);

const camelToSnake = (value) => value
  .replace(/([a-z0-9])([A-Z])/g, '$1_$2')
  .replace(/[^A-Za-z0-9]+/g, '_')
  .replace(/^_+|_+$/g, '')
  .toLowerCase();

const telemetryPointCode = (telemetryKey, sourceKey) => {
  const segment = telemetryKey.split('.').at(-1) ?? sourceKey;
  return camelToSnake(segment);
};

const commandPointCode = (sourceKey) => camelToSnake(sourceKey);

const pointTypeFor = (sourceKey) => {
  if (stateSources.has(sourceKey)) return 'STATE';
  if (settingSources.has(sourceKey)) return 'SETTING';
  if (counterSources.has(sourceKey)) return 'COUNTER';
  return 'TELEMETRY';
};

const commandPointBase = (deviceId, equipmentSlug, sourceKey, telemetryKey, name, valueType, metadata, unit = '') => ({
  deviceId,
  sensorId: '',
  ...equipmentSubject(equipmentSlug),
  sourceKey,
  telemetryKey,
  pointCode: commandPointCode(sourceKey),
  name,
  pointType: 'COMMAND',
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
  'BOOLEAN',
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

export function assignCentralPlantPointIds(points) {
  let observedSequence = centralPlantAreas.length + centralPlantEquipment.length + centralPlantSensors.length + 1;
  let commandSequence = 1;
  return (points ?? []).map((point) => Object.freeze({
    ...point,
    pointId: point.pointType === 'COMMAND'
      ? localUUID(0x500000000000 + commandSequence++)
      : localUUID(observedSequence++),
  }));
}

export function buildCentralPlantControlPoints(observedPoints) {
  const feedbackByRef = new Map((observedPoints ?? []).map((point) => [`${point.deviceId}/${point.telemetryKey}`, point]));
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
  ].map((point) => {
    const declaredFeedbackKey = point.sourceMetadata.feedbackPointKey;
    const feedback = feedbackByRef.get(`${point.deviceId}/${declaredFeedbackKey}`);
    if (!feedback) throw new Error(`control point ${point.deviceId}/${point.pointCode} has no authoritative feedback point ${declaredFeedbackKey}`);
    return Object.freeze({
      ...point,
      sourceMetadata: Object.freeze({
        ...point.sourceMetadata,
        feedbackPointKey: feedback.pointCode,
        feedbackSourceKey: feedback.sourceKey,
      }),
    });
  });
}

const humanize = (value) => value
  .replaceAll('.', ' ')
  .replaceAll('_', ' ')
  .replace(/([a-z0-9])([A-Z])/g, '$1 $2')
  .replace(/\s+/g, ' ')
  .trim();

const subjectForDevice = (deviceId) => {
  if (deviceId === 'METER-HVAC-TOTAL' || deviceId === 'BTU-METER-01' || deviceId === 'WEATHER-STATION-01') return siteSubject();
  const endpoint = centralPlantDeviceEndpoints.find((candidate) => candidate.id === deviceId);
  return equipmentSubject(endpoint.equipmentIds[0].replace(/^equipment-/, ''));
};

export function buildCentralPlantSimulatorPoints(adapterTemplate) {
  if (!Array.isArray(adapterTemplate?.devices) || adapterTemplate.devices.length !== centralPlantDeviceEndpoints.length) {
    throw new Error('adapter template and central plant Device contract differ');
  }
  return adapterTemplate.devices.flatMap((device, deviceIndex) => {
    const endpoint = centralPlantDeviceEndpoints[deviceIndex];
    return device.points.map((point) => {
      const sensorId = sensorByDeviceSource.get(`${endpoint.id}/${point.sourceKey}`) ?? '';
      const subject = subjectForDevice(endpoint.id);
      const pointType = pointTypeFor(point.sourceKey);
      const sampleInterval = pointType === 'STATE' ? '2s' : '1s';
      const publishInterval = pointType === 'STATE' ? '5s' : '2s';
      return Object.freeze({
        deviceId: endpoint.id,
        sensorId,
        ...subject,
        sourceKey: point.sourceKey,
        telemetryKey: point.telemetryKey,
        pointCode: telemetryPointCode(point.telemetryKey, point.sourceKey),
        name: humanize(point.telemetryKey),
        pointType,
        valueType: point.valueType,
        ...(point.unit ? { unit: point.unit } : {}),
        writable: false,
        sampleInterval,
        publishInterval,
        staleAfter: pointType === 'STATE' ? '15s' : '10s',
        sourceProtocol: 'SIMULATED',
        sourceAddress: `${endpoint.id}:${point.sourceKey}`,
      });
    });
  });
}

export function buildCentralPlantSimulatorConfig(adapterTemplate, overrides = {}) {
  const observedPoints = buildCentralPlantSimulatorPoints(adapterTemplate);
  const controlPoints = buildCentralPlantControlPoints(observedPoints);
  const points = assignCentralPlantPointIds([...observedPoints, ...controlPoints]);
  const simulatorSubjectType = (value) => ({ EQUIPMENT: 'ASSET', AREA: 'SPACE' })[value] ?? value;
  return {
    schemaVersion: 3,
    gatewayId: 'EG8200-COMMERCIAL-001',
    publishInterval: overrides.publishInterval ?? '5s',
    scenario: {
      schemaVersion: 1,
      mode: 'STATIC',
      inputs: {
        ambientDryBulbC: 34,
        ambientWetBulbC: 27,
        coolingLoadKw: 864,
      },
    },
    plant: {
      initialEnergyKwh: overrides.initialEnergyKwh ?? 0,
      chiller: { id: 'CHILLER-01', ratedCoolingCapacityKw: 1200, baseCop: 5.6, initialSetpointC: 7, initialLoadLimitPct: 100, initiallyRunning: true },
      chilledWaterPump: { id: 'CHWP-01', ratedPowerKw: 45, ratedFlowM3h: 220, initialFrequencyHz: 50, initiallyRunning: true },
      coolingWaterPump: { id: 'CWP-01', ratedPowerKw: 37, ratedFlowM3h: 260, initialFrequencyHz: 50, initiallyRunning: true },
      coolingTower: { id: 'CT-01', ratedFanPowerKw: 18.5, initialFanSpeedPct: 80, initiallyRunning: true },
      powerMeterId: 'METER-HVAC-TOTAL',
      btuMeterId: 'BTU-METER-01',
      weatherStationId: 'WEATHER-STATION-01',
    },
    spaces: centralPlantAreas.map(({ id, parentId, name, type }) => ({ id, ...(parentId ? { parentId } : {}), name, type })),
    assets: centralPlantEquipment.map(({ id, areaId, name, type }) => ({ id, spaceId: areaId, name, type })),
    devices: centralPlantDeviceEndpoints.map(({ id, areaId, name, type, equipmentIds }) => ({ id, spaceId: areaId, name, type, assetIds: [...equipmentIds] })),
    sensors: centralPlantSensors.map(({ mountedAreaId, ...sensor }) => ({ ...sensor, mountedSpaceId: mountedAreaId })),
    points: points.map((point) => ({
      ...point,
      subjectType: simulatorSubjectType(point.subjectType),
    })),
  };
}
