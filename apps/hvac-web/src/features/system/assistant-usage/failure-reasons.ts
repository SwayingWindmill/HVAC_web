// Why a step of the assistant failed, in the operator's words (#624). The Agent records a code per
// failed call; the page never shows the code itself.

const FINDING_PARTS: Readonly<Record<string, string>> = Object.freeze({
  KEY_FIGURES: '关键数字',
  EVIDENCE_REFS: '依据',
  LIMITATIONS: '局限说明',
  FOLLOW_UPS: '接着问',
  OUTCOME: '结论类型',
  CHART: '图',
});
const FINDING_FAULTS: Readonly<Record<string, string>> = Object.freeze({
  TOO_LONG: '太长',
  TOO_MANY: '太多',
  MISSING: '没写',
  INVALID: '格式不对',
});

const REASONS: Readonly<Record<string, string>> = Object.freeze({
  FINDING_EVIDENCE_NOT_READ: '引用了没读到的数据',
  FINDING_CHART_NOT_READ: '图用了没读到的数据',
  FINDING_CHART_UNAVAILABLE: '那次的数据画不了图',
  FINDING_CORRECTION_REQUIRED: '要先更正「操作过设备」',
  INPUT_REQUEST_CORRECTION_REQUIRED: '要先更正「操作过设备」',
  TERMINAL_ALREADY_ACCEPTED: '重复收尾',
  FINDING_EXTRA_FIELD: '多写了不认识的内容',
  FINDING_CONTENT_INVALID: '内容格式不对',
  FINDING_UNREADABLE: '内容格式不对',
  INPUT_REQUEST_INVALID: '提问格式不对',
  TOOL_ARGUMENTS_INVALID: '参数不对',
  TOOL_UNAUTHORIZED: '没有权限',
  TOOL_CALL_LIMIT: '超出读取次数',
  TOOL_CONCURRENCY_LIMIT: '同时读取太多',
  TOOL_RESULT_TOO_LARGE: '结果太多',
  TOOL_TIMEOUT: '读数据超时',
  TOOL_OWNER_TIMEOUT: '读数据超时',
  TOOL_OWNER_UNAVAILABLE: '数据暂时读不到',
  TOOL_OWNER_RESOURCE_NOT_FOUND: '数据不存在',
  TOOL_OWNER_REQUEST_REJECTED: '请求被拒绝',
  TOOL_OWNER_RESPONSE_INVALID: '返回的数据有误',
  TOOL_CANCELLED: '被停下',
});

/** A failure code in words: 「关键数字太长」「数据暂时读不到」; a code the page does not know reads as 「其他」. */
export const failureReasonWords = (code: string): string => {
  const finding = /^FINDING_(KEY_FIGURES|EVIDENCE_REFS|LIMITATIONS|FOLLOW_UPS|OUTCOME|CHART)_(TOO_LONG|TOO_MANY|MISSING|INVALID)$/u.exec(code);
  if (finding) return `${FINDING_PARTS[finding[1]!]}${FINDING_FAULTS[finding[2]!]}`;
  return REASONS[code] ?? '其他';
};
