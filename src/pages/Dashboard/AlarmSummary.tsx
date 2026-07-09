import { Card, List, Tag, Typography } from 'antd';
import { useNavigate } from 'react-router-dom';
import { mockAlarms } from '@/mock/data';
import { SEVERITY_COLOR, SEVERITY_LABEL, SEVERITY_TONE } from '@/theme/tokens';

// 告警摘要：常驻可见、标「· 优先处理」(B 变体的告警优先思路)，点全部进 /alarms。
export default function AlarmSummary() {
  const navigate = useNavigate();
  return (
    <Card
      variant="borderless"
      title={
        <span>
          <Typography.Text strong>告警摘要</Typography.Text>
          <Tag color="error" style={{ marginLeft: 8 }}>· 优先处理</Tag>
        </span>
      }
      extra={<Typography.Link onClick={() => navigate('/alarms')}>全部 ›</Typography.Link>}
      styles={{ body: { paddingTop: 4, maxHeight: 300, overflow: 'auto' } }}
    >
      <List
        size="small"
        dataSource={mockAlarms}
        renderItem={(a) => (
          <List.Item style={{ cursor: 'pointer', paddingInline: 4 }} onClick={() => navigate('/alarms')}>
            <List.Item.Meta
              avatar={<span style={{ display: 'inline-block', width: 8, height: 8, borderRadius: 8, background: SEVERITY_COLOR[a.severity] }} />}
              title={<span style={{ fontSize: 13 }}>{a.text}</span>}
              description={
                <span style={{ fontSize: 12, opacity: 0.7 }}>
                  {a.device} · {a.ts}
                </span>
              }
            />
            <Tag color={SEVERITY_TONE[a.severity]} style={{ marginInlineEnd: 0 }}>
              {SEVERITY_LABEL[a.severity]}
            </Tag>
          </List.Item>
        )}
      />
    </Card>
  );
}
