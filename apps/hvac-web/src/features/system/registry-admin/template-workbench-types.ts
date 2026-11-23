import type { TemplateKind } from '@/api/generated/platformGateway.gen';
import { isPlainRecord } from '@/lib/typed';
import type { TemplateModbusDriverSummary } from './template-model';

export type PayloadEditorMode = 'structured' | 'json';

export type ReleaseDraft = {
  templateKey: string;
  templateKind: TemplateKind;
  payloadJson: string;
  reason: string;
};

export type AssignmentDraft = {
  targetType: TemplateKind;
  targetId: string;
  templateRevisionId: string;
  reason: string;
};

export interface TargetOption {
  readonly value: string;
  readonly label: string;
}

export interface RevisionOption {
  readonly id: string;
  readonly label: string;
  readonly kind: TemplateKind;
}

export const TEMPLATE_KINDS = ['ASSET', 'DEVICE', 'POINT'] as const satisfies readonly TemplateKind[];
export const ASSIGNMENT_FILTER_KINDS = ['ALL', 'ASSET', 'DEVICE', 'POINT'] as const;

export function parseObject(source: string, label: string): Record<string, unknown> {
  const parsed: unknown = JSON.parse(source);
  if (!isPlainRecord(parsed)) {
    throw new Error(`${label}格式有误，请填写键值对形式的 JSON（以 { 开头）。`);
  }
  return parsed;
}

export function kindLabel(kind: TemplateKind): string {
  if (kind === 'ASSET') return '资产';
  if (kind === 'DEVICE') return '设备';
  return '点位';
}

const ASSIGNMENT_TIME = new Intl.DateTimeFormat('zh-CN', {
  month: '2-digit',
  day: '2-digit',
  hour: '2-digit',
  minute: '2-digit',
  hour12: false,
});

export function formatTimestamp(value: string): string {
  const ms = Date.parse(value);
  if (Number.isNaN(ms)) return value;
  return ASSIGNMENT_TIME.format(ms);
}

export function formatTemplateDriverSummary(summary: TemplateModbusDriverSummary): string {
  switch (summary.driverType) {
    case 'BACNET_IP':
      return `协议驱动 (${summary.driverTypeLabel}) · ${summary.registerCount} 个 BACnet 对象`;
    case 'OPC_UA':
      return `协议驱动 (${summary.driverTypeLabel}) · ${summary.registerCount} 个 OPC-UA 节点`;
    case 'MODBUS_TCP':
    case 'MODBUS_RTU':
      return `Modbus 驱动 (${summary.driverTypeLabel}) · ${summary.registerCount} 寄存器`;
  }
}
