import { createFileRoute } from '@tanstack/react-router';
import { z } from 'zod';
import { RealtimeWorkspace } from '@/features/operations/realtime/components/RealtimeWorkspace';

export const Route = createFileRoute('/_app/_site/operations/realtime')({
  validateSearch: z.object({
    metric: z.enum(['power', 'cooling', 'cop']).optional(),
    window: z.enum(['6h', '24h']).optional(),
  }),
  staticData: { title: '实时运行' },
  component: RealtimeWorkspace,
});
