import { createFileRoute } from '@tanstack/react-router';
import { z } from 'zod';
import { SystemsDevicesWorkspace } from '@/features/operations/systems-devices/components/SystemsDevicesWorkspace';
export const Route = createFileRoute('/_app/_site/operations/systems-devices')({
 validateSearch: z.object({ inspect: z.string().optional(),  q: z.string().optional(), status: z.enum(['running','standby','alarm','offline']).optional(), category: z.string().optional(), lens: z.enum(['system','space','meter']).optional() }), component: SystemsDevicesWorkspace,
});
