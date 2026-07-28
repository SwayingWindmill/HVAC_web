import type {
  DeviceObservationSnapshot,
  TelemetryKeyState,
} from '@/api/generated/s2Telemetry.gen';
import type {
  TelemetryLiveClient,
  TelemetryLiveSession,
  TelemetryLiveState,
  TelemetryLiveTarget,
} from '@/platform/telemetry-live';

export type BrowserLiveMode = 'live-update' | 'reconnect' | 'gap' | 'outage' | 'revoke';

export interface BrowserLiveAudit {
  opens: Array<{ deviceId: string; clientSubscriptionId: string; keys: string[] }>;
  purgeCount: number;
  closeCount: number;
  modes: BrowserLiveMode[];
}

const evaluatedAt = '2026-07-25T05:30:00.000Z';
const sampledAt = '2026-07-25T05:28:00.000Z';

function genericValueStates(updated: boolean): TelemetryKeyState[] {
  if (updated) {
    return [
      {
        key: 'temperature', state: 'PRESENT', value: 23.75, valueType: 'NUMBER', unit: '°C',
        sampledAt: '2026-07-25T05:31:00.000Z', receivedAt: '2026-07-25T05:31:01.000Z',
        freshness: 'FRESH', quality: 'GOOD', qualityReasons: [], policyRevision: 12,
      },
      {
        key: 'humidity', state: 'PRESENT', value: 48, valueType: 'NUMBER', unit: '%RH',
        sampledAt: '2026-07-25T05:31:00.000Z', receivedAt: '2026-07-25T05:31:01.000Z',
        freshness: 'FRESH', quality: 'GOOD', qualityReasons: [], policyRevision: 12,
      },
      {
        key: 'setpoint', state: 'PRESENT', value: 24, valueType: 'NUMBER', unit: '°C',
        sampledAt: '2026-07-25T05:31:00.000Z', receivedAt: '2026-07-25T05:31:01.000Z',
        freshness: 'FRESH', quality: 'GOOD', qualityReasons: [], policyRevision: 12,
      },
      {
        key: 'power', state: 'PRESENT', value: 7.2, valueType: 'NUMBER', unit: 'kW',
        sampledAt: '2026-07-25T05:31:00.000Z', receivedAt: '2026-07-25T05:31:01.000Z',
        freshness: 'FRESH', quality: 'GOOD', qualityReasons: [], policyRevision: 12,
      },
    ];
  }
  return [
    {
      key: 'temperature', state: 'PRESENT', value: 22.5, valueType: 'NUMBER', unit: '°C',
      sampledAt, receivedAt: '2026-07-25T05:28:01.000Z', freshness: 'STALE', quality: 'GOOD', qualityReasons: [], policyRevision: 11,
    },
    {
      key: 'humidity', state: 'PRESENT', value: 46, valueType: 'NUMBER', unit: '%RH',
      sampledAt, receivedAt: '2026-07-25T05:28:01.000Z', freshness: 'FRESH', quality: 'SUSPECT',
      qualityReasons: ['SOURCE_LAG_EXCEEDED'], policyRevision: 11,
    },
    {
      key: 'setpoint', state: 'MISSING', freshness: 'MISSING', missingReason: 'NEVER_OBSERVED', policyRevision: 11,
    },
    {
      key: 'power', state: 'PRESENT', value: 6.8, valueType: 'NUMBER', unit: 'kW',
      sampledAt, receivedAt: '2026-07-25T05:28:01.000Z', freshness: 'FRESH', quality: 'GOOD', qualityReasons: [], policyRevision: 11,
    },
  ];
}

