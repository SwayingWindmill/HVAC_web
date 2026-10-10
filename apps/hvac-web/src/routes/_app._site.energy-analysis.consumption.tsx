import { createFileRoute } from "@tanstack/react-router";
import { z } from "zod";
import { ConsumptionWorkspace } from "@/features/energy-analysis/consumption/components/ConsumptionWorkspace";
export const Route = createFileRoute("/_app/_site/energy-analysis/consumption")({
  validateSearch: z.object({
    period: z.enum(["today", "7d", "month", "year"]).optional(),
  }),
  component: ConsumptionWorkspace,
});
