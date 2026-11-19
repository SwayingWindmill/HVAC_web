import { z } from 'zod';
import {
  failureMessage,
  gatewayPath,
  gatewayRequest,
  GatewayRequestError,
  valueOf,
  type GatewayProblem,
} from './gateway-client.ts';

export const accountingRecalcGranularitySchema = z.enum(['HOUR', 'DAY', 'MONTH', 'ALL']);
export const accountingRecalcStatusSchema = z.enum(['PENDING', 'RUNNING', 'SUCCEEDED', 'FAILED']);
export const accountingRecalcSourceSchema = z.enum([
  'BACKFILL',
  'MANUAL',
  'TARIFF_CHANGE',
  'RATIO_CHANGE',
]);

export const accountingRecalcJobSchema = z.object({
  id: z.string().min(1),
  tenantId: z.string().min(1),
  siteId: z.string().min(1),
  granularity: accountingRecalcGranularitySchema,
  windowStart: z.string().min(1),
  windowEnd: z.string().min(1),
  status: accountingRecalcStatusSchema,
  availableAt: z.string().nullish().transform((value) => value ?? ''),
  requestedBy: z.string().nullish().transform((value) => value ?? ''),
  requestedByDisplay: z.string().nullish().transform((value) => value ?? ''),
  reason: z.string().nullish().transform((value) => value ?? ''),
  source: accountingRecalcSourceSchema,
  attemptCount: z.number().int().nonnegative(),
  lastError: z.string().nullish().transform((value) => value ?? ''),
  createdAt: z.string().min(1),
  updatedAt: z.string().min(1),
  finishedAt: z.string().nullable(),
});

export const accountingRecalcJobListSchema = z.object({
  items: z.array(accountingRecalcJobSchema),
});

export type AccountingRecalcGranularity = z.infer<typeof accountingRecalcGranularitySchema>;
export type AccountingRecalcStatus = z.infer<typeof accountingRecalcStatusSchema>;
export type AccountingRecalcSource = z.infer<typeof accountingRecalcSourceSchema>;
export type AccountingRecalcJob = z.infer<typeof accountingRecalcJobSchema>;

export interface SubmitAccountingRecalcInput {
  readonly fromDate: string;
  readonly toDate: string;
  readonly granularity: AccountingRecalcGranularity;
  readonly reason: string;
  readonly idempotencyKey?: string;
}

const RECALC_UNAVAILABLE_MESSAGE = '暂时无法连接电费与能耗重算服务，请稍后重试';

function describeAccountingRecalcRefusal(problem: GatewayProblem): string {
  const { status, code } = problem;
  if (
    status === 403 ||
    code === 'BILLING_MANAGE_FORBIDDEN' ||
    code === 'SAVINGS_MANAGE_FORBIDDEN' ||
    code === 'FORBIDDEN'
  ) {
    return '当前账号没有重算站点电费与能耗的权限，请联系站点管理员';
  }
  if (status === 404 || code === 'RESOURCE_NOT_FOUND') {
    return '未找到对应的站点记录，请刷新页面后重试';
  }
  if (
    status === 409 ||
    status === 429 ||
    code === 'ACCOUNTING_RECALC_IN_PROGRESS' ||
    code === 'REGISTRY_IDEMPOTENCY_CONFLICT' ||
    code === 'CONFLICT' ||
    code === 'TOO_MANY_REQUESTS'
  ) {
    return '当前已有相同时段的重算任务正在执行或请求过于频繁，请稍后再试';
  }
  if (
    status === 400 ||
    code === 'ACCOUNTING_RECALC_INVALID' ||
    code === 'VALIDATION_ERROR' ||
    code === 'INVALID_ARGUMENT'
  ) {
    return '重算日期范围、统计粒度或原因不符合要求，请核对后重试';
  }
  return '无法提交重算任务，请核对起止日期与原因后重试';
}

export function describeAccountingRecalcError(error: unknown, fallback: string): string {
  if (error instanceof GatewayRequestError) {
    return failureMessage(error.failure, {
      unavailable: fallback,
      refused: describeAccountingRecalcRefusal,
    });
  }
  return fallback;
}

export async function listSiteAccountingRecalcJobs(
  siteId: string,
  options: { readonly signal?: AbortSignal } = {},
): Promise<AccountingRecalcJob[]> {
  const path = gatewayPath('/api/v1/sites/{siteId}/accounting/recalculate-jobs', { siteId });
  const result = await gatewayRequest(path, {
    signal: options.signal,
    parse: (payload) => accountingRecalcJobListSchema.parse(payload).items,
  });
  return valueOf(result, {
    unavailable: RECALC_UNAVAILABLE_MESSAGE,
    refused: describeAccountingRecalcRefusal,
  });
}

export async function submitSiteAccountingRecalc(
  siteId: string,
  csrfToken: string,
  input: SubmitAccountingRecalcInput,
): Promise<AccountingRecalcJob> {
  const path = gatewayPath('/api/v1/sites/{siteId}/accounting/recalculate', { siteId });
  const result = await gatewayRequest(path, {
    method: 'POST',
    csrfToken,
    idempotencyKey: input.idempotencyKey ?? crypto.randomUUID(),
    body: {
      fromDate: input.fromDate,
      toDate: input.toDate,
      granularity: input.granularity,
      reason: input.reason,
    },
    parse: (payload) => accountingRecalcJobSchema.parse(payload),
  });
  return valueOf(result, {
    unavailable: RECALC_UNAVAILABLE_MESSAGE,
    refused: describeAccountingRecalcRefusal,
  });
}
