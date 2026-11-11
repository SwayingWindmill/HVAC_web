import { createFileRoute } from '@tanstack/react-router';
import { z } from 'zod';
import { BillingWorkspace } from '@/features/billing/components/BillingWorkspace';

export const billingSearchSchema = z.object({
  view: z.enum(['overview', 'bills']).optional().catch(undefined),
  month: z
    .string()
    .regex(/^\d{4}-(0[1-9]|1[0-2])$/)
    .optional()
    .catch(undefined),
  inspect: z.string().optional().catch(undefined),
  scope: z.enum(['CHILLER_PLANT', 'WHOLE_BUILDING']).optional().catch(undefined),
  status: z.enum(['DRAFT', 'CONFIRMED']).optional().catch(undefined),
});

export type BillingSearch = z.infer<typeof billingSearchSchema>;

export const Route = createFileRoute('/_app/_site/energy-analysis/billing')({
  validateSearch: billingSearchSchema,
  staticData: { title: '电费账单' },
  component: BillingWorkspace,
});
