import { BUS_PRIORITY_NORMAL, Rs485BusLock } from './rs485-bus-lock.ts';

export const RPC_SCHEMA_VERSION = '1.0' as const;
export const EID_SLOT_COUNT = 200;
export const MAX_EID_SLOTS_PER_FC16 = 20;

export const PROTECTED_WRITE_UIDS: ReadonlySet<number> = new Set([2, 3, 4, 5, 10, 31]);

export const REG = {
  COMMIT: 0x9c41,
  COMMIT_WRITE_PARAMS: 6,
  COMMIT_WRITE_EIDS: 8,
  BLOCK_A_START: 0x9c43,
  BLOCK_A_COUNT: 7,
  BLOCK_B_START: 0x9d0f,
  BLOCK_B_COUNT: 7,
  BLOCK_C_START: 0x9d6d,
  BLOCK_C_COUNT: 10,
  EID_BASE: 0xb799,
  SENSOR_TYPE_BASE: 0x7531,
  VOLTAGE_BASE: 0x75f9,
  TEMP_BASE: 0x76c1,
  RSSI_BASE: 0x9089,
} as const;

export interface ConcentratorParams {
  modbusId: number;
  baudRateCode: number;
  offLineTime: number;
  dataUplinkCycle: number;
  localNet: number;
  timeMark: number;
  rfTxPower: number;
  rfFrequency: number;
  rfBandWidth: number;
  rfSf: number;
  rfLeadCode: number;
  txTimeout: number;
  rxTimeout: number;
  rssiOut: number;
}

export type ConcentratorParamKey = keyof ConcentratorParams;

interface ParamSpec {
  address: number;
  min: number;
  max: number;
}

export const PARAM_SPECS: Readonly<Record<ConcentratorParamKey, ParamSpec>> = {
  modbusId: { address: 0x9c43, min: 1, max: 247 },
  baudRateCode: { address: 0x9c45, min: 0, max: 5 },
  offLineTime: { address: 0x9c48, min: 1, max: 65535 },
  dataUplinkCycle: { address: 0x9c49, min: 1, max: 65535 },
  localNet: { address: 0x9d0f, min: 0, max: 65535 },
  timeMark: { address: 0x9d15, min: 0, max: 3 },
  rfTxPower: { address: 0x9d6d, min: 0, max: 30 },
  rfFrequency: { address: 0x9d6e, min: 100, max: 1000 },
  rfBandWidth: { address: 0x9d6f, min: 0, max: 2 },
  rfSf: { address: 0x9d70, min: 5, max: 12 },
  rfLeadCode: { address: 0x9d71, min: 1, max: 65535 },
  txTimeout: { address: 0x9d73, min: 1, max: 65535 },
  rxTimeout: { address: 0x9d74, min: 1, max: 65535 },
  rssiOut: { address: 0x9d76, min: 0, max: 1 },
};

export interface EIDSlot {
  slot: number;
  eid: number;
}

export interface DirtyEIDChunk {
  startSlot: number;
  slotCount: number;
  startAddress: number;
  registers: number[];
  eids: number[];
}

export interface ChannelReading {
  channel: number;
  eid: number;
  online: boolean;
  sensorType: number;
  sensorModel: string;
  temperatureC: number | null;
  voltageMv: number | null;
  rssiDbm: number | null;
}

export interface ConcentratorRPCRequest {
  schemaVersion: string;
  requestId: string;
  gatewayId: string;
  unitId: number;
  operation: 'READ_STATUS' | 'WRITE_PARAMS' | 'READ_EIDS' | 'WRITE_EIDS';
  issuedAt: number;
  expireAt: number;
  params?: Partial<Record<ConcentratorParamKey, number>>;
  slots?: EIDSlot[];
}

export interface ConcentratorRPCResponse {
  schemaVersion: '1.0';
  requestId: string;
  gatewayId: string;
  unitId: number;
  operation: ConcentratorRPCRequest['operation'];
  status: 'OK' | 'REJECTED' | 'FAILED';
  errorCode?: string;
  errorMessage?: string;
  respondedAt: number;
  params?: ConcentratorParams;
  channels?: ChannelReading[];
  slots?: EIDSlot[];
  writtenCount?: number;
  updatedSlots?: number;
  chunkCount?: number;
}

