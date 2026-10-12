import { createFileRoute } from "@tanstack/react-router";
import { z } from "zod";
import { OverviewWorkspace } from "@/features/overview/OverviewWorkspace";

export const Route = createFileRoute("/_app/_site/overview")({
  validateSearch: z.object({
    metric: z.enum(["electricityKWh", "coolingKWh", "cop", "cost"]).optional(),
  }),
  component: OverviewWorkspace,
});
