import { randomBytes } from 'node:crypto';

export const SONGYAXUAN_GATEWAY_ID = '01a14000-4000-7000-8000-000000000100';
export const COMMAND_TOPIC = `hvac/v1/${SONGYAXUAN_GATEWAY_ID}/down/command`;
export const REPLY_TOPIC = `hvac/v1/${SONGYAXUAN_GATEWAY_ID}/up/reply`;
export const TELEMETRY_TOPIC = `hvac/v1/${SONGYAXUAN_GATEWAY_ID}/up/telemetry`;
export const ET1010_UNIT_ID = 31;
export const WINNER_CONTROLLER_ID = 'songyaxuan-et1010-uid31';
export const DEFAULT_PULSE_DURATION_MS = 500;

export interface SequenceTimings {
  pumpToCoolingPumpMs: number;
  coolingPumpToTowerMs: number;
  towerToChillerPulseMs: number;
  chillerStopCoastDownMs: number;
  auxiliaryStopStepMs: number;
}

export const DEFAULT_SEQUENCE_TIMINGS: Readonly<SequenceTimings> = {
  pumpToCoolingPumpMs: 30_000,
  coolingPumpToTowerMs: 30_000,
  towerToChillerPulseMs: 60_000,
  chillerStopCoastDownMs: 30_000,
  auxiliaryStopStepMs: 10_000,
};

export type ActuationMode = 'LEVEL' | 'PULSE' | 'UNIT_SEQUENCE' | 'EMERGENCY_DO_RELEASE';
export type EdgeReplyStatus = 'EXECUTED' | 'REJECTED' | 'EXPIRED' | 'FAILED';

export interface DownlinkCommandEnvelope {
  schemaVersion: string;
  messageId: string;
  commandId: string;
  traceId?: string;
  issuedAt: number;
  expireAt: number;
  deviceId: string;
  commandCode: string;
  params?: Record<string, number>;
  policy?: {
    requiresReadback: boolean;
    verificationWindowMs: number;
  };
  executionFence: number;
  payloadHash: string;
  pointCode?: string;
  actuationMode?: string;
  channel?: string;
  coilAddress?: number;
  pulseDurationMs?: number;
}

export interface CommandReplyEnvelope {
  schemaVersion: '2.0';
  messageId: string;
  commandId: string;
  traceId?: string;
  eventTime: number;
  edgeStatus: EdgeReplyStatus;
  executionEvidence: {
    requested: { text: string };
    effective: { text: string };
    applied: { text: string };
    winnerControllerId: string;
    cycle: number;
  };
  reasonCode: string | null;
  executionFence: number;
}

export interface UplinkPoint {
  code: string;
  value: number | string;
  quality: number;
}

export interface UplinkDeviceEntry {
  deviceId: string;
  deviceTimestamp: number;
  points: UplinkPoint[];
}

export interface UplinkTelemetryMessage {
  topic: string;
  payload: {
    schemaVersion: '2.0';
    gatewayId: string;
    messageId: string;
    eventTime: number;
    sequence: number;
    replay: boolean;
    payload: { devices: UplinkDeviceEntry[] };
  };
}

export interface CoilWritePort {
  writeCoil(unitId: number, coilAddress: number, value: 0 | 1): Promise<void>;
}

export interface SequenceDiagnosticEvent {
  level: 'warn' | 'error';
  code: string;
  unitCode?: UnitCoilPlan['unitCode'];
  commandId?: string;
  message: string;
}

export interface SongyaxuanCommandExecutorOptions {
  coilWriter: CoilWritePort;
  coilReader?: (coilAddress: number) => Promise<0 | 1>;
  publishReply: (topic: string, reply: CommandReplyEnvelope) => Promise<void> | void;
  publishTelemetry?: (message: UplinkTelemetryMessage) => Promise<void> | void;
  onDiagnostic?: (event: SequenceDiagnosticEvent) => void;
  nowMs?: () => number;
  sleepMs?: (durationMs: number, signal?: AbortSignal) => Promise<void>;
  uuidv7?: () => string;
  pulseDurationMs?: number;
  sequenceTimings?: Partial<SequenceTimings>;
  timings?: {
    pulseDurationMs?: number;
    sequenceTimings?: Partial<SequenceTimings>;
  };
}

export type ExecutorRuntimeOptions = SongyaxuanCommandExecutorOptions;

