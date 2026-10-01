import { createFileRoute } from "@tanstack/react-router";
import { z } from "zod";
import { ConsumptionWorkspace } from "@/features/energy-analysis/consumption/components/ConsumptionWorkspace";
export const Route = createFileRoute("/_app/energy-analysis/consumption")({
  validateSearch: z.object({
    scope: z.string().optional(),
    period: z
      .enum(["current-month", "last-month", "quarter", "year"])
      .optional(),
    view: z.enum(["energy", "cost", "ledger"]).optional(),
  }),
  component: ConsumptionWorkspace,
});
