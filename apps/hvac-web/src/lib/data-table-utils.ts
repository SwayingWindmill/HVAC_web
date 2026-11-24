import type { Column, RowData } from '@tanstack/react-table';

import type {
  ExtendedColumnFilter,
  FilterOperator,
  FilterVariant,
} from '@/lib/data-table-types';
import type { DataTableFeatures } from '@/components/data-table/data-table-features';

export type DataTableConfig = typeof dataTableConfig;

export const dataTableConfig = {
  textOperators: [
    { label: '包含', value: 'iLike' as const },
    { label: '不包含', value: 'notILike' as const },
    { label: '等于', value: 'eq' as const },
    { label: '不等于', value: 'ne' as const },
    { label: '为空', value: 'isEmpty' as const },
    { label: '不为空', value: 'isNotEmpty' as const },
  ],
  numericOperators: [
    { label: '等于', value: 'eq' as const },
    { label: '不等于', value: 'ne' as const },
    { label: '小于', value: 'lt' as const },
    { label: '小于等于', value: 'lte' as const },
    { label: '大于', value: 'gt' as const },
    { label: '大于等于', value: 'gte' as const },
    { label: '介于', value: 'isBetween' as const },
    { label: '为空', value: 'isEmpty' as const },
    { label: '不为空', value: 'isNotEmpty' as const },
  ],
  dateOperators: [
    { label: '等于', value: 'eq' as const },
    { label: '不等于', value: 'ne' as const },
    { label: '早于', value: 'lt' as const },
    { label: '晚于', value: 'gt' as const },
    { label: '不晚于', value: 'lte' as const },
    { label: '不早于', value: 'gte' as const },
    { label: '介于', value: 'isBetween' as const },
    { label: '相对今天', value: 'isRelativeToToday' as const },
    { label: '为空', value: 'isEmpty' as const },
    { label: '不为空', value: 'isNotEmpty' as const },
  ],
  selectOperators: [
    { label: '等于', value: 'eq' as const },
    { label: '不等于', value: 'ne' as const },
    { label: '为空', value: 'isEmpty' as const },
    { label: '不为空', value: 'isNotEmpty' as const },
  ],
  multiSelectOperators: [
    { label: '包含任一', value: 'inArray' as const },
    { label: '不包含任一', value: 'notInArray' as const },
    { label: '为空', value: 'isEmpty' as const },
    { label: '不为空', value: 'isNotEmpty' as const },
  ],
  booleanOperators: [
    { label: '等于', value: 'eq' as const },
    { label: '不等于', value: 'ne' as const },
  ],
  sortOrders: [
    { label: '升序', value: 'asc' as const },
    { label: '降序', value: 'desc' as const },
  ],
  filterVariants: [
    'text',
    'number',
    'range',
    'date',
    'dateRange',
    'boolean',
    'select',
    'multiSelect',
  ] as const,
  operators: [
    'iLike',
    'notILike',
    'eq',
    'ne',
    'inArray',
    'notInArray',
    'isEmpty',
    'isNotEmpty',
    'lt',
    'lte',
    'gt',
    'gte',
    'isBetween',
    'isRelativeToToday',
  ] as const,
  joinOperators: ['and', 'or'] as const,
};

export function getColumnPinningStyle<TData extends RowData>({
  column,
  withBorder = false,
}: {
  column: Column<DataTableFeatures, TData>;
  withBorder?: boolean;
}): React.CSSProperties {
  const isPinned = column.getIsPinned();
  const isLastLeftPinnedColumn =
    isPinned === 'start' && column.getIsLastColumn('start');
  const isFirstRightPinnedColumn =
    isPinned === 'end' && column.getIsFirstColumn('end');

  return {
    boxShadow: withBorder
      ? isLastLeftPinnedColumn
        ? '-4px 0 4px -4px var(--border) inset'
        : isFirstRightPinnedColumn
          ? '4px 0 4px -4px var(--border) inset'
          : undefined
      : undefined,
    left: isPinned === 'start' ? `${column.getStart('start')}px` : undefined,
    right: isPinned === 'end' ? `${column.getAfter('end')}px` : undefined,
    opacity: isPinned ? 0.97 : 1,
    position: isPinned ? 'sticky' : 'relative',
    background: 'var(--background)',
    width: column.getSize(),
    zIndex: isPinned ? 1 : undefined,
  };
}

export function getFilterOperators(filterVariant: FilterVariant) {
  const operatorMap: Record<
    FilterVariant,
    { label: string; value: FilterOperator }[]
  > = {
    text: dataTableConfig.textOperators,
    number: dataTableConfig.numericOperators,
    range: dataTableConfig.numericOperators,
    date: dataTableConfig.dateOperators,
    dateRange: dataTableConfig.dateOperators,
    boolean: dataTableConfig.booleanOperators,
    select: dataTableConfig.selectOperators,
    multiSelect: dataTableConfig.multiSelectOperators,
  };

  return operatorMap[filterVariant] ?? dataTableConfig.textOperators;
}

export function getDefaultFilterOperator(filterVariant: FilterVariant) {
  const operators = getFilterOperators(filterVariant);
  return operators[0]?.value ?? (filterVariant === 'text' ? 'iLike' : 'eq');
}

export function matchesSelectFilter<TData>(
  actual: string | null | undefined,
  filter: ExtendedColumnFilter<TData>,
): boolean {
  const values = Array.isArray(filter.value) ? filter.value : [filter.value];
  switch (filter.operator) {
    case 'eq':
    case 'inArray':
      return actual !== null && actual !== undefined && values.includes(actual);
    case 'ne':
    case 'notInArray':
      return actual === null || actual === undefined || !values.includes(actual);
    case 'isEmpty':
      return actual === null || actual === undefined || actual.length === 0;
    case 'isNotEmpty':
      return actual !== null && actual !== undefined && actual.length > 0;
    default:
      return false;
  }
}

export function getValidFilters<TData>(
  filters: ExtendedColumnFilter<TData>[],
): ExtendedColumnFilter<TData>[] {
  return filters.filter(
    (filter) =>
      filter.operator === 'isEmpty' ||
      filter.operator === 'isNotEmpty' ||
      (Array.isArray(filter.value)
        ? filter.value.length > 0
        : filter.value !== '' &&
          filter.value !== null &&
          filter.value !== undefined),
  );
}
