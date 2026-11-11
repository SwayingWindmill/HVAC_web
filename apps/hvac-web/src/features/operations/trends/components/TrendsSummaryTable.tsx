import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { formatCompactTime, formatDecimal } from '@/lib/operator-format';
import type { TrendSummaryRow } from '../trends-model';

function formatCellValue(value: number | null, precision: number, unit: string): string {
  if (value === null) return '—';
  const formatted = formatDecimal(value, precision);
  return unit ? `${formatted} ${unit}` : formatted;
}

export function TrendsSummaryTable({
  rows,
  timezone,
}: {
  readonly rows: readonly TrendSummaryRow[];
  readonly timezone: string;
}) {
  return (
    <Table aria-label="趋势统计表">
      <TableHeader>
        <TableRow>
          <TableHead>点位</TableHead>
          <TableHead className="text-right">最新值</TableHead>
          <TableHead className="text-right">平均值</TableHead>
          <TableHead className="text-right">最小值</TableHead>
          <TableHead className="text-right">最大值</TableHead>
          <TableHead className="text-right">覆盖率</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {rows.map((row) => (
          <TableRow key={row.ref}>
            <TableCell className="font-medium">
              <span>{row.label}</span>
              {row.isSetpoint ? (
                <span className="ml-1.5 text-xs font-normal text-muted-foreground">设定值</span>
              ) : null}
            </TableCell>
            <TableCell className="text-right tabular-nums">
              {formatCellValue(row.latest, row.precision, row.unit)}
            </TableCell>
            <TableCell className="text-right tabular-nums">
              {formatCellValue(row.average, row.precision, row.unit)}
            </TableCell>
            <TableCell className="text-right tabular-nums">
              {row.minimum ? (
                <div className="flex flex-col items-end">
                  <span>{formatCellValue(row.minimum.value, row.precision, row.unit)}</span>
                  <span className="text-[11px] text-muted-foreground">
                    {formatCompactTime(row.minimum.at, timezone)}
                  </span>
                </div>
              ) : (
                '—'
              )}
            </TableCell>
            <TableCell className="text-right tabular-nums">
              {row.maximum ? (
                <div className="flex flex-col items-end">
                  <span>{formatCellValue(row.maximum.value, row.precision, row.unit)}</span>
                  <span className="text-[11px] text-muted-foreground">
                    {formatCompactTime(row.maximum.at, timezone)}
                  </span>
                </div>
              ) : (
                '—'
              )}
            </TableCell>
            <TableCell className="text-right tabular-nums">
              <span title={`${row.validBuckets} / ${row.totalBuckets} 个时段有有效读数`}>
                {formatDecimal(row.coverageRatio * 100, 0)}%
              </span>
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}
