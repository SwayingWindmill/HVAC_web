import { useCallback } from 'react';
import type { CurrentPrincipalResponse, Site } from '@/api/generated/platformGateway.gen';
import type { HvacRouterContext } from '@/app/router-context';
import type { ProtectedScopeDraft, ProtectedScopeResource } from '@/app/protected-scope';
import { Main } from '@/components/layout/Main';
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from '@/components/ui/empty';
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs';
import {
  AlarmCenterWorkbench,
  type AlarmCenterSearchState,
} from '@/features/alarms/AlarmCenterWorkbench';
import {
  DiagnosticsWorkspace,
  type DiagnosticsSearchState,
} from '@/features/diagnostics/DiagnosticsWorkspace';

export type IssuesWorkspaceView = 'alarms' | 'diagnostics';

export interface IssuesWorkspaceSearchState
  extends AlarmCenterSearchState, DiagnosticsSearchState {
  readonly view?: IssuesWorkspaceView;
}

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
  const view = searchState.view ?? (canReadAlarms ? 'alarms' : 'diagnostics');

  const registerUnsavedDraft = useCallback(
    (draft: ProtectedScopeDraft) => runtime.registerUnsavedDraft(draft),
    [runtime],
  );
  const registerProtectedResource = useCallback(
    (resource: ProtectedScopeResource) => runtime.registerProtectedResource(resource),
    [runtime],
  );

  return (
    <div data-testid="issues-workspace" data-workspace-view={view}>
      <Main fluid className="pb-0 pt-4">
        <Tabs
          value={view}
          onValueChange={(value) => {
            onSearchChange({
              view: value as IssuesWorkspaceView,
              selected: undefined,
              diagnosis: undefined,
            });
          }}
        >
          <TabsList aria-label="告警与诊断工作区视图" className="h-9">
            <TabsTrigger value="alarms">告警</TabsTrigger>
            <TabsTrigger value="diagnostics">诊断</TabsTrigger>
          </TabsList>
        </Tabs>
      </Main>

      {view === 'diagnostics' ? (
        <DiagnosticsWorkspace
          site={site}
          principal={principal}
          runtime={runtime}
          searchState={searchState}
          onSearchChange={(patch) => onSearchChange(patch)}
        />
      ) : canReadAlarms ? (
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
              <EmptyTitle>当前账号无法读取告警</EmptyTitle>
              <EmptyDescription>可以继续使用“诊断”视图；告警队列需要 alarm.list 权限。</EmptyDescription>
            </EmptyHeader>
          </Empty>
        </Main>
      )}
    </div>
  );
}