export interface ModbusBusClient {
  readHoldingRegisters(unitId: number, address: number, length: number): Promise<number[]>;
  readInputRegisters(unitId: number, address: number, length: number): Promise<number[]>;
  writeRegister(unitId: number, address: number, value: number): Promise<void>;
  writeRegisters(unitId: number, address: number, values: number[]): Promise<void>;
}

export interface ExecutorOptions {
  settleMs?: number;
  now?: () => number;
  sleep?: (ms: number) => Promise<void>;
  busLock?: Rs485BusLock;
  busPriority?: number;
}

const UUID_V7_REGEX = /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;

function defaultSleep(ms: number): Promise<void> {
  return ms <= 0 ? Promise.resolve() : new Promise((resolve) => setTimeout(resolve, ms));
}

function u16(value: number | undefined): number {
  return (Number(value) >>> 0) & 0xffff;
}

function s16(value: number | undefined): number {
  const n = u16(value);
  return n >= 0x8000 ? n - 0x10000 : n;
}

function sensorModelFor(sensorType: number): string {
  switch (sensorType) {
    case 0:
      return '离线';
    case 0x35:
      return 'PT100/无线测温';
    case 0x45:
      return '温湿度传感器';
    case 0x55:
      return '无线温度变送器';
    default:
      return `0x${sensorType.toString(16).toUpperCase()}`;
  }
}

export function decodeParamsFromBlocks(blockA: number[], blockB: number[], blockC: number[]): ConcentratorParams {
  if (blockA.length < REG.BLOCK_A_COUNT || blockB.length < REG.BLOCK_B_COUNT || blockC.length < REG.BLOCK_C_COUNT) {
    throw new Error('incomplete holding register blocks returned by concentrator');
  }
  return {
    modbusId: u16(blockA[0]),
    baudRateCode: u16(blockA[2]),
    offLineTime: u16(blockA[5]),
    dataUplinkCycle: u16(blockA[6]),
    localNet: u16(blockB[0]),
    timeMark: u16(blockB[6]),
    rfTxPower: u16(blockC[0]),
    rfFrequency: u16(blockC[1]),
    rfBandWidth: u16(blockC[2]),
    rfSf: u16(blockC[3]),
    rfLeadCode: u16(blockC[4]),
    txTimeout: u16(blockC[6]),
    rxTimeout: u16(blockC[7]),
    rssiOut: u16(blockC[9]),
  };
}

export function encodeUint32BigEndian(value: number): [number, number] {
  const unsigned = value >>> 0;
  return [(unsigned >>> 16) & 0xffff, unsigned & 0xffff];
}

export function decodeUint32BigEndian(highWord: number, lowWord: number): number {
  return (((highWord & 0xffff) << 16) | (lowWord & 0xffff)) >>> 0;
}

export function computeDirtyEIDChunks(
  currentSlots: readonly EIDSlot[],
  desiredSlots: readonly EIDSlot[],
  maxSlotsPerChunk = MAX_EID_SLOTS_PER_FC16,
): DirtyEIDChunk[] {
  if (currentSlots.length !== EID_SLOT_COUNT || desiredSlots.length !== EID_SLOT_COUNT) {
    throw new Error(`expected ${EID_SLOT_COUNT} EID slots`);
  }
  const chunks: DirtyEIDChunk[] = [];
  let idx = 0;
  while (idx < EID_SLOT_COUNT) {
    if ((currentSlots[idx].eid >>> 0) === (desiredSlots[idx].eid >>> 0)) {
      idx += 1;
      continue;
    }
    const startIdx = idx;
    const eids: number[] = [];
    const registers: number[] = [];
    while (
      idx < EID_SLOT_COUNT &&
      eids.length < maxSlotsPerChunk &&
      (currentSlots[idx].eid >>> 0) !== (desiredSlots[idx].eid >>> 0)
    ) {
      const eid = desiredSlots[idx].eid >>> 0;
      const [hi, lo] = encodeUint32BigEndian(eid);
      eids.push(eid);
      registers.push(hi, lo);
      idx += 1;
    }
    const startSlot = startIdx + 1;
    chunks.push({
      startSlot,
      slotCount: eids.length,
      startAddress: REG.EID_BASE + startIdx * 2,
      registers,
      eids,
    });
  }
  return chunks;
}

