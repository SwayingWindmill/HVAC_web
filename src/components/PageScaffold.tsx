import type { ReactNode } from 'react';
import { Typography } from 'antd';
import './OperationsUI.css';

interface PageScaffoldProps {
  title: ReactNode;
  /** Right-side slot for context, filters, and primary actions. */
  extra?: ReactNode;
  /** Optional root class for fixed or specialized operational workspaces. */
  className?: string;
  children: ReactNode;
}

/** Shared application page shell for all operational workspaces outside BigScreen. */
export default function PageScaffold({
  title,
  extra,
  className,
  children,
}: PageScaffoldProps) {
  return (
    <div className={['ops-page', className].filter(Boolean).join(' ')}>
      <header className="ops-page-header">
        <div className="ops-page-heading">
          <Typography.Title level={2} className="ops-page-title">
            {title}
          </Typography.Title>
        </div>
        {extra ? <div className="ops-page-actions">{extra}</div> : null}
      </header>
      <main className="ops-page-content">{children}</main>
    </div>
  );
}
