import { useMemo } from 'react';
import type { ColumnDef } from '@tanstack/react-table';
import { Eye, Pencil, Plus, Receipt } from 'lucide-react';
import type {
  UtilityBill,
  UtilityBillMeterScope,
  UtilityBillStatus,
} from '@/api/generated/platformGateway.gen';
import { DataTableBlock } from '@/blocks/data-table';
import { StateCard } from '@/blocks/state/state-card';
import { DataTable, DataTablePagination, type DataTableFeatures } from '@/components/data-table';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group';
import { useDataTable } from '@/hooks/use-data-table';
import { formatDecimal } from '@/lib/operator-format';
import {
  formatBillingMonthLabel,
  METER_SCOPE_LABEL,
  UTILITY_BILL_STATUS_LABEL,
} from '../billing-model';
import { describeBillingApiError } from '../query';

const SCOPE_FILTERS: readonly {
  readonly value: 'ALL' | UtilityBillMeterScope;
  readonly label: string;
}[] = [
  { value: 'ALL', label: '全部表计' },
  { value: 'CHILLER_PLANT', label: '冷站专变' },
  { value: 'WHOLE_BUILDING', label: '整栋总表' },
];

const STATUS_FILTERS: readonly {
  readonly value: 'ALL' | UtilityBillStatus;
  readonly label: string;
}[] = [
  { value: 'ALL', label: '全部状态' },
  { value: 'CONFIRMED', label: '已确认归档' },
  { value: 'DRAFT', label: '草稿待确认' },
];

export interface UtilityBillsTabProps {
  readonly items: readonly UtilityBill[];
  readonly isLoading: boolean;
  readonly isError: boolean;
  readonly error: unknown;
  readonly scopeFilter: UtilityBillMeterScope | undefined;
  readonly statusFilter: UtilityBillStatus | undefined;
  readonly canManageBilling: boolean;
  readonly onRetry: () => void;
  readonly onScopeFilterChange: (next: UtilityBillMeterScope | undefined) => void;
  readonly onStatusFilterChange: (next: UtilityBillStatus | undefined) => void;
  readonly onInspectBill: (billId: string) => void;
  readonly onEditDraft: (bill: UtilityBill) => void;
  readonly onOpenCreateBillDialog: () => void;
}

