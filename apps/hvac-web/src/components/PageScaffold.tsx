import type { ReactNode } from 'react';
import { PageContainer } from '@ant-design/pro-components';
import { Typography } from 'antd';
import './OperationsUI.css';

interface PageScaffoldProps {
  title: ReactNode;
  /** Optional authoritative heading node, used when the route owns focus restoration. */
  heading?: ReactNode;
  /** Right-side slot for context, filters, and primary actions. */
  extra?: ReactNode;
  /** Optional root class for fixed or specialized operational workspaces. */
  className?: string;
  children: ReactNode;
}

/** Thin HVAC wrapper around Ant Design Pro PageContainer. */
export default function PageScaffold({
  title,
  heading,
  extra,
  className,
  children,
}: PageScaffoldProps) {
  const titleNode = heading ?? (
    <Typography.Title level={2} className="ops-page-title">
      {title}
    </Typography.Title>
  );

  return (
    <PageContainer
      className={['ops-page', className].filter(Boolean).join(' ')}
      header={{
        className: 'ops-page-header',
        title: <div className="ops-page-heading">{titleNode}</div>,
        extra: extra ? <div className="ops-page-actions">{extra}</div> : undefined,
      }}
    >
      <main className="ops-page-content">{children}</main>
    </PageContainer>
  );
}
