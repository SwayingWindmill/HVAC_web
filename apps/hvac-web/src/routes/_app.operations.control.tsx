import { createFileRoute } from "@tanstack/react-router";
import { z } from "zod";
import { ControlWorkspace } from "@/features/control/components/ControlWorkspace";
export const Route = createFileRoute("/_app/operations/control")({
  validateSearch: z.object({
    scope: z.string().optional(),
    view: z.enum(["commands", "strategies", "interlocks"]).optional(),
    q: z.string().optional(),
  }),
  component: ControlWorkspace,
});
