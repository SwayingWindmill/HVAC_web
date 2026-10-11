import { createFileRoute } from '@tanstack/react-router';
import { ForecastWorkspace } from '@/features/forecast/components/ForecastWorkspace';

export const Route = createFileRoute('/_app/_site/energy-analysis/forecast')({
  staticData: { title: '负荷预测' },
  component: ForecastWorkspace,
});