type ValidationResult<T> = { ok: true; value: T } | { ok: false; code: string; message: string };

function validateEIDUpdateSlots(rawSlots: EIDSlot[] | undefined): ValidationResult<EIDSlot[]> {
  if (!Array.isArray(rawSlots) || rawSlots.length !== EID_SLOT_COUNT) {
    return { ok: false, code: 'CONCENTRATOR_EIDS_INVALID', message: `expected ${EID_SLOT_COUNT} EID slots` };
  }
  const ordered: EIDSlot[] = new Array(EID_SLOT_COUNT);
  const seenSlots = new Set<number>();
  const seenNonZero = new Map<number, number>();
  for (const item of rawSlots) {
    if (
      !Number.isInteger(item.slot) ||
      item.slot < 1 ||
      item.slot > EID_SLOT_COUNT ||
      seenSlots.has(item.slot) ||
      !Number.isInteger(item.eid) ||
      item.eid < 0 ||
      item.eid > 0xffffffff
    ) {
      return { ok: false, code: 'CONCENTRATOR_EIDS_INVALID', message: `invalid EID slot entry at slot ${item.slot}` };
    }
    seenSlots.add(item.slot);
    if (item.eid > 0) {
      const prev = seenNonZero.get(item.eid);
      if (prev !== undefined) {
        return {
          ok: false,
          code: 'CONCENTRATOR_EID_DUPLICATE',
          message: `duplicate EID ${item.eid} in slots ${prev} and ${item.slot}`,
        };
      }
      seenNonZero.set(item.eid, item.slot);
    }
    ordered[item.slot - 1] = { slot: item.slot, eid: item.eid >>> 0 };
  }
  return { ok: true, value: ordered };
}

function validateParamsPatch(
  rawPatch: Partial<Record<ConcentratorParamKey, number>> | undefined,
): ValidationResult<Array<[ConcentratorParamKey, number]>> {
  if (!rawPatch || typeof rawPatch !== 'object') {
    return { ok: false, code: 'CONCENTRATOR_PARAMS_INVALID', message: 'params object is required' };
  }
  const entries: Array<[ConcentratorParamKey, number]> = [];
  for (const key of Object.keys(rawPatch)) {
    if (!(key in PARAM_SPECS)) {
      return { ok: false, code: 'CONCENTRATOR_PARAMS_INVALID', message: `unknown concentrator parameter ${key}` };
    }
    const typedKey = key as ConcentratorParamKey;
    const val = rawPatch[typedKey];
    if (typeof val !== 'number' || !Number.isInteger(val)) {
      return { ok: false, code: 'CONCENTRATOR_PARAMS_INVALID', message: `${typedKey} must be an integer` };
    }
    const spec = PARAM_SPECS[typedKey];
    if (val < spec.min || val > spec.max) {
      return {
        ok: false,
        code: 'CONCENTRATOR_PARAMS_INVALID',
        message: `${typedKey}=${val} out of range [${spec.min},${spec.max}]`,
      };
    }
    if (typedKey === 'modbusId' && PROTECTED_WRITE_UIDS.has(val)) {
      return { ok: false, code: 'CONCENTRATOR_UID_PROTECTED', message: `modbusId ${val} is protected on RS485 bus` };
    }
    entries.push([typedKey, val]);
  }
  if (entries.length === 0) {
    return { ok: false, code: 'CONCENTRATOR_PARAMS_INVALID', message: 'at least one parameter must be provided' };
  }
  // Write modbusId last so preceding registers reach the current unitId before commit.
  entries.sort(([a], [b]) => (a === 'modbusId' ? 1 : b === 'modbusId' ? -1 : 0));
  return { ok: true, value: entries };
}

export class ConcentratorRpcExecutor {
  private readonly modbus: ModbusBusClient;
  private readonly settleMs: number;
  private readonly now: () => number;
  private readonly sleep: (ms: number) => Promise<void>;
  private readonly busLock: Rs485BusLock;
  private readonly busPriority: number;

