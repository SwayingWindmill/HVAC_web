import assert from 'node:assert/strict';
import test from 'node:test';
import {
  ConcentratorRpcExecutor,
  EID_SLOT_COUNT,
  PROTECTED_WRITE_UIDS,
  REG,
  computeDirtyEIDChunks,
  handleMqttRpcMessage,
  type ConcentratorRPCRequest,
  type EIDSlot,
  type ModbusBusClient,
} from '../edge/node-red/concentrator-mqtt-rpc.ts';
import { BUS_PRIORITY_PULSE_RESET, Rs485BusLock } from '../edge/node-red/rs485-bus-lock.ts';

class FakeModbusConcentrator implements ModbusBusClient {
  holding = new Map<number, Map<number, number>>();
  input = new Map<number, Map<number, number>>();
  writes: Array<{ unitId: number; address: number; values: number[] }> = [];
  activeCalls = 0;
  maxConcurrentCalls = 0;
  latencyMs = 0;

  constructor(unitId = 1) {
    this.initUnit(unitId);
  }

  initUnit(unitId: number): void {
    const h = new Map<number, number>();
    const defaults: Record<number, number> = {
      0x9c43: unitId,
      0x9c45: 3,
      0x9c48: 60,
      0x9c49: 30,
      0x9d0f: 101,
      0x9d15: 1,
      0x9d6d: 20,
      0x9d6e: 470,
      0x9d6f: 2,
      0x9d70: 7,
      0x9d71: 8,
      0x9d73: 3000,
      0x9d74: 3000,
      0x9d76: 1,
    };
    for (const [addr, val] of Object.entries(defaults)) {
      h.set(Number(addr), val);
    }
    this.holding.set(unitId, h);
    this.input.set(unitId, new Map());
  }

  private async track<T>(fn: () => T): Promise<T> {
    this.activeCalls += 1;
    if (this.activeCalls > this.maxConcurrentCalls) {
      this.maxConcurrentCalls = this.activeCalls;
    }
    if (this.latencyMs > 0) {
      await new Promise((resolve) => setTimeout(resolve, this.latencyMs));
    }
    try {
      return fn();
    } finally {
      this.activeCalls -= 1;
    }
  }

  async readHoldingRegisters(unitId: number, address: number, length: number): Promise<number[]> {
    return this.track(() => {
      const regMap = this.holding.get(unitId);
      if (!regMap) throw new Error(`no Modbus unit ${unitId}`);
      const out: number[] = [];
      for (let i = 0; i < length; i += 1) {
        out.push(regMap.get(address + i) ?? 0);
      }
      return out;
    });
  }

  async readInputRegisters(unitId: number, address: number, length: number): Promise<number[]> {
    return this.track(() => {
      const regMap = this.input.get(unitId);
      if (!regMap) throw new Error(`no Modbus unit ${unitId}`);
      const out: number[] = [];
      for (let i = 0; i < length; i += 1) {
        out.push(regMap.get(address + i) ?? 0);
      }
      return out;
    });
  }

  async writeRegister(unitId: number, address: number, value: number): Promise<void> {
    return this.track(() => {
      const regMap = this.holding.get(unitId);
      if (!regMap) throw new Error(`no Modbus unit ${unitId}`);
      this.writes.push({ unitId, address, values: [value & 0xffff] });
      regMap.set(address, value & 0xffff);
      if (address === 0x9c43 && value !== unitId) {
        this.holding.set(value, regMap);
        this.holding.delete(unitId);
        const inp = this.input.get(unitId);
        if (inp) {
          this.input.set(value, inp);
          this.input.delete(unitId);
        }
      }
    });
  }

  async writeRegisters(unitId: number, address: number, values: number[]): Promise<void> {
    return this.track(() => {
      const regMap = this.holding.get(unitId);
      if (!regMap) throw new Error(`no Modbus unit ${unitId}`);
      this.writes.push({ unitId, address, values: [...values] });
      for (let i = 0; i < values.length; i += 1) {
        regMap.set(address + i, values[i] & 0xffff);
      }
    });
  }
}

const GW_ID = '018f3d00-2000-7000-8000-000000000001';
const REQ_ID = '018f3d00-3000-7000-8000-000000000001';

function emptySlots(): EIDSlot[] {
  return Array.from({ length: EID_SLOT_COUNT }, (_, i) => ({ slot: i + 1, eid: 0 }));
}

