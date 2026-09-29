import { AlertTriangle } from 'lucide-react';
import type { CurrentPrincipalResponse, Site } from '@/api/generated/platformGateway.gen';
import { ALARM_LOCAL_ROUTES_ENABLED, ALARM_PUBLIC_ROUTES_ENABLED } from '@/api/alarms';
import type { ProtectedScopeDraft, ProtectedScopeResource } from '@/app/protected-scope';
import { Badge } from '@/components/ui/badge';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { AlarmCenterWorkbench, type AlarmCenterSearchState } from './AlarmCenterWorkbench';

interface AlarmsProps {
  site: Readonly<Site>;
  principal: CurrentPrincipalResponse;
  searchState: AlarmCenterSearchState;
  onSearchChange: (patch: Partial<AlarmCenterSearchState>) => void;
  registerUnsavedDraft: (draft: ProtectedScopeDraft) => () => void;
  registerProtectedResource: (resource: ProtectedScopeResource) => () => void;
}

function DisabledAlarmSurface({
  site,
  reason,
}: Pick<AlarmsProps, 'site'> & { reason: 'ROUTE_DISABLED' | 'CAPABILITY_DENIED' }) {
  const capabilityDenied = reason === 'CAPABILITY_DENIED';
  return (
    <section
      className="mx-auto flex w-full max-w-[1400px] flex-col gap-6"
      data-testid="real-alarms-disabled"
      data-business-state="DISABLED"
      data-site-id={site.id}
    >
      <header>
        <p className="text-sm text-muted-foreground">{site.displayName}</p>
        <div className="mt-1 flex items-center gap-3">
          <h1 className="text-2xl font-semibold tracking-tight">告警</h1>
          <Badge variant="outline">{capabilityDenied ? '无告警查看权限' : '告警能力未启用'}</Badge>
        </div>
      </header>

      <Card className="max-w-2xl shadow-none">
        <CardHeader>
          <div className="mb-1 grid size-9 place-items-center rounded-md bg-warning/10 text-warning">
            <AlertTriangle className="size-4" />
          </div>
          <CardTitle>{capabilityDenied ? '无告警查看权限' : '告警服务未启用'}</CardTitle>
          <CardDescription>
            {capabilityDenied
              ? '当前账号暂无告警查看权限，如需访问请联系系统管理员开通。'
              : '当前站点未启用告警监视服务。'}
          </CardDescription>
        </CardHeader>
        <CardContent className="border-t pt-4">
          <div className="flex items-center justify-between text-sm">
            <span className="text-muted-foreground">当前站点</span>
            <strong className="font-medium">{site.displayName}</strong>
          </div>
        </CardContent>
      </Card>
    </section>
  );
}

export function Alarms(props: AlarmsProps) {
  if (!ALARM_PUBLIC_ROUTES_ENABLED && !ALARM_LOCAL_ROUTES_ENABLED) {
    return <DisabledAlarmSurface site={props.site} reason="ROUTE_DISABLED" />;
  }
  if (!props.principal.authorization.capabilities.includes('alarm.list')) {
    return <DisabledAlarmSurface site={props.site} reason="CAPABILITY_DENIED" />;
  }
  return <AlarmCenterWorkbench {...props} />;
}
