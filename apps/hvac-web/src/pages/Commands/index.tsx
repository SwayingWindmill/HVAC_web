import { useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  Alert,
  Button,
  Card,
  Col,
  Descriptions,
  Form,
  Input,
  InputNumber,
  Result,
  Row,
  Space,
  Tag,
  Timeline,
  Typography,
} from 'antd';
import { CheckCircleOutlined, ControlOutlined, SearchOutlined } from '@ant-design/icons';
import { useNavigate, useParams } from 'react-router-dom';
import PageScaffold from '@/components/PageScaffold';
import { can } from '@/auth/permissions';
import { useUi } from '@/store/ui';
import { API_MODE } from '@/api/config';
import {
  approveCommand,
  COMMAND_PUBLIC_ROUTES_ENABLED,
  commandErrorMessage,
  createCommand,
  getCommand,
  MOCK_COMMAND_DEVICE_ID,
  MOCK_PENDING_COMMAND_ID,
  type Command,
  type CommandRisk,
  type CommandStatus,
} from '@/api/commands';
import './Commands.css';

const STATUS_LABEL: Record<CommandStatus, string> = {
  SUBMITTED: '已提交',
  VALIDATING: '校验中',
  AWAITING_APPROVAL: '等待审批',
  APPROVED: '已审批',
  QUEUED: '等待下发',
  DISPATCHING: '下发中',
  SUCCEEDED: '已验证成功',
  FAILED: '失败',
  REJECTED: '已拒绝',
  CANCELLED: '已取消',
  EXPIRED: '已过期',
  OUTCOME_UNKNOWN: '设备结果待确认',
};

const STATUS_COLOR: Partial<Record<CommandStatus, string>> = {
  AWAITING_APPROVAL: 'gold',
  APPROVED: 'cyan',
  QUEUED: 'blue',
  DISPATCHING: 'processing',
  SUCCEEDED: 'success',
  FAILED: 'error',
  REJECTED: 'error',
  CANCELLED: 'default',
  EXPIRED: 'default',
  OUTCOME_UNKNOWN: 'warning',
};

const RISK_LABEL: Record<CommandRisk, string> = {
  LOW: '低风险',
  MEDIUM: '中风险',
  HIGH: '高风险',
};

const RISK_COLOR: Record<CommandRisk, string> = {
  LOW: 'green',
  MEDIUM: 'gold',
  HIGH: 'red',
};

const REASON_LABEL: Record<string, string> = {
  COMMAND_SUBMITTED: '用户提交 Canonical Command Intent',
  COMMAND_VALIDATING: 'Command Service 校验作用域与能力',
  COMMAND_GOVERNANCE_EVALUATED: '风险与审批策略已计算',
  APPROVAL_REQUIRED: '需要独立审批人确认',
  APPROVAL_THRESHOLD_MET: '审批阈值已满足',
  COMMAND_QUEUED: '已进入受 Fence 保护的设备队列',
  PROVIDER_ACKNOWLEDGED_AWAITING_REPORTED_STATE: 'Provider 已确认，等待 S2 reported-state',
  ACKNOWLEDGED_AND_REPORTED_STATE_VERIFIED: 'S2 reported-state 已验证目标状态',
  REPORTED_STATE_VERIFICATION_NOT_PROVEN: 'reported-state 未能证明设备结果',
};

interface CreateFormValues {
  deviceId: string;
  setpointC: number;
}

interface LookupFormValues {
  commandId: string;
}

