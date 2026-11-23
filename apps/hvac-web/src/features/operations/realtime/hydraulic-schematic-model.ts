import {
  isRunning,
  loopDifferentialPressure,
  plantReading,
  plantUnits,
  provisionalHeaderReading,
  soleReading,
  type PlantCategory,
  type PlantDevice,
  type PlantReading,
  type PlantUnit,
  type PlantView,
} from './plant-model.ts';

export interface HydraulicValveChip {
  readonly deviceId: string;
  readonly name: string;
  readonly label: string;
  readonly open: boolean;
  readonly closed: boolean;
}

export interface HydraulicNodeView {
  readonly device: PlantUnit;
  readonly primaryReading: PlantReading | null;
  readonly secondaryReading: PlantReading | null;
  readonly branchCode: string | null;
  readonly valves: readonly HydraulicValveChip[];
  readonly hasIssue: boolean;
}

export type HydraulicAnomalyCode =
  | 'CHILLED_PUMP_RUNNING_ZERO_FLOW'
  | 'CONDENSER_PUMP_RUNNING_ZERO_FLOW'
  | 'LOW_DELTA_T_HIGH_FLOW'
  | 'BRANCH_VALVE_CLOSED_WHILE_RUNNING';

export interface HydraulicAnomaly {
  readonly code: HydraulicAnomalyCode;
  readonly severity: 'destructive' | 'warning';
  readonly title: string;
  readonly detail: string;
  readonly deviceId: string;
}

export interface ChilledLoopSummary {
  readonly supplyTemp: PlantReading | null;
  readonly returnTemp: PlantReading | null;
  readonly deltaT: PlantReading | null;
  readonly flowRate: PlantReading | null;
  readonly differentialPressure: PlantReading | null;
}

export interface CondenserLoopSummary {
  readonly enteringTowerTemp: PlantReading | null;
  readonly leavingTowerTemp: PlantReading | null;
  readonly approachTemp: PlantReading | null;
  readonly flowRate: PlantReading | null;
  readonly differentialPressure: PlantReading | null;
}

export interface AmbientSummary {
  readonly indoorTemp: PlantReading | null;
  readonly banquetTemp: PlantReading | null;
  readonly outdoorDryBulb: PlantReading | null;
  readonly outdoorWetBulb: PlantReading | null;
}

export interface HydraulicSchematicModel {
  readonly hasTopology: boolean;
  readonly hasCondenserLoop: boolean;
  readonly chilledFlowActive: boolean;
  readonly coolingFlowActive: boolean;
  readonly towers: readonly HydraulicNodeView[];
  readonly coolingPumps: readonly HydraulicNodeView[];
  readonly chillers: readonly HydraulicNodeView[];
  readonly chilledPumps: readonly HydraulicNodeView[];
  readonly zones: readonly HydraulicNodeView[];
  readonly weather: PlantUnit | null;
  readonly chilled: ChilledLoopSummary;
  readonly condenser: CondenserLoopSummary;
  readonly ambient: AmbientSummary;
  readonly anomalies: readonly HydraulicAnomaly[];
}

const PRIMARY_KEYS: Readonly<Partial<Record<PlantCategory, readonly string[]>>> = {
  CHILLER: ['chiller.compressor_load', 'chiller.power'],
  CHILLED_WATER_PUMP: ['chwp.frequency', 'vfd.frequency_feedback'],
  COOLING_WATER_PUMP: ['cwp.frequency', 'vfd.frequency_feedback'],
  COOLING_TOWER: ['cooling_tower.fan_speed', 'cooling_tower.power'],
  ZONE: ['zone.temperature'],
};

const SECONDARY_KEYS: Readonly<Partial<Record<PlantCategory, readonly string[]>>> = {
  CHILLER: ['chiller.leaving_chilled_water_temperature', 'chiller.cop'],
  CHILLED_WATER_PUMP: ['chwp.power', 'chwp.flow_rate'],
  COOLING_WATER_PUMP: ['cwp.power', 'cwp.flow_rate'],
  COOLING_TOWER: ['cooling_tower.leaving_water_temperature', 'cooling_tower.approach_temperature'],
  ZONE: ['zone.banquet_temperature'],
};

function firstPresentReading(device: PlantDevice, keys: readonly string[] | undefined): PlantReading | null {
  if (!keys) return null;
  for (const key of keys) {
    const reading = device.reading(key);
    if (reading) return reading;
  }
  return null;
}

function isValveDevice(device: PlantDevice): boolean {
  return (
    device.assetType === 'MOTORIZED_VALVE'
    || device.assetType === 'ISOLATION_VALVE'
    || device.reading('valve.open_state') !== null
    || device.reading('valve.position_feedback') !== null
  );
}

interface ParsedValveState {
  readonly chip: HydraulicValveChip;
  readonly strictlyClosed: boolean;
}

