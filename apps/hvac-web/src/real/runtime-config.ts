export const SUPPORTED_REALTIME_PROTOCOL = 'centrifugo-v1' as const;

export interface RealRuntimeConfig {
  buildTarget: 'real';
  buildId: string;
  gatewayBasePath: string;
  realtimeProtocol: typeof SUPPORTED_REALTIME_PROTOCOL;
}

export interface RealRuntimeConfigFailure {
  code: 'INVALID_BUILD_TARGET' | 'MISSING_BUILD_ID' | 'INVALID_GATEWAY_BASE_PATH' | 'UNSUPPORTED_REALTIME_PROTOCOL';
  detail: string;
}

export type RealRuntimeConfigResult =
  | { ok: true; config: RealRuntimeConfig }
  | { ok: false; failures: RealRuntimeConfigFailure[] };

function isSameOriginRelativePath(value: string) {
  if (!value.startsWith('/') || value.startsWith('//')) return false;
  try {
    const resolved = new URL(value, window.location.origin);
    return resolved.origin === window.location.origin
      && resolved.pathname === value
      && !resolved.search
      && !resolved.hash;
  } catch {
    return false;
  }
}

export function validateRealRuntimeConfig(): RealRuntimeConfigResult {
  const failures: RealRuntimeConfigFailure[] = [];
  const buildId = __HVAC_WEB_BUILD_ID__.trim();
  const gatewayBasePath = __HVAC_WEB_GATEWAY_BASE_PATH__.trim().replace(/\/$/, '');
  const realtimeProtocol = __HVAC_WEB_REALTIME_PROTOCOL__.trim();

  if (__HVAC_WEB_BUILD_TARGET__ !== 'real') {
    failures.push({ code: 'INVALID_BUILD_TARGET', detail: 'Real entry graph was not built with the real build target.' });
  }
  if (!buildId) {
    failures.push({ code: 'MISSING_BUILD_ID', detail: 'HVAC_WEB_BUILD_ID is required for a Real artifact.' });
  }
  if (!gatewayBasePath || !isSameOriginRelativePath(gatewayBasePath) || gatewayBasePath !== '/api/v1') {
    failures.push({
      code: 'INVALID_GATEWAY_BASE_PATH',
      detail: 'HVAC_WEB_GATEWAY_BASE_PATH must be the same-origin public Gateway path /api/v1.',
    });
  }
  if (realtimeProtocol !== SUPPORTED_REALTIME_PROTOCOL) {
    failures.push({
      code: 'UNSUPPORTED_REALTIME_PROTOCOL',
      detail: `HVAC_WEB_REALTIME_PROTOCOL must be ${SUPPORTED_REALTIME_PROTOCOL}.`,
    });
  }

  if (failures.length > 0) return { ok: false, failures };

  return {
    ok: true,
    config: {
      buildTarget: 'real',
      buildId,
      gatewayBasePath,
      realtimeProtocol: SUPPORTED_REALTIME_PROTOCOL,
    },
  };
}
