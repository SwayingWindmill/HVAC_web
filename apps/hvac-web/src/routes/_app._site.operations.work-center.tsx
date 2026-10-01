import { createFileRoute } from '@tanstack/react-router';
import { z } from 'zod';
import { WorkCenterWorkspace } from '@/features/work-orders/components/WorkCenterWorkspace';
export const Route = createFileRoute('/_app/_site/operations/work-center')({
 validateSearch: z.object({ inspect: z.string().optional(), alarm: z.string().optional(),  q: z.string().optional(), status: z.enum(['OPEN','IN_PROGRESS','BLOCKED','COMPLETED','CANCELLED']).optional(), view: z.enum(['list','board']).optional() }), component: WorkCenterWorkspace,
});
