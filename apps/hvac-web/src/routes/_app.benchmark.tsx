import { createFileRoute } from '@tanstack/react-router';
import { z } from 'zod';
import { requirePlatform } from '@/app/route-access';
import { useShellSnapshot } from '@/app/ShellRuntimeContext';
import { BenchmarkWorkspace } from '@/features/benchmark/components/BenchmarkWorkspace';

export const Route = createFileRoute('/_app/benchmark')({
  beforeLoad: ({ context }) => requirePlatform(context.runtime),
  staticData: { title: '站点对标', scope: 'platform', requiresPlatform: true },
  validateSearch: z.object({
    period: z.enum(['7d', 'month']).optional(),
    rank: z.enum(['cop', 'intensity']).optional(),
    site: z.string().optional(),
  }),
  component: BenchmarkRoute,
});

function BenchmarkRoute() {
  return <BenchmarkWorkspace snapshot={useShellSnapshot()} />;
}
