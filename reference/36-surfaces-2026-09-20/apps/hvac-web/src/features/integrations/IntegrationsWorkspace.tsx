import { useState, useMemo } from 'react';
import type { ColumnDef } from '@tanstack/react-table';
import {
  PlugZap,
  Server,
  RefreshCw,
  Clock,
  Search,
  Terminal,
  Radio,
  RotateCw,
  Trash2,
} from 'lucide-react';
import { Main } from '@/components/layout/Main';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Checkbox } from '@/components/ui/checkbox';
import {
  DataTable,
  DataTableViewPills,
  StatusPillBadge,
  DataTableViewOptions,
  DataTablePagination,
  type DataTableFeatures,
  type DataTableViewPillOption,
} from '@/components/data-table';
import { useDataTable } from '@/hooks/use-data-table';

interface Connector {
  readonly id: string;
  readonly name: string;
  readonly protocol: 'BACnet/IP' | 'Modbus TCP' | 'OPC-UA' | 'MQTT' | 'REST Webhook';
  readonly endpoint: string;
  readonly authType: string;
  readonly p95LatencyMs: number;
  readonly successRate: number;
  readonly pointsCount: number;
  readonly status: 'ONLINE' | 'DEGRADED' | 'OFFLINE';
  readonly lastHeartbeat: string;
  readonly probeOutput: string[];
}

const CONNECTORS: readonly Connector[] = [
  {
    id: 'CONN-BACNET-01',
    name: 'BMS 楼宇自控系统 BACnet/IP 主网关',
    protocol: 'BACnet/IP',
    endpoint: '192.168.10.250:47808 (Device: 1001)',
    authType: '双向白名单 IP 认证',
    p95LatencyMs: 85,
    successRate: 99.95,
    pointsCount: 840,
    status: 'ONLINE',
    lastHeartbeat: '2秒前',
    probeOutput: [
      '[00:26:01] 正在向 192.168.10.250:47808 发起 BACnet Who-Is 探测...',
      '[00:26:01] 收到 I-Am 广播响应：Device Instance 1001, 网络号 0',
      '[00:26:02] 供应商信息：Honeywell Building Solutions (Vendor ID: 17)',
      '[00:26:02] 固件版本：CP-CORE v4.12.0 / 最大 APDU 长度: 1476 字节',
      '[00:26:02] 读取模拟量输入点位 (AI:0 ~ AI:40) 耗时 42ms，校验和匹配',
      '[00:26:02] 探测结论：通信链路极佳，RTT 85ms，心跳周期 10s',
    ],
  },
  {
    id: 'CONN-MODBUS-CH',
    name: '冷水机房电力监控与变频器 Modbus TCP 通讯机',
    protocol: 'Modbus TCP',
    endpoint: '192.168.10.251:502 (Unit ID: 1~32)',
    authType: '内网专用 VLAN 隔离',
    p95LatencyMs: 142,
    successRate: 99.82,
    pointsCount: 360,
    status: 'ONLINE',
    lastHeartbeat: '5秒前',
    probeOutput: [
      '[00:26:05] 连接 Modbus TCP 端口 192.168.10.251:502...',
      '[00:26:05] 发送功能码 03 (Read Holding Registers, 起始地址 40001, 长度 16)...',
      '[00:26:06] 从机 Unit ID: 1 (1# 变压器多功能表) 响应正常，数据长度 32 字节',
      '[00:26:06] CRC16 校验码校验通过 (0xC5CD)',
      '[00:26:06] 探测结论：网关吞吐正常，无连线丢包，平均延迟 142ms',
    ],
  },
  {
    id: 'CONN-OPCUA-EMS',
    name: '热网计量与能源站 SCADA OPC-UA 驱动',
    protocol: 'OPC-UA',
    endpoint: 'opc.tcp://192.168.20.10:4840',
    authType: 'X.509 证书 (SHA256withRSA)',
    p95LatencyMs: 110,
    successRate: 99.98,
    pointsCount: 180,
    status: 'ONLINE',
    lastHeartbeat: '1秒前',
    probeOutput: [
      '[00:26:10] 握手 opc.tcp://192.168.20.10:4840...',
      '[00:26:10] 安全策略：Basic256Sha256 / 消息模式：SignAndEncrypt',
      '[00:26:10] 校验对端服务端 X.509 证书有效性... 有效期至 2027-12-31 (合法)',
      '[00:26:11] 创建会话 SessionID: ns=1;s=hvac_collector_0918，激活成功',
      '[00:26:11] 批量订阅 180 个热力测点变化，发布周期 1000ms',
      '[00:26:11] 探测结论：安全会话活跃，数据包加密流顺畅',
    ],
  },
  {
    id: 'CONN-MQTT-IOT',
    name: '无线 LoRaWAN 室内环境温湿度 MQTT Broker',
    protocol: 'MQTT',
    endpoint: 'tls://mqtt.iot.internal:8883',
    authType: 'TLS v1.3 + 双向密码认证',
    p95LatencyMs: 65,
    successRate: 99.99,
    pointsCount: 40,
    status: 'ONLINE',
    lastHeartbeat: '3秒前',
    probeOutput: [
      '[00:26:15] 发起 TLS v1.3 握手至 mqtt.iot.internal:8883...',
      '[00:26:15] 密码套件：TLS_AES_256_GCM_SHA384 (握手用时 18ms)',
      '[00:26:15] 发送 CONNECT 报文，客户端 ID: hvac_gateway_sub_01',
      '[00:26:15] 收到 CONNACK 返回码 0 (连接被接受)',
      '[00:26:16] 订阅主题: site/tokyo/indoor/+/telemetry (QoS 1)',
      '[00:26:16] 探测结论：Broker 活跃，吞吐正常，P95 时延 65ms',
    ],
  },
  {
    id: 'CONN-REST-WEATHER',
    name: '气象信息中心室外气象数据 REST Webhook',
    protocol: 'REST Webhook',
    endpoint: 'https://api.cma.gov.cn/v1/grid',
    authType: 'OAuth2 access token (hourly refresh)',
    p95LatencyMs: 380,
    successRate: 98.8,
    pointsCount: 20,
    status: 'ONLINE',
    lastHeartbeat: '10分钟前',
    probeOutput: [
      '[00:26:20] GET https://api.cma.gov.cn/v1/grid?lat=35.68&lon=139.76',
      '[00:26:20] authorization header injected (credential hidden)',
      '[00:26:21] HTTP 200 OK, Content-Type: application/json',
      '[00:26:21] 接收网格气象数据：干球温度 26.2℃, 湿球温度 22.4℃, 气压 101.3kPa',
      '[00:26:21] 探测结论：外网接口正常，更新周期 15min',
    ],
  },
];