  constructor(modbus: ModbusBusClient, options: ExecutorOptions = {}) {
    this.modbus = modbus;
    this.settleMs = options.settleMs ?? 300;
    this.now = options.now ?? (() => Date.now());
    this.sleep = options.sleep ?? defaultSleep;
    this.busLock = options.busLock ?? new Rs485BusLock();
    this.busPriority = typeof options.busPriority === 'number' && Number.isFinite(options.busPriority)
      ? options.busPriority
      : BUS_PRIORITY_NORMAL;
  }

  private runSerialized<T>(fn: () => Promise<T>): Promise<T> {
    return this.busLock.runExclusive(this.busPriority, fn);
  }

  async execute(req: ConcentratorRPCRequest): Promise<ConcentratorRPCResponse> {
    const baseReply = (status: 'OK' | 'REJECTED' | 'FAILED', extra: Partial<ConcentratorRPCResponse> = {}): ConcentratorRPCResponse => ({
      schemaVersion: RPC_SCHEMA_VERSION,
      requestId: req.requestId,
      gatewayId: req.gatewayId,
      unitId: req.unitId,
      operation: req.operation,
      status,
      respondedAt: this.now(),
      ...extra,
    });

    if (req.schemaVersion !== RPC_SCHEMA_VERSION) {
      return baseReply('REJECTED', { errorCode: 'CONCENTRATOR_SCHEMA_UNSUPPORTED', errorMessage: 'unsupported RPC schemaVersion' });
    }
    if (!UUID_V7_REGEX.test(req.requestId) || !UUID_V7_REGEX.test(req.gatewayId)) {
      return baseReply('REJECTED', { errorCode: 'CONCENTRATOR_REQUEST_INVALID', errorMessage: 'invalid gatewayId or requestId' });
    }
    if (!Number.isInteger(req.unitId) || req.unitId < 1 || req.unitId > 247) {
      return baseReply('REJECTED', { errorCode: 'CONCENTRATOR_REQUEST_INVALID', errorMessage: `unitId=${req.unitId} out of [1,247]` });
    }
    if (PROTECTED_WRITE_UIDS.has(req.unitId)) {
      return baseReply('REJECTED', { errorCode: 'CONCENTRATOR_UID_PROTECTED', errorMessage: `unitId ${req.unitId} is protected on RS485 bus` });
    }
    if (req.issuedAt !== undefined && (typeof req.issuedAt !== 'number' || !Number.isFinite(req.issuedAt))) {
      return baseReply('REJECTED', { errorCode: 'CONCENTRATOR_REQUEST_INVALID', errorMessage: 'invalid issuedAt' });
    }
    if (
      !Number.isFinite(req.expireAt) ||
      (typeof req.issuedAt === 'number' && Number.isFinite(req.issuedAt) && req.expireAt <= req.issuedAt) ||
      this.now() >= req.expireAt
    ) {
      return baseReply('FAILED', { errorCode: 'CONCENTRATOR_RPC_EXPIRED', errorMessage: 'request expired before execution' });
    }

    const executeOnBus = <T>(
      errorCode: string,
      action: () => Promise<Partial<ConcentratorRPCResponse>>,
    ): Promise<ConcentratorRPCResponse> => this.runSerialized(async () => {
      if (this.now() >= req.expireAt) {
        return baseReply('FAILED', { errorCode: 'CONCENTRATOR_RPC_EXPIRED', errorMessage: 'request expired while waiting for RS485 bus' });
      }
      try {
        return baseReply('OK', await action());
      } catch (err) {
        return baseReply('FAILED', { errorCode, errorMessage: errorText(err) });
      }
    });

    if (req.operation === 'WRITE_PARAMS') {
      const checked = validateParamsPatch(req.params);
      if (!checked.ok) {
        return baseReply('REJECTED', { errorCode: checked.code, errorMessage: checked.message });
      }
      return executeOnBus('CONCENTRATOR_WRITE_FAILED', () => this.writeParams(req.unitId, checked.value));
    }

    if (req.operation === 'WRITE_EIDS') {
      const checked = validateEIDUpdateSlots(req.slots);
      if (!checked.ok) {
        return baseReply('REJECTED', { errorCode: checked.code, errorMessage: checked.message });
      }
      return executeOnBus('CONCENTRATOR_WRITE_FAILED', () => this.writeEids(req.unitId, checked.value));
    }

    if (req.operation === 'READ_EIDS') {
      return executeOnBus('CONCENTRATOR_READ_FAILED', async () => ({ slots: await this.readAllEids(req.unitId) }));
    }

    if (req.operation === 'READ_STATUS') {
      return executeOnBus('CONCENTRATOR_READ_FAILED', () => this.readStatus(req.unitId));
    }

    return baseReply('REJECTED', { errorCode: 'CONCENTRATOR_OPERATION_UNSUPPORTED', errorMessage: `unsupported operation ${String(req.operation)}` });
  }

