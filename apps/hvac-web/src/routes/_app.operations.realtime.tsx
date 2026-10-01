import { createFileRoute } from '@tanstack/react-router';
import { z } from 'zod';
import { RealtimeWorkspace } from '@/features/operations/realtime/components/RealtimeWorkspace';
export const Route = createFileRoute('/_app/operations/realtime')({
 validateSearch: z.object({ scope: z.string().optional(), view: z.enum(['plant','loops']).optional() }), component: RealtimeWorkspace,
});
