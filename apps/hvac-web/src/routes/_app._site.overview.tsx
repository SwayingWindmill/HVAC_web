import { createFileRoute } from "@tanstack/react-router";
import { z } from "zod";
import { OverviewWorkspace } from "@/features/overview/OverviewWorkspace";

export const Route = createFileRoute("/_app/_site/overview")({
  validateSearch: z.object({
    period: z.enum(["today", "7d", "month", "year"]).optional(),
  }),
  component: OverviewWorkspace,
});