export function UtilityBillsTab({
  items,
  isLoading,
  isError,
  error,
  scopeFilter,
  statusFilter,
  canManageBilling,
  onRetry,
  onScopeFilterChange,
  onStatusFilterChange,
  onInspectBill,
  onEditDraft,
  onOpenCreateBillDialog,
}: UtilityBillsTabProps) {
  const rows = useMemo(() => [...items], [items]);

  // Strictly 5 columns per spec
  const columns = useMemo<ColumnDef<DataTableFeatures, UtilityBill>[]>(
    () => [
      {
        id: 'billingMonth',
        header: () => '账期与单据号',
        cell: ({ row }) => {
          const bill = row.original;
          return (
            <div>
              <div className="font-medium text-foreground">
                {formatBillingMonthLabel(bill.billingMonth)}
              </div>
              <div className="mt-0.5 text-xs text-muted-foreground">
                {bill.invoiceNo
                  ? `单据号 ${bill.invoiceNo}`
                  : bill.utilityAccountNo
                    ? `户号 ${bill.utilityAccountNo}`
                    : `由 ${bill.updatedByDisplay || '管理员'} 维护`}
              </div>
            </div>
          );
        },
      },
      {
        id: 'meterScope',
        header: () => '表计范围',
        cell: ({ row }) => (
          <Badge variant="outline" className="text-foreground">
            {METER_SCOPE_LABEL[row.original.meterScope]}
          </Badge>
        ),
      },
      {
        id: 'readingWindow',
        header: () => '供电局抄表周期',
        cell: ({ row }) => {
          const bill = row.original;
          return (
            <div className="tabular-nums">
              <div>
                {bill.readingStartDate} 至 {bill.readingEndDate}
              </div>
              <div className="mt-0.5 text-xs text-muted-foreground">
                结算电量 {formatDecimal(bill.billedEnergyKwh, 2)} kWh
              </div>
            </div>
          );
        },
      },
      {
        id: 'totalPayableCny',
        header: () => '应收总电费',
        cell: ({ row }) => {
          const bill = row.original;
          return (
            <div className="text-right tabular-nums">
              <div className="font-semibold text-foreground">
                ¥{formatDecimal(bill.totalPayableCny, 2)}
              </div>
              <div className="mt-0.5 text-xs text-muted-foreground">
                电度 ¥{formatDecimal(bill.energyChargeCny, 2)}
                {bill.powerFactorAdjustmentCny < 0
                  ? ` · 力调减免 ¥${formatDecimal(Math.abs(bill.powerFactorAdjustmentCny), 2)}`
                  : bill.basicChargeCny > 0
                    ? ` · 基本 ¥${formatDecimal(bill.basicChargeCny, 2)}`
                    : ''}
              </div>
            </div>
          );
        },
      },
      {
        id: 'actions',
        header: () => '状态与操作',
        cell: ({ row }) => {
          const bill = row.original;
          return (
            <div className="inline-flex flex-wrap items-center justify-end gap-1.5">
              <Badge variant={bill.status === 'CONFIRMED' ? 'success' : 'warning'}>
                {UTILITY_BILL_STATUS_LABEL[bill.status]}
              </Badge>
              <Button
                type="button"
                size="sm"
                variant="outline"
                onClick={() => onInspectBill(bill.id)}
              >
                <Eye data-icon="inline-start" />
                核对详情
              </Button>
              {bill.status === 'DRAFT' && canManageBilling ? (
                <Button
                  type="button"
                  size="sm"
                  variant="ghost"
                  onClick={() => onEditDraft(bill)}
                >
                  <Pencil data-icon="inline-start" />
                  编辑
                </Button>
              ) : null}
            </div>
          );
        },
      },
    ],
    [canManageBilling, onEditDraft, onInspectBill],
  );

  const table = useDataTable({
    key: 'utility-bills',
    data: rows,
    columns,
    pageSize: 20,
    getRowId: (row) => row.id,
  });

  const emptyContent = (
    <div className="flex flex-col items-center justify-center gap-2.5 px-6 py-12 text-center">
      <div className="flex size-10 items-center justify-center rounded-full bg-muted text-muted-foreground">
        <Receipt className="size-5" aria-hidden="true" />
      </div>
      <p className="text-sm font-medium text-foreground">
        {scopeFilter || statusFilter
          ? '当前筛选条件下没有匹配的结算电费单'
          : '尚未录入任何供电局结算电费单'}
      </p>
      <p className="max-w-md text-xs text-muted-foreground">
        {scopeFilter || statusFilter
          ? '可切换上方表计范围或状态筛选查看其他账期记录。'
          : '收到供电局月度电费发票或结算通知单后在此录入，即可核对冷站专变计量偏差或折算整栋总表中的冷站分摊电费。'}
      </p>
      {!scopeFilter && !statusFilter && canManageBilling ? (
        <Button type="button" size="sm" variant="outline" onClick={onOpenCreateBillDialog}>
          <Plus data-icon="inline-start" />
          录入结算电费单
        </Button>
      ) : null}
    </div>
  );

  return (
    <DataTableBlock
      title="结算电费单"
      description="按供电局实际抄表周期归档两部制结算电费单；同一账期与同一表计范围仅允许一张已确认归档单据"
      actions={
        <div className="flex flex-wrap items-center gap-2">
          <ToggleGroup
            type="single"
            size="sm"
            variant="outline"
            value={scopeFilter ?? 'ALL'}
            onValueChange={(value) => {
              if (!value) return;
              table.setPageIndex(0);
              onScopeFilterChange(value === 'ALL' ? undefined : (value as UtilityBillMeterScope));
            }}
            aria-label="表计范围筛选"
          >
            {SCOPE_FILTERS.map((filter) => (
              <ToggleGroupItem key={filter.value} value={filter.value} className="text-xs">
                {filter.label}
              </ToggleGroupItem>
            ))}
          </ToggleGroup>

          <ToggleGroup
            type="single"
            size="sm"
            variant="outline"
            value={statusFilter ?? 'ALL'}
            onValueChange={(value) => {
              if (!value) return;
              table.setPageIndex(0);
              onStatusFilterChange(value === 'ALL' ? undefined : (value as UtilityBillStatus));
            }}
            aria-label="结算电费单状态筛选"
          >
            {STATUS_FILTERS.map((filter) => (
              <ToggleGroupItem key={filter.value} value={filter.value} className="text-xs">
                {filter.label}
              </ToggleGroupItem>
            ))}
          </ToggleGroup>
        </div>
      }
    >
      {isError ? (
        <StateCard
          tone="danger"
          title={<h3 className="text-base font-semibold">无法读取结算电费单</h3>}
          description={describeBillingApiError(error, '请检查网络连接或稍后重试')}
        >
          <Button variant="outline" size="sm" onClick={onRetry}>
            重试
          </Button>
        </StateCard>
      ) : isLoading ? (
        <Skeleton className="h-48 w-full rounded-xl" />
      ) : (
        <DataTable
          table={table}
          tableAriaLabel="结算电费单"
          empty={emptyContent}
          getHeaderCellProps={(header) => ({
            className:
              header.id === 'billingMonth'
                ? 'pl-5'
                : header.id === 'totalPayableCny'
                  ? 'text-right'
                  : header.id === 'actions'
                    ? 'pr-5 text-right'
                    : undefined,
          })}
          getCellProps={(cell) => ({
            className:
              cell.column.id === 'billingMonth'
                ? 'pl-5 align-top'
                : cell.column.id === 'totalPayableCny'
                  ? 'align-top whitespace-nowrap'
                  : cell.column.id === 'actions'
                    ? 'pr-5 text-right align-top whitespace-nowrap'
                    : 'align-top whitespace-nowrap',
          })}
          footer={rows.length > 0 ? <DataTablePagination table={table} totalRows={rows.length} /> : null}
        />
      )}
    </DataTableBlock>
  );
}
