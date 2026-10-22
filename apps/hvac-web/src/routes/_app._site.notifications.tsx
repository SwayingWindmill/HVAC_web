import { createFileRoute } from '@tanstack/react-router';
import { z } from 'zod';
import { NotificationCenter } from '@/features/notifications/NotificationCenter';

export const Route = createFileRoute('/_app/_site/notifications')({
  validateSearch: z.object({
    unack: z.boolean().optional(),
    inspect: z.string().optional(),
  }),
  staticData: { title: '通知' },
  component: NotificationCenter,
});
