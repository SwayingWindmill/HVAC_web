import { useState } from 'react';
import { Card, Tag, Typography, Button, Space, Segmented, Empty, List as AntList } from 'antd';
import { useOps, fddList } from '@/store/ops';
import { useNavigate } from 'react-router-dom';
import { SEVERITY_TONE, SEVERITY_LABEL, type Severity } from '@/theme/tokens';

const FILTERS: { label: string; value: Severity | 'all' }[] = [
  { label: '全部', value: 'all' },
  { label: '紧急', value: 'critical' },
  { label: '重要', value: 'major' },
  { label: '次要', value: 'minor' },
  { label: '提示', value: 'info' },
];

export default function Fdd() {
  const navigate = useNavigate();
  const { generateWorkOrder, generatedFddIds } = useOps();
  const [sev, setSev] = useState<Severity | 'all'>('all');
  const list = sev === 'all' ? fddList : fddList.filter((f) => f.severity === sev);

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
        <Typography.Title level={4} style={{ margin: 0 }}>故障检测与诊断 (FDD)</Typography.Title>
        <Segmented<Severity | 'all'> value={sev} onChange={setSev} options={FILTERS} />
      </div>
      <Typography.Paragraph type="secondary" style={{ fontSize: 13, margin: 0 }}>
        FDD = 发现 + 诊断，不自闭环；确认后「生成工单」流转到 /alarms 由人跟进处置。
      </Typography.Paragraph>

      {!list.length ? (
        <Empty description="该级别暂无故障" style={{ padding: 40 }} />
      ) : (
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(380px, 1fr))', gap: 16 }}>
          {list.map((f) => {
            const done = generatedFddIds.includes(f.id);
            return (
              <Card key={f.id} variant="borderless" size="small" styles={{ body: { padding: 16 } }}>
                <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
                  <Space>
                    <Tag color={SEVERITY_TONE[f.severity]}>{SEVERITY_LABEL[f.severity]}</Tag>
                    <Typography.Text strong>{f.device}</Typography.Text>
                  </Space>
                  <Typography.Text type="secondary" style={{ fontSize: 12 }}>{f.ts}</Typography.Text>
                </div>

                <div style={{ marginTop: 10 }}>
                  <Typography.Text type="secondary" style={{ fontSize: 12 }}>故障现象</Typography.Text>
                  <div style={{ fontSize: 13 }}>{f.phenomenon}</div>
                </div>

                <div style={{ marginTop: 8, padding: 8, borderRadius: 8, background: 'rgba(220,38,38,0.06)' }}>
                  <Typography.Text type="secondary" style={{ fontSize: 12 }}>根因假设</Typography.Text>
                  <div style={{ fontSize: 13 }}>{f.rootCause}</div>
                </div>

                <div style={{ marginTop: 8 }}>
                  <Typography.Text type="secondary" style={{ fontSize: 12 }}>佐证指标</Typography.Text>
                  <AntList size="small" dataSource={f.evidence}
                    renderItem={(e) => (
                      <AntList.Item style={{ padding: '2px 0' }}>
                        <span style={{ fontSize: 12, opacity: 0.7 }}>{e.name}</span>
                        <span style={{ fontSize: 12, fontWeight: 600 }}>{e.value}</span>
                      </AntList.Item>
                    )}
                  />
                </div>

                <div style={{ marginTop: 8 }}>
                  <Typography.Text type="secondary" style={{ fontSize: 12 }}>推荐处置</Typography.Text>
                  <div style={{ fontSize: 13 }}>{f.recommended}</div>
                </div>

                <div style={{ marginTop: 12, display: 'flex', justifyContent: 'flex-end' }}>
                  {done ? (
                    <Tag color="green">已生成工单</Tag>
                  ) : (
                    <Button type="primary" size="small" onClick={() => { generateWorkOrder(f); navigate('/alarms'); }}>
                      生成工单 ›
                    </Button>
                  )}
                </div>
              </Card>
            );
          })}
        </div>
      )}
    </div>
  );
}
