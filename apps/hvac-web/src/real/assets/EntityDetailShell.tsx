import { useEffect, useRef, type ReactNode } from 'react';
import { Button, Drawer, Space, Typography } from 'antd';
import { ReloadOutlined } from '@ant-design/icons';

export type EntityDetailState = 'closed' | 'visible' | 'not-visible';

interface EntityDetailShellProps {
  readonly state: EntityDetailState;
  readonly title: string;
  readonly headingId: string;
  readonly testId: string;
  readonly refreshTestId?: string;
  readonly closeTestId?: string;
  readonly refreshing: boolean;
  readonly onRefresh: () => void;
  readonly onClose: () => void;
  readonly footerActions?: ReactNode;
  readonly notVisible: ReactNode;
  readonly children: ReactNode;
}

export function EntityDetailShell({
  state,
  title,
  headingId,
  testId,
  refreshTestId,
  closeTestId,
  refreshing,
  onRefresh,
  onClose,
  footerActions,
  notVisible,
  children,
}: EntityDetailShellProps) {
  const titleRef = useRef<HTMLHeadingElement | null>(null);

  useEffect(() => {
    if (state === 'closed') return;
    const handle = window.requestAnimationFrame(() => titleRef.current?.focus({ preventScroll: true }));
    return () => window.cancelAnimationFrame(handle);
  }, [state, title]);

  return (
    <Drawer
      width={760}
      rootClassName="ops-detail-drawer"
      open={state !== 'closed'}
      onClose={onClose}
      destroyOnHidden
      title={(
        <Typography.Title id={headingId} ref={titleRef} tabIndex={-1} level={4} style={{ margin: 0 }}>
          {title}
        </Typography.Title>
      )}
      footer={(
        <Space wrap style={{ width: '100%', justifyContent: 'flex-end' }}>
          {footerActions}
          <Button data-testid={refreshTestId ?? `${testId}-refresh`} icon={<ReloadOutlined />} loading={refreshing} disabled={state !== 'visible'} onClick={onRefresh}>刷新</Button>
          <Button data-testid={closeTestId ?? `${testId}-close`} onClick={onClose}>关闭</Button>
        </Space>
      )}
      data-testid={testId}
      data-detail-state={state}
    >
      {state === 'not-visible' ? notVisible : state === 'visible' ? children : null}
    </Drawer>
  );
}