export interface UnitCoilPlan {
  unitCode: 'unit-a' | 'unit-b' | 'unit-c' | 'unit-d';
  chillerDeviceId: string;
  chwp: { deviceId: string; channel: string; coil: number };
  cwp: { deviceId: string; channel: string; coil: number };
  tower: { deviceId: string; channel: string; coil: number };
  chillerStart: { channel: string; coil: number };
  chillerStop: { channel: string; coil: number };
}

interface DirectDeviceRoute {
  mode: 'LEVEL' | 'PULSE';
  channel: string;
  coil: number;
  value: 0 | 1;
  unitCode: UnitCoilPlan['unitCode'];
}

export const CHANNEL_COILS: Record<string, number> = {
  u11: 0, u12: 1, u13: 2, u14: 3,
  u21: 4, u22: 5, u23: 6, u24: 7,
  u31: 8, u32: 9, u33: 10, u34: 11,
  u41: 12, u42: 13, u43: 14, u44: 15,
  u51: 16, u52: 17, u53: 18, u54: 19,
};

export const UNIT_PLANS: Record<UnitCoilPlan['unitCode'], UnitCoilPlan> = {
  'unit-a': {
    unitCode: 'unit-a',
    chillerDeviceId: 'ctrl-chiller-a',
    chwp: { deviceId: 'ctrl-chwp-1', channel: 'u11', coil: 0 },
    cwp: { deviceId: 'ctrl-cwp-1', channel: 'u12', coil: 1 },
    tower: { deviceId: 'ctrl-tower-1', channel: 'u13', coil: 2 },
    chillerStart: { channel: 'u14', coil: 3 },
    chillerStop: { channel: 'u51', coil: 16 },
  },
  'unit-b': {
    unitCode: 'unit-b',
    chillerDeviceId: 'ctrl-chiller-b',
    chwp: { deviceId: 'ctrl-chwp-2', channel: 'u21', coil: 4 },
    cwp: { deviceId: 'ctrl-cwp-2', channel: 'u22', coil: 5 },
    tower: { deviceId: 'ctrl-tower-2', channel: 'u23', coil: 6 },
    chillerStart: { channel: 'u24', coil: 7 },
    chillerStop: { channel: 'u52', coil: 17 },
  },
  'unit-c': {
    unitCode: 'unit-c',
    chillerDeviceId: 'ctrl-chiller-c',
    chwp: { deviceId: 'ctrl-chwp-3', channel: 'u31', coil: 8 },
    cwp: { deviceId: 'ctrl-cwp-3', channel: 'u32', coil: 9 },
    tower: { deviceId: 'ctrl-tower-3', channel: 'u33', coil: 10 },
    chillerStart: { channel: 'u34', coil: 11 },
    chillerStop: { channel: 'u53', coil: 18 },
  },
  'unit-d': {
    unitCode: 'unit-d',
    chillerDeviceId: 'ctrl-chiller-d',
    chwp: { deviceId: 'ctrl-chwp-4', channel: 'u41', coil: 12 },
    cwp: { deviceId: 'ctrl-cwp-4', channel: 'u42', coil: 13 },
    tower: { deviceId: 'ctrl-tower-4', channel: 'u43', coil: 14 },
    chillerStart: { channel: 'u44', coil: 15 },
    chillerStop: { channel: 'u54', coil: 19 },
  },
};

const CHILLER_UNIT_BY_DEVICE = new Map<string, UnitCoilPlan['unitCode']>(
  Object.values(UNIT_PLANS).map((plan) => [plan.chillerDeviceId, plan.unitCode]),
);

const DIRECT_ROUTES = new Map<string, DirectDeviceRoute>();
for (const plan of Object.values(UNIT_PLANS)) {
  for (const part of [plan.chwp, plan.cwp, plan.tower]) {
    DIRECT_ROUTES.set(`${part.deviceId}|START`, { mode: 'LEVEL', channel: part.channel, coil: part.coil, value: 1, unitCode: plan.unitCode });
    DIRECT_ROUTES.set(`${part.deviceId}|STOP`, { mode: 'LEVEL', channel: part.channel, coil: part.coil, value: 0, unitCode: plan.unitCode });
  }
  DIRECT_ROUTES.set(`${plan.chillerDeviceId}|START`, { mode: 'PULSE', channel: plan.chillerStart.channel, coil: plan.chillerStart.coil, value: 1, unitCode: plan.unitCode });
  DIRECT_ROUTES.set(`${plan.chillerDeviceId}|STOP`, { mode: 'PULSE', channel: plan.chillerStop.channel, coil: plan.chillerStop.coil, value: 1, unitCode: plan.unitCode });
}

