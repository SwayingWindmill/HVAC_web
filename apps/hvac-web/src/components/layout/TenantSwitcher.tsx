import { useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { Building2, Check, ChevronsUpDown } from 'lucide-react';
import { useShellRuntime, useShellSnapshot } from '@/app/ShellRuntimeContext';
import { Button } from '@/components/ui/button';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { Spinner } from '@/components/ui/spinner';
import { notifyError } from '@/lib/toast';

export function TenantSwitcher() {
  const runtime = useShellRuntime();
  const snapshot = useShellSnapshot();
  const queryClient = useQueryClient();
  const [switching, setSwitching] = useState(false);

  if (snapshot.state !== 'READY' || !snapshot.principal) return null;

  const tenants = snapshot.principal.availableTenants ?? [];
  if (tenants.length <= 1) return null;

  const activeTenantId = snapshot.principal.context.tenantId;
  const activeTenant = tenants.find((item) => item.tenantId === activeTenantId) ?? tenants[0];
  if (!activeTenant) return null;

  const handleSelect = async (tenantId: string) => {
    if (switching || tenantId === activeTenant.tenantId) return;
    setSwitching(true);
    try {
      const outcome = await runtime.switchTenant(tenantId, {
        currentHref: window.location.href,
        onSwitched: () => {
          queryClient.clear();
        },
      });
      if (outcome.state === 'rejected') {
        notifyError(outcome.message, { onRetry: () => { void handleSelect(tenantId); } });
      }
    } finally {
      setSwitching(false);
    }
  };

  return (
    <div className="relative flex items-center gap-2">
      <span className="mx-0.5 text-xl font-light text-border" aria-hidden="true">/</span>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button
            type="button"
            variant="ghost"
            size="sm"
            disabled={switching}
            aria-label="切换所属组织"
            className="h-8 max-w-52 gap-1.5 px-2 text-sm font-medium"
          >
            {switching ? <Spinner /> : <Building2 className="size-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />}
            <span className="truncate">{switching ? '切换中…' : activeTenant.displayName}</span>
            <ChevronsUpDown className="size-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="start" className="w-64">
          <DropdownMenuLabel className="text-xs font-normal text-muted-foreground">
            所属组织
          </DropdownMenuLabel>
          <DropdownMenuSeparator />
          {tenants.map((tenant) => {
            const current = tenant.tenantId === activeTenant.tenantId;
            return (
              <DropdownMenuItem
                key={tenant.tenantId}
                disabled={switching}
                onSelect={() => {
                  void handleSelect(tenant.tenantId);
                }}
                className="flex items-center justify-between gap-2"
              >
                <div className="min-w-0">
                  <div className="truncate font-medium">{tenant.displayName}</div>
                  <div className="truncate text-xs text-muted-foreground">{tenant.code}</div>
                </div>
                {current ? <Check className="size-4 shrink-0 text-primary" aria-hidden="true" /> : null}
              </DropdownMenuItem>
            );
          })}
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  );
}
