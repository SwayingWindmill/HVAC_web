import { createFileRoute } from '@tanstack/react-router';
import { z } from 'zod';
import { DigitalTwinWorkspace } from '@/features/operations/digital-twin/components/DigitalTwinWorkspace';

export const Route = createFileRoute('/_app/_site/operations/digital-twin')({
  validateSearch: z.object({
    view: z.enum(['overview', 'plant', 'terminal']).optional(),
    floor: z.number().int().min(1).max(9).optional(),
    shell: z.literal('off').optional(),
    pipes: z.literal('on').optional(),
    inspect: z.string().optional(),
  }),
  staticData: { title: '数字孪生' },
  component: DigitalTwinWorkspace,
});