const ALL_DO_COIL_ENTRIES: ReadonlyArray<{ channel: string; coil: number }> = Object.entries(CHANNEL_COILS)
  .map(([channel, coil]) => ({ channel, coil }))
  .sort((a, b) => a.coil - b.coil);

const ALL_AUX_CONTROLLERS = Object.values(UNIT_PLANS).flatMap((plan) => [plan.chwp, plan.cwp, plan.tower]);

export function defaultUuidv7(nowMs = Date.now()): string {
  const bytes = randomBytes(16);
  const ts = BigInt(Math.max(0, Math.floor(nowMs)));
  bytes[0] = Number((ts >> 40n) & 0xffn);
  bytes[1] = Number((ts >> 32n) & 0xffn);
  bytes[2] = Number((ts >> 24n) & 0xffn);
  bytes[3] = Number((ts >> 16n) & 0xffn);
  bytes[4] = Number((ts >> 8n) & 0xffn);
  bytes[5] = Number(ts & 0xffn);
  bytes[6] = (bytes[6] & 0x0f) | 0x70;
  bytes[8] = (bytes[8] & 0x3f) | 0x80;
  const hex = bytes.toString('hex');
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
}

function defaultSleepMs(durationMs: number, signal?: AbortSignal): Promise<void> {
  if (signal?.aborted) return Promise.resolve();
  return new Promise((resolve) => {
    const timer = setTimeout(() => {
      signal?.removeEventListener('abort', onAbort);
      resolve();
    }, Math.max(0, durationMs));
    const onAbort = () => {
      clearTimeout(timer);
      resolve();
    };
    signal?.addEventListener('abort', onAbort, { once: true });
  });
}

function waitNextTurn(signal: AbortSignal): Promise<void> {
  if (signal.aborted) return Promise.resolve();
  return new Promise((resolve) => {
    const onAbort = () => {
      clearImmediate(timer);
      resolve();
    };
    const timer = setImmediate(() => {
      signal.removeEventListener('abort', onAbort);
      resolve();
    });
    signal.addEventListener('abort', onAbort, { once: true });
  });
}

class SequenceAbortError extends Error {
  readonly reasonCode: string;

  constructor(reasonCode: string) {
    super(reasonCode);
    this.reasonCode = reasonCode;
  }
}

class PulseResetError extends Error {
  constructor(cause: unknown) {
    super(cause instanceof Error ? cause.message : String(cause));
  }
}

export class SongyaxuanCommandExecutor {
  private readonly options: SongyaxuanCommandExecutorOptions;
  private readonly nowMs: () => number;
  private readonly sleepMs: (durationMs: number, signal?: AbortSignal) => Promise<void>;
  private readonly uuidv7: () => string;
  private readonly pulseDurationMs: number;
  private readonly sequenceTimings: SequenceTimings;
  private readonly lastFenceByDevice = new Map<string, number>();
  private readonly knownCoilStates = new Map<number, 0 | 1>();
  private readonly activeSequenceAborts = new Map<UnitCoilPlan['unitCode'], AbortController>();
  private readonly activeSequenceTasks = new Map<UnitCoilPlan['unitCode'], Promise<void>>();
  private readonly allSequenceTasks = new Set<Promise<void>>();
  private cycle = 0;
  private uplinkSeq = 0;

  constructor(options: SongyaxuanCommandExecutorOptions) {
    this.options = options;
    this.nowMs = options.nowMs ?? (() => Date.now());
    this.sleepMs = options.sleepMs ?? defaultSleepMs;
    this.uuidv7 = options.uuidv7 ?? (() => defaultUuidv7(this.nowMs()));
    this.pulseDurationMs = options.pulseDurationMs ?? options.timings?.pulseDurationMs ?? DEFAULT_PULSE_DURATION_MS;
    const overrides = options.sequenceTimings ?? options.timings?.sequenceTimings;
    this.sequenceTimings = {
      pumpToCoolingPumpMs: overrides?.pumpToCoolingPumpMs ?? DEFAULT_SEQUENCE_TIMINGS.pumpToCoolingPumpMs,
      coolingPumpToTowerMs: overrides?.coolingPumpToTowerMs ?? DEFAULT_SEQUENCE_TIMINGS.coolingPumpToTowerMs,
      towerToChillerPulseMs: overrides?.towerToChillerPulseMs ?? DEFAULT_SEQUENCE_TIMINGS.towerToChillerPulseMs,
      chillerStopCoastDownMs: overrides?.chillerStopCoastDownMs ?? DEFAULT_SEQUENCE_TIMINGS.chillerStopCoastDownMs,
      auxiliaryStopStepMs: overrides?.auxiliaryStopStepMs ?? DEFAULT_SEQUENCE_TIMINGS.auxiliaryStopStepMs,
    };
  }

