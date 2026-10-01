export type DeviceCategory =
  | "chiller"
  | "pump"
  | "tower"
  | "ahu"
  | "boiler"
  | "transformer"
  | "heat-exchanger"
  | "dosing";

export type DeviceStatus = "running" | "standby" | "alarm" | "offline";

export type ContextLensType = "space" | "system" | "equipment" | "meter";

export interface DeviceSpecs {
  readonly manufacturer: string;
  readonly model: string;
  readonly ratedPowerKw: number;
  readonly ratedCoolingKw?: number;
  readonly refrigerant?: string;
  readonly installDate: string;
  readonly nextMaintenanceDate: string;
  readonly responsiblePerson: string;
}

export interface DeviceSetpoints {
  readonly chilledWaterSupplyTemp?: number;
  readonly mode: "auto" | "manual";
  readonly frequencyLimitHz?: number;
}

export interface DeviceAlarmSummary {
  readonly id: string;
  readonly severity: "critical" | "warning" | "info";
  readonly message: string;
  readonly triggeredAt: string;
}

export interface DeviceItem {
  readonly id: string;
  readonly code: string;
  readonly name: string;
  readonly category: DeviceCategory;
  readonly categoryLabel: string;
  readonly systemId: string;
  readonly systemName: string;
  readonly spaceId: string;
  readonly spaceName: string;
  readonly meterId: string;
  readonly meterName: string;
  readonly status: DeviceStatus;
  readonly statusLabel: string;
  readonly powerKw: number;
  readonly cop?: number;
  readonly loadPercent: number;
  readonly supplyTemp?: number;
  readonly returnTemp?: number;
  readonly flowRate?: number; // m³/h
  readonly activeAlarmsCount: number;
  readonly activeAlarms?: readonly DeviceAlarmSummary[];
  readonly opportunitiesCount: number;
  readonly dataHealthPercent: number;
  readonly specs: DeviceSpecs;
  readonly setpoints: DeviceSetpoints;
}

export interface LensNode {
  readonly id: string;
  readonly name: string;
  readonly type: ContextLensType;
  readonly count: number;
  readonly children?: readonly LensNode[];
}

export interface SystemsDevicesQuery {
  readonly scopeId?: string;
  readonly search?: string;
  readonly category?: string;
  readonly status?: string;
  readonly lens?: ContextLensType;
  readonly lensNodeId?: string;
}
