import { Outlet, createFileRoute } from '@tanstack/react-router';
import { fallback, zodValidator } from '@tanstack/zod-adapter';
import { z } from 'zod';
import { requireCapabilities } from '@/app/route-access';

const deviceSearchSchema = z.object({
  q: z.string().optional(),
  scope: z.string().optional(),
  inspect: z.string().optional(),
  deviceFilters: z.string().optional(),
  deviceJoin: fallback(z.enum(['and', 'or']), 'and').optional(),
});

export const Route = createFileRoute('/_app/sites/$siteId/devices')({
  validateSearch: zodValidator(deviceSearchSchema),
  beforeLoad: ({ context }) => {
    requireCapabilities(context.runtime, ['asset.list', 'device.list']);
  },
  staticData: {
    title: '设备',
    scope: 'site',
    requiredCapabilities: ['site.read', 'asset.list', 'device.list'],
  },
  component: Outlet,
});
