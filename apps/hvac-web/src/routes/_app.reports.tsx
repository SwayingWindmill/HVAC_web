import { createFileRoute } from "@tanstack/react-router";
import { z } from "zod";
import { ReportsWorkspace } from "@/features/reports/ReportsWorkspace";
export const Route = createFileRoute("/_app/reports")({
  validateSearch: z.object({
    scope: z.string().optional(),
    q: z.string().optional(),
    type: z.enum(["energy", "verification"]).optional(),
  }),
  component: ReportsWorkspace,
});