function chillerValueStates(keys: readonly string[], updated: boolean): TelemetryKeyState[] {
  const observationTime = updated ? '2026-07-25T05:31:00.000Z' : sampledAt;
  const receivedTime = updated ? '2026-07-25T05:31:01.000Z' : '2026-07-25T05:28:01.000Z';
  const policyRevision = updated ? 12 : 11;
  const present = (
    key: string,
    value: string | number,
    valueType: 'STRING' | 'NUMBER',
    unit: string | null,
    freshness: 'FRESH' | 'STALE' = 'FRESH',
    quality: 'GOOD' | 'SUSPECT' = 'GOOD',
  ): TelemetryKeyState => ({
    key,
    state: 'PRESENT',
    value,
    valueType,
    unit,
    sampledAt: observationTime,
    receivedAt: receivedTime,
    freshness,
    quality,
    qualityReasons: quality === 'SUSPECT' ? ['SOURCE_LAG_EXCEEDED'] : [],
    policyRevision,
  });
  const states = new Map<string, TelemetryKeyState>([
    ['chiller.run_state', present('chiller.run_state', 'RUNNING', 'STRING', null)],
    ['chiller.power', present('chiller.power', updated ? 220 : 212.5, 'NUMBER', 'kW')],
    ['chiller.cop', present('chiller.cop', updated ? 5.15 : 5.08, 'NUMBER', null)],
    ['chiller.cooling_capacity', present('chiller.cooling_capacity', updated ? 1133 : 1080, 'NUMBER', 'kW', updated ? 'FRESH' : 'STALE', updated ? 'GOOD' : 'SUSPECT')],
    ['chiller.compressor_load', present('chiller.compressor_load', updated ? 82 : 78, 'NUMBER', '%')],
    ['chiller.leaving_chilled_water_temperature', present('chiller.leaving_chilled_water_temperature', updated ? 6.5 : 6.7, 'NUMBER', 'Cel')],
    ['chiller.entering_chilled_water_temperature', present('chiller.entering_chilled_water_temperature', updated ? 12.3 : 12.1, 'NUMBER', 'Cel')],
    ['chiller.chilled_water_temperature_setpoint', present('chiller.chilled_water_temperature_setpoint', 6.5, 'NUMBER', 'Cel')],
    ['chiller.entering_cooling_water_temperature', present('chiller.entering_cooling_water_temperature', updated ? 29.2 : 29.4, 'NUMBER', 'Cel')],
    ['chiller.business_revision', present('chiller.business_revision', updated ? 8 : 7, 'NUMBER', null)],
    ['chiller.fault_code', present('chiller.fault_code', 'NONE', 'STRING', null)],
  ]);
  return keys.map((key) => states.get(key) ?? {
    key,
    state: 'MISSING',
    freshness: 'MISSING',
    missingReason: 'NEVER_OBSERVED',
    policyRevision,
  });
}

function valueStates(keys: readonly string[], updated: boolean): TelemetryKeyState[] {
  return keys.includes('chiller.cop') ? chillerValueStates(keys, updated) : genericValueStates(updated);
}

function snapshot(deviceId: string, keys: readonly string[], updated: boolean): DeviceObservationSnapshot {
  return {
    schemaVersion: 1,
    deviceId,
    owningOrganizationId: '018f6a00-1000-7000-8000-000000000001',
    siteId: '018f6a00-2000-7000-8000-000000000001',
    businessRevision: updated ? 42 : 41,
    evaluatedAt: updated ? '2026-07-25T05:31:02.000Z' : evaluatedAt,
    evaluationAvailability: 'AVAILABLE',
    availabilityReasons: [],
    presence: {
      applicability: 'APPLICABLE',
      currentState: 'ONLINE',
      lastSeenAt: updated ? '2026-07-25T05:31:00.000Z' : sampledAt,
      policyRevision: updated ? 12 : 11,
      lastKnown: {
        state: 'ONLINE',
        lastSeenAt: updated ? '2026-07-25T05:31:00.000Z' : sampledAt,
        evaluatedAt: updated ? '2026-07-25T05:31:02.000Z' : evaluatedAt,
        policyRevision: updated ? 12 : 11,
      },
    },
    telemetryReadiness: updated ? 'CURRENT' : 'DEGRADED',
    displayState: updated ? 'ONLINE' : 'STALE',
    values: valueStates(keys, updated),
  };
}

class ControlledSession implements TelemetryLiveSession {
  private state: TelemetryLiveState;
  private readonly listeners = new Set<(states: ReadonlyArray<TelemetryLiveState>) => void>();
  private closed = false;

