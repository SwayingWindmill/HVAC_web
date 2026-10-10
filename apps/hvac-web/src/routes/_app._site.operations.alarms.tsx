import { createFileRoute } from '@tanstack/react-router';
import { z } from 'zod';
import { AlarmsWorkspace } from '@/features/alarms/components/AlarmsWorkspace';

const severity = z.enum(['CRITICAL', 'MAJOR', 'MINOR', 'WARNING', 'INFO']);

export const Route = createFileRoute('/_app/_site/operations/alarms')({
  validateSearch: z.object({
    view: z.enum(['active', 'unack', 'cleared', 'all']).optional(),
    severity: z.array(severity).optional(),
    device: z.string().optional(),
    q: z.string().optional(),
    inspect: z.string().optional(),
  }),
  staticData: { title: '异常与告警' },
  component: AlarmsWorkspace,
});
