import { createFileRoute } from "@tanstack/react-router";
import { z } from "zod";
import { BenchmarkingWorkspace } from "@/features/energy-analysis/benchmarking/components/BenchmarkingWorkspace";
export const Route = createFileRoute("/_app/energy-analysis/benchmarking")({
  validateSearch: z.object({
    scope: z.string().optional(),
    view: z.enum(["peers", "weather"]).optional(),
    q: z.string().optional(),
  }),
  component: BenchmarkingWorkspace,
});