  abortUnitSequence(unitCode: UnitCoilPlan['unitCode'], _reasonCode = 'SEQUENCE_ABORTED_BY_OPERATOR'): boolean {
    const controller = this.activeSequenceAborts.get(unitCode);
    if (!controller || controller.signal.aborted) return false;
    controller.abort();
    return true;
  }

  async waitForActiveSequences(): Promise<void> {
    while (this.allSequenceTasks.size > 0) {
      await Promise.allSettled([...this.allSequenceTasks]);
    }
  }

  async handleCommand(command: DownlinkCommandEnvelope): Promise<CommandReplyEnvelope> {
    if (!command || command.schemaVersion !== '2.0' || !command.commandId || !command.deviceId || !command.commandCode || !Number.isInteger(command.executionFence) || command.executionFence < 1) {
      return this.emitReply(command, 'REJECTED', 'INVALID_COMMAND_ENVELOPE', 'INVALID_ENVELOPE', 'NOT_APPLIED');
    }

    const previousFence = this.lastFenceByDevice.get(command.deviceId) ?? 0;
    if (command.executionFence <= previousFence) {
      return this.emitReply(command, 'REJECTED', 'STALE_EXECUTION_FENCE', command.commandCode, 'NOT_APPLIED');
    }
    this.lastFenceByDevice.set(command.deviceId, command.executionFence);

    if (this.nowMs() >= command.expireAt) {
      return this.emitReply(command, 'EXPIRED', 'COMMAND_EXPIRED', command.commandCode, 'NOT_APPLIED');
    }

    if (this.isEmergencyDoRelease(command)) {
      return this.executeEmergencyDoRelease(command);
    }

    if (command.commandCode === 'START_UNIT_SEQUENCE' || command.commandCode === 'STOP_UNIT_SEQUENCE') {
      const unitCode = this.resolveUnitCode(command);
      if (!unitCode) {
        return this.emitReply(command, 'REJECTED', 'UNKNOWN_ACTUATION_ROUTE', command.commandCode, 'NOT_APPLIED');
      }
      this.abortUnitSequence(unitCode);
      return this.executeUnitSequence(command, UNIT_PLANS[unitCode]);
    }

    const route = DIRECT_ROUTES.get(`${command.deviceId}|${command.commandCode}`);
    if (!route || !this.matchesExplicitRoute(command, route)) {
      return this.emitReply(command, 'REJECTED', 'UNKNOWN_ACTUATION_ROUTE', command.commandCode, 'NOT_APPLIED');
    }

    if (command.commandCode === 'STOP') {
      this.abortUnitSequence(route.unitCode);
    }

    if (route.mode === 'LEVEL') {
      try {
        await this.writeCoil(route.coil, route.value);
      } catch {
        return this.emitReply(command, 'FAILED', 'MODBUS_WRITE_FAILED', `${route.channel}=${route.value}`, 'NOT_APPLIED');
      }
      await this.emitTelemetryEntries([
        { deviceId: 'et1010', deviceTimestamp: this.nowMs(), points: [{ code: route.channel, value: route.value, quality: 0 }] },
        { deviceId: command.deviceId, deviceTimestamp: this.nowMs(), points: [{ code: 'run_state', value: route.value === 1 ? 'RUNNING' : 'STOPPED', quality: 0 }] },
      ]);
      return this.emitReply(command, 'EXECUTED', null, `${route.channel}=${route.value}`, `${route.channel}=${route.value}`);
    }

    const duration = command.pulseDurationMs && command.pulseDurationMs > 0 ? command.pulseDurationMs : this.pulseDurationMs;
    try {
      await this.executePulse(route.coil, route.channel, duration);
    } catch (error) {
      const reason = error instanceof PulseResetError ? 'PULSE_RESET_FAILED' : 'MODBUS_WRITE_FAILED';
      return this.emitReply(command, 'FAILED', reason, `${route.channel}:pulse(${duration}ms)`, 'NOT_APPLIED');
    }
    return this.emitReply(command, 'EXECUTED', null, `${route.channel}:pulse(${duration}ms)`, `${route.channel}:pulse(${duration}ms)->0`);
  }