  private async readParams(unitId: number): Promise<ConcentratorParams> {
    const blockA = await this.modbus.readHoldingRegisters(unitId, REG.BLOCK_A_START, REG.BLOCK_A_COUNT);
    const blockB = await this.modbus.readHoldingRegisters(unitId, REG.BLOCK_B_START, REG.BLOCK_B_COUNT);
    const blockC = await this.modbus.readHoldingRegisters(unitId, REG.BLOCK_C_START, REG.BLOCK_C_COUNT);
    return decodeParamsFromBlocks(blockA, blockB, blockC);
  }

  private async writeParams(
    unitId: number,
    entries: Array<[ConcentratorParamKey, number]>,
  ): Promise<{ params: ConcentratorParams; writtenCount: number }> {
    let targetUnitId = unitId;
    for (const [key, value] of entries) {
      await this.modbus.writeRegister(unitId, PARAM_SPECS[key].address, value);
      if (key === 'modbusId') {
        targetUnitId = value;
      }
    }
    await this.modbus.writeRegister(targetUnitId, REG.COMMIT, REG.COMMIT_WRITE_PARAMS);
    await this.sleep(this.settleMs);
    const readback = await this.readParams(targetUnitId);
    for (const [key, expected] of entries) {
      if (readback[key] !== expected) {
        throw new Error(`readback mismatch on ${key}: got ${readback[key]}, want ${expected}`);
      }
    }
    return { params: readback, writtenCount: entries.length };
  }

  private async readAllEids(unitId: number): Promise<EIDSlot[]> {
    const slots: EIDSlot[] = [];
    const chunkSlots = MAX_EID_SLOTS_PER_FC16;
    for (let offset = 0; offset < EID_SLOT_COUNT; offset += chunkSlots) {
      const words = await this.modbus.readHoldingRegisters(unitId, REG.EID_BASE + offset * 2, chunkSlots * 2);
      for (let i = 0; i < chunkSlots; i += 1) {
        slots.push({
          slot: offset + i + 1,
          eid: decodeUint32BigEndian(words[i * 2] ?? 0, words[i * 2 + 1] ?? 0),
        });
      }
    }
    return slots;
  }

  private async writeEids(
    unitId: number,
    desiredSlots: EIDSlot[],
  ): Promise<{ updatedSlots: number; chunkCount: number; slots: EIDSlot[] }> {
    const currentSlots = await this.readAllEids(unitId);
    const chunks = computeDirtyEIDChunks(currentSlots, desiredSlots, MAX_EID_SLOTS_PER_FC16);
    if (chunks.length === 0) {
      return { updatedSlots: 0, chunkCount: 0, slots: currentSlots };
    }
    let updatedSlots = 0;
    for (const chunk of chunks) {
      await this.modbus.writeRegisters(unitId, chunk.startAddress, chunk.registers);
      updatedSlots += chunk.slotCount;
    }
    await this.modbus.writeRegister(unitId, REG.COMMIT, REG.COMMIT_WRITE_EIDS);
    await this.sleep(this.settleMs);

    const verified = currentSlots.map((slot) => ({ ...slot }));
    for (const chunk of chunks) {
      const readbackWords = await this.modbus.readHoldingRegisters(unitId, chunk.startAddress, chunk.slotCount * 2);
      for (let i = 0; i < chunk.slotCount; i += 1) {
        const got = decodeUint32BigEndian(readbackWords[i * 2] ?? 0, readbackWords[i * 2 + 1] ?? 0);
        if (got !== chunk.eids[i]) {
          throw new Error(`EID readback mismatch at slot ${chunk.startSlot + i}: got ${got}, want ${chunk.eids[i]}`);
        }
        const slotIndex = chunk.startSlot - 1 + i;
        verified[slotIndex] = { slot: slotIndex + 1, eid: got };
      }
    }
    return { updatedSlots, chunkCount: chunks.length, slots: verified };
  }

