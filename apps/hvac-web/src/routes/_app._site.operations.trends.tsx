import { createFileRoute } from '@tanstack/react-router';
import { z } from 'zod';
import { TrendsWorkspace } from '@/features/operations/trends/components/TrendsWorkspace';

export const trendsSearchSchema = z.object({
  preset: z
    .enum(['plant-overview', 'chiller-comparison', 'pump-hydraulics', 'outdoor-load'])
    .optional()
    .catch(undefined),
  points: z.string().optional().catch(undefined),
  window: z.enum(['1h', '6h', 'today', '7d', 'custom']).optional().catch(undefined),
  from: z.string().optional().catch(undefined),
  to: z.string().optional().catch(undefined),
  granularity: z
    .enum(['MINUTE', 'FIVE_MINUTE', 'QUARTER_HOUR', 'HOUR', 'DAY'])
    .optional()
    .catch(undefined),
  compare: z.enum(['prev']).optional().catch(undefined),
  view: z.enum(['chart', 'table']).optional().catch(undefined),
  overlays: z.string().optional().catch(undefined),
  source: z.enum(['alarm', 'diagnostic']).optional().catch(undefined),
  sourceId: z.string().optional().catch(undefined),
  sourceTitle: z.string().optional().catch(undefined),
});

export type TrendsSearch = z.infer<typeof trendsSearchSchema>;

export const Route = createFileRoute('/_app/_site/operations/trends')({
  validateSearch: trendsSearchSchema,
  staticData: { title: '历史趋势' },
  component: TrendsWorkspace,
});
