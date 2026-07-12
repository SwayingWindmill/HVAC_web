import type { ReactNode } from 'react';
import { Typography } from 'antd';
import './OperationsUI.css';

interface PageScaffoldProps {
  title: ReactNode;
  subtitle?: ReactNode;
  /** Right-side slot for context, filters, and primary actions. */
  extra?: ReactNode;
  /** Short operational context shown above the title. */
  eyebrow?: ReactNode;
  /** Optional root class for fixed or specialized operational workspaces. */
  className?: string;
  children: ReactNode;
}

/** Shared application page shell for all operational workspaces outside BigScreen. */
export default function PageScaffold({
  title,
  subtitle,
  extra,
  eyebrow = '运营工作台',
  className,
  children,
}: PageScaffoldProps) {
  return (
    <div className={['ops-page', className].filter(Boolean).join(' ')}>
      <header className="ops-page-header">
        <div className="ops-page-heading">
          <div className="ops-page-eyebrow">{eyebrow}</div>
          <Typography.Title level={2} className="ops-page-title">
            {title}
          </Typography.Title>
          {subtitle ? (
            <Typography.Text className="ops-page-subtitle">
              {subtitle}
            </Typography.Text>
          ) : null}
        </div>
        {extra ? <div className="ops-page-actions">{extra}</div> : null}
      </header>
      <main className="ops-page-content">{children}</main>
    </div>
  );
}
