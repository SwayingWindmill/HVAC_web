import { createFileRoute } from '@tanstack/react-router';
import { RealtimeWorkspace } from '@/features/operations/realtime/components/RealtimeWorkspace';

export const Route = createFileRoute('/_app/_site/operations/realtime')({
  staticData: { title: '实时运行' },
  component: RealtimeWorkspace,
});
