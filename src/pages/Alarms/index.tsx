import { useState } from 'react';
import { Table, Tag, Button, Segmented, Typography } from 'antd';
import { useOps } from '@/store/ops';
import { SEVERITY_TONE, SEVERITY_LABEL, type Severity } from '@/theme/tokens';
import type { TicketStatus, WorkOrder } from '@/mock/data';

const STATUS_META: Record<TicketStatus, { color: string; label: string; next?: TicketStatus; nextLabel?: string }> = {
  open: { color: 'red', label: '待接手', next: 'assigned', nextLabel: '接手' },
  assigned: { color: 'gold', label: '已派工', next: 'doing', nextLabel: '开始' },
  doing: { color: 'blue', label: '处理中', next: 'done', nextLabel: '完成' },
  done: { color: 'green', label: '已完成' },
};

const FILTERS: { label: string; value: 'all' | 'active' | 'done' }[] = [
  { label: '全部', value: 'all' },
  { label: '待处理', value: 'active' },
  { label: '已完成', value: 'done' },
];

export default function Alarms() {
  const workOrders = useOps((st) => st.workOrders);
  const setTicketStatus = useOps((st) => st.setTicketStatus);
  const [filter, setFilter] = useState<'all' | 'active' | 'done'>('all');

  const rows = workOrders.filter((w) =>
    filter === 'all' ? true : filter === 'done' ? w.status === 'done' : w.status !== 'done',
  );

  const columns = [
    { title: '工单', dataIndex: 'id', key: 'id', width: 110,
      render: (id: string) => <Typography.Text copyable={{ text: id }}>{id}</Typography.Text> },
    { title: '来源', dataIndex: 'source', key: 'source', width: 80,
      render: (s: WorkOrder['source']) => <Tag color={s === 'fdd' ? 'geekblue' : 'default'}>{s === 'fdd' ? 'FDD' : '告警'}</Tag> },
    { title: '设备', dataIndex: 'device', key: 'device', width: 140 },
    { title: '严重级', dataIndex: 'severity', key: 'severity', width: 90,
      render: (sv: Severity) => <Tag color={SEVERITY_TONE[sv]}>{SEVERITY_LABEL[sv]}</Tag> },
    { title: '标题 / 描述', key: 'title',
      render: (_: unknown, r: WorkOrder) => (
        <div>
          <div style={{ fontSize: 13 }}>{r.title}</div>
          <div style={{ fontSize: 12, opacity: 0.6 }}>{r.description}</div>
        </div>
      ) },
    { title: '状态', dataIndex: 'status', key: 'status', width: 100,
      render: (st: TicketStatus) => <Tag color={STATUS_META[st].color}>{STATUS_META[st].label}</Tag> },
    { title: '操作', key: 'action', width: 100,
      render: (_: unknown, r: WorkOrder) => {
        const meta = STATUS_META[r.status];
        return meta.next ? (
          <Button size="small" type="primary" onClick={() => setTicketStatus(r.id, meta.next!)}>
            {meta.nextLabel}
          </Button>
        ) : (
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>已闭环</Typography.Text>
        );
      } },
  ];

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
        <Typography.Title level={4} style={{ margin: 0 }}>报警工单</Typography.Title>
        <Segmented value={filter} onChange={setFilter} options={FILTERS} />
      </div>
      <Typography.Paragraph type="secondary" style={{ fontSize: 13, margin: 0 }}>
        工单处置闭环：FDD 或告警在此接手、派工、处理、完成。FDD 上游诊断产出，本页下游人跟进。
      </Typography.Paragraph>
      <Table rowKey="id" size="small" columns={columns} dataSource={rows}
        pagination={false} bordered={false} />
    </div>
  );
}
