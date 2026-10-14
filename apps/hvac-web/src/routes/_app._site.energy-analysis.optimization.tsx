import { createFileRoute } from '@tanstack/react-router';
import { OptimizationWorkspace } from '@/features/optimization/components/OptimizationWorkspace';

export const Route = createFileRoute('/_app/_site/energy-analysis/optimization')({
  staticData: { title: '优化建议' },
  component: OptimizationWorkspace,
});
