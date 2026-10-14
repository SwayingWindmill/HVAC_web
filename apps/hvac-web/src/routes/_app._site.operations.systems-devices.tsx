import { createFileRoute } from '@tanstack/react-router';
import { z } from 'zod';
import { SystemsDevicesWorkspace } from '@/features/operations/systems-devices/components/SystemsDevicesWorkspace';

export const Route = createFileRoute('/_app/_site/operations/systems-devices')({
  validateSearch: z.object({
    q: z.string().optional(),
    category: z.enum(['CHILLER', 'CHILLED_WATER_PUMP', 'COOLING_WATER_PUMP', 'COOLING_TOWER', 'ZONE', 'METER', 'WEATHER', 'ACQUISITION', 'OTHER']).optional(),
    status: z.enum(['running', 'stopped', 'attention']).optional(),
    inspect: z.string().optional(),
    layout: z.enum(['cards', 'table']).optional(),
  }),
  staticData: { title: '设备' },
  component: SystemsDevicesWorkspace,
});
