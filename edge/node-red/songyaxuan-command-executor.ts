import { randomBytes } from 'node:crypto';
import { BUS_PRIORITY_NORMAL, BUS_PRIORITY_PULSE_RESET, Rs485BusLock } from './rs485-bus-lock.ts';

export const SONGYAXUAN_GATEWAY_ID = '01a14000-4000-7000-8000-000000000100';
export const COMMAND_TOPIC = `hvac/v1/${SONGYAXUAN_GATEWAY_ID}/down/command`;
export const REPLY_TOPIC = `hvac/v1/${SONGYAXUAN_GATEWAY_ID}/up/reply`;
export const TELEMETRY_TOPIC = `hvac/v1/${SONGYAXUAN_GATEWAY_ID}/up/telemetry`;
export const ET1010_UNIT_ID = 31;
export const WINNER_CONTROLLER_ID = 'songyaxuan-et1010-uid31';
export const DEFAULT_PULSE_DURATION_MS = 500;
export const DEFAULT_CHILLER_STOP_POWER_KW = 5;

const PULSE_RESET_ATTEMPTS = 3;
const PULSE_RESET_BACKOFF_MS = 50;
const CHILLER_STOP_RECHECK_LIMIT = 2;
const MAX_EXECUTED_COMMAND_IDS = 1000;

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

export type ActuationMode = 'LEVEL' | 'PULSE' | 'UNIT_SEQUENCE' | 'ABORT_UNIT_SEQUENCE' | 'EMERGENCY_DO_RELEASE';
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
    timestamp: number;
    sequence: number;
    replay: boolean;
    payload: { devices: UplinkDeviceEntry[] };
  };
}

export interface ExecutorPersistedState {
  lastSequence: number;
  lastFenceByDevice: Record<string, number>;
  executedCommandIds: string[];
  pendingPulseCoils: number[];
}

export interface ExecutorStateStore {
  load(): Promise<ExecutorPersistedState | null>;
  save(state: ExecutorPersistedState): Promise<void>;
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
  chillerPowerReader?: (unitCode: UnitCoilPlan['unitCode']) => Promise<number | null>;
  chillerStopPowerKw?: number;
  publishReply: (topic: string, reply: CommandReplyEnvelope) => Promise<void> | void;
  publishTelemetry?: (message: UplinkTelemetryMessage) => Promise<void> | void;
  onDiagnostic?: (event: SequenceDiagnosticEvent) => void;
  stateStore?: ExecutorStateStore;
  busLock?: Rs485BusLock;
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

interface PulseCoilDescriptor {
  coil: number;
  channel: string;
  unitCode: UnitCoilPlan['unitCode'];
}

const PULSE_COIL_DESCRIPTORS: ReadonlyArray<PulseCoilDescriptor> = Object.values(UNIT_PLANS)
  .flatMap((plan) => [
    { coil: plan.chillerStart.coil, channel: plan.chillerStart.channel, unitCode: plan.unitCode },
    { coil: plan.chillerStop.coil, channel: plan.chillerStop.channel, unitCode: plan.unitCode },
  ])
  .sort((a, b) => a.coil - b.coil);

export const PULSE_COILS: ReadonlyArray<number> = PULSE_COIL_DESCRIPTORS.map((entry) => entry.coil);
const PULSE_COIL_SET = new Set<number>(PULSE_COILS);

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
  const waitMs = Number.isFinite(durationMs) && durationMs > 0 ? durationMs : 0;
  if (waitMs === 0) return Promise.resolve();
  return new Promise((resolve) => {
    let timer: ReturnType<typeof setTimeout> | undefined;
    const onAbort = () => {
      if (timer !== undefined) {
        clearTimeout(timer);
        timer = undefined;
      }
      resolve();
    };
    timer = setTimeout(() => {
      timer = undefined;
      signal?.removeEventListener('abort', onAbort);
      resolve();
    }, waitMs);
    signal?.addEventListener('abort', onAbort, { once: true });
  });
}

