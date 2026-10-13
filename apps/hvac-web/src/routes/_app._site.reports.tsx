import { createFileRoute } from '@tanstack/react-router';
import { z } from 'zod';
import { ReportWorkspace } from '@/features/reports/components/ReportWorkspace';

export const Route = createFileRoute('/_app/_site/reports')({
  staticData: { title: '能源报告' },
  validateSearch: z.object({
    view: z.enum(['month', 'year']).optional(),
    month: z.string().regex(/^\d{4}-\d{2}$/).optional(),
    year: z.number().int().min(2000).max(9999).optional(),
  }),
  component: ReportWorkspace,
});
