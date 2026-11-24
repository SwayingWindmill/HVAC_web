import { BrandMark } from "@/components/brand-mark";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { DeltaBadge } from "@/components/ui/delta-badge";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useAssistantPanel } from "@/features/assistant/assistant-panel-store";
import { PERIOD_LABEL, type PeriodCode, type TouCost } from "@/features/tariff/tariff-model";
import { formatDecimal } from "@/lib/operator-format";
import { cn } from "@/lib/utils";
import { explainQuestion, relativeChange } from "../model";
import type { useEnergyComparison } from "../query";

const dash = <span className="text-muted-foreground">—</span>;
const number = (value: number | null | undefined, digits = 1) => (
  value === null || value === undefined ? dash : <span className="tabular-nums">{formatDecimal(value, digits)}</span>
);

export interface ConsumptionDetailTableProps {
  readonly comparison: NonNullable<ReturnType<typeof useEnergyComparison>["data"]>;
  readonly pricing: {
    readonly codes: readonly PeriodCode[];
    readonly byLabel: ReadonlyMap<string, TouCost>;
  } | null;
  readonly previousLabel: string;
  readonly timeZone: string;
  readonly title: string;
  readonly bucketHeader: string;
  readonly crossGapNote: string;
}

/** Period-by-period electricity, cooling, TOU breakdown, and COP table with inline AI explain actions. */
export function ConsumptionDetailTable({
  comparison,
  pricing,
  previousLabel,
  timeZone,
  title,
  bucketHeader,
  crossGapNote,
}: ConsumptionDetailTableProps) {
  const granularity = comparison.current.granularity;
  const explain = useAssistantPanel((state) => state.explain);
  const codes = pricing?.codes ?? [];
  // Newest first: the bucket still running leads.
  const rows = [...comparison.buckets].filter((bucket) => bucket.electricityKWh !== null || bucket.coolingKWh !== null || bucket.complete).reverse();
  return (
    <Table aria-label={title} className="[&_td]:whitespace-nowrap [&_th]:whitespace-nowrap">
      <TableHeader>
        <TableRow className="hover:bg-transparent">
          <TableHead className="pl-5">{bucketHeader}</TableHead>
          <TableHead className="text-right">空调用电</TableHead>
          <TableHead className="text-right">{previousLabel}</TableHead>
          <TableHead className="text-right">变化</TableHead>
          {codes.map((code) => <TableHead key={code} className="text-right">{PERIOD_LABEL[code]}</TableHead>)}
          <TableHead className="text-right">供冷量</TableHead>
          <TableHead className="pr-5 text-right">综合能效</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {granularity !== "month" && comparison.current.crossGapElectricityKWh > 0 ? (
          <TableRow className="bg-muted/25 hover:bg-muted/25">
            <TableCell colSpan={6 + codes.length} className="pl-5 pr-5 py-2 text-xs text-muted-foreground">
              {crossGapNote}
            </TableCell>
          </TableRow>
        ) : null}
        {rows.length === 0 && comparison.current.crossGapElectricityKWh <= 0 ? (
          <TableRow className="hover:bg-transparent">
            <TableCell colSpan={6 + codes.length} className="py-8 text-center text-muted-foreground">所选时段暂无计量数据</TableCell>
          </TableRow>
        ) : rows.map((row) => {
          const change = relativeChange(row.electricityKWh, row.previousElectricityKWh);
          const cost = pricing?.byLabel.get(row.label);
          return (
            <TableRow key={row.label} className="group">
              <TableCell className="pl-5">
                <span className="flex items-center gap-2">
                  <span className="font-medium">{row.label}</span>
                  {!row.complete ? <Badge>进行中</Badge> : null}
                  {row.incomplete || row.missingMeters.length > 0 ? (
                    <Badge variant="warning">
                      {row.missingMeters.length > 0 ? `▲ 不完整，缺 ${row.missingMeters.join("、")}` : "▲ 数据不完整"}
                    </Badge>
                  ) : null}
                  {row.complete && row.electricityKWh !== null ? (
                    <Button
                      variant="ghost"
                      size="sm"
                      className="h-6 gap-1 px-1.5 text-xs text-primary opacity-0 group-hover:opacity-100 focus-visible:opacity-100"
                      aria-label={`分析 ${row.label}`}
                      onClick={() => explain(explainQuestion(row, granularity, timeZone))}
                    >
                      <BrandMark className="size-3 text-primary" />
                      分析原因
                    </Button>
                  ) : null}
                </span>
              </TableCell>
              <TableCell className="text-right font-medium">{number(row.electricityKWh)}</TableCell>
              <TableCell className="text-right text-muted-foreground">{number(row.previousElectricityKWh)}</TableCell>
              <TableCell className="text-right">{change === null ? dash : <DeltaBadge delta={change} invert variant="text" showIcon={false} />}</TableCell>
              {codes.map((code) => {
                const used = cost?.byPeriod.find((usage) => usage.code === code)?.energyKWh;
                return <TableCell key={code} className={cn("text-right", !used && "text-muted-foreground")}>{used ? number(used, 0) : dash}</TableCell>;
              })}
              <TableCell className="text-right">{number(row.coolingKWh)}</TableCell>
              <TableCell className="pr-5 text-right">{row.cop === null ? dash : number(row.cop, 2)}</TableCell>
            </TableRow>
          );
        })}
      </TableBody>
    </Table>
  );
}