function setEidOnFake(fake: FakeModbusConcentrator, unitId: number, slot: number, eid: number): void {
  const regMap = fake.holding.get(unitId)!;
  const addr = REG.EID_BASE + (slot - 1) * 2;
  regMap.set(addr, (eid >>> 16) & 0xffff);
  regMap.set(addr + 1, eid & 0xffff);
}

test('computeDirtyEIDChunks coalesces contiguous runs into <= 20-slot FC16 chunks', () => {
  const current = emptySlots();
  const desired = emptySlots();
  // Contiguous run of 3 slots (1..3)
  desired[0].eid = 1001;
  desired[1].eid = 1002;
  desired[2].eid = 1003;
  // Single dirty slot 50
  desired[49].eid = 33661717;
  // Contiguous run of 25 slots (101..125) -> must split into 20 + 5
  for (let i = 100; i < 125; i += 1) {
    desired[i].eid = 50000 + i;
  }

  const startedAt = performance.now();
  const chunks = computeDirtyEIDChunks(current, desired);
  const elapsedMs = performance.now() - startedAt;

  assert.ok(elapsedMs < 20, `expected < 20ms dirty chunk compute, got ${elapsedMs}ms`);
  assert.equal(chunks.length, 4);
  assert.deepEqual(
    chunks.map((c) => ({ startSlot: c.startSlot, slotCount: c.slotCount, regLen: c.registers.length })),
    [
      { startSlot: 1, slotCount: 3, regLen: 6 },
      { startSlot: 50, slotCount: 1, regLen: 2 },
      { startSlot: 101, slotCount: 20, regLen: 40 },
      { startSlot: 121, slotCount: 5, regLen: 10 },
    ],
  );
  assert.equal(chunks[1].startAddress, REG.EID_BASE + 49 * 2);
});

test('Edge RPC rejects protected RS485 UIDs with zero Modbus traffic', async () => {
  const fake = new FakeModbusConcentrator(1);
  const exec = new ConcentratorRpcExecutor(fake, { settleMs: 0 });

  for (const uid of PROTECTED_WRITE_UIDS) {
    const res = await exec.execute({
      schemaVersion: '1.0',
      requestId: REQ_ID,
      gatewayId: GW_ID,
      unitId: uid,
      operation: 'WRITE_PARAMS',
      issuedAt: 1000,
      expireAt: Date.now() + 10000,
      params: { rfTxPower: 18 },
    });
    assert.equal(res.status, 'REJECTED');
    assert.equal(res.errorCode, 'CONCENTRATOR_UID_PROTECTED');
  }

  const modbusIdPatch = await exec.execute({
    schemaVersion: '1.0',
    requestId: REQ_ID,
    gatewayId: GW_ID,
    unitId: 1,
    operation: 'WRITE_PARAMS',
    issuedAt: 1000,
    expireAt: Date.now() + 10000,
    params: { modbusId: 31 },
  });
  assert.equal(modbusIdPatch.status, 'REJECTED');
  assert.equal(modbusIdPatch.errorCode, 'CONCENTRATOR_UID_PROTECTED');
  assert.equal(fake.writes.length, 0);
});

test('READ_STATUS decodes params, active EIDs, signed temps & RSSI', async () => {
  const fake = new FakeModbusConcentrator(1);
  setEidOnFake(fake, 1, 1, 33661717);
  setEidOnFake(fake, 1, 50, 33661750);
  const inp = fake.input.get(1)!;
  inp.set(REG.SENSOR_TYPE_BASE + 0, 0x35);
  inp.set(REG.VOLTAGE_BASE + 0, 3620);
  inp.set(REG.TEMP_BASE + 0, (-125) & 0xffff); // -12.5 C
  inp.set(REG.RSSI_BASE + 0, (-78) & 0xffff); // -78 dBm
  // Channel 50 has EID > 0 but sensorType == 0 -> offline

  const exec = new ConcentratorRpcExecutor(fake, { settleMs: 0 });
  const res = await exec.execute({
    schemaVersion: '1.0',
    requestId: REQ_ID,
    gatewayId: GW_ID,
    unitId: 1,
    operation: 'READ_STATUS',
    issuedAt: 1000,
    expireAt: Date.now() + 10000,
  });

  assert.equal(res.status, 'OK');
  assert.equal(res.params?.rfFrequency, 470);
  assert.equal(res.channels?.length, 2);
  assert.deepEqual(res.channels?.[0], {
    channel: 1,
    eid: 33661717,
    online: true,
    sensorType: 0x35,
    sensorModel: 'PT100/无线测温',
    temperatureC: -12.5,
    voltageMv: 3620,
    rssiDbm: -78,
  });
  assert.deepEqual(res.channels?.[1], {
    channel: 50,
    eid: 33661750,
    online: false,
    sensorType: 0,
    sensorModel: '离线',
    temperatureC: null,
    voltageMv: null,
    rssiDbm: null,
  });
});

