import { createFileRoute } from "@tanstack/react-router";
import { OverviewDashboard } from "@/features/overview/components/OverviewDashboard";
import { z } from "zod";

export const Route = createFileRoute("/_app/overview")({
  validateSearch: z.object({
    scope: z.string().optional(),
    period: z.enum(["today", "week", "month", "year"]).optional(),
  }),
  component: OverviewDashboard,
});
