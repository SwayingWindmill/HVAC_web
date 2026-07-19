export interface TelemetryPoint {
  ts: number;
  value: number;
}

/** key -> latest point, e.g. { supplyTemp: { ts, value } } */
export type LatestMap = Record<string, TelemetryPoint>;

/** key -> time-ordered points */
export type TimeseriesMap = Record<string, TelemetryPoint[]>;

export interface DeviceSnapshot {
  deviceId: string;
  latest: LatestMap;
}

export type Range = '1h' | '6h' | '24h' | 'day' | 'week' | 'month';

export type RealtimeStatus = 'connecting' | 'open' | 'closed' | 'degraded';

export type PushHandler = (deviceId: string, key: string, value: number, ts: number) => void;
export type StatusHandler = (status: RealtimeStatus) => void;