function CommandDetail({ command, canApprove, onApprove, approving }: {
  command: Command;
  canApprove: boolean;
  onApprove: () => void;
  approving: boolean;
}) {
  const approvalPending = command.status === 'AWAITING_APPROVAL'
    && command.approvalCount < command.requiredApprovalCount;

  return (
    <Space direction="vertical" size={16} className="command-detail-stack">
      {command.status === 'OUTCOME_UNKNOWN' ? (
        <Alert
          type="warning"
          showIcon
          message="设备结果待确认"
          description="系统无法证明请求未执行，也无法证明 reported-state 已达到目标。SETPOINT 控制组保持冻结，不会自动重发。"
        />
      ) : null}

      <Card
        title="Command 权威状态"
        extra={approvalPending ? (
          <Button
            type="primary"
            icon={<CheckCircleOutlined />}
            disabled={!canApprove}
            loading={approving}
            onClick={onApprove}
          >
            审批 Command
          </Button>
        ) : null}
      >
        <Descriptions column={{ xs: 1, sm: 2, xl: 3 }} bordered size="small">
          <Descriptions.Item label="Command ID" span={2}>
            <Typography.Text copyable className="command-monospace">{command.commandId}</Typography.Text>
          </Descriptions.Item>
          <Descriptions.Item label="状态">
            <Tag color={STATUS_COLOR[command.status]}>{STATUS_LABEL[command.status]}</Tag>
          </Descriptions.Item>
          <Descriptions.Item label="Device ID" span={2}>
            <Typography.Text copyable className="command-monospace">{command.deviceId}</Typography.Text>
          </Descriptions.Item>
          <Descriptions.Item label="目标设定值">{command.setpointC.toFixed(1)} °C</Descriptions.Item>
          <Descriptions.Item label="Canonical Capability">SET_TEMPERATURE_SETPOINT</Descriptions.Item>
          <Descriptions.Item label="风险"><Tag color={RISK_COLOR[command.risk]}>{RISK_LABEL[command.risk]}</Tag></Descriptions.Item>
          <Descriptions.Item label="审批进度">{command.approvalCount} / {command.requiredApprovalCount}</Descriptions.Item>
          <Descriptions.Item label="设备命令序号">{command.deviceCommandSequence}</Descriptions.Item>
          <Descriptions.Item label="S2 Snapshot Revision">{command.snapshotRevision}</Descriptions.Item>
          <Descriptions.Item label="Command Version">{command.version}</Descriptions.Item>
          <Descriptions.Item label="更新时间">{new Date(command.updatedAt).toLocaleString('zh-CN')}</Descriptions.Item>
        </Descriptions>
        {approvalPending && !canApprove ? (
          <Alert
            className="command-inline-alert"
            type="info"
            showIcon
            message="当前角色不可审批"
            description="审批身份与角色必须来自认证 Session；浏览器不能提交 Principal、Approver Role、Risk 或 Payload Hash。"
          />
        ) : null}
      </Card>

      <Card title="状态时间线" className="command-timeline-card">
        <Timeline
          items={command.transitions.map((transition) => ({
            color: transition.toStatus === 'OUTCOME_UNKNOWN' ? 'orange' : transition.toStatus === 'SUCCEEDED' ? 'green' : 'blue',
            children: (
              <div className="command-timeline-item">
                <Space wrap>
                  <Tag color={STATUS_COLOR[transition.toStatus]}>{STATUS_LABEL[transition.toStatus]}</Tag>
                  <Typography.Text strong>{REASON_LABEL[transition.reason] ?? transition.reason}</Typography.Text>
                  <Tag>{transition.actorType === 'PRINCIPAL' ? '人员操作' : '平台工作负载'}</Tag>
                </Space>
                <Typography.Text type="secondary">
                  Version {transition.version} · {new Date(transition.occurredAt).toLocaleString('zh-CN')}
                </Typography.Text>
              </div>
            ),
          }))}
        />
      </Card>
    </Space>
  );
}

