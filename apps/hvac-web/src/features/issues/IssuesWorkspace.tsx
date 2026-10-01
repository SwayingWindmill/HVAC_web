import { useCallback } from 'react';
import type { CurrentPrincipalResponse, Site } from '@/api/generated/platformGateway.gen';
import type { HvacRouterContext } from '@/app/router-context';
import type { ProtectedScopeDraft, ProtectedScopeResource } from '@/app/protected-scope';
import { Main } from '@/components/layout/Main';
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from '@/components/ui/empty';
import {
  AlarmCenterWorkbench,
  type AlarmCenterSearchState,
} from '@/features/alarms/AlarmCenterWorkbench';

export interface IssuesWorkspaceSearchState extends AlarmCenterSearchState {}

interface IssuesWorkspaceProps {
  readonly site: Readonly<Site>;
  readonly principal: CurrentPrincipalResponse;
  readonly runtime: HvacRouterContext['runtime'];
  readonly searchState: IssuesWorkspaceSearchState;
  readonly onSearchChange: (patch: Partial<IssuesWorkspaceSearchState>) => void;
}

export function IssuesWorkspace({
  site,
  principal,
  runtime,
  searchState,
  onSearchChange,
}: IssuesWorkspaceProps) {
  const canReadAlarms = principal.authorization.capabilities.includes('alarm.list');

  const registerUnsavedDraft = useCallback(
    (draft: ProtectedScopeDraft) => runtime.registerUnsavedDraft(draft),
    [runtime],
  );
  const registerProtectedResource = useCallback(
    (resource: ProtectedScopeResource) => runtime.registerProtectedResource(resource),
    [runtime],
  );

  return (
    <div data-testid="issues-workspace">
      {canReadAlarms ? (
        <AlarmCenterWorkbench
          site={site}
          principal={principal}
          searchState={searchState}
          onSearchChange={(patch) => onSearchChange(patch)}
          registerUnsavedDraft={registerUnsavedDraft}
          registerProtectedResource={registerProtectedResource}
        />
      ) : (
        <Main fluid>
          <Empty className="min-h-[420px] rounded-lg border">
            <EmptyHeader>
              <EmptyTitle>当前账号无法读取告警与诊断</EmptyTitle>
              <EmptyDescription>该工作区以 Alarm 为问题入口，需要 alarm.list 权限。</EmptyDescription>
            </EmptyHeader>
          </Empty>
        </Main>
      )}
    </div>
  );
}
