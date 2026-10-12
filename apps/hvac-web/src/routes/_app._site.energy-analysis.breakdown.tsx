import { createFileRoute } from '@tanstack/react-router';
import { z } from 'zod';
import { BreakdownWorkspace } from '@/features/breakdown/components/BreakdownWorkspace';

export const Route = createFileRoute('/_app/_site/energy-analysis/breakdown')({
  staticData: { title: '分项能耗' },
  validateSearch: z.object({
    period: z.enum(['today', '7d', 'month']).optional(),
  }),
  component: BreakdownWorkspace,
});
