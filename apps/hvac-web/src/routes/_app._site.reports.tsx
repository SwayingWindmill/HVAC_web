import { createFileRoute } from "@tanstack/react-router";
import { z } from "zod";
import { ReportsWorkspace } from "@/features/reports/ReportsWorkspace";
export const Route = createFileRoute("/_app/_site/reports")({
  validateSearch: z.object({
    
    q: z.string().optional(),
    type: z.enum(["energy", "verification"]).optional(),
  }),
  component: ReportsWorkspace,
});
