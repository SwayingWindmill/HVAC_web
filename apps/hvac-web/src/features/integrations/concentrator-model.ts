import {
  PlatformApiError,
  type ConcentratorAuditItem,
  type ConcentratorChannelReading,
  type Device,
} from '../../api/generated/platformGateway.gen.ts';
import type { GovernanceStatusPresentation } from './governance-shared-model.ts';

export const PROTECTED_RS485_UIDS = [2, 3, 4, 5, 10, 31] as const;

const PROTECTED_RS485_UID_SET = new Set<number>(PROTECTED_RS485_UIDS);

export function isProtectedConcentratorUid(uid: number): boolean {
  return Number.isInteger(uid) && PROTECTED_RS485_UID_SET.has(uid);
}

export function describeProtectedConcentratorUid(uid: number): string {
  return `从站地址 ${uid} 为冷机 / 总电表 / 冷冻泵变频器等受保护 RS485 设备地址（受保护站号：2、3、4、5、10、31），已拦截集中器访问以防总线冲突。`;
}

export const CONCENTRATOR_BATTERY_CRITICAL_MV = 2450;
export const CONCENTRATOR_BATTERY_WARNING_MV = 2600;
export const MAX_CONCENTRATOR_EID = 4_294_967_295;

export interface ConcentratorSelectOption {
  readonly value: number;
  readonly label: string;
}

export const CONCENTRATOR_BAUD_RATE_OPTIONS: readonly ConcentratorSelectOption[] = [
  { value: 0, label: '1200 bps' },
  { value: 1, label: '2400 bps' },
  { value: 2, label: '4800 bps' },
  { value: 3, label: '9600 bps' },
  { value: 4, label: '19200 bps' },
  { value: 5, label: '38400 bps' },
] as const;

export const CONCENTRATOR_BANDWIDTH_OPTIONS: readonly ConcentratorSelectOption[] = [
  { value: 0, label: '125 kHz' },
  { value: 1, label: '250 kHz' },
  { value: 2, label: '500 kHz' },
] as const;

export const CONCENTRATOR_TIME_MARK_OPTIONS: readonly ConcentratorSelectOption[] = [
  { value: 0, label: '0 · 关闭时间戳' },
  { value: 1, label: '1 · 标准帧时间戳' },
  { value: 2, label: '2 · 扩展时间戳' },
  { value: 3, label: '3 · 调试模式时间戳' },
] as const;

export const CONCENTRATOR_RSSI_OUT_OPTIONS: readonly ConcentratorSelectOption[] = [
  { value: 0, label: '0 · 关闭随帧信号强度输出' },
  { value: 1, label: '1 · 开启随帧信号强度输出' },
] as const;

function lookupOptionLabel(
  options: readonly ConcentratorSelectOption[],
  value: number,
): string {
  return options.find((item) => item.value === value)?.label ?? String(value);
}

export function formatConcentratorBaudRate(code: number): string {
  return lookupOptionLabel(CONCENTRATOR_BAUD_RATE_OPTIONS, code);
}

export function formatConcentratorBandwidth(code: number): string {
  return lookupOptionLabel(CONCENTRATOR_BANDWIDTH_OPTIONS, code);
}

export function formatConcentratorTimeMark(code: number): string {
  return lookupOptionLabel(CONCENTRATOR_TIME_MARK_OPTIONS, code);
}

export function formatConcentratorRssiOut(code: number): string {
  return lookupOptionLabel(CONCENTRATOR_RSSI_OUT_OPTIONS, code);
}

export function formatConcentratorEidDisplay(eid: number): string {
  if (eid <= 0) return '未绑定 (0)';
  return `${eid} (0x${eid.toString(16).toUpperCase().padStart(8, '0')})`;
}

export type ConcentratorBatteryHealth =
  | 'NORMAL'
  | 'LOW_VOLTAGE'
  | 'CRITICAL_LOW'
  | 'UNKNOWN';

export interface ConcentratorBatteryClassification {
  readonly health: ConcentratorBatteryHealth;
  readonly status: GovernanceStatusPresentation;
}

export function classifyConcentratorBattery(
  voltageMv: number | null,
): ConcentratorBatteryClassification {
  if (voltageMv === null) {
    return {
      health: 'UNKNOWN',
      status: {
        level: 'NOTICE',
        shape: '■',
        tone: 'neutral',
        label: '无电压读数',
      },
    };
  }
  if (voltageMv < CONCENTRATOR_BATTERY_CRITICAL_MV) {
    return {
      health: 'CRITICAL_LOW',
      status: {
        level: 'CRITICAL',
        shape: '◆',
        tone: 'danger',
        label: '严重欠压 (<2450 mV)',
      },
    };
  }
  if (voltageMv < CONCENTRATOR_BATTERY_WARNING_MV) {
    return {
      health: 'LOW_VOLTAGE',
      status: {
        level: 'WARNING',
        shape: '▲',
        tone: 'warning',
        label: '低电压 (<2600 mV)',
      },
    };
  }
  return {
    health: 'NORMAL',
    status: {
      level: 'NORMAL',
      shape: '○',
      tone: 'success',
      label: '电量正常',
    },
  };
}

