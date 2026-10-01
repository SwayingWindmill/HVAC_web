import { createFileRoute } from "@tanstack/react-router";
import { z } from "zod";
import { LoadDemandWorkspace } from "@/features/energy-analysis/load-demand/components/LoadDemandWorkspace";
export const Route = createFileRoute("/_app/energy-analysis/load-demand")({
  validateSearch: z.object({
    scope: z.string().optional(),
    view: z.enum(["profile", "duration"]).optional(),
  }),
  component: LoadDemandWorkspace,
});