  private resolveUnitCode(command: DownlinkCommandEnvelope): UnitCoilPlan['unitCode'] | null {
    const inferred = CHILLER_UNIT_BY_DEVICE.get(command.deviceId) ?? null;
    if (!inferred) return null;
    if (command.channel && command.channel !== inferred) return null;
    if (command.actuationMode && command.actuationMode !== 'UNIT_SEQUENCE') return null;
    return inferred;
  }

  private matchesExplicitRoute(command: DownlinkCommandEnvelope, route: DirectDeviceRoute): boolean {
    if (command.actuationMode && command.actuationMode !== route.mode) return false;
    if (command.channel && command.channel !== route.channel) return false;
    if (command.coilAddress !== undefined && command.coilAddress !== route.coil) return false;
    return true;
  }

  private isEmergencyDoRelease(command: DownlinkCommandEnvelope): boolean {
    if (command.commandCode !== 'STOP') return false;
    const matched =
      command.actuationMode === 'EMERGENCY_DO_RELEASE'
      || (command.deviceId === 'et1010' && command.pointCode === 'emergency_stop_all');
    if (!matched) return false;
    if (command.actuationMode && command.actuationMode !== 'EMERGENCY_DO_RELEASE') return false;
    if (command.channel && command.channel !== 'all-do') return false;
    return true;
  }

  private async writeCoil(coilAddress: number, value: 0 | 1): Promise<void> {
    await this.options.coilWriter.writeCoil(ET1010_UNIT_ID, coilAddress, value);
    this.knownCoilStates.set(coilAddress, value);
  }

  private async executePulse(coil: number, channel: string, durationMs: number, signal?: AbortSignal): Promise<void> {
    await this.writeCoil(coil, 1);
    let resetError: unknown = null;
    try {
      await this.sleepMs(durationMs, signal);
    } finally {
      try {
        await this.writeCoil(coil, 0);
      } catch (err) {
        resetError = err;
      }
    }
    if (resetError !== null) {
      throw new PulseResetError(resetError);
    }
    await this.emitTelemetryEntries([
      { deviceId: 'et1010', deviceTimestamp: this.nowMs(), points: [{ code: channel, value: 0, quality: 0 }] },
    ]);
  }

  private async executeEmergencyDoRelease(command: DownlinkCommandEnvelope): Promise<CommandReplyEnvelope> {
    for (const plan of Object.values(UNIT_PLANS)) {
      this.abortUnitSequence(plan.unitCode, 'EMERGENCY_DO_RELEASE');
    }

    let writeFailures = 0;
    for (const { coil } of ALL_DO_COIL_ENTRIES) {
      try {
        await this.writeCoil(coil, 0);
      } catch {
        writeFailures += 1;
      }
    }

    const now = this.nowMs();
    const controllerEntries: UplinkDeviceEntry[] = ALL_AUX_CONTROLLERS
      .filter((part) => this.knownCoilStates.get(part.coil) === 0)
      .map((part) => ({
        deviceId: part.deviceId,
        deviceTimestamp: now,
        points: [{ code: 'run_state', value: 'STOPPED', quality: 0 }],
      }));
    const etPoints: UplinkPoint[] = ALL_DO_COIL_ENTRIES
      .filter(({ coil }) => this.knownCoilStates.get(coil) === 0)
      .map(({ channel }) => ({ code: channel, value: 0, quality: 0 }));
    if (writeFailures === 0) {
      etPoints.unshift({ code: 'run_state', value: 'STOPPED', quality: 0 });
    }
    if (controllerEntries.length > 0 || etPoints.length > 0) {
      const entries = etPoints.length > 0
        ? [...controllerEntries, { deviceId: 'et1010', deviceTimestamp: now, points: etPoints }]
        : controllerEntries;
      await this.emitTelemetryEntries(entries);
    }

    if (writeFailures > 0) {
      return this.emitReply(command, 'FAILED', 'MODBUS_WRITE_FAILED', 'all-do:0', 'NOT_APPLIED');
    }
    return this.emitReply(command, 'EXECUTED', null, 'all-do:0', 'all-do:0');
  }