export interface ConcentratorChannelRow {
  readonly channel: number;
  readonly eid: number;
  readonly online: boolean;
  readonly sensorType: number;
  readonly sensorModel: string;
  readonly temperatureC: number | null;
  readonly voltageMv: number | null;
  readonly rssiDbm: number | null;
  readonly batteryHealth: ConcentratorBatteryHealth;
  readonly batteryStatus: GovernanceStatusPresentation;
  readonly linkStatus: GovernanceStatusPresentation;
  readonly overallStatus: GovernanceStatusPresentation;
}

export function buildConcentratorChannelRow(
  reading: ConcentratorChannelReading,
): ConcentratorChannelRow {
  const battery = classifyConcentratorBattery(reading.voltageMv);
  const linkStatus: GovernanceStatusPresentation = reading.online
    ? {
        level: 'NORMAL',
        shape: '○',
        tone: 'success',
        label: '在线',
      }
    : {
        level: 'CRITICAL',
        shape: '◆',
        tone: 'danger',
        label: '离线',
      };

  let overallStatus: GovernanceStatusPresentation = linkStatus;
  if (reading.online) {
    overallStatus =
      battery.health === 'CRITICAL_LOW' || battery.health === 'LOW_VOLTAGE'
        ? battery.status
        : linkStatus;
  }

  return {
    ...reading,
    batteryHealth: battery.health,
    batteryStatus: battery.status,
    linkStatus,
    overallStatus,
  };
}

export interface ConcentratorSummary {
  readonly totalChannels: number;
  readonly boundChannels: number;
  readonly onlineCount: number;
  readonly offlineCount: number;
  readonly lowBatteryCount: number;
  readonly criticalBatteryCount: number;
  readonly avgTemperatureC: number | null;
}

export function buildConcentratorSummary(
  rows: readonly ConcentratorChannelRow[],
): ConcentratorSummary {
  let boundChannels = 0;
  let onlineCount = 0;
  let offlineCount = 0;
  let lowBatteryCount = 0;
  let criticalBatteryCount = 0;
  let tempSum = 0;
  let tempCount = 0;

  for (const row of rows) {
    if (row.eid > 0) boundChannels += 1;
    if (row.online) {
      onlineCount += 1;
    } else {
      offlineCount += 1;
    }
    if (row.voltageMv !== null && row.voltageMv < CONCENTRATOR_BATTERY_WARNING_MV) {
      lowBatteryCount += 1;
      if (row.voltageMv < CONCENTRATOR_BATTERY_CRITICAL_MV) {
        criticalBatteryCount += 1;
      }
    }
    if (row.online && row.temperatureC !== null) {
      tempSum += row.temperatureC;
      tempCount += 1;
    }
  }

  return {
    totalChannels: rows.length,
    boundChannels,
    onlineCount,
    offlineCount,
    lowBatteryCount,
    criticalBatteryCount,
    avgTemperatureC: tempCount > 0 ? Number((tempSum / tempCount).toFixed(1)) : null,
  };
}

export type ConcentratorChannelFilter = 'ALL' | 'ONLINE' | 'OFFLINE' | 'LOW_BATTERY';

export function filterConcentratorChannels(
  rows: readonly ConcentratorChannelRow[],
  filter: ConcentratorChannelFilter,
  query: string,
): readonly ConcentratorChannelRow[] {
  const normalizedQuery = query.trim().toLowerCase();
  return rows.filter((row) => {
    if (filter === 'ONLINE' && !row.online) return false;
    if (filter === 'OFFLINE' && row.online) return false;
    if (
      filter === 'LOW_BATTERY' &&
      (row.voltageMv === null || row.voltageMv >= CONCENTRATOR_BATTERY_WARNING_MV)
    ) {
      return false;
    }
    if (!normalizedQuery) return true;
    const channelText = String(row.channel);
    const channelTag = `ch${row.channel}`;
    const eidDec = String(row.eid);
    const eidHex = row.eid.toString(16).toLowerCase();
    const eidPaddedHex = eidHex.padStart(8, '0');
    const model = row.sensorModel.toLowerCase();
    return (
      channelText.includes(normalizedQuery) ||
      channelTag.includes(normalizedQuery) ||
      eidDec.includes(normalizedQuery) ||
      eidHex.includes(normalizedQuery) ||
      eidPaddedHex.includes(normalizedQuery) ||
      `0x${eidHex}`.includes(normalizedQuery) ||
      `0x${eidPaddedHex}`.includes(normalizedQuery) ||
      model.includes(normalizedQuery)
    );
  });
}

export const CONCENTRATOR_AUDIT_OPERATION_LABELS: Readonly<
  Record<ConcentratorAuditItem['operation'], string>
> = {
  WRITE_PARAMS: '串口与射频参数写入',
  WRITE_EIDS: 'EID 槽位映射写入',
};

export const CONCENTRATOR_AUDIT_OUTCOME_PRESENTATION: Readonly<
  Record<ConcentratorAuditItem['outcome'], GovernanceStatusPresentation>