const isClosedValveDisplay = (display: string): boolean => display === '关闭' || display === '全关';

function parseValveState(device: PlantDevice): ParsedValveState {
  const openState = device.reading('valve.open_state');
  const position = device.reading('valve.position_feedback');
  const online = device.connection !== 'OFFLINE';

  let label = online ? '状态未知' : '离线';
  if (online && openState && openState.state === 'PRESENT') {
    label = openState.display;
  } else if (online && position && position.state === 'PRESENT' && position.numeric !== null) {
    label = position.numeric <= 5 ? '关闭' : position.numeric >= 95 ? '全开' : `开度 ${position.display}%`;
  }

  const stateClosed = online && openState?.state === 'PRESENT' && isClosedValveDisplay(openState.display);
  const posClosed = online && position?.state === 'PRESENT' && position.numeric !== null && position.numeric <= 5;
  const stateOpen = online && openState?.state === 'PRESENT' && openState.display === '全开';
  const posOpen = online && position?.state === 'PRESENT' && position.numeric !== null && position.numeric >= 95;

  const currentStateClosed = online && openState?.current === true && isClosedValveDisplay(openState.display);
  const currentPosClosed = online && position?.current === true && position.numeric !== null && position.numeric <= 5;

  return {
    chip: {
      deviceId: device.deviceId,
      name: device.name,
      label,
      open: stateOpen || posOpen,
      closed: stateClosed || posClosed,
    },
    strictlyClosed: currentStateClosed || currentPosClosed,
  };
}

function formatReadingWithUnit(reading: PlantReading): string {
  return reading.unit ? `${reading.display} ${reading.unit}` : reading.display;
}

function toNodeList(
  plant: PlantView,
  category: PlantCategory,
  valvesByUnit: ReadonlyMap<string, readonly HydraulicValveChip[]>,
): readonly HydraulicNodeView[] {
  return plantUnits(plant)
    .filter((unit) => unit.category === category)
    .sort((left, right) => left.name.localeCompare(right.name, 'zh-CN'))
    .map((unit) => ({
      device: unit,
      primaryReading: firstPresentReading(unit, PRIMARY_KEYS[category]),
      secondaryReading: firstPresentReading(unit, SECONDARY_KEYS[category]),
      branchCode: unit.branchCode,
      valves: valvesByUnit.get(unit.unitId) ?? [],
      hasIssue: unit.connection === 'OFFLINE' || unit.offlineMemberCount > 0 || unit.runState === 'FAULT' || unit.hasStaleData,
    }));
}

