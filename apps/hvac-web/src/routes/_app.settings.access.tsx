import { Suspense } from 'react';
import { createFileRoute } from '@tanstack/react-router';
import { RouteLoading } from '@/app/RouteLoading';
import { AccessControlAuditWorkspace } from '@/features/access-control/AccessControlAuditWorkspace';

export const Route = createFileRoute('/_app/settings/access')({
  staticData: {
    title: '用户、权限与审计',
    scope: 'platform',
    navigation: {
      id: 'settings-access',
      label: '用户、权限与审计',
      group: 'system',
      order: 95,
    },
  },
  component: AccessRoute,
});

function AccessRoute() {
  return (
    <Suspense fallback={<RouteLoading label="正在加载用户与权限" />}>
      <AccessControlAuditWorkspace />
    </Suspense>
  );
}
