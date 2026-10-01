import { createFileRoute } from '@tanstack/react-router';
import { z } from 'zod';
import { AlarmsWorkspace } from '@/features/alarms/components/AlarmsWorkspace';
export const Route = createFileRoute('/_app/_site/operations/alarms')({
 validateSearch: z.object({ inspect: z.string().optional(), device: z.string().optional(),  q: z.string().optional(), severity: z.enum(['CRITICAL','MAJOR','MINOR','WARNING','INFO']).optional(), state: z.enum(['OPEN','INVESTIGATING','ACTION_PENDING','VERIFYING','RESOLVED']).optional() }), component: AlarmsWorkspace,
});