export function buildHydraulicSchematic(plant: PlantView): HydraulicSchematicModel {
  const valvesByDevice = new Map<string, HydraulicValveChip[]>();
  const anomalies: HydraulicAnomaly[] = [];

  for (const branch of plant.branches) {
    const parsedValves = branch.devices.filter(isValveDevice).map((device) => ({ device, parsed: parseValveState(device) }));
    if (parsedValves.length === 0) continue;
    const chips = parsedValves.map((entry) => entry.parsed.chip);
    for (const member of branch.devices) {
      if (isValveDevice(member)) continue;
      valvesByDevice.set(member.unitId, chips);
    }
    if (branch.runningCount > 0) {
      const closedEntry = parsedValves.find((entry) => entry.parsed.strictlyClosed);
      if (closedEntry) {
        anomalies.push({
          code: 'BRANCH_VALVE_CLOSED_WHILE_RUNNING',
          severity: 'destructive',
          title: `支路 ${branch.branchCode} 运行但隔离阀关闭`,
          detail: `${closedEntry.device.name} 当前关闭，支路内已有 ${branch.runningCount} 台设备运行`,
          deviceId: closedEntry.device.deviceId,
        });
      }
    }
  }

  const towers = toNodeList(plant, 'COOLING_TOWER', valvesByDevice);
  const coolingPumps = toNodeList(plant, 'COOLING_WATER_PUMP', valvesByDevice);
  const chillers = toNodeList(plant, 'CHILLER', valvesByDevice);
  const chilledPumps = toNodeList(plant, 'CHILLED_WATER_PUMP', valvesByDevice);
  const zones = toNodeList(plant, 'ZONE', valvesByDevice);
  const weather = plantUnits(plant)
    .filter((unit) => unit.category === 'WEATHER')
    .sort((left, right) => left.name.localeCompare(right.name, 'zh-CN'))[0] ?? null;

  const chilled: ChilledLoopSummary = {
    supplyTemp: plantReading(plant, 'btu_meter.supply_water_temperature')
      ?? soleReading(plant, 'CHILLER', 'chiller.leaving_chilled_water_temperature'),
    returnTemp: plantReading(plant, 'btu_meter.return_water_temperature')
      ?? soleReading(plant, 'CHILLER', 'chiller.entering_chilled_water_temperature'),
    deltaT: plantReading(plant, 'btu_meter.temperature_difference'),
    flowRate: plantReading(plant, 'btu_meter.flow_rate')
      ?? soleReading(plant, 'CHILLED_WATER_PUMP', 'chwp.flow_rate')
      ?? provisionalHeaderReading(plant, 'chwp.flow_rate'),
    differentialPressure: loopDifferentialPressure(plant, 'CHILLED_WATER'),
  };

  const condenser: CondenserLoopSummary = {
    enteringTowerTemp: plantReading(plant, 'cooling_tower.entering_water_temperature')
      ?? soleReading(plant, 'CHILLER', 'chiller.leaving_cooling_water_temperature'),
    leavingTowerTemp: plantReading(plant, 'cooling_tower.leaving_water_temperature')
      ?? soleReading(plant, 'CHILLER', 'chiller.entering_cooling_water_temperature'),
    approachTemp: plantReading(plant, 'cooling_tower.approach_temperature'),
    flowRate: plantReading(plant, 'cwp.flow_rate')
      ?? soleReading(plant, 'COOLING_WATER_PUMP', 'cwp.flow_rate'),
    differentialPressure: loopDifferentialPressure(plant, 'CONDENSER_WATER'),
  };

  const ambient: AmbientSummary = {
    indoorTemp: plantReading(plant, 'zone.temperature'),
    banquetTemp: plantReading(plant, 'zone.banquet_temperature'),
    outdoorDryBulb: plantReading(plant, 'weather.ambient_dry_bulb_temperature'),
    outdoorWetBulb: plantReading(plant, 'weather.ambient_wet_bulb_temperature'),
  };

  const runningChilledPump = chilledPumps.find((node) => isRunning(node.device));
  if (
    runningChilledPump
    && chilled.flowRate?.current === true
    && chilled.flowRate.numeric !== null
    && chilled.flowRate.numeric <= 0.5
  ) {
    anomalies.push({
      code: 'CHILLED_PUMP_RUNNING_ZERO_FLOW',
      severity: 'destructive',
      title: '冷冻水泵运行但流量接近零',
      detail: `${runningChilledPump.device.name} 正在运行，当前冷冻水流量 ${formatReadingWithUnit(chilled.flowRate)}`,
      deviceId: runningChilledPump.device.deviceId,
    });
  }

  const runningCoolingPump = coolingPumps.find((node) => isRunning(node.device));
  if (
    runningCoolingPump
    && condenser.flowRate?.current === true
    && condenser.flowRate.numeric !== null
    && condenser.flowRate.numeric <= 0.5
  ) {
    anomalies.push({
      code: 'CONDENSER_PUMP_RUNNING_ZERO_FLOW',
      severity: 'destructive',
      title: '冷却水泵运行但流量接近零',
      detail: `${runningCoolingPump.device.name} 正在运行，当前冷却水流量 ${formatReadingWithUnit(condenser.flowRate)}`,
      deviceId: runningCoolingPump.device.deviceId,
    });
  }

  const runningChiller = chillers.find((node) => isRunning(node.device));
  if (
    runningChiller
    && (chilled.deltaT?.current === true || chilled.deltaT?.lowConfidence === true)
    && chilled.deltaT.numeric !== null
    && chilled.deltaT.numeric > 0
    && chilled.deltaT.numeric < 2.5
    && chilled.flowRate?.current === true
    && chilled.flowRate.numeric !== null
    && chilled.flowRate.numeric > 10
  ) {
    anomalies.push({
      code: 'LOW_DELTA_T_HIGH_FLOW',
      severity: 'warning',
      title: '大流量小温差',
      detail: `冷冻水流量 ${formatReadingWithUnit(chilled.flowRate)}，供回水温差 ${formatReadingWithUnit(chilled.deltaT)}`,
      deviceId: runningChiller.device.deviceId,
    });
  }

  const hasTopology = towers.length > 0 || coolingPumps.length > 0 || chillers.length > 0 || chilledPumps.length > 0;
  const hasCondenserLoop = towers.length > 0
    || coolingPumps.length > 0
    || condenser.enteringTowerTemp !== null
    || condenser.leavingTowerTemp !== null
    || condenser.flowRate !== null;

  const chilledFlowActive = chilledPumps.some((node) => isRunning(node.device))
    || (chilled.flowRate?.current === true && (chilled.flowRate.numeric ?? 0) > 1);
  const coolingFlowActive = coolingPumps.some((node) => isRunning(node.device))
    || chillers.some((node) => isRunning(node.device))
    || (condenser.flowRate?.current === true && (condenser.flowRate.numeric ?? 0) > 1);

  return {
    hasTopology,
    hasCondenserLoop,
    chilledFlowActive,
    coolingFlowActive,
    towers,
    coolingPumps,
    chillers,
    chilledPumps,
    zones,
    weather,
    chilled,
    condenser,
    ambient,
    anomalies,
  };
}