  private async readStatus(unitId: number): Promise<{ params: ConcentratorParams; channels: ChannelReading[] }> {
    const params = await this.readParams(unitId);
    const eids = await this.readAllEids(unitId);

    const sensorTypes: number[] = [];
    for (let offset = 0; offset < EID_SLOT_COUNT; offset += 40) {
      const block = await this.modbus.readInputRegisters(unitId, REG.SENSOR_TYPE_BASE + offset, 40);
      for (let i = 0; i < 40; i += 1) {
        sensorTypes.push(u16(block[i]));
      }
    }

    const activeChannels: number[] = [];
    for (let i = 0; i < EID_SLOT_COUNT; i += 1) {
      if ((eids[i]?.eid ?? 0) > 0 || (sensorTypes[i] ?? 0) !== 0) {
        activeChannels.push(i + 1);
      }
    }

    const voltageMap = new Map<number, number>();
    const tempMap = new Map<number, number>();
    const rssiMap = new Map<number, number>();

    // Batch contiguous 40-slot input register spans only where active channels exist.
    const activeSpans = new Set<number>();
    for (const ch of activeChannels) {
      activeSpans.add(Math.floor((ch - 1) / 40));
    }
    for (const span of Array.from(activeSpans).sort((a, b) => a - b)) {
      const start = span * 40;
      const volts = await this.modbus.readInputRegisters(unitId, REG.VOLTAGE_BASE + start, 40);
      const temps = await this.modbus.readInputRegisters(unitId, REG.TEMP_BASE + start, 40);
      const rssis = await this.modbus.readInputRegisters(unitId, REG.RSSI_BASE + start, 40);
      for (let i = 0; i < 40; i += 1) {
        const ch = start + i + 1;
        voltageMap.set(ch, u16(volts[i]));
        tempMap.set(ch, s16(temps[i]) / 10);
        rssiMap.set(ch, s16(rssis[i]));
      }
    }

    const channels: ChannelReading[] = activeChannels.map((ch) => {
      const sType = sensorTypes[ch - 1] ?? 0;
      const online = sType !== 0;
      return {
        channel: ch,
        eid: eids[ch - 1]?.eid ?? 0,
        online,
        sensorType: sType,
        sensorModel: sensorModelFor(sType),
        temperatureC: online ? (tempMap.get(ch) ?? null) : null,
        voltageMv: online ? (voltageMap.get(ch) ?? null) : null,
        rssiDbm: online ? (rssiMap.get(ch) ?? null) : null,
      };
    });

    return { params, channels };
  }
}

export function parseDownlinkTopic(topic: string): string | null {
  const parts = topic.split('/');
  if (parts.length !== 5 || parts[0] !== 'hvac' || parts[1] !== 'v1' || parts[3] !== 'down' || parts[4] !== 'concentrator') {
    return null;
  }
  return UUID_V7_REGEX.test(parts[2]) ? parts[2] : null;
}

export function buildUplinkTopic(gatewayId: string): string {
  return `hvac/v1/${gatewayId}/up/concentrator`;
}

export async function handleMqttRpcMessage(
  executor: ConcentratorRpcExecutor,
  topic: string,
  payloadText: string,
): Promise<{ replyTopic: string; reply: ConcentratorRPCResponse } | null> {
  const topicGatewayId = parseDownlinkTopic(topic);
  if (!topicGatewayId) {
    return null;
  }
  let parsed: ConcentratorRPCRequest;
  try {
    parsed = JSON.parse(payloadText) as ConcentratorRPCRequest;
  } catch {
    return null;
  }
  if (!parsed || parsed.gatewayId !== topicGatewayId) {
    return null;
  }
  const reply = await executor.execute(parsed);
  return {
    replyTopic: buildUplinkTopic(topicGatewayId),
    reply,
  };
}

function errorText(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}
