import { createFileRoute } from '@tanstack/react-router';
import { z } from 'zod';
import { SystemsDevicesWorkspace } from '@/features/operations/systems-devices/components/SystemsDevicesWorkspace';

export const Route = createFileRoute('/_app/_site/operations/systems-devices')({
  validateSearch: z.object({
    q: z.string().optional(),
    category: z.enum(['CHILLER', 'CHILLED_WATER_PUMP', 'COOLING_WATER_PUMP', 'COOLING_TOWER', 'METER', 'WEATHER', 'OTHER']).optional(),
    status: z.enum(['running', 'stopped', 'attention']).optional(),
    inspect: z.string().optional(),
  }),
  staticData: { title: '系统与设备' },
  component: SystemsDevicesWorkspace,
});
