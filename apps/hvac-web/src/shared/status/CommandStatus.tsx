import { Tag } from 'antd';
import type { Command } from '@/api/command-contract';
import { projectCommandStage, type RealCommandStage } from './command-status';

export { projectCommandStage } from './command-status';
export type { RealCommandStage } from './command-status';

const STAGE_META: Record<RealCommandStage, { label: string; color: string }> = {
  CREATED: { label: 'CREATED · 已创建', color: 'blue' },
  APPROVAL_PENDING: { label: 'APPROVAL_PENDING · 待审批', color: 'gold' },
  DISPATCHED: { label: 'DISPATCHED · 已下发', color: 'cyan' },
  SENT: { label: 'SENT · 已发送', color: 'cyan' },
  ACKED: { label: 'ACKED · 已确认接收', color: 'geekblue' },
  VERIFIED: { label: 'VERIFIED · 已验证', color: 'green' },
  UNKNOWN: { label: 'UNKNOWN · 结果未知', color: 'orange' },
  FAILED: { label: 'FAILED · 执行失败', color: 'red' },
  REJECTED: { label: 'REJECTED · 已拒绝', color: 'red' },
  EXPIRED: { label: 'EXPIRED · 已过期', color: 'default' },
  TIMEOUT: { label: 'TIMEOUT · 执行超时', color: 'orange' },
};

export function CommandStatusBadge({ command }: { command: Command }) {
  const stage = projectCommandStage(command);
  const meta = STAGE_META[stage];
  return (
    <Tag color={meta.color} data-command-stage={stage}>
      {meta.label}
    </Tag>
  );
}
