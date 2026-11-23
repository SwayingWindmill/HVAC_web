import type { ColumnDef } from '@tanstack/react-table';
import { Pencil, RefreshCw, Trash2 } from 'lucide-react';
import type { Capability, Device, TelemetryPoint } from '@/api/generated/platformGateway.gen';
import type { DataTableFeatures } from '@/components/data-table';
import { StatusBadge } from '@/components/status-badge';
import { Button } from '@/components/ui/button';
import {
  RESOURCE_COPY,
  WRITE_CAPABILITY,
  editTargetFromRow,
  pointSubtypeLabel,
  statusLabel,
  statusTone,
  type EditTarget,
  type ResourceRow,
} from './resource-workbench-model';
import { formatTagEntries } from './tags-model';

interface CreateResourceColumnsOptions {
  readonly filterableType: boolean;
  readonly allSites: boolean;
  readonly siteNameById: ReadonlyMap<string, string>;
  readonly capabilities: ReadonlySet<Capability>;
  readonly retireAllowed: boolean;
  readonly onOpenEditor: (target: EditTarget) => void;
  readonly onReplaceHardware: (device: Device) => void;
  readonly onRetireRow: (row: ResourceRow) => void;
  readonly onSelectDevice: (deviceId: string) => void;
}

export function createResourceColumns({
  filterableType,
  allSites,
  siteNameById,
  capabilities,
  retireAllowed,
  onOpenEditor,
  onReplaceHardware,
  onRetireRow,
  onSelectDevice,
}: CreateResourceColumnsOptions): Array<ColumnDef<DataTableFeatures, ResourceRow>> {
  const base: Array<ColumnDef<DataTableFeatures, ResourceRow>> = [
    {
      id: 'name',
      header: '名称',
      cell: ({ row }) => (
        <div>
          <strong className="block text-sm">{row.original.displayName}</strong>
          <span className="font-mono text-xs text-muted-foreground">{row.original.code}</span>
          {row.original.type === 'DEVICE' && row.original.entity.externalId ? (
            <span className="block font-mono text-[11px] text-muted-foreground">
              硬件标识: {row.original.entity.externalId}
            </span>
          ) : null}
        </div>
      ),
    },
    { id: 'subtype', header: '类型', cell: ({ row }) => row.original.subtype },
  ];

  if (filterableType && allSites) {
    base.push({
      id: 'site',
      header: '所属站点',
      cell: ({ row }) => (
        <span className="text-xs text-muted-foreground">
          {(row.original.siteId && siteNameById.get(row.original.siteId)) ?? row.original.siteId?.slice(0, 8) ?? '-'}
        </span>
      ),
    });
  }

  if (filterableType) {
    base.push({
      id: 'tags',
      header: '标签',
      cell: ({ row }) => {
        const entries = formatTagEntries(row.original.tags);
        if (entries.length === 0) {
          return <span className="text-xs text-muted-foreground">未设标签</span>;
        }
        return (
          <div className="flex flex-wrap gap-1">
            {entries.slice(0, 3).map((token) => (
              <span key={token} className="rounded bg-secondary px-1.5 py-0.5 font-mono text-[11px] text-secondary-foreground">
                {token}
              </span>
            ))}
            {entries.length > 3 ? (
              <span className="text-[11px] text-muted-foreground">+{entries.length - 3}</span>
            ) : null}
          </div>
        );
      },
    });
  }

  base.push(
    {
      id: 'status',
      header: '状态',
      cell: ({ row }) => <StatusBadge tone={statusTone(row.original.status)}>{statusLabel(row.original.status)}</StatusBadge>,
    },
    {
      id: 'actions',
      header: '操作',
      cell: ({ row }) => (
        <div className="flex flex-wrap gap-1">
          <Button
            variant="ghost"
            size="sm"
            disabled={!capabilities.has(WRITE_CAPABILITY[row.original.type]) || row.original.status === 'RETIRED'}
            onClick={() => onOpenEditor(editTargetFromRow(row.original))}
          >
            <Pencil />编辑
          </Button>
          {row.original.type === 'DEVICE' ? (
            <Button
              variant="ghost"
              size="sm"
              disabled={!capabilities.has('device.write') || row.original.status !== 'ACTIVE'}
              onClick={() => {
                if (row.original.type !== 'DEVICE') return;
                onReplaceHardware(row.original.entity);
              }}
            >
              <RefreshCw />更换硬件
            </Button>
          ) : null}
          <Button
            variant="ghost"
            size="sm"
            className="text-destructive hover:text-destructive"
            disabled={!retireAllowed || row.original.status === 'RETIRED'}
            onClick={() => onRetireRow(row.original)}
          >
            <Trash2 />{RESOURCE_COPY[row.original.type].retireAction}
          </Button>
          {row.original.type === 'DEVICE' ? (
            <Button variant="ghost" size="sm" onClick={() => onSelectDevice(row.original.id)}>
              查看点位
            </Button>
          ) : null}
        </div>
      ),
      enableSorting: false,
      enableHiding: false,
    },
  );
  return base;
}

export function createDevicePointColumns(): Array<ColumnDef<DataTableFeatures, TelemetryPoint>> {
  return [
    {
      id: 'point',
      header: '点位',
      cell: ({ row }) => (
        <div>
          <strong className="block text-sm">{row.original.displayName}</strong>
          <span className="font-mono text-xs text-muted-foreground">{row.original.pointCode}</span>
        </div>
      ),
    },
    { id: 'sourceKey', header: '来源键', cell: ({ row }) => <span className="font-mono text-xs">{row.original.sourceKey}</span> },
    { id: 'type', header: '类型', cell: ({ row }) => <>{pointSubtypeLabel(row.original)}</> },
    { id: 'writable', header: '可写', cell: ({ row }) => row.original.writable ? '是' : '否' },
  ];
}
