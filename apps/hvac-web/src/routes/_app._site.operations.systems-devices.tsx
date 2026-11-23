import { createFileRoute } from '@tanstack/react-router';
import { z } from 'zod';
import { SystemsDevicesWorkspace } from '@/features/operations/systems-devices/components/SystemsDevicesWorkspace';

export const Route = createFileRoute('/_app/_site/operations/systems-devices')({
  validateSearch: z.object({
    q: z.string().optional().catch(undefined),
    category: z.enum(['CHILLER', 'CHILLED_WATER_PUMP', 'COOLING_WATER_PUMP', 'COOLING_TOWER', 'ZONE', 'METER', 'WEATHER', 'ACQUISITION', 'OTHER']).optional().catch(undefined),
    status: z.enum(['running', 'stopped', 'attention']).optional().catch(undefined),
    tag: z.string().optional().catch(undefined),
    inspect: z.string().optional().catch(undefined),
    layout: z.enum(['cards', 'table']).optional().catch(undefined),
  }),
  staticData: { title: '设备' },
  component: SystemsDevicesWorkspace,
});