> = {
  APPLIED: {
    level: 'NORMAL',
    shape: '○',
    tone: 'success',
    label: '已写入 Flash',
  },
  REJECTED: {
    level: 'WARNING',
    shape: '▲',
    tone: 'warning',
    label: '校验拒绝',
  },
  FAILED: {
    level: 'CRITICAL',
    shape: '◆',
    tone: 'danger',
    label: '下发失败',
  },
  TIMEOUT: {
    level: 'CRITICAL',
    shape: '◆',
    tone: 'danger',
    label: '网关超时',
  },
  STAGED_SHADOW: {
    level: 'WARNING',
    shape: '▲',
    tone: 'warning',
    label: '已暂存待同步',
  },
};

export function mergeConcentratorAudits(
  ...groups: ReadonlyArray<readonly ConcentratorAuditItem[] | undefined>
): readonly ConcentratorAuditItem[] {
  const byId = new Map<string, ConcentratorAuditItem>();
  for (const group of groups) {
    if (!group) continue;
    for (const item of group) {
      if (!byId.has(item.id)) {
        byId.set(item.id, item);
      }
    }
  }
  return Array.from(byId.values())
    .sort((left, right) => Date.parse(right.occurredAt) - Date.parse(left.occurredAt))
    .slice(0, 20);
}

const CONCENTRATOR_UID_PATTERN = /(?:^|[^a-z0-9])uid[\s_-]*(\d{1,3})(?:[^0-9]|$)/i;

export function inferDefaultConcentratorUid(
  devices: readonly Pick<Device, 'deviceType' | 'code' | 'displayName'>[],
): number {
  for (const device of devices) {
    if (device.deviceType !== 'TEMPERATURE_MODULE') continue;
    for (const candidate of [device.code, device.displayName]) {
      const match = CONCENTRATOR_UID_PATTERN.exec(candidate);
      if (!match) continue;
      const uid = Number(match[1]);
      if (Number.isInteger(uid) && uid >= 1 && uid <= 247 && !isProtectedConcentratorUid(uid)) {
        return uid;
      }
    }
  }
  return 1;
}

export function describeConcentratorError(
  error: unknown,
  fallback = '集中器状态或配置暂时无法读取，请稍后重试',
): string {
  if (error instanceof PlatformApiError) {
    switch (error.problem.code) {
      case 'CONCENTRATOR_UID_PROTECTED':
        return '所选从站地址属于受保护的现场 RS485 设备（冷机 / 总电表 / 冷冻泵变频器），已拒绝本次读写';
      case 'CONCENTRATOR_EID_DUPLICATE':
        return '存在重复的非零探头 EID，请修正冲突槽位后再提交写入 Flash';
      case 'CONCENTRATOR_EIDS_INVALID':
        return '提交的 200 槽 EID 映射格式或数量无效，请核对后重试';
      case 'CONCENTRATOR_PARAMS_INVALID':
        return '提交的串口与射频参数超出有效范围，请核对后重试';
      case 'CONCENTRATOR_REASON_REQUIRED':
        return '请填写本次集中器变更原因后再确认下发';
      case 'CONCENTRATOR_REQUEST_INVALID':
        return '请求参数或 EID 槽位格式无效，请检查从站地址、数值范围与变更原因后重试';
      case 'CREDENTIAL_FORBIDDEN':
        return '当前账号缺少设备读取或集中器下发权限（需要 device.read / device.write）';
      case 'GATEWAY_NOT_FOUND':
        return '未找到所选现场网关，请确认网关登记状态';
      case 'GATEWAY_REVOKED':
        return '该现场网关接入凭据已停用，请先在「现场网关」重新签发接入码并激活';
      case 'CONCENTRATOR_RPC_TIMEOUT':
        return '无线集中器未及时应答或 Modbus 读取超时，请检查 RS485 接线与集中器供电';
      case 'CONCENTRATOR_TRANSPORT_UNAVAILABLE':
        return '现场网关远程连接通道暂未就绪，请先到「现场网关」确认网关状态';
      case 'CONCENTRATOR_EDGE_FAILED':
        return '现场网关转发集中器请求失败，请检查现场网关运行状态与串口通信后重试';
      case 'CONCENTRATOR_SERVICE_UNAVAILABLE':
        return '平台集中器管理服务暂时不可用，请稍后重试';
      default:
        break;
    }
    if (error.problem.status === 403) {
      return '当前账号缺少设备读取或集中器下发权限（需要 device.read / device.write）';
    }
    if (error.problem.status === 404) {
      return '未找到所选现场网关，请确认网关登记状态';
    }
    if (error.problem.status === 409) {
      return '现场网关或集中器从站状态存在冲突，请刷新后重试';
    }
    if (error.problem.status === 422) {
      return '提交的参数、槽位映射或变更原因未通过校验，请核对后重试';
    }
    if (error.problem.status === 429) {
      return '集中器指令请求过于频繁，请稍候数秒再试';
    }
    if (error.problem.status === 502 || error.problem.status === 503 || error.problem.status === 504) {
      return '网关转发集中器请求失败或通道暂不可用，请稍后重试';
    }
  }
  return fallback;
}
