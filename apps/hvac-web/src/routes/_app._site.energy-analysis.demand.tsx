import { createFileRoute } from '@tanstack/react-router';
import { DemandWorkspace } from '@/features/demand/components/DemandWorkspace';

export const Route = createFileRoute('/_app/_site/energy-analysis/demand')({
  staticData: { title: '需量分析' },
  component: DemandWorkspace,
});
