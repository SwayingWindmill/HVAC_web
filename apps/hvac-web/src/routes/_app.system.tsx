import { useCallback } from 'react';
import { createFileRoute } from '@tanstack/react-router';
import { fallback, zodValidator } from '@tanstack/zod-adapter';
import { z } from 'zod';
import { SystemManagement } from '@/features/system/SystemManagement';
import { requireCapabilities, requirePlatform } from '@/app/route-access';
import { useShellRuntime, useShellSnapshot } from '@/app/ShellRuntimeContext';
import type { ProtectedScopeDraft } from '@/app/protected-scope';

const systemSearchSchema = z.object({
  tab: fallback(z.enum(['overview', 'users', 'site', 'registry', 'integrations', 'rules', 'audit']), 'overview').optional(),
});

export const Route = createFileRoute('/_app/system')({
  validateSearch: zodValidator(systemSearchSchema),
  beforeLoad: ({ context }) => {
    requireCapabilities(context.runtime, ['site.list']);
    requirePlatform(context.runtime);
  },
  staticData: {
    title: '系统管理',
    scope: 'platform',
    requiredCapabilities: ['site.list'],
    requiresPlatform: true,
    navigation: { id: 'system', label: '系统管理', group: 'system', order: 910 },
  },
  component: SystemRoute,
});

function SystemRoute() {
  const runtime = useShellRuntime();
  const registerUnsavedDraft = useCallback(
    (draft: ProtectedScopeDraft) => runtime.registerUnsavedDraft(draft),
    [runtime],
  );
  return (
    <SystemManagement
      snapshot={useShellSnapshot()}
      registerUnsavedDraft={registerUnsavedDraft}
    />
  );
}
