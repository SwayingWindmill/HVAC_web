import { useEffect, useState } from 'react';
import { Badge, Button, Space, Typography } from 'antd';
import { ReloadOutlined } from '@ant-design/icons';
import {
  PlatformApiError,
  createPlatformGatewayClient,
  type HealthResponse,
} from '@/api/generated/platformGateway.gen';
import { STATUS } from '@/theme/tokens';

const { Text } = Typography;
const platformGatewayClient = createPlatformGatewayClient();

type GatewaySnapshot =
  | { state: 'checking' }
  | { state: 'online'; health: HealthResponse; traceparent: string | null }
  | { state: 'offline'; detail: string };

export default function PlatformGatewayStatus() {
  const [refreshKey, setRefreshKey] = useState(0);
  const [snapshot, setSnapshot] = useState<GatewaySnapshot>({ state: 'checking' });

  useEffect(() => {
    const controller = new AbortController();
    setSnapshot({ state: 'checking' });
    platformGatewayClient
      .getHealth({ includeBuild: true }, { signal: controller.signal })
      .then((response) => {
        setSnapshot({ state: 'online', health: response.data, traceparent: response.traceparent });
      })
      .catch((error: unknown) => {
        if (controller.signal.aborted) return;
        if (error instanceof PlatformApiError) {
          setSnapshot({ state: 'offline', detail: `${error.problem.code} · trace ${error.problem.traceId.slice(0, 8)}` });
          return;
        }
        setSnapshot({ state: 'offline', detail: 'Gateway 不可达或响应不符合公共契约' });
      });
    return () => controller.abort();
  }, [refreshKey]);

  const online = snapshot.state === 'online';
  const checking = snapshot.state === 'checking';
  const statusLabel = checking ? '检查中' : online ? '在线' : '离线';
  const traceId = online ? snapshot.traceparent?.split('-')[1]?.slice(0, 8) : undefined;
  const detail = online
    ? `${snapshot.health.build?.version ?? 'dev'} · trace ${traceId ?? 'unknown'}`
    : checking
      ? '/api/v1/health'
      : snapshot.detail;

  return (
    <div
      className="system-health-row"
      data-testid="platform-gateway-status"
      data-platform-state={snapshot.state}
      aria-label={`Platform Gateway ${statusLabel}`}
    >
      <Badge color={checking ? STATUS.info : online ? STATUS.ok : STATUS.err} />
      <div className="system-health-copy">
        <div className="system-health-title">
          <Text strong>Platform Gateway</Text>
          <Text type="secondary">公共入口 · Go</Text>
        </div>
        <Text type="secondary">{detail}</Text>
      </div>
      <div className="system-health-value">
        <Text strong>{statusLabel}</Text>
        {online ? (
          <Text type="secondary">契约已校验</Text>
        ) : (
          <Space size={4}>
            <Button
              type="text"
              size="small"
              icon={<ReloadOutlined />}
              loading={checking}
              onClick={() => setRefreshKey((value) => value + 1)}
              aria-label="重新检查 Platform Gateway"
            />
          </Space>
        )}
      </div>
    </div>
  );
}
