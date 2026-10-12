import { createFileRoute } from '@tanstack/react-router';
import { z } from 'zod';
import { SavingsWorkspace } from '@/features/savings/components/SavingsWorkspace';

export const Route = createFileRoute('/_app/_site/energy-analysis/savings')({
  validateSearch: z.object({
    inspect: z.string().optional(),
  }),
  staticData: { title: '节能项目' },
  component: SavingsWorkspace,
});
