import { createFileRoute } from '@tanstack/react-router';
import { z } from 'zod';
import { AlarmsWorkspace } from '@/features/alarms/components/AlarmsWorkspace';

export const Route = createFileRoute('/_app/_site/operations/alarms')({
  validateSearch: z.object({
    view: z.enum(['active', 'unack', 'cleared', 'all']).optional(),
    severity: z.enum(['CRITICAL', 'MAJOR', 'MINOR', 'WARNING', 'INFO']).optional(),
    inspect: z.string().optional(),
  }),
  staticData: { title: '异常与告警' },
  component: AlarmsWorkspace,
});
