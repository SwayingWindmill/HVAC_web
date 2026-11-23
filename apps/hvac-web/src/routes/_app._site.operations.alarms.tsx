import { createFileRoute } from '@tanstack/react-router';
import { z } from 'zod';
import { AlarmsWorkspace } from '@/features/alarms/components/AlarmsWorkspace';

const severity = z.enum(['CRITICAL', 'MAJOR', 'MINOR', 'WARNING', 'INFO']);

export const Route = createFileRoute('/_app/_site/operations/alarms')({
  validateSearch: z.object({
    tab: z.enum(['rules']).optional().catch(undefined),
    view: z.enum(['active', 'unack', 'suppressed', 'cleared', 'all']).optional().catch(undefined),
    severity: z.array(severity).optional().catch(undefined),
    device: z.string().optional().catch(undefined),
    q: z.string().optional().catch(undefined),
    inspect: z.string().optional().catch(undefined),
  }),
  staticData: { title: '告警' },
  component: AlarmsWorkspace,
});
