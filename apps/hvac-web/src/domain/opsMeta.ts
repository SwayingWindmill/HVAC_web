import type {
  Severity,
} from '@/theme/tokens';
import type {
  Suggestion,
  SuggestionRisk,
  SuggestionStatus,
  SuggestionType,
  TicketStatus,
  WorkOrder,
} from '@/mock/data';

export const TICKET_STATUS_META: Record<TicketStatus, { color: string; label: string; next?: TicketStatus; nextLabel?: string; step: number }> = {
  open: { color: 'red', label: '待接手', next: 'assigned', nextLabel: '接手', step: 0 },
  assigned: { color: 'gold', label: '已派工', next: 'doing', nextLabel: '开始处理', step: 1 },
  doing: { color: 'blue', label: '处理中', next: 'done', nextLabel: '完成闭环', step: 2 },
  done: { color: 'green', label: '已完成', step: 3 },
};

export const SUGGESTION_STATUS_META: Record<SuggestionStatus, { color: string; label: string; step: number }> = {
  draft: { color: 'default', label: '草稿', step: 0 },
  pending: { color: 'gold', label: '待审批', step: 1 },
  approved: { color: 'green', label: '已批准', step: 2 },
  dispatched: { color: 'blue', label: '已下发', step: 3 },
  rejected: { color: 'red', label: '已驳回', step: 1 },
};

export const SUGGESTION_TYPE_META: Record<SuggestionType, { color: string; label: string }> = {
  setpoint: { color: 'cyan', label: '设定值优化' },
  schedule: { color: 'purple', label: '运行日程优化' },
};

export const SUGGESTION_RISK_META: Record<SuggestionRisk, { color: string; label: string }> = {
  low: { color: 'green', label: '低风险' },
  medium: { color: 'gold', label: '中风险' },
  high: { color: 'red', label: '高风险' },
};

export const isHighSeverity = (severity: Severity) => severity === 'critical' || severity === 'major';
export const isTicketActive = (status: TicketStatus) => status !== 'done';
export const isWorkOrderActive = (order: WorkOrder) => isTicketActive(order.status);
export const isWorkOrderSlaRisk = (order: WorkOrder) => isWorkOrderActive(order) && isHighSeverity(order.severity);
export const isSuggestionActionable = (status: SuggestionStatus) => status === 'draft' || status === 'pending' || status === 'approved';
export const isSuggestionPendingDecision = (suggestion: Suggestion) => isSuggestionActionable(suggestion.status);
