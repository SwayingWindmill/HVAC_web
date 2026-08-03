import { Card, Space, Tag, Typography } from 'antd';
import { RobotOutlined } from '@ant-design/icons';
import PageScaffold from '@/components/PageScaffold';
import type { CurrentPrincipalResponse, Site } from '@/api/generated/platformGateway.gen';
import { FocusHeading } from './FocusHeading';
import { OperationsInvestigation } from './OperationsInvestigation';
import type { ProtectedScopeResource } from './protected-scope';
import './operations-investigation-page.css';

interface OperationsInvestigationPageProps {
  readonly site: Readonly<Site>;
  readonly principal: CurrentPrincipalResponse;
  readonly registerProtectedResource: (resource: ProtectedScopeResource) => () => void;
}

export function OperationsInvestigationPage(props: OperationsInvestigationPageProps) {
  return (
    <PageScaffold
      title="AI 运维调查"
      heading={(
        <FocusHeading className="ops-page-title ant-typography">
          <Space><RobotOutlined />AI 运维调查</Space>
        </FocusHeading>
      )}
      extra={<Tag color="cyan">PRIMARY AGENT EXPERIENCE</Tag>}
      className="operations-page"
    >
      <Typography.Text type="secondary">
        Site {props.site.displayName} · Investigation 列表、Plan、Evidence、Finding 与 Tool activity 均来自已提交的权威 projection。
      </Typography.Text>
      <Card variant="borderless" className="operations-workspace-card">
        <OperationsInvestigation {...props} embedded />
      </Card>
    </PageScaffold>
  );
}
