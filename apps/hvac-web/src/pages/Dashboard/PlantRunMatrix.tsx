import { Card, Typography } from 'antd';
import { RightOutlined } from '@ant-design/icons';

type PlantTone = 'success' | 'warning' | 'neutral';

type PlantGroup = {
  label: string;
  summary: string;
  primary: string;
  judgement: string;
  tone: PlantTone;
};

const GROUPS: PlantGroup[] = [
  {
    label: '冷水机组',
    summary: '2 / 3 运行',
    primary: '1# 88% · 2# 84%',
    judgement: '负载均衡',
    tone: 'success',
  },
  {
    label: '冷冻水泵',
    summary: '3 / 6 运行',
    primary: '5# 42Hz',
    judgement: '高频运行',
    tone: 'warning',
  },
  {
    label: '冷却水泵',
    summary: '2 / 3 运行',
    primary: '1# 45Hz',
    judgement: '接近高频',
    tone: 'warning',
  },
  {
    label: '冷却塔',
    summary: '6 / 8 运行',
    primary: '7#、8# 停止',
    judgement: '组合正常',
    tone: 'neutral',
  },
];

export default function PlantRunMatrix({ onViewAll }: { onViewAll?: () => void }) {
  const warningCount = GROUPS.filter((group) => group.tone === 'warning').length;

  return (
    <Card
      variant="borderless"
      className="dashboard-section-card dashboard-plant-card dashboard-plant-overview"
      title={<Typography.Text strong>冷源设备运行矩阵</Typography.Text>}
      extra={<span className="dashboard-card-state is-warning">{warningCount} 组运行异常</span>}
    >
      <div className="dashboard-plant-overview-rows">
        {GROUPS.map((group) => (
          <div className={`dashboard-plant-overview-row is-${group.tone}`} key={group.label}>
            <div className="dashboard-plant-overview-row-head">
              <span className="dashboard-plant-overview-system">
                <strong>{group.label}</strong>
                <span>{group.summary}</span>
              </span>
              <span className="dashboard-plant-overview-judgement">{group.judgement}</span>
            </div>
            <span className="dashboard-plant-overview-primary">{group.primary}</span>
          </div>
        ))}
      </div>
      {onViewAll && (
        <button type="button" className="dashboard-plant-link" onClick={onViewAll}>
          查看完整设备状态 <RightOutlined />
        </button>
      )}
    </Card>
  );
}
