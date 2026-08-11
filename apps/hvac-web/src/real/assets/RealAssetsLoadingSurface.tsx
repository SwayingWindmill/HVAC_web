import { ApartmentOutlined } from '@ant-design/icons';
import { Space } from 'antd';
import PageScaffold from '@/components/PageScaffold';
import { FocusHeading } from '../FocusHeading';
import { RealRouteLoading } from '../RealRouteLoading';

export function RealAssetsLoadingSurface({ siteId }: { siteId: string }) {
  return (
    <section
      className="real-assets real-assets--loading"
      data-testid="real-site-route-assets"
      data-business-state="LOADING"
      data-site-id={siteId}
    >
      <PageScaffold
        title="设备与建筑"
        heading={(
          <FocusHeading className="ops-page-title ant-typography">
            <Space><ApartmentOutlined />设备与建筑</Space>
          </FocusHeading>
        )}
        className="assets-page"
      >
        <RealRouteLoading label="正在加载设备与建筑" siteId={siteId} />
      </PageScaffold>
    </section>
  );
}
