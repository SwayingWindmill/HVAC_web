import { createFileRoute } from "@tanstack/react-router";
import { OverviewDashboard } from "@/features/overview/components/OverviewDashboard";
import { z } from "zod";

export const Route = createFileRoute("/_app/_site/overview")({
  validateSearch: z.object({
    
    period: z.enum(["today", "week", "month", "year"]).optional(),
  }),
  component: OverviewDashboard,
});
