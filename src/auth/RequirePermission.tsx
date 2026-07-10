import type { ReactNode } from 'react';
import { can, type PermissionAction, type PermissionSubject } from '@/auth/permissions';
import { useUi } from '@/store/ui';
import { AccessDenied } from '@/components/PageState';

interface RequirePermissionProps {
  action?: PermissionAction;
  subject: PermissionSubject;
  children: ReactNode;
}

export default function RequirePermission({ action = 'view', subject, children }: RequirePermissionProps) {
  const role = useUi((state) => state.role);
  if (!can(role, action, subject)) {
    return <AccessDenied role={role} subject={subject} />;
  }
  return <>{children}</>;
}
