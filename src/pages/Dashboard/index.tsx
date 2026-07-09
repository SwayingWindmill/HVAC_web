import { Row, Col, Card, Typography, Button } from 'antd';
import { ApartmentOutlined } from '@ant-design/icons';
import { useNavigate } from 'react-router-dom';
import KpiCards from './KpiCards';
import EnergyTrend from './EnergyTrend';
import AlarmSummary from './AlarmSummary';
import SetpointVsActual from './SetpointVsActual';
import LoadGauge from './LoadGauge';

// Variant D (settled in #6): A-grid skeleton + B alarm-priority + C COP hero focus.
export default function Dashboard() {
  const navigate = useNavigate();
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      <Typography.Title level={4} style={{ margin: 0 }}>总览驾驶舱</Typography.Title>

      <KpiCards />

      <Row gutter={[16, 16]}>
        <Col xs={24} lg={16}><EnergyTrend /></Col>
        <Col xs={24} lg={8}><AlarmSummary /></Col>
      </Row>

      <Row gutter={[16, 16]}>
        <Col xs={24} md={8}>
          <Card variant="borderless" styles={{ body: { padding: 16 } }}>
            <Typography.Text type="secondary" style={{ fontSize: 13 }}>设备与建筑</Typography.Text>
            <div style={{ marginTop: 8, fontSize: 13 }}>
              当前全局上下文：建筑 → 分区 → 机组，左侧树点选即切换全站数据。
            </div>
            <Button type="primary" ghost icon={<ApartmentOutlined />} style={{ marginTop: 12 }}
              onClick={() => navigate('/assets')}>打开设备树 ›</Button>
          </Card>
        </Col>
        <Col xs={24} md={8}><SetpointVsActual /></Col>
        <Col xs={24} md={8}><LoadGauge /></Col>
      </Row>
    </div>
  );
}
