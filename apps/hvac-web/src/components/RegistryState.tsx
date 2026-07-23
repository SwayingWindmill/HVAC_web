import { Button, Empty, Space, Typography } from 'antd';
import { ReloadOutlined } from '@ant-design/icons';
import { ErrorState } from './PageState';
import { presentRegistryError } from '@/api/registry';

interface RegistryFailureStateProps {
  error: unknown;
  onRetry?: () => void;
  compact?: boolean;
}

export function RegistryFailureState({ error, onRetry, compact = false }: RegistryFailureStateProps) {
  const state = presentRegistryError(error);
  return (
    <div data-testid="registry-failure-state" data-registry-error-kind={state.kind}>
      <ErrorState
        title={state.title}
        description={(
          <Space direction="vertical" size={4}>
            <span>{state.description}</span>
            {state.traceId ? <Typography.Text code>trace {state.traceId}</Typography.Text> : null}
            {compact ? null : <Typography.Text type="secondary">真实模式未使用本地演示数据替代该结果。</Typography.Text>}
          </Space>
        )}
        action={state.retryable && onRetry ? (
          <Button icon={<ReloadOutlined />} onClick={onRetry}>重试</Button>
        ) : undefined}
      />
    </div>
  );
}

interface RegistryEmptyStateProps {
  description: string;
}

export function RegistryEmptyState({ description }: RegistryEmptyStateProps) {
  return <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={description} />;
}

interface RegistryLoadMoreProps {
  hasMore: boolean;
  loading: boolean;
  onLoadMore: () => void;
  label?: string;
}

export function RegistryLoadMore({ hasMore, loading, onLoadMore, label = '加载更多' }: RegistryLoadMoreProps) {
  if (!hasMore) return null;
  return (
    <div style={{ display: 'flex', justifyContent: 'center', paddingTop: 12 }}>
      <Button loading={loading} onClick={onLoadMore}>{label}</Button>
    </div>
  );
}
