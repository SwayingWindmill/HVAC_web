import { Outlet, createFileRoute } from '@tanstack/react-router';
import { fallback, zodValidator } from '@tanstack/zod-adapter';
import { z } from 'zod';

const energySearchSchema = z.object({
  anchor: z.string().regex(/^\d{4}-\d{2}-\d{2}$/).optional(),
  quality: fallback(z.enum(['VALID_ONLY', 'VALID_AND_SUSPECT']), 'VALID_ONLY').optional(),
});

export const Route = createFileRoute('/_app/sites/$siteId/energy')({
  validateSearch: zodValidator(energySearchSchema),
  staticData: {
    title: '能源分析',
    scope: 'site',
    requiredCapabilities: ['site.read'],
    navigation: { id: 'site-energy', label: '能源', group: 'energy', order: 50, siteLeaf: 'energy' },
  },
  component: Outlet,
});
