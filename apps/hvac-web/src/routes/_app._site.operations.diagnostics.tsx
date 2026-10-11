import { createFileRoute } from '@tanstack/react-router';
import { z } from 'zod';
import { DiagnosticsWorkspace } from '@/features/diagnostics/components/DiagnosticsWorkspace';

export const Route = createFileRoute('/_app/_site/operations/diagnostics')({
  validateSearch: z.object({
    inspect: z.string().optional(),
  }),
  staticData: { title: '故障诊断' },
  component: DiagnosticsWorkspace,
});