  constructor(
    private readonly target: TelemetryLiveTarget,
    private readonly audit: BrowserLiveAudit,
  ) {
    this.state = {
      status: 'live',
      clientSubscriptionId: target.clientSubscriptionId,
      deviceId: target.deviceId,
      keys: [...target.keys],
      updatedAt: evaluatedAt,
      snapshot: snapshot(target.deviceId, target.keys, false),
      recovered: false,
    };
  }

  getState(clientSubscriptionId: string): TelemetryLiveState | undefined {
    return clientSubscriptionId === this.target.clientSubscriptionId ? this.state : undefined;
  }

  getStates(): ReadonlyArray<TelemetryLiveState> {
    return [this.state];
  }

  subscribe(listener: (states: ReadonlyArray<TelemetryLiveState>) => void): () => void {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }

  async refresh(): Promise<void> {
    this.transition('live-update');
  }

  async checkpoint(): Promise<void> {}

  close(): void {
    if (this.closed) return;
    this.closed = true;
    this.audit.closeCount += 1;
    this.listeners.clear();
  }

  transition(mode: BrowserLiveMode): void {
    if (this.closed) return;
    const currentSnapshot = this.state.snapshot ?? snapshot(this.target.deviceId, this.target.keys, false);
    if (mode === 'live-update') {
      this.state = {
        status: 'live', clientSubscriptionId: this.target.clientSubscriptionId, deviceId: this.target.deviceId,
        keys: [...this.target.keys], updatedAt: '2026-07-25T05:31:02.000Z', snapshot: snapshot(this.target.deviceId, this.target.keys, true), recovered: true,
      };
    } else if (mode === 'reconnect') {
      this.state = {
        status: 'snapshot', clientSubscriptionId: this.target.clientSubscriptionId, deviceId: this.target.deviceId,
        keys: [...this.target.keys], updatedAt: '2026-07-25T05:31:03.000Z', snapshot: currentSnapshot, reason: 'reconnecting',
      };
    } else if (mode === 'gap') {
      this.state = {
        status: 'unavailable', clientSubscriptionId: this.target.clientSubscriptionId, deviceId: this.target.deviceId,
        keys: [...this.target.keys], updatedAt: '2026-07-25T05:31:04.000Z', snapshot: currentSnapshot,
        reason: 'recovery-required', retryable: true,
      };
    } else if (mode === 'outage') {
      this.state = {
        status: 'unavailable', clientSubscriptionId: this.target.clientSubscriptionId, deviceId: this.target.deviceId,
        keys: [...this.target.keys], updatedAt: '2026-07-25T05:31:05.000Z', snapshot: currentSnapshot,
        reason: 'transport-unavailable', retryable: true,
      };
    } else {
      this.revoke();
      return;
    }
    this.emit();
  }

  revoke(): void {
    if (this.state.status === 'revoked') return;
    this.state = {
      status: 'revoked', clientSubscriptionId: this.target.clientSubscriptionId, deviceId: this.target.deviceId,
      keys: [...this.target.keys], updatedAt: '2026-07-25T05:31:06.000Z', snapshot: null,
    };
    this.emit();
  }

  private emit(): void {
    const states = this.getStates();
    for (const listener of this.listeners) listener(states);
  }
}

export class ControlledTelemetryLiveClient implements TelemetryLiveClient {
  readonly audit: BrowserLiveAudit = { opens: [], purgeCount: 0, closeCount: 0, modes: [] };
  private readonly sessions = new Set<ControlledSession>();

  async open(targets: ReadonlyArray<TelemetryLiveTarget>): Promise<TelemetryLiveSession> {
    if (targets.length !== 1) throw new Error('Browser fixture accepts exactly one live target');
    const target = targets[0];
    this.audit.opens.push({
      deviceId: target.deviceId,
      clientSubscriptionId: target.clientSubscriptionId,
      keys: [...target.keys],
    });
    const session = new ControlledSession(target, this.audit);
    this.sessions.add(session);
    return session;
  }

  setMode(mode: BrowserLiveMode): void {
    this.audit.modes.push(mode);
    for (const session of this.sessions) session.transition(mode);
  }

  purge(): void {
    this.audit.purgeCount += 1;
    for (const session of this.sessions) session.revoke();
  }
}