export default function Commands() {
  const { commandId } = useParams<{ commandId?: string }>();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const role = useUi((state) => state.role);
  const [createForm] = Form.useForm<CreateFormValues>();
  const [lookupForm] = Form.useForm<LookupFormValues>();
  const [actionMessage, setActionMessage] = useState<string | null>(null);
  const routesAvailable = API_MODE === 'mock' || COMMAND_PUBLIC_ROUTES_ENABLED;
  const selectedCommandId = commandId ?? (API_MODE === 'mock' ? MOCK_PENDING_COMMAND_ID : '');
  const mayCreate = can(role, 'create', 'command') && routesAvailable;
  const mayApprove = can(role, 'approve', 'command') && routesAvailable;

  const commandQuery = useQuery({
    queryKey: ['command', selectedCommandId],
    queryFn: ({ signal }) => getCommand(selectedCommandId, signal),
    enabled: Boolean(selectedCommandId) && routesAvailable,
    retry: false,
  });

  const createMutation = useMutation({
    mutationFn: createCommand,
    onSuccess: (command) => {
      queryClient.setQueryData(['command', command.commandId], command);
      setActionMessage('Command Intent 已提交。Provider ACK 或请求受理均不代表设备状态已经成功。');
      navigate(`/commands/${command.commandId}`);
    },
    onError: (error) => setActionMessage(commandErrorMessage(error)),
  });

  const approveMutation = useMutation({
    mutationFn: approveCommand,
    onSuccess: (command) => {
      queryClient.setQueryData(['command', command.commandId], command);
      setActionMessage('审批证据已记录。Command 进入队列后仍需独立的 Provider ACK 与 S2 reported-state 验证。');
    },
    onError: (error) => setActionMessage(commandErrorMessage(error)),
  });

  const environmentAlert = useMemo(() => {
    if (API_MODE === 'mock') {
      return (
        <Alert
          type="info"
          showIcon
          message="S3-08 Mock UX 审计模式"
          description="当前页面只模拟 Command Intent、审批和 Timeline，不连接 ThingsBoard，也不会产生真实设备副作用。26°C 及以上的演示提交会进入中风险审批。"
        />
      );
    }
    return (
      <Alert
        type="warning"
        showIcon
        message="Command 控制路由尚未启用"
        description="Command API 已登记并完成安全基线，但 Route Ownership Registry 仍为 disabled，生产流量为 0%。真实提交、查询和审批均保持阻断。"
      />
    );
  }, []);

  return (
    <PageScaffold
      title={<Space><ControlOutlined />设备控制</Space>}
      extra={<Tag color={routesAvailable ? 'blue' : 'default'}>{API_MODE === 'mock' ? 'MOCK / 无设备副作用' : 'PRODUCTION DISABLED'}</Tag>}
      className="commands-page"
    >
      <Space direction="vertical" size={16} className="commands-page-stack">
        {environmentAlert}
        {actionMessage ? (
          <Alert
            closable
            showIcon
            type={createMutation.isError || approveMutation.isError ? 'error' : 'success'}
            message={actionMessage}
            onClose={() => setActionMessage(null)}
          />
        ) : null}

        <Row gutter={[16, 16]}>
          <Col xs={24} xl={10}>
            <Card title="提交温度设定 Command" className="command-workbench-card">
              <Form<CreateFormValues>
                form={createForm}
                layout="vertical"
                initialValues={{ deviceId: API_MODE === 'mock' ? MOCK_COMMAND_DEVICE_ID : '', setpointC: 24 }}
                onFinish={(values) => createMutation.mutate(values)}
              >
                <Form.Item
                  name="deviceId"
                  label="Canonical Device ID"
                  rules={[{ required: true, message: '请输入 Device UUIDv7' }]}
                >
                  <Input placeholder="Device UUIDv7" autoComplete="off" />
                </Form.Item>
                <Form.Item
                  name="setpointC"
                  label="目标温度"
                  extra="允许范围 16–30°C；浏览器不能选择 Provider Method 或直接构造 RPC 参数。"
                  rules={[{ required: true, message: '请输入目标温度' }]}
                >
                  <InputNumber min={16} max={30} step={0.5} precision={1} addonAfter="°C" className="command-number-input" />
                </Form.Item>
                <Button type="primary" htmlType="submit" block disabled={!mayCreate} loading={createMutation.isPending}>
                  提交 Canonical Command
                </Button>
              </Form>
              {!can(role, 'create', 'command') ? (
                <Typography.Paragraph type="secondary" className="command-permission-note">
                  当前角色没有 Command 提交权限。
                </Typography.Paragraph>
              ) : null}
            </Card>

            <Card title="按 Command ID 查询" className="command-workbench-card command-lookup-card">
              <Form<LookupFormValues>
                form={lookupForm}
                layout="vertical"
                initialValues={{ commandId: selectedCommandId }}
                onFinish={({ commandId: lookupId }) => navigate(`/commands/${lookupId.trim()}`)}
              >
                <Form.Item name="commandId" rules={[{ required: true, message: '请输入 Command ID' }]}>
                  <Input prefix={<SearchOutlined />} placeholder="Command UUIDv7" autoComplete="off" />
                </Form.Item>
                <Button htmlType="submit" block disabled={!routesAvailable}>打开 Command</Button>
              </Form>
            </Card>
          </Col>

          <Col xs={24} xl={14}>
            {!routesAvailable ? (
              <Result
                status="warning"
                title="生产 Command 路由保持 disabled"
                subTitle="页面不会尝试绕过 Route Ownership Registry。启用前必须完成 S3-09 内部低风险 Canary 与正式审批。"
              />
            ) : commandQuery.isLoading ? (
              <Card loading title="读取 Command 权威状态" />
            ) : commandQuery.isError ? (
              <Result status="error" title="无法读取 Command" subTitle={commandErrorMessage(commandQuery.error)} />
            ) : commandQuery.data ? (
              <CommandDetail
                command={commandQuery.data}
                canApprove={mayApprove}
                approving={approveMutation.isPending}
                onApprove={() => approveMutation.mutate(commandQuery.data.commandId)}
              />
            ) : (
              <Result title="输入 Command ID 查看状态时间线" />
            )}
          </Col>
        </Row>
      </Space>
    </PageScaffold>
  );
}
