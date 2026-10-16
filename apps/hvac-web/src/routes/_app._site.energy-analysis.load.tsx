import { createFileRoute } from '@tanstack/react-router';
import { z } from 'zod';
import { LoadWorkspace } from '@/features/load/components/LoadWorkspace';

export const Route = createFileRoute('/_app/_site/energy-analysis/load')({
  staticData: { title: '用电负荷' },
  validateSearch: z.object({
    view: z.enum(['hours', 'days', 'charge']).optional(),
  }),
  component: LoadWorkspace,
});
