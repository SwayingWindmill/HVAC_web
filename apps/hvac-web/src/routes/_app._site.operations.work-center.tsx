import { createFileRoute } from '@tanstack/react-router';
import { z } from 'zod';
import { WorkCenterWorkspace } from '@/features/work-orders/components/WorkCenterWorkspace';

export const Route = createFileRoute('/_app/_site/operations/work-center')({
  validateSearch: z.object({
    view: z.enum(['open', 'mine', 'done', 'all']).optional(),
    inspect: z.string().optional(),
  }),
  staticData: { title: '工作中心' },
  component: WorkCenterWorkspace,
});
