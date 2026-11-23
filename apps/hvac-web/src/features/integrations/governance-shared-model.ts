import {
  PlatformApiError,
  type Asset,
  type TelemetryPoint,
} from '../../api/generated/platformGateway.gen.ts';
import type { WorkOrder } from '../../api/work-orders.ts';
import type {
  AssetsBindingState,
  AssetsPointView,
} from '../assets/model.ts';

export type IntegrationsTabView = 'gateways' | 'concentrators' | 'meters' | 'anomalies' | 'sensors' | 'ota';

export type GovernanceBadgeTone = 'danger' | 'warning' | 'neutral' | 'success';
export type GovernanceShape = '◆' | '▲' | '■' | '○';

export interface GovernanceStatusPresentation {
  readonly level: 'CRITICAL' | 'WARNING' | 'NOTICE' | 'NORMAL';
  readonly shape: GovernanceShape;
  readonly tone: GovernanceBadgeTone;
  readonly label: string;
}

export interface MeterTrendJumpSearch {
  readonly site: string;
  readonly points?: string;
  readonly window: 'custom';
  readonly from: string;
  readonly to: string;
  readonly granularity: 'FIVE_MINUTE';
}

export const QUALITY_REASON_EXPLANATIONS: Readonly<Record<string, string>> = Object.freeze({
  OUT_OF_RANGE: '读数超出物理量程范围',
  SPIKE: '读数变化超出单步允许跳变阈值',
  SENTINEL: '命中仪表断线或故障哨兵值',
  FLATLINE: '读数长时间恒定未变化，疑似卡值',
  DERIVED_INPUT: '上游输入读数存在异常',
  LOW_CONFIDENCE: '小温差时测量不确定度偏高',
  PHYSICS_WET_BULB_DRY: '湿球温度高于干球温度，违背热力学关系',
  PHYSICS_WET_BULB_CONDENSER: '冷凝器出水温度低于室外湿球温度，违背物理极限',
  PHYSICS_POWER: '实测功率超出设备铭牌额定功率合理范围',
  PHYSICS_COP: '实测能效比超出卡诺极限或合理区间',
  PHYSICS_CHW_DIRECTION: '冷冻水供回水温差方向异常（供水高于回水）',
  PHYSICS_CW_DIRECTION: '冷却水进出水温差方向异常（出水低于进水）',
  PHYSICS_HEAT_BALANCE: '冷机热平衡偏差超出允许范围',
  PROBE_OFFSET: '仅水泵运行稳态下检测到供回水探头零点偏差',
});

export function explainQualityReason(code: string): string {
  return QUALITY_REASON_EXPLANATIONS[code] ?? '读数存在质量异常，建议现场核查';
}

export function cleanEndpointDisplayName(displayName: string): string {
  return displayName.replace(/\s*通信端点$/u, '').trim() || displayName;
}

export function primaryBoundAsset(binding: AssetsBindingState): Asset | null {
  if (binding.state === 'bound') return binding.asset;
  if (binding.state === 'multi-bound') return binding.bindings[0]?.asset ?? null;
  return null;
}

export function collectPointReasonCodes(pointView: AssetsPointView | null): string[] {
  if (!pointView) return [];
  const merged = new Set<string>();
  for (const code of pointView.qualityReasons) {
    merged.add(code);
  }
  for (const code of pointView.health?.reasons ?? []) {
    merged.add(code);
  }
  return [...merged];
}

export function formatHourSlotLabel(ms: number, timezone: string): string {
  try {
    return new Intl.DateTimeFormat('zh-CN', {
      timeZone: timezone,
      hour: '2-digit',
      minute: '2-digit',
      hour12: false,
    }).format(new Date(ms));
  } catch {
    return new Date(ms).toISOString().slice(11, 16);
  }
}

