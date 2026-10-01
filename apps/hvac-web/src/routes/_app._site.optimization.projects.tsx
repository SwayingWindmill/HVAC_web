import { createFileRoute } from "@tanstack/react-router";
import { z } from "zod";
import { ProjectsWorkspace } from "@/features/projects/components/ProjectsWorkspace";
export const Route = createFileRoute("/_app/_site/optimization/projects")({
  validateSearch: z.object({
    
    inspect: z.string().optional(),
    opportunity: z.string().optional(),
    q: z.string().optional(),
    stage: z.string().optional(),
    view: z.enum(["list", "board"]).optional(),
  }),
  component: ProjectsWorkspace,
});