test('WRITE_PARAMS writes modbusId last, commits 0x9C41=6, settles & verifies readback from new UID', async () => {
  const fake = new FakeModbusConcentrator(1);
  let slept = 0;
  const exec = new ConcentratorRpcExecutor(fake, {
    settleMs: 300,
    sleep: async (ms) => {
      slept += ms;
    },
  });

  const res = await exec.execute({
    schemaVersion: '1.0',
    requestId: REQ_ID,
    gatewayId: GW_ID,
    unitId: 1,
    operation: 'WRITE_PARAMS',
    issuedAt: 1000,
    expireAt: Date.now() + 10000,
    params: { modbusId: 15, rfTxPower: 25 },
  });

  assert.equal(res.status, 'OK');
  assert.equal(slept, 300);
  assert.equal(res.writtenCount, 2);
  assert.equal(res.params?.modbusId, 15);
  assert.equal(res.params?.rfTxPower, 25);
  assert.deepEqual(fake.writes, [
    { unitId: 1, address: 0x9d6d, values: [25] },
    { unitId: 1, address: 0x9c43, values: [15] },
    { unitId: 15, address: 0x9c41, values: [6] },
  ]);
});

test('WRITE_EIDS rejects duplicates, uses FC16 batching + 0x9C41=8 commit, and reads back verified slots', async () => {
  const fake = new FakeModbusConcentrator(1);
  setEidOnFake(fake, 1, 1, 11111111);

  const exec = new ConcentratorRpcExecutor(fake, { settleMs: 0 });
  const dupSlots = emptySlots();
  dupSlots[0].eid = 22222222;
  dupSlots[5].eid = 22222222;

  const dupRes = await exec.execute({
    schemaVersion: '1.0',
    requestId: REQ_ID,
    gatewayId: GW_ID,
    unitId: 1,
    operation: 'WRITE_EIDS',
    issuedAt: 1000,
    expireAt: Date.now() + 10000,
    slots: dupSlots,
  });
  assert.equal(dupRes.status, 'REJECTED');
  assert.equal(dupRes.errorCode, 'CONCENTRATOR_EID_DUPLICATE');
  assert.equal(fake.writes.length, 0);

  const nextSlots = emptySlots();
  nextSlots[0].eid = 11111111; // unchanged
  nextSlots[1].eid = 33661717;
  nextSlots[2].eid = 33661718;

  const okRes = await exec.execute({
    schemaVersion: '1.0',
    requestId: REQ_ID,
    gatewayId: GW_ID,
    unitId: 1,
    operation: 'WRITE_EIDS',
    issuedAt: 1000,
    expireAt: Date.now() + 10000,
    slots: nextSlots,
  });
  assert.equal(okRes.status, 'OK');
  assert.equal(okRes.updatedSlots, 2);
  assert.equal(okRes.chunkCount, 1);
  assert.equal(okRes.slots?.[1].eid, 33661717);
  assert.equal(okRes.slots?.[2].eid, 33661718);
  assert.equal(fake.writes.length, 2); // 1x FC16 chunk + 1x FC6 0x9C41=8
  assert.equal(fake.writes[0].address, REG.EID_BASE + 2);
  assert.equal(fake.writes[0].values.length, 4);
  assert.deepEqual(fake.writes[1], { unitId: 1, address: REG.COMMIT, values: [REG.COMMIT_WRITE_EIDS] });
});

