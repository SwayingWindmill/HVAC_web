import { useEffect, useMemo, useState } from 'react';
import {
  Alert,
  Badge,
  Button,
  Card,
  Descriptions,
  Drawer,
  Empty,
  Segmented,
  Space,
  Table,
  Tag,
  Typography,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { BellOutlined, CheckOutlined, ReloadOutlined } from '@ant-design/icons';
import PageScaffold from '@/components/PageScaffold';
import {
  listNotifications,
  markNotificationRead,
  notificationErrorMessage,
  type NotificationInboxItem,
} from '@/api/notifications';
import type { ShellSnapshot } from './shell-runtime';
import { FocusHeading } from './FocusHeading';
import { siteRoute } from './site-routing';

interface RealNotificationCenterProps {
  snapshot: ShellSnapshot;
}

const severityColor: Record<NotificationInboxItem['severity'], string> = {
  INFO: 'default',
  WARNING: 'gold',
  MINOR: 'orange',
  MAJOR: 'volcano',
  CRITICAL: 'red',
};

export function RealNotificationCenter({ snapshot }: RealNotificationCenterProps) {
  const principal = snapshot.principal!;
  const [items, setItems] = useState<NotificationInboxItem[]>([]);
  const [filter, setFilter] = useState<'unread' | 'all'>('unread');
  const [loading, setLoading] = useState(true);
  const [failure, setFailure] = useState<string | null>(null);
  const [selected, setSelected] = useState<NotificationInboxItem | null>(null);
  const [markingId, setMarkingId] = useState<string | null>(null);
  const [reloadKey, setReloadKey] = useState(0);

  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    setFailure(null);
    listNotifications(controller.signal)
      .then((notifications) => {
        setItems([...notifications].sort((left, right) => Date.parse(right.createdAt) - Date.parse(left.createdAt)));
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted) setFailure(notificationErrorMessage(error));
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    return () => controller.abort();
  }, [reloadKey]);

  const siteById = useMemo(
    () => new Map((snapshot.sites?.items ?? []).map((site) => [site.id, site])),
    [snapshot.sites?.items],
  );
  const unreadCount = items.filter((item) => item.status === 'UNREAD').length;
  const visibleItems = filter === 'unread' ? items.filter((item) => item.status === 'UNREAD') : items;

  const markRead = async (item: NotificationInboxItem) => {
    if (item.status !== 'UNREAD' || markingId) return;
    setMarkingId(item.inboxItemId);
    setFailure(null);
    try {
      const updated = await markNotificationRead(item.inboxItemId, principal.session.csrfToken);
      setItems((current) => current.map((candidate) => candidate.inboxItemId === updated.inboxItemId ? updated : candidate));
      setSelected((current) => current?.inboxItemId === updated.inboxItemId ? updated : current);
    } catch (error: unknown) {
      setFailure(notificationErrorMessage(error));
    } finally {
      setMarkingId(null);
    }
  };

  const columns: ColumnsType<NotificationInboxItem> = [
    {
      title: '状态',
      dataIndex: 'status',
      width: 100,
      render: (status: NotificationInboxItem['status']) => (
        <Badge status={status === 'UNREAD' ? 'processing' : 'default'} text={status === 'UNREAD' ? '未读' : status === 'READ' ? '已读' : '已确认'} />
      ),
    },
    {
      title: '级别',
      dataIndex: 'severity',
      width: 110,
      render: (severity: NotificationInboxItem['severity']) => <Tag color={severityColor[severity]}>{severity}</Tag>,
    },
    {
      title: '通知',
      key: 'notification',
      render: (_, item) => (
        <Space direction="vertical" size={0}>
          <Typography.Text strong={item.status === 'UNREAD'}>{item.subject}</Typography.Text>
          <Typography.Text type="secondary" ellipsis style={{ maxWidth: 560 }}>{item.body}</Typography.Text>
        </Space>
      ),
    },
    {
      title: '站点',
      dataIndex: 'siteId',
      width: 180,
      render: (siteId: string) => siteById.get(siteId)?.displayName ?? siteId,
    },
    {
      title: '时间',
      dataIndex: 'createdAt',
      width: 190,
      render: (createdAt: string) => new Date(createdAt).toLocaleString(),
    },
    {
      title: '操作',
      width: 110,
      render: (_, item) => item.status === 'UNREAD' ? (
        <Button
          type="link"
          size="small"
          icon={<CheckOutlined />}
          loading={markingId === item.inboxItemId}
          onClick={(event) => {
            event.stopPropagation();
            void markRead(item);
          }}
        >已读</Button>
      ) : null,
    },
  ];

  const selectedSite = selected ? siteById.get(selected.siteId) : undefined;

  return (
    <section data-testid="real-route-notifications" data-route-state="READY" data-business-state="POPULATED">
      <PageScaffold
        title="通知中心"
        heading={<FocusHeading className="ops-page-title ant-typography"><Space><BellOutlined />通知中心</Space></FocusHeading>}
        extra={(
          <Space>
            <Badge count={unreadCount} overflowCount={99} showZero color={unreadCount ? undefined : 'gray'} />
            <Button icon={<ReloadOutlined />} onClick={() => setReloadKey((value) => value + 1)}>刷新</Button>
          </Space>
        )}
      >
        <Space direction="vertical" size={16} style={{ width: '100%' }}>
          <Alert
            type="info"
            showIcon
            message="通知 Inbox 与 Alarm 生命周期分离"
            description="这里只展示服务器为当前 Principal 投递的通知。通知已读状态不会改变 Alarm 的 ACK、CLEAR 或工单状态。"
          />
          {failure ? <Alert type="error" showIcon message="通知读取失败" description={failure} /> : null}
          <Card
            variant="borderless"
            title={<Space><BellOutlined /><span>Inbox</span><Typography.Text type="secondary">{items.length} 条 / {unreadCount} 未读</Typography.Text></Space>}
            extra={<Segmented value={filter} onChange={(value) => setFilter(value as 'unread' | 'all')} options={[{ label: '未读', value: 'unread' }, { label: '全部', value: 'all' }]} />}
          >
            <Table<NotificationInboxItem>
              rowKey="inboxItemId"
              loading={loading}
              columns={columns}
              dataSource={visibleItems}
              pagination={{ pageSize: 20, hideOnSinglePage: true }}
              scroll={{ x: 1100 }}
              locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={filter === 'unread' ? '当前没有未读通知' : '当前没有通知'} /> }}
              onRow={(item) => ({ onClick: () => setSelected(item), style: { cursor: 'pointer' } })}
            />
          </Card>
        </Space>
      </PageScaffold>
      <Drawer
        title={selected?.subject ?? '通知详情'}
        width={560}
        open={Boolean(selected)}
        onClose={() => setSelected(null)}
        extra={selected?.status === 'UNREAD' ? <Button type="primary" icon={<CheckOutlined />} loading={markingId === selected.inboxItemId} onClick={() => void markRead(selected)}>标记已读</Button> : null}
      >
        {selected ? (
          <Space direction="vertical" size={16} style={{ width: '100%' }}>
            <Typography.Paragraph>{selected.body}</Typography.Paragraph>
            <Descriptions bordered column={1} size="small">
              <Descriptions.Item label="状态">{selected.status}</Descriptions.Item>
              <Descriptions.Item label="级别"><Tag color={severityColor[selected.severity]}>{selected.severity}</Tag></Descriptions.Item>
              <Descriptions.Item label="事件">{selected.sourceAction}</Descriptions.Item>
              <Descriptions.Item label="时间">{new Date(selected.createdAt).toLocaleString()}</Descriptions.Item>
              <Descriptions.Item label="站点">{selectedSite?.displayName ?? selected.siteId}</Descriptions.Item>
              <Descriptions.Item label="Alarm"><Typography.Text copyable>{selected.alarmId}</Typography.Text></Descriptions.Item>
              <Descriptions.Item label="Incident"><Typography.Text copyable>{selected.incidentCorrelationId}</Typography.Text></Descriptions.Item>
            </Descriptions>
            {selectedSite ? <Button href={siteRoute(selectedSite, 'alarms')}>进入该站点报警</Button> : null}
          </Space>
        ) : null}
      </Drawer>
    </section>
  );
}
