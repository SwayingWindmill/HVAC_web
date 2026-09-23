import type { CurrentPrincipalResponse, Site } from '@/api/generated/platformGateway.gen';
import type { HvacRouterContext } from '@/app/router-context';
import { Main } from '@/components/layout/Main';
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs';
import {
  ComfortWorkspace,
  type ComfortSearchState,
} from '@/features/comfort/ComfortWorkspace';
import {
  SystemOperations,
  type SystemOperationsSearchState,
} from './SystemOperations';

export type OperationsWorkspaceView = 'systems' | 'comfort';

export interface OperationsWorkspaceSearchState
  extends SystemOperationsSearchState, ComfortSearchState {
  readonly view?: OperationsWorkspaceView;
}

interface OperationsWorkspaceProps {
  readonly site: Readonly<Site>;
  readonly principal: CurrentPrincipalResponse;
  readonly runtime: HvacRouterContext['runtime'];
  readonly searchState: OperationsWorkspaceSearchState;
  readonly onSearchChange: (patch: Partial<OperationsWorkspaceSearchState>) => void;
}

export function OperationsWorkspace({
  site,
  principal,
  runtime,
  searchState,
  onSearchChange,
}: OperationsWorkspaceProps) {
  const view = searchState.view ?? 'systems';

  return (
    <div data-testid="operations-workspace" data-workspace-view={view}>
      <Main fluid className="pb-0 pt-4">
        <Tabs
          value={view}
          onValueChange={(value) => {
            const next = value as OperationsWorkspaceView;
            onSearchChange({
              view: next,
              inspect: next === 'systems' ? undefined : searchState.inspect,
            });
          }}
        >
          <TabsList aria-label="运行工作区视图" className="h-9">
            <TabsTrigger value="systems">系统运行</TabsTrigger>
            <TabsTrigger value="comfort">空间与环境</TabsTrigger>
          </TabsList>
        </Tabs>
      </Main>

      {view === 'comfort' ? (
        <ComfortWorkspace
          site={site}
          principal={principal}
          runtime={runtime}
          searchState={searchState}
          onSearchChange={(patch) => onSearchChange(patch)}
        />
      ) : (
        <SystemOperations
          site={site}
          principal={principal}
          searchState={searchState}
          onSearchChange={(patch) => onSearchChange(patch)}
        />
      )}
    </div>
  );
}
