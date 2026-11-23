import type {
  ConcentratorEidSlot,
  ConcentratorParams,
  ConcentratorParamsPatch,
} from '../../api/generated/platformGateway.gen.ts';
import {
  CONCENTRATOR_BANDWIDTH_OPTIONS,
  CONCENTRATOR_BAUD_RATE_OPTIONS,
  CONCENTRATOR_RSSI_OUT_OPTIONS,
  CONCENTRATOR_TIME_MARK_OPTIONS,
  MAX_CONCENTRATOR_EID,
  describeProtectedConcentratorUid,
  formatConcentratorBandwidth,
  formatConcentratorBaudRate,
  formatConcentratorRssiOut,
  formatConcentratorTimeMark,
  isProtectedConcentratorUid,
  type ConcentratorSelectOption,
} from './concentrator-model.ts';

export interface ConcentratorParamFieldSpec {
  readonly key: keyof ConcentratorParams;
  readonly label: string;
  readonly unit: string;
  readonly min: number;
  readonly max: number;
  readonly group: 'serial' | 'rf';
  readonly description: string;
  readonly options?: readonly ConcentratorSelectOption[];
}

export const CONCENTRATOR_PARAM_SPECS: readonly ConcentratorParamFieldSpec[] = [
  {
    key: 'modbusId',
    label: 'Modbus 从站地址',
    unit: '',
    min: 1,
    max: 247,
    group: 'serial',
    description: '避开 2/3/4/5/10/31 受保护 RS485 设备地址，默认使用 1',
  },
  {
    key: 'baudRateCode',
    label: '串口波特率代码',
    unit: '',
    min: 0,
    max: 5,
    group: 'serial',
    description: 'RS485 串口波特率挡位，须与现场采集总线一致',
    options: CONCENTRATOR_BAUD_RATE_OPTIONS,
  },
  {
    key: 'offLineTime',
    label: '探头离线判定时长',
    unit: '秒',
    min: 1,
    max: 65535,
    group: 'serial',
    description: '超过该时长未收到射频报文则判定对应探头离线',
  },
  {
    key: 'dataUplinkCycle',
    label: '数据主动上报周期',
    unit: '秒',
    min: 1,
    max: 65535,
    group: 'serial',
    description: '集中器周期采集并对外更新通道读数的基准间隔',
  },
  {
    key: 'localNet',
    label: '本地网络号',
    unit: '',
    min: 0,
    max: 65535,
    group: 'serial',
    description: '用于区分同一区域多台集中器的 LoRa 网络分组标识',
  },
  {
    key: 'timeMark',
    label: '时间戳标志',
    unit: '',
    min: 0,
    max: 3,
    group: 'serial',
    description: '控制通道报文是否附带帧时间戳标记',
    options: CONCENTRATOR_TIME_MARK_OPTIONS,
  },
  {
    key: 'rfTxPower',
    label: '射频发射功率',
    unit: 'dBm',
    min: 0,
    max: 30,
    group: 'rf',
    description: '无线下行发射功率（0–30 dBm），过低可能减小覆盖半径',
  },
  {
    key: 'rfFrequency',
    label: '中心频率',
    unit: 'MHz',
    min: 100,
    max: 1000,
    group: 'rf',
    description: '无线载波频率（常见 433 MHz / 470 MHz），须与探头一致',
  },
  {
    key: 'rfBandWidth',
    label: '射频带宽',
    unit: '',
    min: 0,
    max: 2,
    group: 'rf',
    description: '信道带宽越宽速率越高，但抗干扰与灵敏度相应下降',
    options: CONCENTRATOR_BANDWIDTH_OPTIONS,
  },
  {
    key: 'rfSf',
    label: '扩频因子 (SF)',
    unit: '',
    min: 5,
    max: 12,
    group: 'rf',
    description: 'LoRa 扩频因子（5–12），越大穿透越强但空中耗时越长',
  },
  {
    key: 'rfLeadCode',
    label: '前导码长度',
    unit: '',
    min: 1,
    max: 65535,
    group: 'rf',
    description: '无线帧前导同步长度',
  },
  {
    key: 'txTimeout',
    label: '发送超时',
    unit: '毫秒',
    min: 1,
    max: 65535,
    group: 'rf',
    description: '射频单帧发送超时保护阈值',
  },
  {
    key: 'rxTimeout',
    label: '接收超时',
    unit: '毫秒',
    min: 1,
    max: 65535,
    group: 'rf',
    description: '射频应答接收窗口超时阈值',
  },
  {
    key: 'rssiOut',
    label: '输出信号强度',
    unit: '',
    min: 0,
    max: 1,
    group: 'rf',
    description: '是否在传感器通道结构体内附带 RSSI 信号强度字段',
    options: CONCENTRATOR_RSSI_OUT_OPTIONS,
  },
] as const;

export function formatConcentratorParamValue(
  key: keyof ConcentratorParams,
  value: number,
): string {
  switch (key) {
    case 'baudRateCode':
      return formatConcentratorBaudRate(value);
    case 'rfBandWidth':
      return formatConcentratorBandwidth(value);
    case 'timeMark':
      return formatConcentratorTimeMark(value);
    case 'rssiOut':
      return formatConcentratorRssiOut(value);
    case 'rfTxPower':
      return `${value} dBm`;
    case 'rfFrequency':
      return `${value} MHz`;
    case 'offLineTime':
    case 'dataUplinkCycle':
      return `${value} 秒`;
    case 'txTimeout':
    case 'rxTimeout':
      return `${value} ms`;
    case 'rfSf':
      return `SF${value}`;
    default:
      return String(value);
  }
}