  private async executeUnitSequence(command: DownlinkCommandEnvelope, plan: UnitCoilPlan): Promise<CommandReplyEnvelope> {
    const controller = new AbortController();
    this.activeSequenceAborts.set(plan.unitCode, controller);
    const pulseMs = command.pulseDurationMs && command.pulseDurationMs > 0 ? command.pulseDurationMs : this.pulseDurationMs;
    const isStart = command.commandCode === 'START_UNIT_SEQUENCE';
    const checkAborted = () => {
      if (controller.signal.aborted) throw new SequenceAbortError('SEQUENCE_ABORTED_BY_OPERATOR');
    };
    const writeStep = async (part: { deviceId: string; channel: string; coil: number }, value: 0 | 1) => {
      checkAborted();
      await this.writeCoil(part.coil, value);
      await this.emitTelemetryEntries([
        { deviceId: 'et1010', deviceTimestamp: this.nowMs(), points: [{ code: part.channel, value, quality: 0 }] },
        { deviceId: part.deviceId, deviceTimestamp: this.nowMs(), points: [{ code: 'run_state', value: value === 1 ? 'RUNNING' : 'STOPPED', quality: 0 }] },
      ]);
    };
    const waitStep = async (ms: number) => {
      checkAborted();
      await this.sleepMs(ms, controller.signal);
      checkAborted();
    };

    try {
      if (isStart) {
        await writeStep(plan.chwp, 1);
        checkAborted();
        this.launchSequenceTask(plan.unitCode, command.commandId, controller, async () => {
          await waitStep(this.sequenceTimings.pumpToCoolingPumpMs);
          await writeStep(plan.cwp, 1);
          await waitStep(this.sequenceTimings.coolingPumpToTowerMs);
          await writeStep(plan.tower, 1);
          await waitStep(this.sequenceTimings.towerToChillerPulseMs);
          const allClosed = await this.verifyRequiredCoilsClosed(plan, command.commandId, controller.signal);
          if (!allClosed) return;
          checkAborted();
          await this.executePulse(plan.chillerStart.coil, plan.chillerStart.channel, pulseMs, controller.signal);
        });
        return this.emitReply(command, 'EXECUTED', null, command.commandCode, `${plan.unitCode}:sequence-accepted`);
      }

      checkAborted();
      await this.executePulse(plan.chillerStop.coil, plan.chillerStop.channel, pulseMs, controller.signal);
      checkAborted();
      this.launchSequenceTask(plan.unitCode, command.commandId, controller, async () => {
        await waitStep(this.sequenceTimings.chillerStopCoastDownMs);
        await writeStep(plan.tower, 0);
        await waitStep(this.sequenceTimings.auxiliaryStopStepMs);
        await writeStep(plan.cwp, 0);
        await waitStep(this.sequenceTimings.auxiliaryStopStepMs);
        await writeStep(plan.chwp, 0);
      });
      return this.emitReply(command, 'EXECUTED', null, command.commandCode, `${plan.unitCode}:sequence-accepted`);
    } catch (error) {
      if (this.activeSequenceAborts.get(plan.unitCode) === controller) {
        this.activeSequenceAborts.delete(plan.unitCode);
      }
      if (error instanceof SequenceAbortError) {
        return this.emitReply(command, 'REJECTED', error.reasonCode, command.commandCode, 'ABORTED');
      }
      if (error instanceof PulseResetError) {
        return this.emitReply(command, 'FAILED', 'PULSE_RESET_FAILED', command.commandCode, 'NOT_APPLIED');
      }
      return this.emitReply(command, 'FAILED', 'MODBUS_WRITE_FAILED', command.commandCode, 'NOT_APPLIED');
    }
  }

