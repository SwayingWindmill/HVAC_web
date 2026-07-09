import type { ReactNode } from 'react';
import { Typography } from 'antd';

interface PageScaffoldProps {
  title: ReactNode;
  subtitle?: ReactNode;
  /** right-side slot — filter bar / action buttons */
  extra?: ReactNode;
  children: ReactNode;
}

/**
 * Reusable v1 page skeleton (established by the /assets ticket #14, reused by
 * /energy /cost /ai /system). Consistent header (title + subtitle) with a right
 * slot for filters/actions, then a content area. Visual language follows the
 * teal-single-accent + semantic-status tokens in src/theme/tokens.ts.
 */
export default function PageScaffold({ title, subtitle, extra, children }: PageScaffoldProps) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      <div
        style={{
          display: 'flex',
          alignItems: 'flex-start',
          justifyContent: 'space-between',
          gap: 16,
          flexWrap: 'wrap',
        }}
      >
        <div>
          <Typography.Title level={4} style={{ margin: 0 }}>
            {title}
          </Typography.Title>
          {subtitle && (
            <Typography.Text type="secondary" style={{ fontSize: 13 }}>
              {subtitle}
            </Typography.Text>
          )}
        </div>
        {extra && (
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>{extra}</div>
        )}
      </div>
      {children}
    </div>
  );
}