function waitNextTurn(signal: AbortSignal): Promise<void> {
  if (signal.aborted) return Promise.resolve();
  return new Promise((resolve) => {
    let timer: ReturnType<typeof setImmediate> | undefined;
    const onAbort = () => {
      if (timer !== undefined) {
        clearImmediate(timer);
        timer = undefined;
      }
      resolve();
    };
    timer = setImmediate(() => {
      timer = undefined;
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

class StatePersistError extends Error {
  constructor(cause: unknown) {
    super(errorText(cause));
  }
}

export class PulseResetError extends Error {
  constructor(cause: unknown) {
    super(errorText(cause));
  }
}

function errorText(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

function abortReasonText(signal?: AbortSignal): string {
  return typeof signal?.reason === 'string' && signal.reason.length > 0
    ? signal.reason
    : 'SEQUENCE_ABORTED_BY_OPERATOR';
}

export class SongyaxuanCommandExecutor {
  private readonly options: SongyaxuanCommandExecutorOptions;
  private readonly nowMs: () => number;
  private readonly sleepMs: (durationMs: number, signal?: AbortSignal) => Promise<void>;
  private readonly uuidv7: () => string;
  private readonly pulseDurationMs: number;
  private readonly chillerStopPowerKw: number;
  private readonly sequenceTimings: SequenceTimings;
  private readonly lastFenceByDevice = new Map<string, number>();
  private readonly executedCommandIds = new Set<string>();
  private readonly pendingPulseCoils = new Set<number>();
  private readonly knownCoilStates = new Map<number, 0 | 1>();
  private readonly activeSequenceAborts = new Map<UnitCoilPlan['unitCode'], AbortController>();
  private readonly activeSequenceTasks = new Map<UnitCoilPlan['unitCode'], Promise<void>>();
  private readonly allSequenceTasks = new Set<Promise<void>>();
  private readonly allDirectTasks = new Set<Promise<void>>();
  private initialized = false;
  private initPromise: Promise<void> | null = null;
  private admissionChain: Promise<void> = Promise.resolve();
  private persistChain: Promise<void> = Promise.resolve();
  private cycle = 0;
  private uplinkSeq = 0;

  constructor(options: SongyaxuanCommandExecutorOptions) {
    this.options = options;
    this.nowMs = options.nowMs ?? (() => Date.now());
    this.sleepMs = options.sleepMs ?? defaultSleepMs;
    this.uuidv7 = options.uuidv7 ?? (() => defaultUuidv7(this.nowMs()));
    this.pulseDurationMs = options.pulseDurationMs ?? options.timings?.pulseDurationMs ?? DEFAULT_PULSE_DURATION_MS;
    this.chillerStopPowerKw = typeof options.chillerStopPowerKw === 'number' && Number.isFinite(options.chillerStopPowerKw) && options.chillerStopPowerKw >= 0
      ? options.chillerStopPowerKw
      : DEFAULT_CHILLER_STOP_POWER_KW;
    const overrides = options.sequenceTimings ?? options.timings?.sequenceTimings;
    this.sequenceTimings = {
      pumpToCoolingPumpMs: overrides?.pumpToCoolingPumpMs ?? DEFAULT_SEQUENCE_TIMINGS.pumpToCoolingPumpMs,
      coolingPumpToTowerMs: overrides?.coolingPumpToTowerMs ?? DEFAULT_SEQUENCE_TIMINGS.coolingPumpToTowerMs,
      towerToChillerPulseMs: overrides?.towerToChillerPulseMs ?? DEFAULT_SEQUENCE_TIMINGS.towerToChillerPulseMs,
      chillerStopCoastDownMs: overrides?.chillerStopCoastDownMs ?? DEFAULT_SEQUENCE_TIMINGS.chillerStopCoastDownMs,
      auxiliaryStopStepMs: overrides?.auxiliaryStopStepMs ?? DEFAULT_SEQUENCE_TIMINGS.auxiliaryStopStepMs,
    };
  }

  async initialize(): Promise<void> {
    if (this.initialized) return;
    if (!this.initPromise) {
      this.initPromise = this.runInitialize().then(
        () => {
          this.initialized = true;
        },
        (err) => {
          this.initPromise = null;
          throw err;
        },
      );
    }
    return this.initPromise;
  }

  abortUnitSequence(unitCode: UnitCoilPlan['unitCode'], reasonCode = 'SEQUENCE_ABORTED_BY_OPERATOR'): boolean {
    const controller = this.activeSequenceAborts.get(unitCode);
    if (!controller || controller.signal.aborted) return false;
    controller.abort(reasonCode);
    return true;
  }

  async waitForActiveSequences(): Promise<void> {
    while (this.allSequenceTasks.size > 0 || this.allDirectTasks.size > 0) {
      await Promise.allSettled([...this.allSequenceTasks, ...this.allDirectTasks]);
    }
  }

  private withSerializedAdmission<T>(fn: () => Promise<T>): Promise<T> {
    const run = this.admissionChain.then(fn, fn);
    this.admissionChain = run.then(() => undefined, () => undefined);
    return run;
  }

  private async sleepAbortable(durationMs: number, signal?: AbortSignal): Promise<void> {
    if (signal?.aborted) {
      throw new SequenceAbortError(abortReasonText(signal));
    }
    const sleepPromise = this.sleepMs(durationMs, signal);
    if (!signal) {
      await sleepPromise;
      return;
    }
    await new Promise<void>((resolve, reject) => {
      let settled = false;
      const onAbort = () => {
        if (settled) return;
        settled = true;
        reject(new SequenceAbortError(abortReasonText(signal)));
      };
      signal.addEventListener('abort', onAbort, { once: true });
      sleepPromise.then(
        () => {
          if (settled) return;
          settled = true;
          signal.removeEventListener('abort', onAbort);
          resolve();
        },
        (err) => {
          if (settled) return;
          settled = true;
          signal.removeEventListener('abort', onAbort);
          reject(err);
        },
      );
    });
  }

  async handleCommand(command: DownlinkCommandEnvelope): Promise<CommandReplyEnvelope> {
    if (
      !command ||
      command.schemaVersion !== '2.0' ||
      typeof command.commandId !== 'string' ||
      !command.commandId ||
      typeof command.deviceId !== 'string' ||
      !command.deviceId ||
      typeof command.commandCode !== 'string' ||
      !command.commandCode ||
      !Number.isInteger(command.executionFence) ||
      command.executionFence < 1 ||
      typeof command.issuedAt !== 'number' ||
      !Number.isFinite(command.issuedAt) ||
      typeof command.expireAt !== 'number' ||
      !Number.isFinite(command.expireAt) ||
      command.expireAt <= command.issuedAt
    ) {
      return this.emitReply(command, 'REJECTED', 'INVALID_COMMAND_ENVELOPE', 'INVALID_ENVELOPE', 'NOT_APPLIED');
    }

    await this.initialize();

    type SequenceLifecycleHandle = {
      controller: AbortController;
      tracked: Promise<void>;
      resolveDone: () => void;
    };
    type AdmissionOutcome =
      | { kind: 'reply'; reply: CommandReplyEnvelope }
      | { kind: 'emergency' }
      | { kind: 'unit_sequence'; unitCode: UnitCoilPlan['unitCode']; lifecycle: SequenceLifecycleHandle; previousTask: Promise<void> | null }
      | { kind: 'direct'; route: DirectDeviceRoute; directTask: Promise<void>; resolveDirect: () => void };

    const admitted = await this.withSerializedAdmission<AdmissionOutcome>(async () => {
      if (this.executedCommandIds.has(command.commandId)) {
        return { kind: 'reply', reply: await this.emitReply(command, 'REJECTED', 'DUPLICATE_COMMAND', command.commandCode, 'NOT_APPLIED') };
      }

      const previousFence = this.lastFenceByDevice.get(command.deviceId);
      if (command.executionFence <= (previousFence ?? 0)) {
        return { kind: 'reply', reply: await this.emitReply(command, 'REJECTED', 'STALE_EXECUTION_FENCE', command.commandCode, 'NOT_APPLIED') };
      }

      if (this.nowMs() >= command.expireAt) {
        return { kind: 'reply', reply: await this.emitReply(command, 'EXPIRED', 'COMMAND_EXPIRED', command.commandCode, 'NOT_APPLIED') };
      }

      const isEmergency = this.isEmergencyDoRelease(command);
      const isAbort = !isEmergency && this.isAbortUnitSequence(command);
      const isUnitSequence = !isEmergency && !isAbort && (command.commandCode === 'START_UNIT_SEQUENCE' || command.commandCode === 'STOP_UNIT_SEQUENCE');
      let unitCode: UnitCoilPlan['unitCode'] | null = null;
      let directRoute: DirectDeviceRoute | undefined;

      if (isAbort || isUnitSequence) {
        unitCode = this.resolveUnitCode(command, isAbort);
        if (!unitCode) {
          return { kind: 'reply', reply: await this.emitReply(command, 'REJECTED', 'UNKNOWN_ACTUATION_ROUTE', command.commandCode, 'NOT_APPLIED') };
        }
      } else if (!isEmergency) {
        directRoute = DIRECT_ROUTES.get(`${command.deviceId}|${command.commandCode}`);
        if (!directRoute || !this.matchesExplicitRoute(command, directRoute)) {
          return { kind: 'reply', reply: await this.emitReply(command, 'REJECTED', 'UNKNOWN_ACTUATION_ROUTE', command.commandCode, 'NOT_APPLIED') };
        }
      }

      try {
        await this.persistState(this.snapshotWithAcceptedCommand(command));
      } catch (err) {
        this.reportDiagnostic({
          level: 'error',
          code: 'STATE_PERSIST_FAILED',
          commandId: command.commandId,
          message: `failed to persist accepted command ${command.commandId}: ${errorText(err)}`,
        });
        return { kind: 'reply', reply: await this.emitReply(command, 'FAILED', 'STATE_PERSIST_FAILED', command.commandCode, 'NOT_APPLIED') };
      }
      this.commitCommandAccepted(command);

      if (isEmergency) {
        for (const plan of Object.values(UNIT_PLANS)) {
          this.abortUnitSequence(plan.unitCode, 'EMERGENCY_DO_RELEASE');
        }
        return { kind: 'emergency' };
      }

      if (isAbort && unitCode) {
        this.abortUnitSequence(unitCode, 'SEQUENCE_ABORTED_BY_OPERATOR');
        return { kind: 'reply', reply: await this.emitReply(command, 'EXECUTED', null, command.commandCode, `${unitCode}:sequence-aborted`) };
      }

      if (isUnitSequence && unitCode) {
        this.abortUnitSequence(unitCode);
        const previousTask = this.activeSequenceTasks.get(unitCode) ?? null;
        const controller = new AbortController();
        let resolveDone: () => void = () => {};
        const tracked = new Promise<void>((resolve) => {
          resolveDone = resolve;
        });
        this.activeSequenceAborts.set(unitCode, controller);
        this.activeSequenceTasks.set(unitCode, tracked);
        this.allSequenceTasks.add(tracked);
        return {
          kind: 'unit_sequence',
          unitCode,
          lifecycle: { controller, tracked, resolveDone },
          previousTask,
        };
      }

      if (command.commandCode === 'STOP' && directRoute) {
        this.abortUnitSequence(directRoute.unitCode);
      }
      let resolveDirect: () => void = () => {};
      const directTask = new Promise<void>((resolve) => {
        resolveDirect = resolve;
      });
      this.allDirectTasks.add(directTask);
      return { kind: 'direct', route: directRoute!, directTask, resolveDirect };
    });

    if (admitted.kind === 'reply') {
      return admitted.reply;
    }
    if (admitted.kind === 'emergency') {
      return this.executeEmergencyDoRelease(command);
    }
    if (admitted.kind === 'unit_sequence') {
      return this.executeUnitSequence(command, UNIT_PLANS[admitted.unitCode], admitted.lifecycle, admitted.previousTask);
    }

    const { route, directTask, resolveDirect } = admitted;
    try {
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

      const duration = typeof command.pulseDurationMs === 'number' && Number.isFinite(command.pulseDurationMs) && command.pulseDurationMs > 0
        ? command.pulseDurationMs
        : this.pulseDurationMs;
      try {
        await this.executePulse(route.coil, route.channel, duration, undefined, { unitCode: route.unitCode, commandId: command.commandId });
      } catch (error) {
        const reason = error instanceof PulseResetError
          ? 'PULSE_RESET_FAILED'
          : error instanceof StatePersistError
            ? 'STATE_PERSIST_FAILED'
            : 'MODBUS_WRITE_FAILED';
        return this.emitReply(command, 'FAILED', reason, `${route.channel}:pulse(${duration}ms)`, 'NOT_APPLIED');
      }
      return this.emitReply(command, 'EXECUTED', null, `${route.channel}:pulse(${duration}ms)`, `${route.channel}:pulse(${duration}ms)->0`);
    } finally {
      this.allDirectTasks.delete(directTask);
      resolveDirect();
    }
  }

  private async runInitialize(): Promise<void> {
    const loaded = await this.options.stateStore?.load();
    this.lastFenceByDevice.clear();
    this.executedCommandIds.clear();
    this.pendingPulseCoils.clear();
    if (loaded && typeof loaded === 'object') {
      if (typeof loaded.lastSequence === 'number' && Number.isSafeInteger(loaded.lastSequence) && loaded.lastSequence >= 0 && loaded.lastSequence > this.uplinkSeq) {
        this.uplinkSeq = loaded.lastSequence;
      }
      if (loaded.lastFenceByDevice && typeof loaded.lastFenceByDevice === 'object' && !Array.isArray(loaded.lastFenceByDevice)) {
        for (const [deviceId, fence] of Object.entries(loaded.lastFenceByDevice)) {
          if (typeof deviceId === 'string' && deviceId.length > 0 && typeof fence === 'number' && Number.isSafeInteger(fence) && fence >= 1) {
            const existing = this.lastFenceByDevice.get(deviceId) ?? 0;
            if (fence > existing) this.lastFenceByDevice.set(deviceId, fence);
          }
        }
      }
      if (Array.isArray(loaded.executedCommandIds)) {
        const startIdx = Math.max(0, loaded.executedCommandIds.length - MAX_EXECUTED_COMMAND_IDS);
        for (let i = startIdx; i < loaded.executedCommandIds.length; i += 1) {
          const commandId = loaded.executedCommandIds[i];
          if (typeof commandId === 'string' && commandId.length > 0) {
            this.recordExecutedCommandId(commandId);
          }
        }
      }
      if (Array.isArray(loaded.pendingPulseCoils)) {
        for (const coil of loaded.pendingPulseCoils) {
          if (typeof coil === 'number' && Number.isInteger(coil) && PULSE_COIL_SET.has(coil)) {
            this.pendingPulseCoils.add(coil);
          }
        }
      }
    }

    const recoveredPending = new Set(this.pendingPulseCoils);
    const resetPoints: UplinkPoint[] = [];
    const clearedRecoveredLabels: string[] = [];

    for (const { coil, channel, unitCode } of PULSE_COIL_DESCRIPTORS) {
      try {
        await this.resetPulseCoilWithRetry(coil);
        this.pendingPulseCoils.delete(coil);
        resetPoints.push({ code: channel, value: 0, quality: 0 });
        if (recoveredPending.has(coil)) {
          clearedRecoveredLabels.push(`${channel}(coil=${coil})`);
        }
      } catch (err) {
        this.pendingPulseCoils.add(coil);
        this.reportDiagnostic({
          level: 'error',
          code: 'PULSE_RESET_FAILED',
          unitCode,
          message: `startup failed to reset pulse coil ${channel}(coil=${coil}) to 0 after ${PULSE_RESET_ATTEMPTS} attempts: ${errorText(err)}`,
        });
      }
    }

    if (clearedRecoveredLabels.length > 0) {
      this.reportDiagnostic({
        level: 'warn',
        code: 'STARTUP_PENDING_PULSE_RESET',
        message: `cleared pending pulse coils on startup: ${clearedRecoveredLabels.join(', ')}`,
      });
    }

    await this.persistState();
    if (resetPoints.length > 0) {
      await this.emitTelemetryEntries([
        { deviceId: 'et1010', deviceTimestamp: this.nowMs(), points: resetPoints },
      ]);
    }
  }

  private commitCommandAccepted(command: DownlinkCommandEnvelope): void {
    this.lastFenceByDevice.set(command.deviceId, command.executionFence);
    this.recordExecutedCommandId(command.commandId);
  }

  private recordExecutedCommandId(commandId: string): void {
    if (this.executedCommandIds.has(commandId)) return;
    this.executedCommandIds.add(commandId);
    while (this.executedCommandIds.size > MAX_EXECUTED_COMMAND_IDS) {
      const oldest = this.executedCommandIds.values().next().value;
      if (oldest === undefined) break;
      this.executedCommandIds.delete(oldest);
    }
  }

  private snapshotState(): ExecutorPersistedState {
    return {
      lastSequence: this.uplinkSeq,
      lastFenceByDevice: Object.fromEntries(this.lastFenceByDevice.entries()),
      executedCommandIds: [...this.executedCommandIds],
      pendingPulseCoils: [...this.pendingPulseCoils].sort((a, b) => a - b),
    };
  }

  private snapshotWithAcceptedCommand(command: DownlinkCommandEnvelope): ExecutorPersistedState {
    const nextFences = new Map(this.lastFenceByDevice);
    nextFences.set(command.deviceId, command.executionFence);
    const nextIds = new Set(this.executedCommandIds);
    nextIds.add(command.commandId);
    while (nextIds.size > MAX_EXECUTED_COMMAND_IDS) {
      const oldest = nextIds.values().next().value;
      if (oldest === undefined) break;
      nextIds.delete(oldest);
    }
    return {
      lastSequence: this.uplinkSeq,
      lastFenceByDevice: Object.fromEntries(nextFences.entries()),
      executedCommandIds: [...nextIds],
      pendingPulseCoils: [...this.pendingPulseCoils].sort((a, b) => a - b),
    };
  }

  private persistState(snapshot: ExecutorPersistedState = this.snapshotState()): Promise<void> {
    const store = this.options.stateStore;
    if (!store) return Promise.resolve();
    const writeTask = this.persistChain.then(() => store.save(snapshot));
    this.persistChain = writeTask.catch(() => undefined);
    return writeTask;
  }

  private isAbortUnitSequence(command: DownlinkCommandEnvelope): boolean {
    return command.actuationMode === 'ABORT_UNIT_SEQUENCE' || command.commandCode === 'ABORT_UNIT_SEQUENCE';
  }

  private resolveUnitCode(command: DownlinkCommandEnvelope, allowAbortMode: boolean): UnitCoilPlan['unitCode'] | null {
    const inferred = CHILLER_UNIT_BY_DEVICE.get(command.deviceId) ?? null;
    if (!inferred) return null;
    if (command.channel && command.channel !== inferred) return null;
    if (command.actuationMode) {
      if (allowAbortMode) {
        if (command.actuationMode !== 'ABORT_UNIT_SEQUENCE' && command.actuationMode !== 'UNIT_SEQUENCE') {
          return null;
        }
      } else if (command.actuationMode !== 'UNIT_SEQUENCE') {
        return null;
      }
    }
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

  private async writeCoil(coilAddress: number, value: 0 | 1, priority = BUS_PRIORITY_NORMAL): Promise<void> {
    const run = async () => {
      await this.options.coilWriter.writeCoil(ET1010_UNIT_ID, coilAddress, value);
      this.knownCoilStates.set(coilAddress, value);
    };
    if (this.options.busLock) {
      await this.options.busLock.runExclusive(priority, run);
      return;
    }
    await run();
  }

  private async readCoil(coilAddress: number): Promise<0 | 1> {
    const reader = this.options.coilReader;
    if (!reader) return this.knownCoilStates.get(coilAddress) ?? 0;
    const run = async () => {
      const state = await reader(coilAddress);
      this.knownCoilStates.set(coilAddress, state);
      return state;
    };
    return this.options.busLock ? this.options.busLock.runExclusive(BUS_PRIORITY_NORMAL, run) : run();
  }

  private async resetPulseCoilWithRetry(coil: number): Promise<void> {
    let lastError: unknown = null;
    for (let attempt = 1; attempt <= PULSE_RESET_ATTEMPTS; attempt += 1) {
      try {
        await this.writeCoil(coil, 0, BUS_PRIORITY_PULSE_RESET);
        return;
      } catch (err) {
        lastError = err;
        if (attempt < PULSE_RESET_ATTEMPTS) {
          await this.sleepMs(PULSE_RESET_BACKOFF_MS * attempt);
        }
      }
    }
    throw lastError;
  }

  private async executePulse(
    coil: number,
    channel: string,
    durationMs: number,
    signal?: AbortSignal,
    context?: { unitCode?: UnitCoilPlan['unitCode']; commandId?: string },
  ): Promise<void> {
    this.pendingPulseCoils.add(coil);
    try {
      await this.persistState();
    } catch (err) {
      this.pendingPulseCoils.delete(coil);
      this.reportDiagnostic({
        level: 'error',
        code: 'STATE_PERSIST_FAILED',
        unitCode: context?.unitCode,
        commandId: context?.commandId,
        message: `failed to persist pending pulse coil ${channel}(coil=${coil}) before pulse start: ${errorText(err)}`,
      });
      throw new StatePersistError(err);
    }

    let writeStartError: unknown = null;
    let resetError: unknown = null;
    let sleepError: unknown = null;
    try {
      await this.writeCoil(coil, 1);
      await this.sleepAbortable(durationMs, signal);
    } catch (err) {
      if (err instanceof SequenceAbortError) {
        sleepError = err;
      } else {
        writeStartError = err;
      }
    } finally {
      try {
        await this.resetPulseCoilWithRetry(coil);
        this.pendingPulseCoils.delete(coil);
      } catch (err) {
        resetError = err;
      }
    }
    if (resetError !== null) {
      try {
        await this.persistState();
      } catch (persistErr) {
        this.reportDiagnostic({
          level: 'error',
          code: 'STATE_PERSIST_FAILED',
          unitCode: context?.unitCode,
          commandId: context?.commandId,
          message: `failed to persist pending pulse coil ${channel}(coil=${coil}) after reset failure: ${errorText(persistErr)}`,
        });
      }
      this.reportDiagnostic({
        level: 'error',
        code: 'PULSE_RESET_FAILED',
        unitCode: context?.unitCode,
        commandId: context?.commandId,
        message: `failed to reset pulse coil ${channel}(coil=${coil}) to 0 after ${PULSE_RESET_ATTEMPTS} attempts: ${errorText(resetError)}`,
      });
      throw new PulseResetError(resetError);
    }

    try {
      await this.persistState();
    } catch (persistErr) {
      this.reportDiagnostic({
        level: 'error',
        code: 'STATE_PERSIST_FAILED',
        unitCode: context?.unitCode,
        commandId: context?.commandId,
        message: `failed to clear pending pulse coil ${channel}(coil=${coil}) after reset: ${errorText(persistErr)}`,
      });
      if (writeStartError === null && sleepError === null) {
        throw new StatePersistError(persistErr);
      }
    }

    if (writeStartError !== null) {
      throw writeStartError;
    }

    await this.emitTelemetryEntries([
      { deviceId: 'et1010', deviceTimestamp: this.nowMs(), points: [{ code: channel, value: 0, quality: 0 }] },
    ]);

    if (sleepError !== null) {
      throw sleepError;
    }
  }

  private async executeEmergencyDoRelease(command: DownlinkCommandEnvelope): Promise<CommandReplyEnvelope> {
    for (const plan of Object.values(UNIT_PLANS)) {
      this.abortUnitSequence(plan.unitCode, 'EMERGENCY_DO_RELEASE');
    }
    await this.waitForActiveSequences();

    let writeFailures = 0;
    for (const { coil } of ALL_DO_COIL_ENTRIES) {
      try {
        await this.writeCoil(coil, 0, BUS_PRIORITY_PULSE_RESET);
        this.pendingPulseCoils.delete(coil);
      } catch {
        writeFailures += 1;
      }
    }
    let persistFailed = false;
    try {
      await this.persistState();
    } catch (err) {
      persistFailed = true;
      this.reportDiagnostic({
        level: 'error',
        code: 'STATE_PERSIST_FAILED',
        commandId: command.commandId,
        message: `failed to persist emergency DO release state: ${errorText(err)}`,
      });
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
    if (persistFailed) {
      return this.emitReply(command, 'FAILED', 'STATE_PERSIST_FAILED', 'all-do:0', 'NOT_APPLIED');
    }
    return this.emitReply(command, 'EXECUTED', null, 'all-do:0', 'all-do:0');
  }

  private releaseSequenceLifecycle(
    unitCode: UnitCoilPlan['unitCode'],
    controller: AbortController,
    tracked: Promise<void>,
    resolveDone: () => void,
  ): void {
    if (this.activeSequenceAborts.get(unitCode) === controller) {
      this.activeSequenceAborts.delete(unitCode);
    }
    if (this.activeSequenceTasks.get(unitCode) === tracked) {
      this.activeSequenceTasks.delete(unitCode);
    }
    this.allSequenceTasks.delete(tracked);
    resolveDone();
  }

  private async executeUnitSequence(
    command: DownlinkCommandEnvelope,
    plan: UnitCoilPlan,
    lifecycle: { controller: AbortController; tracked: Promise<void>; resolveDone: () => void },
    previousTask: Promise<void> | null,
  ): Promise<CommandReplyEnvelope> {
    const { controller, tracked, resolveDone } = lifecycle;
    const pulseMs = typeof command.pulseDurationMs === 'number' && Number.isFinite(command.pulseDurationMs) && command.pulseDurationMs > 0
      ? command.pulseDurationMs
      : this.pulseDurationMs;
    const isStart = command.commandCode === 'START_UNIT_SEQUENCE';
    const checkAborted = () => {
      if (controller.signal.aborted) throw new SequenceAbortError(abortReasonText(controller.signal));
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
      await this.sleepAbortable(ms, controller.signal);
      checkAborted();
    };

    let tailLaunched = false;
    try {
      if (previousTask) {
        await previousTask.catch(() => undefined);
        checkAborted();
      }
      if (isStart) {
        await writeStep(plan.chwp, 1);
        checkAborted();
        tailLaunched = true;
        this.launchSequenceTask(plan.unitCode, command.commandId, lifecycle, async () => {
          await waitStep(this.sequenceTimings.pumpToCoolingPumpMs);
          await writeStep(plan.cwp, 1);
          await waitStep(this.sequenceTimings.coolingPumpToTowerMs);
          await writeStep(plan.tower, 1);
          await waitStep(this.sequenceTimings.towerToChillerPulseMs);
          const allClosed = await this.verifyRequiredCoilsClosed(plan, command.commandId, controller.signal);
          if (!allClosed) return;
          checkAborted();
          await this.executePulse(
            plan.chillerStart.coil,
            plan.chillerStart.channel,
            pulseMs,
            controller.signal,
            { unitCode: plan.unitCode, commandId: command.commandId },
          );
        });
        return this.emitReply(command, 'EXECUTED', null, command.commandCode, `${plan.unitCode}:sequence-accepted`);
      }

      checkAborted();
      await this.executePulse(
        plan.chillerStop.coil,
        plan.chillerStop.channel,
        pulseMs,
        controller.signal,
        { unitCode: plan.unitCode, commandId: command.commandId },
      );
      checkAborted();
      tailLaunched = true;
      this.launchSequenceTask(plan.unitCode, command.commandId, lifecycle, async () => {
        await waitStep(this.sequenceTimings.chillerStopCoastDownMs);
        const stopConfirmed = await this.confirmChillerStoppedBeforeAuxShutdown(plan, command.commandId, controller.signal);
        if (!stopConfirmed) return;
        await writeStep(plan.tower, 0);
        await waitStep(this.sequenceTimings.auxiliaryStopStepMs);
        await writeStep(plan.cwp, 0);
        await waitStep(this.sequenceTimings.auxiliaryStopStepMs);
        await writeStep(plan.chwp, 0);
      });
      return this.emitReply(command, 'EXECUTED', null, command.commandCode, `${plan.unitCode}:sequence-accepted`);
    } catch (error) {
      if (error instanceof SequenceAbortError) {
        return this.emitReply(command, 'REJECTED', error.reasonCode, command.commandCode, 'ABORTED');
      }
      if (error instanceof PulseResetError) {
        return this.emitReply(command, 'FAILED', 'PULSE_RESET_FAILED', command.commandCode, 'NOT_APPLIED');
      }
      if (error instanceof StatePersistError) {
        return this.emitReply(command, 'FAILED', 'STATE_PERSIST_FAILED', command.commandCode, 'NOT_APPLIED');
      }
      return this.emitReply(command, 'FAILED', 'MODBUS_WRITE_FAILED', command.commandCode, 'NOT_APPLIED');
    } finally {
      if (!tailLaunched) {
        this.releaseSequenceLifecycle(plan.unitCode, controller, tracked, resolveDone);
      }
    }
  }

  private async confirmChillerStoppedBeforeAuxShutdown(
    plan: UnitCoilPlan,
    commandId: string,
    signal: AbortSignal,
  ): Promise<boolean> {
    const reader = this.options.chillerPowerReader;
    if (!reader) return true;

    const readPower = async (): Promise<number | null> => {
      if (signal.aborted) throw new SequenceAbortError(abortReasonText(signal));
      const value = await reader(plan.unitCode);
      if (signal.aborted) throw new SequenceAbortError(abortReasonText(signal));
      return typeof value === 'number' && Number.isFinite(value) ? value : null;
    };

    let power = await readPower();
    if (power === null || power <= this.chillerStopPowerKw) return true;

    for (let recheck = 0; recheck < CHILLER_STOP_RECHECK_LIMIT; recheck += 1) {
      await this.sleepAbortable(this.sequenceTimings.auxiliaryStopStepMs, signal);
      if (signal.aborted) throw new SequenceAbortError(abortReasonText(signal));
      const nextPower = await readPower();
      if (nextPower !== null) {
        power = nextPower;
        if (power <= this.chillerStopPowerKw) return true;
      }
    }

    this.reportDiagnostic({
      level: 'error',
      code: 'CHILLER_STOP_UNCONFIRMED_AUX_HELD',
      unitCode: plan.unitCode,
      commandId,
      message: `${plan.unitCode}: aborted auxiliary shutdown because chiller power remained ${power} kW (> ${this.chillerStopPowerKw} kW)`,
    });
    return false;
  }

  private async verifyRequiredCoilsClosed(
    plan: UnitCoilPlan,
    commandId: string,
    signal: AbortSignal,
  ): Promise<boolean> {
    const required = [plan.chwp, plan.cwp, plan.tower];
    const openParts: Array<{ deviceId: string; channel: string; coil: number }> = [];
    for (const part of required) {
      if (signal.aborted) throw new SequenceAbortError(abortReasonText(signal));
      const state = await this.readCoil(part.coil);
      if (signal.aborted) throw new SequenceAbortError(abortReasonText(signal));
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
    lifecycle: { controller: AbortController; tracked: Promise<void>; resolveDone: () => void },
    runTail: () => Promise<void>,
  ): void {
    const { controller, tracked, resolveDone } = lifecycle;
    void (async (): Promise<void> => {
      try {
        await waitNextTurn(controller.signal);
        if (controller.signal.aborted) return;
        await runTail();
      } catch (error) {
        if (error instanceof SequenceAbortError) return;
        if (!(error instanceof PulseResetError)) {
          this.reportDiagnostic({
            level: 'error',
            code: 'SEQUENCE_STEP_FAILED',
            unitCode,
            commandId,
            message: `${unitCode}: background sequence failed: ${errorText(error)}`,
          });
        }
      } finally {
        this.releaseSequenceLifecycle(unitCode, controller, tracked, resolveDone);
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
    const nextSequence = Math.max(now * 1000, this.uplinkSeq + 1);
    this.uplinkSeq = nextSequence;
    try {
      await this.persistState();
    } catch (err) {
      this.reportDiagnostic({
        level: 'error',
        code: 'STATE_PERSIST_FAILED',
        message: `failed to persist telemetry sequence state: ${errorText(err)}`,
      });
    }
    await this.options.publishTelemetry({
      topic: TELEMETRY_TOPIC,
      payload: {
        schemaVersion: '2.0',
        gatewayId: SONGYAXUAN_GATEWAY_ID,
        messageId: this.uuidv7(),
        timestamp: now,
        sequence: nextSequence,
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
