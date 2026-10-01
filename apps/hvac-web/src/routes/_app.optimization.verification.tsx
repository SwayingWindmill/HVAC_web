import { createFileRoute } from "@tanstack/react-router";
import { z } from "zod";
import { VerificationWorkspace } from "@/features/verification/components/VerificationWorkspace";
export const Route = createFileRoute("/_app/optimization/verification")({
  validateSearch: z.object({
    scope: z.string().optional(),
    q: z.string().optional(),
    status: z.string().optional(),
    project: z.string().optional(),
    record: z.string().optional(),
    inspect: z.string().optional(),
  }),
  component: VerificationWorkspace,
});
