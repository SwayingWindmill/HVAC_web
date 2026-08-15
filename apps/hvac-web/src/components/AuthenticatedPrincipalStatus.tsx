import { useEffect, useState } from 'react';
import { Badge, Button, Space, Typography } from 'antd';
import { LoginOutlined, LogoutOutlined, ReloadOutlined } from '@ant-design/icons';
import { useQueryClient } from '@tanstack/react-query';
import {
  PlatformApiError,
  createPlatformGatewayClient,
  type CurrentPrincipalResponse,
} from '@/api/generated/platformGateway.gen';
import { purgeTelemetryCurrentState } from '@/api/telemetry-current';
import { STATUS } from '@/theme/tokens';

const { Text } = Typography;
const client = createPlatformGatewayClient();

type PrincipalSnapshot =
  | { state: 'checking' }
  | { state: 'anonymous' }
  | { state: 'authenticated'; principal: CurrentPrincipalResponse }
  | { state: 'error'; detail: string };

export default function AuthenticatedPrincipalStatus() {
  const queryClient = useQueryClient();
  const [refreshKey, setRefreshKey] = useState(0);
  const [snapshot, setSnapshot] = useState<PrincipalSnapshot>({ state: 'checking' });
  const [loggingOut, setLoggingOut] = useState(false);

  useEffect(() => {
    const controller = new AbortController();
    setSnapshot({ state: 'checking' });
    client.getCurrentPrincipal({ signal: controller.signal })
      .then((response) => setSnapshot({ state: 'authenticated', principal: response.data }))
      .catch((error: unknown) => {
        if (controller.signal.aborted) return;
        if (error instanceof PlatformApiError && ['AUTHENTICATION_REQUIRED', 'SESSION_INVALID'].includes(error.problem.code)) {
          setSnapshot({ state: 'anonymous' });
          return;
        }
        if (error instanceof PlatformApiError) {
          setSnapshot({ state: 'error', detail: `${error.problem.code} · trace ${error.problem.traceId.slice(0, 8)}` });
          return;
        }
        setSnapshot({ state: 'error', detail: '身份服务不可达或响应不符合公共契约' });
      });
    return () => controller.abort();
  }, [refreshKey]);

  const login = () => window.location.assign(client.loginUrl({ returnTo: '/system' }));
  const logout = async () => {
    if (snapshot.state !== 'authenticated') return;
    setLoggingOut(true);
    try {
      await client.logout(snapshot.principal.session.csrfToken);
      purgeTelemetryCurrentState(queryClient);
      setSnapshot({ state: 'anonymous' });
    } finally {
      setLoggingOut(false);
    }
  };

  const authenticated = snapshot.state === 'authenticated';
  const checking = snapshot.state === 'checking';
  const detail = authenticated
    ? `${snapshot.principal.principal.displayName} · roles ${snapshot.principal.principal.roles.join(', ') || 'none'} · org ${snapshot.principal.context.tenantId} · IAM policy ${snapshot.principal.authorization.policyRevision} · capabilities ${snapshot.principal.authorization.capabilities.length}: ${snapshot.principal.authorization.capabilities.join(', ') || 'none'}`
    : snapshot.state === 'anonymous'
      ? '浏览器仅持有 HttpOnly Secure BFF Session Cookie'
      : snapshot.state === 'error'
        ? snapshot.detail
        : '/api/v1/principal';

  return (
    <div
      className="system-health-row"
      data-testid="authenticated-principal-status"
      data-principal-state={snapshot.state}
      data-policy-revision={authenticated ? snapshot.principal.authorization.policyRevision : undefined}
      data-capability-count={authenticated ? snapshot.principal.authorization.capabilities.length : undefined}
      aria-label={`Authenticated Principal ${snapshot.state}`}
    >
      <Badge color={checking ? STATUS.info : authenticated ? STATUS.ok : snapshot.state === 'anonymous' ? STATUS.warn : STATUS.err} />
      <div className="system-health-copy">
        <div className="system-health-title">
          <Text strong>Authenticated Principal</Text>
          <Text type="secondary">OIDC + PKCE · Gateway → IAM mTLS</Text>
        </div>
        <Text type="secondary">{detail}</Text>
      </div>
      <div className="system-health-value">
        {authenticated ? (
          <Space size={4}>
            <Text strong>已认证</Text>
            <Button type="text" size="small" icon={<LogoutOutlined />} loading={loggingOut} onClick={logout} aria-label="退出平台会话" />
          </Space>
        ) : snapshot.state === 'anonymous' ? (
          <Button type="link" size="small" icon={<LoginOutlined />} onClick={login}>登录</Button>
        ) : (
          <Button type="text" size="small" icon={<ReloadOutlined />} loading={checking} onClick={() => setRefreshKey((value) => value + 1)} aria-label="重新检查身份状态" />
        )}
      </div>
    </div>
  );
}
