import { createFileRoute } from '@tanstack/react-router';
import { z } from 'zod';
import { ReportWorkspace } from '@/features/reports/components/ReportWorkspace';

export const Route = createFileRoute('/_app/_site/reports')({
  staticData: { title: '月度报告' },
  validateSearch: z.object({
    month: z.string().regex(/^\d{4}-\d{2}$/).optional(),
  }),
  component: ReportWorkspace,
});
