import { createFileRoute } from '@tanstack/react-router';
import { z } from 'zod';
import { ControlWorkspace } from '@/features/control/components/ControlWorkspace';

export const Route = createFileRoute('/_app/_site/operations/control')({
  validateSearch: z.object({
    inspect: z.string().optional(),
  }),
  staticData: { title: '控制指令' },
  component: ControlWorkspace,
});
