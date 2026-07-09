import { useState } from 'react';
import { Card, Tabs, Tag, Button, Segmented, Typography, Space, Popconfirm, Empty, Alert } from 'antd';
import { ExperimentOutlined } from '@ant-design/icons';
import { useOps } from '@/store/ops';
import { useUi } from '@/store/ui';
import { type Suggestion, type SuggestionStatus } from '@/mock/data';

const STATUS_META: Record<SuggestionStatus, { color: string; label: string }> = {
  draft: { color: 'default', label: '草稿' },
  pending: { color: 'gold', label: '待审批' },
  approved: { color: 'green', label: '已批准' },
  dispatched: { color: 'blue', label: '已下发' },
  rejected: { color: 'red', label: '已驳回' },
};

type Metric = 'kwh' | 'cny' | 'co2';

function SuggestionCard({ s, review }: { s: Suggestion; review?: boolean }) {
  const [metric, setMetric] = useState<Metric>('cny');
  const demoMode = useUi((st) => st.demoMode);
  const { submitApproval, approve, reject, dispatch, simulateDispatch } = useOps();
  const meta = STATUS_META[s.status];
  const val = metric === 'kwh' ? s.saving.kwh : metric === 'cny' ? s.saving.cny : s.saving.co2;
  const unit = metric === 'kwh' ? 'kWh' : metric === 'cny' ? '¥' : 'kgCO₂';
  const prefix = metric === 'cny' ? '¥' : '';
  const down = s.saving.kwh >= 0;

  const actions = review ? (
    s.status === 'pending' ? (
      <Space>
        <Button type="primary" size="small" onClick={() => approve(s.id)}>批准</Button>
        <Button danger size="small" onClick={() => reject(s.id)}>驳回</Button>
      </Space>
    ) : s.status === 'approved' ? (
      demoMode ? (
        <Button size="small" onClick={() => simulateDispatch(s.id)}>模拟下发</Button>
      ) : (
        <Popconfirm title="确认下发设备？此操作不可逆" onConfirm={() => dispatch(s.id)}>
          <Button size="small">下发</Button>
        </Popconfirm>
      )
    ) : null
  ) : (
    s.status === 'draft' ? (
      <Button type="primary" size="small" onClick={() => submitApproval(s.id)}>提交审批</Button>
    ) : s.status === 'pending' ? (
      <Tag color="gold">待审批</Tag>
    ) : s.status === 'approved' ? (
      demoMode ? (
        <Button size="small" onClick={() => simulateDispatch(s.id)}>一键模拟下发</Button>
      ) : (
        <Popconfirm title="确认下发设备？此操作不可逆" onConfirm={() => dispatch(s.id)}>
          <Button size="small">二次确认下发</Button>
        </Popconfirm>
      )
    ) : s.status === 'dispatched' ? (
      <Tag color="blue">已下发</Tag>
    ) : (
      <Tag color="red">已驳回</Tag>
    )
  );

  return (
    <Card variant="borderless" size="small" styles={{ body: { padding: 16 } }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start' }}>
        <div>
          <Typography.Text strong>{s.title}</Typography.Text>
          <div style={{ fontSize: 12, opacity: 0.65, marginTop: 2 }}>{s.device} · {s.createdAt}</div>
        </div>
        <Tag color={meta.color}>{meta.label}</Tag>
      </div>

      <div style={{ margin: '10px 0', fontSize: 13 }}>
        <span style={{ opacity: 0.7 }}>{s.diff.param}：</span>
        <span style={{ textDecoration: 'line-through', opacity: 0.5 }}>{s.diff.current}{s.diff.unit}</span>
        {' → '}
        <span style={{ color: '#0FB5AE', fontWeight: 700 }}>{s.diff.proposed}{s.diff.unit}</span>
        <span style={{ color: down ? '#16A34A' : '#DC2626', marginLeft: 8, fontSize: 12 }}>
          {down ? '↓ 预计节能' : '↑ 升耗'}
        </span>
      </div>

      <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
        <Segmented<Metric>
          size="small"
          value={metric}
          onChange={setMetric}
          options={[{ label: 'kWh', value: 'kwh' }, { label: '¥', value: 'cny' }, { label: 'kgCO₂', value: 'co2' }]}
        />
        <Typography.Text strong style={{ fontSize: 18 }}>
          {prefix}{val} <span style={{ fontSize: 12, fontWeight: 400, opacity: 0.6 }}>{unit}</span>
        </Typography.Text>
        <Typography.Text type="secondary" style={{ fontSize: 12 }}>· 节能 {s.saving.kwh} kWh</Typography.Text>
        <Typography.Text type="secondary" style={{ fontSize: 12, marginLeft: 'auto' }}>
          置信度 {Math.round(s.confidence * 100)}%
        </Typography.Text>
      </div>

      <div style={{ marginTop: 12, display: 'flex', justifyContent: 'flex-end' }}>{actions}</div>
    </Card>
  );
}

export default function Optimize() {
  const suggestions = useOps((st) => st.suggestions);
  const [tab, setTab] = useState('all');

  const byType = (t: string) =>
    t === 'all' ? suggestions : suggestions.filter((s) => s.type === t);
  const reviewList = suggestions.filter((s) => s.status === 'pending' || s.status === 'approved');

  const items = [
    { key: 'setpoint', label: '设定值优化', children: <List cards={byType('setpoint')} /> },
    { key: 'schedule', label: '运行日程优化', children: <List cards={byType('schedule')} /> },
    { key: 'all', label: '全部', children: <List cards={byType('all')} /> },
    { key: 'review', label: `待审 (${reviewList.length})`, children: <List cards={reviewList} review /> },
  ];

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
      <Typography.Title level={4} style={{ margin: 0 }}>节能优化建议</Typography.Title>
      <Alert
        type="info"
        showIcon
        icon={<ExperimentOutlined />}
        message="人在回路：建议只可「提交审批」，绝不直接下发设备；审批在「待审」收件箱完成，下发需二次确认。"
        style={{ fontSize: 13 }}
      />
      <Tabs activeKey={tab} onChange={setTab} items={items} />
    </div>
  );
}

function List({ cards, review }: { cards: Suggestion[]; review?: boolean }) {
  if (!cards.length) return <Empty description="暂无建议" style={{ padding: 40 }} />;
  return (
    <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(360px, 1fr))', gap: 16 }}>
      {cards.map((s) => <SuggestionCard key={s.id} s={s} review={review} />)}
    </div>
  );
}
