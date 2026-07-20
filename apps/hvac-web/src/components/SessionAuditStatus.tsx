import { useEffect, useState } from 'react';
import { Badge, Button, Typography } from 'antd';
import { ReloadOutlined } from '@ant-design/icons';
import {
  PlatformApiError,
  createPlatformGatewayClient,
  type AuditRecord,
} from '@/api/generated/platformGateway.gen';
import { STATUS } from '@/theme/tokens';

const { Text } = Typography;
const client = createPlatformGatewayClient();

type AuditSnapshot =
  | { state: 'checking' }
  | { state: 'anonymous' }
  | { state: 'pending'; messageId: string }
  | { state: 'recorded'; record: AuditRecord }
  | { state: 'error'; detail: string };

const MAX_POLL_ATTEMPTS = 30;
const POLL_INTERVAL_MS = 500;

export default function SessionAuditStatus() {
  const [refreshKey, setRefreshKey] = useState(0);
  const [snapshot, setSnapshot] = useState<AuditSnapshot>({ state: 'checking' });

  useEffect(() => {
    const controller = new AbortController();
    let timer: number | undefined;
    let attempt = 0;

    const poll = async () => {
      try {
        const principal = await client.getCurrentPrincipal({ signal: controller.signal });
        const messageId = principal.data.session.lastAuditMessageId;
        try {
          const audit = await client.getSessionAuditEvent(messageId, { signal: controller.signal });
          setSnapshot({ state: 'recorded', record: audit.data });
          return;
        } catch (error: unknown) {
          if (error instanceof PlatformApiError && error.problem.code === 'AUDIT_RECORD_NOT_FOUND' && attempt < MAX_POLL_ATTEMPTS) {
            attempt += 1;
            setSnapshot({ state: 'pending', messageId });
            timer = window.setTimeout(() => void poll(), POLL_INTERVAL_MS);
            return;
          }
          throw error;
        }
      } catch (error: unknown) {
        if (controller.signal.aborted) return;
        if (error instanceof PlatformApiError && ['AUTHENTICATION_REQUIRED', 'SESSION_INVALID'].includes(error.problem.code)) {
          setSnapshot({ state: 'anonymous' });
          return;
        }
        if (error instanceof PlatformApiError) {
          setSnapshot({ state: 'error', detail: `${error.problem.code} · trace ${error.problem.traceId.slice(0, 8)}` });
          return;
        }
        setSnapshot({ state: 'error', detail: 'Audit Ledger 不可达或响应不符合生成契约' });
      }
    };

    setSnapshot({ state: 'checking' });
    void poll();
    return () => {
      controller.abort();
      if (timer !== undefined) window.clearTimeout(timer);
    };
  }, [refreshKey]);

  const detail = snapshot.state === 'recorded'
    ? `${snapshot.record.action} · v${snapshot.record.aggregateVersion} · ${snapshot.record.recordHash.slice(0, 12)}`
    : snapshot.state === 'pending'
      ? `Outbox/Inbox 收敛中 · ${snapshot.messageId.slice(0, 12)}`
      : snapshot.state === 'anonymous'
        ? '登录后可验证 Session 事务对应的 append-only Audit Record'
        : snapshot.state === 'error'
          ? snapshot.detail
          : 'State + Audit Intent + Outbox → Protobuf → Transactional Inbox';

  const label = snapshot.state === 'recorded'
    ? '已记录'
    : snapshot.state === 'pending'
      ? '待收敛'
      : snapshot.state === 'anonymous'
        ? '未认证'
        : snapshot.state === 'error'
          ? '异常'
          : '检查中';

  return (
    <div
      className="system-health-row"
      data-testid="session-audit-status"
      data-audit-state={snapshot.state}
      aria-label={`Session Audit ${snapshot.state}`}
    >
      <Badge color={snapshot.state === 'recorded' ? STATUS.ok : snapshot.state === 'error' ? STATUS.err : snapshot.state === 'pending' ? STATUS.warn : STATUS.info} />
      <div className="system-health-copy">
        <div className="system-health-title">
          <Text strong>Durable Session Audit</Text>
          <Text type="secondary">PostgreSQL · Outbox · Protobuf · Transactional Inbox</Text>
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
          aria-label="重新检查 Session Audit 状态"
        />
      </div>
    </div>
  );
}
