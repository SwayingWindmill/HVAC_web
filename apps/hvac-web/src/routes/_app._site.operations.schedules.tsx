import { createFileRoute } from '@tanstack/react-router';
import { z } from 'zod';
import { SchedulesWorkspace } from '@/features/schedules/components/SchedulesWorkspace';

export const schedulesSearchSchema = z.object({
  view: z.enum(['week', 'schedule', 'exceptions']).optional().catch(undefined),
  week: z.string().regex(/^\d{4}-\d{2}-\d{2}$/).optional().catch(undefined),
  inspect: z.string().optional().catch(undefined),
  status: z.enum(['PENDING_APPROVAL', 'APPROVED', 'REJECTED', 'CANCELLED']).optional().catch(undefined),
  page: z.coerce.number().int().min(1).optional().catch(undefined),
  pageSize: z.coerce.number().int().min(10).max(100).optional().catch(undefined),
});

export type SchedulesSearch = z.infer<typeof schedulesSearchSchema>;

export const Route = createFileRoute('/_app/_site/operations/schedules')({
  validateSearch: schedulesSearchSchema,
  staticData: { title: '运行计划' },
  component: SchedulesWorkspace,
});
