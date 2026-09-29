import { Navigate, createFileRoute } from '@tanstack/react-router';

export const Route = createFileRoute('/_app/sites/$siteId/')({
  component: SiteIndexRoute,
});

function SiteIndexRoute() {
  const { siteId } = Route.useParams();
  return <Navigate to="/sites/$siteId/overview" params={{ siteId }} replace />;
}