interface DeadLetterRecord {
  id: string;
  time: string;
  connector: string;
  errorType: string;
  payload: string;
  retries: number;
  resolved: string;
  status: 'RETRYING' | 'RESOLVED' | 'DISCARDED';
}

const INITIAL_DEAD_LETTERS: DeadLetterRecord[] = [
  {
    id: 'DLQ-01',
    time: '2026-09-18 11:42:15',
    connector: 'CONN-MODBUS-CH',
    errorType: 'CRC16 校验和错误 (校验失败重传)',
    payload: '01 03 00 00 00 0A C5 CD',
    retries: 2,
    resolved: '已通过第 2 次重传成功校核',
    status: 'RESOLVED',
  },
  {
    id: 'DLQ-02',
    time: '2026-09-18 08:15:02',
    connector: 'CONN-BACNET-01',
    errorType: 'APDU Timeout 超时未响应',
    payload: 'ReadPropertyRequest (InvokeID: 88, Object: AI:12)',
    retries: 1,
    resolved: '网络抖动重传成功',
    status: 'RESOLVED',
  },
];

export function IntegrationsWorkspace() {
  const [searchQuery, setSearchQuery] = useState('');
  const [protocolFilter, setProtocolFilter] = useState<string>('ALL');
  const [activeProbe, setActiveProbe] = useState<Connector | null>(null);
  const [deadLetters, setDeadLetters] = useState<DeadLetterRecord[]>(INITIAL_DEAD_LETTERS);

  const [dlqStatusFilter, setDlqStatusFilter] = useState<string>('ALL');
  const [dlqSearchQuery, setDlqSearchQuery] = useState('');

  const protocolPills: readonly DataTableViewPillOption[] = useMemo(() => [
    { key: 'ALL', label: '全部协议', count: CONNECTORS.length },
    { key: 'BACnet/IP', label: 'BACnet/IP', count: CONNECTORS.filter((c) => c.protocol === 'BACnet/IP').length },
    { key: 'Modbus TCP', label: 'Modbus TCP', count: CONNECTORS.filter((c) => c.protocol === 'Modbus TCP').length },
    { key: 'OPC-UA', label: 'OPC-UA', count: CONNECTORS.filter((c) => c.protocol === 'OPC-UA').length },
    { key: 'MQTT', label: 'MQTT', count: CONNECTORS.filter((c) => c.protocol === 'MQTT').length },
    { key: 'REST Webhook', label: 'REST Webhook', count: CONNECTORS.filter((c) => c.protocol === 'REST Webhook').length },
  ], []);

  const dlqStatusPills: readonly DataTableViewPillOption[] = useMemo(() => [
    { key: 'ALL', label: '全部记录', count: deadLetters.length },
    { key: 'RESOLVED', label: '已校核/入库', count: deadLetters.filter((d) => d.status === 'RESOLVED').length },
    { key: 'RETRYING', label: '重试中', count: deadLetters.filter((d) => d.status === 'RETRYING').length },
    { key: 'DISCARDED', label: '已丢弃', count: deadLetters.filter((d) => d.status === 'DISCARDED').length },
  ], [deadLetters]);

  const filteredConnectors = useMemo(() => {
    return CONNECTORS.filter((c) => {
      if (protocolFilter !== 'ALL' && c.protocol !== protocolFilter) return false;
      if (!searchQuery.trim()) return true;
      const q = searchQuery.toLowerCase();
      return (
        c.name.toLowerCase().includes(q) ||
        c.endpoint.toLowerCase().includes(q) ||
        c.protocol.toLowerCase().includes(q) ||
        c.id.toLowerCase().includes(q)
      );
    });
  }, [searchQuery, protocolFilter]);

  const filteredDlq = useMemo(() => {
    return deadLetters.filter((item) => {
      if (dlqStatusFilter !== 'ALL' && item.status !== dlqStatusFilter) return false;
      if (!dlqSearchQuery.trim()) return true;
      const q = dlqSearchQuery.toLowerCase();
      return (
        item.id.toLowerCase().includes(q) ||
        item.connector.toLowerCase().includes(q) ||
        item.errorType.toLowerCase().includes(q) ||
        item.payload.toLowerCase().includes(q)
      );
    });
  }, [deadLetters, dlqStatusFilter, dlqSearchQuery]);

  const handleRetryDeadLetter = (id: string) => {
    setDeadLetters((prev) =>
      prev.map((item) =>
        item.id === id ? { ...item, status: 'RESOLVED', resolved: '手动触发重试成功，已入库' } : item
      )
    );
  };

  const handleDiscardDeadLetter = (id: string) => {
    setDeadLetters((prev) =>
      prev.map((item) =>
        item.id === id ? { ...item, status: 'DISCARDED', resolved: '已人工确认弃用丢弃' } : item
      )
    );
  };

  const connectorColumns = useMemo<Array<ColumnDef<DataTableFeatures, Connector>>>(() => [
    {
      id: 'select',
      header: ({ table }) => (
        <Checkbox
          checked={table.getIsAllPageRowsSelected() ? true : table.getIsSomePageRowsSelected() ? 'indeterminate' : false}
          onCheckedChange={(checked) => table.toggleAllPageRowsSelected(Boolean(checked))}
          aria-label="选择全部驱动"
        />
      ),
      cell: ({ row }) => (
        <Checkbox
          checked={row.getIsSelected()}
          onCheckedChange={(checked) => row.toggleSelected(Boolean(checked))}
          aria-label={`选择 ${row.original.name}`}
        />
      ),
      enableSorting: false,
      enableHiding: false,
    },
    { id: 'id', header: '驱动标识', cell: ({ row }) => <span className="font-mono text-[11px] font-medium text-foreground">{row.original.id}</span> },
    {
      id: 'name',
      header: '驱动服务名称',
      cell: ({ row }) => <div><div className="font-medium leading-snug text-foreground">{row.original.name}</div><div className="text-[10px] text-muted-foreground">心跳: {row.original.lastHeartbeat}</div></div>,
    },
    { id: 'protocol', header: '通信协议', cell: ({ row }) => <span className="font-mono text-[11px] font-medium text-foreground">{row.original.protocol}</span> },
    { id: 'endpoint', header: '通信端点 (Endpoint)', cell: ({ row }) => <span className="font-mono text-[11px] text-muted-foreground">{row.original.endpoint}</span> },
    { id: 'authType', header: '安全鉴权机制', cell: ({ row }) => <span className="text-[11px] text-muted-foreground">{row.original.authType}</span> },
    { id: 'p95LatencyMs', header: 'P95 延迟', cell: ({ row }) => <span className="font-mono text-[11px] text-foreground tabular-nums">{row.original.p95LatencyMs} ms</span> },
    { id: 'successRate', header: '成功率', cell: ({ row }) => <span className="font-mono text-[11px] font-medium text-emerald-600 tabular-nums">{row.original.successRate}%</span> },
    { id: 'pointsCount', header: '测点数', cell: ({ row }) => <span className="font-mono text-[11px] text-foreground tabular-nums">{row.original.pointsCount} 点</span> },
    {
      id: 'status',
      header: '连接状态',
      cell: ({ row }) => <StatusPillBadge label={row.original.status} tone={row.original.status === 'ONLINE' ? 'success' : row.original.status === 'DEGRADED' ? 'warning' : 'destructive'} />,
    },
    {
      id: 'actions',
      header: '操作',
      cell: ({ row }) => <Button variant="ghost" size="sm" onClick={() => setActiveProbe(row.original)} className="h-7 gap-1 px-2 text-xs text-primary hover:bg-primary/10 hover:text-primary"><Terminal className="h-3.5 w-3.5" />测试探针</Button>,
      enableSorting: false,
    },
  ], []);

  const connectorTable = useDataTable({
    key: 'surface-32-integrations-connectors',
    data: [...filteredConnectors],
    columns: connectorColumns,
    pageSize: 10,
    getRowId: (row) => row.id,
  });

  const dlqColumns = useMemo<Array<ColumnDef<DataTableFeatures, DeadLetterRecord>>>(() => [
    {
      id: 'select',
      header: ({ table }) => (
        <Checkbox
          checked={table.getIsAllPageRowsSelected() ? true : table.getIsSomePageRowsSelected() ? 'indeterminate' : false}
          onCheckedChange={(checked) => table.toggleAllPageRowsSelected(Boolean(checked))}
          aria-label="选择全部记录"
        />
      ),
      cell: ({ row }) => (
        <Checkbox
          checked={row.getIsSelected()}
          onCheckedChange={(checked) => row.toggleSelected(Boolean(checked))}
          aria-label={`选择 ${row.original.id}`}
        />
      ),
      enableSorting: false,
      enableHiding: false,
    },
    { id: 'id', header: '记录编号', cell: ({ row }) => <span className="font-mono text-[11px] font-medium text-foreground">{row.original.id}</span> },
    { id: 'time', header: '发生时间', cell: ({ row }) => <span className="font-mono text-[11px] text-muted-foreground tabular-nums">{row.original.time}</span> },
    { id: 'connector', header: '关联驱动', cell: ({ row }) => <span className="font-mono text-[11px] text-foreground">{row.original.connector}</span> },
    { id: 'errorType', header: '异常根因诊断', cell: ({ row }) => <span className="font-medium text-foreground">{row.original.errorType}</span> },
    { id: 'payload', header: '原始十六进制 / 请求报文', cell: ({ row }) => <span className="font-mono text-[11px] text-muted-foreground">{row.original.payload}</span> },
    { id: 'retries', header: '重试次数', cell: ({ row }) => <span className="font-mono font-medium tabular-nums">{row.original.retries}</span> },
    {
      id: 'resolved',
      header: '处置判定与核验结论',
      cell: ({ row }) => <StatusPillBadge label={row.original.resolved} tone={row.original.status === 'RESOLVED' ? 'success' : row.original.status === 'DISCARDED' ? 'neutral' : 'warning'} />,
    },
    {
      id: 'actions',
      header: '操作',
      cell: ({ row }) => row.original.status !== 'RESOLVED' && row.original.status !== 'DISCARDED' ? (
        <div className="flex items-center justify-end gap-1">
          <Button variant="ghost" size="sm" onClick={() => handleRetryDeadLetter(row.original.id)} className="h-6 px-1.5 text-xs text-primary"><RotateCw className="mr-1 h-3 w-3" />重试</Button>
          <Button variant="ghost" size="sm" onClick={() => handleDiscardDeadLetter(row.original.id)} className="h-6 px-1.5 text-xs text-destructive"><Trash2 className="mr-1 h-3 w-3" />丢弃</Button>
        </div>
      ) : <span className="text-[10px] text-muted-foreground">已归档</span>,
      enableSorting: false,
    },
  ], []);

  const dlqTable = useDataTable({
    key: 'surface-32-integrations-dlq',
    data: [...filteredDlq],
    columns: dlqColumns,
    pageSize: 10,
    getRowId: (row) => row.id,
  });

  return (
    <Main className="space-y-6">
      {/* 顶部标题栏 */}
      <div className="flex flex-col gap-3 md:flex-row md:items-center md:justify-between border-b pb-4">
        <div>
          <h1 className="text-2xl font-bold tracking-tight text-foreground">集成管理</h1>
          <p className="text-sm text-muted-foreground mt-1">
            管理现场通信协议连接器（BACnet、Modbus、OPC-UA、MQTT）、采集通信质量与失败重试记录
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" className="h-8 text-xs gap-1.5">
            <RefreshCw className="h-3.5 w-3.5" />
            刷新驱动状态
          </Button>
          <Button size="sm" className="h-8 text-xs gap-1.5">
            <PlugZap className="h-3.5 w-3.5" />
            新建协议连接器
          </Button>
        </div>
      </div>

      {/* 紧凑型工业驱动舰队状态栏 (替代模板 4 卡片) */}
      <div className="rounded-lg border border-border/80 bg-muted/20 p-4">
        <div className="grid grid-cols-2 gap-4 md:grid-cols-4 md:divide-x md:divide-border/60">
          <div className="space-y-1">
            <div className="text-[11px] font-medium text-muted-foreground">活跃协议连接器</div>
            <div className="flex items-baseline gap-2">
              <span className="text-xl font-bold tracking-tight tabular-nums text-foreground">5 / 5</span>
              <span className="text-xs font-medium text-emerald-600 flex items-center gap-1">
                <Radio className="h-3 w-3 animate-pulse" /> 全部在线
              </span>
            </div>
            <div className="text-[11px] text-muted-foreground">
              BACnet, Modbus, OPC-UA, MQTT, REST
            </div>
          </div>

          <div className="space-y-1 md:pl-6">
            <div className="text-[11px] font-medium text-muted-foreground">接入测点总数</div>
            <div className="flex items-baseline gap-2">
              <span className="text-xl font-bold tracking-tight tabular-nums text-foreground">1,440</span>
              <span className="text-xs text-muted-foreground">实时测点</span>
            </div>
            <div className="text-[11px] text-emerald-600 font-medium">
              语义模型打标率 98.6% (1,420 点)
            </div>
          </div>

          <div className="space-y-1 md:pl-6">
            <div className="text-[11px] font-medium text-muted-foreground">网络延迟与 P95 抖动</div>
            <div className="flex items-baseline gap-2">
              <span className="text-xl font-bold tracking-tight tabular-nums text-foreground">112</span>
              <span className="text-xs text-muted-foreground">ms (P95)</span>
              <span className="text-xs text-emerald-600 font-medium ml-1"><span className="tabular-nums">99.92%</span> 可用</span>
            </div>
            <div className="text-[11px] text-muted-foreground">
              最长外网 API 380ms · OT 内网 ≤ 85ms
            </div>
          </div>

          <div className="space-y-1 md:pl-6">
            <div className="text-[11px] font-medium text-muted-foreground">死信与异常重试队列</div>
            <div className="flex items-baseline gap-2">
              <span className="text-xl font-bold tracking-tight text-emerald-600"><span className="tabular-nums">0</span> 阻塞</span>
              <span className="text-xs text-muted-foreground">/ <span className="tabular-nums">2</span> 次瞬态成功</span>
            </div>
            <div className="text-[11px] text-muted-foreground">
              自动指数退避重传机制已启用
            </div>
          </div>
        </div>
      </div>

      {/* 工作区 Tabs */}
      <Tabs defaultValue="connectors" className="space-y-4">
        <TabsList className="bg-muted/50 p-1 border border-border/60">
          <TabsTrigger value="connectors" className="text-xs gap-1.5">
            <Server className="h-3.5 w-3.5" />
            协议驱动连接器 ({CONNECTORS.length})
          </TabsTrigger>
          <TabsTrigger value="deadletter" className="text-xs gap-1.5">
            <Clock className="h-3.5 w-3.5" />
            死信队列与异常通信重试流水 ({deadLetters.length})
          </TabsTrigger>
        </TabsList>

        {/* Tab 1: 连接器列表 */}
        <TabsContent value="connectors" className="space-y-3 mt-0">
          {/* 工具栏 */}
          <div className="flex flex-col gap-2.5 sm:flex-row sm:items-center sm:justify-between">
            <div className="flex flex-wrap items-center gap-2">
              <div className="relative w-64">
                <Search className="absolute left-2.5 top-2.5 h-3.5 w-3.5 text-muted-foreground" />
                <Input
                  placeholder="按驱动名称 / 协议 / IP / 端口搜索..."
                  value={searchQuery}
                  onChange={(e) => {
                    setSearchQuery(e.target.value);
                    connectorTable.setPageIndex(0);
                  }}
                  className="h-8 pl-8 text-xs bg-background"
                />
              </div>

              <DataTableViewPills
                options={protocolPills}
                value={protocolFilter}
                onValueChange={(v) => {
                  setProtocolFilter(v);
                  connectorTable.setPageIndex(0);
                }}
              />
            </div>

            <div className="flex items-center gap-2">
              <DataTableViewOptions table={connectorTable} />
              <div className="text-xs text-muted-foreground">
                共 <span className="tabular-nums font-medium">{filteredConnectors.length}</span> 个驱动
              </div>
            </div>
          </div>

          <DataTable
            table={connectorTable}
            tableAriaLabel="协议连接器列表"
            empty="无匹配的驱动实例"
            getHeaderRowProps={() => ({ className: 'bg-muted/40 text-xs hover:bg-muted/40' })}
            getHeaderCellProps={(header) => ({
              className:
                header.id === 'select' ? 'w-[40px] px-3' :
                header.id === 'id' ? 'w-[140px] font-medium' :
                header.id === 'name' ? 'w-[200px] font-medium' :
                header.id === 'protocol' ? 'w-[110px] font-medium' :
                header.id === 'endpoint' ? 'w-[220px] font-medium' :
                header.id === 'authType' ? 'w-[170px] font-medium' :
                header.id === 'p95LatencyMs' || header.id === 'successRate' ? 'w-[90px] text-right font-medium' :
                header.id === 'pointsCount' ? 'w-[80px] text-right font-medium' :
                header.id === 'status' ? 'w-[110px] text-center font-medium' :
                header.id === 'actions' ? 'w-[90px] text-right font-medium' :
                undefined,
            })}
            getRowProps={() => ({ className: 'text-xs transition-colors hover:bg-muted/30' })}
            getCellProps={(cell) => ({
              className:
                cell.column.id === 'select' ? 'px-3' :
                ['p95LatencyMs', 'successRate', 'pointsCount', 'actions'].includes(cell.column.id) ? 'text-right' :
                cell.column.id === 'status' ? 'text-center' :
                undefined,
            })}
            footer={(
              <DataTablePagination
                table={connectorTable}
                totalRows={filteredConnectors.length}
                
                
                
                
                
              />
            )}
          />
        </TabsContent>

        {/* Tab 2: 死信重试队列 */}
        <TabsContent value="deadletter" className="space-y-4 mt-0">
          <div className="rounded-lg border border-border/80 overflow-hidden bg-card">
            <div className="p-4 border-b border-border/60 bg-muted/10 flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
              <div>
                <div className="text-sm font-semibold text-foreground">工业通信死信队列与瞬态异常重试流水 (DLQ)</div>
                <div className="text-xs text-muted-foreground mt-0.5">
                  记录通信握手超时、校验和错误、非标编码报文，支持指数退避重试与人工干预重新注入
                </div>
              </div>
              <div className="flex flex-wrap items-center gap-2">
                <div className="relative w-56">
                  <Search className="absolute left-2.5 top-2.5 h-3.5 w-3.5 text-muted-foreground" />
                  <Input
                    placeholder="按记录编号 / 驱动 / 根因搜索..."
                    value={dlqSearchQuery}
                    onChange={(e) => {
                      setDlqSearchQuery(e.target.value);
                      dlqTable.setPageIndex(0);
                    }}
                    className="h-8 pl-8 text-xs bg-background"
                  />
                </div>
                <DataTableViewPills
                  options={dlqStatusPills}
                  value={dlqStatusFilter}
                  onValueChange={(v) => {
                    setDlqStatusFilter(v);
                    dlqTable.setPageIndex(0);
                  }}
                />
                <DataTableViewOptions table={dlqTable} />
              </div>
            </div>
            <DataTable
              table={dlqTable}
              className="gap-0"
              tableAriaLabel="工业通信死信队列"
              empty="无死信与异常通信记录"
              getHeaderRowProps={() => ({ className: 'bg-muted/40 text-xs hover:bg-muted/40' })}
              getHeaderCellProps={(header) => ({
                className:
                  header.id === 'select' ? 'w-[40px] px-3' :
                  header.id === 'id' ? 'w-[100px] font-medium' :
                  header.id === 'time' ? 'w-[150px] font-medium' :
                  header.id === 'connector' ? 'w-[140px] font-medium' :
                  header.id === 'errorType' ? 'w-[220px] font-medium' :
                  header.id === 'payload' ? 'font-medium' :
                  header.id === 'retries' ? 'w-[80px] text-center font-medium' :
                  header.id === 'resolved' ? 'w-[180px] font-medium' :
                  header.id === 'actions' ? 'w-[120px] text-right font-medium' :
                  undefined,
              })}
              getRowProps={() => ({ className: 'text-xs hover:bg-muted/30' })}
              getCellProps={(cell) => ({
                className:
                  cell.column.id === 'select' ? 'px-3' :
                  cell.column.id === 'retries' ? 'text-center' :
                  cell.column.id === 'actions' ? 'text-right' :
                  undefined,
              })}
              footer={(
                <DataTablePagination
                  table={dlqTable}
                  totalRows={filteredDlq.length}
                  
                  
                  
                  
                  
                />
              )}
            />
          </div>
        </TabsContent>
      </Tabs>

      {/* 实时探针测试控制台 (Probe Terminal Dialog) */}
      <Dialog open={Boolean(activeProbe)} onOpenChange={(open) => !open && setActiveProbe(null)}>
        <DialogContent className="sm:max-w-xl">
          <DialogHeader>
            <div className="flex items-center gap-2">
              <Terminal className="h-5 w-5 text-primary" />
              <DialogTitle className="text-base font-bold">南向工业驱动联通性测试探针</DialogTitle>
            </div>
            <DialogDescription className="text-xs font-mono">
              目标: {activeProbe?.name} ({activeProbe?.endpoint})
            </DialogDescription>
          </DialogHeader>

          {activeProbe && (
            <div className="space-y-3 py-2">
              <div className="rounded-md bg-zinc-950 p-3.5 font-mono text-[11px] text-zinc-100 space-y-1.5 max-h-72 overflow-y-auto border border-zinc-800 shadow-inner">
                <div className="text-zinc-500">// 正在发起实时低层协议握手测试...</div>
                {activeProbe.probeOutput.map((line, idx) => (
                  <div key={idx} className={line.includes('结论') ? 'text-emerald-400 font-semibold' : 'text-zinc-300'}>
                    {line}
                  </div>
                ))}
              </div>

              <div className="flex items-center justify-between text-xs pt-1">
                <span className="text-muted-foreground">协议握手成功 · 遥测点位可用度 100%</span>
                <Button size="sm" className="h-8 text-xs" onClick={() => setActiveProbe(null)}>
                  完成并退出探针
                </Button>
              </div>
            </div>
          )}
        </DialogContent>
      </Dialog>
    </Main>
  );
}
