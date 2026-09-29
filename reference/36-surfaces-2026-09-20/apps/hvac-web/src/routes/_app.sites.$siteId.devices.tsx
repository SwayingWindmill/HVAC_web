import { Outlet, createFileRoute } from '@tanstack/react-router';
import { fallback, zodValidator } from '@tanstack/zod-adapter';
import { z } from 'zod';
import { requireCapabilities } from '@/app/route-access';

const deviceSearchSchema = z.object({
  q: z.string().optional(),
  scope: z.string().optional(),
  deviceType: z.string().optional(),
  connection: fallback(z.enum(['all', 'ONLINE', 'OFFLINE', 'UNKNOWN']), 'all').optional(),
  running: fallback(z.enum(['all', 'RUNNING', 'STOPPED', 'STANDBY', 'UNKNOWN']), 'all').optional(),
  data: fallback(z.enum(['all', 'healthy', 'issue']), 'all').optional(),
  page: fallback(z.number().int().positive(), 1).optional(),
  inspect: z.string().optional(),
});

export const Route = createFileRoute('/_app/sites/$siteId/devices')({
  validateSearch: zodValidator(deviceSearchSchema),
  beforeLoad: ({ context }) => {
    requireCapabilities(context.runtime, ['asset.list', 'device.list']);
  },
  staticData: {
    title: '设备中心',
    scope: 'site',
    requiredCapabilities: ['site.read', 'asset.list', 'device.list'],
  },
  component: Outlet,
});
