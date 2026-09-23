import {
  useTable,
  type ColumnDef,
  type RowData,
} from '@tanstack/react-table';

import {
  dataTableFeatures,
  type DataTableFeatures,
} from '@/components/data-table/data-table-features';
import type { DataTableMeta } from '@/lib/data-table-types';

interface UseDataTableOptions<TData extends RowData> {
  readonly key: string;
  readonly data: TData[];
  readonly columns: ColumnDef<DataTableFeatures, TData>[];
  readonly pageSize?: number;
  readonly paginate?: boolean;
  readonly getRowId?: (row: TData, index: number) => string;
  readonly meta?: DataTableMeta;
}

export function useDataTable<TData extends RowData>({
  key,
  data,
  columns,
  pageSize = 10,
  paginate = true,
  getRowId,
  meta,
}: UseDataTableOptions<TData>) {
  return useTable({
    key,
    features: dataTableFeatures,
    data,
    columns,
    meta,
    initialState: {
      pagination: {
        pageIndex: 0,
        pageSize: paginate ? pageSize : Number.MAX_SAFE_INTEGER,
      },
    },
    getRowId,
  });
}
