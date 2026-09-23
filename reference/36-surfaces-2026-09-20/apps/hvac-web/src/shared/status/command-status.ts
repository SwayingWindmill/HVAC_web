import type { Command } from '../../api/command-contract.ts';

export type CommandStage =
  | 'CREATED'
  | 'APPROVAL_PENDING'
  | 'DISPATCHED'
  | 'SENT'
  | 'ACKED'
  | 'VERIFIED'
  | 'UNKNOWN'
  | 'FAILED'
  | 'REJECTED'
  | 'EXPIRED'
  | 'TIMEOUT';

export function projectCommandStage(command: Command): CommandStage {
  const latestReason = command.transitions.at(-1)?.reason.toUpperCase() ?? '';
  if (latestReason.includes('TIMEOUT')) return 'TIMEOUT';
  switch (command.status) {
    case 'SUBMITTED':
    case 'VALIDATING':
      return 'CREATED';
    case 'AWAITING_APPROVAL':
      return 'APPROVAL_PENDING';
    case 'APPROVED':
    case 'QUEUED':
      return 'DISPATCHED';
    case 'DISPATCHING':
      return 'SENT';
    case 'SUCCEEDED':
      return 'VERIFIED';
    case 'OUTCOME_UNKNOWN':
      return 'UNKNOWN';
    case 'REJECTED':
      return 'REJECTED';
    case 'EXPIRED':
      return 'EXPIRED';
    case 'FAILED':
    case 'CANCELLED':
      return 'FAILED';
  }
}
