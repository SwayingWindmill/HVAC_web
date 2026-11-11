import { createFileRoute } from '@tanstack/react-router';
import { z } from 'zod';
import { TrendsWorkspace } from '@/features/operations/trends/components/TrendsWorkspace';

export const trendsSearchSchema = z.object({
  preset: z
    .enum(['plant-overview', 'chiller-comparison', 'pump-hydraulics', 'outdoor-load'])
    .optional(),
  points: z.string().optional(),
  window: z.enum(['1h', '6h', 'today', '7d', 'custom']).optional(),
  from: z.string().optional(),
  to: z.string().optional(),
  granularity: z
    .enum(['MINUTE', 'FIVE_MINUTE', 'QUARTER_HOUR', 'HOUR', 'DAY'])
    .optional(),
  compare: z.enum(['prev']).optional(),
  view: z.enum(['chart', 'table']).optional(),
  overlays: z.string().optional(),
  source: z.enum(['alarm', 'diagnostic']).optional(),
  sourceId: z.string().optional(),
  sourceTitle: z.string().optional(),
});

export type TrendsSearch = z.infer<typeof trendsSearchSchema>;

export const Route = createFileRoute('/_app/_site/operations/trends')({
  validateSearch: trendsSearchSchema,
  staticData: { title: '历史趋势' },
  component: TrendsWorkspace,
});