test('MQTT envelope helper rejects gatewayId mismatch, expires stale RPC, and serializes RS485 access', async () => {
  const fake = new FakeModbusConcentrator(1);
  fake.latencyMs = 2;
  const exec = new ConcentratorRpcExecutor(fake, { settleMs: 0 });

  const req: ConcentratorRPCRequest = {
    schemaVersion: '1.0',
    requestId: REQ_ID,
    gatewayId: GW_ID,
    unitId: 1,
    operation: 'READ_EIDS',
    issuedAt: 1000,
    expireAt: Date.now() + 10000,
  };

  const mismatch = await handleMqttRpcMessage(exec, 'hvac/v1/018f3d00-2000-7000-8000-000000000009/down/concentrator', JSON.stringify(req));
  assert.equal(mismatch, null);

  const expired = await handleMqttRpcMessage(
    exec,
    `hvac/v1/${GW_ID}/down/concentrator`,
    JSON.stringify({ ...req, expireAt: 1 }),
  );
  assert.equal(expired?.reply.status, 'FAILED');
  assert.equal(expired?.reply.errorCode, 'CONCENTRATOR_RPC_EXPIRED');

  const [r1, r2] = await Promise.all([
    handleMqttRpcMessage(exec, `hvac/v1/${GW_ID}/down/concentrator`, JSON.stringify(req)),
    handleMqttRpcMessage(exec, `hvac/v1/${GW_ID}/down/concentrator`, JSON.stringify(req)),
  ]);
  assert.equal(r1?.reply.status, 'OK');
  assert.equal(r2?.reply.status, 'OK');
  assert.equal(fake.maxConcurrentCalls, 1);
});

test('Edge RPC rejects NaN/Infinity/missing expireAt, expireAt <= issuedAt, and invalid issuedAt with zero Modbus calls', async () => {
  const fake = new FakeModbusConcentrator(1);
  let now = 5000;
  const exec = new ConcentratorRpcExecutor(fake, { settleMs: 0, now: () => now });
  const base: ConcentratorRPCRequest = {
    schemaVersion: '1.0',
    requestId: REQ_ID,
    gatewayId: GW_ID,
    unitId: 1,
    operation: 'READ_EIDS',
    issuedAt: 4000,
    expireAt: 6000,
  };

  const omittedExpire = { ...base };
  Reflect.deleteProperty(omittedExpire, 'expireAt');
  for (const expireAt of [Number.NaN, Number.POSITIVE_INFINITY, 4000, 3999, 5000]) {
    const res = await exec.execute({ ...base, expireAt });
    assert.equal(res.status, 'FAILED');
    assert.equal(res.errorCode, 'CONCENTRATOR_RPC_EXPIRED');
  }
  const missingExpireRes = await exec.execute(omittedExpire);
  assert.equal(missingExpireRes.status, 'FAILED');
  assert.equal(missingExpireRes.errorCode, 'CONCENTRATOR_RPC_EXPIRED');

  const badIssued = await exec.execute({ ...base, issuedAt: Number.NaN });
  assert.equal(badIssued.status, 'REJECTED');
  assert.equal(badIssued.errorCode, 'CONCENTRATOR_REQUEST_INVALID');
  assert.equal(fake.maxConcurrentCalls, 0);
});

test('ConcentratorRpcExecutor shares Rs485BusLock with higher-priority callers', async () => {
  const fake = new FakeModbusConcentrator(1);
  const busLock = new Rs485BusLock();
  const sharedExec = new ConcentratorRpcExecutor(fake, { settleMs: 0, now: () => 5000, busLock });
  const req: ConcentratorRPCRequest = {
    schemaVersion: '1.0',
    requestId: REQ_ID,
    gatewayId: GW_ID,
    unitId: 1,
    operation: 'READ_EIDS',
    issuedAt: 4000,
    expireAt: 6000,
  };
  const events: string[] = [];
  let blockerEntered!: () => void;
  const blockerStarted = new Promise<void>((resolve) => {
    blockerEntered = resolve;
  });
  let releaseBlocker!: () => void;
  const blocker = busLock.runExclusive(0, () => new Promise<void>((resolve) => {
    events.push('blocker-start');
    blockerEntered();
    releaseBlocker = () => {
      events.push('blocker-end');
      resolve();
    };
  }));
  await blockerStarted;
  const queuedRpc = sharedExec.execute(req).then((res) => {
    events.push(`rpc-${res.status}`);
    return res;
  });
  const urgentTask = busLock.runExclusive(BUS_PRIORITY_PULSE_RESET, async () => {
    events.push('urgent-pulse-reset');
  });
  releaseBlocker();
  await Promise.all([blocker, queuedRpc, urgentTask]);
  assert.deepEqual(events, ['blocker-start', 'blocker-end', 'urgent-pulse-reset', 'rpc-OK']);
});
