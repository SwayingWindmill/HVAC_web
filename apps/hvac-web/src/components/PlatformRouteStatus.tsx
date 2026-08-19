import { useEffect, useState } from 'react';
import { Badge, Button, Typography } from 'antd';
import { ReloadOutlined } from '@ant-design/icons';
import {
  PlatformApiError,
  createPlatformGatewayClient,
  type PlatformStatusResponse,
} from '@/api/generated/platformGateway.gen';
import { STATUS } from '@/theme/tokens';

const { Text } = Typography;
const client = createPlatformGatewayClient();

type Snapshot =
  | { state: 'checking' }
  | { state: 'anonymous' }
  | { state: 'ready'; data: PlatformStatusResponse }
  | { state: 'error'; detail: string };

export default function PlatformRouteStatus() {
  const [refreshKey, setRefreshKey] = useState(0);
  const [snapshot, setSnapshot] = useState<Snapshot>({ state: 'checking' });

  useEffect(() => {
    const controller = new AbortController();
    setSnapshot({ state: 'checking' });
    void client.getPlatformStatus({ signal: controller.signal })
      .then((response) => setSnapshot({ state: 'ready', data: response.data }))
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
        setSnapshot({ state: 'error', detail: 'Route Ownership 响应不符合生成契约' });
      });
    return () => controller.abort();
  }, [refreshKey]);

  const detail = snapshot.state === 'ready'
    ? `${snapshot.data.implementation.toUpperCase()} · policy r${snapshot.data.routePolicyRevision} · route r${snapshot.data.routeRevision} · ${snapshot.data.compatibilityMode}`
    : snapshot.state === 'anonymous'
      ? '登录后由服务端 Tenant + Principal 计算稳定 cohort'
      : snapshot.state === 'error'
        ? snapshot.detail
        : 'Route Ownership Registry 正在解析唯一 owner';
  const label = snapshot.state === 'ready'
    ? snapshot.data.implementation === 'legacy' ? 'Legacy 只读' : 'Go 原生'
    : snapshot.state === 'anonymous'
      ? '未认证'
      : snapshot.state === 'error'
        ? '异常'
        : '检查中';

  return (
    <div
      className="system-health-row"
      data-testid="platform-route-status"
      data-route-state={snapshot.state}
      data-route-implementation={snapshot.state === 'ready' ? snapshot.data.implementation : ''}
      data-route-policy-revision={snapshot.state === 'ready' ? snapshot.data.routePolicyRevision : ''}
      aria-label={`Platform route ${snapshot.state}`}
    >
      <Badge color={snapshot.state === 'ready' ? STATUS.ok : snapshot.state === 'error' ? STATUS.err : STATUS.info} />
      <div className="system-health-copy">
        <div className="system-health-title">
          <Text strong>Route Ownership</Text>
          <Text type="secondary">Gateway · stable cohort · anti-corruption</Text>
        </div>
        <Text type="secondary">{detail}</Text>
      </div>
      <div className="system-health-value">
        <Text strong>{label}</Text>
        <Button
          type="text"
          size="small"
          icon={<ReloadOutlined />}
          onClick={() => setRefreshKey((value) => value + 1)}
          aria-label="重新检查 Route Ownership 状态"
        />
      </div>
    </div>
  );
}
