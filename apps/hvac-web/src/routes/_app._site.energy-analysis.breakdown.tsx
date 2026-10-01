import { createFileRoute } from "@tanstack/react-router";
import { z } from "zod";
import { BreakdownWorkspace } from "@/features/energy-analysis/breakdown/components/BreakdownWorkspace";
export const Route = createFileRoute("/_app/_site/energy-analysis/breakdown")({
  validateSearch: z.object({
    
    view: z.enum(["flow", "ledger"]).optional(),
    q: z.string().optional(),
  }),
  component: BreakdownWorkspace,
});