  private async verifyRequiredCoilsClosed(
    plan: UnitCoilPlan,
    commandId: string,
    signal: AbortSignal,
  ): Promise<boolean> {
    const required = [plan.chwp, plan.cwp, plan.tower];
    const openParts: Array<{ deviceId: string; channel: string; coil: number }> = [];
    for (const part of required) {
      if (signal.aborted) throw new SequenceAbortError('SEQUENCE_ABORTED_BY_OPERATOR');
      let state: 0 | 1;
      if (this.options.coilReader) {
        state = await this.options.coilReader(part.coil);
        this.knownCoilStates.set(part.coil, state);
      } else {
        state = this.knownCoilStates.get(part.coil) ?? 0;
      }
      if (signal.aborted) throw new SequenceAbortError('SEQUENCE_ABORTED_BY_OPERATOR');
      if (state !== 1) {
        openParts.push(part);
      }
    }
    if (openParts.length === 0) return true;

    const channels = openParts.map((part) => `${part.channel}(coil=${part.coil})`).join(', ');
    this.reportDiagnostic({
      level: 'warn',
      code: 'PRE_PULSE_COIL_OPEN',
      unitCode: plan.unitCode,
      commandId,
      message: `${plan.unitCode}: aborted chiller start pulse because coils are open: ${channels}`,
    });
    const now = this.nowMs();
    await this.emitTelemetryEntries(
      openParts.flatMap((part) => [
        { deviceId: 'et1010', deviceTimestamp: now, points: [{ code: part.channel, value: 0, quality: 0 }] },
        { deviceId: part.deviceId, deviceTimestamp: now, points: [{ code: 'run_state', value: 'STOPPED', quality: 0 }] },
      ]),
    );
    return false;
  }

  private launchSequenceTask(
    unitCode: UnitCoilPlan['unitCode'],
    commandId: string,
    controller: AbortController,
    runTail: () => Promise<void>,
  ): void {
    let resolveDone: () => void = () => {};
    const tracked = new Promise<void>((resolve) => {
      resolveDone = resolve;
    });
    this.activeSequenceTasks.set(unitCode, tracked);
    this.allSequenceTasks.add(tracked);
    void (async (): Promise<void> => {
      try {
        await waitNextTurn(controller.signal);
        if (controller.signal.aborted) return;
        await runTail();
      } catch (error) {
        if (error instanceof SequenceAbortError) return;
        this.reportDiagnostic({
          level: 'error',
          code: error instanceof PulseResetError ? 'PULSE_RESET_FAILED' : 'SEQUENCE_STEP_FAILED',
          unitCode,
          commandId,
          message: `${unitCode}: background sequence failed: ${error instanceof Error ? error.message : String(error)}`,
        });
      } finally {
        if (this.activeSequenceAborts.get(unitCode) === controller) {
          this.activeSequenceAborts.delete(unitCode);
        }
        if (this.activeSequenceTasks.get(unitCode) === tracked) {
          this.activeSequenceTasks.delete(unitCode);
        }
        this.allSequenceTasks.delete(tracked);
        resolveDone();
      }
    })();
  }

  private reportDiagnostic(event: SequenceDiagnosticEvent): void {
    if (this.options.onDiagnostic) {
      this.options.onDiagnostic(event);
      return;
    }
    console.warn(`[songyaxuan-command-executor] ${event.code}: ${event.message}`);
  }

  private async emitTelemetryEntries(devices: UplinkDeviceEntry[]): Promise<void> {
    if (!this.options.publishTelemetry) return;
    const now = this.nowMs();
    this.uplinkSeq += 1;
    await this.options.publishTelemetry({
      topic: TELEMETRY_TOPIC,
      payload: {
        schemaVersion: '2.0',
        gatewayId: SONGYAXUAN_GATEWAY_ID,
        messageId: this.uuidv7(),
        eventTime: now,
        sequence: this.uplinkSeq,
        replay: false,
        payload: { devices },
      },
    });
  }

  private async emitReply(
    command: DownlinkCommandEnvelope,
    edgeStatus: EdgeReplyStatus,
    reasonCode: string | null,
    requestedText: string,
    appliedText: string,
  ): Promise<CommandReplyEnvelope> {
    this.cycle += 1;
    const reply: CommandReplyEnvelope = {
      schemaVersion: '2.0',
      messageId: this.uuidv7(),
      commandId: command?.commandId ?? '',
      ...(command?.traceId ? { traceId: command.traceId } : {}),
      eventTime: this.nowMs(),
      edgeStatus,
      executionEvidence: {
        requested: { text: requestedText },
        effective: { text: edgeStatus === 'EXECUTED' ? appliedText : reasonCode ?? edgeStatus },
        applied: { text: edgeStatus === 'EXECUTED' ? appliedText : 'NOT_APPLIED' },
        winnerControllerId: WINNER_CONTROLLER_ID,
        cycle: this.cycle,
      },
      reasonCode: edgeStatus === 'EXECUTED' ? null : reasonCode,
      executionFence: command?.executionFence ?? 0,
    };
    await this.options.publishReply(REPLY_TOPIC, reply);
    return reply;
  }
}
