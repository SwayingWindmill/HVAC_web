import { createFileRoute } from '@tanstack/react-router';
import { TrendsWorkspace } from '@/features/operations/trends/components/TrendsWorkspace';
import { trendsSearchSchema, type TrendsSearch } from '@/features/operations/trends/trends-search';

export { trendsSearchSchema, type TrendsSearch };

export const Route = createFileRoute('/_app/_site/operations/trends')({
  validateSearch: trendsSearchSchema,
  staticData: { title: '历史趋势' },
  component: TrendsWorkspace,
});
