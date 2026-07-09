import { Card, Typography, Table } from 'antd';
import { useLatest, MOCK_DEVICES } from '@/api';

// 设定值 vs 实际：供水/回水温度走实时层（useLatest），其余设定类指标暂用配置基线。
export default function SetpointVsActual() {
  const { data: latest } = useLatest(MOCK_DEVICES[0], ['supplyTemp', 'returnTemp']);
  const supply = latest?.supplyTemp?.value ?? 7.0;
  const rtn = latest?.returnTemp?.value ?? 12.0;

  const data = [
    { label: '冷冻水供水温度', set: 7, actual: supply, unit: '℃' },
    { label: '冷冻水回水温度', set: 12, actual: rtn, unit: '℃' },
    { label: '冷却水供水温度', set: 32, actual: 33.1, unit: '℃' },
    { label: '末端供水压力', set: 0.45, actual: 0.43, unit: 'MPa' },
  ];
  type Row = (typeof data)[number];

  const columns = [
    { title: '参数', dataIndex: 'label', key: 'label' },
    { title: '设定值', dataIndex: 'set', key: 'set', render: (v: number, r: Row) => `${v}${r.unit}` },
    {
      title: '实际值', dataIndex: 'actual', key: 'actual',
      render: (v: number, r: Row) => {
        const dev = Math.abs(v - r.set);
        const off = dev > (r.unit === '℃' ? 1 : 0.05);
        return <span style={{ color: off ? '#DC2626' : '#16A34A' }}>{v}{r.unit}</span>;
      },
    },
    {
      title: '偏差', key: 'delta',
      render: (_: unknown, r: Row) => {
        const d = (r.actual - r.set).toFixed(1);
        return <span style={{ color: Number(d) === 0 ? '#16A34A' : '#DC2626' }}>{d}{r.unit}</span>;
      },
    },
  ];
  return (
    <Card variant="borderless" title={<Typography.Text strong>设定值 vs 实际</Typography.Text>}
      styles={{ body: { paddingTop: 4 } }}>
      <Table rowKey="label" size="small" pagination={false} columns={columns}
        dataSource={data} bordered={false} />
    </Card>
  );
}
