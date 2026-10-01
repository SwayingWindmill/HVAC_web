import { createFileRoute } from "@tanstack/react-router";
import { z } from "zod";
import { ConfigurationWorkspace } from "@/features/system-settings/ConfigurationWorkspace";
export const Route = createFileRoute("/_app/_site/settings/")({
  validateSearch: z.object({
    
    view: z.enum(["site", "tariff"]).optional(),
  }),
  component: ConfigurationWorkspace,
});
