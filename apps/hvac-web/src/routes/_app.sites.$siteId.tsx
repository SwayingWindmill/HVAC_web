import { Outlet, createFileRoute } from '@tanstack/react-router';
import { requireSite } from '@/app/route-access';

export const Route = createFileRoute('/_app/sites/$siteId')({
  beforeLoad: ({ context, params }) => requireSite(context.runtime, params.siteId),
  component: Outlet,
});
