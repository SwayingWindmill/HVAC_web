export type EquipmentOperationalStatus = 'RUNNING' | 'STANDBY' | 'FAULT' | 'MAINTENANCE' | 'OFFLINE';

export interface SystemOperatingSummary {
  readonly systemMode: string; // e.g. 'AI全局寻优闭环'
  readonly optimizationStatus: 'ACTIVE' | 'STANDBY' | 'OVERRIDDEN';
  readonly totalCoolingCapacityKW: number;
  readonly coolingLoadPercent: number; // e.g. 74.2%
  readonly instantCop: number; // e.g. 5.28
  readonly instantPowerKW: number; // e.g. 648
  readonly chwSupplyTemp: number; // e.g. 7.2 °C
  readonly chwReturnTemp: number; // e.g. 12.4 °C
  readonly chwSetpointTemp: number; // e.g. 7.0 °C
  readonly cwSupplyTemp: number; // e.g. 26.8 °C
  readonly cwReturnTemp: number; // e.g. 31.6 °C
  readonly cwApproachTemp: number; // e.g. 2.8 °C
  readonly systemDeltaPMPa: number; // e.g. 0.22 MPa
  readonly systemDeltaPSetpoint: number; // e.g. 0.21 MPa
  readonly activeFaultCount: number;
}

export interface ChillerUnitTelemetry {
  readonly id: string;
  readonly name: string;
  readonly type: 'CENTRIFUGAL' | 'MAGLEV' | 'SCREW';
  readonly status: EquipmentOperationalStatus;
  readonly loadPercent: number; // %
  readonly powerKW: number;
  readonly currentA: number;
  readonly cop: number;
  readonly chwSupplyTemp: number;
  readonly chwReturnTemp: number;
  readonly cwSupplyTemp: number;
  readonly cwReturnTemp: number;
  readonly evapPressureKPa: number;
  readonly condPressureKPa: number;
  readonly oilPressureKPa: number;
  readonly oilTempC: number;
  readonly runningHours: number;
  readonly faults?: readonly string[];
}

export interface PumpUnitTelemetry {
  readonly id: string;
  readonly name: string;
  readonly loop: 'CHW' | 'CW';
  readonly status: EquipmentOperationalStatus;
  readonly frequencyHz: number;
  readonly powerKW: number;
  readonly currentA: number;
  readonly flowM3H: number;
  readonly headM: number;
  readonly runningHours: number;
  readonly faults?: readonly string[];
}

export interface TowerUnitTelemetry {
  readonly id: string;
  readonly name: string;
  readonly status: EquipmentOperationalStatus;
  readonly fanSpeedPercent: number;
  readonly frequencyHz: number;
  readonly powerKW: number;
  readonly currentA: number;
  readonly inletTemp: number;
  readonly outletTemp: number;
  readonly runningHours: number;
  readonly faults?: readonly string[];
}

export interface ControlLoopItem {
  readonly id: string;
  readonly name: string;
  readonly loopTag: string;
  readonly variableName: string;
  readonly setpoint: number;
  readonly actualValue: number;
  readonly deviation: number;
  readonly unit: string;
  readonly outputPercent: number;
  readonly mode: 'AUTO' | 'MANUAL' | 'CASCADE';
  readonly status: 'TRACKING' | 'STABLE' | 'DEVIATING';
}

export interface RealtimeAnalyticsData {
  readonly summary: SystemOperatingSummary;
  readonly chillers: readonly ChillerUnitTelemetry[];
  readonly chwPumps: readonly PumpUnitTelemetry[];
  readonly cwPumps: readonly PumpUnitTelemetry[];
  readonly towers: readonly TowerUnitTelemetry[];
  readonly controlLoops: readonly ControlLoopItem[];
  readonly bypassValveOpeningPercent: number;
  readonly updatedAt: string;
}

export interface RealtimeFilterParams {
  readonly subsystem?: 'ALL' | 'CHILLERS' | 'PUMPS' | 'TOWERS';
  readonly search?: string;
}
