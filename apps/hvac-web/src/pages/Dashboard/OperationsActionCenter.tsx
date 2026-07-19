import { Card, Typography } from 'antd';
import { RightOutlined } from '@ant-design/icons';
import { useNavigate } from 'react-router-dom';
import { fddList, useOps } from '@/store/ops';
import { TICKET_STATUS_META, isWorkOrderActive, isWorkOrderSlaRisk } from '@/domain/opsMeta';

type ActionTone = 'critical' | 'major' | 'warning' | 'active';

type ActionItem = {
  key: string;
  title: string;
  meta: string;
  sideValue: string;
  sideLabel: string;
  indicatorTone: ActionTone;
  statusTone: ActionTone;
  urgent: boolean;
  path: '/fdd' | '/alarms';
};

function deadlineText(dueAt?: string) {
  if (!dueAt) return '';
  return dueAt.replace(/^今天\s*/, '');
}

export default function OperationsActionCenter() {
  const navigate = useNavigate();
  const workOrders = useOps((state) => state.workOrders);
  const activeOrders = workOrders.filter(isWorkOrderActive);
  const highRisk = fddList.filter((item) => item.severity === 'critical' || item.severity === 'major');
  const slaRisk = activeOrders.filter(isWorkOrderSlaRisk).length;

  const diagnosisItems: ActionItem[] = highRisk.map((diagnosis) => {
    const linked = activeOrders.find((order) => order.device === diagnosis.device || order.title.includes(diagnosis.device));
    const linkedHasSlaRisk = linked ? isWorkOrderSlaRisk(linked) : false;
    const deadline = deadlineText(linked?.dueAt);

    return {
      key: diagnosis.id,
      title: diagnosis.phenomenon,
      meta: `${diagnosis.device} · ${diagnosis.ts}`,
      sideValue: linked?.id ?? '未派工',
      sideLabel: linked
        ? `${TICKET_STATUS_META[linked.status].label}${deadline ? ` · 截止 ${deadline}` : ''}`
        : '立即创建工单',
      indicatorTone: diagnosis.severity === 'critical' ? 'critical' : 'major',
      statusTone: linked ? (linkedHasSlaRisk ? 'warning' : 'active') : 'critical',
      urgent: !linked && diagnosis.severity === 'critical',
      path: '/fdd',
    };
  });

  const linkedOrderIds = new Set(diagnosisItems.map((item) => item.sideValue).filter((value) => value.startsWith('WO-')));
  const orderItems: ActionItem[] = activeOrders
    .filter((order) => !linkedOrderIds.has(order.id))
    .map((order) => {
      const deadline = deadlineText(order.dueAt);
      return {
        key: order.id,
        title: order.title,
        meta: `${order.device} · ${order.createdAt}`,
        sideValue: order.id,
        sideLabel: `${TICKET_STATUS_META[order.status].label}${deadline ? ` · 截止 ${deadline}` : ''}`,
        indicatorTone: order.severity === 'critical' ? 'critical' : order.severity === 'major' ? 'major' : 'warning',
        statusTone: isWorkOrderSlaRisk(order) ? 'warning' : 'active',
        urgent: false,
        path: '/alarms',
      };
    });
  const items = [...diagnosisItems, ...orderItems].slice(0, 3);

  return (
    <Card
      variant="borderless"
      className="dashboard-section-card dashboard-list-card dashboard-action-card"
      title={<Typography.Text strong>异常与工单闭环</Typography.Text>}
      extra={<Typography.Link onClick={() => navigate('/alarms')}>进入工作台 <RightOutlined /></Typography.Link>}
    >
      <div className="dashboard-list-summary dashboard-list-summary-three">
        <div><span>待确认异常</span><strong>{highRisk.length}</strong></div>
        <div><span>处理中工单</span><strong>{activeOrders.length}</strong></div>
        <div><span>SLA 风险</span><strong className={slaRisk ? 'is-warning' : ''}>{slaRisk}</strong></div>
      </div>

      <div className="dashboard-list-rows">
        {items.length ? items.map((item) => (
          <button
            type="button"
            className={`dashboard-data-row dashboard-event-row${item.urgent ? ' is-urgent' : ''}`}
            key={item.key}
            onClick={() => navigate(item.path)}
          >
            <span className={`dashboard-row-indicator is-${item.indicatorTone}`} />
            <span className="dashboard-row-content">
              <span className="dashboard-row-title">{item.title}</span>
              <span className="dashboard-row-meta">{item.meta}</span>
            </span>
            <span className="dashboard-row-side dashboard-event-side">
              <strong className={`dashboard-side-value is-${item.statusTone}`}>{item.sideValue}</strong>
              <span className={`dashboard-side-label is-${item.statusTone}`}>{item.sideLabel}</span>
            </span>
          </button>
        )) : (
          <div className="dashboard-list-empty">暂无高风险异常</div>
        )}
      </div>
    </Card>
  );
}