export interface ConcentratorParamsValidation {
  readonly valid: boolean;
  readonly protectedModbusId: boolean;
  readonly errors: Partial<Record<keyof ConcentratorParams, string>>;
}

export function validateConcentratorParamsDraft(
  draft: ConcentratorParams,
): ConcentratorParamsValidation {
  const errors: Partial<Record<keyof ConcentratorParams, string>> = {};
  let valid = true;

  for (const spec of CONCENTRATOR_PARAM_SPECS) {
    const val = draft[spec.key];
    if (!Number.isInteger(val) || val < spec.min || val > spec.max) {
      errors[spec.key] = `请输入 ${spec.min} 到 ${spec.max} 之间的整数`;
      valid = false;
    }
  }

  const protectedModbusId = isProtectedConcentratorUid(draft.modbusId);
  if (protectedModbusId) {
    errors.modbusId = describeProtectedConcentratorUid(draft.modbusId);
    valid = false;
  }

  return {
    valid,
    protectedModbusId,
    errors,
  };
}

export interface ConcentratorParamDiffItem {
  readonly key: keyof ConcentratorParams;
  readonly label: string;
  readonly before: number;
  readonly after: number;
  readonly beforeDisplay: string;
  readonly afterDisplay: string;
}

export interface ConcentratorParamsDiffResult {
  readonly hasChanges: boolean;
  readonly patch: ConcentratorParamsPatch | null;
  readonly changes: readonly ConcentratorParamDiffItem[];
}

export function buildConcentratorParamsDiff(
  original: ConcentratorParams,
  draft: ConcentratorParams,
): ConcentratorParamsDiffResult {
  const patch: Partial<Record<keyof ConcentratorParams, number>> = {};
  const changes: ConcentratorParamDiffItem[] = [];

  for (const spec of CONCENTRATOR_PARAM_SPECS) {
    const before = original[spec.key];
    const after = draft[spec.key];
    if (before === after) continue;
    patch[spec.key] = after;
    changes.push({
      key: spec.key,
      label: spec.label,
      before,
      after,
      beforeDisplay: formatConcentratorParamValue(spec.key, before),
      afterDisplay: formatConcentratorParamValue(spec.key, after),
    });
  }

  if (changes.length === 0) {
    return { hasChanges: false, patch: null, changes: [] };
  }

  return {
    hasChanges: true,
    patch,
    changes,
  };
}

export interface ConcentratorEidConflictResult {
  readonly hasConflicts: boolean;
  readonly duplicatesByEid: ReadonlyMap<number, readonly number[]>;
  readonly conflictingSlotSet: ReadonlySet<number>;
}

export function detectDuplicateNonZeroEids(
  slots: readonly ConcentratorEidSlot[],
): ConcentratorEidConflictResult {
  const byEid = new Map<number, number[]>();
  for (const entry of slots) {
    if (!Number.isInteger(entry.eid) || entry.eid <= 0) continue;
    const list = byEid.get(entry.eid);
    if (list) {
      list.push(entry.slot);
    } else {
      byEid.set(entry.eid, [entry.slot]);
    }
  }

  const duplicatesByEid = new Map<number, readonly number[]>();
  const conflictingSlotSet = new Set<number>();

  for (const [eid, slotNumbers] of byEid) {
    if (slotNumbers.length < 2) continue;
    const sorted = [...slotNumbers].sort((a, b) => a - b);
    duplicatesByEid.set(eid, sorted);
    for (const slotNum of sorted) {
      conflictingSlotSet.add(slotNum);
    }
  }

  return {
    hasConflicts: duplicatesByEid.size > 0,
    duplicatesByEid,
    conflictingSlotSet,
  };
}

export interface ConcentratorEidBatchFillInput {
  readonly startSlot: number;
  readonly count: number;
  readonly startEid: number;
  readonly step: number;
}

export function applyConcentratorEidBatchFill(
  slots: readonly ConcentratorEidSlot[],
  input: ConcentratorEidBatchFillInput,
): readonly ConcentratorEidSlot[] {
  const { startSlot, count, startEid, step } = input;
  if (
    !Number.isInteger(startSlot) ||
    startSlot < 1 ||
    startSlot > 200 ||
    !Number.isInteger(count) ||
    count < 1 ||
    !Number.isInteger(startEid) ||
    startEid < 0 ||
    startEid > MAX_CONCENTRATOR_EID ||
    !Number.isInteger(step) ||
    step < 0
  ) {
    return slots;
  }

  const endSlot = Math.min(200, startSlot + count - 1);
  return slots.map((item) => {
    if (item.slot < startSlot || item.slot > endSlot) return item;
    const offset = item.slot - startSlot;
    const nextEid =
      startEid === 0 ? 0 : Math.min(MAX_CONCENTRATOR_EID, startEid + offset * step);
    return {
      slot: item.slot,
      eid: nextEid,
    };
  });
}

export interface ConcentratorEidDiffItem {
  readonly slot: number;
  readonly beforeEid: number;
  readonly afterEid: number;
}

export function buildConcentratorEidDiff(
  original: readonly ConcentratorEidSlot[],
  draft: readonly ConcentratorEidSlot[],
): readonly ConcentratorEidDiffItem[] {
  const originalBySlot = new Map<number, number>();
  for (const item of original) {
    originalBySlot.set(item.slot, item.eid);
  }
  const diffs: ConcentratorEidDiffItem[] = [];
  for (const item of draft) {
    const beforeEid = originalBySlot.get(item.slot) ?? 0;
    if (beforeEid !== item.eid) {
      diffs.push({
        slot: item.slot,
        beforeEid,
        afterEid: item.eid,
      });
    }
  }
  return diffs.sort((a, b) => a.slot - b.slot);
}
