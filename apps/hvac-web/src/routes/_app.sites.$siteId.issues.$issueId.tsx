import { createFileRoute, getRouteApi } from '@tanstack/react-router';

import { IssueDetail } from '@/features/issues/IssueDetail';

const issuesRoute = getRouteApi('/_app/sites/$siteId/issues');

export const Route = createFileRoute('/_app/sites/$siteId/issues/$issueId')({
  component: IssueDetailRoute,
});

function IssueDetailRoute() {
  const { site, principal } = Route.useRouteContext();
  const { issueId } = Route.useParams();
  const search = issuesRoute.useSearch();
  const navigate = issuesRoute.useNavigate();

  return (
    <section
      className="real-route-surface"
      data-route-state="READY"
      data-site-id={site.id}
      data-site-route="issue-detail"
      aria-label="问题详情"
    >
      <IssueDetail
        site={site}
        principal={principal}
        issueId={issueId}
        onBack={() => {
          void navigate({
            to: '/sites/$siteId/issues',
            params: { siteId: site.id },
            search,
          });
        }}
      />
    </section>
  );
}
