import { createFileRoute } from '@tanstack/react-router';
import { z } from 'zod';
import { OptimizationWorkspace } from '@/features/optimization/components/OptimizationWorkspace';

export const optimizationSearchSchema = z.object({
  view: z.enum(['proposal', 'analysis']).optional().catch(undefined),
});

export type OptimizationSearch = z.infer<typeof optimizationSearchSchema>;

export const Route = createFileRoute('/_app/_site/energy-analysis/optimization')({
  validateSearch: optimizationSearchSchema,
  staticData: { title: '优化建议' },
  component: OptimizationWorkspace,
});