export function formatSlotDateTime(msOrIso: number | string, timezone: string): string {
  const ms = typeof msOrIso === 'number' ? msOrIso : Date.parse(msOrIso);
  if (!Number.isFinite(ms)) return '—';
  try {
    return new Intl.DateTimeFormat('zh-CN', {
      timeZone: timezone,
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      hour12: false,
    }).format(new Date(ms));
  } catch {
    return new Date(ms).toISOString().slice(5, 16).replace('T', ' ');
  }
}

const PROTOCOL_LABELS: Readonly<Record<string, string>> = Object.freeze({
  MODBUS_TCP: 'Modbus TCP',
  MODBUS_RTU: 'Modbus RTU',
  MQTT: 'MQTT',
  BACNET_IP: 'BACnet/IP',
  OPC_UA: 'OPC-UA',
  HTTP: 'HTTP',
  COPIES: '共享通道',
  PROVISIONAL: '临时换算',
});

function formatProtocolLabel(raw: string): string {
  return PROTOCOL_LABELS[raw.toUpperCase()] ?? raw;
}

export function formatSourceChannel(metadata: Readonly<Record<string, unknown>>): string {
  const rawProtocol =
    typeof metadata.protocol === 'string' && metadata.protocol.trim()
      ? metadata.protocol.trim()
      : null;
  const protocol = rawProtocol ? formatProtocolLabel(rawProtocol) : null;
  const addressCandidate =
    (typeof metadata.address === 'string' && metadata.address.trim()) ||
    (typeof metadata.channel === 'string' && metadata.channel.trim()) ||
    (typeof metadata.register === 'string' && metadata.register.trim()) ||
    (typeof metadata.register === 'number' ? `寄存器 ${metadata.register}` : null);

  if (protocol && addressCandidate) return `${protocol} · ${addressCandidate}`;
  if (addressCandidate) return addressCandidate;
  if (protocol) return protocol;
  return '未登记通道';
}

export function formatSampleInterval(sampleIntervalMs: number): string {
  if (sampleIntervalMs % 60_000 === 0) {
    return `每 ${sampleIntervalMs / 60_000} 分钟`;
  }
  if (sampleIntervalMs >= 1000) {
    return `每 ${Math.round(sampleIntervalMs / 1000)} 秒`;
  }
  return `${sampleIntervalMs} ms`;
}

export function formatCounterPolicy(point: TelemetryPoint | null): string {
  if (!point || point.pointType !== 'COUNTER') {
    return '瞬时量监测（无累计脉冲）';
  }
  if (point.counterDecreaseMode === 'ROLLOVER') {
    const modulus = point.counterRolloverModulus
      ? `（模数 ${new Intl.NumberFormat('en-US').format(point.counterRolloverModulus)}）`
      : '';
    return `满量程回零${modulus}`;
  }
  if (point.counterDecreaseMode === 'RESET_TO_ZERO') {
    return '清零重置后继续累计';
  }
  if (point.counterDecreaseMode === 'INVALID') {
    return '读数回退判为无效';
  }
  return '连续单调累计';
}

const OPEN_WORK_ORDER_STATUSES = new Set<WorkOrder['status']>([
  'DRAFT',
  'OPEN',
  'IN_PROGRESS',
  'BLOCKED',
]);

export function findOpenExternalWorkOrder(
  workOrders: readonly WorkOrder[] | undefined,
  resourceId: string,
): WorkOrder | null {
  if (!workOrders) return null;
  for (const order of workOrders) {
    if (!OPEN_WORK_ORDER_STATUSES.has(order.status)) continue;
    if (
      order.sourceReferences.some(
        (ref) => ref.domain === 'EXTERNAL' && ref.resourceId === resourceId,
      )
    ) {
      return order;
    }
  }
  return null;
}

export function describeIntegrationsError(error: unknown, fallback = '数据暂时无法读取，请稍后重试'): string {
  if (error instanceof PlatformApiError) {
    if (error.problem.status === 403) {
      return '当前账号没有读取该站点接入或能耗统计数据的权限';
    }
    if (error.problem.status === 404) {
      return '未找到所选站点的资产或接入登记数据';
    }
    return '平台服务暂时无法返回接入核对数据，请稍后重试';
  }
  return fallback;
}
