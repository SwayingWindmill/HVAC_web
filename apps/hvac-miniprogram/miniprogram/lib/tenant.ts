import { GatewayError, type Gateway } from './gateway';

export interface TenantMembership {
  readonly tenantId: string;
  readonly code: string;
  readonly displayName: string;
}

export interface PrincipalTenantResponse {
  readonly context?: { readonly tenantId?: string };
  readonly availableTenants?: readonly TenantMembership[];
}

export interface TenantState {
  readonly activeTenantId: string | null;
  readonly activeTenantName: string;
  readonly activeIndex: number;
  readonly tenants: readonly TenantMembership[];
  readonly canSwitch: boolean;
}

export interface TenantSwitchResult {
  readonly switched: boolean;
  readonly state?: TenantState;
  readonly principal?: PrincipalTenantResponse;
  readonly message: string;
}

const TENANT_SWITCH_PATH = '/api/v1/auth/session/tenant';

export function readTenantState(principal: PrincipalTenantResponse): TenantState {
  const tenants = Array.isArray(principal.availableTenants) ? principal.availableTenants : [];
  const activeTenantId = principal.context?.tenantId ?? tenants[0]?.tenantId ?? null;
  const matchedIndex = activeTenantId ? tenants.findIndex((item) => item.tenantId === activeTenantId) : -1;
  const activeIndex = matchedIndex >= 0 ? matchedIndex : 0;
  const active = tenants[activeIndex];
  return {
    activeTenantId: active?.tenantId ?? activeTenantId,
    activeTenantName: active?.displayName ?? '',
    activeIndex,
    tenants,
    canSwitch: tenants.length > 1,
  };
}

export async function switchActiveTenant(
  gateway: Gateway,
  tenantId: string,
  clearCache: () => void,
): Promise<TenantSwitchResult> {
  try {
    const nextPrincipal = await gateway.post<PrincipalTenantResponse>(TENANT_SWITCH_PATH, { tenantId });
    clearCache();
    const state = readTenantState(nextPrincipal);
    return {
      switched: true,
      state,
      principal: nextPrincipal,
      message: state.activeTenantName ? `已切换到 ${state.activeTenantName}` : '已切换组织',
    };
  } catch (error) {
    if (error instanceof GatewayError && error.code === 'TENANT_SWITCH_FORBIDDEN') {
      return { switched: false, message: '你当前没有这个组织的访问权限。' };
    }
    return { switched: false, message: '暂时切换不了组织，请稍后再试。' };
  }
}
