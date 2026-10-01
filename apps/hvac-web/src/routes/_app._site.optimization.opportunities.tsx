import { createFileRoute } from "@tanstack/react-router";
import { z } from "zod";
import { OpportunitiesWorkspace } from "@/features/opportunities/components/OpportunitiesWorkspace";
export const Route = createFileRoute("/_app/_site/optimization/opportunities")({
  validateSearch: z.object({
    
    inspect: z.string().optional(),
    q: z.string().optional(),
    subsystem: z.string().optional(),
    status: z.string().optional(),
    sort: z.enum(["saving", "confidence", "payback"]).optional(),
  }),
  component: OpportunitiesWorkspace,
});
